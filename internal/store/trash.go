// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// TrashDirName 是 data-root 下的回收站目录名。
	TrashDirName      = "trash"
	trashKindInstance = "instance"
	trashKindRuntime  = "runtime"
)

// MoveToTrash 把已存在的 srcPath 移到 <data-root>/trash/<kind>-<name>-<unixMillis>；
// kind 只接受 instance 或 runtime；返回目标路径。目标重名时追加随机后缀。
// 源路径不存在、跨设备（EXDEV）或权限不足时返回可操作错误，不留下半移动状态。
func MoveToTrash(dataRoot, kind, srcPath string) (string, error) {
	return moveToTrash(dataRoot, kind, srcPath, time.Now().UnixMilli())
}

// moveToTrash 是 MoveToTrash 的可注入时间实现（便于测试重名后缀逻辑）。
func moveToTrash(dataRoot, kind, srcPath string, nowMillis int64) (string, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return "", fmt.Errorf("%w: data-root 不能为空", ErrInvalidInput)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析 data-root 失败: %w", err)
	}
	if kind != trashKindInstance && kind != trashKindRuntime {
		return "", fmt.Errorf("%w: kind 只支持 instance 或 runtime（当前 %q）", ErrInvalidInput, kind)
	}
	src := strings.TrimSpace(srcPath)
	if src == "" {
		return "", fmt.Errorf("%w: 待移入 trash 的路径不能为空", ErrInvalidInput)
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("解析源路径失败: %w", err)
	}
	srcAbs = filepath.Clean(srcAbs)
	trashDir := filepath.Join(rootAbs, TrashDirName)
	if isWithin(rootAbs, srcAbs) {
		return "", fmt.Errorf("%w: 不能移动 data-root 本身或其上级目录（%s）", ErrInvalidInput, srcAbs)
	}
	if isWithin(srcAbs, trashDir) {
		return "", fmt.Errorf("%w: 源路径已位于 trash 目录中（%s）", ErrInvalidInput, srcAbs)
	}
	if _, err := os.Lstat(srcAbs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("待移入 trash 的路径不存在：%s", srcAbs)
		}
		return "", fmt.Errorf("检查待移入 trash 的路径 %s 失败: %w", srcAbs, err)
	}
	name := filepath.Base(srcAbs)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("%w: 无法从路径 %q 推导 trash 名称", ErrInvalidInput, srcAbs)
	}
	name = sanitizeTrashName(name)
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return "", fmt.Errorf("创建 trash 目录 %s 失败: %w", trashDir, err)
	}
	base := fmt.Sprintf("%s-%s-%d", kind, name, nowMillis)
	dest := filepath.Join(trashDir, base)
	for attempt := 0; attempt < 8; attempt++ {
		_, statErr := os.Lstat(dest)
		switch {
		case errors.Is(statErr, fs.ErrNotExist):
			if err := os.Rename(srcAbs, dest); err != nil {
				if errors.Is(err, syscall.EXDEV) {
					return "", fmt.Errorf("跨设备移动 %s 到 trash 失败；请确认 data-root 与源路径在同一文件系统: %w", srcAbs, err)
				}
				return "", fmt.Errorf("移动 %s 到 trash（%s）失败，请检查文件权限与目标占用情况: %w", srcAbs, dest, err)
			}
			return dest, nil
		case statErr != nil:
			return "", fmt.Errorf("检查 trash 目标 %s 失败: %w", dest, statErr)
		}
		// 目标已存在：追加随机后缀重试（保留原名称便于恢复）。
		suffix, err := randomTrashSuffix()
		if err != nil {
			return "", err
		}
		dest = filepath.Join(trashDir, base+"-"+suffix)
	}
	return "", fmt.Errorf("trash 目标名称冲突过多，无法移动 %s", srcAbs)
}

// isWithin 判断 path 是否等于 dir 或位于 dir 之下（基于清理后的绝对路径）。
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sanitizeTrashName 只保留文件名安全字符，避免控制字符/路径分隔符进入 trash 名称。
func sanitizeTrashName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "item"
	}
	return builder.String()
}

// randomTrashSuffix 生成 6 位十六进制随机后缀。
func randomTrashSuffix() (string, error) {
	var buf [3]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成 trash 随机后缀失败: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
