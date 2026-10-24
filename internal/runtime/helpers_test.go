// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test helper scenarios select the behaviour of this test binary when it is
// re-executed as a fake Node interpreter. The scenario is encoded in the fake
// entry (or pnpm.cjs) path, because probes and installs run children with a
// blank environment.
const (
	scenarioOK           = "scenario-ok"
	scenarioMinimal      = "scenario-minimal"
	scenarioAppFail      = "scenario-app-fail"
	scenarioAppUseless   = "scenario-app-useless"
	scenarioLauncherFail = "scenario-launcher-fail"
	scenarioSleep        = "scenario-sleep"

	// fakePnpmRecordFile is written by the fake pnpm into its working
	// directory so tests can assert the exact argv/environment used by Install.
	fakePnpmRecordFile = ".fake-pnpm.json"
)

const fakeLauncherHelp = `Usage: dsh [options] [command]

Commands:
  web [options]    Start the DeepSeek Harness web application
  plugin           Manage profile plugins

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

const fakeMinimalHelp = `Usage: dsh [options]

Options:
  --help  Show help
`

const fakeUselessAppHelp = `No help available for this profile.
`

// fakePnpmRecord captures what Install passed to pnpm.
type fakePnpmRecord struct {
	Args []string          `json:"args"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// TestMain lets this test binary act as a fake "node" executable so capability
// probing and installation can be tested without a real Node.js toolchain.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && looksLikeFakeNodeInvocation(os.Args[1]) {
		os.Exit(fakeNodeMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// looksLikeFakeNodeInvocation recognises the script argument of a re-executed
// "node <script>" invocation. Test flags never end with these suffixes.
func looksLikeFakeNodeInvocation(argument string) bool {
	lowered := strings.ToLower(argument)
	for _, suffix := range []string{".js", ".cjs", ".mjs"} {
		if strings.HasSuffix(lowered, suffix) {
			return true
		}
	}
	return false
}

func fakeNodeMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fake node: missing a script argument")
		return 64
	}
	script := args[0]
	flags := args[1:]
	lowered := strings.ToLower(filepath.ToSlash(script))
	if strings.HasSuffix(lowered, ".cjs") || strings.HasSuffix(lowered, ".mjs") {
		return fakePnpmMain(script, flags)
	}
	scenario := fakeScenario(script)
	launcherHelp := len(flags) == 1 && flags[0] == "--help"
	appHelp := !launcherHelp && containsString(flags, "--help")
	switch scenario {
	case scenarioLauncherFail:
		fmt.Fprintln(os.Stderr, "fake node: cannot run --help")
		return 2
	case scenarioSleep:
		time.Sleep(5 * time.Second)
		return 0
	case scenarioMinimal:
		if launcherHelp {
			fmt.Print(fakeMinimalHelp)
			return 0
		}
		fmt.Print(fakeUselessAppHelp)
		return 0
	case scenarioAppFail:
		if launcherHelp {
			fmt.Print(fakeLauncherHelp)
			return 0
		}
		fmt.Fprintln(os.Stderr, "error: unknown profile")
		return 3
	case scenarioAppUseless:
		if launcherHelp {
			fmt.Print(fakeLauncherHelp)
			return 0
		}
		fmt.Print(fakeUselessAppHelp)
		return 0
	default:
		if launcherHelp {
			fmt.Print(fakeLauncherHelp)
			return 0
		}
		if appHelp {
			fmt.Print(fakeAppHelp)
			return 0
		}
		fmt.Fprintln(os.Stderr, "fake node: unsupported invocation")
		return 64
	}
}

// fakePnpmMain emulates the pnpm install step used by Install: it records the
// invocation into its working directory and creates a plausible node_modules
// tree in the staging directory.
func fakePnpmMain(script string, flags []string) int {
	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake pnpm: cannot read working directory")
		return 65
	}
	fakePnpmWriteRecord(workingDirectory, flags)
	scenario := fakeScenario(script)
	if scenario == scenarioLauncherFail {
		fmt.Fprintln(os.Stderr, "ERR_PNPM_META_FETCH_FAIL  GET https://registry.example/boom: network down")
		return 1
	}
	if len(flags) == 0 || flags[0] != "install" {
		fmt.Fprintln(os.Stderr, "fake pnpm: only install is supported")
		return 64
	}
	fmt.Println("Scope: all 1 workspace projects")
	fmt.Println("Progress: resolved 1, reused 0, downloaded 0, added 0")
	packageDirectory := filepath.Join(workingDirectory, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(filepath.Join(packageDirectory, "lib"), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "fake pnpm:", err)
		return 66
	}
	manifestName := "@deepseek-ai/dsh"
	manifestVersion := fakeRequestedVersion(workingDirectory)
	if scenario == scenarioAppFail {
		manifestVersion = "0.0.1-broken"
	}
	binField := `"bin":{"dsh":"lib/bin.js"}`
	if scenario == scenarioMinimal {
		binField = `"bin":"lib/bin.js"`
	}
	if scenario == scenarioAppUseless {
		binField = `"bin":{"dsh":"../escape.js"}`
	}
	manifest := fmt.Sprintf(`{"name":%q,"version":%q,%s}`, manifestName, manifestVersion, binField)
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(manifest), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "fake pnpm:", err)
		return 66
	}
	if err := os.WriteFile(filepath.Join(packageDirectory, "lib", "bin.js"), []byte("fake entry"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "fake pnpm:", err)
		return 66
	}
	if scenario == scenarioAppUseless {
		if err := os.WriteFile(filepath.Join(workingDirectory, "node_modules", "@deepseek-ai", "escape.js"), []byte("outside"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "fake pnpm:", err)
			return 66
		}
	}
	fmt.Println("Progress: resolved 1, reused 0, downloaded 1, added 1, done")
	fmt.Println("Done in 1.2s")
	return 0
}

// fakePnpmWriteRecord stores the argv and relevant environment variables of
// one fake pnpm invocation. Failures are ignored: the install under test will
// surface its own assertion error.
func fakePnpmWriteRecord(workingDirectory string, flags []string) {
	record := fakePnpmRecord{
		Args: append([]string(nil), flags...),
		Cwd:  workingDirectory,
		Env:  make(map[string]string, 16),
	}
	for _, key := range []string{
		"HOME",
		"DSH_HOME",
		"DSH_AGENTS_HOME",
		"HDSL_NPM_REGISTRY",
		"NPM_CONFIG_REGISTRY",
		"NPM_CONFIG_CACHE",
		"PNPM_HOME",
		"PNPM_STORE_DIR",
		"PATH",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NPM_CONFIG_PROXY",
		"NPM_CONFIG_HTTPS_PROXY",
		"NO_PROXY",
		"XDG_CACHE_HOME",
		"DSH_TELEMETRY_MODE",
	} {
		record.Env[key] = os.Getenv(key)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(workingDirectory, fakePnpmRecordFile), data, 0o600)
}

// fakeRequestedVersion reads the requested version from the staged
// package.json so install tests exercise the manifest version check.
func fakeRequestedVersion(workingDirectory string) string {
	data, err := os.ReadFile(filepath.Join(workingDirectory, "package.json"))
	if err != nil {
		return "0.2.1-rc.3"
	}
	text := string(data)
	const dependencyName = "@deepseek-ai/dsh"
	index := strings.Index(text, dependencyName)
	if index < 0 {
		return "0.2.1-rc.3"
	}
	rest := text[index+len(dependencyName):]
	// Skip the closing quote of the key, then the quote that opens the value.
	start := strings.IndexByte(rest, '"')
	if start < 0 {
		return "0.2.1-rc.3"
	}
	rest = rest[start+1:]
	start = strings.IndexByte(rest, '"')
	if start < 0 {
		return "0.2.1-rc.3"
	}
	rest = rest[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "0.2.1-rc.3"
	}
	return rest[:end]
}

func fakeScenario(path string) string {
	lowered := strings.ToLower(filepath.ToSlash(path))
	for _, scenario := range []string{
		scenarioLauncherFail,
		scenarioAppUseless,
		scenarioAppFail,
		scenarioMinimal,
		scenarioSleep,
	} {
		if strings.Contains(lowered, scenario) {
			return scenario
		}
	}
	return scenarioOK
}

// writeRuntimeAt creates a valid-looking <runtimesDir>/<version> runtime tree
// and returns its dsh CLI entry path.
func writeRuntimeAt(t *testing.T, runtimesDirectory, version string) string {
	t.Helper()
	packageDirectory := filepath.Join(runtimesDirectory, version, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(filepath.Join(packageDirectory, "lib"), 0o700); err != nil {
		t.Fatalf("mkdir fake runtime: %v", err)
	}
	manifest := fmt.Sprintf(`{"name":"@deepseek-ai/dsh","version":%q,"bin":{"dsh":"lib/bin.js"}}`, version)
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write fake package.json: %v", err)
	}
	entry := filepath.Join(packageDirectory, "lib", "bin.js")
	if err := os.WriteFile(entry, []byte("fake node entry"), 0o600); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return entry
}

// writeFakeRuntime creates a runtime tree under a scenario-specific temp root,
// so probe/probe-failure scenarios can be selected through the entry path.
func writeFakeRuntime(t *testing.T, scenario, version string) string {
	t.Helper()
	runDirectory := filepath.Join(t.TempDir(), scenario, "runtimes")
	return writeRuntimeAt(t, runDirectory, version)
}

// fakePnpmScript creates a fake pnpm.cjs whose path encodes the scenario.
func fakePnpmScript(t *testing.T, scenario string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), scenario, "pnpm")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("mkdir fake pnpm dir: %v", err)
	}
	path := filepath.Join(directory, "pnpm.cjs")
	if err := os.WriteFile(path, []byte("fake pnpm script"), 0o600); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
	return path
}

// testNodePath returns this test binary, which acts as the fake node executable.
func testNodePath(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return executable
}

// writeCompatibility writes <dataRoot>/config/compatibility.json.
func writeCompatibility(t *testing.T, dataRoot, body string) {
	t.Helper()
	directory := filepath.Join(dataRoot, "config")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "compatibility.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write compatibility.json: %v", err)
	}
}

// readFakePnpmRecord decodes the fake pnpm invocation record.
func readFakePnpmRecord(t *testing.T, runtimeDirectory string) fakePnpmRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runtimeDirectory, fakePnpmRecordFile))
	if err != nil {
		t.Fatalf("read fake pnpm record: %v", err)
	}
	var record fakePnpmRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode fake pnpm record: %v", err)
	}
	return record
}
