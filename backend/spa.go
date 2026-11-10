package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// spaHandler serves the embedded SPA assets.
func spaHandler(fsys fs.FS, baseurl string) http.Handler {
	sub, err := fs.Sub(fsys, "embed")
	if err == nil {
		fsys = sub
	}
	base := strings.TrimRight(baseurl, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if base != "" && (p == base || strings.HasPrefix(p, base+"/")) {
			p = strings.TrimPrefix(p, base)
			if p == "" {
				p = "/"
			}
		}
		reqPath := strings.TrimPrefix(p, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		// 优先直接提供存在的文件（含 .html 静态页，如 callback.html）。
		// 首页（index.html）始终注入 base href，保证资源URL带上正确的 baseurl 前缀。
		if b, err := fs.ReadFile(fsys, reqPath); err == nil {
			if reqPath == "index.html" {
				b = rewriteIndexBase(b, base)
			}
			// 构建期已为 hashed assets 生成 .gz 兄弟文件（vite.config.ts 的
			// precompressAssets）；只在客户端明确接受 gzip 时发预压缩字节。
			// index.html 不预压缩：它每次都要注入 <base>，且 entry 很小。
			if strings.HasPrefix(reqPath, "assets/") && acceptsGzip(r) {
				if gz, gzErr := fs.ReadFile(fsys, reqPath+".gz"); gzErr == nil {
					w.Header().Set("Content-Encoding", "gzip")
					serveBytes(w, r, reqPath, gz)
					return
				}
			}
			serveBytes(w, r, reqPath, b)
			return
		}
		// 不存在的路径回退到 index.html 以支持 SPA 前端路由
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		if b, err := fs.ReadFile(fsys, "index.html"); err == nil {
			b = rewriteIndexBase(b, base)
			serveBytes(w, r, "index.html", b)
			return
		}
		http.NotFound(w, r)
	})
}

func rewriteIndexBase(body []byte, base string) []byte {
	s := string(body)
	s = strings.ReplaceAll(s, `src="./assets/`, `src="`+base+`/assets/`)
	s = strings.ReplaceAll(s, `href="./assets/`, `href="`+base+`/assets/`)
	headTag := `<base href="` + base + `/">`
	if idx := strings.Index(strings.ToLower(s), "<head"); idx != -1 {
		if ci := strings.Index(s[idx:], ">"); ci != -1 {
			pos := idx + ci + 1
			return []byte(s[:pos] + headTag + s[pos:])
		}
	}
	return []byte(headTag + s)
}

// acceptsGzip 判断 Accept-Encoding 是否接受 gzip（含 q=0 的显式拒绝）。
// 只做 gzip：.gz 是构建期产物，运行时不需要 br/zstd 的压缩库。
//
// `*` 通配符按 RFC 9110 处理：**显式的 gzip 条目优先于 `*`**（`gzip;q=0, *` 是拒绝），
// 没有显式条目时才看 `*` 的 q。客户端什么都没写、或只写了别的编码时返回 false。
func acceptsGzip(r *http.Request) bool {
	wildcard := -1.0 // <0 表示客户端没写 `*`
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		q := 1.0
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if len(f) >= 2 && strings.EqualFold(f[:2], "q=") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(f[2:]), 64); err == nil {
					q = v
				}
			}
		}
		if strings.EqualFold(name, "gzip") {
			return q > 0
		}
		if name == "*" {
			wildcard = q
		}
	}
	return wildcard > 0
}

// serveBytes 输出一个内嵌静态文件，并按文件类型带上缓存策略：
//
//   - `assets/**` 是 Vite 构建的**内容哈希产物**（index-DAFlHLaA.css / LogView-xxx.js），
//     内容一变文件名就变，因此可以放心长期强缓存：升级后 index.html 会指向新文件名，
//     浏览器直接拿新的，不会用到旧字节。这一层是控制台首屏的最大收益（几个百 KB 的
//     bundle 不再每次回源）。
//   - 其余文件（index.html / callback.html 等入口文件，文件名不带哈希）必须**每次回源
//     校验**（no-cache + ETag）：缓存住就会在升级后继续指向已被替换掉的旧哈希资源，
//     表现为「重启了控制台还是旧界面」——这正是 dsh 插件 bundle 那边踩过的坑。
//     ETag 由实际输出的字节（含注入的 base href）算出，校验命中回 304，不重传正文。
func serveBytes(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	if ct := mimeTypeByExt(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		// 同一 URL 有 identity / gzip 两种表示（见 spaHandler），缓存必须按该头分流。
		w.Header().Set("Vary", "Accept-Encoding")
	} else {
		etag := etagOf(data)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// etagOf 用内容摘要生成强 ETag（内嵌 FS 没有可用的修改时间，内容摘要才是可靠校验器）。
func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagMatches 判断请求头里的 If-None-Match 是否命中当前 ETag：支持逗号分隔的多值与
// `*`，并忽略弱校验前缀（W/）—— 浏览器侧我们只发强 ETag，这里宽松匹配即可。
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		p = strings.TrimPrefix(p, "W/")
		if p == etag {
			return true
		}
	}
	return false
}

func mimeTypeByExt(ext string) string {
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".woff", ".woff2":
		return "font/woff2"
	case ".ico":
		return "image/x-icon"
	}
	return ""
}
