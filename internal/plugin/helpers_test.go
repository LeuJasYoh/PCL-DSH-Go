// SPDX-License-Identifier: MIT

package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const (
	fixtureVersion = "0.2.1-rc.3"
	fakeRecordFile = ".fake-plugin-bridge.json"
)

// fakeBridgeRecord captures what the fake Node bridge received.
type fakeBridgeRecord struct {
	Args []string          `json:"args"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// TestMain lets this test binary act as a fake "node" (probe help) and as the
// fake embedded bridge. The scenario is encoded in the path (data root name).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		lowered := strings.ToLower(os.Args[1])
		switch {
		case strings.HasSuffix(lowered, ".mjs"):
			os.Exit(fakeBridgeMain(os.Args[1:]))
		case strings.HasSuffix(lowered, ".js") || strings.HasSuffix(lowered, ".cjs"):
			os.Exit(fakeNodeMain(os.Args[1:]))
		}
	}
	os.Exit(m.Run())
}

func fakeNodeMain(args []string) int {
	script := args[0]
	flags := args[1:]
	scenario := scenarioFromArgs(args)
	launcherHelp := len(flags) == 1 && flags[0] == "--help"
	appHelp := !launcherHelp && containsArg(flags, "--help")
	if scenario == "probe-fail" {
		fmt.Fprintln(os.Stderr, "fake node: cannot run --help")
		return 2
	}
	if launcherHelp {
		if scenario == "no-plugin" {
			fmt.Print(fakeLauncherHelpNoPlugin)
			return 0
		}
		fmt.Print(fakeLauncherHelp)
		return 0
	}
	if appHelp {
		fmt.Print(fakeAppHelp)
		return 0
	}
	fmt.Fprintf(os.Stderr, "fake node: unsupported invocation for %s\n", filepath.Base(script))
	return 64
}

func fakeBridgeMain(args []string) int {
	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake bridge: cannot read working directory")
		return 65
	}
	record := fakeBridgeRecord{Args: append([]string(nil), args...), Cwd: workingDirectory, Env: make(map[string]string, 12)}
	for _, key := range []string{
		"HOME", "DSH_HOME", "DSH_AGENTS_HOME", "NPM_CONFIG_REGISTRY",
		"PNPM_HOME", "PNPM_CONFIG_STORE_DIR", "PATH",
		"HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NO_PROXY",
	} {
		if value := os.Getenv(key); value != "" {
			record.Env[key] = value
		}
	}
	if data, err := json.Marshal(record); err == nil {
		_ = os.WriteFile(filepath.Join(workingDirectory, fakeRecordFile), data, 0o600)
	}
	switch scenarioFromArgs(args) {
	case "mutate-fail":
		fmt.Fprintln(os.Stderr, "ERR_PNPM_META_FETCH_FAIL  GET https://registry.example/boom?token=secret123: network down")
		fmt.Fprintln(os.Stdout, "Progress: resolved 1, reused 0, downloaded 0")
		return 1
	case "sleep":
		time.Sleep(5 * time.Second)
		return 0
	default:
		fmt.Fprintln(os.Stdout, "Progress: resolved 3, reused 0, downloaded 3, added 3, done")
		return 0
	}
}

func scenarioFromArgs(args []string) string {
	joined := strings.ToLower(filepath.ToSlash(strings.Join(args, " ")))
	for _, scenario := range []string{"mutate-fail", "probe-fail", "no-plugin", "sleep"} {
		if strings.Contains(joined, scenario) {
			return scenario
		}
	}
	return "ok"
}

func containsArg(args []string, wanted string) bool {
	for _, argument := range args {
		if argument == wanted {
			return true
		}
	}
	return false
}

const fakeLauncherHelp = `Usage: dsh [options] [command]

Commands:
  web [options]    Start the DeepSeek Harness web application
  plugin           Manage profile plugins

Options:
  --profile <name>  Select the profile to launch
  --help            Show help
`

const fakeLauncherHelpNoPlugin = `Usage: dsh [options] [command]

Commands:
  web [options]    Start the DeepSeek Harness web application

Options:
  --profile <name>  Select the profile to launch
  --help            Show help
`

const fakeAppHelp = `Usage: dsh web [options]

Options:
  --port <port>    Port to listen on
  --host <host>    Host to bind (default 127.0.0.1)
  --no-open        Do not open the browser
`

// pluginFixture is a temp data root with one instance and a fake toolchain.
type pluginFixture struct {
	t        *testing.T
	dataRoot string
	dirs     config.Dirs
	store    *store.Store
	tools    *tools.Tools
	runtime  *runtime.Service
	service  *Service
	instance store.Instance
	entry    string
}

func newPluginFixture(t *testing.T, scenario string) *pluginFixture {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), scenario)
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
	entry := writePluginFakeRuntime(t, dirs.RuntimesDir, fixtureVersion)
	toolchain := &tools.Tools{
		NodePath:   testNodePath(t),
		PnpmScript: filepath.Join(dataRoot, "tools", "pnpm", "pnpm.cjs"),
		ToolRoot:   filepath.Join(dataRoot, "tools"),
	}
	if err := os.MkdirAll(filepath.Dir(toolchain.PnpmScript), 0o700); err != nil {
		t.Fatalf("mkdir fake pnpm: %v", err)
	}
	if err := os.WriteFile(toolchain.PnpmScript, []byte("fake pnpm"), 0o600); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
	dataStore, err := store.Open(dataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	instance, err := dataStore.CreateInstance(store.CreateInstanceInput{
		Name:    "插件测试实例",
		Version: fixtureVersion,
		Profile: "web",
		Port:    allocateFreePort(t),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	runtimeService := runtime.New(discardLogger(), toolchain, dataRoot)
	service, err := New(Options{
		Dirs:     dirs,
		Store:    dataStore,
		Tools:    toolchain,
		Runtime:  runtimeService,
		Registry: "https://registry.npmjs.org",
		Logger:   discardLogger(),
	})
	if err != nil {
		t.Fatalf("plugin.New: %v", err)
	}
	// On WSL2 a Linux-free ephemeral port can still be reachable through
	// Windows localhost forwarding, so the running probe is deterministic in
	// tests; TestInstanceRunningProbe covers the real dial separately.
	disableInstanceRunningProbe(t)
	fixture := &pluginFixture{
		t:        t,
		dataRoot: dataRoot,
		dirs:     dirs,
		store:    dataStore,
		tools:    toolchain,
		runtime:  runtimeService,
		service:  service,
		instance: instance,
		entry:    entry,
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return fixture
}

func writePluginFakeRuntime(t *testing.T, runtimesDirectory, version string) string {
	t.Helper()
	packageDirectory := filepath.Join(runtimesDirectory, version, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(filepath.Join(packageDirectory, "lib"), 0o700); err != nil {
		t.Fatalf("mkdir fake runtime: %v", err)
	}
	manifest := fmt.Sprintf(`{"name":"@deepseek-ai/dsh","version":%q,"bin":{"dsh":"lib/bin.js"}}`, version)
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write fake runtime manifest: %v", err)
	}
	entry := filepath.Join(packageDirectory, "lib", "bin.js")
	if err := os.WriteFile(entry, []byte("fake node entry"), 0o600); err != nil {
		t.Fatalf("write fake runtime entry: %v", err)
	}
	return entry
}

// profileDirectory returns the instance profile directory.
func (f *pluginFixture) profileDirectory() string {
	return filepath.Join(f.dirs.InstancesDir, f.instance.ID, "dsh-home", "profiles", "web")
}

// workspaceDirectory returns the instance workspace.
func (f *pluginFixture) workspaceDirectory() string {
	return filepath.Join(f.dirs.InstancesDir, f.instance.ID, "workspace")
}

// writeProfileManifest writes the profile package.json body.
func (f *pluginFixture) writeProfileManifest(body string) {
	f.t.Helper()
	directory := f.profileDirectory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		f.t.Fatalf("mkdir profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(body), 0o600); err != nil {
		f.t.Fatalf("write profile package.json: %v", err)
	}
}

// writeInstalledPackage creates <base>/<name>/package.json with the given JSON.
func (f *pluginFixture) writeInstalledPackage(base, name, body string) {
	f.t.Helper()
	directory := filepath.Join(base, filepath.FromSlash(name))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		f.t.Fatalf("mkdir installed package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(body), 0o600); err != nil {
		f.t.Fatalf("write installed package: %v", err)
	}
}

// mkdirInWorkspace creates a directory (and optional package.json) inside the
// instance workspace, for local file:/link: plugin specs.
func (f *pluginFixture) mkdirInWorkspace(relative, packageJSON string) {
	f.t.Helper()
	directory := filepath.Join(f.workspaceDirectory(), filepath.FromSlash(relative))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		f.t.Fatalf("mkdir %s in workspace: %v", relative, err)
	}
	if packageJSON != "" {
		if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(packageJSON), 0o600); err != nil {
			f.t.Fatalf("write workspace package.json: %v", err)
		}
	}
}

// readBridgeRecord reads the fake bridge invocation record.
func (f *pluginFixture) readBridgeRecord() fakeBridgeRecord {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.workspaceDirectory(), fakeRecordFile))
	if err != nil {
		f.t.Fatalf("read fake bridge record: %v", err)
	}
	var record fakeBridgeRecord
	if err := json.Unmarshal(data, &record); err != nil {
		f.t.Fatalf("decode fake bridge record: %v", err)
	}
	return record
}

// bridgePath is the materialized embedded bridge path.
func (f *pluginFixture) bridgePath() string {
	return filepath.Join(f.dataRoot, "tools", "shims", pluginBridgeFileName)
}

// allocateFreePort returns a currently-free loopback TCP port. Tests must not
// bind a fixed port: on Windows the parallel package test processes can race for
// it (M3 flake: fixed 39881 occasionally already in use).
func allocateFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close temporary listener: %v", err)
	}
	return port
}

// disableInstanceRunningProbe makes the loopback running check return false for
// the duration of one test and restores the production probe afterwards.
func disableInstanceRunningProbe(t *testing.T) {
	t.Helper()
	previous := instanceRunningProbe
	instanceRunningProbe = func(int) bool { return false }
	t.Cleanup(func() { instanceRunningProbe = previous })
}

// forceInstanceRunning makes the loopback running check return true for the
// duration of one test.
func forceInstanceRunning(t *testing.T) {
	t.Helper()
	previous := instanceRunningProbe
	instanceRunningProbe = func(int) bool { return true }
	t.Cleanup(func() { instanceRunningProbe = previous })
}

func testNodePath(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return executable
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
