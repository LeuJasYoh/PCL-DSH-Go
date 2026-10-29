// SPDX-License-Identifier: MIT

//go:build linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInitDataRootPermissions 验证 Linux 下 data-root 目录权限为 0700。
func TestInitDataRootPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	dirs, err := InitDataRoot(Config{DataRoot: root})
	if err != nil {
		t.Fatalf("InitDataRoot 失败：%v", err)
	}
	dirsToCheck := []string{
		dirs.DataRoot, dirs.ConfigDir, dirs.InstancesDir, dirs.RuntimesDir,
		dirs.CacheDir, dirs.LogsDir, dirs.TrashDir,
		filepath.Join(dirs.CacheDir, "pnpm-store"),
		filepath.Join(dirs.CacheDir, "npm"),
		filepath.Join(dirs.CacheDir, "registry-metadata"),
		filepath.Join(dirs.CacheDir, "probes"),
		filepath.Join(dirs.CacheDir, "uploads"),
		filepath.Join(dirs.CacheDir, "exports"),
		filepath.Join(dirs.CacheDir, "pack-staging"),
	}
	for _, dir := range dirsToCheck {
		stat, err := os.Stat(dir)
		if err != nil {
			t.Errorf("Stat %s 失败：%v", dir, err)
			continue
		}
		if perm := stat.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s 权限 = %o, 期望 700", dir, perm)
		}
	}
}
