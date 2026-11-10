//go:build !unix

package codex

import "os/exec"

func configureProcess(c *exec.Cmd) {}
func terminateProcess(c *exec.Cmd) { _ = c.Process.Kill() }
