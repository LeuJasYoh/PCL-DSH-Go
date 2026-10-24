// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// staticFixture builds a minimal built-WebUI directory and returns it.
func staticFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "dist")
	files := map[string]string{
		"index.html":              "<!doctype html><html><body><div id=\"app\">SPA-INDEX-v1</div></body></html>",
		"assets/app-abc123.js":    "console.log(\"asset-v1\");",
		"assets/app-abc123.css":   "body{color:#123}",
		"assets/chunk-def456.mjs": "export const x = 1;",
		"favicon.webp":            "RIFF-webp-bytes",
		"licenses/LICENSE.txt":    "GPL-3.0-or-later",
		"nested/asset.json":       "{\"nested\":true}",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}
	return root
}

// newStaticEnv builds an API env serving the fixture directory.
func newStaticEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	root := staticFixture(t)
	env := newTestEnv(t, false, func(deps *Deps) { deps.StaticDir = root })
	return env, root
}

func TestStaticSPAFallback(t *testing.T) {
	env, _ := newStaticEnv(t)

	// SPA routes are anonymous (the page itself redirects to login) and always
	// answer with the current index.html, uncached.
	for _, target := range []string{"/", "/instances", "/packs/detail/42", "/logs", "/settings/"} {
		rec := env.request(t, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body = %s", target, rec.Code, rec.Body.String())
		}
		if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("GET %s Content-Type = %q, want text/html", target, contentType)
		}
		if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q, want no-store", target, cacheControl)
		}
		if !strings.Contains(rec.Body.String(), "SPA-INDEX-v1") {
			t.Fatalf("GET %s body = %q, want the index page", target, rec.Body.String())
		}
		assertSecurityHeaders(t, rec)
	}
}

func TestStaticFilesAndMIME(t *testing.T) {
	env, _ := newStaticEnv(t)

	cases := []struct {
		target    string
		wantBody  string
		wantType  string
		wantCache string
	}{
		{"/assets/app-abc123.js", "asset-v1", "javascript", immutableAssetCache},
		{"/assets/app-abc123.css", "color:#123", "text/css", immutableAssetCache},
		{"/assets/chunk-def456.mjs", "export const x", "javascript", immutableAssetCache},
		{"/favicon.webp", "RIFF-webp-bytes", "image/webp", "no-store"},
		{"/licenses/LICENSE.txt", "GPL-3.0-or-later", "text/plain", "no-store"},
		{"/nested/asset.json", "nested", "application/json", "no-store"},
	}
	for _, testCase := range cases {
		rec := env.request(t, http.MethodGet, testCase.target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body = %s", testCase.target, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), testCase.wantBody) {
			t.Fatalf("GET %s body = %q, want %q", testCase.target, rec.Body.String(), testCase.wantBody)
		}
		if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, testCase.wantType) {
			t.Fatalf("GET %s Content-Type = %q, want %q", testCase.target, contentType, testCase.wantType)
		}
		if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != testCase.wantCache {
			t.Fatalf("GET %s Cache-Control = %q, want %q", testCase.target, cacheControl, testCase.wantCache)
		}
	}
}

func TestStaticHeadRequests(t *testing.T) {
	env, _ := newStaticEnv(t)

	rec := env.request(t, http.MethodHead, "/assets/app-abc123.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD asset = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD asset body = %q, want empty", rec.Body.String())
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != immutableAssetCache {
		t.Fatalf("HEAD asset Cache-Control = %q", cacheControl)
	}

	rec = env.request(t, http.MethodHead, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD / = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD / body = %q, want empty", rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("HEAD / Content-Type = %q", contentType)
	}
}

func TestStaticMissingAssetsAre404(t *testing.T) {
	env, _ := newStaticEnv(t)

	for _, target := range []string{
		"/assets/missing.js",
		"/assets/index.js",
		"/missing.png",
		"/favicon.ico",
		"/assets/",
		"/licenses",
		"/nested",
	} {
		rec := env.request(t, http.MethodGet, target, nil)
		body := decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
		if strings.Contains(rec.Body.String(), "SPA-INDEX-v1") {
			t.Fatalf("GET %s fell back to the SPA, want 404", target)
		}
		if body.TraceID == "" {
			t.Fatalf("GET %s missing traceId", target)
		}
	}
}

func TestStaticTraversalRejected(t *testing.T) {
	env, root := newStaticEnv(t)
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET-OUTSIDE-ROOT"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	targets := []string{
		"/../secret.txt",
		"/assets/../../secret.txt",
		"/%2e%2e/secret.txt",
		"/assets/%2e%2e/%2e%2e/secret.txt",
		"/assets/app%00.js",
		"/%00",
		"/assets/app%5c..%5c..%5csecret.txt",
	}
	for _, target := range targets {
		rec := env.request(t, http.MethodGet, target, nil)
		decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)
		if strings.Contains(rec.Body.String(), "TOP-SECRET-OUTSIDE-ROOT") {
			t.Fatalf("GET %s leaked a file outside the static root", target)
		}
		if location := rec.Header().Get("Location"); location != "" {
			t.Fatalf("GET %s was redirected to %q, want 400", target, location)
		}
	}
}

func TestStaticMethodNotAllowed(t *testing.T) {
	env, _ := newStaticEnv(t)
	for _, target := range []string{"/", "/instances", "/assets/app-abc123.js"} {
		rec := env.request(t, http.MethodPost, target, nil)
		decodeError(t, rec, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed)
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Fatalf("POST %s Allow = %q, want GET, HEAD", target, allow)
		}
	}
	rec := env.request(t, http.MethodPut, "/instances", nil)
	decodeError(t, rec, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed)
}

func TestStaticIndexIsReadFreshEveryRequest(t *testing.T) {
	env, root := newStaticEnv(t)
	indexPath := filepath.Join(root, "index.html")

	rec := env.request(t, http.MethodGet, "/", nil)
	if !strings.Contains(rec.Body.String(), "SPA-INDEX-v1") {
		t.Fatalf("first index body = %q", rec.Body.String())
	}
	if err := os.WriteFile(indexPath, []byte("<html>SPA-INDEX-v2</html>"), 0o644); err != nil {
		t.Fatalf("rewrite index.html error = %v", err)
	}
	rec = env.request(t, http.MethodGet, "/", nil)
	if !strings.Contains(rec.Body.String(), "SPA-INDEX-v2") {
		t.Fatalf("second index body = %q, want the rebuilt file", rec.Body.String())
	}
}

func TestStaticAPIRoutesStillWin(t *testing.T) {
	env, _ := newStaticEnv(t)

	// /api/** keeps its own routing/auth semantics (not SPA, not static).
	rec := env.request(t, http.MethodGet, "/api/health", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "\"status\":\"UP\"") {
		t.Fatalf("GET /api/health = %d %s", rec.Code, rec.Body.String())
	}
	rec = env.request(t, http.MethodGet, "/api/state", nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodGet, "/api/does-not-exist", nil)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "SPA-INDEX-v1") {
		t.Fatalf("unknown API path fell into the SPA: %d %s", rec.Code, rec.Body.String())
	}
	// /workbench keeps the session gate.
	rec = env.request(t, http.MethodGet, "/workbench/i-aaaaaaaaaaaa/", nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
}

func TestStaticUnconfiguredKeepsPlaceholder(t *testing.T) {
	env := newTestEnv(t, false)

	rec := env.request(t, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PCL-DSH-Go") {
		t.Fatalf("placeholder body = %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SPA-INDEX") {
		t.Fatal("placeholder mode served a SPA index")
	}
	// Non-root paths are not served in placeholder mode (no SPA fallback).
	rec = env.request(t, http.MethodGet, "/instances", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("placeholder GET /instances = %d, want 404", rec.Code)
	}
}

func TestStaticDirValidation(t *testing.T) {
	env := newTestEnv(t, false)
	base := Deps{
		Config:   env.api.cfg,
		Logger:   env.api.logger,
		Dirs:     env.api.dirs,
		Store:    env.store,
		Security: env.security,
		Jobs:     env.jobs,
		App:      env.app,
		Pack:     env.pack,
		Plugins:  env.plugins,
	}

	relative := base
	relative.StaticDir = "web/dist"
	if _, err := New(relative); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("relative static dir error = %v, want 绝对路径", err)
	}

	missing := base
	missing.StaticDir = filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := New(missing); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing static dir error = %v, want 不存在", err)
	}

	file := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	notDir := base
	notDir.StaticDir = file
	if _, err := New(notDir); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("file static dir error = %v, want 不是目录", err)
	}

	valid := base
	valid.StaticDir = staticFixture(t)
	if _, err := New(valid); err != nil {
		t.Fatalf("New(valid static dir) error = %v", err)
	}
}
