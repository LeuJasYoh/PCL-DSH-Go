// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
	"gopkg.in/yaml.v3"
)

const launchTestVersion = "0.2.1-rc.3"
const launchTestInstanceID = "i-0123456789ab"

type launchFixture struct {
	service   *Service
	dirs      config.Dirs
	toolchain *tools.Tools
	instance  store.Instance
	caps      Capabilities
	entry     string
}

func newLaunchFixture(t *testing.T) launchFixture {
	t.Helper()
	dataRoot := t.TempDir()
	dirs := config.Dirs{
		DataRoot:     dataRoot,
		ConfigDir:    filepath.Join(dataRoot, "config"),
		InstancesDir: filepath.Join(dataRoot, "instances"),
		RuntimesDir:  filepath.Join(dataRoot, "runtimes"),
		CacheDir:     filepath.Join(dataRoot, "cache"),
		LogsDir:      filepath.Join(dataRoot, "logs"),
		TrashDir:     filepath.Join(dataRoot, "trash"),
	}
	for _, directory := range []string{dirs.ConfigDir, dirs.InstancesDir, dirs.RuntimesDir, dirs.CacheDir, dirs.LogsDir, dirs.TrashDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", directory, err)
		}
	}
	entry := writeRuntimeAt(t, dirs.RuntimesDir, launchTestVersion)
	toolchain := &tools.Tools{
		NodePath:   filepath.Join(dataRoot, "tools", "node", "bin", "node"),
		PnpmScript: filepath.Join(dataRoot, "tools", "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs"),
		ToolRoot:   filepath.Join(dataRoot, "tools"),
	}
	instance := store.Instance{
		ID:      launchTestInstanceID,
		Name:    "工作实例",
		Version: launchTestVersion,
		Profile: "web",
		Port:    3080,
	}
	capabilities := Capabilities{
		LauncherOptions: []string{"--profile", "--help"},
		AppOptions:      []string{"--port", "--host", "--no-open"},
		Version:         launchTestVersion,
		Profile:         "web",
		EntryPath:       entry,
	}
	return launchFixture{
		service:   New(nil, toolchain, dataRoot),
		dirs:      dirs,
		toolchain: toolchain,
		instance:  instance,
		caps:      capabilities,
		entry:     entry,
	}
}

func TestBuildLaunchSpecSuccess(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	spec, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "https://registry.npmjs.org", "", fixture.caps)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	wantCommand := []string{
		fixture.toolchain.NodePath,
		fixture.entry,
		"--profile", "web",
		"--port", "3080",
		"--host", "127.0.0.1",
		"--no-open",
	}
	if strings.Join(spec.Command, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("Command = %v, want %v", spec.Command, wantCommand)
	}
	if spec.InstanceID != launchTestInstanceID || spec.Port != 3080 {
		t.Fatalf("spec = %+v", spec)
	}
	wantWorkspace := filepath.Join(fixture.dirs.InstancesDir, launchTestInstanceID, "workspace")
	if spec.Dir != wantWorkspace {
		t.Fatalf("Dir = %q, want %q", spec.Dir, wantWorkspace)
	}
	if want := filepath.Join(fixture.dirs.LogsDir, "instance-"+launchTestInstanceID+".log"); spec.LogPath != want {
		t.Fatalf("LogPath = %q, want %q", spec.LogPath, want)
	}
	instanceDirectory := filepath.Join(fixture.dirs.InstancesDir, launchTestInstanceID)
	for _, directory := range []string{
		filepath.Join(instanceDirectory, "dsh-home"),
		filepath.Join(instanceDirectory, "agents-home"),
		filepath.Join(instanceDirectory, "workspace"),
		filepath.Join(fixture.dirs.DataRoot, "tools", "shims"),
	} {
		if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
			t.Fatalf("directory %s was not created: %v", directory, statErr)
		}
	}
	patchData, err := os.ReadFile(filepath.Join(instanceDirectory, "dsh-home", "cordis.patch.yml"))
	if err != nil {
		t.Fatalf("read workspace patch: %v", err)
	}
	var patches []struct {
		ID     string `yaml:"id"`
		Config struct {
			DocumentsDirectory string `yaml:"documentsDirectory"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(patchData, &patches); err != nil {
		t.Fatalf("parse workspace patch: %v", err)
	}
	if len(patches) != 1 || patches[0].ID != "workspace-controller" || patches[0].Config.DocumentsDirectory != wantWorkspace {
		t.Fatalf("workspace patch = %+v, want documentsDirectory %q", patches, wantWorkspace)
	}

	values := environmentMap(t, spec.Env)
	expected := map[string]string{
		"HOME":                  wantWorkspace,
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
			// docs/DSH_INTEGRATION 1.2: the manager PATH is appended last.
			os.Getenv("PATH"),
		}, string(os.PathListSeparator)),
	}
	for key, want := range expected {
		if got := values[key]; got != want {
			t.Fatalf("Env[%q] = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NO_PROXY"} {
		if _, present := values[key]; present {
			t.Fatalf("proxy variable %q set without a proxy", key)
		}
	}
	for _, argument := range spec.Command {
		for _, forbidden := range []string{"--patch", "--account", "--api-key", "--token", "--resume"} {
			if argument == forbidden {
				t.Fatalf("command injected %q: %v", forbidden, spec.Command)
			}
		}
	}
}

func TestBuildLaunchSpecPreservesCustomDocumentsDirectory(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	dshHome := filepath.Join(fixture.dirs.InstancesDir, launchTestInstanceID, "dsh-home")
	if err := os.MkdirAll(dshHome, 0o700); err != nil {
		t.Fatalf("mkdir dsh-home: %v", err)
	}
	original := "# local settings\n- id: workspace-controller\n  config:\n    documentsDirectory: /srv/custom-documents\n    documentsLookupTimeoutMs: 5000\n- id: local-custom\n  disabled: !!js process.env.LOCAL_CUSTOM === 'yes'\n"
	path := filepath.Join(dshHome, "cordis.patch.yml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write custom patch: %v", err)
	}
	if _, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "", fixture.caps); err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read custom patch: %v", err)
	}
	if string(updated) != original {
		t.Fatalf("custom patch changed:\n%s", updated)
	}
}

func TestBuildLaunchSpecProxy(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	spec, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "https://registry.npmjs.org", "http://127.0.0.1:7890", fixture.caps)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	values := environmentMap(t, spec.Env)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY"} {
		if got := values[key]; got != "http://127.0.0.1:7890" {
			t.Fatalf("Env[%q] = %q", key, got)
		}
	}
	if got := values["NO_PROXY"]; got != ProxyEnvironment {
		t.Fatalf("NO_PROXY = %q, want %q", got, ProxyEnvironment)
	}
}

func TestBuildLaunchSpecDefaultRegistryAndWebAlias(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	fixture.caps = Capabilities{
		LauncherOptions: []string{"--help"},
		AppOptions:      []string{"--port"},
		WebAlias:        true,
		Version:         launchTestVersion,
		Profile:         "web",
	}
	spec, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "", fixture.caps)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	if strings.Join(spec.Command, "\x00") != strings.Join([]string{fixture.toolchain.NodePath, fixture.entry, "web", "--port", "3080"}, "\x00") {
		t.Fatalf("Command = %v", spec.Command)
	}
	values := environmentMap(t, spec.Env)
	if got := values["HDSL_NPM_REGISTRY"]; got != DefaultRegistry {
		t.Fatalf("HDSL_NPM_REGISTRY = %q, want %q", got, DefaultRegistry)
	}
}

func TestBuildLaunchSpecIgnoresUnverifiedEntryPath(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	fixture.caps.EntryPath = filepath.Join(string(filepath.Separator), "evil", "bin.js")
	spec, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "", fixture.caps)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	if spec.Command[1] != fixture.entry {
		t.Fatalf("Command[1] = %q, want the validated entry %q", spec.Command[1], fixture.entry)
	}
}

func TestBuildLaunchSpecCustomProfile(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	fixture.instance.Profile = "my-profile"
	fixture.caps.Profile = "my-profile"
	fixture.caps.AppOptions = []string{"--port"}
	spec, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "", fixture.caps)
	if err != nil {
		t.Fatalf("BuildLaunchSpec() error = %v", err)
	}
	want := []string{fixture.toolchain.NodePath, fixture.entry, "--profile", "my-profile", "--port", "3080"}
	if strings.Join(spec.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("Command = %v, want %v", spec.Command, want)
	}
}

func TestBuildLaunchSpecFailClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*launchFixture)
	}{
		{"caps version mismatch", func(f *launchFixture) { f.caps.Version = "0.2.0" }},
		{"caps profile mismatch", func(f *launchFixture) { f.caps.Profile = "other" }},
		{"missing toolchain", func(f *launchFixture) { f.toolchain = nil }},
		{"missing runtime", func(f *launchFixture) { f.instance.Version = "0.1.0"; f.caps.Version = "0.1.0" }},
		{"unconfirmed port", func(f *launchFixture) { f.caps.AppOptions = []string{"--host"} }},
		{"no profile selector", func(f *launchFixture) {
			f.caps.LauncherOptions = []string{"--help"}
			f.caps.WebAlias = false
			f.caps.AppOptions = []string{"--port"}
		}},
		{"invalid registry", func(*launchFixture) {}},
		{"invalid instance port", func(f *launchFixture) { f.instance.Port = 70000 }},
		{"empty instance id", func(f *launchFixture) { f.instance.ID = "" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newLaunchFixture(t)
			tc.mutate(&fixture)
			registry := "https://registry.npmjs.org"
			proxy := ""
			if tc.name == "invalid registry" {
				registry = "ftp://registry"
			}
			if tc.name == "unconfirmed port" {
				// Expected failure comes from the missing --port; keep others valid.
				fixture.instance.Port = 3080
			}
			if _, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, registry, proxy, fixture.caps); err == nil {
				t.Fatalf("BuildLaunchSpec() succeeded for %s", tc.name)
			}
		})
	}
}

func TestBuildLaunchSpecInvalidProxy(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	if _, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "socks5://127.0.0.1:1080", fixture.caps); err == nil {
		t.Fatalf("BuildLaunchSpec() accepted an invalid proxy")
	}
}

func TestBuildLaunchSpecIncompleteDirs(t *testing.T) {
	t.Parallel()
	fixture := newLaunchFixture(t)
	fixture.dirs = config.Dirs{}
	if _, err := fixture.service.BuildLaunchSpec(fixture.instance, fixture.dirs, fixture.toolchain, "", "", fixture.caps); err == nil {
		t.Fatalf("BuildLaunchSpec() accepted empty directories")
	}
}
