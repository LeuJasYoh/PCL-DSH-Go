//go:build linux

// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAdminPermissionsLinux 验证 admin.json 与 initial-password 权限为 0600。
func TestAdminPermissionsLinux(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	if err := admin.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	paths := map[string]string{
		"admin.json":       filepath.Join(dataRoot, "config", "admin.json"),
		"initial-password": filepath.Join(dataRoot, InitialPasswordFileName),
	}
	for name, path := range paths {
		stat, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s 失败: %v", name, err)
		}
		if perm := stat.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s 权限 = %04o，期望 0600", name, perm)
		}
	}
	configDir, err := os.Stat(filepath.Join(dataRoot, "config"))
	if err != nil {
		t.Fatalf("stat config 目录失败: %v", err)
	}
	if perm := configDir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config 目录权限 = %04o，期望 0700", perm)
	}
}

// TestTrashPermissionsLinux 验证 trash 目录权限为 0700。
func TestTrashPermissionsLinux(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := filepath.Join(dataRoot, "instances", "i-0123456789ab")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatalf("创建实例目录失败: %v", err)
	}
	if _, err := MoveToTrash(dataRoot, "instance", src); err != nil {
		t.Fatalf("MoveToTrash 失败: %v", err)
	}
	stat, err := os.Stat(filepath.Join(dataRoot, TrashDirName))
	if err != nil {
		t.Fatalf("stat trash 目录失败: %v", err)
	}
	if perm := stat.Mode().Perm(); perm != 0o700 {
		t.Fatalf("trash 目录权限 = %04o，期望 0700", perm)
	}
}
