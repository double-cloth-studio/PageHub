package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/double-cloth-studio/PageHub/backend/internal/docker"
	"github.com/double-cloth-studio/PageHub/backend/internal/presentation"
	"github.com/double-cloth-studio/PageHub/backend/internal/registry"
)

type Gateway struct {
	Store  registry.Store
	Docker docker.Client
	Image  string
}

type PublicApp struct {
	DisplayReady bool     `json:"displayReady"`
	Managed      bool     `json:"managed,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Image        string   `json:"image,omitempty"`
	Driver       string   `json:"driver,omitempty"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	IconPath     string   `json:"iconPath,omitempty"`
	Path         string   `json:"path"`
	Kind         string   `json:"kind"`
	Startup      string   `json:"startup"`
	State        string   `json:"state"`
	Actions      []string `json:"actions"`
	OfflineReady bool     `json:"offlineReady"`
}

func Catalog(r registry.Registry) []PublicApp {
	apps := []PublicApp{}
	for _, a := range r.Apps {
		m := a.Manifest
		if m.Visibility != "public" || a.Disabled {
			continue
		}
		apps = append(apps, PublicApp{ID: m.ID, Name: m.Name, Description: m.Description, IconPath: m.IconPath, Path: m.Path, Kind: m.Kind, Startup: m.Startup, State: "unknown", Actions: m.Lifecycle.Allow})
	}
	return apps
}

func CatalogWithReadiness(r registry.Registry, store registry.Store) []PublicApp {
	apps := Catalog(r)
	byID := map[string]registry.Record{}
	for _, record := range r.Apps {
		byID[record.Manifest.ID] = record
	}
	for i := range apps {
		apps[i].OfflineReady = presentation.OfflineReady(store, byID[apps[i].ID])
		apps[i].DisplayReady = presentation.DisplayReady(store, byID[apps[i].ID])
	}
	return apps
}

func Render(r registry.Registry) string {
	apps := append([]registry.Record{}, r.Apps...)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Manifest.Path < apps[j].Manifest.Path })
	var s strings.Builder
	s.WriteString("{\n auto_https off\n}\n:80 {\n route {\n respond /healthz 200\n @admin path /admin /admin/*\n respond @admin 403\n handle /_pagehub/catalog.json {\n root * /etc/caddy/public\n rewrite * /catalog.json\n header Cache-Control no-store\n file_server\n }\n handle /_pagehub/social/* {\n reverse_proxy unix//run/pagehub-social/public.sock {\n header_up X-PageHub-Client {remote_host}\n flush_interval -1\n }\n }\n handle /_pagehub/api/* {\n reverse_proxy unix//run/pagehub-public/manager.sock\n }\n")
	for _, a := range apps {
		m := a.Manifest
		fmt.Fprintf(&s, " redir %s %s 308\n", strings.TrimSuffix(m.Path, "/"), m.Path)
		directive := "handle_path"
		if m.RouteMode == "preserve_prefix" {
			directive = "handle"
		}
		fmt.Fprintf(&s, " %s %s* {\n", directive, m.Path)
		if a.Disabled {
			s.WriteString(" respond \"Application stopped\" 503\n")
		} else if m.Visibility == "private" {
			s.WriteString(" respond \"Private application\" 403\n")
		} else if m.Kind == "static" {
			if a.StaticRestore != "" {
				fmt.Fprintf(&s, " root * /apps/%s/restores/%s/%s\n", m.ID, a.StaticRestore, m.Source.StaticDir)
			} else {
				fmt.Fprintf(&s, " root * /apps/%s/releases/%s/%s\n", m.ID, a.Digest, m.Source.StaticDir)
			}
			if m.Source.Fallback != "" {
				fmt.Fprintf(&s, " try_files {path} /%s\n", m.Source.Fallback)
			}
			s.WriteString(" file_server\n")
		} else if m.Kind == "external" || m.Kind == "external-compose" {
			fmt.Fprintf(&s, " reverse_proxy %s\n", m.Source.Upstream)
		} else {
			fmt.Fprintf(&s, " reverse_proxy pagehub-%s-web:%d\n", m.ID, m.Source.ContainerPort)
		}
		s.WriteString(" }\n")
	}
	s.WriteString(" handle {\n reverse_proxy pagehub-portal-web:80\n }\n }\n handle_errors {\n respond \"Application temporarily unavailable\" 503\n }\n}\n")
	return s.String()
}

func (g Gateway) Validate(ctx context.Context, r registry.Registry) error {
	p := g.Store.Path("gateway", "Caddyfile.candidate")
	if err := registry.AtomicWrite(p, []byte(Render(r)), 0644); err != nil {
		return err
	}
	_, err := g.Docker.Run(ctx, "", "run", "--rm", "--pull", "never", "--network", "none", "-v", g.Store.Path("gateway")+":/etc/caddy:ro", g.Image, "caddy", "validate", "--config", "/etc/caddy/Caddyfile.candidate", "--adapter", "caddyfile")
	return err
}

func (g Gateway) Commit(ctx context.Context, r registry.Registry) error {
	if err := registry.AtomicWrite(g.Store.Path("gateway", "Caddyfile"), []byte(Render(r)), 0644); err != nil {
		return err
	}
	if err := registry.WriteJSON(g.Store.Path("gateway", "public", "catalog.json"), CatalogWithReadiness(r, g.Store), 0644); err != nil {
		return err
	}
	// 首次安装时网关尚未启动，后续由安装流程启动。
	out, err := g.Docker.Run(ctx, "", "ps", "--filter", "label=com.docker.compose.project=pagehub-gateway", "--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return nil
	}
	_, err = g.Docker.Run(ctx, "", "exec", id, "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	return err
}

func (g Gateway) Reload(ctx context.Context) error {
	unlock, err := registry.Lock(g.Store.Path("state", "routes.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	r, err := g.Store.Load()
	if err != nil {
		return err
	}
	if err = g.Validate(ctx, r); err != nil {
		return err
	}
	old, err := os.ReadFile(g.Store.Path("gateway", "Caddyfile"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = g.Commit(ctx, r); err != nil {
		_ = registry.AtomicWrite(filepath.Join(g.Store.Root, "gateway", "Caddyfile"), old, 0644)
		return err
	}
	return nil
}
