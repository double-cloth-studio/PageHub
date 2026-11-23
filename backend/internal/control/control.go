package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/double-cloth-studio/PageHub/backend/internal/adapters"
	"github.com/double-cloth-studio/PageHub/backend/internal/auth"
	"github.com/double-cloth-studio/PageHub/backend/internal/gateway"
	"github.com/double-cloth-studio/PageHub/backend/internal/lifecycle"
	"github.com/double-cloth-studio/PageHub/backend/internal/managed"
	"github.com/double-cloth-studio/PageHub/backend/internal/manifests"
	"github.com/double-cloth-studio/PageHub/backend/internal/presentation"
	"github.com/double-cloth-studio/PageHub/backend/internal/registry"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Fail(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"error": message})
}

func UnixListener(path string, mode os.FileMode) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("Socket 路径被普通文件占用")
		}
		if conn, dialErr := net.DialTimeout("unix", path, time.Second); dialErr == nil {
			conn.Close()
			return nil, errors.New("Socket 已有服务监听")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func RunnerHandler(service lifecycle.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /managed/self/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := (managed.Service{Legacy: service}).SelfResult(r.Context(), r.PathValue("id"))
		if err != nil {
			Fail(w, 409, err.Error())
			return
		}
		JSON(w, 200, result)
	})
	mux.HandleFunc("GET /managed/operations", func(w http.ResponseWriter, r *http.Request) {
		result, err := (managed.Service{Legacy: service}).Operations()
		if err != nil {
			Fail(w, 409, "读取运维记录失败")
			return
		}
		JSON(w, 200, result)
	})
	mux.HandleFunc("GET /managed/plan/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !manifests.IDPattern.MatchString(id) {
			Fail(w, 400, "应用 ID 无效")
			return
		}
		s := managed.Service{Legacy: service}
		if id == "self" {
			plan, err := s.SelfPlan(r.Context())
			if err != nil {
				Fail(w, 409, err.Error())
				return
			}
			JSON(w, 200, plan)
			return
		}
		c, err := s.Config()
		if err != nil {
			Fail(w, 409, err.Error())
			return
		}
		plan, err := s.TargetPlan(r.Context(), c, id)
		if err != nil {
			Fail(w, 409, err.Error())
			return
		}
		JSON(w, 200, plan)
	})
	mux.HandleFunc("GET /status/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, err := service.Store.Get(r.PathValue("id"))
		if err != nil {
			Fail(w, 404, "应用未登记")
			return
		}
		if err = service.Store.Validate(a); err != nil {
			Fail(w, 409, "登记版本校验失败")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		JSON(w, 200, service.Adapter.Inspect(ctx, a))
	})
	mux.HandleFunc("POST /execute", func(w http.ResponseWriter, r *http.Request) {
		var p adapters.ExecutionPlan
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil {
			Fail(w, 400, "执行计划无效")
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			Fail(w, 400, "执行计划格式无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		if p.Action == "backup" || p.Action == "restore" || p.Action == "update" || p.Action == "update-self" || p.Action == "rollback" || p.Action == "uninstall" {
			result, err := (managed.Service{Legacy: service}).Execute(ctx, p)
			if err != nil {
				Fail(w, 409, err.Error())
				return
			}
			JSON(w, 200, result)
			return
		}
		if err := service.Execute(ctx, p); err != nil {
			Fail(w, 409, err.Error())
			return
		}
		JSON(w, 200, map[string]string{"state": "succeeded"})
	})
	return mux
}

type RunnerClient struct{ Socket string }

func (c RunnerClient) Call(ctx context.Context, method, path string, body io.Reader, out any) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	}}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, method, "http://runner"+path, body)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: transport, Timeout: 31 * time.Minute}).Do(req)
	if err != nil {
		return errors.New("Runner 无法连接")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("Runner 拒绝或执行失败，请检查登记状态、任务锁及本机日志")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

type Job struct {
	Changes   []managed.Change  `json:"changes,omitempty"`
	Commit    string            `json:"commit,omitempty"`
	Result    map[string]string `json:"result,omitempty"`
	ID        string            `json:"id"`
	AppID     string            `json:"app_id"`
	Action    string            `json:"action"`
	State     string            `json:"state"`
	Error     string            `json:"error,omitempty"`
	Actor     string            `json:"actor,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}
type Manager struct {
	Store     registry.Store
	Runner    RunnerClient
	TokenFile string
	Frontend  string
	Sessions  auth.Store
	mu        sync.Mutex
	active    map[string]bool
}

func (m *Manager) Public() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_pagehub/api/portal", func(w http.ResponseWriter, r *http.Request) {
		settings, err := presentation.Public(m.Store)
		if err != nil {
			Fail(w, 503, "展示配置暂不可用")
			return
		}
		JSON(w, 200, settings)
	})
	mux.HandleFunc("GET /_pagehub/api/health", func(w http.ResponseWriter, r *http.Request) { JSON(w, 200, map[string]string{"state": "healthy"}) })
	mux.HandleFunc("GET /_pagehub/api/apps", func(w http.ResponseWriter, r *http.Request) {
		rgy, err := m.Store.Load()
		if err != nil {
			Fail(w, 503, "应用清单暂不可用")
			return
		}
		JSON(w, 200, gateway.CatalogWithReadiness(rgy, m.Store))
	})
	mux.HandleFunc("GET /_pagehub/api/apps/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a, err := m.Store.Get(id)
		if err != nil || a.Manifest.Visibility != "public" {
			Fail(w, 404, "应用不存在")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		status := adapters.Status{ID: id, State: "unknown"}
		_ = m.Runner.Call(ctx, "GET", "/status/"+id, nil, &status)
		status.Message = ""
		JSON(w, 200, status)
	})
	return mux
}

func (m *Manager) authorized(w http.ResponseWriter, r *http.Request) bool {
	if m.Sessions.Authorized(r) {
		return true
	}
	b, err := os.ReadFile(m.TokenFile)
	if err != nil {
		Fail(w, 503, "管理凭据不可用")
		return false
	}
	value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	provided := sha256.Sum256([]byte(value))
	expected := sha256.Sum256([]byte(strings.TrimSpace(string(b))))
	if value == "" || subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		Fail(w, 401, "需要管理员凭据")
		return false
	}
	if !auth.SafeOrigin(r) {
		Fail(w, 403, "请求来源不允许")
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		Fail(w, 403, "拒绝跨站管理请求")
		return false
	}
	return true
}

func (m *Manager) Admin() http.Handler {
	m.active = map[string]bool{}
	// 将进程退出时未完成的任务标为失败，避免重启后永远显示 running。
	files, _ := filepath.Glob(m.Store.Path("state", "manager", "jobs", "*.json"))
	for _, file := range files {
		var job Job
		if b, e := os.ReadFile(file); e == nil && json.Unmarshal(b, &job) == nil && (job.State == "running" || job.State == "queued") {
			if job.Action == "update-self" && job.Result["request_id"] != "" {
				continue
			}
			job.State = "error"
			job.Error = "Manager 重启中断任务，请检查实际容器状态"
			job.UpdatedAt = time.Now().UTC()
			_ = registry.WriteJSON(file, job, 0600)
		}
	}
	mux := http.NewServeMux()
	m.portalRoutes(mux)
	mux.HandleFunc("GET /admin/api/apps/{id}/plan", func(w http.ResponseWriter, r *http.Request) {
		if !m.authorized(w, r) {
			return
		}
		id := r.PathValue("id")
		if !manifests.IDPattern.MatchString(id) {
			Fail(w, 400, "应用 ID 无效")
			return
		}
		var plan managed.Plan
		if err := m.Runner.Call(r.Context(), "GET", "/managed/plan/"+id, nil, &plan); err != nil {
			Fail(w, 409, err.Error())
			return
		}
		JSON(w, 200, plan)
	})
	mux.Handle("/_pagehub/api/", m.Public())
	mux.HandleFunc("GET /admin/api/apps", func(w http.ResponseWriter, r *http.Request) {
		if !m.authorized(w, r) {
			return
		}
		rgy, err := m.Store.Load()
		if err != nil {
			Fail(w, 503, "读取失败")
			return
		}
		apps := []gateway.PublicApp{}
		for _, a := range rgy.Apps {
			entry := gateway.PublicApp{ID: a.Manifest.ID, Name: a.Manifest.Name, Description: a.Manifest.Description, IconPath: a.Manifest.IconPath, Path: a.Manifest.Path, Kind: a.Manifest.Kind, Startup: a.Manifest.Startup, State: "unknown", Actions: a.Manifest.Lifecycle.Allow, OfflineReady: a.Manifest.Visibility == "public" && presentation.OfflineReady(m.Store, a)}
			if a.Managed != nil {
				entry.Actions = []string{"start", "stop", "restart"}
				entry.Managed = true
				entry.Commit = a.Managed.Commit
				entry.Image = a.Managed.Image
				entry.Driver = a.Managed.Config.Deployment.Driver
			}
			entry.DisplayReady = presentation.DisplayReady(m.Store, a)
			apps = append(apps, entry)
		}
		JSON(w, 200, apps)
	})
	mux.HandleFunc("GET /admin/api/apps/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		if !m.authorized(w, r) {
			return
		}
		id := r.PathValue("id")
		if _, err := m.Store.Get(id); err != nil {
			Fail(w, 404, "应用不存在")
			return
		}
		status := adapters.Status{ID: id, State: "unknown"}
		if err := m.Runner.Call(r.Context(), "GET", "/status/"+id, nil, &status); err != nil {
			status.Message = err.Error()
		}
		JSON(w, 200, status)
	})
	mux.HandleFunc("POST /admin/api/apps/{id}/{action}", func(w http.ResponseWriter, r *http.Request) { m.startJob(w, r) })
	mux.HandleFunc("GET /admin/api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !m.authorized(w, r) {
			return
		}
		id := r.PathValue("id")
		if len(id) != 32 {
			Fail(w, 404, "任务不存在")
			return
		}
		if _, err := hex.DecodeString(id); err != nil {
			Fail(w, 404, "任务不存在")
			return
		}
		b, err := os.ReadFile(m.Store.Path("state", "manager", "jobs", id+".json"))
		if err != nil {
			Fail(w, 404, "任务不存在")
			return
		}
		var job Job
		if json.Unmarshal(b, &job) != nil {
			Fail(w, 500, "任务记录损坏")
			return
		}
		m.refreshPlatformJob(r.Context(), &job)
		JSON(w, 200, job)
	})
	mux.HandleFunc("GET /admin/api/audit", func(w http.ResponseWriter, r *http.Request) {
		if !m.authorized(w, r) {
			return
		}
		files, _ := filepath.Glob(m.Store.Path("state", "manager", "jobs", "*.json"))
		auditFiles, _ := filepath.Glob(m.Store.Path("state", "manager", "audit", "*.json"))
		files = append(files, auditFiles...)
		jobs := []Job{}
		for _, file := range files {
			var job Job
			if b, e := os.ReadFile(file); e == nil && json.Unmarshal(b, &job) == nil {
				m.refreshPlatformJob(r.Context(), &job)
				jobs = append(jobs, job)
			}
		}
		var operations []Job
		if m.Runner.Call(r.Context(), "GET", "/managed/operations", nil, &operations) == nil {
			jobs = append(jobs, operations...)
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
		jobs = jobs[:min(len(jobs), 200)]
		JSON(w, 200, jobs)
	})
	fs := http.FileServer(http.Dir(m.Frontend))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		if r.URL.Path == "/admin" || r.URL.Path == "/admin/" {
			r.URL.Path = "/"
		}
		fs.ServeHTTP(w, r)
	})
	return mux
}

func (m *Manager) startJob(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(w, r) {
		return
	}
	id, action := r.PathValue("id"), r.PathValue("action")
	if !manifests.IDPattern.MatchString(id) {
		Fail(w, 404, "应用不存在")
		return
	}
	platformUpdate := id == "self" && action == "update-self"
	var a registry.Record
	var err error
	if !platformUpdate {
		a, err = m.Store.Get(id)
		if err != nil {
			Fail(w, 404, "应用不存在")
			return
		}
	}
	maintenance := platformUpdate || action == "backup" || action == "restore" || action == "update" || action == "rollback" || action == "uninstall"
	if action != "start" && action != "stop" && action != "restart" && !maintenance {
		Fail(w, 400, "操作无效")
		return
	}
	var approval struct {
		Backup       string `json:"backup"`
		Password     string `json:"password"`
		ConfirmID    string `json:"confirm_id"`
		ApprovedPlan string `json:"approved_plan_sha256"`
	}
	if maintenance {
		if !platformUpdate && a.Managed == nil {
			Fail(w, 403, "应用尚未托管")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&approval) != nil || approval.ConfirmID != id {
			Fail(w, 400, "需要确认目标应用")
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			Fail(w, 400, "审批请求无效")
			return
		}
		if err := m.Sessions.VerifyPassword(m.Store.Path("secrets", "admin-password.json"), approval.Password, r.RemoteAddr); err != nil {
			Fail(w, 401, "需要重新验证管理员密码")
			return
		}
	}
	if !maintenance && !a.Allows(action) {
		Fail(w, 403, "应用未授权该操作")
		return
	}
	m.mu.Lock()
	if m.active[id] {
		m.mu.Unlock()
		Fail(w, 409, "该应用有未完成任务")
		return
	}
	m.active[id] = true
	m.mu.Unlock()
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		m.release(id)
		Fail(w, 500, "生成任务失败")
		return
	}
	job := Job{ID: hex.EncodeToString(random), AppID: id, Action: action, State: "queued", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	file := m.Store.Path("state", "manager", "jobs", job.ID+".json")
	if err = registry.WriteJSON(file, job, 0600); err != nil {
		m.release(id)
		Fail(w, 500, "持久化任务失败")
		return
	}
	p := adapters.ExecutionPlan{ID: id, Action: action, Fingerprint: registry.Fingerprint(a)}
	p.ApprovedPlan = approval.ApprovedPlan
	p.Backup = approval.Backup
	go func() {
		defer m.release(id)
		job.State = "running"
		job.UpdatedAt = time.Now().UTC()
		if registry.WriteJSON(file, job, 0600) != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 31*time.Minute)
		defer cancel()
		payload, _ := json.Marshal(p)
		var result map[string]string
		err := m.Runner.Call(ctx, "POST", "/execute", strings.NewReader(string(payload)), &result)
		job.State = "succeeded"
		job.Result = result
		if result["request_id"] != "" {
			job.State = "running"
		}
		if err != nil {
			job.State = "error"
			job.Error = err.Error()
		}
		job.UpdatedAt = time.Now().UTC()
		_ = registry.WriteJSON(file, job, 0600)
	}()
	JSON(w, 202, job)
}
func (m *Manager) release(id string) { m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }

func (m *Manager) refreshPlatformJob(ctx context.Context, job *Job) {
	if job.Action != "update-self" || job.Result["request_id"] == "" || (job.State != "running" && job.State != "queued") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result map[string]string
	if m.Runner.Call(ctx, "GET", "/managed/self/"+job.Result["request_id"], nil, &result) != nil {
		return
	}
	state := result["state"]
	if state != "succeeded" && state != "error" {
		return
	}
	job.State = state
	job.Error = result["error"]
	job.UpdatedAt = time.Now().UTC()
	_ = registry.WriteJSON(m.Store.Path("state", "manager", "jobs", job.ID+".json"), job, 0600)
}
