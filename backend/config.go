package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type RuntimeEnv struct {
	DataDir, Home, CodexHome, Path, Lang             string
	AdminSock, AdminBaseURL, Listen, Password        string
	TRIMApiToken, TRIMAppName, SessionDir, Workspace string
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadRuntimeEnv() (*RuntimeEnv, error) {
	dir, err := filepath.Abs(envOr("CODEX_CONSOLE_DATA", envOr("HARNESS_DATA_DIR", envOr("TRIM_PKGVAR", "codex-data"))))
	if err != nil {
		return nil, err
	}
	app := envOr("TRIM_APPNAME", "Codex")
	base := strings.TrimRight(envOr("CODEX_CONSOLE_BASE", envOr("HARNESS_ADMIN_BASEURL", "/app/"+app)), "/")
	if base != "" && (!strings.HasPrefix(base, "/") || path.Clean(base) != base || strings.ContainsAny(base, "\\?#<>\"' \r\n\t&")) {
		return nil, fmt.Errorf("invalid console base path")
	}
	workspace := os.Getenv("CODEX_WORKSPACE")
	if workspace != "" && !filepath.IsAbs(workspace) {
		return nil, fmt.Errorf("CODEX_WORKSPACE must be absolute")
	}
	listen := os.Getenv("CODEX_CONSOLE_LISTEN")
	password := os.Getenv("CODEX_CONSOLE_PASSWORD")
	if listen != "" && password == "" {
		return nil, fmt.Errorf("CODEX_CONSOLE_LISTEN requires CODEX_CONSOLE_PASSWORD")
	}
	return &RuntimeEnv{
		DataDir: dir, CodexHome: filepath.Join(dir, "users"), Home: os.Getenv("HOME"),
		Path: os.Getenv("PATH"), Lang: os.Getenv("TRIM_SYS_LANGUAGE"), SessionDir: sessionDirFor(dir),
		TRIMAppName: app, TRIMApiToken: os.Getenv("TRIM_API_TOKEN"), AdminBaseURL: base,
		AdminSock: envOr("CODEX_CONSOLE_SOCKET", envOr("HARNESS_ADMIN_SOCK", filepath.Join(envOr("TRIM_APPDEST", dir), "app.sock"))),
		Listen:    listen, Password: password, Workspace: workspace,
	}, nil
}
