// SPDX-License-Identifier: MIT

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssembleLaunchCommandProfileAndAppOptions(t *testing.T) {
	t.Parallel()
	nodePath := filepath.Join("opt", "node", "bin", "node")
	entryPath := filepath.Join("data", "runtimes", "0.2.1-rc.3", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if !filepath.IsAbs(nodePath) {
		nodePath = filepath.Join(os.TempDir(), nodePath)
		entryPath = filepath.Join(os.TempDir(), entryPath)
	}
	capabilities := Capabilities{
		LauncherOptions: []string{"--profile", "--help"},
		AppOptions:      []string{"--port", "--host", "--no-open"},
	}
	command, err := assembleLaunchCommand(nodePath, entryPath, capabilities, "web", 3080)
	if err != nil {
		t.Fatalf("assembleLaunchCommand() error = %v", err)
	}
	want := []string{nodePath, entryPath, "--profile", "web", "--port", "3080", "--host", "127.0.0.1", "--no-open"}
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %v, want %v", command, want)
	}
	for _, forbidden := range []string{"--patch", "--account", "--api-key", "--token"} {
		for _, argument := range command {
			if argument == forbidden {
				t.Fatalf("command injected account-related argument %q: %v", forbidden, command)
			}
		}
	}
}

func TestAssembleLaunchCommandWebAlias(t *testing.T) {
	t.Parallel()
	base := absoluteTestPaths(t)
	capabilities := Capabilities{
		LauncherOptions: []string{"--help"},
		AppOptions:      []string{"--port"},
		WebAlias:        true,
	}
	command, err := assembleLaunchCommand(base.node, base.entry, capabilities, "web", 3080)
	if err != nil {
		t.Fatalf("assembleLaunchCommand() error = %v", err)
	}
	want := []string{base.node, base.entry, "web", "--port", "3080"}
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %v, want %v", command, want)
	}

	// A non-web profile cannot use the web alias.
	if _, err := assembleLaunchCommand(base.node, base.entry, capabilities, "custom", 3080); err == nil {
		t.Fatalf("assembleLaunchCommand() accepted a non-web profile with only a web alias")
	}
}

func TestAssembleLaunchCommandFailClosedWithoutPort(t *testing.T) {
	t.Parallel()
	base := absoluteTestPaths(t)
	capabilities := Capabilities{
		LauncherOptions: []string{"--profile"},
		AppOptions:      []string{"--host"},
	}
	_, err := assembleLaunchCommand(base.node, base.entry, capabilities, "web", 3080)
	if err == nil {
		t.Fatalf("assembleLaunchCommand() succeeded without confirmed --port support")
	}
	if !strings.Contains(err.Error(), "--port") {
		t.Fatalf("error = %v, want a --port fail-closed message", err)
	}
}

func TestAssembleLaunchCommandNoLaunchEntry(t *testing.T) {
	t.Parallel()
	base := absoluteTestPaths(t)
	capabilities := Capabilities{LauncherOptions: []string{"--help"}, AppOptions: []string{"--port"}}
	if _, err := assembleLaunchCommand(base.node, base.entry, capabilities, "web", 3080); err == nil {
		t.Fatalf("assembleLaunchCommand() succeeded without a profile selector")
	}
}

func TestAssembleLaunchCommandValidation(t *testing.T) {
	t.Parallel()
	base := absoluteTestPaths(t)
	capabilities := Capabilities{LauncherOptions: []string{"--profile"}, AppOptions: []string{"--port"}}
	cases := []struct {
		name   string
		mutate func(*string, *string, *Capabilities, *string, *int)
	}{
		{"node empty", func(n, e *string, c *Capabilities, p *string, port *int) { *n = "" }},
		{"node relative", func(n, e *string, c *Capabilities, p *string, port *int) { *n = "node" }},
		{"entry empty", func(n, e *string, c *Capabilities, p *string, port *int) { *e = "" }},
		{"profile", func(n, e *string, c *Capabilities, p *string, port *int) { *p = "bad/profile" }},
		{"port zero", func(n, e *string, c *Capabilities, p *string, port *int) { *port = 0 }},
		{"port high", func(n, e *string, c *Capabilities, p *string, port *int) { *port = 65536 }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			nodePath, entryPath, caps, profile, port := base.node, base.entry, capabilities, "web", 3080
			tc.mutate(&nodePath, &entryPath, &caps, &profile, &port)
			if _, err := assembleLaunchCommand(nodePath, entryPath, caps, profile, port); err == nil {
				t.Fatalf("assembleLaunchCommand() succeeded for %s", tc.name)
			}
		})
	}
}

func TestAssembleLaunchCommandCustomProfileOrder(t *testing.T) {
	t.Parallel()
	base := absoluteTestPaths(t)
	capabilities := Capabilities{
		LauncherOptions: []string{"--profile"},
		AppOptions:      []string{"--port", "--host"},
	}
	command, err := assembleLaunchCommand(base.node, base.entry, capabilities, "my-profile", 4096)
	if err != nil {
		t.Fatalf("assembleLaunchCommand() error = %v", err)
	}
	want := []string{base.node, base.entry, "--profile", "my-profile", "--port", "4096", "--host", "127.0.0.1"}
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %v, want %v (profile selector must precede app options)", command, want)
	}
}

type testAbsolutePaths struct {
	node  string
	entry string
}

func absoluteTestPaths(t *testing.T) testAbsolutePaths {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "runtimes", "0.2.1-rc.3", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	return testAbsolutePaths{
		node:  filepath.Join(root, "tools", "node", "bin", "node"),
		entry: entry,
	}
}

func TestBuildLaunchEnvironmentWhitelist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	options := launchEnvOptions{
		DataRoot:       root,
		NodePath:       filepath.Join(root, "tools", "node", "bin", "node"),
		PnpmHome:       filepath.Join(root, "tools", "pnpm"),
		ShimsDirectory: filepath.Join(root, "tools", "shims"),
		DSHHome:        filepath.Join(root, "instances", "i-0123456789ab", "dsh-home"),
		AgentsHome:     filepath.Join(root, "instances", "i-0123456789ab", "agents-home"),
		Registry:       "https://registry.npmjs.org",
		Home:           filepath.Join(root, "instances", "i-0123456789ab", "workspace"),
	}
	environment, err := buildLaunchEnvironment(options)
	if err != nil {
		t.Fatalf("buildLaunchEnvironment() error = %v", err)
	}
	values := environmentMap(t, environment)
	expected := map[string]string{
		"HOME":                  options.Home,
		"DSH_HOME":              options.DSHHome,
		"DSH_AGENTS_HOME":       options.AgentsHome,
		"HDSL_NPM_REGISTRY":     "https://registry.npmjs.org",
		"NPM_CONFIG_REGISTRY":   "https://registry.npmjs.org",
		"NPM_CONFIG_CACHE":      filepath.Join(root, "cache", "npm"),
		"PNPM_HOME":             options.PnpmHome,
		"PNPM_CONFIG_STORE_DIR": filepath.Join(root, "cache", "pnpm-store"),
		"NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS": "false",
		"NPM_CONFIG_PACKAGE_MANAGER_STRICT":          "false",
		"PNPM_CONFIG_AUTO_INSTALL_PEERS":             "true",
		"XDG_CACHE_HOME":                             filepath.Join(root, "cache"),
		"DSH_TELEMETRY_MODE":                         "DISABLED",
		"PATH": strings.Join([]string{
			filepath.Dir(options.NodePath),
			options.ShimsDirectory,
			options.PnpmHome,
		}, string(os.PathListSeparator)),
	}
	for key, want := range expected {
		if got := values[key]; got != want {
			t.Fatalf("environment[%q] = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NO_PROXY"} {
		if _, ok := values[key]; ok {
			t.Fatalf("proxy variable %q set without a proxy", key)
		}
	}
	if len(environment) != len(expected) {
		t.Fatalf("environment has %d entries, want %d: %v", len(environment), len(expected), environment)
	}
}

func TestBuildLaunchEnvironmentProxyAndInstallHome(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	options := launchEnvOptions{
		DataRoot:       root,
		NodePath:       filepath.Join(root, "tools", "node", "bin", "node"),
		PnpmHome:       filepath.Join(root, "tools", "pnpm"),
		ShimsDirectory: filepath.Join(root, "tools", "shims"),
		DSHHome:        filepath.Join(root, "staging", ".hdsl-install-home"),
		AgentsHome:     filepath.Join(root, "staging", "agents-home"),
		Registry:       "http://127.0.0.1:4873/prefix/",
		Proxy:          "http://127.0.0.1:7890",
		Home:           filepath.Join(root, "staging", ".hdsl-install-home"),
	}
	environment, err := buildLaunchEnvironment(options)
	if err != nil {
		t.Fatalf("buildLaunchEnvironment() error = %v", err)
	}
	values := environmentMap(t, environment)
	if got := values["HOME"]; got != options.Home {
		t.Fatalf("HOME = %q, want %q", got, options.Home)
	}
	if got := values["NPM_CONFIG_REGISTRY"]; got != "http://127.0.0.1:4873/prefix" {
		t.Fatalf("registry = %q, want trailing slash stripped", got)
	}
	expectedProxyValues := map[string]string{
		"HTTP_PROXY":             "http://127.0.0.1:7890",
		"HTTPS_PROXY":            "http://127.0.0.1:7890",
		"NPM_CONFIG_PROXY":       "http://127.0.0.1:7890",
		"NPM_CONFIG_HTTPS_PROXY": "http://127.0.0.1:7890",
		"NO_PROXY":               ProxyEnvironment,
	}
	for key, want := range expectedProxyValues {
		if got := values[key]; got != want {
			t.Fatalf("environment[%q] = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(values["NO_PROXY"], "127.0.0.1") || !strings.Contains(values["NO_PROXY"], "::1") {
		t.Fatalf("NO_PROXY = %q, want loopback bypass", values["NO_PROXY"])
	}
}

func TestBuildLaunchEnvironmentValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	valid := launchEnvOptions{
		DataRoot:   root,
		NodePath:   filepath.Join(root, "node"),
		PnpmHome:   filepath.Join(root, "pnpm"),
		DSHHome:    filepath.Join(root, "dsh-home"),
		AgentsHome: filepath.Join(root, "agents-home"),
		Home:       filepath.Join(root, "workspace"),
	}
	cases := []struct {
		name   string
		mutate func(*launchEnvOptions)
	}{
		{"data root", func(o *launchEnvOptions) { o.DataRoot = "" }},
		{"node path", func(o *launchEnvOptions) { o.NodePath = "" }},
		{"dsh home", func(o *launchEnvOptions) { o.DSHHome = "" }},
		{"agents home", func(o *launchEnvOptions) { o.AgentsHome = "" }},
		{"home empty", func(o *launchEnvOptions) { o.Home = "" }},
		{"home relative", func(o *launchEnvOptions) { o.Home = "workspace" }},
		{"registry", func(o *launchEnvOptions) { o.Registry = "ftp://registry" }},
		{"proxy", func(o *launchEnvOptions) { o.Proxy = "socks5://127.0.0.1:1080" }},
		{"proxy userinfo", func(o *launchEnvOptions) { o.Proxy = "http://user:pass@127.0.0.1:7890" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			options := valid
			tc.mutate(&options)
			if _, err := buildLaunchEnvironment(options); err == nil {
				t.Fatalf("buildLaunchEnvironment() succeeded for %s", tc.name)
			}
		})
	}
}

func TestBuildLaunchPathHostPath(t *testing.T) {
	t.Parallel()
	nodePath := filepath.Join("opt", "node", "bin", "node")
	basePath := strings.Join([]string{
		filepath.Dir(nodePath),
		filepath.Join("data", "tools", "shims"),
		filepath.Join("tools", "pnpm"),
	}, string(os.PathListSeparator))
	if got := buildLaunchPath(nodePath, filepath.Join("data", "tools", "shims"), filepath.Join("tools", "pnpm"), ""); got != basePath {
		t.Fatalf("buildLaunchPath(no host) = %q, want %q", got, basePath)
	}
	if strings.HasSuffix(basePath, string(os.PathListSeparator)) {
		t.Fatalf("PATH must not end with a separator when HostPath is empty: %q", basePath)
	}
	// docs/DSH_INTEGRATION 1.2: <node-bin>:<shims>:<pnpm>:<原 PATH>.
	hostPath := strings.Join([]string{"/usr/local/bin", "/usr/bin", "/bin"}, string(os.PathListSeparator))
	want := basePath + string(os.PathListSeparator) + hostPath
	if got := buildLaunchPath(nodePath, filepath.Join("data", "tools", "shims"), filepath.Join("tools", "pnpm"), hostPath); got != want {
		t.Fatalf("buildLaunchPath(with host) = %q, want %q", got, want)
	}
	if got := buildLaunchPath(nodePath, filepath.Join("data", "tools", "shims"), filepath.Join("tools", "pnpm"), "   "); got != basePath {
		t.Fatalf("buildLaunchPath(blank host) = %q, want %q", got, basePath)
	}
}

func TestBuildLaunchEnvironmentHostPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hostPath := strings.Join([]string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/bin"}, string(os.PathListSeparator))
	options := launchEnvOptions{
		DataRoot:       root,
		NodePath:       filepath.Join(root, "tools", "node", "bin", "node"),
		PnpmHome:       filepath.Join(root, "tools", "pnpm"),
		ShimsDirectory: filepath.Join(root, "tools", "shims"),
		DSHHome:        filepath.Join(root, "dsh-home"),
		AgentsHome:     filepath.Join(root, "agents-home"),
		Registry:       "https://registry.npmjs.org",
		Home:           filepath.Join(root, "workspace"),
		HostPath:       hostPath,
	}
	environment, err := buildLaunchEnvironment(options)
	if err != nil {
		t.Fatalf("buildLaunchEnvironment() error = %v", err)
	}
	values := environmentMap(t, environment)
	wantPath := strings.Join([]string{
		filepath.Dir(options.NodePath),
		options.ShimsDirectory,
		options.PnpmHome,
		hostPath,
	}, string(os.PathListSeparator))
	if got := values["PATH"]; got != wantPath {
		t.Fatalf("PATH = %q, want %q", got, wantPath)
	}
}

func TestDerivePnpmHome(t *testing.T) {
	t.Parallel()
	if got, want := derivePnpmHome(filepath.Join("opt", "tools"), "/data"), filepath.Join("opt", "tools", "pnpm"); got != want {
		t.Fatalf("derivePnpmHome(toolRoot) = %q, want %q", got, want)
	}
	if got, want := derivePnpmHome("", "/data"), filepath.Join("/data", "tools", "pnpm"); got != want {
		t.Fatalf("derivePnpmHome(fallback) = %q, want %q", got, want)
	}
}

func TestValidateProxy(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"", "http://127.0.0.1:7890", "https://proxy.example.com:8443", "http://proxy.example.com"} {
		if err := validateProxy(valid); err != nil {
			t.Fatalf("validateProxy(%q) error = %v", valid, err)
		}
	}
	for _, invalid := range []string{"127.0.0.1:7890", "socks5://127.0.0.1:1080", "http://user:pass@127.0.0.1:7890", "http://127.0.0.1:7890/?x=1"} {
		if err := validateProxy(invalid); err == nil {
			t.Fatalf("validateProxy(%q) succeeded, want error", invalid)
		}
	}
}

func environmentMap(t *testing.T, environment []string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		index := strings.IndexByte(entry, '=')
		if index <= 0 {
			t.Fatalf("malformed environment entry %q", entry)
		}
		key := entry[:index]
		if _, exists := values[key]; exists {
			t.Fatalf("duplicate environment key %q", key)
		}
		values[key] = entry[index+1:]
	}
	return values
}
