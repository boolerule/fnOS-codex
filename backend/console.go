package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type AdminMux struct {
	renv        *RuntimeEnv
	codex       *CodexManager
	sessions    *SessionManager
	fnos        *FnosClient
	workbench   userWorkbenches
	diagnostics diagnosticLog
}

type consoleIdentityKey struct{}

func withConsoleIdentity(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, consoleIdentityKey{}, user)
}

func consoleUser(r *http.Request) string {
	id, _ := r.Context().Value(consoleIdentityKey{}).(string)
	return id
}

// Only the Unix listener is behind the fnOS gateway. Never accept gateway
// identity or forwarding headers on the optional password-protected listener.
func (m *AdminMux) handler(gateway bool) http.Handler {
	base := ""
	if gateway {
		base = m.renv.AdminBaseURL
	}
	spa := spaHandler(embeddedFS, base)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Codex-Console-Version", consoleVersion)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		user := "local"
		if gateway {
			uid, err := strconv.Atoi(r.Header.Get("X-Trim-Userid"))
			admin := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Trim-Isadmin")))
			if err != nil || uid <= 0 || strings.TrimSpace(r.Header.Get("X-Trim-Username")) == "" || (admin != "true" && admin != "yes" && admin != "1") {
				writeErrU(w, 403, "err_admin_required", "请使用飞牛管理员账号打开控制台")
				return
			}
			user = strconv.Itoa(uid)
		} else {
			name, pass, ok := r.BasicAuth()
			expected, actual := sha256.Sum256([]byte(m.renv.Password)), sha256.Sum256([]byte(pass))
			if !ok || name != "codex" || m.renv.Password == "" || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Codex Console", charset="UTF-8"`)
				writeErrU(w, 401, "err_login_required", "请输入控制台用户名和密码")
				return
			}
		}
		r = r.WithContext(withConsoleIdentity(r.Context(), user))
		p := r.URL.Path
		if base != "" && (p == base || strings.HasPrefix(p, base+"/")) {
			p = strings.TrimPrefix(p, base)
		}
		if strings.HasPrefix(p, "/api/") || (p == "/terminal" && r.Header.Get("Upgrade") != "") {
			w.Header().Set("Cache-Control", "no-store")
			if !sameOrigin(r, gateway) {
				writeErrU(w, 403, "err_origin_denied", "跨站请求已拒绝")
				return
			}
			if m.terminalRoute(w, r, p) {
				return
			}
			switch {
			case p == "/api/logs" && r.Method == http.MethodGet:
				writeJSON(w, map[string]any{"entries": m.diagnostics.snapshot(user)})
			case strings.HasPrefix(p, "/api/workbench/"):
				m.handleWorkbench(w, r, p)
			case p == "/api/codex/status" && r.Method == http.MethodGet:
				writeJSON(w, m.codex.Status())
			case p == "/api/codex/versions" && r.Method == http.MethodGet:
				writeJSON(w, m.codex.Versions(r.Context()))
			case p == "/api/codex/local-versions" && r.Method == http.MethodGet:
				writeJSON(w, m.codex.LocalVersions(r.Context(), r.URL.Query().Get("refresh") == "1"))
			case p == "/api/codex/install" && r.Method == http.MethodPost:
				m.handleCodexInstall(w, r)
			case p == "/api/workspaces" && r.Method == http.MethodGet:
				workspaces, err := m.workspaces(r)
				if err != nil {
					writeErr(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				writeJSON(w, map[string]any{"workspaces": workspaces, "fnos": gateway && m.renv.TRIMApiToken != "", "appName": m.renv.TRIMAppName})
			default:
				http.NotFound(w, r)
			}
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		spa.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request, gateway bool) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := r.Host
	if gateway {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
			host = forwarded
		}
	}
	return strings.EqualFold(u.Host, host)
}
