// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDataRootCreatesLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	dirs, err := InitDataRoot(Config{DataRoot: root})
	if err != nil {
		t.Fatalf("InitDataRoot 失败：%v", err)
	}
	if dirs.DataRoot != filepath.Clean(root) {
		t.Errorf("DataRoot = %q, 期望 %q", dirs.DataRoot, root)
	}
	expected := map[string]string{
		"ConfigDir":    dirs.ConfigDir,
		"InstancesDir": dirs.InstancesDir,
		"RuntimesDir":  dirs.RuntimesDir,
		"CacheDir":     dirs.CacheDir,
		"LogsDir":      dirs.LogsDir,
		"TrashDir":     dirs.TrashDir,
	}
	for name, dir := range expected {
		stat, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s 目录不存在：%v", name, err)
			continue
		}
		if !stat.IsDir() {
			t.Errorf("%s = %q 不是目录", name, dir)
		}
		if !strings.HasPrefix(dir, dirs.DataRoot+string(os.PathSeparator)) {
			t.Errorf("%s = %q 不在 data-root 下", name, dir)
		}
	}
	for _, sub := range []string{"pnpm-store", "npm", "registry-metadata", "probes", "uploads", "exports", "pack-staging"} {
		if stat, err := os.Stat(filepath.Join(dirs.CacheDir, sub)); err != nil || !stat.IsDir() {
			t.Errorf("cache/%s 目录不存在（err=%v）", sub, err)
		}
	}
}

func TestInitDataRootIdempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if _, err := InitDataRoot(Config{DataRoot: root}); err != nil {
		t.Fatalf("第一次 InitDataRoot 失败：%v", err)
	}
	marker := filepath.Join(root, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("写入标记文件失败：%v", err)
	}
	if _, err := InitDataRoot(Config{DataRoot: root}); err != nil {
		t.Fatalf("第二次 InitDataRoot 失败：%v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("已有内容被破坏：%v", err)
	}
}

func TestInitDataRootFileConflict(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(root, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("准备冲突文件失败：%v", err)
	}
	_, err := InitDataRoot(Config{DataRoot: root})
	if err == nil {
		t.Fatal("data-root 是文件时 InitDataRoot 应报错")
	}
	if !strings.Contains(err.Error(), "不是目录") {
		t.Errorf("错误 %q 未说明不是目录", err.Error())
	}
}

func TestInitDataRootEmpty(t *testing.T) {
	if _, err := InitDataRoot(Config{DataRoot: "  "}); err == nil {
		t.Fatal("空 data-root 应报错")
	}
}
