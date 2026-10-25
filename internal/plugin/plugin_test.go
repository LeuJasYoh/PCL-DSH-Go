// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

const (
	pluginTestVersion = "0.2.1-rc.3"
	pluginTestID      = "i-000000000001"
)

func TestNewValidation(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatalf("New(empty) succeeded, want error")
	}
	fixture := newPluginFixture(t, "new-ok")
	if _, err := New(Options{Dirs: fixture.dirs, Store: nil}); err == nil {
		t.Fatalf("New(nil store) succeeded, want error")
	}
	service, err := New(Options{Dirs: fixture.dirs, Store: fixture.store})
	if err != nil {
		t.Fatalf("New() minimal options error = %v", err)
	}
	if service == nil {
		t.Fatalf("New() returned nil service")
	}
}

func TestListProfileMetadata(t *testing.T) {
	fixture := newPluginFixture(t, "list-ok")
	fixture.writeProfileManifest(`{
  "dependencies": {
    "@deepseek-ai/dsh-web": "^0.2.1-rc.3",
    "dep-local": "file:./vendor/dep-local",
    "dep-missing": "^2.0.0",
    "dep-plain": "^1.2.0"
  },
  "optionalDependencies": {"dep-optional": "1.0.0"},
  "dsh": {"profile": {"bundles": ["bundle-string", {"name": "bundle-object"}, {"package": "@deepseek-ai/dsh-builtin"}]}}
}`)
	profileNodeModules := filepath.Join(fixture.profileDirectory(), "node_modules")
	runtimeNodeModules := filepath.Join(fixture.dirs.RuntimesDir, pluginTestVersion, "node_modules")
	fixture.writeInstalledPackage(profileNodeModules, "dep-plain", `{"name":"dep-plain","version":"1.2.3","description":"plain plugin"}`)
	fixture.writeInstalledPackage(profileNodeModules, "dep-local", `{"name":"dep-local","version":"0.1.0"}`)
	fixture.writeInstalledPackage(runtimeNodeModules, "bundle-string", `{"name":"bundle-string","version":"9.9.9","description":"runtime bundle"}`)
	fixture.writeInstalledPackage(runtimeNodeModules, "bundle-object", `{"name":"bundle-object","version":"8.8.8"}`)
	fixture.writeInstalledPackage(runtimeNodeModules, "@deepseek-ai/dsh-builtin", `{"name":"@deepseek-ai/dsh-builtin","version":"7.7.7"}`)
	fixture.writeInstalledPackage(runtimeNodeModules, "dep-optional", `{"name":"dep-optional","version":"3.3.3"}`)

	rows, err := fixture.service.List(context.Background(), fixture.instance.ID, "web")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("List() returned %d rows, want 8: %+v", len(rows), rows)
	}
	byName := make(map[string]DTO, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}
	expected := map[string]struct {
		version     string
		description string
		source      string
	}{
		"bundle-string":            {version: "9.9.9", description: "runtime bundle", source: sourceBuiltin},
		"bundle-object":            {version: "8.8.8", source: sourceBuiltin},
		"@deepseek-ai/dsh-builtin": {version: "7.7.7", source: sourceBuiltin},
		"@deepseek-ai/dsh-web":     {version: "待恢复依赖", source: sourceOfficial},
		"dep-local":                {version: "0.1.0", source: sourceLocal},
		"dep-missing":              {version: "待恢复依赖", source: sourceCommunity},
		"dep-optional":             {version: "3.3.3", source: sourceCommunity},
		"dep-plain":                {version: "1.2.3", description: "plain plugin", source: sourceCommunity},
	}
	for name, want := range expected {
		row, ok := byName[name]
		if !ok {
			t.Fatalf("List() is missing %q: %+v", name, rows)
		}
		if row.Version != want.version {
			t.Fatalf("%s version = %q, want %q", name, row.Version, want.version)
		}
		if row.Description != want.description {
			t.Fatalf("%s description = %q, want %q", name, row.Description, want.description)
		}
		if row.Source != want.source {
			t.Fatalf("%s source = %q, want %q", name, row.Source, want.source)
		}
	}
	previousRank := -1
	previousName := ""
	for _, row := range rows {
		rank := sourceRank(row.Source)
		if rank < previousRank {
			t.Fatalf("List() ordering is not grouped by source: %+v", rows)
		}
		if rank == previousRank && previousName != "" && row.Name < previousName {
			t.Fatalf("List() names are not sorted inside a source group: %+v", rows)
		}
		previousRank = rank
		previousName = row.Name
	}
}

func TestListEmptyProfileAndErrors(t *testing.T) {
	fixture := newPluginFixture(t, "list-empty")
	rows, err := fixture.service.List(context.Background(), fixture.instance.ID, "web")
	if err != nil {
		t.Fatalf("List(uninitialized profile) error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("List(uninitialized profile) = %+v, want empty", rows)
	}

	if _, err := fixture.service.List(context.Background(), "i-000000000000", "web"); err == nil {
		t.Fatalf("List(missing instance) succeeded")
	} else if apiErr, ok := apierr.As(err); !ok || apiErr.Status != 404 || apiErr.Code != apierr.CodeNotFound {
		t.Fatalf("List(missing instance) error = %v, want 404 NOT_FOUND", err)
	}
	for _, profile := range []string{"", "bad/profile", ".."} {
		if _, err := fixture.service.List(context.Background(), fixture.instance.ID, profile); err == nil {
			t.Fatalf("List(profile=%q) succeeded, want 400", profile)
		} else if apiErr, ok := apierr.As(err); !ok || apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
			t.Fatalf("List(profile=%q) error = %v, want 400 VALIDATION", profile, err)
		}
	}

	fixture.writeProfileManifest(`{not json`)
	if _, err := fixture.service.List(context.Background(), fixture.instance.ID, "web"); err == nil {
		t.Fatalf("List(malformed manifest) succeeded, want error")
	} else if apiErr, ok := apierr.As(err); !ok || apiErr.Status != 500 {
		t.Fatalf("List(malformed manifest) error = %v, want 500", err)
	}
}

// expectedMutateArgs builds the bridge record args (os.Args[1:] of the child).
func expectedMutateArgs(fixture *pluginFixture, args ...string) []string {
	command := []string{fixture.bridgePath(), fixture.tools.PnpmScript, fixture.entry, "plugin", "--profile", "web"}
	command = append(command, args...)
	return command
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, `|`) != strings.Join(want, `|`) {
		t.Fatalf("bridge args = %v, want %v", got, want)
	}
}

func TestMutateAddSuccess(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-ok")
	var logs []string
	result, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID,
		Profile:    "web",
		Action:     ActionAdd,
		Spec:       "@acme/plugin-demo",
	}, func(line string) { logs = append(logs, line) })
	if err != nil {
		t.Fatalf("Mutate(add) error = %v", err)
	}
	if result.InstanceID != fixture.instance.ID || result.Profile != "web" || result.Action != ActionAdd || result.Name != "@acme/plugin-demo" {
		t.Fatalf("Mutate result = %+v", result)
	}
	record := fixture.readBridgeRecord()
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"add", "@acme/plugin-demo", "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))
	if record.Cwd != fixture.workspaceDirectory() {
		t.Fatalf("bridge cwd = %q, want workspace %q", record.Cwd, fixture.workspaceDirectory())
	}
	expectedEnv := map[string]string{
		"HOME":                filepath.Join(fixture.dataRoot, "cache", "pnpm-home", fixture.instance.ID, "web"),
		"DSH_HOME":            filepath.Join(fixture.dirs.InstancesDir, fixture.instance.ID, "dsh-home"),
		"NPM_CONFIG_REGISTRY": "https://registry.npmjs.org",
		"PNPM_HOME":           filepath.Join(fixture.dataRoot, "tools", "pnpm"),
	}
	for key, want := range expectedEnv {
		if got := record.Env[key]; got != want {
			t.Fatalf("bridge env[%q] = %q, want %q", key, got, want)
		}
	}
	if record.Env["PATH"] == "" || !strings.Contains(record.Env["PATH"], filepath.Dir(fixture.tools.NodePath)) {
		t.Fatalf("bridge PATH = %q, want the bundled node directory", record.Env["PATH"])
	}
	if _, present := record.Env["HTTP_PROXY"]; present {
		t.Fatalf("bridge env unexpectedly contains HTTP_PROXY")
	}
	joinedLogs := strings.Join(logs, "\n")
	if !strings.Contains(joinedLogs, "原生 Harness plugin add") || !strings.Contains(joinedLogs, "插件操作完成") {
		t.Fatalf("logs = %q, want start/finish lines", joinedLogs)
	}
	bridgeData, err := os.ReadFile(fixture.bridgePath())
	if err != nil {
		t.Fatalf("embedded bridge was not written: %v", err)
	}
	bridgeText := string(bridgeData)
	for _, marker := range []string{"HDSL plugin bridge", "rejects unstructured shell commands", "runCli"} {
		if !strings.Contains(bridgeText, marker) {
			t.Fatalf("embedded bridge does not contain %q", marker)
		}
	}
}

func TestMutateLocalSpecAnchoring(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-anchor")
	fixture.mkdirInWorkspace("local-plugin", `{"name":"local-plugin","version":"1.0.0"}`)
	fixture.mkdirInWorkspace("vendor/bare-plugin", `{"name":"bare-plugin","version":"1.0.0"}`)
	_, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID,
		Profile:    "web",
		Action:     ActionAdd,
		Spec:       "file:./local-plugin",
	}, nil)
	if err != nil {
		t.Fatalf("Mutate(add file:) error = %v", err)
	}
	record := fixture.readBridgeRecord()
	wantSpec := "file:" + filepath.Join(fixture.workspaceDirectory(), "local-plugin")
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"add", wantSpec, "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))

	_, err = fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID,
		Profile:    "web",
		Action:     ActionAdd,
		Spec:       "./vendor/bare-plugin",
	}, nil)
	if err != nil {
		t.Fatalf("Mutate(add bare relative) error = %v", err)
	}
	record = fixture.readBridgeRecord()
	wantBare := filepath.Join(fixture.workspaceDirectory(), "vendor", "bare-plugin")
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"add", wantBare, "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))

	_, err = fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID,
		Profile:    "web",
		Action:     ActionAdd,
		Spec:       "github:acme/plugin",
	}, nil)
	if err != nil {
		t.Fatalf("Mutate(add github) error = %v", err)
	}
	record = fixture.readBridgeRecord()
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"add", "github:acme/plugin", "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))
}

func TestMutateUpdateAndRemove(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-update-remove")
	fixture.writeProfileManifest(`{"dependencies":{"dep-one":"^1.0.0"},"dsh":{"profile":{"bundles":["bundle-x"]}}}`)

	result, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionUpdate,
	}, nil)
	if err != nil {
		t.Fatalf("Mutate(update) error = %v", err)
	}
	if result.Name != "" {
		t.Fatalf("update result name = %q, want empty", result.Name)
	}
	record := fixture.readBridgeRecord()
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"update", "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))

	result, err = fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionRemove, Spec: "dep-one",
	}, nil)
	if err != nil {
		t.Fatalf("Mutate(remove) error = %v", err)
	}
	if result.Action != ActionRemove || result.Name != "dep-one" {
		t.Fatalf("remove result = %+v", result)
	}
	record = fixture.readBridgeRecord()
	assertArgs(t, record.Args, expectedMutateArgs(fixture,
		"remove", "dep-one", "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true"))

	_, err = fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionRemove, Spec: "bundle-x",
	}, nil)
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
		t.Fatalf("remove builtin error = %v, want 400 VALIDATION", err)
	}
}

func TestMutatePreconditions(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-preconditions")
	cases := []struct {
		name      string
		input     MutateInput
		wantStaus int
		wantCode  string
	}{
		{
			name:      "missing instance",
			input:     MutateInput{InstanceID: "i-000000000000", Profile: "web", Action: ActionAdd, Spec: "@acme/x"},
			wantStaus: 404,
			wantCode:  apierr.CodeNotFound,
		},
		{
			name:      "invalid profile",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "bad/profile", Action: ActionAdd, Spec: "@acme/x"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "invalid action",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: "explode", Spec: "@acme/x"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "empty spec",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "flag-like spec",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "--config.ignore-scripts=false"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "control chars in spec",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "plugin\u0001name"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "oversized spec",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: strings.Repeat("a", 215)},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "update with spec",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionUpdate, Spec: "dep-one"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
		{
			name:      "remove invalid name",
			input:     MutateInput{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionRemove, Spec: "not a package"},
			wantStaus: 400,
			wantCode:  apierr.CodeValidation,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := fixture.service.Mutate(context.Background(), tc.input, nil)
			apiErr, ok := apierr.As(err)
			if !ok {
				t.Fatalf("Mutate(%s) error = %v, want *apierr.Error", tc.name, err)
			}
			if apiErr.Status != tc.wantStaus || apiErr.Code != tc.wantCode {
				t.Fatalf("Mutate(%s) = %+v, want %d %s", tc.name, apiErr, tc.wantStaus, tc.wantCode)
			}
		})
	}
}

// TestInstanceRunningProbe covers the production loopback running check.
//
// It uses ephemeral listeners only: a fixed test port can be bound by a sibling
// package test process (the Windows flake) and, under WSL2, a Linux-free
// ephemeral port can still be reachable through Windows localhost forwarding.
func TestInstanceRunningProbe(t *testing.T) {
	closedObserved := false
	for attempt := 0; attempt < 5; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen on ephemeral port: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if !probeInstancePort(port) {
			_ = listener.Close()
			t.Fatalf("probeInstancePort(%d) = false, want true for a live listener", port)
		}
		_ = listener.Close()
		// WSL localhost forwarding can release a closed listener asynchronously.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if !probeInstancePort(port) {
				closedObserved = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if closedObserved {
			break
		}
	}
	if !closedObserved {
		t.Fatalf("closed loopback ports were never observed as free")
	}
}

// TestMutateRunningInstanceReturnsConflict verifies the fail-safe 409 when the
// instance loopback port is live. The probe is injected to keep the test
// deterministic; the authoritative running check still lives in the API/app
// layer, which returns the same conflict before Jobs.Start.
func TestMutateRunningInstanceReturnsConflict(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-running")
	forceInstanceRunning(t)
	_, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin",
	}, nil)
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != 409 || apiErr.Code != apierr.CodeConflictInstanceRunning {
		t.Fatalf("Mutate(running instance) error = %v, want 409 CONFLICT_INSTANCE_RUNNING", err)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.workspaceDirectory(), fakeRecordFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bridge must not run while the instance is running")
	}
}

func TestMutatePluginManagementUnsupported(t *testing.T) {
	for _, scenario := range []string{"no-plugin", "probe-fail"} {
		scenario := scenario
		t.Run(scenario, func(t *testing.T) {
			fixture := newPluginFixture(t, scenario)
			_, err := fixture.service.Mutate(context.Background(), MutateInput{
				InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin",
			}, nil)
			apiErr, ok := apierr.As(err)
			if !ok || apiErr.Status != 422 || apiErr.Code != apierr.CodeUnsupportedRuntime {
				t.Fatalf("Mutate(%s) error = %v, want 422 UNSUPPORTED_RUNTIME", scenario, err)
			}
		})
	}
}

func TestMutateFailureSummaryIsRedacted(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-fail")
	var logs []string
	_, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin",
	}, func(line string) { logs = append(logs, line) })
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != 500 || apiErr.Code != apierr.CodeInternal {
		t.Fatalf("Mutate(failure) error = %v, want 500 INTERNAL", err)
	}
	summary := ""
	if details, ok := apiErr.Details.(map[string]any); ok {
		summary, _ = details["summary"].(string)
	}
	if !strings.Contains(summary, "ERR_PNPM_META_FETCH_FAIL") {
		t.Fatalf("error summary = %q, want the pnpm failure line", summary)
	}
	if strings.Contains(summary, "secret123") {
		t.Fatalf("error summary leaked the token: %q", summary)
	}
	if strings.Contains(strings.Join(logs, "\n"), "secret123") {
		t.Fatalf("logf leaked the token: %v", logs)
	}
}

func TestMutateContextCancelled(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.service.Mutate(ctx, MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin",
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Mutate(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

func TestValidateMutationInput(t *testing.T) {
	malicious := []struct {
		name  string
		input MutateInput
	}{
		{"scoped double slash", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "@bad//name"}},
		{"scoped extra segment", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "@scope/name/extra"}},
		{"scoped empty name", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "@scope/"}},
		{"parent escape", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "file:../../outside"}},
		{"absolute path", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "file:/etc/passwd"}},
		{"windows drive", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "file:C:/Windows/win.ini"}},
		{"unc path", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "file://server/share"}},
		{"link parent escape", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "link:../outside"}},
		{"link absolute", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "link:/etc/passwd"}},
		{"flag-like spec", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "--config.ignore-scripts=false"}},
		{"spaces in package name", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "bad name with space"}},
		{"remove malformed name", MutateInput{InstanceID: pluginTestID, Profile: "web", Action: ActionRemove, Spec: "@bad//name"}},
	}
	for _, tc := range malicious {
		tc := tc
		t.Run("malicious/"+tc.name, func(t *testing.T) {
			err := ValidateMutationInput(tc.input)
			apiErr, ok := apierr.As(err)
			if !ok {
				t.Fatalf("ValidateMutationInput(%s) error = %v, want *apierr.Error", tc.name, err)
			}
			if apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
				t.Fatalf("ValidateMutationInput(%s) = %+v, want 400 VALIDATION", tc.name, apiErr)
			}
		})
	}

	valid := []MutateInput{
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin-demo"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "lodash"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "lodash@4.17.21"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "@acme/plugin@^1.2.0"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "github:acme/plugin"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "npm:lodash@4"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "acme/plugin"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "./local-plugin"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "file:./local-plugin"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionAdd, Spec: "link:./local-plugin"},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionUpdate},
		{InstanceID: pluginTestID, Profile: "web", Action: ActionRemove, Spec: "dep-one"},
	}
	for index, input := range valid {
		if err := ValidateMutationInput(input); err != nil {
			t.Fatalf("ValidateMutationInput(valid[%d] %+v) error = %v", index, input, err)
		}
	}
}

func TestMutateMaliciousSpecsReturnValidation(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-malicious")
	cases := []MutateInput{
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "@bad//name"},
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "file:../../outside"},
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "file:/etc/passwd"},
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "link:../outside"},
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "file://server/share"},
		{InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "bad name with space"},
	}
	for index, input := range cases {
		_, err := fixture.service.Mutate(context.Background(), input, nil)
		apiErr, ok := apierr.As(err)
		if !ok || apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
			t.Fatalf("Mutate(malicious[%d] %q) error = %v, want 400 VALIDATION", index, input.Spec, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(fixture.workspaceDirectory(), fakeRecordFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bridge must not run for a rejected spec")
	}
}

func TestMutateLocalTargetValidation(t *testing.T) {
	fixture := newPluginFixture(t, "mutate-local-target")

	// Missing workspace target -> 400 VALIDATION (no INTERNAL, no bridge run).
	_, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "file:./missing-plugin",
	}, nil)
	if apiErr, ok := apierr.As(err); !ok || apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
		t.Fatalf("Mutate(missing local target) error = %v, want 400 VALIDATION", err)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.workspaceDirectory(), fakeRecordFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bridge must not run when the local target is missing")
	}

	// Existing target inside the workspace -> success.
	fixture.mkdirInWorkspace("local-plugin", `{"name":"local-plugin","version":"1.0.0"}`)
	if _, err := fixture.service.Mutate(context.Background(), MutateInput{
		InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "link:./local-plugin",
	}, nil); err != nil {
		t.Fatalf("Mutate(valid link target) error = %v", err)
	}

	// Symlink escaping the workspace -> 400 VALIDATION.
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside-plugin")
		if err := os.MkdirAll(outside, 0o700); err != nil {
			t.Fatalf("mkdir outside plugin: %v", err)
		}
		workspace := fixture.workspaceDirectory()
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatalf("mkdir workspace: %v", err)
		}
		escapeLink := filepath.Join(workspace, "escape")
		if err := os.Symlink(outside, escapeLink); err == nil {
			_, err = fixture.service.Mutate(context.Background(), MutateInput{
				InstanceID: fixture.instance.ID, Profile: "web", Action: ActionAdd, Spec: "link:./escape",
			}, nil)
			if apiErr, ok := apierr.As(err); !ok || apiErr.Status != 400 || apiErr.Code != apierr.CodeValidation {
				t.Fatalf("Mutate(symlink escape) error = %v, want 400 VALIDATION", err)
			}
		}
	}
}

func TestRunCommandTimeout(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sleep")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bridge := filepath.Join(directory, "bridge.mjs")
	if err := os.WriteFile(bridge, []byte("fake bridge"), 0o600); err != nil {
		t.Fatalf("write fake bridge: %v", err)
	}
	entry := filepath.Join(directory, "bin.js")
	if err := os.WriteFile(entry, []byte("fake entry"), 0o600); err != nil {
		t.Fatalf("write fake entry: %v", err)
	}
	started := time.Now()
	_, err := runCommand(context.Background(), commandSpec{
		Args:    []string{testNodePath(t), bridge, "pnpm.cjs", entry, "plugin"},
		Dir:     directory,
		Env:     []string{},
		Timeout: 150 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("runCommand() error = %v, want a timeout error", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("runCommand() timeout took too long: %v", time.Since(started))
	}
}
