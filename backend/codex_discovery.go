package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Path is the canonical executable, never an arbitrary path supplied by HTTP.
// Prefix is internal and only identifies npm's conventional global layout.
type SystemCodex struct {
	Version    string `json:"version"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Selectable bool   `json:"selectable"`
	Prefix     string `json:"-"`
}

type managedCodex struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}
type localCodexVersions struct {
	Installed []string       `json:"installed"`
	Managed   []managedCodex `json:"managed"`
	System    []SystemCodex  `json:"system"`
	Error     string         `json:"error,omitempty"`
}
type systemInventoryCache struct {
	mu    sync.Mutex
	until time.Time
	items []SystemCodex
}

func (m *CodexManager) LocalVersions(ctx context.Context, refresh bool) localCodexVersions {
	// Scans use known installation layouts; managed installs can change at any
	// moment and are always enumerated fresh.
	c := &m.inventory
	c.mu.Lock()
	var scanError string
	if refresh || time.Now().After(c.until) {
		scanCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		candidates := scanSystemCodex(scanCtx, m.renv, true)
		c.items = candidates
		if scanCtx.Err() == nil {
			c.until = time.Now().Add(30 * time.Second)
		} else {
			c.until = time.Time{}
			scanError = "系统扫描未完成，当前仅列出已发现的安装；可重新扫描"
		}
		cancel()
	}
	system := append([]SystemCodex{}, c.items...)
	c.mu.Unlock()
	result := localCodexVersions{Installed: []string{}, Managed: []managedCodex{}, System: system, Error: scanError}
	dirs, err := os.ReadDir(m.root)
	if err != nil && !os.IsNotExist(err) {
		result.Error = err.Error()
		return result
	}
	for _, d := range dirs {
		if !d.IsDir() || !validCodexReleaseVersion(d.Name()) {
			continue
		}
		if path, err := codexPackageBinary(filepath.Join(m.root, d.Name())); err == nil {
			result.Managed = append(result.Managed, managedCodex{Version: d.Name(), Path: path})
		}
	}
	slices.SortFunc(result.Managed, func(a, b managedCodex) int { return compareCodexVersions(b.Version, a.Version) })
	for _, v := range result.Managed {
		result.Installed = append(result.Installed, v.Version)
	}
	return result
}

func executablePath(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(real) {
		return ""
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) {
		return ""
	}
	return real
}
func nativeCodex(path string) bool {
	f, err := openDiscoveryFile(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return false
	}
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return string(hdr[:2]) == "MZ"
	}
	return string(hdr[:]) == "\x7fELF"
}

// Package manifests can be read from other users' accessible homes. Never
// follow a FIFO/device, parse an unbounded file, or interpret a shim as shell.
func packageCandidate(root, kind, prefix string) (SystemCodex, bool) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return SystemCodex{}, false
	}
	manifest := filepath.Join(real, "package.json")
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return SystemCodex{}, false
	}
	f, err := openDiscoveryFile(manifest)
	if err != nil {
		return SystemCodex{}, false
	}
	defer f.Close()
	openInfo, e := f.Stat()
	if e != nil || !os.SameFile(info, openInfo) {
		return SystemCodex{}, false
	}
	var pkg struct {
		Name    string
		Version string
		Bin     map[string]string
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&pkg) != nil || pkg.Name != "@openai/codex" || !validCodexReleaseVersion(pkg.Version) {
		return SystemCodex{}, false
	}
	rel := pkg.Bin["codex"]
	if !filepath.IsLocal(rel) {
		return SystemCodex{}, false
	}
	launcher := executablePath(filepath.Join(real, rel))
	if runtime.GOOS == "windows" {
		// The installer uses a native Windows payload because exec.Command
		// cannot directly execute the npm cmd shim.
		modules := filepath.Dir(filepath.Dir(real))
		native, err := codexPackageBinary(filepath.Dir(modules))
		if err != nil {
			return SystemCodex{}, false
		}
		launcher = executablePath(native)
	} else if launcher != "" && strings.HasSuffix(launcher, ".js") {
		// Prefer the real, matching platform payload when available. It can run
		// without relying on the selected nvm/fnm installation's node interpreter.
		arch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
		npmArch := map[string]string{"arm64": "arm64", "amd64": "x64"}[runtime.GOARCH]
		if arch != "" && runtime.GOOS == "linux" {
			modules := filepath.Dir(filepath.Dir(real))
			platform := "codex-linux-" + npmArch
			for _, vendor := range []string{filepath.Join(real, "node_modules", "@openai", platform, "vendor"), filepath.Join(modules, "@openai", platform, "vendor"), filepath.Join(real, "vendor")} {
				for _, sub := range []string{"bin", "codex"} {
					if found := executablePath(filepath.Join(vendor, arch+"-unknown-linux-musl", sub, "codex")); found != "" {
						launcher = found
						break
					}
				}
				if !strings.HasSuffix(launcher, ".js") {
					break
				}
			}
		}
	}
	if launcher == "" {
		return SystemCodex{}, false
	}
	// A symlink to a trusted payload in an untrusted project does not make the
	// surrounding npm metadata a trusted basis for choosing that version.
	return SystemCodex{pkg.Version, launcher, kind, trustedCodexPath(root) && trustedCodexPath(manifest) && trustedCodexPath(launcher), prefix}, true
}

type codexDiscovery struct {
	ctx      context.Context
	env      *RuntimeEnv
	probe    bool
	seen     map[string]int
	binDirs  map[string]bool
	packages map[string]bool
	results  []SystemCodex
}

func (d *codexDiscovery) add(v SystemCodex) {
	if v.Path == "" {
		return
	}
	if d.env.DataDir != "" {
		managed := filepath.Join(d.env.DataDir, "codex-cli")
		if within, err := filepath.Rel(managed, v.Path); err == nil && filepath.IsLocal(within) {
			return
		}
	}
	if old, ok := d.seen[v.Path]; ok {
		prev := d.results[old]
		if (!prev.Selectable && v.Selectable) || (prev.Selectable == v.Selectable && ((prev.Prefix == "" && v.Prefix != "") || (prev.Version == "" && v.Version != "") || (prev.Kind == "binary" && v.Kind != "binary"))) {
			d.results[old] = v
		}
		return
	}
	d.seen[v.Path] = len(d.results)
	d.results = append(d.results, v)
}
func (d *codexDiscovery) pkg(root, kind, prefix string) {
	if d.ctx.Err() != nil || d.packages[root] {
		return
	}
	d.packages[root] = true
	if v, ok := packageCandidate(root, kind, prefix); ok {
		d.add(v)
	}
}

// Inspect a PATH directory without executing its codex shell shim. Return
// only an executable that the directory really exposed (for PATH fallback).
func pathCodex(dir string) (SystemCodex, bool) {
	if !filepath.IsAbs(dir) {
		return SystemCodex{}, false
	}
	path, err := exec.LookPath(filepath.Join(dir, "codex"))
	if err != nil {
		return SystemCodex{}, false
	}
	real := executablePath(path)
	if real == "" {
		return SystemCodex{}, false
	}
	if strings.HasSuffix(real, ".js") {
		if v, ok := packageCandidate(filepath.Dir(filepath.Dir(real)), "npm", ""); ok {
			v.Selectable = v.Selectable && trustedCodexPath(path)
			return v, true
		}
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(real), ".exe") {
		if v, ok := packageCandidate(filepath.Join(dir, "node_modules", "@openai", "codex"), "npm", ""); ok {
			v.Selectable = v.Selectable && trustedCodexPath(path)
			return v, true
		}
	}
	name := filepath.Base(real)
	if name != "codex" && !strings.EqualFold(name, "codex.exe") && !strings.HasPrefix(name, "codex-x86_64-") && !strings.HasPrefix(name, "codex-aarch64-") {
		return SystemCodex{}, false
	}
	return SystemCodex{Path: real, Kind: "binary", Selectable: trustedCodexPath(path) && nativeCodex(real)}, true
}
func (d *codexDiscovery) bin(dir string) {
	if !filepath.IsAbs(dir) || d.ctx.Err() != nil {
		return
	}
	dir = filepath.Clean(dir)
	if d.binDirs[dir] {
		return
	}
	d.binDirs[dir] = true
	if filepath.Base(dir) == "bin" {
		prefix := filepath.Dir(dir)
		d.pkg(filepath.Join(prefix, "lib", "node_modules", "@openai", "codex"), "npm", prefix)
	}
	if runtime.GOOS == "windows" {
		d.pkg(filepath.Join(dir, "node_modules", "@openai", "codex"), "npm", "")
	}
	if v, ok := pathCodex(dir); ok {
		prior, seen := d.seen[v.Path]
		if d.probe && v.Selectable && v.Version == "" && (!seen || !d.results[prior].Selectable) {
			// Only probe native binaries in trusted ownership trees. Never run
			// shell shims merely to populate the version picker.
			m := CodexManager{renv: d.env}
			ctx, cancel := context.WithTimeout(d.ctx, 2*time.Second)
			v.Version, _ = m.verify(ctx, v.Path)
			cancel()
			v.Selectable = v.Version != ""
		}
		d.add(v)
	}
}

// Expand a single known layout level. Do not walk repositories, npm caches,
// shared volumes or arbitrary pnpm content-addressable stores.
func eachCodexDir(ctx context.Context, parent string, fn func(string)) {
	if ctx.Err() != nil || !filepath.IsAbs(parent) {
		return
	}
	f, e := openDiscoveryDir(parent)
	if e != nil {
		return
	}
	defer f.Close()
	for ctx.Err() == nil {
		entries, err := f.ReadDir(128)
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				fn(filepath.Join(parent, entry.Name()))
			}
		}
		if err != nil {
			return
		}
	}
}
func (d *codexDiscovery) pnpm(home string) {
	if !filepath.IsAbs(home) {
		return
	}
	eachCodexDir(d.ctx, filepath.Join(home, "global"), func(dir string) {
		d.pkg(filepath.Join(dir, "node_modules", "@openai", "codex"), "pnpm", "")
		if strings.HasPrefix(filepath.Base(dir), "v") {
			eachCodexDir(d.ctx, dir, func(group string) { d.pkg(filepath.Join(group, "node_modules", "@openai", "codex"), "pnpm", "") })
		}
	})
}
func (d *codexDiscovery) home(home string) {
	if !filepath.IsAbs(home) || home == "/" {
		return
	}
	for _, dir := range []string{".local/bin", ".npm-global/bin", ".local/share/npm/bin", ".cargo/bin", "bin"} {
		d.bin(filepath.Join(home, dir))
	}
	for _, root := range []string{".nvm/versions/node", ".local/share/fnm/node-versions", ".asdf/installs/nodejs", ".local/share/mise/installs/node", ".volta/tools/image/node"} {
		eachCodexDir(d.ctx, filepath.Join(home, root), func(v string) {
			if strings.Contains(root, "fnm/") {
				v = filepath.Join(v, "installation")
			}
			d.bin(filepath.Join(v, "bin"))
		})
	}
	d.pnpm(filepath.Join(home, ".local", "share", "pnpm"))
	d.standalone(filepath.Join(home, ".codex"))
}

// The official installer retains immutable release directories and updates
// a "current" symlink. Identify old versions from their named layouts too.
func (d *codexDiscovery) standalone(home string) {
	if !filepath.IsAbs(home) {
		return
	}
	for _, pkg := range []string{"standalone", "app-server-daemon"} {
		eachCodexDir(d.ctx, filepath.Join(home, "packages", pkg, "releases"), func(release string) {
			name := filepath.Base(release)
			for _, suffix := range []string{"-x86_64-unknown-linux-musl", "-aarch64-unknown-linux-musl", "-x86_64-unknown-linux-gnu", "-aarch64-unknown-linux-gnu", "-x86_64-pc-windows-msvc", "-aarch64-pc-windows-msvc"} {
				v, ok := strings.CutSuffix(name, suffix)
				if !ok || !validCodexReleaseVersion(v) {
					continue
				}
				file := "codex"
				if runtime.GOOS == "windows" {
					file += ".exe"
				}
				p := filepath.Join(release, "bin", file)
				real := executablePath(p)
				if real == "" {
					return
				}
				arch := strings.Split(suffix, "-")[1]
				compatible := (arch == "x86_64" && runtime.GOARCH == "amd64") || (arch == "aarch64" && runtime.GOARCH == "arm64")
				d.add(SystemCodex{Version: v, Path: real, Kind: "binary", Selectable: compatible && filepath.Base(real) == file && trustedCodexPath(p) && nativeCodex(real)})
				return
			}
		})
	}
}

func scanSystemCodex(ctx context.Context, env *RuntimeEnv, probeNative bool) []SystemCodex {
	d := codexDiscovery{ctx: ctx, env: env, probe: probeNative, seen: map[string]int{}, binDirs: map[string]bool{}, packages: map[string]bool{}}
	for _, dir := range filepath.SplitList(env.Path) {
		d.bin(dir)
	}
	for _, dir := range filepath.SplitList(os.Getenv("CODEX_SCAN_PATHS")) {
		d.bin(dir)
	}
	if prefix := envOr("NPM_CONFIG_PREFIX", os.Getenv("npm_config_prefix")); filepath.IsAbs(prefix) {
		d.bin(filepath.Join(prefix, "bin"))
	}
	d.pnpm(os.Getenv("PNPM_HOME"))
	d.bin(os.Getenv("CODEX_INSTALL_DIR"))
	d.standalone(os.Getenv("CODEX_HOME"))
	if nvm := os.Getenv("NVM_DIR"); filepath.IsAbs(nvm) {
		eachCodexDir(ctx, filepath.Join(nvm, "versions", "node"), func(dir string) { d.bin(filepath.Join(dir, "bin")) })
	}
	d.home(env.Home)
	eachCodexDir(ctx, env.CodexHome, func(path string) {
		user := filepath.Base(path)
		_, err := strconv.ParseUint(user, 10, 32)
		if err == nil || user == "local" {
			d.home(path)
		}
	})
	if runtime.GOOS == "linux" {
		for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/opt/bin", "/opt/codex/bin"} {
			d.bin(dir)
		}
		eachCodexDir(ctx, "/var/apps", func(dir string) {
			if strings.HasPrefix(filepath.Base(dir), "nodejs_") {
				d.bin(filepath.Join(dir, "target", "bin"))
			}
		})
		// fnOS can put users' actual homes on mounted volumes. NSS/passwd
		// locations are authoritative and avoid traversing the volume trees.
		if f, e := os.Open("/etc/passwd"); e == nil {
			sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
			for ctx.Err() == nil && sc.Scan() {
				fields := strings.Split(sc.Text(), ":")
				if len(fields) != 7 {
					continue
				}
				uid, err := strconv.ParseUint(fields[2], 10, 32)
				if err == nil && (uid == 0 || (uid >= 1000 && uid != 65534) || strings.HasPrefix(fields[5], "/vol")) {
					d.home(fields[5])
				}
			}
			f.Close()
		}
	}
	slices.SortFunc(d.results, func(a, b SystemCodex) int {
		if c := compareCodexVersions(b.Version, a.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return d.results
}
