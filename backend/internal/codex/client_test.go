package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Run the test executable as a controllable JSONL peer.
func TestMain(m *testing.M) {
	if slices.Equal(os.Args[1:], []string{"app-server", "--listen", "stdio://"}) {
		os.Exit(fixture(os.Getenv("TEST_SERVER_MODE")))
	}
	os.Exit(m.Run())
}

func fixture(mode string) int {
	if mode == "noHandshake" {
		time.Sleep(time.Minute)
		return 1
	}
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	var held Message
	for in.Scan() {
		var msg Message
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			return 1
		}
		switch msg.Method {
		case "initialize":
			if os.Getenv("TEST_PARENT_SECRET") != "" || os.Getenv("HOME") != filepath.Dir(os.Getenv("CODEX_HOME")) {
				return 1
			}
			wd, _ := os.Getwd()
			if wd != os.Getenv("HOME") {
				return 1
			}
			out.Encode(Message{ID: msg.ID, Result: json.RawMessage(`{}`)})
		case "initialized":
			out.Encode(Message{Method: "ready"})
		case "hold":
			held = msg
			out.Encode(Message{Method: "holding"})
		case "release":
			out.Encode(Message{ID: msg.ID, Result: msg.Params})
			out.Encode(Message{ID: held.ID, Result: held.Params})
		case "approval":
			out.Encode(Message{ID: json.RawMessage("77"), Method: "item/commandExecution/requestApproval"})
			out.Encode(Message{ID: msg.ID, Result: json.RawMessage(`{}`)})
		case "":
			if string(msg.ID) != "77" {
				return 1
			}
			out.Encode(Message{Method: "decision", Params: msg.Result})
		case "block":
			out.Encode(Message{Method: "blocked"})
			time.Sleep(time.Minute)
		case "invalid":
			fmt.Println("broken frame")
			time.Sleep(time.Minute)
		}
	}
	return 0
}

func TestConcurrentCallsAndApproval(t *testing.T) {
	t.Setenv("TEST_PARENT_SECRET", "not-for-Codex")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := make(chan Message, 8)
	client, err := Start(ctx, bin, filepath.Join(t.TempDir(), ".codex"), "test", nil, io.Discard, func(m Message) { events <- m }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	event := func(method string) Message {
		t.Helper()
		select {
		case got := <-events:
			if got.Method != method {
				t.Fatalf("got %s, want %s", got.Method, method)
			}
			return got
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return Message{}
		}
	}
	event("ready")
	first := make(chan error, 1)
	go func() {
		var result int
		err := client.Call(ctx, "hold", 42, &result)
		if err == nil && result != 42 {
			err = fmt.Errorf("wrong hold result: %d", result)
		}
		first <- err
	}()
	event("holding")
	var result int
	if err := client.Call(ctx, "release", 13, &result); err != nil || result != 13 {
		t.Fatal(result, err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "approval", nil, nil); err != nil {
		t.Fatal(err)
	}
	approval := event("item/commandExecution/requestApproval")
	if err := client.Reply(approval.ID, map[string]string{"decision": "decline"}); err != nil {
		t.Fatal(err)
	}
	if got := event("decision"); string(got.Params) != `{"decision":"decline"}` {
		t.Fatal(string(got.Params))
	}

	// A blocked child may stop consuming stdin entirely. A large write must
	// respect the caller's deadline while queued or blocked on the OS pipe.
	blocked := make(chan error, 1)
	go func() { blocked <- client.Call(ctx, "block", nil, nil) }()
	event("blocked")
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if err := client.Call(short, "large", strings.Repeat("x", 2<<20), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	client.Close()
	if err := <-blocked; err == nil {
		t.Fatal("inflight call survived Close")
	}
	if client.Alive() {
		t.Fatal("client still alive")
	}
}

func TestHandshakeDeadlineAndMalformedFrame(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	home := filepath.Join(t.TempDir(), ".codex")
	if _, err := Start(ctx, bin, home, "test", []string{"TEST_SERVER_MODE=noHandshake"}, io.Discard, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	exited := make(chan error, 1)
	client, err := Start(ctx, bin, home, "test", nil, io.Discard, nil, func(err error) { exited <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Call(ctx, "invalid", nil, nil); err == nil {
		t.Fatal("invalid frame was accepted")
	}
	select {
	case err := <-exited:
		if err == nil || !strings.Contains(err.Error(), "invalid app-server message") {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
