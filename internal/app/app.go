// SPDX-License-Identifier: MIT

// Package app is the M1 orchestration layer. It wires the runtime, process and
// store packages into the operations exposed by the HTTP API and the job
// manager (docs/M1_INTERFACES.md §7).
//
// The M0 process semantics are preserved: DSH bootstrap tokens stay in memory,
// readiness/stop/port behaviour is unchanged. The number of concurrently
// running instances is configurable (default 1; 0 = unlimited) via
// Deps.MaxRunningInstances. Blocking operations accept a logf callback so the job manager can
// stream progress lines; every line is redacted before it leaves this package
// and bootstrap tokens never reach logs or job results.
//
// Expected failures are always returned as *apierr.Error (400/404/409/422/
// 500/502/504); context cancellation and deadline errors are returned
// unwrapped so the job layer can map them to CANCELLED/TIMEOUT.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const (
	// defaultProfile is probed by ProbeRuntime; launches probe the instance profile.
	defaultProfile = "web"
	// autoPortMin/autoPortMax bound automatic instance port allocation
	// (docs/DSH_INTEGRATION.md §9: 3080–65535).
	autoPortMin = 3080
	autoPortMax = 65535
	// availableVersionsTTL caches a successful registry version query for the
	// state endpoint (docs/M1_INTERFACES.md §7).
	availableVersionsTTL = 60 * time.Second
	// toolchainVerifyTimeout bounds the startup node/pnpm version probe.
	toolchainVerifyTimeout = 40 * time.Second
	// defaultMaxRunningInstances (0 = unlimited) is used when Deps.MaxRunningInstances is nil
	// (docs/IMPLEMENTATION_SPEC.md; 2C/1.6GiB baseline).
	defaultMaxRunningInstances = 0
)

// Deps are the application dependencies. Tools may be nil: the manager then
// still starts, but runtime installs/probes and instance launches fail with an
// actionable error.
type Deps struct {
	Config config.Config
	Dirs   config.Dirs
	Logger *slog.Logger
	Store  *store.Store
	Tools  *tools.Tools
	// MaxRunningInstances is the concurrent running-instance limit: nil means
	// defaultMaxRunningInstances (0 = unlimited); negative values fall
	// back to the default (config.Load already rejects them).
	MaxRunningInstances *int
}

// LaunchResult is returned once an instance is ready.
type LaunchResult struct {
	InstanceID string
	Status     string
	WebURL     string
}

// ProbeResult is the outcome of a capability probe.
type ProbeResult struct {
	Version       string
	Compatibility string
}

// RuntimeInfo describes one installed runtime (docs/M1_INTERFACES.md §7).
type RuntimeInfo struct {
	Version       string
	Installed     bool
	Compatibility string
}

// LiveStatus is an in-memory snapshot of one instance process.
type LiveStatus struct {
	InstanceID    string
	Port          int
	StatusCode    string // process.StatusXxx
	StatusMessage string // 中文状态
	Ready         bool
	Bootstrap     *url.URL // 仅内存，禁止序列化/日志
}

// InstanceRuntimeStatus is one entry of Running (API state payload).
type InstanceRuntimeStatus struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	Ready      bool   `json:"ready"`
	PID        int    `json:"pid"`
}

// StateSnapshot is the aggregate used by the state endpoint
// (docs/M1_INTERFACES.md §7; the API layer fills the remaining fields).
type StateSnapshot struct {
	CurrentInstanceID string
	Running           []InstanceRuntimeStatus
}

// runtimeBackend is the subset of runtime.Service used by App. Tests inject
// fakes; the production implementation is *runtime.Service.
type runtimeBackend interface {
	Install(ctx context.Context, version, registry string, logf func(string)) (runtime.InstallResult, error)
	Probe(ctx context.Context, version, profile string) (runtime.Capabilities, error)
	AvailableVersions(ctx context.Context, registry string) ([]string, error)
	SetProxy(proxy string)
	Installed(version string) bool
	CompatibilityOf(version string) string
	Entries() []runtime.Entry
	Remove(version string) (string, error)
	BuildLaunchSpec(instance store.Instance, dirs config.Dirs, tl *tools.Tools, registry, proxy string, capabilities runtime.Capabilities) (process.LaunchSpec, error)
	EntryPath(version string) (string, error)
	ProfileInstallEnvironment(dirs config.Dirs, instanceID, profile, registry, proxy string) (dir string, env []string, err error)
}

// instanceProcess is the subset of process.Handle used by App.
type instanceProcess interface {
	InstanceID() string
	Port() int
	Status() process.Status
	Ready() bool
	BootstrapURL() *url.URL
	Stop(ctx context.Context) error
}

// processBackend is the subset of process.Manager used by App.
type processBackend interface {
	Launch(ctx context.Context, spec process.LaunchSpec) (instanceProcess, error)
	Get(instanceID string) (instanceProcess, bool)
	Running() []instanceProcess
	StopAll(ctx context.Context) error
}

// App is the M1 orchestrator. Mutating operations are serialised with a
// try-lock: a second concurrent mutation fails with 409 CONFLICT_BUSY instead
// of queueing (the job manager is the primary single-task gate).
type App struct {
	cfg     config.Config
	dirs    config.Dirs
	logger  *slog.Logger
	store   *store.Store
	tools   *tools.Tools
	runtime runtimeBackend
	process processBackend
	// maxRunningInstances is resolved at construction: >0 is the limit, 0 means
	// unlimited (Deps.MaxRunningInstances == nil resolves to the default 0).
	maxRunningInstances int

	// installer runs pnpm for RestoreProfileDependencies; tests replace it so
	// no real pnpm is needed.
	installer profileInstaller
	// runtimeService is the concrete runtime service behind the runtime backend
	// (nil for test instances built with fake backends).
	runtimeService *runtime.Service

	mu sync.Mutex // serialises mutating operations

	availableMu sync.Mutex
	available   []string
	availableAt time.Time
}

// New builds the production application. The toolchain is verified when
// available but a missing/broken toolchain never prevents startup.
func New(deps Deps) (*App, error) {
	if deps.Logger == nil {
		return nil, errors.New("app: logger is required")
	}
	if deps.Store == nil {
		return nil, errors.New("app: store is required")
	}
	if strings.TrimSpace(deps.Dirs.DataRoot) == "" {
		return nil, errors.New("app: data root is required")
	}
	rt := runtime.New(deps.Logger, deps.Tools, deps.Dirs.DataRoot)
	rt.SetProxy(deps.Store.Settings().Proxy)
	application := newApp(deps, rt, managerProcess{manager: process.NewManager(deps.Logger)})
	application.runtimeService = rt
	if deps.Tools != nil {
		verifyCtx, cancel := context.WithTimeout(context.Background(), toolchainVerifyTimeout)
		defer cancel()
		if err := deps.Tools.Verify(verifyCtx); err != nil {
			deps.Logger.Warn("内置 Node/pnpm 工具链校验失败，安装/启动将不可用", "error", err)
		} else {
			deps.Logger.Info("内置 Node/pnpm 工具链已就绪",
				"node", deps.Tools.NodeVersion, "pnpm", deps.Tools.PnpmVersion, "toolRoot", deps.Tools.ToolRoot)
		}
	}
	return application, nil
}

// newApp assembles an App with explicit backends (used by New and tests).
func newApp(deps Deps, rt runtimeBackend, proc processBackend) *App {
	maxRunning := defaultMaxRunningInstances
	if deps.MaxRunningInstances != nil {
		maxRunning = *deps.MaxRunningInstances
	}
	if maxRunning < 0 {
		maxRunning = defaultMaxRunningInstances
	}
	return &App{
		cfg:                 deps.Config,
		dirs:                deps.Dirs,
		logger:              deps.Logger,
		store:               deps.Store,
		tools:               deps.Tools,
		runtime:             rt,
		process:             proc,
		maxRunningInstances: maxRunning,
		installer:           newExecProfileInstaller(deps.Tools),
	}
}

// managerProcess adapts process.Manager to processBackend.
type managerProcess struct {
	manager *process.Manager
}

func (m managerProcess) Launch(ctx context.Context, spec process.LaunchSpec) (instanceProcess, error) {
	handle, err := m.manager.Launch(ctx, spec)
	if handle == nil {
		return nil, err
	}
	return handle, err
}

func (m managerProcess) Get(instanceID string) (instanceProcess, bool) {
	handle, ok := m.manager.Get(instanceID)
	if !ok || handle == nil {
		return nil, false
	}
	return handle, true
}

func (m managerProcess) Running() []instanceProcess {
	handles := m.manager.Running()
	result := make([]instanceProcess, 0, len(handles))
	for _, handle := range handles {
		if handle != nil {
			result = append(result, handle)
		}
	}
	return result
}

func (m managerProcess) StopAll(ctx context.Context) error {
	return m.manager.StopAll(ctx)
}

// RuntimeService exposes the concrete runtime.Service used by the App so the
// process-wide wiring (pack/plugin services, Lead main wiring) shares the same
// capability cache and proxy configuration instead of creating a second runtime
// service. Test Apps built with fake backends return nil.
func (a *App) RuntimeService() *runtime.Service { return a.runtimeService }

// MaxRunningInstances returns the concurrent running-instance limit: 0 means
// unlimited, otherwise LaunchInstance rejects once that many instances run
// (docs/IMPLEMENTATION_SPEC.md; default 1). The API layer uses the same value
// for its synchronous precheck.
func (a *App) MaxRunningInstances() int { return a.maxRunningInstances }

func (a *App) tryLock() bool { return a.mu.TryLock() }

func (a *App) unlock() { a.mu.Unlock() }

// registry returns the configured npm registry or the default.
func (a *App) registry() string {
	registry := strings.TrimSpace(a.store.Settings().Registry)
	if registry == "" {
		return runtime.DefaultRegistry
	}
	return registry
}

// proxyValue returns the configured outbound proxy.
func (a *App) proxyValue() string { return strings.TrimSpace(a.store.Settings().Proxy) }

// emit sends one progress line to both the job log and the manager log.
func (a *App) emit(logf func(string), format string, args ...any) {
	line := strings.TrimSpace(logging.Redact(fmt.Sprintf(format, args...)))
	if line == "" {
		return
	}
	if logf != nil {
		logf(line)
	}
	a.logger.Info(line, "component", "app")
}

// forwardLogf returns a redacting sink for high-volume runtime/process output.
func (a *App) forwardLogf(logf func(string)) func(string) {
	return func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		line = logging.Redact(line)
		if logf != nil {
			logf(line)
			return
		}
		a.logger.Debug(line, "component", "app")
	}
}

// instanceActive reports whether the instance has a live process tree (root
// process alive or tracked descendants) according to the process manager.
func (a *App) instanceActive(instanceID string) bool {
	for _, handle := range a.process.Running() {
		if handle.InstanceID() == instanceID {
			return true
		}
	}
	return false
}

func validationError(message string) error { return apierr.Validation(message) }

func notFoundError(message string) error { return apierr.NotFound(message) }

func conflictError(code, message string) error { return apierr.New(409, code, message) }

func internalError(message string) error { return apierr.New(500, apierr.CodeInternal, message) }

// mapStoreError converts store failures into API errors.
func (a *App) mapStoreError(action string, err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := apierr.As(err); ok {
		return apiErr
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFoundError("实例不存在。")
	case errors.Is(err, store.ErrInvalidInput):
		return validationError(err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		a.logger.Error(action+"失败", "error", err)
		return internalError(action + "失败，请稍后重试。")
	}
}

// mapRuntimeFailure converts runtime/upstream failures into API errors. The
// original error must already have been logged by the caller.
func (a *App) mapRuntimeFailure(action string, err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := apierr.As(err); ok {
		return apiErr
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, runtime.ErrUpstreamTimeout):
		return apierr.New(504, apierr.CodeUpstreamTimeout, action+"超时，请稍后重试。")
	case errors.Is(err, runtime.ErrUpstream):
		return apierr.New(502, apierr.CodeUpstreamError, action+"失败：npm 上游服务不可用，请稍后重试或更换 registry。")
	case errors.Is(err, runtime.ErrInvalidRegistry):
		return apierr.New(400, apierr.CodeValidation, "npm registry 地址无效，请在设置中修正。")
	default:
		return internalError(action + "失败，请查看任务日志。")
	}
}

// Shutdown stops every manager-owned DSH process tree.
func (a *App) Shutdown(ctx context.Context) error {
	if err := a.process.StopAll(ctx); err != nil {
		a.logger.Error("停机时停止实例失败", "error", err)
		return err
	}
	return nil
}
