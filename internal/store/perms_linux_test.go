//go:build linux

// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionsLinux(t *testing.T) {
	s, dataRoot := newTestStore(t)
	mustCreate(t, s, "一", 3080)
	mustCreate(t, s, "二", 3081)

	dirInfo, err := os.Stat(filepath.Join(dataRoot, "config"))
	if err != nil {
		t.Fatalf("stat config 目录失败: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config 目录权限 = %04o，期望 0700", got)
	}
	for _, name := range []string{"instances.json", "manager.json"} {
		info, err := os.Stat(filepath.Join(dataRoot, "config", name))
		if err != nil {
			t.Fatalf("stat %s 失败: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s 权限 = %04o，期望 0600", name, got)
		}
	}
}
