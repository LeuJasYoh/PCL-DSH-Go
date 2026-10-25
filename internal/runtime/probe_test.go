// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newProbeTestOptions(t *testing.T, scenario, profile string) probeOptions {
	t.Helper()
	entry := writeFakeRuntime(t, scenario, "0.2.1-rc.3")
	return probeOptions{
		NodePath:  testNodePath(t),
		EntryPath: entry,
		DataRoot:  t.TempDir(),
		Version:   "0.2.1-rc.3",
		Profile:   profile,
	}
}

func TestProbeCapabilitiesSuccess(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioOK, "web")
	var logs []string
	options.Logf = func(line string) { logs = append(logs, line) }
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v", err)
	}
	if !capabilities.ProfileSelection() {
		t.Fatalf("ProfileSelection() = false, want true (launcher help declares --profile)")
	}
	if capabilities.Profile != "web" {
		t.Fatalf("Profile = %q, want web", capabilities.Profile)
	}
	if capabilities.Version != "0.2.1-rc.3" {
		t.Fatalf("Version = %q", capabilities.Version)
	}
	if capabilities.EntryPath != options.EntryPath {
		t.Fatalf("EntryPath = %q, want %q", capabilities.EntryPath, options.EntryPath)
	}
	if !capabilities.WebAlias {
		t.Fatalf("WebAlias = false, want true")
	}
	if !capabilities.PluginManagement {
		t.Fatalf("PluginManagement = false, want true")
	}
	if !capabilities.AppHelpVerified() {
		t.Fatalf("AppHelpVerified() = false, want true")
	}
	for _, option := range []string{"--port", "--host", "--no-open"} {
		if !capabilities.Supports(option) {
			t.Fatalf("Supports(%q) = false; app options = %v", option, capabilities.AppOptions)
		}
	}
	if len(capabilities.Warnings()) != 0 {
		t.Fatalf("warnings = %v, want none", capabilities.Warnings())
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "profile 启动") {
		t.Fatalf("logs = %q, want a capability summary", joined)
	}
	// Probe directories are throw-away cache entries.
	probeRoot := filepath.Join(options.DataRoot, "cache", "probes", "0.2.1-rc.3")
	entries, err := os.ReadDir(probeRoot)
	if err == nil && len(entries) != 0 {
		t.Fatalf("probe directory was not removed: %v", entries)
	}
}

func TestProbeCapabilitiesMinimalHelp(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioMinimal, "web")
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v", err)
	}
	if capabilities.ProfileSelection() || capabilities.WebAlias {
		t.Fatalf("unexpected launch entry: %+v", capabilities)
	}
	if capabilities.AppHelpVerified() {
		t.Fatalf("AppHelpVerified() = true without an app help probe")
	}
	if capabilities.Supports("--port") {
		t.Fatalf("Supports(--port) = true, want false")
	}
	joined := strings.Join(capabilities.Warnings(), "\n")
	if !strings.Contains(joined, "未识别") || !strings.Contains(joined, "尚未验证") {
		t.Fatalf("warnings = %v", capabilities.Warnings())
	}
}

func TestProbeCapabilitiesAppHelpFailureIsTolerated(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioAppFail, "web")
	var logs []string
	options.Logf = func(line string) { logs = append(logs, line) }
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v (app help failure must not abort the launcher probe)", err)
	}
	if capabilities.AppHelpVerified() {
		t.Fatalf("AppHelpVerified() = true after the app help command failed")
	}
	if capabilities.Supports("--port") {
		t.Fatalf("Supports(--port) = true without verified app help")
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "Web 帮助探测未完成") {
		t.Fatalf("logs = %q, want a warning about the failed app help probe", joined)
	}
}

func TestProbeCapabilitiesAppHelpWithoutUsageIsUnverified(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioAppUseless, "web")
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v", err)
	}
	if capabilities.AppHelpVerified() {
		t.Fatalf("AppHelpVerified() = true for output without usage/options/--port")
	}
	if len(capabilities.AppOptions) != 0 {
		t.Fatalf("AppOptions = %v, want none for unverified help", capabilities.AppOptions)
	}
}

func TestProbeCapabilitiesLauncherFailureIsFailClosed(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioLauncherFail, "web")
	_, err := probeCapabilities(context.Background(), options)
	if err == nil {
		t.Fatalf("probeCapabilities() succeeded, want a fail-closed error")
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Fatalf("error = %v, want an explicit fail-closed message", err)
	}
}

func TestProbeCapabilitiesTimeout(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioSleep, "web")
	options.LauncherHelpTimeout = 150 * time.Millisecond
	started := time.Now()
	_, err := probeCapabilities(context.Background(), options)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatalf("probeCapabilities() succeeded, want a timeout error")
	}
	if !strings.Contains(err.Error(), "超过") {
		t.Fatalf("error = %v, want a timeout message", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took %v, want it enforced promptly", elapsed)
	}
}

func TestProbeCapabilitiesCompatibilityOverrides(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioMinimal, "web")
	writeCompatibility(t, options.DataRoot, `{
  "versions": {
    "0.2.1-rc.3": {
      "launcherOptions": ["--profile"],
      "appOptions": ["--port", "--host"],
      "pluginManagement": true
    }
  }
}`)
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v", err)
	}
	if !capabilities.ProfileSelection() {
		t.Fatalf("manual --profile declaration was not applied")
	}
	if !capabilities.Supports("--port") || !capabilities.Supports("--host") {
		t.Fatalf("manual app options were not applied: %v", capabilities.AppOptions)
	}
	if capabilities.Supports("--no-open") {
		t.Fatalf("undeclared --no-open must not be guessed")
	}
	if !capabilities.PluginManagement {
		t.Fatalf("manual pluginManagement declaration was not applied")
	}
	joined := strings.Join(capabilities.Warnings(), "\n")
	if !strings.Contains(joined, "人工能力声明") {
		t.Fatalf("warnings = %v, want a manual-declaration warning", capabilities.Warnings())
	}
}

func TestProbeCapabilitiesInvalidCompatibilityOption(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioMinimal, "web")
	writeCompatibility(t, options.DataRoot, `{"versions":{"0.2.1-rc.3":{"appOptions":["--ok","--bad flag"]}}}`)
	if _, err := probeCapabilities(context.Background(), options); err == nil {
		t.Fatalf("invalid option declaration was accepted")
	}
}

func TestProbeCapabilitiesMalformedCompatibility(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioMinimal, "web")
	writeCompatibility(t, options.DataRoot, `{not json`)
	_, err := probeCapabilities(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "compatibility.json") {
		t.Fatalf("error = %v, want a compatibility.json error", err)
	}
}

func TestProbeCapabilitiesCustomProfileDeclaredOptions(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioMinimal, "myprofile")
	writeCompatibility(t, options.DataRoot, `{
  "versions": {
    "0.2.1-rc.3": {
      "launcherOptions": ["--profile"],
      "profiles": {"myprofile": {"appOptions": ["--port", "--host"]}}
    }
  }
}`)
	capabilities, err := probeCapabilities(context.Background(), options)
	if err != nil {
		t.Fatalf("probeCapabilities() error = %v", err)
	}
	if capabilities.Profile != "myprofile" {
		t.Fatalf("Profile = %q", capabilities.Profile)
	}
	if strings.Join(capabilities.AppOptions, ",") != "--port,--host" {
		t.Fatalf("AppOptions = %v, want the declared profile options", capabilities.AppOptions)
	}
	if capabilities.AppHelpVerified() {
		t.Fatalf("AppHelpVerified() = true for manual profile options")
	}
	if joined := strings.Join(capabilities.Warnings(), "\n"); !strings.Contains(joined, "指定的应用参数") {
		t.Fatalf("warnings = %v", capabilities.Warnings())
	}
}

func TestProbeCapabilitiesCustomProfileWithoutDeclarationFailsClosed(t *testing.T) {
	t.Parallel()
	options := newProbeTestOptions(t, scenarioOK, "custom")
	_, err := probeCapabilities(context.Background(), options)
	if err == nil {
		t.Fatalf("probeCapabilities() succeeded for an unverified custom profile")
	}
	if !strings.Contains(err.Error(), "compatibility.json") {
		t.Fatalf("error = %v, want a compatibility.json hint", err)
	}
}

func TestProbeCapabilitiesInputValidation(t *testing.T) {
	t.Parallel()
	executable := testNodePath(t)
	base := probeOptions{NodePath: executable, EntryPath: executable, DataRoot: t.TempDir(), Version: "0.2.1-rc.3", Profile: "web"}
	cases := []struct {
		name   string
		mutate func(*probeOptions)
	}{
		{"version", func(o *probeOptions) { o.Version = "latest" }},
		{"profile", func(o *probeOptions) { o.Profile = "bad/profile" }},
		{"data root", func(o *probeOptions) { o.DataRoot = "" }},
		{"node path", func(o *probeOptions) { o.NodePath = "" }},
		{"entry path", func(o *probeOptions) { o.EntryPath = "" }},
		{"relative node", func(o *probeOptions) { o.NodePath = "node" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			options := base
			tc.mutate(&options)
			if _, err := probeCapabilities(context.Background(), options); err == nil {
				t.Fatalf("probeCapabilities(%s) succeeded, want error", tc.name)
			}
		})
	}
}

func TestOptionsFromHelp(t *testing.T) {
	t.Parallel()
	help := "Usage: dsh [options]\r\n\r\n  --profile <name>  select profile\r\n  -p, --port <port>  port\r\n  --help\r\n  --x=a\r\n  --bad:value\r\n  --trail"
	got := optionsFromHelp(help)
	want := []string{"--profile", "--port", "--help", "--x", "--trail"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("optionsFromHelp() = %v, want %v", got, want)
	}
	if duplicates := optionsFromHelp("  --help\n  --help\n"); len(duplicates) != 1 {
		t.Fatalf("optionsFromHelp() = %v, want a single --help", duplicates)
	}
	if none := optionsFromHelp(""); len(none) != 0 {
		t.Fatalf("optionsFromHelp(\"\") = %v, want empty", none)
	}
}

func TestLooksLikeAppHelp(t *testing.T) {
	t.Parallel()
	for _, output := range []string{"Usage: dsh web", "Options:", "  --port <port>"} {
		if !looksLikeAppHelp(output) {
			t.Fatalf("looksLikeAppHelp(%q) = false", output)
		}
	}
	for _, output := range []string{"", "No help available", "error: unknown"} {
		if looksLikeAppHelp(output) {
			t.Fatalf("looksLikeAppHelp(%q) = true", output)
		}
	}
}

func TestProbeEnvironmentIsBlankBase(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	nodePath := filepath.Join("opt", "node", "bin", "node")
	environment := probeEnvironment(dataRoot, "/tmp/dsh-home", "/tmp/agents-home", nodePath)
	joined := strings.Join(environment, "\n")
	for _, expected := range []string{
		"HOME=/tmp/dsh-home",
		"DSH_HOME=/tmp/dsh-home",
		"DSH_AGENTS_HOME=/tmp/agents-home",
		"XDG_CACHE_HOME=" + filepath.Join(dataRoot, "cache"),
		"DSH_TELEMETRY_MODE=DISABLED",
		"PATH=" + filepath.Dir(nodePath),
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("environment does not contain %q: %v", expected, environment)
		}
	}
	for _, forbidden := range []string{"USER=", "TZ=", "NODE_OPTIONS="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("environment inherited %q: %v", forbidden, environment)
		}
	}
}
