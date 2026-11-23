package desired

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/double-cloth-studio/PageHub/backend/internal/manifests"
	"gopkg.in/yaml.v3"
)

type Config struct {
	SchemaVersion int      `yaml:"schema_version" json:"schema_version"`
	Platform      Platform `yaml:"platform" json:"platform"`
	Update        Update   `yaml:"update" json:"update"`
	Apps          []App    `yaml:"apps" json:"apps"`
}

type Platform struct {
	Name         string   `yaml:"name" json:"name"`
	Repository   string   `yaml:"repository" json:"repository"`
	Branch       string   `yaml:"branch" json:"branch"`
	Root         string   `yaml:"root" json:"root"`
	SourceDir    string   `yaml:"source_dir" json:"source_dir"`
	PublicOrigin string   `yaml:"public_origin" json:"public_origin"`
	Gateway      Endpoint `yaml:"gateway" json:"gateway"`
	Admin        Endpoint `yaml:"admin" json:"admin"`
	Tunnel       Tunnel   `yaml:"tunnel" json:"tunnel"`
}

type Endpoint struct {
	Bind string `yaml:"bind" json:"bind"`
	Port int    `yaml:"port" json:"port"`
}

type Tunnel struct {
	Provider    string `yaml:"provider" json:"provider"`
	Mode        string `yaml:"mode" json:"mode"`
	LocalTarget string `yaml:"local_target" json:"local_target"`
}

type Update struct {
	DefaultBranch      string `yaml:"default_branch" json:"default_branch"`
	Mode               string `yaml:"mode" json:"mode"`
	VerifyHealth       bool   `yaml:"verify_health" json:"verify_health"`
	RetainReleases     int    `yaml:"retain_releases" json:"retain_releases"`
	BackupBeforeUpdate bool   `yaml:"backup_before_update" json:"backup_before_update"`
}

type App struct {
	ID          string            `yaml:"id" json:"id"`
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	IconPath    string            `yaml:"icon_path,omitempty" json:"icon_path,omitempty"`
	Enabled     bool              `yaml:"enabled" json:"enabled"`
	Path        string            `yaml:"path" json:"path"`
	Source      Source            `yaml:"source" json:"source"`
	Deployment  Deployment        `yaml:"deployment" json:"deployment"`
	Settings    map[string]string `yaml:"settings" json:"settings,omitempty"`
	Persistence Persistence       `yaml:"persistence" json:"persistence"`
	Startup     string            `yaml:"startup" json:"startup"`
}

type Source struct {
	Type        string   `yaml:"type" json:"type"`
	Repository  string   `yaml:"repository" json:"repository"`
	Branch      string   `yaml:"branch" json:"branch"`
	SparsePaths []string `yaml:"sparse_paths" json:"sparse_paths,omitempty"`
}

type Deployment struct {
	Driver       string   `yaml:"driver" json:"driver"`
	Project      string   `yaml:"project" json:"project,omitempty"`
	ComposeFiles []string `yaml:"compose_files" json:"compose_files,omitempty"`
	Service      string   `yaml:"service" json:"service,omitempty"`
	UpstreamPort int      `yaml:"upstream_port" json:"upstream_port,omitempty"`
	IngressAlias string   `yaml:"ingress_alias" json:"ingress_alias,omitempty"`
	RouteMode    string   `yaml:"route_mode" json:"route_mode"`
	Publish      Publish  `yaml:"publish" json:"publish,omitempty"`
}

type Publish struct {
	SourceFile string `yaml:"source_file" json:"source_file,omitempty"`
	TargetFile string `yaml:"target_file" json:"target_file,omitempty"`
}

type Persistence struct {
	Kind              string `yaml:"kind" json:"kind"`
	Volume            string `yaml:"volume" json:"volume,omitempty"`
	BackupDriver      string `yaml:"backup_driver" json:"backup_driver,omitempty"`
	RetainOnUninstall bool   `yaml:"retain_on_uninstall" json:"retain_on_uninstall"`
}

const PlatformRepository = "https://github.com/double-cloth-studio/PageHub.git"
const DiaryRepository = "https://github.com/double-cloth-studio/travel-diary.git"
const StaticRepository = "https://github.com/double-cloth-studio/Whalgebra.git"

var branchPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,127}$`)
var rootPattern = regexp.MustCompile(`^/[a-zA-Z0-9/_-]+$`)
var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func Load(file string) (Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return Config{}, err
	}
	return Decode(b)
}

func Decode(data []byte) (Config, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return Config{}, errors.New("配置 YAML 格式无效（内容已隐藏）")
	}
	if len(document.Content) != 1 {
		return Config{}, errors.New("配置缺少根对象")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "apps" {
			continue
		}
		for _, app := range root.Content[i+1].Content {
			enabled := false
			for j := 0; j+1 < len(app.Content); j += 2 {
				if app.Content[j].Value == "enabled" {
					enabled = app.Content[j+1].Tag == "!!bool"
				}
				if app.Content[j].Value == "settings" {
					for k := 1; k < len(app.Content[j+1].Content); k += 2 {
						if app.Content[j+1].Content[k].Tag != "!!str" {
							return Config{}, errors.New("settings 的值须显式使用字符串")
						}
					}
				}
			}
			if !enabled {
				return Config{}, errors.New("每个应用须显式声明布尔 enabled")
			}
		}
	}
	c := Config{Update: Update{DefaultBranch: "main", Mode: "manual", VerifyHealth: true, RetainReleases: 3, BackupBeforeUpdate: true}}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, errors.New("pagehub.yaml 解析失败，请核对字段和类型（配置内容已隐藏）")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("配置只允许一个 YAML 文档")
	}
	if c.Platform.Branch == "" {
		c.Platform.Branch = c.Update.DefaultBranch
	}
	for i := range c.Apps {
		if c.Apps[i].Source.Branch == "" {
			c.Apps[i].Source.Branch = c.Update.DefaultBranch
		}
		if c.Apps[i].Startup == "" {
			c.Apps[i].Startup = "always"
		}
	}
	return c, c.Validate()
}

func validBranch(branch string) bool {
	for _, part := range strings.Split(branch, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return branchPattern.MatchString(branch) && !strings.Contains(branch, "..") && !strings.Contains(branch, "//") && !strings.HasSuffix(branch, "/") && !strings.HasSuffix(branch, ".lock")
}

func (c Config) Validate() error {
	p := c.Platform
	if c.SchemaVersion != 1 || strings.TrimSpace(p.Name) == "" {
		return errors.New("schema_version 或平台名称无效")
	}
	if p.Repository != PlatformRepository || !validBranch(p.Branch) || !validBranch(c.Update.DefaultBranch) {
		return errors.New("平台仓库须为审核过的 PageHub HTTPS 仓库，分支须合法")
	}
	if !rootPattern.MatchString(p.Root) || path.Clean(p.Root) != p.Root || p.Root == "/" || p.SourceDir != p.Root+"/sources/pagehub" {
		return errors.New("平台 root 或 source_dir 无效")
	}
	u, err := url.Parse(p.PublicOrigin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !regexp.MustCompile(`^https?://[a-zA-Z0-9.\[\]:-]+$`).MatchString(p.PublicOrigin) {
		return errors.New("public_origin 必须为不含路径和凭据的完整 HTTP(S) Origin")
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil || u.Port() == "0" {
			return errors.New("public_origin 端口无效")
		}
	}
	for _, endpoint := range []Endpoint{p.Gateway, p.Admin} {
		if endpoint.Bind != "127.0.0.1" || endpoint.Port < 1024 || endpoint.Port > 65535 {
			return errors.New("网关和管理端须绑定 127.0.0.1，端口范围为 1024..65535")
		}
	}
	if p.Gateway.Port == p.Admin.Port || p.Tunnel.Mode != "external" || p.Tunnel.Provider != "sakurafrp" || p.Tunnel.LocalTarget != fmt.Sprintf("127.0.0.1:%d", p.Gateway.Port) {
		return errors.New("端口冲突或外部隧道目标不匹配")
	}
	if c.Update.Mode != "manual" || !c.Update.VerifyHealth || !c.Update.BackupBeforeUpdate || c.Update.RetainReleases < 2 || c.Update.RetainReleases > 100 {
		return errors.New("更新必须为 manual、验证健康且更新前备份，保留版本数为 2..100")
	}
	projects := map[string]bool{}
	aliases := map[string]bool{}
	for i, a := range c.Apps {
		if err := a.Validate(p.PublicOrigin); err != nil {
			return fmt.Errorf("应用 %d: %w", i+1, err)
		}
		for _, b := range c.Apps[:i] {
			if manifests.Conflict(manifests.Manifest{ID: a.ID, Path: a.Path}, manifests.Manifest{ID: b.ID, Path: b.Path}) {
				return errors.New("应用 ID 或路径重复、重叠")
			}
		}
		if a.Deployment.Driver == "managed-compose" {
			if projects[a.Deployment.Project] || aliases[a.Deployment.IngressAlias] {
				return errors.New("Compose 项目名或网络别名重复")
			}
			projects[a.Deployment.Project], aliases[a.Deployment.IngressAlias] = true, true
		}
	}
	return nil
}

func (a App) Validate(origin string) error {
	m := manifests.Manifest{SchemaVersion: 1, ID: a.ID, Name: a.Name, Description: a.Description, IconPath: a.IconPath, Version: "config", Kind: "static", Path: a.Path, Startup: "always", Visibility: "public", RouteMode: a.Deployment.RouteMode, Source: manifests.Source{StaticDir: "dist"}, Health: manifests.Health{Path: "/", TimeoutSeconds: 5, IntervalSeconds: 15}}
	m.Backup.Mode = "none"
	if err := m.Validate(false); err != nil {
		return err
	}
	if a.ID == "self" || a.ID == "pagehub" || a.Source.Type != "git" || !validBranch(a.Source.Branch) || (a.Startup != "always" && a.Startup != "manual") {
		return errors.New("应用 ID、Git 来源、分支或 startup 无效")
	}
	for _, file := range a.Source.SparsePaths {
		if !manifests.RelativeFile(file) {
			return errors.New("sparse_paths 必须为仓库内路径")
		}
	}
	d := a.Deployment
	switch d.Driver {
	case "managed-static":
		if a.Startup != "always" {
			return errors.New("静态应用 startup 须为 always，启停通过公开路由控制")
		}
		if a.Source.Repository != StaticRepository || !manifests.RelativeFile(d.Publish.SourceFile) || !manifests.RelativeFile(d.Publish.TargetFile) || d.Project != "" || len(d.ComposeFiles) > 0 || d.Service != "" || d.UpstreamPort != 0 || d.IngressAlias != "" || len(a.Settings) > 0 || a.Persistence.Kind != "none" || a.Persistence.Volume != "" || a.Persistence.BackupDriver != "" {
			return errors.New("managed-static 来源或发布字段无效")
		}
		if len(a.Source.SparsePaths) > 0 && (len(a.Source.SparsePaths) != 1 || a.Source.SparsePaths[0] != d.Publish.SourceFile) {
			return errors.New("静态稀疏获取须精确匹配 source_file")
		}
	case "managed-compose":
		if strings.HasPrefix(d.IngressAlias, "pagehub-") {
			return errors.New("ingress_alias 不能占用 pagehub- 平台保留前缀")
		}
		if !strings.HasPrefix(origin, "https://") {
			return errors.New("Travel Diary Portal 运行时要求 HTTPS public_origin")
		}
		if a.Source.Repository != DiaryRepository || !projectPattern.MatchString(d.Project) || strings.HasPrefix(d.Project, "pagehub-") || d.Service != "app" || d.UpstreamPort != 9000 || !manifests.IDPattern.MatchString(d.IngressAlias) || len(a.Source.SparsePaths) > 0 || d.Publish.SourceFile != "" || d.Publish.TargetFile != "" {
			return errors.New("managed-compose 只支持受审核的 Travel Diary app/9000 适配器")
		}
		if strings.Join(d.ComposeFiles, ",") != "compose.yaml,compose.build.yaml,compose.portal.yaml" || a.Persistence.Kind != "compose-volume" || a.Persistence.Volume != "runtime" || a.Persistence.BackupDriver != "travel-diary-native" || !a.Persistence.RetainOnUninstall {
			return errors.New("日记必须使用 Portal Compose、稳定 runtime 卷及原生备份并保留数据")
		}
		allowed := map[string]string{"TRAVEL_DEPLOYMENT_MODE": "portal", "TRAVEL_BASE_PATH": strings.TrimSuffix(a.Path, "/"), "TRAVEL_PORTAL_NETWORK": "pagehub-ingress", "TRAVEL_PORTAL_ALIAS": d.IngressAlias}
		for key, value := range a.Settings {
			if fixed, ok := allowed[key]; ok {
				if value != fixed {
					return errors.New("日记 Portal 设置与派生配置冲突")
				}
				continue
			}
			if key == "TRAVEL_MEDIA_PREVIEWS_ENABLED" && (value == "true" || value == "false") {
				continue
			}
			if key == "TRAVEL_WRITE_MODE" && (value == "local" || value == "remote" && strings.HasPrefix(origin, "https://")) {
				continue
			}
			return errors.New("settings 包含未授权字段或值，密码和 Token 必须使用 Secret，远程写入须单独验收")
		}
	default:
		return errors.New("deployment.driver 无效")
	}
	return nil
}

func Digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a App) Environment(origin string) map[string]string {
	u, _ := url.Parse(origin)
	env := map[string]string{"COMPOSE_PROJECT_NAME": a.Deployment.Project, "SITE_DOMAIN": u.Hostname(), "SITE_SCHEME": u.Scheme, "TRAVEL_PUBLIC_ORIGIN": origin, "TRAVEL_DEPLOYMENT_MODE": "portal", "TRAVEL_BASE_PATH": strings.TrimSuffix(a.Path, "/"), "TRAVEL_PORTAL_NETWORK": "pagehub-ingress", "TRAVEL_PORTAL_ALIAS": a.Deployment.IngressAlias, "TRAVEL_WRITE_MODE": "local", "TRAVEL_MEDIA_PREVIEWS_ENABLED": "true"}
	for key, value := range a.Settings {
		env[key] = value
	}
	return env
}
