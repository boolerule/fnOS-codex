// Package codex connects one workbench to a Codex app-server over JSONL stdio.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

var errExited = errors.New("Codex app-server exited")

type writeRequest struct {
	ctx     context.Context
	data    []byte
	written chan error
}

type Client struct {
	cmd      *exec.Cmd
	writes   chan writeRequest
	done     chan struct{}
	exitErr  error // Published by closing done.
	finished chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	pending  map[string]chan Message
	next     uint64
}

// Start accepts the caller's allowlisted environment. The context controls the
// handshake; Close controls the lifetime of the initialized process. Callbacks
// execute in receive order and must return without waiting for a client call.
func Start(ctx context.Context, binary, home, clientVersion string, env []string, stderr io.Writer, onEvent func(Message), onExit func(error)) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(binary) || !filepath.IsAbs(home) {
		return nil, errors.New("Codex executable and CODEX_HOME must be absolute paths")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "app-server", "--listen", "stdio://")
	cmd.Dir = filepath.Dir(home)
	cmd.Env = append(append([]string{}, env...), "HOME="+cmd.Dir, "PWD="+cmd.Dir, "CODEX_HOME="+home)
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	r, w := io.Pipe()
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		in.Close()
		r.Close()
		w.Close()
		return nil, err
	}
	c := &Client{cmd: cmd, writes: make(chan writeRequest), done: make(chan struct{}), finished: make(chan struct{}), pending: make(map[string]chan Message)}
	// Using an io.Pipe allows Wait to reap a crashed process even when one of
	// its children has inherited stdout. WaitDelay bounds that drain.
	reaped := make(chan struct{})
	go func() {
		err := cmd.Wait()
		w.CloseWithError(err)
		close(reaped)
	}()
	go c.writeLoop(in)
	go func() {
		err := c.readLoop(r, onEvent)
		r.CloseWithError(err)
		if err != nil {
			c.exitErr = fmt.Errorf("%w: %w", errExited, err)
			c.stop()
		} else {
			c.exitErr = errExited
		}
		close(c.done)
		<-reaped
		if onExit != nil {
			onExit(err)
		}
		close(c.finished)
	}()

	init := map[string]any{"clientInfo": map[string]string{"name": "codex_fnos_console", "title": "Codex for fnOS", "version": clientVersion}}
	err = c.Call(ctx, "initialize", init, nil)
	if err == nil {
		err = c.send(ctx, map[string]string{"method": "initialized"})
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) readLoop(r io.Reader, onEvent func(Message)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var m Message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			return fmt.Errorf("invalid app-server message: %w", err)
		}
		if m.Method != "" {
			if onEvent != nil {
				onEvent(m)
			}
			continue
		}
		var id string
		if err := json.Unmarshal(m.ID, &id); err != nil {
			return fmt.Errorf("invalid app-server response ID: %w", err)
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	return scanner.Err()
}

func (c *Client) writeLoop(in io.WriteCloser) {
	defer in.Close()
	for {
		select {
		case <-c.done:
			return
		case req := <-c.writes:
			err := req.ctx.Err()
			if err == nil {
				_, err = in.Write(req.data)
			}
			req.written <- err
		}
	}
}

func (c *Client) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req := writeRequest{ctx, append(b, '\n'), make(chan error, 1)}
	select {
	case c.writes <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.exitErr
	}
	select {
	case err := <-req.written:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.exitErr
	}
}

func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.next++
	id := "c" + strconv.FormatUint(c.next, 10)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	var m Message
	select {
	case m = <-ch:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		select {
		case m = <-ch:
		default:
			return c.exitErr
		}
	}
	if m.Error != nil {
		return m.Error
	}
	if out != nil {
		return json.Unmarshal(m.Result, out)
	}
	return nil
}

func (c *Client) Reply(id json.RawMessage, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.send(ctx, map[string]any{"id": id, "result": result})
}

func (c *Client) Reject(id json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.send(ctx, map[string]any{"id": id, "error": RPCError{-32601, "Client does not support this request"}})
}

func (c *Client) stop() { c.stopOnce.Do(func() { terminateProcess(c.cmd) }) }

func (c *Client) Close() {
	if c.Alive() {
		c.stop()
	}
	<-c.finished
}

func (c *Client) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}
