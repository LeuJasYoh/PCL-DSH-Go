// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// staticAssetPrefix is the directory of Vite-hashed bundles (long-lived cache).
const staticAssetPrefix = "assets/"

// immutableAssetCache is the documented cache policy for hashed bundles
// (docs/M4_INTERFACES.md 2).
const immutableAssetCache = "public, max-age=31536000, immutable"

// extraStaticTypes covers the frontend extensions the Go MIME table may miss.
var extraStaticTypes = map[string]string{
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".mjs":   "text/javascript; charset=utf-8",
	".map":   "application/json; charset=utf-8",
}

// resolveStaticDir validates the optional --server.static-dir value
// (docs/M4_INTERFACES.md 2): empty means "no SPA configured"; otherwise the
// directory must exist, be a real directory and be absolute (config.Load
// resolves relative flag values first). The returned path is symlink-resolved
// and used as the containment root.
func resolveStaticDir(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("静态资源目录必须是绝对路径：%s", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("静态资源目录不存在：%s", dir)
		}
		return "", fmt.Errorf("检查静态资源目录 %s 失败：%w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("静态资源路径不是目录：%s", dir)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("解析静态资源目录真实路径失败：%w", err)
	}
	return resolved, nil
}

// serveStatic serves the built WebUI (Vue SPA) from the configured directory
// (docs/M4_INTERFACES.md 2):
//
//   - existing regular files are served with their MIME type (hashed assets get
//     an immutable one-year cache, everything else no-store);
//   - extensionless unknown routes fall back to index.html (read from disk on
//     every request, no-store) so the SPA router can handle them;
//   - unknown paths with an extension are a real 404 (never the SPA);
//   - directories are 404 (no listings), traversal/control characters are 400,
//     non-GET/HEAD requests are 405.
func (a *API) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		a.writeError(w, r, apierr.New(http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed,
			"静态资源只支持 GET/HEAD 请求。"))
		return
	}
	if unsafeStaticURL(r.URL) {
		a.writeError(w, r, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "静态资源路径无效。"))
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if rel != "" {
		target := filepath.Join(a.staticRoot, filepath.FromSlash(rel))
		info, statErr := os.Stat(target)
		switch {
		case statErr == nil && info.IsDir():
			// Never list directories.
			a.writeError(w, r, notFoundError("资源不存在。"))
			return
		case statErr == nil && info.Mode().IsRegular():
			resolved, resolveErr := filepath.EvalSymlinks(target)
			if resolveErr != nil || !staticPathWithinRoot(a.staticRoot, resolved) {
				// A symlink escaping the static root is treated as missing.
				a.writeError(w, r, notFoundError("资源不存在。"))
				return
			}
			a.serveStaticFile(w, r, resolved, rel, info)
			return
		case statErr == nil:
			a.writeError(w, r, notFoundError("资源不存在。"))
			return
		}
		// Missing path: a file extension means a real asset was requested.
		if path.Ext(rel) != "" {
			a.writeError(w, r, notFoundError("资源不存在。"))
			return
		}
	}
	a.serveStaticIndex(w, r)
}

// serveStaticFile streams one regular file with range/HEAD support.
func (a *API) serveStaticFile(w http.ResponseWriter, r *http.Request, filePath, rel string, info fs.FileInfo) {
	file, err := os.Open(filePath)
	if err != nil {
		a.writeError(w, r, notFoundError("资源不存在。"))
		return
	}
	defer func() { _ = file.Close() }()

	header := w.Header()
	if contentType := staticContentType(rel); contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if strings.HasPrefix(rel, staticAssetPrefix) {
		header.Set("Cache-Control", immutableAssetCache)
	} else {
		header.Set("Cache-Control", "no-store")
	}
	// ServeContent handles HEAD, Range and conditional requests; Content-Type
	// and Cache-Control set above are preserved.
	http.ServeContent(w, r, path.Base(rel), info.ModTime(), file)
}

// serveStaticIndex serves index.html as the SPA entry point. The file is read
// on every request so a rebuilt frontend is picked up without restarting the
// manager, and index responses are never cached.
func (a *API) serveStaticIndex(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(a.staticRoot, "index.html"))
	if err != nil {
		a.writeError(w, r, notFoundError("页面不存在。"))
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// unsafeStaticURL rejects traversal and control characters in SPA requests. It
// mirrors the workbench raw-path guard: ServeMux cleanPath would otherwise turn
// a ".." path into a redirect instead of the documented 400.
func unsafeStaticURL(u *url.URL) bool {
	if u == nil {
		return true
	}
	paths := []string{u.Path}
	if escaped := u.EscapedPath(); escaped != "" && escaped != u.Path {
		decoded, err := url.PathUnescape(escaped)
		if err != nil {
			return true
		}
		paths = append(paths, decoded)
	}
	for _, candidate := range paths {
		if unsafeRawPath(candidate) {
			return true
		}
		// Backslashes are path separators on Windows: never accept them in URLs.
		if strings.IndexByte(candidate, 0x5c) >= 0 {
			return true
		}
	}
	return false
}

// withStaticRawPathGuard rejects traversal and control characters in SPA page
// requests before http.ServeMux can cleanPath them into a redirect
// (docs/M4_INTERFACES.md 2). API and /workbench paths keep their own guards.
func (a *API) withStaticRawPathGuard(next http.Handler) http.Handler {
	if a.staticRoot == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) && !isWorkbenchPath(r.URL.Path) && unsafeStaticURL(r.URL) {
			a.writeError(w, r, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "静态资源路径无效。"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticContentType picks a MIME type from the request path.
func staticContentType(rel string) string {
	ext := strings.ToLower(path.Ext(rel))
	if extra, ok := extraStaticTypes[ext]; ok {
		return extra
	}
	return mime.TypeByExtension(ext)
}

// staticPathWithinRoot reports whether target (symlink-resolved) stays inside
// the resolved static root.
func staticPathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
