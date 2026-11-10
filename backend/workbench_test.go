package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkbenchGatewayIsolatesUsersAndAuthorizedProjects(t *testing.T) {
	dir := t.TempDir()
	env := &RuntimeEnv{DataDir: dir, CodexHome: filepath.Join(dir, "users"), AdminBaseURL: "/app/CodexTerminal"}
	app := &AdminMux{renv: env, codex: NewCodexManager(env)}
	defer app.codex.Close()
	defer app.workbench.close()
	h := app.handler(true)
	request := func(user, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, env.AdminBaseURL+"/api/workbench/"+path, strings.NewReader(body))
		r.Header.Set("X-Trim-Userid", user)
		r.Header.Set("X-Trim-Username", "administrator")
		r.Header.Set("X-Trim-Isadmin", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var roots []string
	result := request("42", http.MethodGet, "roots", "")
	if err := json.Unmarshal(result.Body.Bytes(), &roots); err != nil || len(roots) != 1 {
		t.Fatal(result.Body.String(), err)
	}
	data, _ := json.Marshal(map[string]string{"name": "Project for 42", "path": roots[0]})
	if got := request("42", http.MethodPost, "projects", string(data)); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request("43", http.MethodPost, "projects", string(data)); got.Code != 403 {
		t.Fatal("another user's workspace accepted", got.Code, got.Body.String())
	}
	if got := request("43", http.MethodGet, "projects", ""); got.Code != 200 || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatal("another user's project visible", got.Code, got.Body.String())
	}
	if got := request("42", http.MethodGet, "projects", ""); !strings.Contains(got.Body.String(), "Project for 42") {
		t.Fatal(got.Body.String())
	}
}
