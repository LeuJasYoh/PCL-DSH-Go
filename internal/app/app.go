// SPDX-License-Identifier: GPL-3.0-or-later

// Package app wires the runtime, process and store packages into the M0
// orchestration used by internal/server (the M1 API will build on it).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// Deps are the application dependencies. Tools may be nil: the manager then
// still starts and serves /api/health, but runtime installs and instance
// launches fail with an actionable error.
type Deps struct {
	Config config.Config
	Dirs   config.Dirs
	Logger *slog.Logger
	Store  *store.Store
	Tools  *tools.Tools
}

// App implements server.Orchestrator on top of runtime.Service and
// process.Manager. Mutating operations are serialised: DSH is a single
// instance/single task deployment (docs/DEVELOPMENT_PLAN.md).
type App struct {
	cfg     config.Config
	dirs    config.Dirs
	logger  *slog.Logger
	store   *store.Store
	tools   *tools.Tools
	runtime *runtime.Service
	process *process.Manager

	mu sync.Mutex // serialises install/launch/stop
}

// New builds the application. It verifies the toolchain when available but
// never fails because of a missing toolchain (failures are reported per call).
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
	application := &App{
		cfg:     deps.Config,
		dirs:    deps.Dirs,
		logger:  deps.Logger,
		store:   deps.Store,
		tools:   deps.Tools,
		runtime: rt,
		process: process.NewManager(deps.Logger),
	}
	if deps.Tools != nil {
		verifyCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
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

// tryLock acquires the mutation mutex without blocking. It reports whether the
// lock was acquired; a false return means another task is in progress.
func (a *App) tryLock() bool { return a.mu.TryLock() }

func (a *App) unlock() { a.mu.Unlock() }

// busyError is returned when a mutating operation is already running.
func busyError() error {
	return server.NewAPIError(409, server.CodeConflictBusy, "已有任务正在进行中，请稍后再试。")
}

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

// logf forwards pnpm output into the manager log.
func (a *App) logf(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	a.logger.Info(line, "component", "runtime-install")
}

// mapRuntimeError maps runtime/upstream failures to API errors.
func mapRuntimeError(action string, err error) error {
	switch {
	case errors.Is(err, runtime.ErrUpstreamTimeout), errors.Is(err, context.DeadlineExceeded):
		return server.NewAPIError(504, "UPSTREAM_TIMEOUT", action+"超时，请稍后重试。")
	case errors.Is(err, runtime.ErrUpstream):
		return server.NewAPIError(502, "UPSTREAM_ERROR", action+"失败：上游 npm 服务不可用。")
	case errors.Is(err, runtime.ErrInvalidRegistry):
		return server.NewAPIError(400, server.CodeValidation, action+"失败：npm 源地址无效。")
	default:
		return server.NewAPIError(500, server.CodeInternal, action+"失败："+err.Error())
	}
}

// InstallRuntime installs one exact DSH version (blocking).
func (a *App) InstallRuntime(ctx context.Context, version string) (server.RuntimeInstallResult, error) {
	version = strings.TrimSpace(version)
	if !runtime.IsVersion(version) {
		return server.RuntimeInstallResult{}, server.NewAPIError(400, server.CodeValidation,
			"版本号必须是完整的 SemVer（例如 0.2.1-rc.3）。")
	}
	if !a.tryLock() {
		return server.RuntimeInstallResult{}, busyError()
	}
	defer a.unlock()
	if a.tools == nil {
		return server.RuntimeInstallResult{}, server.NewAPIError(500, server.CodeInternal,
			"未定位到内置 Node/pnpm 工具链，无法安装运行时；请检查 --pcl.tool-root。")
	}
	result, err := a.runtime.Install(ctx, version, a.registry(), a.logf)
	if err != nil {
		a.logger.Error("运行时安装失败", "version", version, "error", err)
		return server.RuntimeInstallResult{}, mapRuntimeError("安装运行时 "+version, err)
	}
	a.logger.Info("运行时安装完成", "version", result.Version, "entry", result.EntryPath, "reused", result.Reused)
	return server.RuntimeInstallResult{
		Version:   result.Version,
		EntryPath: result.EntryPath,
		Reused:    result.Reused,
	}, nil
}

// LaunchInstance starts one instance and blocks until the workbench is ready.
func (a *App) LaunchInstance(ctx context.Context, instanceID string) (server.InstanceLaunchResult, error) {
	if !a.tryLock() {
		return server.InstanceLaunchResult{}, busyError()
	}
	defer a.unlock()

	instance, ok := a.store.GetInstance(instanceID)
	if !ok {
		return server.InstanceLaunchResult{}, server.NewAPIError(404, server.CodeNotFound, "实例不存在。")
	}
	if handle, running := a.process.Get(instanceID); running && handleActive(handle) {
		return server.InstanceLaunchResult{}, server.NewAPIError(409, server.CodeConflictBusy,
			"该实例已在运行中。")
	}
	for _, handle := range a.process.Running() {
		if handleActive(handle) {
			return server.InstanceLaunchResult{}, server.NewAPIError(409, server.CodeConflictRunning,
				"已有其他实例在运行；当前版本只允许同时运行一个 DSH 实例。")
		}
	}
	if a.tools == nil {
		return server.InstanceLaunchResult{}, server.NewAPIError(500, server.CodeInternal,
			"未定位到内置 Node/pnpm 工具链，无法启动实例；请检查 --pcl.tool-root。")
	}
	if !a.runtime.Installed(instance.Version) {
		return server.InstanceLaunchResult{}, server.NewAPIError(409, server.CodeInstanceNotReady,
			fmt.Sprintf("Harness %s 尚未安装，请先安装运行时。", instance.Version))
	}
	if busy, err := portInUse(instance.Port); err != nil {
		return server.InstanceLaunchResult{}, server.NewAPIError(500, server.CodeInternal,
			"检查实例端口失败："+err.Error())
	} else if busy {
		return server.InstanceLaunchResult{}, server.NewAPIError(409, server.CodeConflictPortBusy,
			fmt.Sprintf("端口 %d 已被占用，请修改实例端口后重试。", instance.Port))
	}

	capabilities, err := a.runtime.Probe(ctx, instance.Version, instance.Profile)
	if err != nil {
		a.logger.Error("运行时能力探测失败", "instance", instanceID, "version", instance.Version, "error", err)
		return server.InstanceLaunchResult{}, server.NewAPIError(422, server.CodeUnsupportedRuntime,
			"运行时能力探测失败，已拒绝启动："+err.Error())
	}
	spec, err := a.runtime.BuildLaunchSpec(instance, a.dirs, a.tools, a.registry(), a.proxyValue(), capabilities)
	if err != nil {
		return server.InstanceLaunchResult{}, server.NewAPIError(500, server.CodeInternal,
			"构建启动命令失败："+err.Error())
	}
	a.logger.Info("正在启动实例", "instance", instanceID, "version", instance.Version,
		"profile", instance.Profile, "port", instance.Port, "log", spec.LogPath)
	handle, err := a.process.Launch(ctx, spec)
	if err != nil {
		a.logger.Error("实例启动失败", "instance", instanceID, "error", err)
		return server.InstanceLaunchResult{}, server.NewAPIError(409, server.CodeInstanceNotReady,
			"实例启动未就绪："+err.Error()+"；请查看实例日志 "+spec.LogPath)
	}
	a.logger.Info("实例已就绪", "instance", instanceID, "port", instance.Port)
	return server.InstanceLaunchResult{
		InstanceID: instanceID,
		Status:     handle.Status().Message,
		WebURL:     "/workbench/" + instanceID + "/",
	}, nil
}

// StopInstance stops a running instance. Stopping a stopped instance succeeds.
func (a *App) StopInstance(ctx context.Context, instanceID string) error {
	handle, ok := a.process.Get(instanceID)
	if !ok || !handleActive(handle) {
		return nil
	}
	if !a.tryLock() {
		return busyError()
	}
	defer a.unlock()
	if err := handle.Stop(ctx); err != nil {
		a.logger.Error("停止实例失败", "instance", instanceID, "error", err)
		return server.NewAPIError(500, server.CodeInternal, "停止实例失败："+err.Error())
	}
	a.logger.Info("实例已停止", "instance", instanceID)
	return nil
}

// ResolveProxy returns the /workbench target of a ready instance.
func (a *App) ResolveProxy(_ context.Context, instanceID string) (proxy.Target, error) {
	instance, ok := a.store.GetInstance(instanceID)
	if !ok {
		return proxy.Target{}, proxy.ErrInstanceNotFound
	}
	handle, ok := a.process.Get(instanceID)
	if !ok {
		return proxy.Target{}, proxy.ErrInstanceNotRunning
	}
	status := handle.Status()
	if status.Code == process.StatusNotReady {
		return proxy.Target{}, proxy.ErrInstanceNotReady
	}
	if status.Code != process.StatusRunning || !handle.Ready() {
		return proxy.Target{}, proxy.ErrInstanceNotRunning
	}
	bootstrap := handle.BootstrapURL()
	if bootstrap == nil {
		return proxy.Target{}, proxy.ErrInstanceNotReady
	}
	path := bootstrap.Path
	if path == "" {
		path = "/"
	}
	return proxy.Target{
		InstanceID:     instanceID,
		Port:           instance.Port,
		BootstrapPath:  path,
		BootstrapQuery: bootstrap.RawQuery,
		Ready:          true,
	}, nil
}

// Running lists tracked instances with live status.
func (a *App) Running() []server.InstanceRuntimeStatus {
	handles := a.process.Running()
	statuses := make([]server.InstanceRuntimeStatus, 0, len(handles))
	for _, handle := range handles {
		status := handle.Status()
		statuses = append(statuses, server.InstanceRuntimeStatus{
			InstanceID: handle.InstanceID(),
			Status:     status.Message,
			Ready:      handle.Ready(),
			PID:        status.PID,
		})
	}
	return statuses
}

// Shutdown stops all manager-owned instances (called on graceful shutdown).
func (a *App) Shutdown(ctx context.Context) error {
	if err := a.process.StopAll(ctx); err != nil {
		a.logger.Error("停机时停止实例失败", "error", err)
		return err
	}
	return nil
}

// handleActive reports whether a handle still represents a live/progressing
// instance (starting, running or not-ready-but-alive).
func handleActive(handle *process.Handle) bool {
	switch handle.Status().Code {
	case process.StatusStarting, process.StatusRunning, process.StatusNotReady:
		return true
	default:
		return false
	}
}

// portInUse reports whether 127.0.0.1:port accepts a TCP connection. DSH binds
// loopback only, so a successful connect means the port is taken
// (docs/DSH_INTEGRATION.md 5.1).
func portInUse(port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, fmt.Errorf("端口 %d 超出范围", port)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return false, nil
		}
		return false, nil
	}
	_ = conn.Close()
	return true, nil
}
