//go:build !unix

// SPDX-License-Identifier: MIT

package app

import "os/exec"

// 非 Unix 平台（仅保证可编译；真实 pnpm 只在 Linux/WSL 运行）：无进程组，
// 取消时只能杀掉 pnpm 根进程。

func prepareInstallCommand(*exec.Cmd) {}

func terminateInstallCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
