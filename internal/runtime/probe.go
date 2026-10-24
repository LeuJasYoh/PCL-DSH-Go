// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// launcherHelpTimeout bounds "node <entry> --help" (docs/DSH_INTEGRATION.md 4.1).
	launcherHelpTimeout = 35 * time.Second
	// appHelpTimeout bounds "node <entry> --profile web --help".
	appHelpTimeout = 60 * time.Second
	// maxCompatibilityBytes bounds the manual capability declaration file.
	maxCompatibilityBytes = 1 << 20
)

var (
	// helpOptionPattern finds option names at the start of a help line.
	// Lookahead is applied manually in optionsFromHelp because RE2 lacks it.
	helpOptionPattern = regexp.MustCompile("(?m)^[ \t]*(?:-[A-Za-z?],?[ \t]+)?(--[A-Za-z][A-Za-z0-9-]*)")
	// pluginManagementPattern detects a "plugin" launcher command.
	pluginManagementPattern = regexp.MustCompile(`(?m)(?:^[ \t]*plugin(?:[ \t]|\[)|\bdsh[ \t]+plugin[ \t])`)
	// webAliasPattern detects a "web" launcher alias command.
	webAliasPattern = regexp.MustCompile(`(?m)(?:^[ \t]*web(?:[ \t]|\[)|\bdsh[ \t]+web(?:[ \t]|$))`)
	// optionFlagPattern validates manual option declarations.
	optionFlagPattern = regexp.MustCompile("^--[A-Za-z][A-Za-z0-9-]*$")
	// profilePattern matches the profile names accepted by the instance store.
	profilePattern = regexp.MustCompile("^[A-Za-z0-9._-]{1,128}$")
)

// Capabilities is the frozen capability descriptor for an installed runtime
// (docs/M0_INTERFACES.md). Options hold CLI flags observed in --help output or
// declared manually in config/compatibility.json; unknown flags are never
// guessed.
type Capabilities struct {
	LauncherOptions  []string
	AppOptions       []string
	PluginManagement bool
	WebAlias         bool
	Profile          string
	EntryPath        string
	Version          string

	// appHelpVerified records that the web app help was successfully parsed, so
	// launching must only rely on verified or manually declared options.
	appHelpVerified bool
	// warnings carries human-readable capability caveats for logs and the UI.
	warnings []string
}

// Supports reports whether the launcher or the app help declared the option.
func (c Capabilities) Supports(option string) bool {
	return containsString(c.LauncherOptions, option) || containsString(c.AppOptions, option)
}

// ProfileSelection reports whether the launcher accepts --profile <name>.
func (c Capabilities) ProfileSelection() bool {
	return containsString(c.LauncherOptions, "--profile")
}

// AppHelpVerified reports whether the web app help was parsed successfully.
func (c Capabilities) AppHelpVerified() bool { return c.appHelpVerified }

// Warnings returns the capability caveats (copy).
func (c Capabilities) Warnings() []string {
	return append([]string(nil), c.warnings...)
}

// Summary renders a one-line capability summary for installation/probe logs.
func (c Capabilities) Summary() string {
	launch := "启动入口待确认"
	switch {
	case c.ProfileSelection():
		launch = "profile 启动"
	case c.WebAlias:
		launch = "web 启动"
	}
	plugin := "未检测到插件命令"
	if c.PluginManagement {
		plugin = "原生插件管理"
	}
	port := "端口参数待确认"
	if c.Supports("--port") {
		port = "可设端口"
	}
	summary := fmt.Sprintf("Harness %s · %s · %s · %s", c.Version, launch, plugin, port)
	if len(c.warnings) > 0 {
		summary += "\n" + strings.Join(c.warnings, "\n")
	}
	return summary
}

// compatibilityFile mirrors the structure of <dataRoot>/config/compatibility.json
// (docs/DSH_INTEGRATION.md 4.1). It lets operators declare capabilities that
// --help probing could not confirm; it is never written by the manager.
type compatibilityFile struct {
	Versions map[string]compatibilityEntry `json:"versions"`
}

type compatibilityEntry struct {
	LauncherOptions  []string                        `json:"launcherOptions"`
	AppOptions       []string                        `json:"appOptions"`
	PluginManagement *bool                           `json:"pluginManagement"`
	WebAlias         *bool                           `json:"webAlias"`
	Profiles         map[string]compatibilityProfile `json:"profiles"`
}

type compatibilityProfile struct {
	AppOptions []string `json:"appOptions"`
}

// probeOptions describes one capability probe invocation.
type probeOptions struct {
	NodePath  string
	EntryPath string
	DataRoot  string
	Version   string
	// Profile selects the launch profile; "web" is the default launcher profile.
	Profile string
	// Logf receives probe diagnostics (may be nil).
	Logf func(string)
	// LauncherHelpTimeout and AppHelpTimeout are overridable for tests.
	LauncherHelpTimeout time.Duration
	AppHelpTimeout      time.Duration
}

// probeCapabilities inspects an installed runtime in a throw-away probe home and
// returns its command-line capabilities. Probe failures are fail-closed: the
// caller must not launch a runtime whose help could not be read.
func probeCapabilities(ctx context.Context, opts probeOptions) (Capabilities, error) {
	if !IsVersion(opts.Version) {
		return Capabilities{}, fmt.Errorf("版本号 %q 无效", opts.Version)
	}
	if opts.NodePath == "" || opts.EntryPath == "" {
		return Capabilities{}, fmt.Errorf("探测失败：缺少 Node 或运行时入口路径")
	}
	if !filepath.IsAbs(opts.NodePath) || !filepath.IsAbs(opts.EntryPath) {
		return Capabilities{}, fmt.Errorf("探测失败：Node 与运行时入口必须是绝对路径")
	}
	if opts.DataRoot == "" {
		return Capabilities{}, fmt.Errorf("探测失败：缺少数据目录")
	}
	if opts.Profile != "" && !validProfileName(opts.Profile) {
		return Capabilities{}, fmt.Errorf("profile 名称无效：%q", opts.Profile)
	}
	if opts.LauncherHelpTimeout <= 0 {
		opts.LauncherHelpTimeout = launcherHelpTimeout
	}
	if opts.AppHelpTimeout <= 0 {
		opts.AppHelpTimeout = appHelpTimeout
	}

	probeRoot, err := createProbeDirectory(opts.DataRoot, opts.Version)
	if err != nil {
		return Capabilities{}, err
	}
	defer func() { _ = os.RemoveAll(probeRoot) }()
	workspace := filepath.Join(probeRoot, "workspace")
	dshHome := filepath.Join(probeRoot, "dsh-home")
	agentsHome := filepath.Join(probeRoot, "agents-home")
	for _, directory := range []string{workspace, dshHome, agentsHome} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return Capabilities{}, fmt.Errorf("创建探测目录失败：%w", err)
		}
	}
	environment := probeEnvironment(opts.DataRoot, dshHome, agentsHome, opts.NodePath)

	base := []string{opts.NodePath, opts.EntryPath}
	launcherHelp, err := runHelpCommand(ctx, append(append([]string(nil), base...), "--help"), workspace, environment, opts.LauncherHelpTimeout)
	if err != nil {
		return Capabilities{}, fmt.Errorf("Harness 启动器帮助探测失败，已拒绝继续（fail-closed）：%w", err)
	}
	capabilities := capabilitiesFromHelp(opts.Version, launcherHelp, "", false)

	appHelp := ""
	appVerified := false
	if capabilities.ProfileSelection() || capabilities.WebAlias {
		appArgs := append([]string(nil), base...)
		if capabilities.ProfileSelection() {
			appArgs = append(appArgs, "--profile", "web", "--help")
		} else {
			appArgs = append(appArgs, "web", "--help")
		}
		appOutput, appErr := runHelpCommand(ctx, appArgs, workspace, environment, opts.AppHelpTimeout)
		if appErr != nil {
			emitLogf(opts.Logf, "Web 帮助探测未完成：%v", appErr)
		} else {
			appHelp = appOutput
			appVerified = looksLikeAppHelp(appOutput)
		}
	}
	capabilities = capabilitiesFromHelp(opts.Version, launcherHelp, appHelp, appVerified)

	declarations, err := loadCompatibility(opts.DataRoot)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities, err = applyCompatibility(capabilities, declarations, opts.Profile, opts.Logf)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities.EntryPath = opts.EntryPath
	capabilities.Profile = opts.Profile
	emitLogf(opts.Logf, "%s", capabilities.Summary())
	return capabilities, nil
}

// capabilitiesFromHelp parses one launcher help output (and the verified web app
// help, when available) into a Capabilities value.
func capabilitiesFromHelp(version, launcherHelp, appHelp string, appVerified bool) Capabilities {
	launcherOptions := optionsFromHelp(launcherHelp)
	appOptions := []string{}
	if appVerified {
		appOptions = optionsFromHelp(appHelp)
	}
	pluginManagement := pluginManagementPattern.MatchString(launcherHelp)
	webAlias := webAliasPattern.MatchString(launcherHelp)
	warnings := make([]string, 0, 2)
	if !containsString(launcherOptions, "--profile") && !webAlias {
		warnings = append(warnings, "未识别 profile 或 web 启动入口，请检查此版本的 CLI 帮助。")
	}
	if !appVerified {
		warnings = append(warnings, "Web 应用帮助尚未验证，不会猜测端口等参数。")
	}
	return Capabilities{
		LauncherOptions:  launcherOptions,
		AppOptions:       appOptions,
		PluginManagement: pluginManagement,
		WebAlias:         webAlias,
		Version:          version,
		appHelpVerified:  appVerified,
		warnings:         warnings,
	}
}

// optionsFromHelp extracts long option names from help output, preserving order
// and removing duplicates. A candidate is only accepted when it is followed by
// whitespace, '=', ',', '<', '[', '|' or end of line (equivalent to the reference
// implementation's lookahead).
func optionsFromHelp(help string) []string {
	seen := make(map[string]bool)
	options := make([]string, 0, 16)
	for _, match := range helpOptionPattern.FindAllStringSubmatchIndex(help, -1) {
		start, end := match[2], match[3]
		if end < len(help) && !isHelpOptionTerminator(help[end]) {
			continue
		}
		option := help[start:end]
		if seen[option] {
			continue
		}
		seen[option] = true
		options = append(options, option)
	}
	return options
}

func isHelpOptionTerminator(character byte) bool {
	switch character {
	case ' ', '\t', '\r', '\n', '=', ',', '<', '[', '|':
		return true
	default:
		return false
	}
}

// looksLikeAppHelp verifies that the app help output actually describes CLI
// usage; the reference implementation applies the same check.
func looksLikeAppHelp(output string) bool {
	lowered := strings.ToLower(output)
	return strings.Contains(lowered, "usage") || strings.Contains(lowered, "options") || strings.Contains(output, "--port")
}

// loadCompatibility reads config/compatibility.json. A missing file means "no
// manual declarations"; a malformed file is a hard error (fail-closed).
func loadCompatibility(dataRoot string) (compatibilityFile, error) {
	path := filepath.Join(dataRoot, "config", "compatibility.json")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return compatibilityFile{}, nil
		}
		return compatibilityFile{}, fmt.Errorf("无法读取 config/compatibility.json：%w", err)
	}
	if !info.Mode().IsRegular() {
		return compatibilityFile{}, fmt.Errorf("config/compatibility.json 不是普通文件")
	}
	if info.Size() > maxCompatibilityBytes {
		return compatibilityFile{}, fmt.Errorf("config/compatibility.json 过大（超过 %d 字节）", maxCompatibilityBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return compatibilityFile{}, fmt.Errorf("无法读取 config/compatibility.json：%w", err)
	}
	var declarations compatibilityFile
	if err := json.Unmarshal(data, &declarations); err != nil {
		return compatibilityFile{}, fmt.Errorf("config/compatibility.json 格式无效：%w", err)
	}
	return declarations, nil
}

// applyCompatibility merges manual declarations over the probed capabilities and
// resolves profile-specific app options. It fails closed when a custom profile
// has no verified or declared app options.
func applyCompatibility(capabilities Capabilities, declarations compatibilityFile, profile string, logf func(string)) (Capabilities, error) {
	entry, declared := declarations.Versions[capabilities.Version]
	if declared {
		if err := appendOptions(&capabilities.LauncherOptions, entry.LauncherOptions); err != nil {
			return Capabilities{}, err
		}
		if err := appendOptions(&capabilities.AppOptions, entry.AppOptions); err != nil {
			return Capabilities{}, err
		}
		if entry.PluginManagement != nil {
			capabilities.PluginManagement = *entry.PluginManagement
		}
		if entry.WebAlias != nil {
			capabilities.WebAlias = *entry.WebAlias
		}
		capabilities.warnings = append(capabilities.warnings, "已应用本机 config/compatibility.json 的人工能力声明。")
	}
	if profile != "" && profile != "web" {
		manual := []string(nil)
		if declared {
			if declaredProfile, ok := entry.Profiles[profile]; ok {
				manual = declaredProfile.AppOptions
			}
		}
		if len(manual) == 0 {
			return Capabilities{}, fmt.Errorf("自定义 profile %q 的应用参数尚未确认：请在 config/compatibility.json 的 versions.%s.profiles.%s.appOptions 声明已核实参数后重试", profile, capabilities.Version, profile)
		}
		capabilities.AppOptions = append([]string(nil), manual...)
		if err := validateOptionFlags(capabilities.AppOptions); err != nil {
			return Capabilities{}, err
		}
		capabilities.appHelpVerified = false
		capabilities.warnings = append(capabilities.warnings, fmt.Sprintf("已使用本机为 %s 指定的应用参数。", profile))
		emitLogf(logf, "已使用本机为 %s 指定的应用参数。", profile)
	}
	return capabilities, nil
}

// appendOptions validates and appends manually declared option names.
func appendOptions(target *[]string, declared []string) error {
	for _, option := range declared {
		if optionFlagPattern.MatchString(option) == false {
			return fmt.Errorf("config/compatibility.json 包含无效的参数名 %q", option)
		}
		if !containsString(*target, option) {
			*target = append(*target, option)
		}
	}
	return nil
}

func validateOptionFlags(options []string) error {
	for _, option := range options {
		if !optionFlagPattern.MatchString(option) {
			return fmt.Errorf("config/compatibility.json 包含无效的参数名 %q", option)
		}
	}
	return nil
}

// createProbeDirectory creates a unique probe root under cache/probes/<version>.
func createProbeDirectory(dataRoot, version string) (string, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return "", err
	}
	root := filepath.Join(dataRoot, "cache", "probes", version, suffix)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("创建探测目录失败：%w", err)
	}
	return root, nil
}

// probeEnvironment builds the blank-base environment for capability probes. No
// user environment or credential is inherited.
func probeEnvironment(dataRoot, dshHome, agentsHome, nodePath string) []string {
	return []string{
		"HOME=" + dshHome,
		"DSH_HOME=" + dshHome,
		"DSH_AGENTS_HOME=" + agentsHome,
		"XDG_CACHE_HOME=" + filepath.Join(dataRoot, "cache"),
		"DSH_TELEMETRY_MODE=DISABLED",
		"PATH=" + filepath.Dir(nodePath),
	}
}

// runHelpCommand runs one --help invocation and returns its merged output.
func runHelpCommand(ctx context.Context, args []string, directory string, environment []string, timeout time.Duration) (string, error) {
	result, err := runCommand(ctx, commandSpec{
		Args:    args,
		Dir:     directory,
		Env:     environment,
		Timeout: timeout,
	})
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// validProfileName validates a profile name using the same character set as the
// instance store.
func validProfileName(profile string) bool {
	return profilePattern.MatchString(profile)
}
