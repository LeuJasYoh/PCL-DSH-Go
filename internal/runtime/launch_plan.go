// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/ports"
)

// ProxyEnvironment is the loopback-safe NO_PROXY value injected into DSH
// child processes when an outbound proxy is configured.
const ProxyEnvironment = "localhost,127.0.0.1,::1"

// launchEnvOptions parameterises the DSH child-process environment. The
// environment is always built from a blank base: nothing is inherited from the
// manager process (docs/DSH_INTEGRATION.md 1.2).
type launchEnvOptions struct {
	// DataRoot is the manager data root; cache paths are derived from it.
	DataRoot string
	// NodePath is the absolute node executable used for the child.
	NodePath string
	// PnpmHome is the vendored pnpm directory (PNPM_HOME).
	PnpmHome string
	// ShimsDirectory is <dataRoot>/tools/shims, where the pnpm shim lives.
	ShimsDirectory string
	// DSHHome and AgentsHome are the instance-specific DSH directories.
	DSHHome    string
	AgentsHome string
	// Registry is the npm registry used by DSH and pnpm.
	Registry string
	// Proxy is an optional http(s) proxy applied to every package operation.
	Proxy string
	// Home is the instance workspace for DSH launches and a throw-away directory
	// for install/restore helpers.
	Home string
	// HostPath is the manager process PATH (os.Getenv("PATH")). It is appended
	// to the constructed PATH as required by docs/DSH_INTEGRATION.md 1.2:
	// <node-bin>:<data>/tools/shims:<pnpm-home>:<原 PATH>.
	HostPath string
}

// buildLaunchEnvironment builds the whitelisted environment for a DSH child
// process. It returns a deterministic, fully-specified slice (no inheritance).
func buildLaunchEnvironment(options launchEnvOptions) ([]string, error) {
	if options.DataRoot == "" || !filepath.IsAbs(options.DataRoot) {
		return nil, fmt.Errorf("数据目录必须是绝对路径")
	}
	if options.NodePath == "" || !filepath.IsAbs(options.NodePath) {
		return nil, fmt.Errorf("Node 路径必须是绝对路径")
	}
	if options.DSHHome == "" || options.AgentsHome == "" {
		return nil, fmt.Errorf("缺少 DSH_HOME 或 DSH_AGENTS_HOME")
	}
	if options.Home == "" || !filepath.IsAbs(options.Home) {
		return nil, fmt.Errorf("HOME 必须是绝对路径")
	}
	registry, err := normalizeRegistry(options.Registry)
	if err != nil {
		return nil, err
	}
	if err := validateProxy(options.Proxy); err != nil {
		return nil, err
	}
	cacheDirectory := filepath.Join(options.DataRoot, "cache")
	environment := make([]string, 0, 23)
	environment = append(environment,
		"HOME="+options.Home,
		"DSH_HOME="+options.DSHHome,
		"DSH_AGENTS_HOME="+options.AgentsHome,
		"HDSL_NPM_REGISTRY="+registry,
		"NPM_CONFIG_REGISTRY="+registry,
		"NPM_CONFIG_CACHE="+filepath.Join(cacheDirectory, "npm"),
		"PNPM_HOME="+options.PnpmHome,
		"PNPM_CONFIG_STORE_DIR="+filepath.Join(cacheDirectory, "pnpm-store"),
		"NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS=false",
		"NPM_CONFIG_PACKAGE_MANAGER_STRICT=false",
		"PNPM_CONFIG_AUTO_INSTALL_PEERS=true",
		"XDG_CACHE_HOME="+cacheDirectory,
		"DSH_TELEMETRY_MODE=DISABLED",
		"PATH="+buildLaunchPath(options.NodePath, options.ShimsDirectory, options.PnpmHome, options.HostPath),
	)
	if options.Proxy != "" {
		environment = append(environment,
			"HTTP_PROXY="+options.Proxy,
			"HTTPS_PROXY="+options.Proxy,
			"NPM_CONFIG_PROXY="+options.Proxy,
			"NPM_CONFIG_HTTPS_PROXY="+options.Proxy,
			"NO_PROXY="+ProxyEnvironment,
		)
	}
	return environment, nil
}

// buildLaunchPath composes PATH from the bundled node directory, the pnpm shim
// directory, PNPM_HOME and finally the manager's own PATH (docs/DSH_INTEGRATION
// 1.2). An empty hostPath leaves no trailing separator.
func buildLaunchPath(nodePath, shimsDirectory, pnpmHome, hostPath string) string {
	entries := []string{filepath.Dir(nodePath)}
	for _, entry := range []string{shimsDirectory, pnpmHome} {
		if entry == "" {
			continue
		}
		duplicate := false
		for _, existing := range entries {
			if strings.EqualFold(existing, entry) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			entries = append(entries, entry)
		}
	}
	path := strings.Join(entries, string(os.PathListSeparator))
	if trimmedHostPath := strings.TrimSpace(hostPath); trimmedHostPath != "" {
		path += string(os.PathListSeparator) + trimmedHostPath
	}
	return path
}

// validateProxy accepts an empty proxy or a plain http(s) URL without
// credentials (the same shape the settings store validates).
func validateProxy(proxy string) error {
	trimmed := strings.TrimSpace(proxy)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("代理地址无效：%v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("代理地址无效：只支持 http(s)")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("代理地址无效：缺少主机名")
	}
	if parsed.User != nil {
		return fmt.Errorf("代理地址无效：不能包含用户名或密码")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("代理地址无效：不能包含查询串或片段")
	}
	return nil
}

// derivePnpmHome picks the PNPM_HOME directory for a located toolchain.
func derivePnpmHome(toolRoot, dataRoot string) string {
	if toolRoot != "" {
		return filepath.Join(toolRoot, "pnpm")
	}
	return filepath.Join(dataRoot, "tools", "pnpm")
}

// assembleLaunchCommand builds the DSH argv from verified capabilities
// (docs/DSH_INTEGRATION.md 4.2):
//
//	<node> <entry> [--profile <profile> | web] --port <port> [--host 127.0.0.1] [--no-open]
//
// The profile selector always precedes app options. No account/patch argument
// is ever injected.
func assembleLaunchCommand(nodePath, entryPath string, capabilities Capabilities, profile string, port int) ([]string, error) {
	if nodePath == "" || !filepath.IsAbs(nodePath) {
		return nil, fmt.Errorf("Node 路径必须是绝对路径")
	}
	if entryPath == "" || !filepath.IsAbs(entryPath) {
		return nil, fmt.Errorf("运行时入口必须是绝对路径")
	}
	if !validProfileName(profile) {
		return nil, fmt.Errorf("profile 名称无效：%q", profile)
	}
	if !ports.ValidInstance(port) {
		return nil, fmt.Errorf("端口应为 %d–%d", ports.InstanceMin, ports.Max)
	}
	command := []string{nodePath, entryPath}
	switch {
	case capabilities.ProfileSelection():
		command = append(command, "--profile", profile)
	case profile == "web" && capabilities.WebAlias:
		command = append(command, "web")
	default:
		return nil, fmt.Errorf("此运行时未提供可识别的 profile 启动参数；请查看 CLI 帮助或兼容性配置")
	}
	if !capabilities.Supports("--port") {
		return nil, fmt.Errorf("此版本尚未确认 --port 支持，无法保证实例端口隔离；请刷新检测或添加本机兼容性声明")
	}
	command = append(command, "--port", strconv.Itoa(port))
	if capabilities.Supports("--host") {
		command = append(command, "--host", "127.0.0.1")
	}
	if capabilities.Supports("--no-open") {
		command = append(command, "--no-open")
	}
	return command, nil
}
