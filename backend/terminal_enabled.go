//go:build !noterminal

package main

import "path/filepath"

const terminalFeature = true

func sessionDirFor(dataDir string) string {
	return filepath.Join(dataDir, "terminal-sessions")
}
