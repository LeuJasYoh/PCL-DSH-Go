// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package plugin

import "os/exec"

// configurePluginProcessGroup is a no-op on Windows (development only).
func configurePluginProcessGroup(*exec.Cmd) {}

// terminatePluginCommandTree stops the bridge process. Descendants are not
// tracked on Windows; release deployments run on Linux.
func terminatePluginCommandTree(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
