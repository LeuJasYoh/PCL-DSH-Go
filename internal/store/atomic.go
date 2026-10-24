// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile 以“同目录临时文件 → fsync → rename”的方式原子替换目标文件，
// 避免崩溃或写失败时留下半截配置。rename 后对目录做一次 best-effort fsync。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("设置临时文件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换目标文件失败: %w", err)
	}
	cleanup = false

	// 目录 fsync 保证 rename 持久化；部分平台/文件系统不支持，失败不影响结果。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
