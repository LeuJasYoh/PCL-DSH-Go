// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const testBootstrapToken = "bootstrap-token-should-not-leak"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestDirs(t *testing.T) config.Dirs {
	t.Helper()
	root := t.TempDir()
	dirs := config.Dirs{
		DataRoot:     root,
		ConfigDir:    filepath.Join(root, "config"),
		InstancesDir: filepath.Join(root, "instances"),
		RuntimesDir:  filepath.Join(root, "runtimes"),
		CacheDir:     filepath.Join(root, "cache"),
		LogsDir:      filepath.Join(root, "logs"),
		TrashDir:     filepath.Join(root, "trash"),
	}
	for _, dir := range []string{dirs.ConfigDir, dirs.InstancesDir, dirs.RuntimesDir, dirs.CacheDir, dirs.LogsDir, dirs.TrashDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("创建目录 %s 失败: %v", dir, err)
		}
	}
	return dirs
}

func newTestApp(t *testing.T) (*App, *fakeRuntime, *fakeProcess, config.Dirs) {
	t.Helper()
	dirs := newTestDirs(t)
	st, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fakeRT := newFakeRuntime()
	fakeProc := newFakeProcess()
	application := newApp(Deps{
		Config: config.Config{DataRoot: dirs.DataRoot},
		Dirs:   dirs,
		Logger: discardLogger(),
		Store:  st,
		Tools:  &tools.Tools{NodePath: "/fake/node", PnpmScript: "/fake/pnpm.cjs"},
	}, fakeRT, fakeProc)
	return application, fakeRT, fakeProc, dirs
}

func requireAPIError(t *testing.T, err error, wantStatus int, wantCode string) *apierr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 (status=%d code=%s)，实际 nil", wantStatus, wantCode)
	}
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("错误不是 *apierr.Error: %v", err)
	}
	if wantStatus != 0 && apiErr.Status != wantStatus {
		t.Fatalf("状态码 = %d，期望 %d（错误 %v）", apiErr.Status, wantStatus, err)
	}
	if wantCode != "" && apiErr.Code != wantCode {
		t.Fatalf("错误码 = %q，期望 %q（错误 %v）", apiErr.Code, wantCode, err)
	}
	return apiErr
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("监听临时端口失败: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		if bindable, err := portBindable(context.Background(), port); err == nil && bindable {
			return port
		}
	}
	t.Fatal("无法找到可绑定的回环端口")
	return 0
}

func mustCreateInstance(t *testing.T, application *App, name string, port int) store.Instance {
	t.Helper()
	instance, err := application.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name:    name,
		Version: "0.2.1-rc.3",
		Profile: "web",
		Port:    port,
	})
	if err != nil {
		t.Fatalf("CreateInstance(%q, %d) 失败: %v", name, port, err)
	}
	return instance
}

// --- fakes -----------------------------------------------------------------

type fakeRuntime struct {
	mu sync.Mutex

	installCalls  int
	installArgs   []string
	installResult runtime.InstallResult
	installErr    error

	probeCalls    int
	probeCaps     runtime.Capabilities
	probeErr      error
	lastProbeVer  string
	lastProbeProf string

	availableCalls int
	available      []string
	availableErr   error

	installed     map[string]bool
	entries       []runtime.Entry
	compatibility string

	removeCalls  int
	removeTrash  string
	removeErr    error
	removed      []string
	proxy        string
	buildSpecErr error

	entryPaths      map[string]string
	entryPathErr    error
	entryPathCalls  int
	profileEnvDir   string
	profileEnv      []string
	profileEnvErr   error
	profileEnvCalls int
	lastProfileEnv  profileEnvCall
}

// profileEnvCall records ProfileInstallEnvironment arguments for assertions.
type profileEnvCall struct {
	Dirs       config.Dirs
	InstanceID string
	Profile    string
	Registry   string
	Proxy      string
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		installResult: runtime.InstallResult{Version: "0.2.1-rc.3", EntryPath: "/fake/entry.js"},
		probeCaps:     runtime.Capabilities{Version: "0.2.1-rc.3", AppOptions: []string{"--port"}, LauncherOptions: []string{"--profile"}},
		available:     []string{"0.2.1-rc.3", "0.2.0"},
		installed:     map[string]bool{},
		compatibility: "verified",
	}
}

func (f *fakeRuntime) Install(_ context.Context, version, registry string, logf func(string)) (runtime.InstallResult, error) {
	f.mu.Lock()
	f.installCalls++
	f.installArgs = append(f.installArgs, version+"@"+registry)
	result, err := f.installResult, f.installErr
	f.mu.Unlock()
	if logf != nil {
		logf("fake pnpm output for " + version)
	}
	return result, err
}

func (f *fakeRuntime) Probe(_ context.Context, version, profile string) (runtime.Capabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls++
	f.lastProbeVer = version
	f.lastProbeProf = profile
	caps := f.probeCaps
	caps.Version = version
	caps.Profile = profile
	return caps, f.probeErr
}

func (f *fakeRuntime) AvailableVersions(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.availableCalls++
	return append([]string(nil), f.available...), f.availableErr
}

func (f *fakeRuntime) SetProxy(proxy string) {
	f.mu.Lock()
	f.proxy = proxy
	f.mu.Unlock()
}

func (f *fakeRuntime) Installed(version string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed[version]
}

func (f *fakeRuntime) EntryPath(version string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entryPathCalls++
	if f.entryPathErr != nil {
		return "", f.entryPathErr
	}
	if path, ok := f.entryPaths[version]; ok {
		return path, nil
	}
	return "", os.ErrNotExist
}

func (f *fakeRuntime) ProfileInstallEnvironment(dirs config.Dirs, instanceID, profile, registry, proxy string) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileEnvCalls++
	f.lastProfileEnv = profileEnvCall{
		Dirs:       dirs,
		InstanceID: instanceID,
		Profile:    profile,
		Registry:   registry,
		Proxy:      proxy,
	}
	if f.profileEnvErr != nil {
		return "", nil, f.profileEnvErr
	}
	dir := f.profileEnvDir
	if dir == "" {
		dir = filepath.Join(dirs.InstancesDir, instanceID, "dsh-home", "profiles", profile)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	return dir, append([]string(nil), f.profileEnv...), nil
}

func (f *fakeRuntime) CompatibilityOf(string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.compatibility
}

func (f *fakeRuntime) Entries() []runtime.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.Entry(nil), f.entries...)
}

func (f *fakeRuntime) Remove(version string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	if f.removeErr != nil {
		return "", f.removeErr
	}
	f.removed = append(f.removed, version)
	return f.removeTrash, nil
}

func (f *fakeRuntime) BuildLaunchSpec(instance store.Instance, dirs config.Dirs, _ *tools.Tools, _, _ string, _ runtime.Capabilities) (process.LaunchSpec, error) {
	f.mu.Lock()
	err := f.buildSpecErr
	f.mu.Unlock()
	if err != nil {
		return process.LaunchSpec{}, err
	}
	return process.LaunchSpec{
		InstanceID: instance.ID,
		Command:    []string{"/fake/node", "/fake/entry.js", "--port", strconv.Itoa(instance.Port)},
		Port:       instance.Port,
		LogPath:    filepath.Join(dirs.LogsDir, instance.ID+".log"),
	}, nil
}

type fakeHandle struct {
	mu sync.Mutex

	id        string
	port      int
	status    process.Status
	ready     bool
	bootstrap *url.URL
	stopErr   error
	stopCalls int
}

func (h *fakeHandle) InstanceID() string { return h.id }

func (h *fakeHandle) Port() int { return h.port }

func (h *fakeHandle) Status() process.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *fakeHandle) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready
}

func (h *fakeHandle) BootstrapURL() *url.URL {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.bootstrap == nil {
		return nil
	}
	copied := *h.bootstrap
	return &copied
}

func (h *fakeHandle) Stop(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopCalls++
	return h.stopErr
}

func (h *fakeHandle) active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.status.Code {
	case process.StatusStarting, process.StatusRunning, process.StatusNotReady, process.StatusStopping:
		return true
	default:
		return false
	}
}

func newRunningFakeHandle(instanceID string, port int) *fakeHandle {
	bootstrap, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port) + "/?token=" + testBootstrapToken)
	return &fakeHandle{
		id:        instanceID,
		port:      port,
		status:    process.Status{Code: process.StatusRunning, Message: "运行中", PID: 4242},
		ready:     true,
		bootstrap: bootstrap,
	}
}

type fakeProcess struct {
	mu sync.Mutex

	handles     map[string]*fakeHandle
	launchSpecs []process.LaunchSpec
	launchErr   error
	launchNext  *fakeHandle
	stopAllErr  error
	stopAllHits int
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{handles: make(map[string]*fakeHandle)}
}

func (f *fakeProcess) add(handle *fakeHandle) {
	f.mu.Lock()
	f.handles[handle.id] = handle
	f.mu.Unlock()
}

func (f *fakeProcess) remove(instanceID string) {
	f.mu.Lock()
	delete(f.handles, instanceID)
	f.mu.Unlock()
}

func (f *fakeProcess) Launch(_ context.Context, spec process.LaunchSpec) (instanceProcess, error) {
	f.mu.Lock()
	f.launchSpecs = append(f.launchSpecs, spec)
	if f.launchErr != nil {
		err := f.launchErr
		f.mu.Unlock()
		return nil, err
	}
	handle := f.launchNext
	if handle == nil {
		handle = newRunningFakeHandle(spec.InstanceID, spec.Port)
	}
	f.handles[spec.InstanceID] = handle
	f.mu.Unlock()
	return handle, nil
}

func (f *fakeProcess) Get(instanceID string) (instanceProcess, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle, ok := f.handles[instanceID]
	if !ok || handle == nil {
		return nil, false
	}
	return handle, true
}

func (f *fakeProcess) Running() []instanceProcess {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]instanceProcess, 0, len(f.handles))
	for _, handle := range f.handles {
		if handle.active() {
			result = append(result, handle)
		}
	}
	return result
}

func (f *fakeProcess) StopAll(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopAllHits++
	return f.stopAllErr
}
