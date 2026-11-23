package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/double-cloth-studio/PageHub/backend/internal/desired"
	"github.com/double-cloth-studio/PageHub/backend/internal/manifests"
	"github.com/double-cloth-studio/PageHub/backend/internal/registry"
)

type adoption struct {
	ID     string `json:"Id"`
	Image  string `json:"Image"`
	Config struct {
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		WorkingDir string            `json:"WorkingDir"`
		User       string            `json:"User"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Privileged     bool           `json:"Privileged"`
		NetworkMode    string         `json:"NetworkMode"`
		PidMode        string         `json:"PidMode"`
		IpcMode        string         `json:"IpcMode"`
		CapAdd         []string       `json:"CapAdd"`
		Devices        []any          `json:"Devices"`
		DeviceRequests []any          `json:"DeviceRequests"`
		PortBindings   map[string]any `json:"PortBindings"`
		RestartPolicy  struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			Aliases []string `json:"Aliases"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

func (s Service) inspectAdoption(ctx context.Context, app desired.App) (*adoption, error) {
	out, err := s.Legacy.Adapter.Docker.Run(ctx, "", "ps", "--all", "--filter", "label=com.docker.compose.project="+project(app), "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) != 1 {
		return nil, errors.New("接管要求原工程只有一个 app 容器，请先独立迁移原网关")
	}
	out, err = s.Legacy.Adapter.Docker.Run(ctx, "", "inspect", ids[0])
	if err != nil {
		return nil, err
	}
	var entries []adoption
	if err = json.Unmarshal(out, &entries); err != nil || len(entries) != 1 {
		return nil, errors.New("无法核对原容器")
	}
	a := entries[0]
	if !manifests.ValidImage(a.Image, false) || a.Config.Labels["com.docker.compose.project"] != project(app) || a.Config.Labels["com.docker.compose.service"] != "app" {
		return nil, errors.New("原项目或服务标签不匹配")
	}
	h := a.HostConfig
	if h.Privileged || h.NetworkMode == "host" || h.PidMode != "" || h.IpcMode == "host" || len(h.PortBindings) > 0 || len(h.CapAdd) > 0 || len(h.Devices) > 0 || len(h.DeviceRequests) > 0 {
		return nil, errors.New("现有容器权限超出接管白名单")
	}
	if len(a.Mounts) != 1 || a.Mounts[0].Type != "volume" || a.Mounts[0].Name != project(app)+"_runtime" || a.Mounts[0].Destination != "/var/lib/travel-diary" || !a.Mounts[0].RW {
		return nil, errors.New("原 runtime 卷名或挂载不匹配，禁止创建空卷替代")
	}
	return &a, nil
}

func (s Service) captureAdoption(app desired.App, origin string, existing adoption) (registry.Record, error) {
	digest := desired.Digest(existing)
	dir := s.Legacy.Store.Path("apps", app.ID, "releases", digest)
	m := manifests.Manifest{SchemaVersion: 1, ID: app.ID, Name: app.Name, Description: app.Description, IconPath: app.IconPath, Version: "adopted", Kind: "compose", Path: app.Path, Startup: app.Startup, Visibility: "public", RouteMode: "strip_prefix", Source: manifests.Source{ComposeFile: "pagehub.runtime.json", WebService: "app", ContainerPort: 9000}, Health: manifests.Health{Path: "/readyz", TimeoutSeconds: 5, IntervalSeconds: 15}}
	m.Backup.Mode = "external"
	m.Lifecycle.Allow = []string{"start", "stop", "restart"}
	networks := map[string]any{}
	serviceNetworks := map[string]any{}
	for name, network := range existing.NetworkSettings.Networks {
		networks[name] = map[string]any{"external": true, "name": name}
		serviceNetworks[name] = map[string]any{"aliases": network.Aliases}
	}
	web := map[string]any{"image": existing.Image, "environment": existing.Config.Env, "command": existing.Config.Cmd, "entrypoint": existing.Config.Entrypoint, "working_dir": existing.Config.WorkingDir, "user": existing.Config.User, "restart": existing.HostConfig.RestartPolicy.Name, "volumes": []any{map[string]any{"type": "volume", "source": "runtime", "target": "/var/lib/travel-diary"}}, "networks": serviceNetworks}
	runtime := map[string]any{"services": map[string]any{"app": web}, "networks": networks, "volumes": map[string]any{"runtime": map[string]any{"external": true, "name": project(app) + "_runtime"}}}
	a := registry.Record{Manifest: m, Digest: digest, Directory: dir, Project: project(app), Adopted: true, UpdatedAt: time.Now().UTC(), Managed: &registry.Managed{Config: app, Origin: origin, SourceUnknown: true, Image: existing.Image, ConfigSHA256: desired.Digest(existing), RuntimeSHA256: desired.Digest(runtime)}}
	if _, err := os.Stat(dir); err == nil {
		return a, s.Legacy.Store.Validate(a)
	}
	if err := registry.WriteJSON(filepath.Join(dir, "app.yaml"), m, 0644); err != nil {
		return a, err
	}
	if err := registry.WriteJSON(filepath.Join(dir, "pagehub.runtime.json"), runtime, 0600); err != nil {
		return a, err
	}
	if err := registry.WriteJSON(filepath.Join(dir, "managed.json"), a, 0600); err != nil {
		return a, err
	}
	return a, s.Legacy.Store.Validate(a)
}
