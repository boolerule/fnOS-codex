package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// The data returned by other users' manifests is display-only. Reject paths
// whose files or parents can be replaced by a different NAS account.
func trustedCodexPath(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(real) {
		return false
	}
	return trustedCodexAncestors(path) && (path == real || trustedCodexAncestors(real))
}

func trustedCodexAncestors(path string) bool {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
			return false
		}
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0022 != 0 && !(info.IsDir() && info.Mode()&os.ModeSticky != 0 && st.Uid == 0) {
			return false
		}
		if filepath.Dir(p) == p {
			return true
		}
	}
}

func openDiscoveryDir(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
}
func openDiscoveryFile(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
