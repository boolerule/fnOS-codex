package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureInstallerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func lockInstallerDirectory(root string) (*os.File, error) {
	dir, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		dir.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("另一个控制台正在安装 Codex")
		}
		return nil, err
	}
	return dir, nil
}
