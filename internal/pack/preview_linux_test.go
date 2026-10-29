//go:build linux

// SPDX-License-Identifier: MIT

package pack

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPreviewUploadPermissionsLinux verifies preview uploads are 0600 inside a
// 0700 cache/uploads directory (docs/DSH_INTEGRATION.md 7.1/7.3).
func TestPreviewUploadPermissionsLinux(t *testing.T) {
	service, dirs, _, _, _ := newPackTestService(t)
	preview, err := previewArchive(t, service, "demo.zip", validDSPackProfile())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	record := lookupPreview(t, service, preview.ID)
	info, err := os.Stat(record.archivePath)
	if err != nil {
		t.Fatalf("stat 上传归档失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("上传归档权限 = %04o，期望 0600", perm)
	}
	uploadsDir := filepath.Join(dirs.CacheDir, "uploads")
	dirInfo, err := os.Stat(uploadsDir)
	if err != nil {
		t.Fatalf("stat uploads 目录失败: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("uploads 目录权限 = %04o，期望 0700", perm)
	}
}
