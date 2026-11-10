//go:build !noterminal

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCodexLaunchAndLoginCommands(t *testing.T) {
	t.Setenv("TRIM_API_TOKEN", "platform-secret")
	t.Setenv("CODEX_CONSOLE_PASSWORD", "console-secret")
	t.Setenv("OPENAI_API_KEY", "another-account")
	home := t.TempDir()
	workspace := filepath.Join(home, "project & $(touch unexpected)")
	for _, tc := range []struct {
		mode string
		cwd  string
		args []string
	}{
		{"codex", workspace, []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}},
		{"login", home, []string{"login", "--device-auth"}},
	} {
		cmd, err := codexTerminalCommand("codex", home, workspace, tc.mode)
		if err != nil || cmd.Dir != tc.cwd || !reflect.DeepEqual(cmd.Args[1:], tc.args) {
			t.Fatalf("%s: cmd=%v err=%v", tc.mode, cmd, err)
		}
		if !slices.Contains(cmd.Env, "HOME="+home) || !slices.Contains(cmd.Env, "CODEX_HOME="+filepath.Join(home, ".codex")) {
			t.Fatal("missing user-specific Codex home")
		}
		for _, v := range cmd.Env {
			for _, k := range []string{"TRIM_API_TOKEN=", "CODEX_CONSOLE_PASSWORD=", "OPENAI_API_KEY="} {
				if strings.HasPrefix(v, k) {
					t.Fatal("secret inherited", k)
				}
			}
		}
	}
	if _, err := codexTerminalCommand("codex", home, workspace, "--dangerous-option"); err == nil {
		t.Fatal("custom CLI argument accepted")
	}
}

func TestTerminalSessionOwner(t *testing.T) {
	session, _ := newHistorySession(t, "private")
	mgr := NewSessionManager(&RuntimeEnv{})
	mgr.sessions[session.id] = session
	appendHistory(session, []byte("private transcript"))
	for _, other := range []string{"", "bob"} {
		if len(mgr.list(other)) > 0 {
			t.Fatal("listed another user's session")
		}
		if _, ok := mgr.get(other, session.id); ok {
			t.Fatal("could attach to another user's session")
		}
		if _, err := mgr.history(other, session.id); err == nil {
			t.Fatal("read another user's history")
		}
		if err := mgr.clearHistory(other, session.id); err == nil {
			t.Fatal("cleared another user's history")
		}
		if err := mgr.closeByID(other, session.id); err == nil {
			t.Fatal("closed another user's session")
		}
	}
	if got, err := mgr.history(session.user, session.id); err != nil || string(got) != "private transcript" {
		t.Fatalf("owner history: %q, %v", got, err)
	}
}

func TestCodexPTYLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fnOS PTY integration runs on Linux")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mock-codex")
	err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$CODEX_HOME\" \"$*\"\nread answer\n"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&RuntimeEnv{CodexHome: filepath.Join(dir, "users"), SessionDir: filepath.Join(dir, "sessions")})
	defer mgr.CloseAll()
	session, err := mgr.create("1001", dir, "codex", bin)
	if err != nil {
		t.Fatal(err)
	}
	want := "--ask-for-approval on-request"
	deadline := time.Now().Add(5 * time.Second)
	for {
		history, err := mgr.history("1001", session.id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(history), want) {
			if !strings.Contains(string(history), filepath.Join(dir, "users", "1001", ".codex")) {
				t.Fatal("missing private CLI home")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing PTY output: %q", history)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := mgr.closeByID("1001", session.id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", session.id+".log")); !os.IsNotExist(err) {
		t.Fatal("transcript survived close")
	}
}
