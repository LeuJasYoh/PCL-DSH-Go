// SPDX-License-Identifier: MIT

//go:build !linux

package config

import "os"

// lockFileHandle 是 Linux 之外的占位实现：只保证可编译，不提供真实排他语义。
// M0 目标平台是 Linux；Windows 仅用于开发与单元测试。
func lockFileHandle(f *os.File) error {
	_ = f
	return nil
}

// unlockFileHandle 是 Linux 之外的占位实现，与 lockFileHandle 对应。
func unlockFileHandle(f *os.File) error {
	_ = f
	return nil
}
