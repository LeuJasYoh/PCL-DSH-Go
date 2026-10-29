// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTrashSource 创建待移入 trash 的目录与内容文件，返回目录路径。
func makeTrashSource(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "workspace"), 0o700); err != nil {
		t.Fatalf("创建源目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "workspace", "note.txt"), []byte("keep-me"), 0o600); err != nil {
		t.Fatalf("写入源文件失败: %v", err)
	}
	return path
}

func TestMoveToTrashInstance(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := makeTrashSource(t, filepath.Join(dataRoot, "instances", "i-0123456789ab"))
	dest, err := MoveToTrash(dataRoot, "instance", src)
	if err != nil {
		t.Fatalf("MoveToTrash 失败: %v", err)
	}
	wantPrefix := "instance-i-0123456789ab-"
	base := filepath.Base(dest)
	if !strings.HasPrefix(base, wantPrefix) {
		t.Fatalf("trash 目标名 %q 应以 %q 开头", base, wantPrefix)
	}
	if !strings.HasPrefix(dest, filepath.Join(dataRoot, TrashDirName)+string(os.PathSeparator)) {
		t.Fatalf("trash 目标 %q 不在 trash 目录下", dest)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("移动后源目录应不存在，Stat err = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "workspace", "note.txt"))
	if err != nil {
		t.Fatalf("读取 trash 内文件失败: %v", err)
	}
	if string(data) != "keep-me" {
		t.Fatalf("trash 内文件内容 = %q", string(data))
	}
}

func TestMoveToTrashRuntime(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := makeTrashSource(t, filepath.Join(dataRoot, "runtimes", "0.2.1-rc.3"))
	dest, err := MoveToTrash(dataRoot, "runtime", src)
	if err != nil {
		t.Fatalf("MoveToTrash 失败: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(dest), "runtime-0.2.1-rc.3-") {
		t.Fatalf("trash 目标名 %q 不符合 runtime-<name>-<millis>", filepath.Base(dest))
	}
	if _, err := os.Stat(filepath.Join(dest, "workspace", "note.txt")); err != nil {
		t.Fatalf("runtime 移入 trash 后内容缺失: %v", err)
	}
}

func TestMoveToTrashInvalidKind(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := makeTrashSource(t, filepath.Join(dataRoot, "instances", "i-0123456789ab"))
	for _, kind := range []string{"", "instances", "pack", "../instance", "instance "} {
		if _, err := MoveToTrash(dataRoot, kind, src); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("kind=%q 应返回 ErrInvalidInput，实际 %v", kind, err)
		}
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("非法 kind 不应移动源目录: %v", err)
	}
}

func TestMoveToTrashMissingSource(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := filepath.Join(dataRoot, "instances", "i-000000000000")
	_, err := MoveToTrash(dataRoot, "instance", src)
	if err == nil {
		t.Fatal("源路径不存在时应返回错误")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误 %q 未说明源路径不存在", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(dataRoot, TrashDirName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("源不存在时不应创建 trash 目录，Stat err = %v", statErr)
	}
}

func TestMoveToTrashRejectsDataRoot(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		t.Fatalf("创建 data-root 失败: %v", err)
	}
	if _, err := MoveToTrash(dataRoot, "instance", dataRoot); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("移动 data-root 本身应返回 ErrInvalidInput，实际 %v", err)
	}
	parent := filepath.Dir(dataRoot)
	if _, err := MoveToTrash(dataRoot, "instance", parent); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("移动 data-root 上级目录应返回 ErrInvalidInput，实际 %v", err)
	}
	if _, err := MoveToTrash(dataRoot, "instance", filepath.Join(dataRoot, TrashDirName)); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("移动 trash 目录本身应返回 ErrInvalidInput，实际 %v", err)
	}
}

func TestMoveToTrashRejectsTrashSubtree(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := makeTrashSource(t, filepath.Join(dataRoot, TrashDirName, "instance-old-1"))
	if _, err := MoveToTrash(dataRoot, "instance", src); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("移动 trash 内路径应返回 ErrInvalidInput，实际 %v", err)
	}
}

func TestMoveToTrashConflictSuffix(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	first := makeTrashSource(t, filepath.Join(dataRoot, "instances", "a", "same-name"))
	second := makeTrashSource(t, filepath.Join(dataRoot, "instances", "b", "same-name"))
	dest1, err := moveToTrash(dataRoot, "instance", first, 1750000000000)
	if err != nil {
		t.Fatalf("第一次 moveToTrash 失败: %v", err)
	}
	dest2, err := moveToTrash(dataRoot, "instance", second, 1750000000000)
	if err != nil {
		t.Fatalf("第二次 moveToTrash 失败: %v", err)
	}
	wantBase := "instance-same-name-1750000000000"
	if got := filepath.Base(dest1); got != wantBase {
		t.Fatalf("第一个目标名 = %q，期望 %q", got, wantBase)
	}
	if dest1 == dest2 {
		t.Fatalf("重名冲突时目标路径不应相同: %q", dest1)
	}
	base2 := filepath.Base(dest2)
	if !strings.HasPrefix(base2, wantBase+"-") {
		t.Fatalf("第二个目标名 %q 应带随机后缀", base2)
	}
	if suffix := strings.TrimPrefix(base2, wantBase+"-"); len(suffix) != 6 {
		t.Fatalf("随机后缀 %q 长度 = %d，期望 6", suffix, len(suffix))
	}
	for _, dest := range []string{dest1, dest2} {
		if _, err := os.Stat(filepath.Join(dest, "workspace", "note.txt")); err != nil {
			t.Fatalf("目标 %q 内容缺失: %v", dest, err)
		}
	}
}

func TestMoveToTrashSanitizesName(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	src := makeTrashSource(t, filepath.Join(dataRoot, "instances", "weird name!"))
	dest, err := moveToTrash(dataRoot, "instance", src, 42)
	if err != nil {
		t.Fatalf("moveToTrash 失败: %v", err)
	}
	if got := filepath.Base(dest); got != "instance-weird_name_-42" {
		t.Fatalf("trash 名称清理结果 = %q，期望 %q", got, "instance-weird_name_-42")
	}
}

func TestMoveToTrashEmptyArguments(t *testing.T) {
	if _, err := MoveToTrash("  ", "instance", "/tmp/x"); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空 data-root 应返回 ErrInvalidInput，实际 %v", err)
	}
	dataRoot := filepath.Join(t.TempDir(), "data")
	if _, err := MoveToTrash(dataRoot, "instance", "  "); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空源路径应返回 ErrInvalidInput，实际 %v", err)
	}
}
