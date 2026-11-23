package manifests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	SchemaVersion int    `yaml:"schema_version" json:"schema_version"`
	ID            string `yaml:"id" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Description   string `yaml:"description,omitempty" json:"description,omitempty"`
	IconPath      string `yaml:"icon_path,omitempty" json:"icon_path,omitempty"`
	Version       string `yaml:"version" json:"version"`
	Kind          string `yaml:"kind" json:"kind"`
	Path          string `yaml:"path" json:"path"`
	Startup       string `yaml:"startup" json:"startup"`
	Visibility    string `yaml:"visibility" json:"visibility"`
	RouteMode     string `yaml:"route_mode" json:"route_mode"`
	Source        Source `yaml:"source" json:"source"`
	Health        Health `yaml:"health" json:"health"`
	Lifecycle     struct {
		Allow []string `yaml:"allow" json:"allow"`
	} `yaml:"lifecycle" json:"lifecycle"`
	Backup struct {
		Mode string `yaml:"mode" json:"mode"`
	} `yaml:"backup" json:"backup"`
}

type Source struct {
	Upstream         string         `yaml:"upstream,omitempty" json:"upstream,omitempty"`
	ComposeDirectory string         `yaml:"compose_directory,omitempty" json:"compose_directory,omitempty"`
	Project          string         `yaml:"project,omitempty" json:"project,omitempty"`
	ConfigSHA256     string         `yaml:"config_sha256,omitempty" json:"config_sha256,omitempty"`
	Approvals        map[string]any `yaml:"approvals,omitempty" json:"approvals,omitempty"`
	StaticDir        string         `yaml:"static_dir,omitempty" json:"static_dir,omitempty"`
	Fallback         string         `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	Image            string         `yaml:"image,omitempty" json:"image,omitempty"`
	ComposeFile      string         `yaml:"compose_file,omitempty" json:"compose_file,omitempty"`
	WebService       string         `yaml:"web_service,omitempty" json:"web_service,omitempty"`
	ContainerPort    int            `yaml:"container_port,omitempty" json:"container_port,omitempty"`
}

type Health struct {
	Path            string `yaml:"path" json:"path"`
	IntervalSeconds int    `yaml:"interval_seconds" json:"interval_seconds"`
	TimeoutSeconds  int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

var IDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)
var routePattern = regexp.MustCompile(`^/[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_-]+)*/$`)
var versionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
var ImagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:[a-f0-9]{64}$`)
var servicePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
var iconPathPattern = regexp.MustCompile(`^/assets/(?:[a-zA-Z0-9_-]+/)*[a-zA-Z0-9_-]+\.(?:svg|png|webp|ico|jpe?g|avif)$`)

func Decode(data []byte, devImages bool) (Manifest, error) {
	var m Manifest
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil {
		return m, fmt.Errorf("清单解析失败: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return m, errors.New("清单仅允许一个 YAML 文档")
	}
	if m.Health.Path == "" {
		m.Health.Path = "/"
	}
	if m.Health.TimeoutSeconds == 0 {
		m.Health.TimeoutSeconds = 5
	}
	if m.Health.IntervalSeconds == 0 {
		m.Health.IntervalSeconds = 15
	}
	return m, m.Validate(devImages)
}

func RelativeFile(p string) bool {
	return regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`).MatchString(p) && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}

func (m Manifest) Validate(devImages bool) error {
	if m.SchemaVersion != 1 || !IDPattern.MatchString(m.ID) || !versionPattern.MatchString(m.Version) || strings.TrimSpace(m.Name) == "" || utf8.RuneCountInString(m.Name) > 100 || utf8.RuneCountInString(m.Description) > 500 {
		return errors.New("schema_version、id、name、description 或 version 无效")
	}
	if m.IconPath != "" && !iconPathPattern.MatchString(m.IconPath) {
		return errors.New("icon_path 必须是 /assets/ 下的本地图标路径")
	}
	if !routePattern.MatchString(m.Path) || strings.HasPrefix(m.Path, "/_pagehub/") || strings.HasPrefix(m.Path, "/admin/") || m.Path == "/healthz/" {
		return errors.New("应用路径无效或占用保留路径")
	}
	if m.Startup != "always" && m.Startup != "manual" {
		return errors.New("startup 必须为 always 或 manual")
	}
	if m.Visibility != "public" && m.Visibility != "private" {
		return errors.New("visibility 无效")
	}
	if m.RouteMode != "strip_prefix" && m.RouteMode != "preserve_prefix" {
		return errors.New("route_mode 无效")
	}
	u, err := url.Parse(m.Health.Path)
	if err != nil || !strings.HasPrefix(m.Health.Path, "/") || strings.HasPrefix(m.Health.Path, "//") || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(m.Health.Path, "\\\x00\r\n") || (u.Path != "/" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/")) || m.Health.TimeoutSeconds < 1 || m.Health.TimeoutSeconds > 60 || m.Health.IntervalSeconds < 1 || m.Health.IntervalSeconds > 3600 {
		return errors.New("health 配置无效")
	}
	seen := map[string]bool{}
	for _, a := range m.Lifecycle.Allow {
		if (a != "start" && a != "stop" && a != "restart") || seen[a] {
			return errors.New("lifecycle.allow 无效")
		}
		seen[a] = true
	}
	if m.Backup.Mode != "none" && m.Backup.Mode != "volume" && m.Backup.Mode != "external" {
		return errors.New("backup.mode 无效")
	}
	s := m.Source
	if m.Kind != "external" && m.Kind != "external-compose" && (s.Upstream != "" || s.ComposeDirectory != "" || s.Project != "" || s.ConfigSHA256 != "" || len(s.Approvals) > 0) {
		return errors.New("标准包不能声明外部接入字段")
	}
	switch m.Kind {
	case "external", "external-compose":
		u, err := url.Parse(s.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !regexp.MustCompile(`^https?://[a-zA-Z0-9.\[\]:_-]+$`).MatchString(s.Upstream) {
			return errors.New("upstream 必须是网关可达的 HTTP(S) Origin")
		}
		if m.Backup.Mode != "external" || m.Startup != "manual" {
			return errors.New("外部应用要求 manual 和 external 备份")
		}
		if m.Kind == "external" && len(m.Lifecycle.Allow) != 0 {
			return errors.New("外部服务生命周期由原部署工具管理")
		}
		if m.Kind == "external-compose" && (!strings.HasPrefix(s.ComposeDirectory, "/") || !RelativeFile(s.ComposeFile) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`).MatchString(s.Project) || (s.Project == "pagehub-gateway" || s.Project == "pagehub-portal") || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(s.ConfigSHA256)) {
			return errors.New("既有 Compose 需要绝对目录、原项目名和已审核配置摘要")
		}
	case "static":
		if !RelativeFile(s.StaticDir) || (s.Fallback != "" && !RelativeFile(s.Fallback)) || s.Image != "" || s.ComposeFile != "" || s.WebService != "" || s.ContainerPort != 0 || len(m.Lifecycle.Allow) != 0 || m.Startup != "always" || m.RouteMode != "strip_prefix" || m.Backup.Mode == "volume" {
			return errors.New("static 配置无效，静态应用必须 always、strip_prefix 且不提供容器操作")
		}
	case "image", "compose":
		if s.ContainerPort < 1 || s.ContainerPort > 65535 || s.StaticDir != "" || s.Fallback != "" {
			return errors.New("容器端口或 source 配置无效")
		}
		if m.Kind == "image" && (!ValidImage(s.Image, devImages) || s.ComposeFile != "" || s.WebService != "") {
			return errors.New("image 必须固定 sha256 摘要，开发模式须显式启用")
		}
		if m.Kind == "compose" && (!RelativeFile(s.ComposeFile) || !servicePattern.MatchString(s.WebService) || s.Image != "") {
			return errors.New("compose_file 或 web_service 无效")
		}
	default:
		return errors.New("不支持的适配器类型")
	}
	return nil
}

func ValidImage(image string, dev bool) bool {
	return ImagePattern.MatchString(image) || regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image) || (dev && regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./:_-]+$`).MatchString(image))
}

func Conflict(a, b Manifest) bool {
	return a.ID == b.ID || strings.HasPrefix(a.Path, b.Path) || strings.HasPrefix(b.Path, a.Path)
}

func (m Manifest) Allows(action string) bool {
	for _, a := range m.Lifecycle.Allow {
		if a == action {
			return true
		}
	}
	return false
}
