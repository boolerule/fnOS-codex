package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// This command is called ONLY by the opt-in uninstall hook, after the service
// has stopped. Installations are discovered again; no paths from a browser
// request or a saved inventory are accepted as uninstall targets.
func uninstallSystemCodex() error {
	if os.Getenv("TRIM_APPNAME") != "CodexTerminal" || os.Getenv("TRIM_APP_STATUS") != "UNINSTALL" || os.Getenv("wizard_codex_cleanup") != "remove" {
		return errors.New("需要在飞牛卸载向导中明确选择同步清理 CLI")
	}
	if os.Geteuid() == 0 {
		return errors.New("请用工作台应用账户运行 Codex 清理")
	}
	env, err := loadRuntimeEnv()
	if err != nil {
		return err
	}
	pkgvar := os.Getenv("TRIM_PKGVAR")
	if !filepath.IsAbs(pkgvar) || filepath.Clean(pkgvar) != env.DataDir {
		return errors.New("无法确认飞牛应用数据目录")
	}
	real, err := filepath.EvalSymlinks(pkgvar)
	if err != nil || real == "/" || !filepath.IsAbs(real) {
		return errors.New("无法解析飞牛应用数据目录")
	}
	env.DataDir = real
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return uninstallCodexCopies(ctx, env, scanSystemCodex(ctx, env, false), uninstallNpmCodex)
}

func uninstallCodexCopies(ctx context.Context, env *RuntimeEnv, copies []SystemCodex, remove func(context.Context, string) error) error {
	seen := map[string]bool{}
	var failures []error
	for _, copy := range copies {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if copy.Kind != "npm" || copy.Prefix == "" {
			failures = append(failures, fmt.Errorf("%s：请使用原安装方式卸载该 %s 安装。", copy.Path, copy.Kind))
			continue
		}
		prefix, err := filepath.EvalSymlinks(copy.Prefix)
		if err != nil || !filepath.IsAbs(prefix) || prefix == "/" {
			failures = append(failures, fmt.Errorf("%s：无法确认 npm 安装前缀。", copy.Path))
			continue
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		if err = checkGlobalCodex(prefix); err == nil {
			err = remove(ctx, prefix)
		}
		if err == nil {
			// npm may report success after a concurrent removal or a broken
			// installation. Check both the package and its global command.
			for _, p := range []string{globalCodexDir(prefix), filepath.Join(prefix, "bin", "codex")} {
				if _, statErr := os.Lstat(p); !errors.Is(statErr, os.ErrNotExist) {
					err = fmt.Errorf("卸载后仍需检查 %s", p)
					break
				}
			}
		}
		if err != nil {
			quoted := "'" + strings.ReplaceAll(prefix, "'", "'\\''") + "'"
			failures = append(failures, fmt.Errorf("%s：%v。请由安装所有者核实并处理（npm 全局安装可用 npm uninstall --global --ignore-scripts --prefix %s @openai/codex）。", copy.Path, err, quoted))
		}
	}
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("以下安装需要手动处理。处理后可重试，也可选择保留系统安装以继续卸载工作台。\n%w", errors.Join(failures...))
	}
	// Do not follow an unexpected link to a different install's CLI store.
	root, err := os.OpenRoot(env.DataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat("codex-cli")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("托管 CLI 路径不是应用数据目录下的常规目录")
	}
	lock, err := lockInstallerDirectory(filepath.Join(env.DataDir, "codex-cli"))
	if err != nil {
		return err
	}
	defer lock.Close()
	return root.RemoveAll("codex-cli")
}

func globalCodexDir(prefix string) string {
	return filepath.Join(prefix, "lib", "node_modules", "@openai", "codex")
}

// A global npm package has a concrete directory and a bin link to the
// launcher declared in its manifest. Keep pnpm links, npm link packages,
// shadowing executables and unrelated files in place.
func checkGlobalCodex(prefix string) error {
	dir := globalCodexDir(prefix)
	for _, p := range []string{dir, filepath.Join(dir, "package.json"), filepath.Join(prefix, "bin")} {
		real, err := filepath.EvalSymlinks(p)
		if err != nil || real != p {
			return errors.New("安装布局已变化或使用链接")
		}
		if !trustedCodexPath(p) {
			return errors.New("安装由其他账户持有或位于共享可写目录")
		}
	}
	file, err := os.OpenFile(filepath.Join(dir, "package.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("无法验证 npm 软件包清单")
	}
	var pkg struct {
		Name string
		Bin  json.RawMessage
		Man  json.RawMessage
	}
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&pkg); err != nil || pkg.Name != "@openai/codex" || len(pkg.Man) > 0 {
		return errors.New("无法确认官方 Codex 软件包")
	}
	var bins map[string]string
	var entry string
	if err := json.Unmarshal(pkg.Bin, &bins); err == nil && len(bins) == 1 {
		entry = bins["codex"]
	} else {
		_ = json.Unmarshal(pkg.Bin, &entry)
	}
	entry = filepath.Clean(entry)
	if !filepath.IsLocal(entry) || entry == "." {
		return errors.New("软件包的命令入口不符合预期")
	}
	launcher := filepath.Join(dir, entry)
	real, err := filepath.EvalSymlinks(launcher)
	if err != nil || real != launcher || !trustedCodexPath(launcher) {
		return errors.New("软件包的命令入口不是常规安装")
	}
	link := filepath.Join(prefix, "bin", "codex")
	info, err = os.Lstat(link)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New("全局 codex 命令不是该安装的链接")
		}
		target, err := filepath.EvalSymlinks(link)
		if err != nil || target != launcher {
			return errors.New("全局命令已指向其他程序")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, path := range []string{filepath.Join(prefix, "bin"), filepath.Dir(dir)} {
		if err := syscall.Access(path, 3); err != nil {
			return errors.New("应用账户没有移除该全局安装的权限")
		}
	}
	return nil
}

func uninstallNpmCodex(ctx context.Context, prefix string) error {
	// A distro package may use the same layout. Its ownership database wins.
	if _, e := os.Stat("/var/lib/dpkg/status"); e == nil {
		query := exec.CommandContext(ctx, "/usr/bin/dpkg-query", "-S", filepath.Join(globalCodexDir(prefix), "package.json"), filepath.Join(prefix, "bin", "codex"))
		query.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
		out, err := query.Output()
		var exit *exec.ExitError
		if len(out) > 0 || err == nil {
			return errors.New("该文件由系统包管理器登记，请使用系统包管理器卸载")
		}
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return errors.New("无法核对系统包管理器的文件归属")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	const runtimeDir = "/var/apps/nodejs_v24/target"
	tmp, err := os.MkdirTemp("", "codex-npm-uninstall-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.CommandContext(ctx, runtimeDir+"/bin/node", runtimeDir+"/lib/node_modules/npm/bin/npm-cli.js", "uninstall", "--global", "--ignore-scripts", "--offline", "--no-audit", "--no-fund", "--userconfig="+filepath.Join(tmp, "user.npmrc"), "--globalconfig="+filepath.Join(tmp, "global.npmrc"), "--prefix", prefix, "@openai/codex")
	cmd.Dir = tmp
	cmd.Env = []string{"PATH=" + runtimeDir + "/bin:/usr/bin:/bin", "HOME=" + tmp, "npm_config_cache=" + filepath.Join(tmp, "cache")}
	configureInstallerProcess(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("飞牛 Node.js 24 的 npm 卸载失败：%w", err)
	}
	return nil
}
