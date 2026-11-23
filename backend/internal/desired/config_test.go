package desired

import (
	"os"
	"strings"
	"testing"
)

func example(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../config/pagehub.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExampleAndDerivedEnvironment(t *testing.T) {
	c, err := Decode(example(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range c.Apps {
		if app.Description == "" || app.IconPath == "" {
			t.Errorf("缺少项目描述或图标: %s", app.ID)
		}
	}
	env := c.Apps[0].Environment("https://example.com:63445")
	for key, wanted := range map[string]string{"TRAVEL_PUBLIC_ORIGIN": "https://example.com:63445", "SITE_DOMAIN": "example.com", "SITE_SCHEME": "https", "TRAVEL_BASE_PATH": "/diary", "COMPOSE_PROJECT_NAME": "travel-diary", "TRAVEL_WRITE_MODE": "local"} {
		if env[key] != wanted {
			t.Errorf("%s = %q", key, env[key])
		}
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	base := strings.ReplaceAll(string(example(t)), "\r\n", "\n")
	tests := map[string]string{
		"unknown":              strings.Replace(base, "schema_version: 1", "schema_version: 1\nunknown: true", 1),
		"nested unknown":       strings.Replace(base, "    service: app", "    service: app\n    privileged: true", 1),
		"duplicate key":        strings.Replace(base, "schema_version: 1", "schema_version: 1\nschema_version: 1", 1),
		"documents":            base + "\n---\nschema_version: 1\n",
		"missing enabled":      strings.Replace(base, "    enabled: true\n", "", 1),
		"untrusted repository": strings.Replace(base, "double-cloth-studio/travel-diary.git", "attacker/travel-diary.git", 1),
		"origin path":          strings.Replace(base, "https://example.com:63445", "https://example.com:63445/diary", 1),
		"origin credentials":   strings.Replace(base, "https://example.com:63445", "https://user:password@example.com", 1),
		"port collision":       strings.Replace(base, "port: 8091", "port: 8080", 1),
		"admin exposure":       strings.Replace(base, "bind: 127.0.0.1", "bind: 0.0.0.0", 1),
		"duplicate id":         strings.Replace(base, "id: whalgebra", "id: travel-diary", 1),
		"remote icon":          strings.Replace(base, "/assets/projects/whalgebra.ico", "//example.com/icon.ico", 1),
		"overlapping route":    strings.Replace(base, "path: /calc/", "path: /diary/calc/", 1),
		"traversal":            strings.Replace(base, "source_file: dist/Whalgebra.html", "source_file: ../outside.html", 1),
		"secret":               strings.Replace(base, "      TRAVEL_WRITE_MODE: local", "      ADMIN_PASSWORD: secret", 1),
		"unquoted setting":     strings.Replace(base, "'true'", "true", 1),
		"remote without TLS":   strings.ReplaceAll(strings.Replace(base, "TRAVEL_WRITE_MODE: local", "TRAVEL_WRITE_MODE: remote", 1), "https://example.com:63445", "http://example.com:63445"),
		"volume retention":     strings.Replace(base, "retain_on_uninstall: true", "retain_on_uninstall: false", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(input)); err == nil {
				t.Fatal("无效配置被接受")
			}
		})
	}
}
