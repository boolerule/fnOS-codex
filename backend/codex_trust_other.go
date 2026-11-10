//go:build !linux

package main

import (
	"os"
	"path/filepath"
)

// Development hosts only trust paths under the current user's profile or
// system installation directories. fnOS uses the Linux ownership checks.
func trustedCodexPath(path string) bool {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	home, _ := os.UserHomeDir()
	for _, base := range []string{home, os.Getenv("ProgramFiles"), os.Getenv("SystemRoot")} {
		base, err := filepath.EvalSymlinks(base)
		if err == nil && filepath.IsAbs(base) {
			rel, err := filepath.Rel(base, path)
			if err == nil && filepath.IsLocal(rel) {
				return true
			}
		}
	}
	return false
}

func openDiscoveryDir(p string) (*os.File, error)  { return os.Open(p) }
func openDiscoveryFile(p string) (*os.File, error) { return os.Open(p) }
