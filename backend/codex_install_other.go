//go:build !linux

package main

import (
	"os"
	"os/exec"
)

// Non-fnOS development builds use exec.CommandContext's process cancellation.
func configureInstallerProcess(cmd *exec.Cmd) {}

func lockInstallerDirectory(root string) (*os.File, error) { return os.Open(root) }
