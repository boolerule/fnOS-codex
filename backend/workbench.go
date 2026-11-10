package main

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"fnos-codex/backend/internal/workbench"
)

// Each authenticated administrator gets a separate app-server and history.
// Their web workbench and terminal intentionally share the same Codex login.
type userWorkbenches struct {
	mu     sync.Mutex
	users  map[string]*workbench.Server
	closed bool
}

func (m *AdminMux) userWorkbench(user string) (*workbench.Server, error) {
	b := &m.workbench
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("console is stopping")
	}
	if s := b.users[user]; s != nil {
		return s, nil
	}
	home := filepath.Join(m.renv.CodexHome, user)
	s, err := workbench.New(workbench.Config{
		ClientVersion: consoleVersion,
		DataDir:       filepath.Join(home, "workbench"),
		CodexHome:     filepath.Join(home, ".codex"),
		Env:           codexUserEnv(home),
		Binary:        m.codex.Binary,
		OnLoginEvent:  func(code string) { m.diagnostics.record(user, code) },
		Roots: func(r *http.Request) ([]string, error) {
			// Background checks need the same gateway uid as HTTP requests.
			r = r.WithContext(withConsoleIdentity(r.Context(), user))
			entries, err := m.workspaces(r)
			if err != nil {
				return nil, err
			}
			paths := make([]string, 0, len(entries))
			for _, entry := range entries {
				paths = append(paths, entry.Path)
			}
			return paths, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if b.users == nil {
		b.users = make(map[string]*workbench.Server)
	}
	b.users[user] = s
	return s, nil
}

func (m *AdminMux) handleWorkbench(w http.ResponseWriter, r *http.Request, path string) {
	user := consoleUser(r)
	if user == "" {
		writeErr(w, "authentication required", http.StatusUnauthorized)
		return
	}
	s, err := m.userWorkbench(user)
	if err != nil {
		writeErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	req := r.Clone(r.Context())
	req.URL.Path = "/api/" + strings.TrimPrefix(path, "/api/workbench/")
	req.URL.RawPath = ""
	s.ServeHTTP(w, req)
}

func (b *userWorkbenches) close() {
	b.mu.Lock()
	b.closed = true
	users := b.users
	b.users = nil
	b.mu.Unlock()
	for _, s := range users {
		s.Close()
	}
}
