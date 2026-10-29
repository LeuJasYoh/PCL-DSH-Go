// SPDX-License-Identifier: MIT

package plugin

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// pluginBridgeSource is the embedded Node bridge (docs/DSH_INTEGRATION.md 8).
//
// The bridge routes every pnpm spawn of the DSH CLI to the bundled pnpm.cjs and
// rejects shell-based commands (fail-closed) so plugin management never runs
// through the host shell.
//
//go:embed plugin_bridge.mjs
var pluginBridgeSource string

// pluginBridgeFileName is the bridge filename under <dataRoot>/tools/shims.
const pluginBridgeFileName = "hdsl-plugin-bridge.mjs"

// ensurePluginBridge writes the embedded Node bridge and returns its path.
func (s *Service) ensurePluginBridge() (string, error) {
	directory := filepath.Join(s.dirs.DataRoot, "tools", "shims")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("创建插件桥目录失败：%w", err)
	}
	path := filepath.Join(directory, pluginBridgeFileName)
	if err := os.WriteFile(path, []byte(pluginBridgeSource), 0o600); err != nil {
		return "", fmt.Errorf("写入插件桥失败：%w", err)
	}
	return path, nil
}
