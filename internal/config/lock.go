// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LockFileName 是 data-root 下的单实例锁文件名。
const LockFileName = ".lock"

// lockFile 持有 data-root/.lock 的排他锁。
type lockFile struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// AcquireLock 获取 data-root/.lock 的排他 flock（Linux 使用 syscall.Flock，其他平台为空实现）。
// 拿不到锁时返回包含锁文件路径的清晰错误；返回的 io.Closer 关闭即释放锁，可重复调用。
func AcquireLock(dataRoot string) (io.Closer, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, errors.New("data-root 不能为空，无法创建单实例锁")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析 data-root 绝对路径失败：%w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败：%w", root, err)
	}
	path := filepath.Join(root, LockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s 失败：%w", path, err)
	}
	if err := lockFileHandle(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("数据目录已被另一个 pcl-dsh-go 进程占用（锁文件 %s）：%w", path, err)
	}
	// 在锁内写入 PID 便于诊断；失败不影响加锁结果。
	if err := f.Truncate(0); err == nil {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
			_ = f.Sync()
		}
	}
	return &lockFile{file: f, path: path}, nil
}

// Close 释放锁并关闭锁文件；可重复调用。
func (l *lockFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := unlockFileHandle(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return fmt.Errorf("释放锁文件 %s 失败：%w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭锁文件 %s 失败：%w", l.path, closeErr)
	}
	return nil
}
