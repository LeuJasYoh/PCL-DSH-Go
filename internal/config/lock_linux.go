// SPDX-License-Identifier: MIT

//go:build linux

package config

import (
	"os"
	"syscall"
)

// lockFileHandle 使用非阻塞排他 flock 获取单实例锁。
func lockFileHandle(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFileHandle 释放 flock（Close 本身也会释放，这里显式释放便于诊断）。
func unlockFileHandle(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
