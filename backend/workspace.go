package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type workspaceEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func (m *AdminMux) workspaces(r *http.Request) ([]workspaceEntry, error) {
	user := consoleUser(r)
	if user == "" {
		return nil, errors.New("missing console identity")
	}
	dir := m.renv.Workspace
	if dir == "" {
		dir = filepath.Join(m.renv.DataDir, "workspaces", user)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	paths := []string{dir}
	if user != "local" && m.renv.TRIMApiToken != "" {
		uid, err := strconv.Atoi(user)
		if err != nil {
			return nil, err
		}
		roots, err := m.fnos.GetUserAccessibleFolders(r.Context(), uid)
		if err != nil {
			return nil, err
		}
		paths = append(paths, roots...)
	}
	entries := make([]workspaceEntry, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil || seen[real] {
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[real] = true
		entries = append(entries, workspaceEntry{Path: real, Name: filepath.Base(filepath.Clean(p))})
	}
	return entries, nil
}

// Resolve fresh grants on launch and attach. Request parameters never add a
// workspace to the grant list.
func (m *AdminMux) workspace(r *http.Request, path string) (string, error) {
	entries, err := m.workspaces(r)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if path == entry.Path {
			return path, nil
		}
	}
	return "", errors.New("workspace is unavailable or not authorized")
}
