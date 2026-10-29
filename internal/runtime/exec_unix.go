// SPDX-License-Identifier: MIT

//go:build unix

package runtime

import (
	"os/exec"
	"syscall"
)

// configureCommandProcessGroup places helper commands in their own process
// group so a timeout can terminate every descendant (pnpm spawns children).
func configureCommandProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateCommandTree kills the helper process group.
func terminateCommandTree(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	// Negative pid signals the whole process group established above.
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
		return command.Process.Kill()
	}
	return nil
}
