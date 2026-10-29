//go:build linux

// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReceiveUploadPermissionsLinux verifies the 0600 file / 0700 directory
// permissions on Linux (Windows does not model POSIX permissions).
func TestReceiveUploadPermissionsLinux(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	body, contentType := multipartUpload(t, "file", "pack.zip", []byte("payload"))
	upload, err := receiveUpload(context.Background(), uploadRequest(body, contentType), dir, maxPackUploadBytes)
	if err != nil {
		t.Fatalf("receiveUpload() error = %v", err)
	}
	defer func() { _ = os.Remove(upload.Path) }()

	fileInfo, err := os.Stat(upload.Path)
	if err != nil {
		t.Fatalf("Stat(upload) error = %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("upload file mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("upload dir mode = %04o, want 0700", got)
	}
}
