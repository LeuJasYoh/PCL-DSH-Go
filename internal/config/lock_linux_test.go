// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcquireLockExclusive 验证 Linux 下 flock 的真实排他语义。
func TestAcquireLockExclusive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	first, err := AcquireLock(root)
	if err != nil {
		t.Fatalf("第一次 AcquireLock 失败：%v", err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Errorf("Close 失败：%v", err)
		}
	}()
	second, err := AcquireLock(root)
	if err == nil {
		_ = second.Close()
		t.Fatal("持锁期间第二次 AcquireLock 应失败")
	}
	if !strings.Contains(err.Error(), "占用") {
		t.Errorf("错误 %q 未说明锁被占用", err.Error())
	}
}

func TestAcquireLockFileMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	lock, err := AcquireLock(root)
	if err != nil {
		t.Fatalf("AcquireLock 失败：%v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("Close 失败：%v", err)
		}
	}()
	stat, err := os.Stat(filepath.Join(root, LockFileName))
	if err != nil {
		t.Fatalf("Stat 锁文件失败：%v", err)
	}
	if perm := stat.Mode().Perm(); perm != 0o600 {
		t.Errorf("锁文件权限 = %o, 期望 600", perm)
	}
}
