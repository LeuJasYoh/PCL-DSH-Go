// SPDX-License-Identifier: MIT

//go:build unix

package plugin

import (
	"os/exec"
	"syscall"
)

// configurePluginProcessGroup puts the plugin CLI into its own process group so
// a timeout kills pnpm and its children too.
func configurePluginProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminatePluginCommandTree kills the helper process group.
func terminatePluginCommandTree(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
		return command.Process.Kill()
	}
	return nil
}
