// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// traversalTargets are raw request targets that must be rejected with 400 by the
// raw-path guard instead of being redirected (307) by ServeMux's cleanPath.
var traversalTargets = []string{
	"/workbench/i-aaaaaaaaaaaa/../../etc/passwd",
	"/workbench/i-aaaaaaaaaaaa/%2e%2e/etc/passwd",
	"/workbench/i-aaaaaaaaaaaa/..%2fetc/passwd",
	"/workbench/i-aaaaaaaaaaaa/%2e%2e%2fetc%2fpasswd",
	"/workbench/i-aaaaaaaaaaaa/assets/../../api/state",
	"/workbench/i-aaaaaaaaaaaa/a%2e%2eb/c",
	"/workbench/i-aaaaaaaaaaaa/%00",
	"/workbench/i-aaaaaaaaaaaa/%0d%0aX-Injected:%201",
	"/workbench/i-aaaaaaaaaaaa/%7f",
	"http://127.0.0.1:18080/workbench/i-aaaaaaaaaaaa/../../etc/passwd",
}

func TestWorkbenchRawPathGuardRejectsTraversal(t *testing.T) {
	env := newTestEnv(t, false)

	for _, target := range traversalTargets {
		rec := env.request(t, http.MethodGet, target, nil)
		body := decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)
		if !strings.Contains(body.Message, "路径") {
			t.Fatalf("%s message = %q, want a Chinese path error", target, body.Message)
		}
		// The error must be JSON with a trace id, never a redirect or body leak.
		if location := rec.Header().Get("Location"); location != "" {
			t.Fatalf("%s was redirected to %q, want 400", target, location)
		}
		if strings.Contains(rec.Body.String(), "root:") {
			t.Fatalf("%s body leaked file content: %s", target, rec.Body.String())
		}
	}
	if hits := env.workbench.HitCount(); hits != 0 {
		t.Fatalf("workbench handler reached %d times for traversal paths", hits)
	}

	// The guard runs before the session guard: an authenticated traversal is
	// still a 400 (not 200/404/409).
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodGet, traversalTargets[0], nil, sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)
	if hits := env.workbench.HitCount(); hits != 0 {
		t.Fatalf("workbench handler reached %d times for an authenticated traversal", hits)
	}

	// Other methods on traversal paths are rejected as well.
	rec = env.request(t, http.MethodPost, traversalTargets[0], nil, sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)
}

func TestWorkbenchRawPathGuardAllowsNormalPaths(t *testing.T) {
	env := newTestEnv(t, false)
	normal := []string{
		"/workbench/i-aaaaaaaaaaaa/",
		"/workbench/i-aaaaaaaaaaaa/assets/app.min.js",
		"/workbench/i-aaaaaaaaaaaa/api/foo?x=1",
	}

	// Unauthenticated normal workbench paths keep the documented 401: the guard
	// must not intercept them.
	for _, target := range normal {
		rec := env.request(t, http.MethodGet, target, nil)
		decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	}
	if hits := env.workbench.HitCount(); hits != 0 {
		t.Fatalf("workbench handler reached %d times anonymously", hits)
	}

	// Authenticated: they reach the workbench handler unchanged.
	sess := env.loginAs(t)
	for _, target := range normal {
		rec := env.request(t, http.MethodGet, target, nil, sess.withSession())
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body = %s", target, rec.Code, rec.Body.String())
		}
	}
	if hits := env.workbench.HitCount(); hits != len(normal) {
		t.Fatalf("workbench handler hits = %d, want %d", hits, len(normal))
	}
}

func TestUnsafeRawPath(t *testing.T) {
	cases := map[string]bool{
		"/workbench/i-x/":                  false,
		"/workbench/i-x/assets/app.min.js": false,
		"/workbench/i-x/../etc":            true,
		"/workbench/i-x/a..b":              true,
		// A percent-encoded traversal is only visible after net/url decoding;
		// at this string layer "%2e%2e" contains no dot characters.
		"/workbench/i-x/%2e%2e":      false,
		"/workbench/i-x/../x/%2e%2e": true,
		"/workbench/i-x/\x00":        true,
		"/workbench/i-x/\x7f":        true,
		"/workbench/i-x/\r\n":        true,
	}
	for path, want := range cases {
		if got := unsafeRawPath(path); got != want {
			t.Fatalf("unsafeRawPath(%q) = %v, want %v", path, got, want)
		}
	}
}
