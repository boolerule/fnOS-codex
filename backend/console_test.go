package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleTrustBoundaries(t *testing.T) {
	env := &RuntimeEnv{Password: "test-password", DataDir: t.TempDir(), AdminBaseURL: "/app/Custom"}
	app := &AdminMux{renv: env}
	for _, tc := range []struct {
		name                     string
		gateway, password, admin bool
		origin                   string
		want                     int
	}{
		{"direct ignores gateway headers", false, false, true, "", 401},
		{"direct with credentials", false, true, false, "http://example.com", 200},
		{"direct rejects foreign origin", false, true, true, "http://foreign.invalid", 403},
		{"gateway requires administrator", true, true, false, "", 403},
		{"gateway recognizes administrator", true, false, true, "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.com/api/workspaces", nil)
			r.Header.Set("X-Trim-Userid", "42")
			r.Header.Set("X-Trim-Username", "test")
			r.Header.Set("X-Forwarded-Host", "foreign.invalid")
			if tc.admin {
				r.Header.Set("X-Trim-Isadmin", "true")
			}
			if tc.password {
				r.SetBasicAuth("codex", env.Password)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			app.handler(tc.gateway).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if tc.want == 200 && !strings.Contains(w.Body.String(), "workspaces") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestWorkspaceRequiresCurrentUserGrant(t *testing.T) {
	app := &AdminMux{renv: &RuntimeEnv{DataDir: t.TempDir()}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), consoleIdentityKey{}, "42"))
	entries, err := app.workspaces(r)
	if err != nil || len(entries) != 1 {
		t.Fatalf("workspaces: %v %v", entries, err)
	}
	accepted, err := app.workspace(r, entries[0].Path)
	if err != nil || accepted != entries[0].Path {
		t.Fatal(accepted, err)
	}
	for _, outside := range []string{t.TempDir(), filepath.Join(filepath.Dir(accepted), "43"), ""} {
		if _, err := app.workspace(r, outside); err == nil {
			t.Fatalf("accepted %q", outside)
		}
	}
}

func TestDirectListenerRequiresPassword(t *testing.T) {
	t.Setenv("CODEX_CONSOLE_LISTEN", "127.0.0.1:3079")
	t.Setenv("CODEX_CONSOLE_PASSWORD", "")
	if _, err := loadRuntimeEnv(); err == nil {
		t.Fatal("accepted empty password")
	}
}

func TestGatewayDoesNotUnlinkRegularFile(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "app.sock")
	if err := os.WriteFile(socket, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := listenGateway(socket)
	if err == nil {
		ln.Close()
		t.Fatal("occupied socket accepted")
	}
	b, err := os.ReadFile(socket)
	if err != nil || string(b) != "existing" {
		t.Fatal("regular file was modified", err)
	}
}
