//go:build unix

package codex

import (
	"os/exec"
	"syscall"
)

func configureProcess(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func terminateProcess(c *exec.Cmd) {
	if c.Process.Signal(syscall.Signal(0)) == nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
