package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestLogPageScopeAndAuthentication(t *testing.T) {
	app := &AdminMux{renv: &RuntimeEnv{Password: "test-password", AdminBaseURL: "/app/CodexTerminal"}}
	app.diagnostics.record("", "service_started")
	app.diagnostics.record("1000", "device_code_forbidden")
	app.diagnostics.record("2000", "browser_callback_forwarded")
	app.diagnostics.record("local", "api_key_saved")
	app.diagnostics.record("1000", "invalid event containing fixture-secret")
	app.diagnostics.record("", "account_login_failed")
	for _, tc := range []struct {
		name                string
		gateway, authorized bool
		want                int
		codes               []string
	}{
		{"password identifies local user", false, true, 200, []string{"api_key_saved", "service_started"}},
		{"gateway identifies admin", true, true, 200, []string{"device_code_forbidden", "service_started"}},
		{"direct rejects forged headers", false, false, 401, nil},
		{"gateway requires admin", true, false, 403, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/logs?user=2000"
			if tc.gateway {
				path = app.renv.AdminBaseURL + path
			}
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("X-Trim-Userid", "1000")
			r.Header.Set("X-Trim-Username", "tester")
			if tc.authorized || !tc.gateway {
				r.Header.Set("X-Trim-Isadmin", "true")
			}
			if tc.authorized && !tc.gateway {
				r.SetBasicAuth("codex", app.renv.Password)
			}
			w := httptest.NewRecorder()
			app.handler(tc.gateway).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got HTTP %d, want %d", w.Code, tc.want)
			}
			if w.Code != 200 {
				return
			}
			var data struct{ Entries []diagnosticEntry }
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, entry := range data.Entries {
				codes = append(codes, entry.Code)
				if entry.Time.IsZero() || entry.Message == "" || entry.Level == "" {
					t.Fatalf("incomplete log entry: %+v", entry)
				}
			}
			if !slices.Equal(codes, tc.codes) || strings.Contains(w.Body.String(), "fixture-secret") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unexpected snapshot", w.Body.String())
			}
		})
	}
}

func TestDiagnosticLogBoundedAndOrdered(t *testing.T) {
	var d diagnosticLog
	if rows := d.snapshot("local"); rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty array, got %#v", rows)
	}
	d.record("local", "api_key_failed")
	for i := 0; i < len(d.entries); i++ {
		d.record("local", "api_key_saved")
	}
	rows := d.snapshot("local")
	if len(rows) != len(d.entries) || rows[len(rows)-1].Code != "api_key_saved" {
		t.Fatal("old entries were not evicted")
	}
}
