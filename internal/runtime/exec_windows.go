// SPDX-License-Identifier: MIT

//go:build windows

package runtime

import "os/exec"

// configureCommandProcessGroup is a no-op on Windows. Real installs run on
// Linux; Windows is only used for development and unit tests.
func configureCommandProcessGroup(*exec.Cmd) {}

// terminateCommandTree stops the helper process. Descendants of pnpm are not
// tracked on Windows; this path is not used by the release runtime.
func terminateCommandTree(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
