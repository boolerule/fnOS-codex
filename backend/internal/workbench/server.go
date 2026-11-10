// Package workbench serves an authenticated user through the Codex app-server protocol.
package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fnos-codex/backend/internal/codex"
	"maps"
)

type Config struct {
	ClientVersion      string
	DataDir, CodexHome string
	Binary             func() (string, error)
	Roots              func(*http.Request) ([]string, error)
	Env                []string
	OnLoginEvent       func(string)
}
type loginAttempt struct {
	Type            string `json:"type"`
	LoginID         string `json:"loginId,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	AuthURL         string `json:"authUrl,omitempty"`
	expires         time.Time
	timer           *time.Timer
	forwarded       bool
}

const deviceLoginFailed = "Codex device sign-in failed"
const browserLoginFailed = "Codex browser sign-in failed"

type Approval struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	RawID  json.RawMessage `json:"-"`
}
type Server struct {
	cfg                      Config
	op                       sync.Mutex // 同一实例的任务提交、重启、账号切换串行化。
	mu                       sync.Mutex
	state                    State
	client                   *codex.Client
	ready                    bool
	failure                  string
	activeThread, activeTurn string
	approvals                map[string]Approval
	live                     map[string][]map[string]any
	subscribers              map[chan struct{}]bool
	binary                   string
	generation               uint64 // Late notifications from a retired process cannot change the new process.
	ctx                      context.Context
	cancel                   context.CancelFunc
	activeRequest            *http.Request
	pendingLogin             *loginAttempt
	// A login can complete before account/login/start returns its ID.
	earlyLoginResults map[string]bool
	retryAfter        time.Time
	loaded            map[string]bool
}

func New(cfg Config) (*Server, error) {
	if cfg.Binary == nil || cfg.Roots == nil || !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.CodexHome) {
		return nil, errors.New("invalid workbench configuration")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	s, err := loadState(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{cfg: cfg, state: s, ctx: ctx, cancel: cancel, approvals: map[string]Approval{}, live: map[string][]map[string]any{}, subscribers: map[chan struct{}]bool{}, loaded: map[string]bool{}}
	go server.watchAuthorization()
	return server, nil
}
func (s *Server) loginEvent(code string) {
	if s.cfg.OnLoginEvent != nil {
		s.cfg.OnLoginEvent(code)
	}
}

func (s *Server) notifyLocked() {
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Caller holds op, so a version change cannot race a new turn or approval.
func (s *Server) ensureStarted(force bool) error {
	binary, err := s.cfg.Binary()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return errors.New("workbench closed")
	}
	if s.ready && (s.activeThread != "" || (!force && err == nil && s.binary == binary)) {
		s.mu.Unlock()
		return nil
	}
	if !force && !s.ready && binary == s.binary && time.Now().Before(s.retryAfter) {
		e := errors.New(s.failure)
		s.mu.Unlock()
		return e
	}
	old := s.client
	previousFailure := s.failure
	s.generation++
	gen := s.generation
	s.resetLocked()
	s.binary = binary
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	var client *codex.Client
	if err == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		defer cancel()
		client, err = codex.Start(ctx, binary, s.cfg.CodexHome, s.cfg.ClientVersion, s.cfg.Env, io.Discard,
			func(m codex.Message) { s.event(gen, m) }, func(e error) { s.exited(gen, e) })
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && !client.Alive() {
		err = errors.New("app-server exited during initialization")
	}
	if err != nil {
		s.failure = err.Error()
		s.retryAfter = time.Now().Add(3 * time.Second)
		if s.failure != previousFailure || force {
			s.loginEvent("app_server_start_failed")
		}
	} else {
		s.client = client
		s.ready = true
		s.failure = ""
		s.loginEvent("app_server_started")
	}
	s.notifyLocked()
	return err
}

func (s *Server) resetLocked() {
	s.client = nil
	s.ready = false
	s.loaded = map[string]bool{}
	s.activeThread, s.activeTurn = "", ""
	s.activeRequest = nil
	s.approvals = map[string]Approval{}
	s.clearPendingLoginLocked()
	s.earlyLoginResults = nil
}

func (s *Server) Close() {
	s.cancel()
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	old := s.client
	s.generation++
	s.resetLocked()
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (s *Server) exited(gen uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.generation {
		return
	}
	s.loginEvent("app_server_exited")
	s.resetLocked()
	if err != nil {
		s.failure = err.Error()
	}
	s.notifyLocked()
}
func (s *Server) rpc(method string, params, out any) error {
	s.mu.Lock()
	c := s.client
	ready := s.ready
	s.mu.Unlock()
	if c == nil || !ready {
		return errors.New("Codex is not ready")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	return c.Call(ctx, method, params, out)
}
func str(m map[string]any, key string) string { v, _ := m[key].(string); return v }
func (s *Server) event(gen uint64, m codex.Message) {
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	s.mu.Lock()
	if gen != s.generation {
		s.mu.Unlock()
		return
	}
	threadID := str(p, "threadId")
	if m.Method == "account/login/completed" {
		id := str(p, "loginId")
		success, ok := p["success"].(bool)
		if s.earlyLoginResults != nil && id != "" && ok {
			s.earlyLoginResults[id] = success
		}
		if ok && s.pendingLogin != nil && id == s.pendingLogin.LoginID {
			s.recordLoginResult(s.pendingLogin.Type, success)
			s.clearPendingLoginLocked()
		}
	}

	if len(m.ID) > 0 {
		if threadID != "" && threadID == s.activeThread && len(s.approvals) < 100 && len(m.Params) <= 256*1024 && (m.Method == "item/commandExecution/requestApproval" || m.Method == "item/fileChange/requestApproval" || m.Method == "item/tool/requestUserInput" || m.Method == "item/permissions/requestApproval") {
			id := newID()
			s.approvals[id] = Approval{ID: id, Method: m.Method, Params: m.Params, RawID: m.ID}
		} else {
			c := s.client
			if c != nil {
				go c.Reject(m.ID)
			}
		}
	}
	if m.Method == "turn/started" && threadID != "" && threadID == s.activeThread {
		if turn, ok := p["turn"].(map[string]any); ok {
			s.activeThread = threadID
			s.activeTurn = str(turn, "id")
		}
	}
	if m.Method == "turn/completed" && threadID != "" {
		if turn, _ := p["turn"].(map[string]any); s.activeThread == threadID && (s.activeTurn == "" || str(turn, "id") == s.activeTurn) {
			s.activeRequest = nil
			s.activeThread = ""
			s.activeTurn = ""
		}
		for id, a := range s.approvals {
			var ap map[string]any
			_ = json.Unmarshal(a.Params, &ap)
			if str(ap, "threadId") == threadID {
				delete(s.approvals, id)
			}
		}
	}
	if m.Method == "serverRequest/resolved" {
		rid, _ := json.Marshal(p["requestId"])
		for id, a := range s.approvals {
			if string(rid) == string(a.RawID) {
				delete(s.approvals, id)
			}
		}
	}
	if threadID != "" && threadID == s.activeThread && (m.Method == "item/started" || m.Method == "item/completed") {
		if item, ok := p["item"].(map[string]any); ok {
			if len(m.Params) > 128*1024 {
				item = map[string]any{"id": str(item, "id"), "type": str(item, "type"), "status": item["status"], "text": tail(str(item, "text"), 64*1024), "aggregatedOutput": tail(str(item, "aggregatedOutput"), 64*1024), "truncated": true}
			}
			s.upsertLocked(threadID, item)
		}
	}
	if threadID != "" && threadID == s.activeThread && (m.Method == "item/agentMessage/delta" || m.Method == "item/commandExecution/outputDelta") {
		id := str(p, "itemId")
		items := s.live[threadID]
		found := false
		for _, item := range items {
			if str(item, "id") == id {
				field := "text"
				if m.Method == "item/commandExecution/outputDelta" {
					field = "aggregatedOutput"
				}
				v := str(item, field) + str(p, "delta")
				item[field] = tail(v, 64*1024)
				found = true
				break
			}
		}
		if !found && m.Method == "item/agentMessage/delta" {
			s.upsertLocked(threadID, map[string]any{"id": id, "type": "agentMessage", "text": tail(str(p, "delta"), 64*1024)})
		}
	}
	if m.Method == "turn/completed" && threadID != "" {
		if turn, ok := p["turn"].(map[string]any); ok {
			s.upsertLocked(threadID, map[string]any{"id": "status-" + str(turn, "id"), "type": "turnStatus", "status": turn["status"], "error": turn["error"]})
		}
	}
	s.notifyLocked()
	s.mu.Unlock()
}
func tail(v string, n int) string {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}
func (s *Server) upsertLocked(id string, item map[string]any) {
	items := s.live[id]
	for i, old := range items {
		if str(old, "id") == str(item, "id") {
			items[i] = item
			s.live[id] = items
			return
		}
	}
	items = append(items, item)
	if len(items) > 256 {
		items = items[len(items)-256:]
	}
	s.live[id] = items
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "detail": detail, "error": detail})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "err_request", err)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		fail(w, 400, "err_request", errors.New("single JSON body required"))
		return false
	}
	return true
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" && path == "/api/events" {
		s.events(w, r)
		return
	}
	// Mutation serialization prevents competing turns, login changes and restarts.
	s.op.Lock()
	defer s.op.Unlock()
	switch path {
	case "/api/status", "/api/account", "/api/models", "/api/account/login", "/api/account/logout", "/api/threads", "/api/thread", "/api/turn":
		if err := s.ensureStarted(false); err != nil && path != "/api/status" && !(path == "/api/threads" && r.Method == http.MethodGet) {
			fail(w, 503, "err_codex", err)
			return
		}
	}
	switch {
	case path == "/api/status" && r.Method == "GET":
		s.mu.Lock()
		status := map[string]any{"ready": s.ready, "error": s.failure, "activeThread": s.activeThread, "activeTurn": s.activeTurn}
		s.mu.Unlock()
		writeJSON(w, status)
	case path == "/api/restart" && r.Method == "POST":
		if s.busy(w) {
			return
		}
		if err := s.ensureStarted(true); err != nil {
			fail(w, 503, "err_codex", err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "/api/account" && r.Method == "GET":
		s.account(w)
	case path == "/api/models" && r.Method == "GET":
		s.callHTTP(w, "model/list", map[string]any{})
	case path == "/api/account/login" && r.Method == "POST":
		if s.busy(w) {
			return
		}
		var b struct {
			Type   string `json:"type"`
			APIKey string `json:"apiKey"`
		}
		if !decode(w, r, &b) {
			return
		}
		p := map[string]any{"type": b.Type}
		if b.Type == "apiKey" && strings.TrimSpace(b.APIKey) != "" {
			p["apiKey"] = strings.TrimSpace(b.APIKey)
		} else if (b.Type != "chatgptDeviceCode" && b.Type != "chatgpt") || b.APIKey != "" {
			fail(w, 400, "err_request", nil)
			return
		}
		s.login(w, p)
	case path == "/api/account/login/callback" && r.Method == "POST":
		if !s.busy(w) {
			s.browserCallback(w, r)
		}
	case path == "/api/account/login/cancel" && r.Method == "POST":
		s.cancelLogin(w, r)
	case path == "/api/account/logout" && r.Method == "POST":
		if !s.busy(w) {
			s.mu.Lock()
			s.clearPendingLoginLocked()
			s.mu.Unlock()
			s.callHTTP(w, "account/logout", nil)
		}
	case path == "/api/roots" && r.Method == "GET":
		roots, err := s.cfg.Roots(r)
		if err != nil {
			fail(w, 502, "err_roots", err)
			return
		}
		writeJSON(w, roots)
	case path == "/api/projects" && r.Method == "GET":
		// The op lock serializes changes to the project and thread directory.
		writeJSON(w, s.state.Projects)
	case path == "/api/projects" && r.Method == "POST":
		s.addProject(w, r)
	case path == "/api/projects/remove" && r.Method == "POST":
		s.removeProject(w, r)
	case path == "/api/threads" && r.Method == "GET":
		writeJSON(w, s.state.Threads)
	case path == "/api/threads" && r.Method == "POST":
		s.newThread(w, r)
	case path == "/api/thread" && r.Method == "GET":
		s.readThread(w, r)
	case path == "/api/turn" && r.Method == "POST":
		s.turn(w, r)
	case path == "/api/interrupt" && r.Method == "POST":
		s.mu.Lock()
		thread, turn := s.activeThread, s.activeTurn
		s.mu.Unlock()
		if turn == "" {
			fail(w, 409, "err_idle", nil)
			return
		}
		s.callHTTP(w, "turn/interrupt", map[string]string{"threadId": thread, "turnId": turn})
	case path == "/api/approvals" && r.Method == "GET":
		s.mu.Lock()
		thread := s.activeThread
		a := make([]Approval, 0, len(s.approvals))
		for _, v := range s.approvals {
			a = append(a, v)
		}
		s.mu.Unlock()
		if len(a) > 0 {
			if _, _, err := s.thread(r, thread); err != nil {
				fail(w, 403, "err_path", err)
				return
			}
		}
		writeJSON(w, a)
	case path == "/api/approvals" && r.Method == "POST":
		s.approve(w, r)
	default:
		fail(w, 404, "err_not_found", nil)
	}
}
func (s *Server) busy(w http.ResponseWriter) bool {
	s.mu.Lock()
	busy := s.activeThread != ""
	s.mu.Unlock()
	if busy {
		fail(w, 409, "err_busy", nil)
	}
	return busy
}
func (s *Server) callHTTP(w http.ResponseWriter, method string, p any) {
	var out json.RawMessage
	if err := s.rpc(method, p, &out); err != nil {
		fail(w, 502, "err_codex", err)
		return
	}
	writeJSON(w, out)
}
func (s *Server) account(w http.ResponseWriter) {
	var out struct {
		Account *struct {
			Type     string `json:"type"`
			Email    string `json:"email,omitempty"`
			PlanType string `json:"planType,omitempty"`
		} `json:"account"`
		RequiresOpenaiAuth bool          `json:"requiresOpenaiAuth"`
		PendingLogin       *loginAttempt `json:"pendingLogin,omitempty"`
		LoginError         string        `json:"loginError,omitempty"`
	}
	if err := s.rpc("account/read", map[string]bool{"refreshToken": false}, &out); err != nil {
		fail(w, 502, "err_codex", err)
		return
	}
	s.mu.Lock()
	out.PendingLogin = s.pendingLogin
	if s.failure == deviceLoginFailed {
		out.LoginError = "err_device_login_failed"
	}
	if s.failure == browserLoginFailed {
		out.LoginError = "err_browser_login"
	}
	s.mu.Unlock()
	writeJSON(w, out)
}
func (s *Server) login(w http.ResponseWriter, p map[string]any) {
	browser := p["type"] == "chatgpt"
	if browser {
		browserPortMu.Lock()
		defer browserPortMu.Unlock()
		s.mu.Lock()
		pending := s.pendingLogin
		s.mu.Unlock()
		if pending != nil && pending.Type == "chatgpt" && time.Now().Before(pending.expires) {
			writeJSON(w, pending)
			return
		}
		// Codex sends GET /cancel to an occupied port. An existing listener
		// may belong to another fnOS account or a standalone CLI session.
		if owner := browserOwner.Load(); (owner != nil && time.Now().Before(owner.expires)) || !browserPortFree() {
			s.loginEvent("browser_auth_failed")
			fail(w, 409, "err_browser_login_busy", nil)
			return
		}
	}
	keyLogin := p["type"] == "apiKey"
	if !keyLogin {
		s.mu.Lock()
		s.earlyLoginResults = map[string]bool{}
		s.mu.Unlock()
	}
	var out loginAttempt
	err := s.rpc("account/login/start", p, &out)
	if err == nil && browser {
		_, _, valid := parseBrowserGrant(out.AuthURL)
		if out.Type != "chatgpt" || out.LoginID == "" || !valid {
			if out.LoginID != "" {
				_ = s.rpc("account/login/cancel", map[string]string{"loginId": out.LoginID}, nil)
			}
			err = errors.New("invalid browser login from Codex")
		}
		out.expires = time.Now().Add(10 * time.Minute)
	}
	s.mu.Lock()
	success, alreadyCompleted := s.earlyLoginResults[out.LoginID]
	s.earlyLoginResults = nil
	if err != nil {
		s.mu.Unlock()
		var rpcErr *codex.RPCError
		switch {
		case keyLogin:
			s.loginEvent("api_key_failed")
			// Upstream errors can quote the request, including the submitted key.
			fail(w, 502, "err_api_key_login", errors.New("API key sign-in failed"))
		case browser:
			s.loginEvent("browser_auth_failed")
			fail(w, 502, "err_browser_login", nil)
		case errors.As(err, &rpcErr) && strings.Contains(rpcErr.Message, "device code request failed with status 403"):
			s.loginEvent("device_code_forbidden")
			fail(w, 502, "err_device_code_forbidden", errors.New("Codex device code request returned HTTP 403"))
		default:
			s.loginEvent("device_code_failed")
			fail(w, 502, "err_codex", err)
		}
		return
	}
	if s.failure == deviceLoginFailed || s.failure == browserLoginFailed {
		s.failure = ""
	}
	s.clearPendingLoginLocked()
	if !keyLogin && (browser || out.Type == "chatgptDeviceCode") {
		if !alreadyCompleted {
			s.pendingLogin = &out
			if browser {
				browserOwner.Store(&out)
				out.timer = time.AfterFunc(time.Until(out.expires), func() { s.expireBrowserLogin(&out) })
			}
		} else {
			s.recordLoginResult(out.Type, success)
		}
	}
	s.notifyLocked()
	s.mu.Unlock()
	if keyLogin {
		// Only the account type belongs in the API key login response.
		s.loginEvent("api_key_saved")
		writeJSON(w, loginAttempt{Type: "apiKey"})
		return
	}
	if browser {
		s.loginEvent("browser_auth_started")
	} else {
		s.loginEvent("device_code_issued")
	}
	writeJSON(w, out)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[ch] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subscribers, ch); s.mu.Unlock() }()
	_, _ = io.WriteString(w, "data: refresh\n\n")
	f.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-r.Context().Done():
			return
		case <-ch:
			_, _ = io.WriteString(w, "data: refresh\n\n")
			f.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			f.Flush()
		}
	}
}
func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if !decode(w, r, &b) {
		return
	}
	roots, err := s.cfg.Roots(r)
	if err != nil {
		fail(w, 502, "err_roots", err)
		return
	}
	path, err := allowedPath(b.Path, roots)
	if err != nil {
		fail(w, 403, "err_path", err)
		return
	}
	if b.Name == "" {
		b.Name = filepath.Base(path)
	}
	if len(b.Name) > 128 {
		fail(w, 400, "err_request", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.state.Projects {
		if p.Path == path {
			writeJSON(w, p)
			return
		}
	}
	p := Project{ID: newID(), Name: b.Name, Path: path}
	s.state.Projects = append(s.state.Projects, p)
	if err := saveState(s.cfg.DataDir, s.state); err != nil {
		s.state.Projects = s.state.Projects[:len(s.state.Projects)-1]
		fail(w, 500, "err_storage", err)
		return
	}
	writeJSON(w, p)
	s.notifyLocked()
}
func (s *Server) removeProject(w http.ResponseWriter, r *http.Request) {
	if s.busy(w) {
		return
	}
	var b struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &b) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.state.Projects
	next := []Project{}
	for _, p := range old {
		if p.ID != b.ID {
			next = append(next, p)
		}
	}
	s.state.Projects = next
	oldThreads := s.state.Threads
	s.state.Threads = slices.DeleteFunc(slices.Clone(oldThreads), func(t Thread) bool { return t.ProjectID == b.ID })
	if err := saveState(s.cfg.DataDir, s.state); err != nil {
		s.state.Projects = old
		s.state.Threads = oldThreads
		fail(w, 500, "err_storage", err)
		return
	}
	s.notifyLocked()
	writeJSON(w, map[string]bool{"ok": true})
}
func (s *Server) project(r *http.Request, id string) (Project, error) {
	s.mu.Lock()
	var p Project
	for _, v := range s.state.Projects {
		if v.ID == id {
			p = v
			break
		}
	}
	s.mu.Unlock()
	if p.ID == "" {
		return p, errors.New("unknown project")
	}
	roots, err := s.cfg.Roots(r)
	if err != nil {
		return p, err
	}
	path, err := allowedPath(p.Path, roots)
	if err != nil {
		return p, err
	}
	if path != p.Path {
		return p, errors.New("project path changed")
	}
	return p, nil
}
func (s *Server) thread(r *http.Request, id string) (Thread, Project, error) {
	s.mu.Lock()
	var t Thread
	for _, v := range s.state.Threads {
		if v.ID == id {
			t = v
			break
		}
	}
	s.mu.Unlock()
	if t.ID == "" {
		return t, Project{}, errors.New("unknown thread")
	}
	p, err := s.project(r, t.ProjectID)
	return t, p, err
}
func (s *Server) newThread(w http.ResponseWriter, r *http.Request) {
	if s.busy(w) {
		return
	}
	var b struct {
		ProjectID string `json:"projectId"`
		Model     string `json:"model"`
	}
	if !decode(w, r, &b) {
		return
	}
	p, err := s.project(r, b.ProjectID)
	if err != nil {
		fail(w, 403, "err_path", err)
		return
	}
	params := map[string]any{"cwd": p.Path, "approvalPolicy": "on-request", "sandbox": "workspace-write"}
	if b.Model != "" {
		params["model"] = b.Model
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.rpc("thread/start", params, &out); err != nil {
		fail(w, 502, "err_codex", err)
		return
	}
	if out.Thread.ID == "" {
		fail(w, 502, "err_codex", errors.New("missing thread ID"))
		return
	}
	t := Thread{ID: out.Thread.ID, ProjectID: p.ID, Title: p.Name, Created: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded[t.ID] = true
	s.state.Threads = append(s.state.Threads, t)
	if err := saveState(s.cfg.DataDir, s.state); err != nil {
		s.state.Threads = s.state.Threads[:len(s.state.Threads)-1]
		fail(w, 500, "err_storage", err)
		return
	}
	writeJSON(w, t)
	s.notifyLocked()
}
func (s *Server) readThread(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	_, project, err := s.thread(r, id)
	if err != nil {
		fail(w, 403, "err_path", err)
		return
	}
	params := map[string]any{"threadId": id, "limit": 50, "sortDirection": "desc", "itemsView": "full"}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		params["cursor"] = cursor
	}
	type historyTurn struct {
		Items  []map[string]any `json:"items"`
		Status string           `json:"status"`
		ID     string           `json:"id"`
		Error  any              `json:"error"`
	}
	var history struct {
		Data       []historyTurn `json:"data"`
		NextCursor any           `json:"nextCursor"`
	}
	err = s.rpc("thread/turns/list", params, &history)
	var historyErr *codex.RPCError
	if errors.As(err, &historyErr) && historyErr.Code == -32601 && r.URL.Query().Get("cursor") == "" {
		// Older app-servers expose turn history only through thread/read.
		var old struct {
			Thread struct {
				Turns []historyTurn `json:"turns"`
			} `json:"thread"`
		}
		err = s.rpc("thread/read", map[string]any{"threadId": id, "includeTurns": true}, &old)
		if errors.As(err, &historyErr) && historyErr.Code == -32601 {
			s.mu.Lock()
			active := s.activeThread
			knownEmpty := s.loaded[id]
			for _, receipt := range s.state.Receipts {
				if receipt.ThreadID == id {
					knownEmpty = false
					break
				}
			}
			s.mu.Unlock()
			switch {
			case active == id || knownEmpty:
				// A newly created thread can have neither RPC history nor
				// notifications yet. While running, the live items fill in below.
				err = nil
			case active == "":
				// Older CLIs can reconstruct past turns from their rollout on
				// resume even when their paginated history store is unavailable.
				err = s.rpc("thread/resume", map[string]any{"threadId": id, "excludeTurns": false, "cwd": project.Path, "sandbox": "workspace-write", "approvalPolicy": "on-request"}, &old)
				if err == nil {
					s.mu.Lock()
					s.loaded[id] = true
					s.mu.Unlock()
				}
			}
		}
		history.Data = old.Thread.Turns
		slices.Reverse(history.Data)
	}
	// Codex does not materialize a new thread until its first user message.
	if errors.As(err, &historyErr) && historyErr.Code == -32600 && strings.Contains(historyErr.Message, "is not materialized yet") {
		err = nil
	}
	items := []map[string]any{}
	if err == nil {
		for i := len(history.Data) - 1; i >= 0; i-- {
			turn := history.Data[i]
			items = append(items, turn.Items...)
			if turn.Status != "inProgress" {
				items = append(items, map[string]any{"id": "status-" + turn.ID, "type": "turnStatus", "status": turn.Status, "error": turn.Error})
			}
		}
	}
	s.mu.Lock()
	if err != nil {
		s.mu.Unlock()
		fail(w, 502, "err_codex", err)
		return
	}
	if r.URL.Query().Get("cursor") == "" {
		for _, live := range s.live[id] {
			found := false
			for i, item := range items {
				if str(item, "id") == str(live, "id") {
					items[i] = maps.Clone(live)
					found = true
					break
				}
			}
			if !found {
				items = append(items, maps.Clone(live))
			}
		}
	}
	active := s.activeThread == id
	s.mu.Unlock()
	writeJSON(w, map[string]any{"items": items, "nextCursor": history.NextCursor, "active": active})
}
func (s *Server) turn(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ThreadID  string `json:"threadId"`
		Text      string `json:"text"`
		RequestID string `json:"requestId"`
		Model     string `json:"model"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.RequestID) < 8 || len(b.RequestID) > 128 || strings.TrimSpace(b.Text) == "" || len(b.Text) > 128*1024 {
		fail(w, 400, "err_request", nil)
		return
	}
	_, p, err := s.thread(r, b.ThreadID)
	if err != nil {
		fail(w, 403, "err_path", err)
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(b.Model+"\x00"+b.Text)))
	s.mu.Lock()
	receipt, exists := s.state.Receipts[b.RequestID]
	s.mu.Unlock()
	if exists {
		if receipt.ThreadID != b.ThreadID || receipt.Digest != digest {
			fail(w, 409, "err_duplicate", nil)
		} else if receipt.Uncertain {
			fail(w, 409, "err_uncertain", nil)
		} else {
			writeJSON(w, receipt.Result)
		}
		return
	}
	if s.busy(w) {
		return
	}
	s.mu.Lock()
	loaded := s.loaded[b.ThreadID]
	s.mu.Unlock()
	if !loaded {
		if err := s.rpc("thread/resume", map[string]any{"threadId": b.ThreadID, "excludeTurns": true, "cwd": p.Path, "approvalPolicy": "on-request", "sandbox": "workspace-write"}, nil); err != nil {
			fail(w, 502, "err_codex", err)
			return
		}
		s.mu.Lock()
		s.loaded[b.ThreadID] = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.state.Receipts[b.RequestID] = Receipt{ThreadID: b.ThreadID, Digest: digest, Uncertain: true}
	err = saveState(s.cfg.DataDir, s.state)
	if err == nil {
		s.live = map[string][]map[string]any{}
		s.activeRequest = (&http.Request{}).WithContext(context.WithoutCancel(r.Context()))
		s.activeThread = b.ThreadID
		s.activeTurn = ""
	}
	if err != nil {
		delete(s.state.Receipts, b.RequestID)
	}
	s.mu.Unlock()
	if err != nil {
		fail(w, 500, "err_storage", err)
		return
	}
	params := map[string]any{"threadId": b.ThreadID, "input": []any{map[string]any{"type": "text", "text": b.Text}}, "cwd": p.Path, "approvalPolicy": "on-request", "sandboxPolicy": map[string]any{"type": "workspaceWrite", "writableRoots": []string{p.Path}, "networkAccess": false}}
	if b.Model != "" {
		params["model"] = b.Model
	}
	var out json.RawMessage
	err = s.rpc("turn/start", params, &out)
	if err != nil {
		var rpcErr *codex.RPCError
		definite := errors.As(err, &rpcErr)
		if !definite {
			s.mu.Lock()
			c := s.client
			s.mu.Unlock()
			if c != nil {
				c.Close()
			}
		}
		s.mu.Lock()
		s.activeThread = ""
		s.activeTurn = ""
		s.activeRequest = nil
		if definite {
			// The server explicitly rejected the request; it is safe to retry.
			delete(s.state.Receipts, b.RequestID)
			if saveErr := saveState(s.cfg.DataDir, s.state); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
		}
		s.notifyLocked()
		s.mu.Unlock()
		if definite {
			fail(w, 502, "err_codex", err)
		} else {
			fail(w, 502, "err_uncertain", err)
		}
		return
	}
	s.mu.Lock()
	if s.activeThread == b.ThreadID && s.activeTurn == "" {
		var started struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(out, &started) == nil {
			s.activeTurn = started.Turn.ID
		}
	}
	for i, t := range s.state.Threads {
		if t.ID == b.ThreadID && t.Title == p.Name {
			title := []rune(strings.Join(strings.Fields(b.Text), " "))
			s.state.Threads[i].Title = string(title[:min(len(title), 60)])
			break
		}
	}
	s.state.Receipts[b.RequestID] = Receipt{ThreadID: b.ThreadID, Digest: digest, Result: out}
	err = saveState(s.cfg.DataDir, s.state)
	s.notifyLocked()
	s.mu.Unlock()
	if err != nil {
		fail(w, 500, "err_storage", err)
		return
	}
	writeJSON(w, out)
}
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
		Answers  map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if !decode(w, r, &b) {
		return
	}
	s.mu.Lock()
	a, ok := s.approvals[b.ID]
	c := s.client
	s.mu.Unlock()
	if !ok || c == nil {
		fail(w, 409, "err_stale", nil)
		return
	}
	var p map[string]any
	_ = json.Unmarshal(a.Params, &p)
	if _, _, err := s.thread(r, str(p, "threadId")); err != nil {
		fail(w, 403, "err_path", err)
		return
	}
	var result any
	switch a.Method {
	case "item/tool/requestUserInput":
		if len(b.Answers) == 0 {
			fail(w, 400, "err_request", nil)
			return
		}
		result = map[string]any{"answers": b.Answers}
	case "item/permissions/requestApproval":
		var err error
		result, err = permissionDecision(a.Params, b.Decision)
		if err != nil {
			fail(w, 400, "err_request", err)
			return
		}
	default:
		if b.Decision != "accept" && b.Decision != "decline" && b.Decision != "cancel" {
			fail(w, 400, "err_request", nil)
			return
		}
		if choices, ok := p["availableDecisions"].([]any); ok && len(choices) > 0 {
			found := false
			for _, choice := range choices {
				if choice == b.Decision {
					found = true
				}
			}
			if !found {
				fail(w, 400, "err_request", nil)
				return
			}
		}
		result = map[string]string{"decision": b.Decision}
	}
	if err := c.Reply(a.RawID, result); err != nil {
		fail(w, 502, "err_codex", err)
		return
	}
	s.mu.Lock()
	delete(s.approvals, b.ID)
	s.notifyLocked()
	s.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// Live directory grants can be revoked while the browser is disconnected.
func (s *Server) watchAuthorization() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		s.op.Lock()
		s.checkActiveGrant()
		s.op.Unlock()
	}
}
func (s *Server) checkActiveGrant() {
	s.mu.Lock()
	thread, turn, req, c := s.activeThread, s.activeTurn, s.activeRequest, s.client
	s.mu.Unlock()
	if req == nil || thread == "" || c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	if _, _, err := s.thread(req.WithContext(ctx), thread); err == nil {
		return
	}
	if turn == "" || s.rpc("turn/interrupt", map[string]string{"threadId": thread, "turnId": turn}, nil) != nil {
		c.Close()
	}
	s.mu.Lock()
	s.failure = "Task interrupted: project authorization is unavailable"
	s.notifyLocked()
	s.mu.Unlock()
}
