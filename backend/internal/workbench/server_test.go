package workbench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"time"

	"fnos-codex/backend/internal/codex"
)

// TestMain doubles as a deterministic child-process protocol fixture. It never
// calls a model, uses credentials or executes commands requested by a prompt.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "app-server" {
		fakeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fakeServer() {
	scanner := bufio.NewScanner(os.Stdin)
	materialized := false
	var account map[string]string
	legacyHistory := os.Getenv("CODEX_FIXTURE_LEGACY_HISTORY") == "1"
	rollout := filepath.Join(os.Getenv("CODEX_HOME"), "fixture-rollout")
	var outputMu sync.Mutex
	send := func(v any) { outputMu.Lock(); _ = json.NewEncoder(os.Stdout).Encode(v); outputMu.Unlock() }
	for scanner.Scan() {
		var v map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &v)
		method, _ := v["method"].(string)
		p, _ := v["params"].(map[string]any)
		id := v["id"]
		if method == "" {
			send(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"requestId": id, "threadId": "t1"}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "t1", "turn": map[string]any{"id": "turn1", "status": "completed"}}})
			continue
		}
		if method == "initialized" {
			continue
		}
		result := any(map[string]any{})
		switch method {
		case "initialize":
			result = map[string]string{"userAgent": "fixture"}
		case "account/read":
			result = map[string]any{"account": account, "accessToken": "fixture-secret"}
		case "account/login/start":
			if p["type"] == "chatgpt" {
				result = map[string]string{"type": "chatgpt", "loginId": "browser1", "authUrl": "https://auth.openai.com/oauth/authorize?state=fixture-state&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback"}
				break
			}
			if p["type"] == "apiKey" {
				if p["apiKey"] != "fixture-api-key" {
					send(map[string]any{"id": id, "error": map[string]any{"code": -32603, "message": fmt.Sprintf("unusable key %v", p["apiKey"])}})
					continue
				}
				account = map[string]string{"type": "apiKey"}
				result = map[string]string{"type": "apiKey", "apiKey": "fixture-api-key"}
				break
			}
			if message := os.Getenv("CODEX_FIXTURE_DEVICE_ERROR"); message != "" {
				send(map[string]any{"id": id, "error": map[string]any{"code": -32603, "message": message}})
				continue
			}
			if os.Getenv("CODEX_FIXTURE_LOGIN_COMPLETED_EARLY") == "1" {
				send(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": "login1", "success": false}})
			}
			result = map[string]string{"type": "chatgptDeviceCode", "loginId": "login1", "userCode": "ABCD", "verificationUrl": "https://auth.openai.com/codex/device", "accessToken": "fixture-secret"}
		case "account/login/cancel":
			result = map[string]string{"status": "canceled"}
		case "model/list":
			result = map[string]any{"data": []any{}}
		case "thread/resume":
			if legacyHistory {
				text, err := os.ReadFile(rollout)
				if err == nil && p["excludeTurns"] == false && p["cwd"] != "" && p["sandbox"] == "workspace-write" && p["approvalPolicy"] == "on-request" {
					result = map[string]any{"thread": map[string]any{"id": "t1", "turns": []any{map[string]any{"id": "restored", "status": "completed", "items": []any{map[string]string{"type": "userMessage", "id": "restored-item", "text": string(text)}}}}}}
					break
				}
			}
			if !materialized {
				send(map[string]any{"id": id, "error": map[string]any{"code": -32600, "message": "thread is not materialized yet"}})
				continue
			}
			result = map[string]any{"thread": map[string]any{"id": "t1"}}
		case "thread/start":
			if p["approvalPolicy"] != "on-request" || p["sandbox"] != "workspace-write" {
				os.Exit(12)
			}
			result = map[string]any{"thread": map[string]any{"id": "t1"}}
		case "thread/turns/list":
			send(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "list_turns is not supported yet"}})
			continue
		case "thread/read":
			if p["includeTurns"] != true {
				os.Exit(13)
			}
			if legacyHistory {
				send(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "list_turns is not supported yet"}})
				continue
			}
			if !materialized {
				send(map[string]any{"id": id, "error": map[string]any{"code": -32600, "message": "thread is not materialized yet"}})
				continue
			}
			result = map[string]any{"thread": map[string]any{"turns": []any{map[string]any{"id": "earlier", "status": "completed", "items": []any{map[string]string{"id": "hi1", "type": "userMessage", "text": "persisted history"}}}}}}
		case "turn/start":
			sandbox, _ := p["sandboxPolicy"].(map[string]any)
			if p["approvalPolicy"] != "on-request" || sandbox["type"] != "workspaceWrite" || sandbox["networkAccess"] != false {
				os.Exit(11)
			}
			materialized = true
			text := p["input"].([]any)[0].(map[string]any)["text"].(string)
			if legacyHistory {
				if err := os.WriteFile(rollout, []byte(text), 0600); err != nil {
					os.Exit(14)
				}
			}
			if text == "crash" {
				os.Exit(7)
			}
			send(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "t1", "turn": map[string]any{"id": "turn1", "status": "inProgress"}}})
			send(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "t1", "item": map[string]any{"id": "i1", "type": "agentMessage", "text": ""}}})
			send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "t1", "itemId": "i1", "delta": "fixture response"}})
			send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn1"}}})
			send(map[string]any{"id": 17, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "t1", "turnId": "turn1", "itemId": "cmd1", "command": "echo fixture", "availableDecisions": []string{"accept", "decline"}}})
			continue
		case "turn/interrupt":
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "t1", "turn": map[string]any{"id": "turn1", "status": "interrupted"}}})
		}
		send(map[string]any{"id": id, "result": result})
	}
}
func setup(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	data := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: data, CodexHome: filepath.Join(data, ".codex"), Binary: func() (string, error) { return binary, nil }, Roots: func(*http.Request) ([]string, error) { return []string{root}, nil }, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, root
}
func call(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &b)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}
func projectThread(t *testing.T, s *Server, root string) string {
	t.Helper()
	r := call(t, s, "POST", "/api/projects", map[string]string{"path": root})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var p Project
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	r = call(t, s, "POST", "/api/threads", map[string]string{"projectId": p.ID})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	return "t1"
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not settle")
}
func TestApprovalRefreshDuplicateAndStale(t *testing.T) {
	s, root := setup(t)
	id := projectThread(t, s, root)
	body := map[string]string{"threadId": id, "text": "approval", "requestId": "request-123"}
	first := call(t, s, "POST", "/api/turn", body)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.approvals) == 1 })
	duplicate := call(t, s, "POST", "/api/turn", body)
	if duplicate.Code != 200 || duplicate.Body.String() != first.Body.String() {
		t.Fatal("idempotency failed", duplicate.Body.String())
	}
	r := call(t, s, "GET", "/api/approvals", nil)
	var a []Approval
	_ = json.Unmarshal(r.Body.Bytes(), &a)
	if len(a) != 1 {
		t.Fatal("approval not restored")
	}
	history := call(t, s, "GET", "/api/thread?id=t1", nil)
	if !strings.Contains(history.Body.String(), "fixture response") || !strings.Contains(history.Body.String(), "persisted history") {
		t.Fatal("stream snapshot lost", history.Body.String())
	}
	approve := map[string]string{"id": a[0].ID, "decision": "decline"}
	r = call(t, s, "POST", "/api/approvals", approve)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = call(t, s, "POST", "/api/approvals", approve); r.Code != 409 {
		t.Fatal("stale approval accepted")
	}
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.activeThread == "" })
	stored, err := loadState(s.cfg.DataDir)
	if err != nil || len(stored.Threads) != 1 || stored.Receipts["request-123"].Uncertain {
		t.Fatal("persistence failed", err)
	}
}

func TestDeviceLoginRestoredWithoutCredentials(t *testing.T) {
	s, _ := setup(t)
	login := call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgptDeviceCode"})
	if login.Code != 200 || !strings.Contains(login.Body.String(), "ABCD") || strings.Contains(login.Body.String(), "fixture-secret") {
		t.Fatal(login.Body.String())
	}
	account := call(t, s, "GET", "/api/account", nil)
	if account.Code != 200 || !strings.Contains(account.Body.String(), "ABCD") || strings.Contains(account.Body.String(), "fixture-secret") {
		t.Fatal(account.Body.String())
	}
}

func TestDeviceCodeForbiddenThenAPIKeyLogin(t *testing.T) {
	t.Setenv("CODEX_FIXTURE_DEVICE_ERROR", "failed to request device code: device code request failed with status 403 Forbidden")
	s, _ := setup(t)
	device := call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgptDeviceCode"})
	if device.Code != 502 || !strings.Contains(device.Body.String(), `"code":"err_device_code_forbidden"`) {
		t.Fatal(device.Code, device.Body.String())
	}
	for _, key := range []string{"", " \t "} {
		invalid := call(t, s, "POST", "/api/account/login", map[string]string{"type": "apiKey", "apiKey": key})
		if invalid.Code != 400 {
			t.Fatal("accepted empty API key", invalid.Code)
		}
	}
	invalid := call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgptDeviceCode", "apiKey": "unexpected-secret"})
	if invalid.Code != 400 || strings.Contains(invalid.Body.String(), "unexpected-secret") {
		t.Fatal("unexpected key for device login")
	}
	rejected := call(t, s, "POST", "/api/account/login", map[string]string{"type": "apiKey", "apiKey": "rejected-secret"})
	if rejected.Code != 502 || !strings.Contains(rejected.Body.String(), `"code":"err_api_key_login"`) || strings.Contains(rejected.Body.String(), "rejected-secret") {
		t.Fatal("API key RPC error was not masked")
	}
	ok := call(t, s, "POST", "/api/account/login", map[string]string{"type": "apiKey", "apiKey": "fixture-api-key"})
	if ok.Code != 200 || strings.TrimSpace(ok.Body.String()) != `{"type":"apiKey"}` {
		t.Fatal("API key login did not return the public account type")
	}
	status := call(t, s, "GET", "/api/account", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"account":{"type":"apiKey"}`) || strings.Contains(status.Body.String(), "fixture-") || strings.Contains(status.Body.String(), "pendingLogin") {
		t.Fatal("unexpected account state after API key login")
	}
}

func TestDeviceCodeOtherErrorsRemainDiagnostic(t *testing.T) {
	t.Setenv("CODEX_FIXTURE_DEVICE_ERROR", "device code request failed with status 503 Service Unavailable")
	s, _ := setup(t)
	r := call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgptDeviceCode"})
	if r.Code != 502 || !strings.Contains(r.Body.String(), `"code":"err_codex"`) || !strings.Contains(r.Body.String(), "503 Service Unavailable") {
		t.Fatal(r.Code, r.Body.String())
	}
}

func TestAPIKeyLoginClearsEarlierDeviceFailure(t *testing.T) {
	t.Setenv("CODEX_FIXTURE_LOGIN_COMPLETED_EARLY", "1")
	s, _ := setup(t)
	call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgptDeviceCode"})
	account := call(t, s, "GET", "/api/account", nil)
	status := call(t, s, "GET", "/api/status", nil)
	if strings.Contains(account.Body.String(), "pendingLogin") || !strings.Contains(status.Body.String(), deviceLoginFailed) {
		t.Fatal("early device failure was lost")
	}
	call(t, s, "POST", "/api/account/login", map[string]string{"type": "apiKey", "apiKey": "fixture-api-key"})
	// A late cancellation of the replaced device flow must not undo the success.
	s.event(s.generation, codex.Message{Method: "account/login/completed", Params: json.RawMessage(`{"loginId":"login1","success":false}`)})
	status = call(t, s, "GET", "/api/status", nil)
	if !strings.Contains(status.Body.String(), `"error":""`) {
		t.Fatal(status.Body.String())
	}
}

func TestOneTurnPermissionsOnly(t *testing.T) {
	raw := json.RawMessage(`{"threadId":"t1","permissions":{"network":{"enabled":true},"fileSystem":null}}`)
	for _, tc := range []struct{ decision, want string }{{"accept", `{"permissions":{"network":{"enabled":true}},"scope":"turn"}`}, {"decline", `{"permissions":{},"scope":"turn"}`}} {
		reply, err := permissionDecision(raw, tc.decision)
		data, _ := json.Marshal(reply)
		if err != nil || string(data) != tc.want {
			t.Fatal(err, string(data))
		}
	}
	for _, raw := range []string{`{"permissions":{"systemAdmin":true}}`, `{"permissions":{"network":{"enabled":true,"unknown":true}}}`, `{"permissions":{"fileSystem":{"entries":[{"access":"write","path":{"type":"future","path":"/"}}]}}}`} {
		if _, err := permissionDecision(json.RawMessage(raw), "accept"); err == nil {
			t.Fatalf("unknown permission accepted: %s", raw)
		}
	}
	if _, err := permissionDecision(raw, "acceptForSession"); err == nil {
		t.Fatal("session grant accepted")
	}
}
func TestCrashClearsStateAndBlocksRetry(t *testing.T) {
	s, root := setup(t)
	projectThread(t, s, root)
	body := map[string]string{"threadId": "t1", "text": "crash", "requestId": "request-crash"}
	r := call(t, s, "POST", "/api/turn", body)
	if r.Code != 502 {
		t.Fatal(r.Code)
	}
	eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.ready && s.activeThread == "" && len(s.approvals) == 0
	})
	r = call(t, s, "POST", "/api/turn", body)
	if r.Code != 409 {
		t.Fatal("uncertain request repeated")
	}
}
func TestSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := allowedPath(link, []string{root}); err == nil {
		t.Fatal("symlink escaped root")
	}
}
func TestBusyInterruptAndRevokedProject(t *testing.T) {
	s, root := setup(t)
	projectThread(t, s, root)
	body := map[string]string{"threadId": "t1", "text": "approval", "requestId": "busy-one"}
	if r := call(t, s, "POST", "/api/turn", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	body["requestId"] = "busy-two"
	if r := call(t, s, "POST", "/api/turn", body); r.Code != 409 {
		t.Fatal("concurrent turn accepted")
	}
	if r := call(t, s, "POST", "/api/account/logout", map[string]any{}); r.Code != 409 {
		t.Fatal("account switched during turn")
	}
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.activeTurn != "" })
	if r := call(t, s, "POST", "/api/interrupt", map[string]any{}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.activeThread == "" })
	s.op.Lock()
	s.cfg.Roots = func(*http.Request) ([]string, error) { return nil, nil }
	s.op.Unlock()
	if r := call(t, s, "POST", "/api/turn", body); r.Code != 403 {
		t.Fatal("revoked root accepted")
	}
}
func TestJSONRejectsTrailingData(t *testing.T) {
	s, _ := setup(t)
	r := httptest.NewRequest("POST", "/api/projects", strings.NewReader(`{"path":"/"} {}`))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(fmt.Sprint(w.Code))
	}
}

func TestNewThreadHistoryBeforeMaterialization(t *testing.T) {
	s, root := setup(t)
	projectThread(t, s, root)
	r := call(t, s, "GET", "/api/thread?id=t1", nil)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var out struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || len(out.Items) != 0 {
		t.Fatal("expected empty history", err)
	}
}

func TestLegacyHistoryWhileActiveAndAfterRestart(t *testing.T) {
	s, root := setup(t)
	s.cfg.Env = append(s.cfg.Env, "CODEX_FIXTURE_LEGACY_HISTORY=1")
	id := projectThread(t, s, root)
	read := func(s *Server, contains string) {
		t.Helper()
		r := call(t, s, "GET", "/api/thread?id="+id, nil)
		if r.Code != 200 || !strings.Contains(r.Body.String(), contains) {
			t.Fatalf("history %d: %s", r.Code, r.Body.String())
		}
	}
	read(s, `"items":[]`)
	body := map[string]string{"threadId": id, "text": "history-after-restart", "requestId": "legacy-turn-1"}
	if r := call(t, s, "POST", "/api/turn", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	read(s, "fixture response")
	if r := call(t, s, "POST", "/api/interrupt", nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s.Close()
	reopened, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read(reopened, "history-after-restart")
}

func TestMissingAndChangedRuntime(t *testing.T) {
	s, root := setup(t)
	binary, _ := s.cfg.Binary()
	now := binary
	s.cfg.Binary = func() (string, error) {
		if now == "" {
			return "", os.ErrNotExist
		}
		return now, nil
	}
	now = ""
	resp := call(t, s, "GET", "/api/status", nil)
	if !strings.Contains(resp.Body.String(), `"ready":false`) {
		t.Fatal(resp.Body.String())
	}
	now = binary
	projectThread(t, s, root)
	body := map[string]string{"threadId": "t1", "text": "approval", "requestId": "switch-1"}
	if r := call(t, s, "POST", "/api/turn", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	other := filepath.Join(t.TempDir(), filepath.Base(binary))
	bytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, bytes, 0755); err != nil {
		t.Fatal(err)
	}
	now = other
	resp = call(t, s, "GET", "/api/status", nil)
	s.mu.Lock()
	during := s.binary
	s.mu.Unlock()
	if during != binary {
		t.Fatal("switched during active turn")
	}
	if r := call(t, s, "POST", "/api/interrupt", nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.activeThread == "" })
	resp = call(t, s, "GET", "/api/status", nil)
	s.mu.Lock()
	current := s.binary
	s.mu.Unlock()
	if !strings.Contains(resp.Body.String(), `"ready":true`) || current != other {
		t.Fatal(resp.Body.String(), current)
	}
}
