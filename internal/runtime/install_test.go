// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const (
	installTestRegistry = "https://registry.npmjs.org"
	installTestVersion  = "0.2.1-rc.3"
)

// newInstallTestService builds a Service with a fake toolchain and a seeded,
// verified cohort cache so Install never touches the network.
func newInstallTestService(t *testing.T, pnpmScenario string, overrides map[string]string) (*Service, string, *tools.Tools) {
	t.Helper()
	dataRoot := t.TempDir()
	seedCohortCache(t, dataRoot, installTestRegistry, installTestVersion, overrides)
	toolchain := &tools.Tools{
		NodePath:   testNodePath(t),
		PnpmScript: fakePnpmScript(t, pnpmScenario),
	}
	return New(nil, toolchain, dataRoot), dataRoot, toolchain
}

// seedCohortCache writes a valid cohort cache so install tests stay offline.
func seedCohortCache(t *testing.T, dataRoot, registry, version string, overrides map[string]string) {
	t.Helper()
	if overrides == nil {
		overrides = map[string]string{}
	}
	now := time.Now()
	cached := cohortCacheFile{
		SchemaVersion:        cohortCacheSchemaVersion,
		Complete:             true,
		Package:              DSHPackage,
		Version:              version,
		Registry:             registry,
		CheckedAt:            epochMillis(now.UnixMilli()),
		CheckedAtEpochMillis: now.UnixMilli(),
		Count:                len(overrides),
		Overrides:            overrides,
		SHA256:               cohortDigest(registry, version, overrides),
	}
	writeCohortCacheFile(t, dataRoot, registry, version, cached)
}

func runtimePath(t *testing.T, dataRoot, version string) string {
	t.Helper()
	return filepath.Join(dataRoot, "runtimes", version)
}

func TestInstallSuccess(t *testing.T) {
	t.Parallel()
	overrides := map[string]string{
		"@deepseek-ai/dsh-base": "0.2.1-rc.3",
		"@deepseek-ai/dsh-web":  "0.2.1-rc.3",
	}
	service, dataRoot, toolchain := newInstallTestService(t, scenarioOK, overrides)
	var logs []string
	result, err := service.Install(context.Background(), installTestVersion, installTestRegistry, func(line string) {
		logs = append(logs, line)
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.Version != installTestVersion || result.Reused {
		t.Fatalf("Install() result = %+v", result)
	}
	target := runtimePath(t, dataRoot, installTestVersion)
	wantEntry := filepath.Join(target, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if result.EntryPath != wantEntry {
		t.Fatalf("EntryPath = %q, want %q", result.EntryPath, wantEntry)
	}
	if _, err := os.Stat(result.EntryPath); err != nil {
		t.Fatalf("installed entry missing: %v", err)
	}

	// package.json must carry name/private/dependencies/pnpm.overrides.
	manifestData, err := os.ReadFile(filepath.Join(target, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	var manifest struct {
		Name         string            `json:"name"`
		Private      bool              `json:"private"`
		Dependencies map[string]string `json:"dependencies"`
		PNPM         *struct {
			Overrides map[string]string `json:"overrides"`
		} `json:"pnpm"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode package.json: %v", err)
	}
	if manifest.Name != "hdsl-runtime" || !manifest.Private {
		t.Fatalf("package.json name/private = %q/%v", manifest.Name, manifest.Private)
	}
	if manifest.Dependencies[DSHPackage] != installTestVersion {
		t.Fatalf("dependencies = %v", manifest.Dependencies)
	}
	if manifest.PNPM == nil || len(manifest.PNPM.Overrides) != len(overrides) {
		t.Fatalf("pnpm.overrides = %+v, want %d entries", manifest.PNPM, len(overrides))
	}
	for name, pinned := range overrides {
		if manifest.PNPM.Overrides[name] != pinned {
			t.Fatalf("override %q = %q, want %q", name, manifest.PNPM.Overrides[name], pinned)
		}
	}

	// pnpm-workspace.yaml starts with the verbatim base block and carries the
	// release-cohort overrides (pnpm 11 reads overrides from the workspace file).
	// After install pnpm may append its own keys (e.g. minimumReleaseAgeExclude),
	// so only the prefix and the generated overrides block are asserted.
	workspaceData, err := os.ReadFile(filepath.Join(target, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatalf("read pnpm-workspace.yaml: %v", err)
	}
	workspaceText := string(workspaceData)
	if !strings.HasPrefix(workspaceText, pnpmWorkspaceYAML) {
		t.Fatalf("pnpm-workspace.yaml does not start with the verbatim base block:\n%s", workspaceText)
	}
	overridesBlock := "overrides:\n  '@deepseek-ai/dsh-base': '0.2.1-rc.3'\n  '@deepseek-ai/dsh-web': '0.2.1-rc.3'\n"
	if !strings.Contains(workspaceText, overridesBlock) {
		t.Fatalf("pnpm-workspace.yaml overrides block missing or unsorted:\n%s", workspaceText)
	}

	// The fake pnpm recorded the exact argv/environment of the install.
	record := readFakePnpmRecord(t, target)
	storeDirectory := filepath.Join(dataRoot, "cache", "pnpm-store")
	wantArgs := []string{
		"install",
		"--prod",
		"--no-frozen-lockfile",
		"--registry=" + installTestRegistry,
		"--store-dir=" + storeDirectory,
		"--reporter=append-only",
	}
	if strings.Join(record.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("pnpm args = %v, want %v", record.Args, wantArgs)
	}
	stagingFragment := ".install-" + installTestVersion + "-"
	if !strings.Contains(record.Cwd, stagingFragment) {
		t.Fatalf("pnpm cwd = %q, want a staging directory", record.Cwd)
	}
	if home := record.Env["HOME"]; !strings.Contains(home, stagingFragment) || !strings.HasSuffix(home, installHomeDirectoryName) {
		t.Fatalf("pnpm HOME = %q, want the staging install home", home)
	}
	if record.Env["DSH_HOME"] != record.Env["HOME"] {
		t.Fatalf("DSH_HOME = %q, want the install home", record.Env["DSH_HOME"])
	}
	if record.Env["NPM_CONFIG_REGISTRY"] != installTestRegistry {
		t.Fatalf("NPM_CONFIG_REGISTRY = %q", record.Env["NPM_CONFIG_REGISTRY"])
	}
	if record.Env["PNPM_STORE_DIR"] != storeDirectory {
		t.Fatalf("PNPM_STORE_DIR = %q, want %q", record.Env["PNPM_STORE_DIR"], storeDirectory)
	}
	wantPath := strings.Join([]string{
		filepath.Dir(toolchain.NodePath),
		filepath.Join(dataRoot, "tools", "shims"),
		filepath.Join(dataRoot, "tools", "pnpm"),
		os.Getenv("PATH"),
	}, string(os.PathListSeparator))
	if record.Env["PATH"] != wantPath {
		t.Fatalf("pnpm PATH = %q, want %q", record.Env["PATH"], wantPath)
	}
	if record.Env["DSH_TELEMETRY_MODE"] != "DISABLED" {
		t.Fatalf("DSH_TELEMETRY_MODE = %q", record.Env["DSH_TELEMETRY_MODE"])
	}

	// Staging must be gone after the atomic move.
	entries, err := os.ReadDir(filepath.Join(dataRoot, "runtimes"))
	if err != nil {
		t.Fatalf("read runtimes dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != installTestVersion {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("runtimes entries = %v, want only %s", names, installTestVersion)
	}
	joinedLogs := strings.Join(logs, "\n")
	if !strings.Contains(joinedLogs, "[安装进度]") || !strings.Contains(joinedLogs, "安装完成") {
		t.Fatalf("logs = %q, want progress and completion lines", joinedLogs)
	}

	if !service.Installed(installTestVersion) {
		t.Fatalf("Installed() = false after a successful install")
	}
	if versions := service.InstalledVersions(); len(versions) != 1 || versions[0] != installTestVersion {
		t.Fatalf("InstalledVersions() = %v", versions)
	}

	// A second install must reuse the runtime even when pnpm would fail.
	failingToolchain := &tools.Tools{
		NodePath:   toolchain.NodePath,
		PnpmScript: fakePnpmScript(t, scenarioLauncherFail),
	}
	reuseService := New(nil, failingToolchain, dataRoot)
	reused, err := reuseService.Install(context.Background(), installTestVersion, installTestRegistry, nil)
	if err != nil {
		t.Fatalf("reused Install() error = %v", err)
	}
	if !reused.Reused || reused.EntryPath != result.EntryPath {
		t.Fatalf("reused Install() result = %+v", reused)
	}
}

func TestPnpmWorkspaceYAMLWithOverrides(t *testing.T) {
	t.Parallel()
	empty, err := pnpmWorkspaceYAMLWithOverrides(nil)
	if err != nil {
		t.Fatalf("pnpmWorkspaceYAMLWithOverrides(nil) error = %v", err)
	}
	if string(empty) != pnpmWorkspaceYAML {
		t.Fatalf("empty overrides changed the base block: %q", string(empty))
	}
	overrides := map[string]string{
		"@deepseek-ai/dsh-web":  "0.2.1-rc.3",
		"@deepseek-ai/dsh-base": "0.2.1-rc.3",
		"@deepseek-ai/dsh-z":    "0.2.1-rc.3",
	}
	rendered, err := pnpmWorkspaceYAMLWithOverrides(overrides)
	if err != nil {
		t.Fatalf("pnpmWorkspaceYAMLWithOverrides() error = %v", err)
	}
	want := pnpmWorkspaceYAML +
		"overrides:\n" +
		"  '@deepseek-ai/dsh-base': '0.2.1-rc.3'\n" +
		"  '@deepseek-ai/dsh-web': '0.2.1-rc.3'\n" +
		"  '@deepseek-ai/dsh-z': '0.2.1-rc.3'\n"
	if string(rendered) != want {
		t.Fatalf("workspace yaml = %q, want %q", string(rendered), want)
	}
	// YAML-safe quoting: the scoped name starts with '@', which is not a legal
	// plain scalar, so both key and value must be single quoted.
	if !strings.Contains(string(rendered), "  '@deepseek-ai/dsh-base': '0.2.1-rc.3'") {
		t.Fatalf("workspace yaml does not quote the overrides safely:\n%s", rendered)
	}
	if _, err := pnpmWorkspaceYAMLWithOverrides(map[string]string{"bad name": "0.2.1-rc.3"}); err == nil {
		t.Fatalf("invalid package name was accepted")
	}
	if _, err := pnpmWorkspaceYAMLWithOverrides(map[string]string{"@deepseek-ai/dsh-x": "latest"}); err == nil {
		t.Fatalf("invalid version was accepted")
	}
}

func TestInstallPnpmFailureKeepsStaging(t *testing.T) {
	t.Parallel()
	service, dataRoot, _ := newInstallTestService(t, scenarioLauncherFail, nil)
	var logs []string
	_, err := service.Install(context.Background(), installTestVersion, installTestRegistry, func(line string) {
		logs = append(logs, line)
	})
	if err == nil {
		t.Fatalf("Install() succeeded, want a pnpm failure")
	}
	if !strings.Contains(err.Error(), "安装失败") {
		t.Fatalf("error = %v, want a clear install failure", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "ERR_PNPM") {
		t.Fatalf("logs = %q, want the live pnpm output", logs)
	}
	if _, statErr := os.Stat(runtimePath(t, dataRoot, installTestVersion)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target directory exists after a failed install")
	}
	entries, readErr := os.ReadDir(filepath.Join(dataRoot, "runtimes"))
	if readErr != nil {
		t.Fatalf("read runtimes dir: %v", readErr)
	}
	staging := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-"+installTestVersion+"-") {
			staging = filepath.Join(dataRoot, "runtimes", entry.Name())
		}
	}
	if staging == "" {
		t.Fatalf("staging directory was removed; docs/DSH_INTEGRATION.md 3.2 requires keeping it for diagnosis")
	}
	if _, statErr := os.Stat(filepath.Join(staging, "package.json")); statErr != nil {
		t.Fatalf("staging package.json missing: %v", statErr)
	}
}

func TestInstallValidationFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		scenario string
		contains string
	}{
		{"version mismatch", scenarioAppFail, "版本"},
		{"bin escapes package", scenarioAppUseless, "入口"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, dataRoot, _ := newInstallTestService(t, tc.scenario, nil)
			_, err := service.Install(context.Background(), installTestVersion, installTestRegistry, nil)
			if err == nil {
				t.Fatalf("Install() succeeded for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.contains)
			}
			if _, statErr := os.Stat(runtimePath(t, dataRoot, installTestVersion)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("target directory exists after a failed validation")
			}
		})
	}
}

func TestInstallRejectsExistingUnvalidatedTarget(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	target := runtimePath(t, dataRoot, installTestVersion)
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	toolchain := &tools.Tools{NodePath: testNodePath(t), PnpmScript: fakePnpmScript(t, scenarioOK)}
	service := New(nil, toolchain, dataRoot)
	_, err := service.Install(context.Background(), installTestVersion, installTestRegistry, nil)
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("error = %v, want a refusal to overwrite an existing target", err)
	}
}

func TestInstallInvalidInputs(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	toolchain := &tools.Tools{NodePath: testNodePath(t), PnpmScript: fakePnpmScript(t, scenarioOK)}
	service := New(nil, toolchain, dataRoot)
	if _, err := service.Install(context.Background(), "latest", installTestRegistry, nil); err == nil {
		t.Fatalf("Install(latest) succeeded")
	}
	if _, err := service.Install(context.Background(), installTestVersion, "ftp://registry", nil); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("Install(ftp registry) error = %v, want ErrInvalidRegistry", err)
	}
	noTools := New(nil, nil, dataRoot)
	if _, err := noTools.Install(context.Background(), installTestVersion, installTestRegistry, nil); err == nil {
		t.Fatalf("Install() succeeded without a toolchain")
	}
}

func TestInstallEarlyFailureCleansStaging(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	toolchain := &tools.Tools{NodePath: testNodePath(t), PnpmScript: fakePnpmScript(t, scenarioOK)}
	service := New(nil, toolchain, dataRoot)
	if _, err := service.Install(context.Background(), installTestVersion, server.URL, nil); err == nil {
		t.Fatalf("Install() succeeded while the registry failed")
	}
	entries, err := os.ReadDir(filepath.Join(dataRoot, "runtimes"))
	if err == nil && len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("staging directories left behind after an early failure: %v", names)
	}
}
