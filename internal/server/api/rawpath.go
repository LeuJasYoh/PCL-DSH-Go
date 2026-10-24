// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// withWorkbenchRawPathGuard rejects /workbench requests whose raw path carries
// dot segments or control characters before http.ServeMux can cleanPath them
// into a 301/307 redirect. docs/ACCEPTANCE.md G4 requires a 400 for such paths,
// and the reverse proxy itself rejects them as well; the ServeMux redirect
// would otherwise mask that validation with a redirect.
//
// Placement: inside withTracing (so the JSON error body carries a traceId and
// the X-Trace-Id header is present) and outside the mux. Like the proxy's own
// check, it runs before the session guard: a malformed workbench path is a bad
// request regardless of authentication.
func (a *API) withWorkbenchRawPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL == nil || !isWorkbenchPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if unsafeWorkbenchURL(r.URL) {
			a.writeError(w, r, apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "工作台路径无效。"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unsafeWorkbenchURL reports whether the decoded path or the escaped path
// (decoded once more) contains ".." or control characters. URL.Path is already
// percent-decoded by net/url, so an encoded "%2e%2e" is visible there; checking
// the escaped form as well keeps the guard robust if a caller constructs a URL
// with an explicit RawPath.
func unsafeWorkbenchURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	if unsafeRawPath(u.Path) {
		return true
	}
	if escaped := u.EscapedPath(); escaped != "" && escaped != u.Path {
		decoded, err := url.PathUnescape(escaped)
		if err != nil {
			// A path that cannot be unescaped is malformed: reject it.
			return true
		}
		if unsafeRawPath(decoded) {
			return true
		}
	}
	return false
}

// unsafeRawPath rejects ".." anywhere in the path (the same conservative rule as
// internal/server/proxy.unsafeProxyPath) and raw control characters (<0x20 or
// 0x7f) that may have arrived percent-encoded.
func unsafeRawPath(path string) bool {
	if strings.Contains(path, "..") {
		return true
	}
	for index := 0; index < len(path); index++ {
		if path[index] < 0x20 || path[index] == 0x7f {
			return true
		}
	}
	return false
}
