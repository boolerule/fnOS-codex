package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uninstallFixture(t *testing.T) (*RuntimeEnv, SystemCodex) {
	t.Helper()
	dir := t.TempDir()
	env := &RuntimeEnv{DataDir: filepath.Join(dir, "appdata")}
	prefix := filepath.Join(dir, "npm prefix")
	pkg := globalCodexDir(prefix)
	for _, p := range []string{filepath.Join(pkg, "bin"), filepath.Join(prefix, "bin"), filepath.Join(env.DataDir, "codex-cli", "1.2.3"), filepath.Join(env.DataDir, "users", "42", ".codex"), filepath.Join(env.DataDir, "workspaces", "42")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@openai/codex","version":"1.2.3","bin":{"codex":"bin/codex.js"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(pkg, "bin", "codex.js")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(launcher, filepath.Join(prefix, "bin", "codex")); err != nil {
		t.Fatal(err)
	}
	return env, SystemCodex{Kind: "npm", Prefix: prefix, Path: launcher, Version: "1.2.3"}
}

func TestUninstallCodexRemovesOnlyRequestedInstallations(t *testing.T) {
	env, c := uninstallFixture(t)
	sibling := filepath.Join(filepath.Dir(globalCodexDir(c.Prefix)), "another")
	if err := os.Mkdir(sibling, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := uninstallCodexCopies(context.Background(), env, []SystemCodex{c, c}, func(_ context.Context, prefix string) error {
		calls++
		if prefix != c.Prefix {
			t.Fatal("unexpected prefix", prefix)
		}
		if err := os.Remove(filepath.Join(prefix, "bin", "codex")); err != nil {
			return err
		}
		return os.RemoveAll(globalCodexDir(prefix))
	})
	if err != nil || calls != 1 {
		t.Fatalf("cleanup: %v, calls %d", err, calls)
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, "codex-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("managed CLI remains")
	}
	for _, p := range []string{sibling, filepath.Join(env.DataDir, "users", "42", ".codex"), filepath.Join(env.DataDir, "workspaces", "42")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unrelated path touched: %s", p)
		}
	}
}

func TestUninstallCodexPreservesAmbiguousOrUnsupported(t *testing.T) {
	env, c := uninstallFixture(t)
	link := filepath.Join(c.Prefix, "bin", "codex")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("belongs to another installer"), 0755); err != nil {
		t.Fatal(err)
	}
	copies := []SystemCodex{c, {Kind: "binary", Path: "/example/path/to/code-binary"}}
	err := uninstallCodexCopies(context.Background(), env, copies, func(context.Context, string) error { t.Fatal("called npm for non-owned link"); return nil })
	if err == nil || !strings.Contains(err.Error(), c.Path) || !strings.Contains(err.Error(), copies[1].Path) {
		t.Fatal(err)
	}
	for _, p := range []string{link, globalCodexDir(c.Prefix), filepath.Join(env.DataDir, "codex-cli")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUninstallCodexVerifiesNpmExit(t *testing.T) {
	env, c := uninstallFixture(t)
	for _, fail := range []error{nil, os.ErrPermission} {
		err := uninstallCodexCopies(context.Background(), env, []SystemCodex{c}, func(context.Context, string) error { return fail })
		if err == nil {
			t.Fatal("reported removal with package still installed")
		}
	}
}

func TestUninstallCodexRequiresExplicitUninstallOperation(t *testing.T) {
	t.Setenv("TRIM_APPNAME", "CodexTerminal")
	t.Setenv("TRIM_APP_STATUS", "UPGRADE")
	t.Setenv("wizard_codex_cleanup", "remove")
	if err := uninstallSystemCodex(); err == nil || !strings.Contains(err.Error(), "卸载向导") {
		t.Fatal(err)
	}
	t.Setenv("TRIM_APP_STATUS", "UNINSTALL")
	t.Setenv("wizard_codex_cleanup", "keep")
	if err := uninstallSystemCodex(); err == nil || !strings.Contains(err.Error(), "卸载向导") {
		t.Fatal(err)
	}
}

func TestUninstallCodexRejectsManagedSymlink(t *testing.T) {
	env := &RuntimeEnv{DataDir: t.TempDir()}
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(env.DataDir, "codex-cli")); err != nil {
		t.Fatal(err)
	}
	if err := uninstallCodexCopies(context.Background(), env, nil, nil); err == nil {
		t.Fatal("accepted symlinked manager root")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("target removed", err)
	}
}
