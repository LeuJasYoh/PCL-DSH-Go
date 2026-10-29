//go:build !linux

// SPDX-License-Identifier: MIT

package process

import (
	"errors"
	"os"
	"os/exec"
)

// 非 Linux 平台（仅保证可编译；真实进程测试在 Linux/WSL 执行）：
// 无进程组与 /proc starttime 跟踪，停止时仅能对根进程发信号。

func configureProcessGroup(*exec.Cmd) {}

func snapshotDescendants(int) []descendant { return nil }

func processAlive(int, uint64) bool { return false }

func terminatePID(int, uint64) error { return nil }

func killPID(int, uint64) error { return nil }

func terminateProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Signal(os.Interrupt); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return process.Kill()
	}
	return nil
}

func killProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
