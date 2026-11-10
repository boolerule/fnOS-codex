package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexVersionCatalog(t *testing.T) {
	var requests atomic.Int32
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/mirror/@openai/codex" || r.Header.Get("Accept") != "application/vnd.npm.install-v1+json" {
			t.Errorf("unexpected registry request: %s %s", r.URL, r.Header.Get("Accept"))
		}
		if offline.Load() {
			http.Error(w, "offline", 503)
			return
		}
		fmt.Fprint(w, `{"dist-tags":{"latest":"1.10.0"},"versions":{
			"1.10.0-alpha.9":{"bin":{"codex":"bin/codex.js"}},
			"1.2.0":{"bin":{"codex":"bin/codex.js"}},
			"1.10.0-alpha.10":{"bin":{"codex":"bin/codex.js"}},
			"1.10.0":{"bin":{"codex":"bin/codex.js"}},
			"1.10.0-linux-x64":{"os":["linux"]},
			"1.11.0-alpha.1":{"bin":{"something":"bin/other.js"}}
		}}`)
	}))
	defer server.Close()
	t.Setenv("CODEX_NPM_REGISTRY", server.URL+"/mirror/")
	m := NewCodexManager(&RuntimeEnv{DataDir: t.TempDir()})
	defer m.Close()
	got := m.Versions(context.Background())
	expected := []string{"1.10.0", "1.10.0-alpha.10", "1.10.0-alpha.9", "1.2.0"}
	if got.Error != "" || got.Latest != "1.10.0" || !slices.Equal(got.Versions, expected) || got.Installed == nil {
		t.Fatalf("initial catalog: %+v", got)
	}
	offline.Store(true)
	localVersion := "1.1.2"
	bin := filepath.Join(m.root, localVersion, "node_modules", ".bin", "codex")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(m.root, localVersion, "node_modules", "@openai", "codex-win32-x64", "vendor", "test", "bin", "codex.exe")
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.root, "2.0.0"), 0700); err != nil {
		t.Fatal(err)
	}
	got = m.Versions(context.Background())
	if got.Error != "" || requests.Load() != 1 || !slices.Equal(got.Installed, []string{localVersion}) ||
		!slices.Equal(got.Versions, append(expected, localVersion)) {
		t.Fatalf("cached and local catalogs: %+v (requests=%d)", got, requests.Load())
	}
	m.catalog.until = time.Time{}
	got = m.Versions(context.Background())
	if got.Error == "" || got.Latest != "1.10.0" || requests.Load() != 2 || !slices.Equal(got.Versions, append(expected, localVersion)) {
		t.Fatalf("failed refresh lost useful versions: %+v", got)
	}
	// A fresh console also offers the local installation with no network.
	reopened := NewCodexManager(m.renv)
	defer reopened.Close()
	got = reopened.Versions(context.Background())
	if got.Error == "" || !slices.Equal(got.Versions, []string{localVersion}) || got.Latest != "" {
		t.Fatalf("offline catalog on startup: %+v", got)
	}
	offline.Store(false)
	if got = reopened.Versions(context.Background()); got.Error != "" || got.Latest != "1.10.0" {
		t.Fatalf("retry did not recover: %+v", got)
	}
}
