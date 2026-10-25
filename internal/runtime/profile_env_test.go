// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
)

func TestEntryPath(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	entry, err := fixture.service.EntryPath(launchTestVersion)
	if err != nil {
		t.Fatalf("EntryPath() error = %v", err)
	}
	if entry != fixture.entry {
		t.Fatalf("EntryPath() = %q, want %q", entry, fixture.entry)
	}
	if !filepath.IsAbs(entry) {
		t.Fatalf("EntryPath() = %q, want an absolute path", entry)
	}
	entries := fixture.service.Entries()
	if len(entries) != 1 || entries[0].EntryPath != entry {
		t.Fatalf("Entries() = %+v, want the same entry path", entries)
	}
	if _, err := fixture.service.EntryPath("0.1.0"); err == nil {
		t.Fatalf("EntryPath(missing version) succeeded, want error")
	}
	if _, err := fixture.service.EntryPath("latest"); err == nil {
		t.Fatalf("EntryPath(invalid version) succeeded, want error")
	}
}

func TestProfileInstallEnvironmentSuccess(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	dir, environment, err := fixture.service.ProfileInstallEnvironment(
		fixture.dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", "")
	if err != nil {
		t.Fatalf("ProfileInstallEnvironment() error = %v", err)
	}
	instanceDirectory := filepath.Join(fixture.dirs.InstancesDir, launchTestInstanceID)
	wantDir := filepath.Join(instanceDirectory, "dsh-home", "profiles", "web")
	if dir != wantDir {
		t.Fatalf("dir = %q, want %q", dir, wantDir)
	}
	for _, directory := range []string{
		wantDir,
		filepath.Join(instanceDirectory, "dsh-home"),
		filepath.Join(fixture.dirs.DataRoot, "cache", "pnpm-home", launchTestInstanceID, "web"),
		filepath.Join(fixture.dirs.DataRoot, "tools", "shims"),
	} {
		if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
			t.Fatalf("directory %q was not created: %v", directory, statErr)
		}
	}

	values := environmentMap(t, environment)
	expected := map[string]string{
		"HOME":                  filepath.Join(fixture.dirs.DataRoot, "cache", "pnpm-home", launchTestInstanceID, "web"),
		"DSH_HOME":              filepath.Join(instanceDirectory, "dsh-home"),
		"DSH_AGENTS_HOME":       filepath.Join(instanceDirectory, "agents-home"),
		"HDSL_NPM_REGISTRY":     "https://registry.npmjs.org",
		"NPM_CONFIG_REGISTRY":   "https://registry.npmjs.org",
		"NPM_CONFIG_CACHE":      filepath.Join(fixture.dirs.DataRoot, "cache", "npm"),
		"PNPM_HOME":             filepath.Join(fixture.dirs.DataRoot, "tools", "pnpm"),
		"PNPM_CONFIG_STORE_DIR": filepath.Join(fixture.dirs.DataRoot, "cache", "pnpm-store"),
		"XDG_CACHE_HOME":        filepath.Join(fixture.dirs.DataRoot, "cache"),
		"DSH_TELEMETRY_MODE":    "DISABLED",
		"PATH": strings.Join([]string{
			filepath.Dir(fixture.toolchain.NodePath),
			filepath.Join(fixture.dirs.DataRoot, "tools", "shims"),
			filepath.Join(fixture.dirs.DataRoot, "tools", "pnpm"),
			os.Getenv("PATH"),
		}, string(os.PathListSeparator)),
	}
	for key, want := range expected {
		if got := values[key]; got != want {
			t.Fatalf("env[%q] = %q, want %q", key, got, want)
		}
	}
	for _, forbidden := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NO_PROXY"} {
		if _, present := values[forbidden]; present {
			t.Fatalf("proxy variable %q set without a proxy", forbidden)
		}
	}
	for key := range values {
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "API_KEY") {
			t.Fatalf("environment contains an account variable: %q", key)
		}
	}
	if values["HOME"] == values["DSH_HOME"] {
		t.Fatalf("install HOME must be a throw-away cache directory, not dsh-home")
	}
}

func TestProfileInstallEnvironmentProxyAndDefaultRegistry(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	_, environment, err := fixture.service.ProfileInstallEnvironment(
		fixture.dirs, launchTestInstanceID, "web", "", "http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("ProfileInstallEnvironment() error = %v", err)
	}
	values := environmentMap(t, environment)
	if got := values["HDSL_NPM_REGISTRY"]; got != DefaultRegistry {
		t.Fatalf("HDSL_NPM_REGISTRY = %q, want %q for an empty registry", got, DefaultRegistry)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY"} {
		if got := values[key]; got != "http://127.0.0.1:7890" {
			t.Fatalf("env[%q] = %q", key, got)
		}
	}
	if got := values["NO_PROXY"]; got != ProxyEnvironment {
		t.Fatalf("NO_PROXY = %q, want %q", got, ProxyEnvironment)
	}
}

func TestProfileInstallEnvironmentValidation(t *testing.T) {
	t.Parallel()
	type setup func(fixture *launchFixture) (service *Service, dirs config.Dirs, instance, profile, registry, proxy string)
	cases := []struct {
		name  string
		setup setup
	}{
		{
			name: "empty data root",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				dirs := f.dirs
				dirs.DataRoot = ""
				return f.service, dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "empty instances dir",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				dirs := f.dirs
				dirs.InstancesDir = ""
				return f.service, dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "relative data root",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				dirs := f.dirs
				dirs.DataRoot = "relative-data"
				return f.service, dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "missing toolchain",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return New(nil, nil, f.dirs.DataRoot), f.dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "empty instance",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, "", "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "instance traversal",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, "..", "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "instance separator",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, "i-0123/../x", "web", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "empty profile",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, launchTestInstanceID, "", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "profile spaces",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, launchTestInstanceID, "bad/profile", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "profile traversal",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, launchTestInstanceID, "..", "https://registry.npmjs.org", ""
			},
		},
		{
			name: "invalid registry",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, launchTestInstanceID, "web", "ftp://registry", ""
			},
		},
		{
			name: "invalid proxy",
			setup: func(f *launchFixture) (*Service, config.Dirs, string, string, string, string) {
				return f.service, f.dirs, launchTestInstanceID, "web", "https://registry.npmjs.org", "socks5://127.0.0.1:1080"
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newLaunchFixture(t)
			service, dirs, instance, profile, registry, proxy := tc.setup(&fixture)
			dir, environment, err := service.ProfileInstallEnvironment(dirs, instance, profile, registry, proxy)
			if err == nil {
				t.Fatalf("ProfileInstallEnvironment() succeeded, want error")
			}
			if dir != "" || environment != nil {
				t.Fatalf("failed call returned dir=%q env=%v, want zero values", dir, environment)
			}
		})
	}
}
