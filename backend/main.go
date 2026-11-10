package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

//go:embed all:embed
var embeddedFS embed.FS

var consoleVersion = "dev"

func listenGateway(socket string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0750); err != nil {
		return nil, err
	}
	listen := func() (net.Listener, error) { return net.Listen("unix", socket) }
	ln, err := listen()
	if err != nil {
		fi, statErr := os.Lstat(socket)
		if statErr != nil || fi.Mode()&os.ModeSocket == 0 {
			return nil, err
		}
		c, dialErr := net.DialTimeout("unix", socket, time.Second)
		if dialErr == nil {
			c.Close()
			return nil, fmt.Errorf("gateway socket already in use: %s", socket)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, err
		}
		current, statErr := os.Lstat(socket)
		if statErr != nil || !os.SameFile(fi, current) {
			return nil, err
		}
		if err = os.Remove(socket); err != nil {
			return nil, err
		}
		ln, err = listen()
		if err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(socket, 0660); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func run(ctx context.Context) error {
	env, err := loadRuntimeEnv()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(env.DataDir, 0700); err != nil {
		return err
	}
	type endpoint struct {
		listener net.Listener
		gateway  bool
	}
	var endpoints []endpoint
	defer func() {
		for _, ep := range endpoints {
			ep.listener.Close()
		}
	}()
	if env.Listen == "" || os.Getenv("TRIM_APPDEST") != "" || os.Getenv("CODEX_CONSOLE_SOCKET") != "" || os.Getenv("HARNESS_ADMIN_SOCK") != "" {
		ln, err := listenGateway(env.AdminSock)
		if err != nil {
			return err
		}
		endpoints = append(endpoints, endpoint{ln, true})
	}
	if env.Listen != "" {
		ln, err := net.Listen("tcp", env.Listen)
		if err != nil {
			return err
		}
		endpoints = append(endpoints, endpoint{ln, false})
	}
	if env.SessionDir != "" {
		// Only this process owns its replay files. Remove them on exit.
		if err := os.MkdirAll(env.SessionDir, 0700); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(env.SessionDir, "run-")
		if err != nil {
			return err
		}
		env.SessionDir = dir
		defer os.RemoveAll(dir)
	}
	pidPath := filepath.Join(env.DataDir, "codex-console.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return err
	}
	defer os.Remove(pidPath)
	app := &AdminMux{renv: env, codex: NewCodexManager(env), fnos: NewFnosClient(env), sessions: NewSessionManager(env)}
	app.diagnostics.record("", "service_started")
	defer app.codex.Close()
	defer app.sessions.CloseAll()
	defer app.workbench.close()
	go app.codex.LocalVersions(ctx, false)
	errs := make(chan error, len(endpoints))
	var servers []*http.Server
	for _, ep := range endpoints {
		srv := &http.Server{Handler: app.handler(ep.gateway), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
		servers = append(servers, srv)
		go func() { errs <- srv.Serve(ep.listener) }()
		logInfo("listening on %s", ep.listener.Addr())
	}
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	for _, srv := range servers {
		srv.Close()
	}
	return err
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "uninstall-system-codex" {
		if err := uninstallSystemCodex(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		logError("%v", err)
		flushLog()
		os.Exit(1)
	}
	flushLog()
}
