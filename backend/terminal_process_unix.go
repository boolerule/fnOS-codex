//go:build !noterminal && unix

package main

import (
	"os"
	"syscall"
)

// pty.StartWithSize creates a new session and process group for the CLI.
// npm's Codex launcher can have a native child holding the slave PTY open.
func stopTerminalProcess(process *os.Process) {
	if process.Signal(syscall.Signal(0)) == nil {
		_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
		_ = process.Kill()
	}
}
