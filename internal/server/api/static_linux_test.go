//go:build linux

// SPDX-License-Identifier: MIT

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// TestStaticSymlinkEscapeRejectedLinux ensures a symlink inside the static root
// cannot serve files from outside it (docs/M4_INTERFACES.md 2).
func TestStaticSymlinkEscapeRejectedLinux(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET-OUTSIDE-ROOT"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	root := staticFixture(t)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	env := newTestEnv(t, false, func(deps *Deps) { deps.StaticDir = root })

	rec := env.request(t, http.MethodGet, "/escape/secret.txt", nil)
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
	if containsSecret := len(rec.Body.Bytes()) > 0 && string(rec.Body.Bytes()) != "" &&
		bytesContains(rec.Body.Bytes(), []byte("TOP-SECRET-OUTSIDE-ROOT")); containsSecret {
		t.Fatalf("symlink escape leaked file content: %s", rec.Body.String())
	}
}

// bytesContains is bytes.Contains without importing bytes twice at call sites.
func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if string(haystack[index:index+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
