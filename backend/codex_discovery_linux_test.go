package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeNpmFixture(t *testing.T, dir, version, body string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin", "codex.js")
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fmt.Sprintf(`{"name":"@openai/codex","version":%q,"bin":{"codex":"bin/codex.js"}}`, version)), 0644); err != nil {
		t.Fatal(err)
	}
	return bin
}
func TestScanSystemCodexLayoutsAndTrust(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "was-executed")
	npm := filepath.Join(dir, ".nvm/versions/node/24/lib/node_modules/@openai/codex")
	pnpm := filepath.Join(dir, ".local/share/pnpm/global/v11/group123/node_modules/@openai/codex")
	path1 := writeNpmFixture(t, npm, "1.2.1", "#!/bin/sh\ntouch "+marker+"\n")
	path2 := writeNpmFixture(t, pnpm, "1.3.1", "#!/bin/sh\nexit 9\n")
	bin := filepath.Join(dir, ".nvm/versions/node/24/bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path1, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	dangerous := filepath.Join(dir, "public")
	if err := os.Mkdir(dangerous, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dangerous, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(dangerous, "codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pnpm, 0777); err != nil {
		t.Fatal(err)
	}
	standalone := filepath.Join(dir, ".codex/packages/standalone/releases/0.153.4-x86_64-unknown-linux-musl/bin/codex")
	if err := os.MkdirAll(filepath.Dir(standalone), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standalone, []byte("\x7fELFfixture"), 0755); err != nil {
		t.Fatal(err)
	}
	// A bad home layout must not be opened as a blocking FIFO.
	if err := os.MkdirAll(filepath.Join(dir, ".asdf/installs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, ".asdf/installs/nodejs"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SCAN_PATHS", strings.Join([]string{dangerous, bin}, ":"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	got := scanSystemCodex(ctx, &RuntimeEnv{DataDir: filepath.Join(dir, "data"), Home: dir, Path: bin}, false)
	if time.Since(started) > 5*time.Second {
		t.Fatal("scan blocked")
	}
	count := map[string]int{}
	for _, v := range got {
		count[v.Path]++
		switch v.Path {
		case path1:
			if !v.Selectable || v.Version != "1.2.1" || v.Kind != "npm" || v.Prefix != filepath.Dir(bin) {
				t.Fatalf("npm: %+v", v)
			}
		case path2:
			if v.Selectable || v.Version != "1.3.1" || v.Kind != "pnpm" {
				t.Fatalf("pnpm: %+v", v)
			}
		case standalone:
			if v.Version != "0.153.4" || v.Kind != "binary" {
				t.Fatalf("standalone: %+v", v)
			}
		}
	}
	if count[path1] != 1 || count[path2] != 1 || count[standalone] != 1 {
		t.Fatalf("missing or duplicate paths: %+v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("scanner ran npm launcher")
	}
	if _, ok := pathCodex(dangerous); ok {
		t.Fatal("non-Codex ELF accepted through alias")
	}
}

func TestSelectSystemCodexOfflineAndRecheck(t *testing.T) {
	home := t.TempDir()
	prefix := filepath.Join(home, "npm-live")
	bin := filepath.Join(prefix, "bin")
	root := filepath.Join(prefix, "lib/node_modules/@openai/codex")
	js := writeNpmFixture(t, root, "1.2.3", "#!/bin/sh\necho codex-cli 1.2.3\n")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_SCAN_PATHS", bin)
	env := &RuntimeEnv{DataDir: filepath.Join(home, "data"), Home: home, Path: "/usr/bin:/bin"}
	m := NewCodexManager(env)
	defer m.Close()
	if err := m.selectSystem("1.2.3", "/bin/sh"); err == nil {
		t.Fatal("accepted arbitrary path")
	}
	if err := m.selectSystem("1.2.3", js); err != nil {
		t.Fatal(err)
	}
	m.installer.Wait()
	st := m.Status()
	if !st.Installed || st.SystemPath != js || st.Source != "path" || st.Version != "1.2.3" || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	m2 := NewCodexManager(env)
	defer m2.Close()
	if got, err := m2.Binary(); got != js || err != nil {
		t.Fatalf("selection after restart: %q %v", got, err)
	}
	// External updates may replace executables without notifying the console.
	if err := os.WriteFile(js, []byte("#!/bin/sh\necho codex-cli 9.11.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Binary(); err == nil {
		t.Fatal("quietly launched a different version")
	}
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	if err := m2.selectSystem("1.2.3", js); err == nil {
		t.Fatal("accepted an untrusted package")
	}
}
