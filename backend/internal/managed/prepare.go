package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/double-cloth-studio/PageHub/backend/internal/adapters"
	"github.com/double-cloth-studio/PageHub/backend/internal/desired"
	"github.com/double-cloth-studio/PageHub/backend/internal/manifests"
	"github.com/double-cloth-studio/PageHub/backend/internal/registry"
)

func (s Service) prepare(ctx context.Context, app desired.App, origin, commit string) (registry.Record, error) {
	digest := desired.Digest([]string{commit, configDigest(app, origin)})
	dir := s.Legacy.Store.Path("apps", app.ID, "releases", digest)
	m := manifests.Manifest{SchemaVersion: 1, ID: app.ID, Name: app.Name, Description: app.Description, IconPath: app.IconPath, Version: commit, Kind: "static", Path: app.Path, Startup: "always", Visibility: "public", RouteMode: "strip_prefix", Health: manifests.Health{Path: "/", IntervalSeconds: 15, TimeoutSeconds: 5}}
	m.Source.StaticDir = "dist"
	m.Backup.Mode = "external"
	a := registry.Record{Manifest: m, Digest: digest, Directory: dir, Project: project(app), UpdatedAt: time.Now().UTC(), Managed: &registry.Managed{Config: app, Origin: origin, Commit: commit, ConfigSHA256: configDigest(app, origin)}}
	if _, err := os.Stat(filepath.Join(dir, "app.yaml")); err == nil {
		// 失败重试也复用已完成的不可变产物，禁止覆盖在线版本。
		var saved registry.Record
		b, err := os.ReadFile(filepath.Join(dir, "managed.json"))
		if err != nil {
			return a, err
		}
		if err = json.Unmarshal(b, &saved); err != nil {
			return a, err
		}
		if saved.Digest != digest || saved.Managed == nil || saved.Managed.ConfigSHA256 != a.Managed.ConfigSHA256 {
			return a, errors.New("已存在的托管产物与计划不一致")
		}
		return saved, s.Legacy.Store.Validate(saved)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0750); err != nil {
		return a, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".staging-")
	if err != nil {
		return a, err
	}
	defer os.RemoveAll(stage)
	source := filepath.Join(stage, "source")
	sparse := app.Source.SparsePaths
	if app.Deployment.Driver == "managed-static" {
		sparse = []string{app.Deployment.Publish.SourceFile}
	}
	if err = checkout(ctx, s.Legacy.Store.Path("sources", app.ID), app.Source.Repository, app.Source.Branch, commit, source, sparse); err != nil {
		return a, err
	}
	if app.Deployment.Driver == "managed-static" {
		a.Manifest.Source.Fallback = app.Deployment.Publish.TargetFile
		data, err := os.ReadFile(filepath.Join(source, app.Deployment.Publish.SourceFile))
		if err != nil {
			return a, err
		}
		sum := sha256.Sum256(data)
		a.Managed.AssetSHA256 = hex.EncodeToString(sum[:])
		if err = registry.AtomicWrite(filepath.Join(stage, "dist", app.Deployment.Publish.TargetFile), data, 0644); err != nil {
			return a, err
		}
		if err = os.RemoveAll(source); err != nil {
			return a, err
		}
	} else {
		a.Manifest.Kind = "compose"
		a.Manifest.Startup = app.Startup
		a.Manifest.Source = manifests.Source{ComposeFile: "pagehub.runtime.json", WebService: app.Deployment.Service, ContainerPort: app.Deployment.UpstreamPort}
		a.Manifest.Health.Path = "/readyz"
		a.Manifest.Lifecycle.Allow = []string{"start", "stop", "restart"}
		a.Adopted = true
		if err = s.prepareCompose(ctx, &a, stage, source); err != nil {
			return a, err
		}
	}
	data, err := json.Marshal(a.Manifest)
	if err != nil {
		return a, err
	}
	if err = registry.AtomicWrite(filepath.Join(stage, "app.yaml"), data, 0644); err != nil {
		return a, err
	}
	if err = registry.WriteJSON(filepath.Join(stage, "managed.json"), a, 0600); err != nil {
		return a, err
	}
	if err = os.Rename(stage, dir); err != nil {
		return a, err
	}
	return a, s.Legacy.Store.Validate(a)
}

func (s Service) prepareCompose(ctx context.Context, a *registry.Record, stage, source string) error {
	app := a.Managed.Config
	env := app.Environment(a.Managed.Origin)
	tag := "pagehub-managed-" + app.ID + ":" + a.Managed.Commit
	env["TRAVEL_IMAGE"] = tag
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&text, "%s=%s\n", key, env[key])
	}
	envFile := filepath.Join(stage, "generated.env")
	if err := registry.AtomicWrite(envFile, []byte(text.String()), 0600); err != nil {
		return err
	}
	args := []string{"compose", "--project-name", a.Project, "--project-directory", source, "--env-file", envFile}
	for _, file := range app.Deployment.ComposeFiles {
		args = append(args, "-f", filepath.Join(source, file))
	}
	out, err := s.Legacy.Adapter.Docker.Run(ctx, source, append(args, "config", "--format", "json")...)
	if err != nil {
		return fmt.Errorf("解析托管 Compose 失败: %w", err)
	}
	var resolved map[string]any
	if err = json.Unmarshal(out, &resolved); err != nil {
		return err
	}
	services, _ := resolved["services"].(map[string]any)
	web, ok := services[app.Deployment.Service].(map[string]any)
	if !ok {
		return errors.New("Compose 缺少已授权 Web 服务")
	}
	// 只构建和运行 app，原项目独立 Caddy 与维护宿主挂载不进入托管配置。
	delete(web, "image")
	web["networks"] = map[string]any{"default": nil}
	web["restart"] = "no"
	if app.Startup == "always" {
		web["restart"] = "unless-stopped"
	}
	volumes, _ := resolved["volumes"].(map[string]any)
	volume, ok := volumes[app.Persistence.Volume].(map[string]any)
	if !ok || volume["name"] != a.Project+"_runtime" {
		return errors.New("runtime 卷名不匹配，拒绝隐式创建替代卷")
	}
	config := map[string]any{"services": map[string]any{app.Deployment.Service: web}, "volumes": map[string]any{"runtime": volume}, "networks": map[string]any{"default": map[string]any{"name": a.Project + "_default"}}}
	if err = checkDiaryCompose(config, source, a.Project); err != nil {
		return err
	}
	buildFile := filepath.Join(stage, "build.json")
	if err = registry.WriteJSON(buildFile, config, 0600); err != nil {
		return err
	}
	// 给本次构建设置 SHA 标签，随后只使用真实镜像 ID。
	web["image"] = tag
	if err = registry.WriteJSON(buildFile, config, 0600); err != nil {
		return err
	}
	if _, err = s.Legacy.Adapter.Docker.Run(ctx, source, "compose", "--project-name", a.Project, "-f", buildFile, "build", app.Deployment.Service); err != nil {
		return fmt.Errorf("构建托管镜像失败: %w", err)
	}
	out, err = s.Legacy.Adapter.Docker.Run(ctx, "", "image", "inspect", tag, "--format", "{{.Id}}")
	if err != nil {
		return err
	}
	a.Managed.Image = strings.TrimSpace(string(out))
	if !manifests.ValidImage(a.Managed.Image, false) {
		return errors.New("构建未返回固定镜像 ID")
	}
	delete(web, "build")
	web["image"] = a.Managed.Image
	if err = checkDiaryCompose(config, source, a.Project); err != nil {
		return err
	}
	web["networks"] = map[string]any{"default": nil, "pagehub-ingress": map[string]any{"aliases": []string{"pagehub-" + app.ID + "-web", app.Deployment.IngressAlias}}}
	config["networks"].(map[string]any)["pagehub-ingress"] = map[string]any{"external": true, "name": "pagehub-ingress"}
	a.Managed.RuntimeSHA256 = desired.Digest(config)
	if err = registry.WriteJSON(filepath.Join(stage, "pagehub.runtime.json"), config, 0600); err != nil {
		return err
	}
	if err = os.Remove(buildFile); err != nil {
		return err
	}
	return nil
}

func checkDiaryCompose(config map[string]any, source, project string) error {
	services := config["services"].(map[string]any)
	for _, value := range services {
		web := value.(map[string]any)
		// logging 仅允许 Docker 的本地 json-file 驱动及大小限制。
		if logging, exists := web["logging"]; exists {
			b, _ := json.Marshal(logging)
			var spec struct {
				Driver  string            `json:"driver"`
				Options map[string]string `json:"options"`
			}
			if json.Unmarshal(b, &spec) != nil || spec.Driver != "json-file" {
				return errors.New("日志驱动未授权")
			}
			for key := range spec.Options {
				if key != "max-size" && key != "max-file" {
					return errors.New("日志选项未授权")
				}
			}
			delete(web, "logging")
			defer func() { web["logging"] = logging }()
		}
	}
	if err := adapters.CheckRaw(config); err != nil {
		return err
	}
	if err := adapters.CheckResolved(config, source, project, false); err != nil {
		return err
	}
	web := services["app"].(map[string]any)
	mounts, _ := web["volumes"].([]any)
	if len(mounts) != 1 {
		return errors.New("日记只允许 runtime 数据卷")
	}
	mount, _ := mounts[0].(map[string]any)
	if mount["type"] != "volume" || mount["source"] != "runtime" || mount["target"] != "/var/lib/travel-diary" {
		return errors.New("日记数据卷挂载不匹配")
	}
	return nil
}
