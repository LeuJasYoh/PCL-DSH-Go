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

// addTestArtifact writes a file into the artifact store directory and registers
// it, mirroring what pack export does before returning the artifact id.
func addTestArtifact(t *testing.T, env *testEnv, filename string, content []byte) string {
	t.Helper()
	path := filepath.Join(env.dirs.CacheDir, "exports", filename)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	stored, err := env.artifacts.Add(filename, path, "application/zip", int64(len(content)))
	if err != nil {
		t.Fatalf("artifact.Add() error = %v", err)
	}
	return stored.ID
}

func TestArtifactDownload(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	content := []byte("dspack-payload-bytes")
	id := addTestArtifact(t, env, "export-test.dspack", content)

	// Anonymous downloads are rejected by the session guard.
	rec := env.request(t, http.MethodGet, "/api/artifacts/"+id, nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)

	// Logged-in download: streamed attachment with no-store and the API's
	// inherited security headers.
	rec = env.request(t, http.MethodGet, "/api/artifacts/"+id, nil, sess.withSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact download = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); string(got) != string(content) {
		t.Fatalf("artifact body = %q, want %q", got, content)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", contentType)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment") || !strings.Contains(disposition, "export-test.dspack") {
		t.Fatalf("Content-Disposition = %q, want attachment with the filename", disposition)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	assertSecurityHeaders(t, rec)

	// HEAD is served by the same GET pattern without a body.
	rec = env.request(t, http.MethodHead, "/api/artifacts/"+id, nil, sess.withSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact HEAD = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("artifact HEAD body = %q, want empty", rec.Body.String())
	}
}

func TestArtifactDownloadUnknownOrMalformed(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	for _, id := range []string{"ffffffffffff", "not-an-id", "000000000000"} {
		rec := env.request(t, http.MethodGet, "/api/artifacts/"+id, nil, sess.withSession())
		decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
	}
}

func TestArtifactRequiresSession(t *testing.T) {
	env := newTestEnv(t, false)
	id := addTestArtifact(t, env, "anon.dspack", []byte("x"))
	rec := env.request(t, http.MethodGet, "/api/artifacts/"+id, nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
}
