package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type grantsTransport func(*http.Request) (*http.Response, error)

func (f grantsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRecheckGrantedDirectoryBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "project with spaces")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{shared}
	transport := grantsTransport(func(r *http.Request) (*http.Response, error) {
		var req struct {
			Req  string
			Data struct{ UID int }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Req != "trim.file.getUserAccessibleFolders" || req.Data.UID != 7 || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("incorrect fnOS grant request %+v", req)
		}
		b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"paths": paths}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	env := &RuntimeEnv{DataDir: root, TRIMApiToken: "test-token"}
	app := &AdminMux{renv: env, fnos: NewFnosClient(env)}
	app.fnos.client.Transport = transport
	r := httptest.NewRequest("GET", "/api/workspaces", nil)
	r = r.WithContext(context.WithValue(r.Context(), consoleIdentityKey{}, "7"))
	shared, err := filepath.EvalSymlinks(shared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.workspace(r, shared); err != nil {
		t.Fatal(err)
	}
	paths = nil
	if _, err := app.workspace(r, shared); err == nil {
		t.Fatal("accepted revoked workspace")
	}
}
