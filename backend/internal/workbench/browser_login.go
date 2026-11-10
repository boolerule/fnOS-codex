package workbench

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// All workbench identities share the two OAuth callback ports registered by
// Codex. Serialize the probe and bind, and the final loopback exchange.
var browserPortMu sync.Mutex
var browserOwner atomic.Pointer[loginAttempt]

func (s *Server) clearPendingLoginLocked() {
	if p := s.pendingLogin; p != nil && p.Type == "chatgpt" {
		browserOwner.CompareAndSwap(p, nil)
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	s.pendingLogin = nil
}

func (s *Server) expireBrowserLogin(p *loginAttempt) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.pendingLogin != p {
		s.mu.Unlock()
		return
	}
	s.clearPendingLoginLocked()
	s.recordLoginResult("chatgpt", false)
	s.notifyLocked()
	s.mu.Unlock()
	_ = s.rpc("account/login/cancel", map[string]string{"loginId": p.LoginID}, nil)
}

func browserPortFree() bool {
	l, err := net.Listen("tcp4", "127.0.0.1:1455")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func loopbackURL(u *url.URL, path string) bool {
	if u == nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.EscapedPath() != path {
		return false
	}
	switch u.Host {
	case "localhost:1455", "localhost:1457", "127.0.0.1:1455", "127.0.0.1:1457":
		return true
	}
	return false
}

func parseBrowserGrant(raw string) (*url.URL, string, bool) {
	auth, err := url.Parse(raw)
	if err != nil || auth.Scheme != "https" || (auth.Host != "auth.openai.com" && auth.Host != "chatgpt.com") || auth.User != nil || auth.Fragment != "" || auth.EscapedPath() != "/oauth/authorize" {
		return nil, "", false
	}
	params, err := url.ParseQuery(auth.RawQuery)
	if err != nil || len(params["state"]) != 1 || params.Get("state") == "" || len(params["redirect_uri"]) != 1 {
		return nil, "", false
	}
	target, err := url.Parse(params.Get("redirect_uri"))
	if err != nil || !loopbackURL(target, "/auth/callback") || target.RawQuery != "" || target.ForceQuery {
		return nil, "", false
	}
	return target, params.Get("state"), true
}

// Called with s.mu held. The upstream notification can contain token endpoint
// data, so only expose the outcome of the matching login.
func (s *Server) recordLoginResult(kind string, success bool) {
	if success {
		s.loginEvent("account_login_completed")
		return
	}
	s.loginEvent("account_login_failed")
	if kind == "chatgpt" {
		s.failure = browserLoginFailed
	} else {
		s.failure = deviceLoginFailed
	}
}

func (s *Server) cancelLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		LoginID string `json:"loginId"`
	}
	if !decode(w, r, &b) {
		return
	}
	s.mu.Lock()
	pending := s.pendingLogin
	s.mu.Unlock()
	if pending == nil || b.LoginID != pending.LoginID {
		fail(w, 409, "err_login_expired", nil)
		return
	}
	if pending.Type == "chatgpt" {
		browserPortMu.Lock()
		defer browserPortMu.Unlock()
	}
	if err := s.rpc("account/login/cancel", map[string]string{"loginId": b.LoginID}, nil); err != nil {
		fail(w, 502, "err_browser_login", nil)
		return
	}
	s.mu.Lock()
	s.clearPendingLoginLocked()
	if s.failure == deviceLoginFailed || s.failure == browserLoginFailed {
		s.failure = ""
	}
	s.notifyLocked()
	s.mu.Unlock()
	// A canceled listener can take a moment to close after the RPC returns.
	if pending.Type == "chatgpt" {
		deadline := time.Now().Add(time.Second)
		for !browserPortFree() && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) browserCallback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LoginID     string `json:"loginId"`
		CallbackURL string `json:"callbackUrl"`
	}
	// Avoid echoing any part of a malformed credential-bearing request.
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		s.loginEvent("browser_callback_rejected")
		fail(w, 400, "err_callback_invalid", nil)
		return
	}
	browserPortMu.Lock()
	defer browserPortMu.Unlock()
	s.mu.Lock()
	pending := s.pendingLogin
	s.mu.Unlock()
	if pending == nil || browserOwner.Load() != pending || pending.LoginID != in.LoginID || !time.Now().Before(pending.expires) || pending.forwarded {
		s.loginEvent("browser_callback_rejected")
		fail(w, 409, "err_login_expired", nil)
		return
	}
	target, state, valid := parseBrowserGrant(pending.AuthURL)
	pasted, err := url.Parse(strings.TrimSpace(in.CallbackURL))
	if !valid || err != nil || !loopbackURL(pasted, "/auth/callback") || target.Host != pasted.Host {
		s.loginEvent("browser_callback_rejected")
		fail(w, 400, "err_callback_invalid", nil)
		return
	}
	q, err := url.ParseQuery(pasted.RawQuery)
	if err != nil || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(state), []byte(q.Get("state"))) != 1 || len(q["code"]) != 1 || q.Get("code") == "" || q.Has("error") {
		s.loginEvent("browser_callback_rejected")
		fail(w, 400, "err_callback_invalid", nil)
		return
	}
	// Never dial a user-supplied host or proxy. Rebuild the request exclusively
	// from the stored redirect and the two OAuth parameters Codex verifies.
	target.Host = net.JoinHostPort("127.0.0.1", target.Port())
	target.RawQuery = url.Values{"code": {q.Get("code")}, "state": {state}}.Encode()
	pending.forwarded = true // s.op serializes all HTTP operations on this account.
	if !completeLoopbackLogin(r, target) {
		s.loginEvent("browser_callback_failed")
		fail(w, 502, "err_callback_failed", nil)
		return
	}
	s.loginEvent("browser_callback_forwarded")
	// Completion is confirmed separately via account/read and the Codex event.
	writeJSON(w, map[string]bool{"ok": true})
}

func completeLoopbackLogin(r *http.Request, target *url.URL) bool {
	client := http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       30 * time.Second,
	}
	get := func(u *url.URL) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	resp, err := get(target)
	if err != nil {
		return false
	}
	resp.Body.Close()
	// Codex can return a rendered error page with HTTP 200. A successful
	// token exchange redirects either to its local /success or a hosted page.
	if resp.StatusCode != http.StatusFound {
		return false
	}
	location, err := resp.Location()
	if err != nil {
		return false
	}
	if location.Scheme == "https" && location.User == nil && (location.Host == "chatgpt.com" || location.Host == "auth.openai.com") {
		return true
	}
	if !loopbackURL(location, "/success") || location.Port() != target.Port() {
		return false
	}
	// Older Codex waits for GET /success before publishing login/completed.
	// Its Location may contain tokens. Discard it and request a fixed path.
	done, err := get(&url.URL{Scheme: "http", Host: target.Host, Path: "/success"})
	if err != nil {
		return false
	}
	done.Body.Close()
	return done.StatusCode == http.StatusOK
}
