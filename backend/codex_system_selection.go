package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type systemSelection struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

func (m *CodexManager) verifiedSystemVersion(ctx context.Context, path string) (string, error) {
	if path == "" || executablePath(path) != path || !trustedCodexPath(path) {
		return "", errors.New("系统 Codex 路径不可用或权限不可信，请重新扫描")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	same := m.probeFile != nil && os.SameFile(info, m.probeFile) && info.Size() == m.probeFile.Size() && info.ModTime().Equal(m.probeFile.ModTime())
	if path != m.probePath || !same || time.Now().After(m.probeUntil) {
		m.probeVersion, m.probeErr = m.verify(ctx, path)
		m.probeFile, m.probePath, m.probeUntil = info, path, time.Now().Add(30*time.Second)
	}
	return m.probeVersion, m.probeErr
}

func (m *CodexManager) selectedSystem(data []byte) (string, string, string, error) {
	var entry systemSelection
	if json.Unmarshal(data, &entry) != nil || !filepath.IsAbs(entry.Path) || !validCodexReleaseVersion(entry.Version) {
		return "", "", "path", errors.New("系统 Codex 选择记录无效，请重新扫描")
	}
	actual, err := m.verifiedSystemVersion(m.ctx, entry.Path)
	if err == nil && actual != entry.Version {
		err = fmt.Errorf("系统 Codex 已变更为 %s，请重新扫描并选择版本", actual)
	}
	return entry.Path, entry.Version, "path", err
}

func (m *CodexManager) selectSystem(version, path string) error {
	if !validCodexReleaseVersion(version) {
		return errInvalidCodexVersion
	}
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return errors.New("系统 Codex 路径无效")
	}
	// Rescan known layouts; a browser cannot turn this endpoint into a way
	// to execute or persist an arbitrary local file.
	scanCtx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
	defer cancel()
	verified := false
	for _, entry := range scanSystemCodex(scanCtx, m.renv, false) {
		if entry.Path == path && entry.Selectable && (entry.Version == "" || entry.Version == version) {
			verified = true
			break
		}
	}
	if !verified {
		return errors.New("未找到此可信系统安装，请重新扫描")
	}
	lock, err := m.beginInstall()
	if err != nil {
		return err
	}
	m.runInstall(lock, func(ctx context.Context) error {
		actual, err := m.verifiedSystemVersion(ctx, path)
		if err != nil {
			return err
		}
		if actual != version {
			return fmt.Errorf("系统 Codex 当前版本为 %s，需要 %s；请重新扫描", actual, version)
		}
		data, err := json.Marshal(systemSelection{Version: version, Path: path})
		if err != nil {
			return err
		}
		return m.activate(ctx, string(data))
	})
	return nil
}
