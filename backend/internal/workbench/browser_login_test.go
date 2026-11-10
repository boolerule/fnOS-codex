package workbench

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Exercise the two official loopback requests, including the final /success
// request required by versions which keep serving after a local redirect.
func TestBrowserLoginRelay(t *testing.T) {
	if !browserPortFree() {
		t.Skip("Codex loopback login port is in use")
	}
	s, _ := setup(t)
	other, _ := setup(t)
	login := call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgpt"})
	if login.Code != 200 || !strings.Contains(login.Body.String(), "authUrl") {
		t.Fatal(login.Code, login.Body.String())
	}
	if !strings.Contains(call(t, s, "GET", "/api/account", nil).Body.String(), "browser1") {
		t.Fatal("pending login missing")
	}
	payload := map[string]string{"loginId": "browser1", "callbackUrl": "http://localhost:1455/auth/callback?code=test-secret&state=fixture-state&scope=ignored"}
	if got := call(t, other, "POST", "/api/account/login/callback", payload); got.Code != 409 {
		t.Fatal("cross-user callback", got.Code)
	}
	l, err := net.Listen("tcp4", "127.0.0.1:1455")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []string
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		if r.URL.Path == "/auth/callback" {
			w.Header().Set("Location", "http://localhost:1455/success?token=secret")
			w.WriteHeader(302)
		} else {
			io.WriteString(w, "test secret page")
		}
	})}
	go server.Serve(l)
	defer server.Close()
	if r := call(t, other, "POST", "/api/account/login", map[string]string{"type": "chatgpt"}); r.Code != 409 {
		t.Fatal("would cancel another user's login", r.Code)
	}
	for _, raw := range []string{"http://127.0.0.1:80/auth/callback?state=fixture-state&code=test-secret", "http://example.org:1455/auth/callback?state=fixture-state&code=test-secret", "http://localhost:1455/success?state=fixture-state&code=test-secret", "http://localhost:1455/auth/callback?state=wrong&code=test-secret", "http://localhost:1455/auth/callback?state=fixture-state&code=x&code=test-secret"} {
		r := call(t, s, "POST", "/api/account/login/callback", map[string]string{"loginId": "browser1", "callbackUrl": raw})
		if r.Code != 400 || strings.Contains(r.Body.String(), "test-secret") {
			t.Fatal("unsafe callback", r.Code)
		}
	}
	mu.Lock()
	n := len(requests)
	mu.Unlock()
	if n != 0 {
		t.Fatal("invalid requests forwarded")
	}
	r := call(t, s, "POST", "/api/account/login/callback", payload)
	var result struct{ OK bool }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || !result.OK || strings.Contains(r.Body.String(), "secret") {
		t.Fatal("callback response", r.Code)
	}
	mu.Lock()
	seen := strings.Join(requests, ",")
	mu.Unlock()
	if seen != "/auth/callback?code=test-secret&state=fixture-state,/success" {
		t.Fatal("unexpected callback path or sensitive redirect forwarded")
	}
	if r = call(t, s, "POST", "/api/account/login/callback", payload); r.Code != 409 {
		t.Fatal("replay accepted")
	}
}

func TestBrowserLoginInvalidGrantAndResponse(t *testing.T) {
	good := "https://auth.openai.com/oauth/authorize?state=a&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback"
	for _, grant := range []string{strings.Replace(good, "auth.openai.com", "evil.test", 1), strings.Replace(good, "https://", "http://", 1), strings.Replace(good, "state=a", "state=", 1), strings.Replace(good, "1455", "22", 1), strings.Replace(good, "127.0.0.1", "169.254.169.254", 1), good + "&state=b"} {
		if _, _, ok := parseBrowserGrant(grant); ok {
			t.Fatal("accepted unsafe OAuth grant")
		}
	}
	// Neither a token-exchange failure rendered with HTTP 200 nor an
	// untrusted redirect indicates a successful callback.
	for _, location := range []string{"", "https://evil.test/success?secret=x", "http://localhost:80/success"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if location != "" {
				w.Header().Set("Location", location)
				w.WriteHeader(302)
			}
		}))
		target, _ := url.Parse(server.URL)
		accepted := completeLoopbackLogin(httptest.NewRequest("POST", "/", nil), target)
		server.Close()
		if accepted {
			t.Fatal("accepted invalid response")
		}
	}
}

func TestBrowserLoginCancelExpireAndReplace(t *testing.T) {
	if !browserPortFree() {
		t.Skip("Codex loopback login port is in use")
	}
	s, _ := setup(t)
	other, _ := setup(t)
	start := func(s *Server) int {
		return call(t, s, "POST", "/api/account/login", map[string]string{"type": "chatgpt"}).Code
	}
	if start(s) != 200 || start(other) != 409 {
		t.Fatal("concurrent account was allowed to replace login")
	}
	payload := map[string]string{"loginId": "browser1"}
	if call(t, other, "POST", "/api/account/login/cancel", payload).Code != 409 || call(t, s, "POST", "/api/account/login/cancel", payload).Code != 200 {
		t.Fatal("cancel ownership")
	}
	if start(other) != 200 {
		t.Fatal("cancel didn't release login")
	}
	other.expireBrowserLogin(other.pendingLogin)
	result := call(t, other, "GET", "/api/account", nil).Body.String()
	if strings.Contains(result, "pendingLogin") || !strings.Contains(result, "err_browser_login") {
		t.Fatal("expiry wasn't visible")
	}
	if start(s) != 200 {
		t.Fatal("expiry didn't release login")
	}
}
