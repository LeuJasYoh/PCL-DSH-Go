//go:build linux

// SPDX-License-Identifier: MIT

package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionsLinux(t *testing.T) {
	s, dir, _ := newTestStore(t)
	mustAdd(t, s, dir, "export.zip", "data", "application/zip")

	info, err := os.Stat(filepath.Join(dir, metadataFilename))
	if err != nil {
		t.Fatalf("stat %s 失败: %v", metadataFilename, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s 权限 = %04o，期望 0600", metadataFilename, got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat 存储目录失败: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("存储目录权限 = %04o，期望 0700", got)
	}
}
