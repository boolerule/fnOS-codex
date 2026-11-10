package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type CodexStatus struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	SystemPath string `json:"systemPath,omitempty"`
	Installing bool   `json:"installing"`
	Error      string `json:"error"`
}

// Managed installations are immutable. A terminal can continue to run its
// original version while new sessions pick up a replaced current-version file.
type CodexManager struct {
	catalog    codexCatalogCache
	inventory  systemInventoryCache
	renv       *RuntimeEnv
	root       string
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	installing bool
	lastError  string
	installer  sync.WaitGroup

	probeMu      sync.Mutex
	probePath    string
	probeUntil   time.Time
	probeFile    os.FileInfo
	probeVersion string
	probeErr     error
}

func NewCodexManager(renv *RuntimeEnv) *CodexManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &CodexManager{renv: renv, root: filepath.Join(renv.DataDir, "codex-cli"), ctx: ctx, cancel: cancel}
}

// npm publishes platform-specific payloads alongside the CLI wrapper under
// the same package name. Only wrapper releases are installation targets.
const semverNumber = `(0|[1-9][0-9]*)`
const prereleaseID = `(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)`

var releaseVersion = regexp.MustCompile(`^` + semverNumber + `\.` + semverNumber + `\.` + semverNumber +
	`(-` + prereleaseID + `(\.` + prereleaseID + `)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var platformVersion = regexp.MustCompile(`-(linux|darwin|win32)-(arm64|x64)(\+|$)`)
var errInvalidCodexVersion = errors.New("请选择有效的 Codex 版本号")

func validCodexReleaseVersion(version string) bool {
	return len(version) <= 128 && releaseVersion.MatchString(version) && !platformVersion.MatchString(version)
}

// PATH is provided by the deployment. Never look up executables relative to
// the service's current directory or the workspace selected by the user.
func (m *CodexManager) findExecutable(name string) (string, error) {
	for _, dir := range filepath.SplitList(m.renv.Path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("PATH 中找不到可执行文件 %s", name)
}

func codexPackageBinary(prefix string) (string, error) {
	if runtime.GOOS == "windows" {
		// exec.Command cannot launch npm's .cmd shim directly. npm can hoist
		// the optional platform package or nest it under the CLI package.
		for _, modules := range []string{filepath.Join(prefix, "node_modules"), filepath.Join(prefix, "node_modules", "@openai", "codex", "node_modules")} {
			files, _ := filepath.Glob(filepath.Join(modules, "@openai", "codex-win32-*", "vendor", "*", "bin", "codex.exe"))
			if len(files) == 1 {
				return files[0], nil
			}
		}
		return "", errors.New("找不到 Codex Windows 可执行文件")
	}
	return exec.LookPath(filepath.Join(prefix, "node_modules", ".bin", "codex"))
}

func (m *CodexManager) activeBinary() (binary, version, source string, err error) {
	data, err := os.ReadFile(filepath.Join(m.root, "current"))
	if err == nil {
		if strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
			return m.selectedSystem(data)
		}
		version = strings.TrimSpace(string(data))
		if !validCodexReleaseVersion(version) {
			return "", "", "", errors.New("托管 Codex 版本记录损坏，请重新安装")
		}
		binary, err = codexPackageBinary(filepath.Join(m.root, version))
		return binary, version, "managed", err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "", "", err
	}
	for _, dir := range filepath.SplitList(m.renv.Path) {
		if v, ok := pathCodex(dir); ok && v.Selectable && trustedCodexPath(v.Path) {
			return v.Path, v.Version, "path", nil
		}
	}
	return "", "", "path", errors.New("PATH 中找不到可信的 Codex 程序")
}

func (m *CodexManager) Binary() (string, error) {
	bin, _, _, err := m.activeBinary()
	return bin, err
}

func (m *CodexManager) command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = []string{"PATH=" + m.renv.Path}
	if m.renv.Home != "" {
		cmd.Env = append(cmd.Env, "HOME="+m.renv.Home)
	}
	if m.renv.CodexHome != "" {
		cmd.Env = append(cmd.Env, "CODEX_HOME="+m.renv.CodexHome)
	}
	// npm and version probes only need deployment networking and OS runtime
	// settings. The console's gateway credentials belong to the parent.
	keys := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
		"LANG", "LC_ALL", "LC_CTYPE", "TMPDIR",
	}
	if runtime.GOOS == "windows" {
		keys = append(keys, "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP",
			"USERPROFILE", "APPDATA", "LOCALAPPDATA")
	}
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.WaitDelay = 2 * time.Second
	configureInstallerProcess(cmd)
	return cmd
}

func (m *CodexManager) verify(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := m.command(ctx, bin, "--version")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, 512))
	stdout.Close()
	err = errors.Join(readErr, cmd.Wait())
	if err != nil {
		return "", fmt.Errorf("运行 codex --version 失败: %w", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "codex-cli ")
	if !validCodexReleaseVersion(version) {
		return "", errors.New("Codex CLI 返回了无法识别的版本号")
	}
	return version, nil
}

func (m *CodexManager) Status() CodexStatus {
	m.mu.Lock()
	status := CodexStatus{Installing: m.installing, Error: m.lastError}
	m.mu.Unlock()
	bin, version, source, err := m.activeBinary()
	// The PATH copy can be updated outside the console. Cache its CLI probe
	// briefly so UI polling does not repeatedly start Node or Codex.
	if err == nil && source == "path" {
		status.SystemPath = bin
		if version == "" {
			version, err = m.verifiedSystemVersion(m.ctx, bin)
		}
	}
	if err == nil {
		status.Source, status.Version, status.Installed = source, version, true
	} else if status.Error == "" && (bin != "" || source != "path") {
		status.Error = err.Error()
	}
	return status
}

func (m *CodexManager) Install(target string) error {
	if target != "latest" && !validCodexReleaseVersion(target) {
		return errInvalidCodexVersion
	}
	var cached bool
	if target != "latest" {
		_, err := os.Lstat(filepath.Join(m.root, target))
		cached = err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	var npm string
	if !cached {
		var err error
		npm, err = m.findExecutable("npm")
		if err != nil {
			return err
		}
	}
	lock, err := m.beginInstall()
	if err != nil {
		return err
	}
	m.runInstall(lock, func(ctx context.Context) error {
		if cached {
			if err := m.checkManaged(ctx, target); err != nil {
				return err
			}
			return m.activate(ctx, target)
		}
		return m.installFromRegistry(ctx, npm, target)
	})
	return nil
}

func (m *CodexManager) beginInstall() (*os.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return nil, errors.New("控制台正在关闭")
	}
	if m.installing {
		return nil, errors.New("Codex 安装正在进行中")
	}
	if !filepath.IsAbs(m.root) {
		return nil, errors.New("Codex 安装目录必须是绝对路径")
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return nil, err
	}
	lock, err := lockInstallerDirectory(m.root)
	if err != nil {
		return nil, err
	}
	m.installing, m.lastError = true, ""
	m.installer.Add(1)
	return lock, nil
}
func (m *CodexManager) runInstall(lock *os.File, operation func(context.Context) error) {
	go func() {
		defer m.installer.Done()
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Minute)
		defer cancel()
		err := operation(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		lock.Close()
		if err != nil {
			m.lastError = err.Error()
		}
		m.installing = false
	}()
}

func (m *CodexManager) installFromRegistry(ctx context.Context, npm, target string) error {
	// All fnOS installers hold the same lock, so leftover staging directories
	// from a killed process cannot belong to another active install.
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir(m.root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".staging-") || strings.HasPrefix(entry.Name(), ".current-") {
				if err := os.RemoveAll(filepath.Join(m.root, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	stage, err := os.MkdirTemp(m.root, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	registry := codexRegistry()
	args := []string{"install", "--prefix", stage, "--no-save", "--no-package-lock", "--include=optional",
		"--ignore-scripts", "--no-fund", "--no-audit", "--registry", registry, "--@openai:registry=" + registry, "@openai/codex@" + target}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(npm), ".cmd") {
		script := filepath.Join(filepath.Dir(npm), "node_modules", "npm", "bin", "npm-cli.js")
		npm, err = m.findExecutable("node")
		if err != nil {
			return err
		}
		args = append([]string{script}, args...)
	}
	cmd := m.command(ctx, npm, args...)
	cmd.Dir = stage
	cache := filepath.Join(m.root, "npm-cache")
	cmd.Env = append(cmd.Env, "npm_config_cache="+cache)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("npm 安装 @openai/codex 失败 (%v)，详见 %s", err, filepath.Join(cache, "_logs"))
	}
	bin, err := codexPackageBinary(stage)
	if err != nil {
		return fmt.Errorf("安装包缺少 Codex CLI: %w", err)
	}
	version, err := m.verify(ctx, bin)
	if err != nil {
		return err
	}
	if target != "latest" && version != target {
		return fmt.Errorf("请求安装 Codex %s，实际版本为 %s", target, version)
	}

	final := filepath.Join(m.root, version)
	err = os.Rename(stage, final)
	if err != nil {
		// On repeat installs keep the immutable, previously verified directory.
		if checkErr := m.checkManaged(ctx, version); checkErr != nil {
			return fmt.Errorf("保存 Codex %s 失败: %w", version, errors.Join(err, checkErr))
		}
	}
	return m.activate(ctx, version)
}

func (m *CodexManager) checkManaged(ctx context.Context, version string) error {
	dir := filepath.Join(m.root, version)
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("托管 Codex %s 的版本目录无效", version)
	}
	bin, err := codexPackageBinary(dir)
	if err != nil {
		return fmt.Errorf("托管 Codex %s 缺少可执行文件: %w", version, err)
	}
	got, err := m.verify(ctx, bin)
	if err != nil {
		return fmt.Errorf("校验托管 Codex %s 失败: %w", version, err)
	}
	if got != version {
		return fmt.Errorf("现有 Codex 版本目录中的版本号为 %s，应为 %s", got, version)
	}
	return nil
}

func (m *CodexManager) activate(ctx context.Context, version string) error {
	temp, err := os.CreateTemp(m.root, ".current-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.WriteString(version + "\n")
	closeErr := temp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(m.root, "current"))
}

func (m *CodexManager) Close() {
	m.mu.Lock()
	m.cancel()
	m.mu.Unlock()
	m.installer.Wait()
}
