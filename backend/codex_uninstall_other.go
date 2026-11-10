//go:build !linux

package main

import "errors"

func uninstallSystemCodex() error { return errors.New("system Codex cleanup requires fnOS Linux") }
