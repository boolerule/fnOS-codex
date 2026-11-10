package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Use copies of this test executable as npm/codex so the entire asynchronous
// install path can be exercised without a registry or a platform shell.
func TestMain(m *testing.M) {
	mode, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".codex-installer-fixture"))
	if err == nil {
		bin := os.Args[0]
		if os.Getenv("CODEX_CONSOLE_PASSWORD") != "" || os.Getenv("TRIM_API_TOKEN") != "" ||
			os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("NO_PROXY") != "test-localhost" {
			fmt.Fprintln(os.Stderr, "unexpected installer/probe environment")
			os.Exit(4)
		}
		switch strings.TrimSuffix(filepath.Base(bin), ".exe") {
		case "codex":
			version := "1.0.0"
			if data, err := os.ReadFile(bin + ".version"); err == nil {
				version = string(data)
			}
			fmt.Println("codex-cli", version)
			os.Exit(0)
		case "npm":
			if string(mode) == "fail" {
				os.Exit(2)
			}
			if string(mode) == "wait" {
				time.Sleep(time.Minute)
			}
			i := slices.Index(os.Args, "--prefix")
			spec, ok := strings.CutPrefix(os.Args[len(os.Args)-1], "@openai/codex@")
			if i < 0 || i+1 >= len(os.Args) || !ok {
				os.Exit(3)
			}
			version := spec
			if spec == "latest" || string(mode) == "mismatch" {
				version = "2.0.0"
			}
			target := filepath.Join(os.Args[i+1], "node_modules", ".bin", "codex")
			if runtime.GOOS == "windows" {
				target = filepath.Join(os.Args[i+1], "node_modules", "@openai", "codex-win32-x64", "vendor", "test", "bin", "codex.exe")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				panic(err)
			}
			data, err := os.ReadFile(bin)
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(target, data, 0755); err != nil {
				panic(err)
			}
			if err := os.WriteFile(target+".version", []byte(version), 0600); err != nil {
				panic(err)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestCodexInstallKeepsWorkingVersion(t *testing.T) {
	t.Setenv("CODEX_CONSOLE_PASSWORD", "test-console-password")
	t.Setenv("TRIM_API_TOKEN", "test-trim-token")
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	t.Setenv("NO_PROXY", "test-localhost")
	root := t.TempDir()
	setMode := func(mode string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, ".codex-installer-fixture"), []byte(mode), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setMode("install")
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm", "codex"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(binDir, name), data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	renv := &RuntimeEnv{DataDir: root, Path: binDir, Home: root}
	manager := NewCodexManager(renv)
	defer manager.Close()
	if s := manager.Status(); !s.Installed || s.Source != "path" || s.Version != "1.0.0" {
		v, ok := pathCodex(binDir)
		t.Fatalf("system version: %+v (binDir=%s, PATH candidate=%+v,ok=%v)", s, binDir, v, ok)
	}
	start := func(version string) {
		t.Helper()
		if err := manager.Install(version); err != nil {
			t.Fatal(err)
		}
	}
	wait := func() CodexStatus {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if s := manager.Status(); !s.Installing {
				return s
			}
			time.Sleep(time.Millisecond * 10)
		}
		t.Fatal("install timed out")
		return CodexStatus{}
	}
	start("latest")
	if s := wait(); s.Error != "" || s.Source != "managed" || s.Version != "2.0.0" {
		t.Fatalf("managed install: %+v", s)
	}
	before, err := manager.Binary()
	if err != nil {
		t.Fatal(err)
	}
	setMode("fail")
	start("latest")
	if s := wait(); s.Error == "" || s.Version != "2.0.0" {
		t.Fatalf("failed upgrade: %+v", s)
	}
	after, err := manager.Binary()
	if err != nil || before != after {
		t.Fatalf("active binary changed to %q after failed update: %v", after, err)
	}
	setMode("mismatch")
	start("1.8.2")
	if s := wait(); s.Error == "" || s.Version != "2.0.0" {
		t.Fatalf("activated a version that did not match the request: %+v", s)
	}
	setMode("install")
	start("1.9.0")
	if s := wait(); s.Error != "" || s.Version != "1.9.0" {
		t.Fatalf("exact version not installed: %+v", s)
	}
	// Previously installed versions can be selected without npm or network.
	npm, err := manager.findExecutable("npm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(npm, npm+".disabled"); err != nil {
		t.Fatal(err)
	}
	start("2.0.0")
	if s := wait(); s.Error != "" || s.Version != "2.0.0" {
		t.Fatalf("offline reactivation failed: %+v", s)
	}
	cached, err := codexPackageBinary(filepath.Join(manager.root, "1.9.0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached+".version", []byte("1.8.0"), 0600); err != nil {
		t.Fatal(err)
	}
	start("1.9.0")
	if s := wait(); s.Error == "" || s.Version != "2.0.0" {
		t.Fatalf("activated corrupted local version: %+v", s)
	}
	if err := os.Rename(npm+".disabled", npm); err != nil {
		t.Fatal(err)
	}
	setMode("wait")
	start("latest")
	if err := manager.Install("1.9.0"); err == nil {
		t.Fatal("accepted a second concurrent install")
	}
	manager.Close()
	if err := manager.Install("latest"); err == nil {
		t.Fatal("closed manager started an install")
	}
	reopened := NewCodexManager(renv)
	defer reopened.Close()
	if s := reopened.Status(); s.Source != "managed" || s.Version != "2.0.0" {
		t.Fatalf("lost active installation on restart: %+v", s)
	}
}

func TestCodexManagerWithoutInstalledExecutable(t *testing.T) {
	manager := NewCodexManager(&RuntimeEnv{DataDir: t.TempDir(), Path: t.TempDir()})
	defer manager.Close()
	if _, err := manager.Binary(); err == nil {
		t.Fatal("empty PATH reported a binary")
	}
	if s := manager.Status(); s.Installed || s.Installing || s.Error != "" {
		t.Fatalf("empty status: %+v", s)
	}
	if err := manager.Install("latest"); err == nil {
		t.Fatal("install accepted with npm missing")
	}
}

func TestCodexReleaseVersion(t *testing.T) {
	for _, version := range []string{"0.153.4", "0.41.0-alpha.1", "0.167.0-alpha.4.1", "1.0.0-rc.1+sha.abcdef"} {
		if !validCodexReleaseVersion(version) {
			t.Errorf("rejected %q", version)
		}
	}
	for _, version := range []string{"latest", "1", ">=1.0.0", "1.0.0;echo", "v0.153.4", "01.2.3", "1.2.3-alpha.01", "file:/tmp/x", "0.167.0-linux-x64", "0.167.0-alpha.5-win32-arm64", "0.167.0-darwin-x64+build", "1.0.0..", "1.0.0-", "1.0.0-rc..1", "1.0.0\n"} {
		if validCodexReleaseVersion(version) {
			t.Errorf("accepted %q", version)
		}
	}
}

func TestCodexInstallRequest(t *testing.T) {
	m := NewCodexManager(&RuntimeEnv{DataDir: t.TempDir(), Path: t.TempDir()})
	defer m.Close()
	app := &AdminMux{codex: m}
	for _, body := range []string{"{", "null", `{"version":"1.0.0","systemPath":null}`, `{"version":"1.0.0","systemPath":3}`, `{"version":null}`, `{"version":""}`, `{"version":4}`, `{"version":"--help"}`, `{"versions":"1.0.0"}`, "{}{}", strings.Repeat(" ", 4097)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		app.handleCodexInstall(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid install request accepted: got %d", w.Code)
		}
	}
	for _, body := range []string{"", "{}", `{"version":"latest"}`, `{"version":"0.153.4"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		app.handleCodexInstall(w, r)
		// All supported forms reach the installer, which reports missing npm.
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "npm") {
			t.Errorf("valid body %q: %d %s", body, w.Code, w.Body)
		}
	}
}

func TestCodexManagerRejectsInvalidSelection(t *testing.T) {
	dir := t.TempDir()
	manager := NewCodexManager(&RuntimeEnv{DataDir: dir, Path: t.TempDir()})
	defer manager.Close()
	if err := os.Mkdir(manager.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manager.root, "current"), []byte("../outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Binary(); err == nil {
		t.Fatal("accepted a version outside the installation directory")
	}
	if status := manager.Status(); status.Installed || status.Error == "" {
		t.Fatalf("invalid status for corrupted selection: %+v", status)
	}
}
