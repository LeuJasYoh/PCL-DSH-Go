// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLockCreatesFileAndReleases(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	lock, err := AcquireLock(root)
	if err != nil {
		t.Fatalf("AcquireLock 失败：%v", err)
	}
	path := filepath.Join(root, LockFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取锁文件失败：%v", err)
	}
	if !strings.Contains(string(data), "pid=") {
		t.Errorf("锁文件内容 %q 缺少 pid 信息", string(data))
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("重复 Close 失败：%v", err)
	}
	again, err := AcquireLock(root)
	if err != nil {
		t.Fatalf("释放后再次 AcquireLock 失败：%v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("再次 Close 失败：%v", err)
	}
}

func TestAcquireLockEmptyDataRoot(t *testing.T) {
	if _, err := AcquireLock("  "); err == nil {
		t.Fatal("空 data-root 应报错")
	}
}
