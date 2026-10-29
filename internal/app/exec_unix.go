//go:build unix

// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"os/exec"
	"syscall"
)

// prepareInstallCommand puts pnpm into its own process group so a cancelled
// import can terminate the whole pnpm tree (pnpm spawns node child processes).
func prepareInstallCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateInstallCommand kills the pnpm process group.
func terminateInstallCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return cmd.Process.Kill()
	}
	return nil
}
