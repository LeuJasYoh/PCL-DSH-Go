// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// CreateInstance creates a configured instance. Port 0 triggers automatic
// allocation from 3080–65535, skipping ports used by configured instances and
// ports that cannot be bound on 127.0.0.1.
func (a *App) CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error) {
	if err := ctx.Err(); err != nil {
		return store.Instance{}, err
	}
	if !a.tryLock() {
		return store.Instance{}, apierr.Busy()
	}
	defer a.unlock()

	if in.Port == 0 {
		port, err := a.allocatePort(ctx)
		if err != nil {
			return store.Instance{}, err
		}
		in.Port = port
	} else if err := a.checkPortAvailable(ctx, in.Port); err != nil {
		return store.Instance{}, err
	}

	instance, err := a.store.CreateInstance(in)
	if err != nil {
		return store.Instance{}, a.mapStoreError("创建实例", err)
	}
	// Create the instance directory layout eagerly (docs/IMPLEMENTATION_SPEC.md
	// §3) so that DELETE of a never-launched instance still moves a real
	// directory to trash and returns a non-empty trashedPath.
	if err := a.ensureInstanceDirectories(instance.ID); err != nil {
		a.logger.Error("创建实例目录失败，回滚实例记录",
			"instance", instance.ID, "dir", filepath.Join(a.dirs.InstancesDir, instance.ID), "error", err)
		_ = os.RemoveAll(filepath.Join(a.dirs.InstancesDir, instance.ID))
		if rollbackErr := a.store.DeleteInstance(instance.ID); rollbackErr != nil {
			a.logger.Error("回滚失败的实例记录失败", "instance", instance.ID, "error", rollbackErr)
		}
		return store.Instance{}, internalError("创建实例目录失败，请检查数据目录权限。")
	}
	a.logger.Info("实例已创建",
		"instance", instance.ID, "name", instance.Name, "version", instance.Version,
		"profile", instance.Profile, "port", instance.Port)
	return instance, nil
}

// instanceSubdirectories is the per-instance directory layout from
// docs/IMPLEMENTATION_SPEC.md §3, created eagerly by CreateInstance (MkdirAll is
// idempotent; launch/runtime code re-creates them defensively).
var instanceSubdirectories = []string{"dsh-home", "workspace", "agents-home"}

// ensureInstanceDirectories creates instances/<id>/{dsh-home,workspace,agents-home}
// with 0700 permissions.
func (a *App) ensureInstanceDirectories(instanceID string) error {
	instanceDir := filepath.Join(a.dirs.InstancesDir, instanceID)
	for _, sub := range instanceSubdirectories {
		if err := os.MkdirAll(filepath.Join(instanceDir, sub), 0o700); err != nil {
			return fmt.Errorf("创建实例子目录 %s 失败: %w", sub, err)
		}
	}
	return nil
}

// allocatePort finds a free instance port: unused by configured instances and
// bindable on loopback.
func (a *App) allocatePort(ctx context.Context) (int, error) {
	used := make(map[int]struct{}, 4)
	for _, instance := range a.store.ListInstances() {
		used[instance.Port] = struct{}{}
	}
	for port := autoPortMin; port <= autoPortMax; port++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, taken := used[port]; taken {
			continue
		}
		bindable, err := portBindable(ctx, port)
		if err != nil {
			return 0, err
		}
		if bindable {
			return port, nil
		}
	}
	return 0, conflictError(apierr.CodeConflictPortBusy,
		fmt.Sprintf("端口 %d–%d 范围内没有可用的回环端口，请释放端口后重试。", autoPortMin, autoPortMax))
}

// checkPortAvailable validates an explicitly requested instance port.
func (a *App) checkPortAvailable(ctx context.Context, port int) error {
	if port < 1 || port > 65535 {
		return validationError(fmt.Sprintf("端口须在 1-65535 之间（当前 %d）。", port))
	}
	for _, existing := range a.store.ListInstances() {
		if existing.Port == port {
			return conflictError(apierr.CodeConflictPortBusy,
				fmt.Sprintf("端口 %d 已分配给实例「%s」。", port, existing.Name))
		}
	}
	bindable, err := portBindable(ctx, port)
	if err != nil {
		return err
	}
	if !bindable {
		return conflictError(apierr.CodeConflictPortBusy, fmt.Sprintf("端口 %d 已被其他程序占用。", port))
	}
	return nil
}

// portBindable reports whether 127.0.0.1:port can be bound right now. A
// successful probe closes the listener immediately; callers must still tolerate
// a concurrent grab before the DSH process binds it.
func portBindable(ctx context.Context, port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, nil
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, nil
	}
	if closeErr := listener.Close(); closeErr != nil {
		return false, nil
	}
	return true, nil
}

// UpdateInstance updates only the mutable instance fields (name/port). Changing
// the port of a running instance is rejected with CONFLICT_INSTANCE_RUNNING;
// version/profile are immutable in the store.
func (a *App) UpdateInstance(ctx context.Context, id string, name *string, port *int) (store.Instance, error) {
	if err := ctx.Err(); err != nil {
		return store.Instance{}, err
	}
	if !a.tryLock() {
		return store.Instance{}, apierr.Busy()
	}
	defer a.unlock()

	current, ok := a.store.GetInstance(id)
	if !ok {
		return store.Instance{}, notFoundError("实例不存在。")
	}
	if port != nil && *port != current.Port {
		if a.instanceActive(id) {
			return store.Instance{}, conflictError(apierr.CodeConflictInstanceRunning,
				"实例正在运行，不能修改端口；请先停止实例。")
		}
		if err := a.checkPortAvailable(ctx, *port); err != nil {
			return store.Instance{}, err
		}
	}
	updated, err := a.store.UpdateInstance(id, store.UpdateInstanceInput{Name: name, Port: port})
	if err != nil {
		return store.Instance{}, a.mapStoreError("更新实例", err)
	}
	a.logger.Info("实例已更新", "instance", updated.ID, "name", updated.Name, "port", updated.Port)
	return updated, nil
}

// DeleteInstance moves the instance directory to trash and removes the instance
// record; running instances are rejected. CreateInstance creates the directory
// eagerly, so a normal deletion always returns a non-empty trashedPath. For
// legacy instances whose directory is missing, deletion still succeeds (the
// store record is removed) but the trashed path is empty and a warning is
// logged.
//
// The store clears the selection when the selected instance is deleted.
func (a *App) DeleteInstance(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !a.tryLock() {
		return "", apierr.Busy()
	}
	defer a.unlock()

	if _, ok := a.store.GetInstance(id); !ok {
		return "", notFoundError("实例不存在。")
	}
	if a.instanceActive(id) {
		return "", conflictError(apierr.CodeConflictInstanceRunning,
			"实例正在运行，不能删除；请先停止实例。")
	}

	instanceDir := filepath.Join(a.dirs.InstancesDir, id)
	trashedPath := ""
	_, statErr := os.Lstat(instanceDir)
	switch {
	case statErr == nil:
		path, err := store.MoveToTrash(a.dirs.DataRoot, "instance", instanceDir)
		if err != nil {
			a.logger.Error("实例目录移入回收站失败", "instance", id, "dir", instanceDir, "error", err)
			return "", internalError("删除实例目录失败，请检查数据目录权限。")
		}
		trashedPath = path
	case errors.Is(statErr, fs.ErrNotExist):
		a.logger.Warn("实例目录不存在，跳过 trash（旧数据）", "instance", id, "dir", instanceDir)
	case !errors.Is(statErr, fs.ErrNotExist):
		a.logger.Error("检查实例目录失败", "instance", id, "dir", instanceDir, "error", statErr)
		return "", internalError("删除实例目录失败，请检查数据目录权限。")
	}

	if err := a.store.DeleteInstance(id); err != nil {
		return "", a.mapStoreError("删除实例", err)
	}
	a.logger.Info("实例已删除", "instance", id, "trash", trashedPath)
	return trashedPath, nil
}

// LaunchInstance probes the runtime, starts the instance process and blocks
// until the workbench is ready. logf receives phase/progress lines.
func (a *App) LaunchInstance(ctx context.Context, instanceID string, logf func(string)) (LaunchResult, error) {
	if err := ctx.Err(); err != nil {
		return LaunchResult{}, err
	}
	if !a.tryLock() {
		return LaunchResult{}, apierr.Busy()
	}
	defer a.unlock()

	instance, ok := a.store.GetInstance(instanceID)
	if !ok {
		return LaunchResult{}, notFoundError("实例不存在。")
	}
	if a.instanceActive(instanceID) {
		return LaunchResult{}, conflictError(apierr.CodeConflictRunningInstance, "该实例已在运行。")
	}
	for _, handle := range a.process.Running() {
		if handle.InstanceID() != instanceID {
			return LaunchResult{}, conflictError(apierr.CodeConflictRunningInstance,
				"已有其他实例在运行；当前版本只允许同时运行一个 DSH 实例。")
		}
	}
	if a.tools == nil {
		return LaunchResult{}, internalError("未定位到内置 Node/pnpm 工具链，无法启动实例；请检查 --pcl.tool-root。")
	}
	if !a.runtime.Installed(instance.Version) {
		return LaunchResult{}, apierr.New(409, apierr.CodeInstanceNotReady,
			fmt.Sprintf("Harness %s 尚未安装，请先安装运行时。", instance.Version))
	}

	a.emit(logf, "正在探测 Harness %s 的能力（profile=%s）…", instance.Version, instance.Profile)
	capabilities, err := a.runtime.Probe(ctx, instance.Version, instance.Profile)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return LaunchResult{}, ctxErr
		}
		if apiErr, ok := apierr.As(err); ok {
			return LaunchResult{}, apiErr
		}
		a.logger.Error("运行时能力探测失败", "instance", instanceID, "version", instance.Version, "error", err)
		return LaunchResult{}, apierr.New(422, apierr.CodeUnsupportedRuntime,
			"运行时能力探测失败，已拒绝启动；请查看实例日志或补充 compatibility.json。")
	}
	for _, line := range strings.Split(capabilities.Summary(), "\\n") {
		if line = strings.TrimSpace(line); line != "" {
			a.emit(logf, "%s", line)
		}
	}

	spec, err := a.runtime.BuildLaunchSpec(instance, a.dirs, a.tools, a.registry(), a.proxyValue(), capabilities)
	if err != nil {
		a.logger.Error("构建启动命令失败", "instance", instanceID, "error", err)
		return LaunchResult{}, internalError("构建启动命令失败，请查看实例日志。")
	}
	a.emit(logf, "正在启动进程（端口 %d）…", instance.Port)
	handle, err := a.process.Launch(ctx, spec)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			a.logger.Info("实例启动已取消", "instance", instanceID)
			return LaunchResult{}, ctxErr
		}
		a.logger.Error("实例启动失败", "instance", instanceID, "error", err)
		switch {
		case errors.Is(err, process.ErrPortBusy):
			return LaunchResult{}, conflictError(apierr.CodeConflictPortBusy,
				fmt.Sprintf("端口 %d 已被占用，请修改实例端口后重试。", instance.Port))
		case errors.Is(err, process.ErrAlreadyRunning):
			return LaunchResult{}, conflictError(apierr.CodeConflictRunningInstance, "该实例已在运行。")
		case errors.Is(err, process.ErrExitedBeforeReady):
			return LaunchResult{}, apierr.New(409, apierr.CodeInstanceNotReady,
				"实例进程在服务就绪前退出；请查看实例日志。")
		case errors.Is(err, process.ErrNotReady):
			return LaunchResult{}, apierr.New(409, apierr.CodeInstanceNotReady,
				"实例进程已启动但服务在超时内未就绪；请查看实例日志后重试或停止。")
		default:
			return LaunchResult{}, apierr.New(409, apierr.CodeInstanceNotReady,
				"实例启动失败；请查看实例日志。")
		}
	}
	status := handle.Status()
	a.emit(logf, "服务已就绪（PID %d，端口 %d）", status.PID, instance.Port)
	a.logger.Info("实例已就绪", "instance", instanceID, "pid", status.PID, "port", instance.Port)
	return LaunchResult{
		InstanceID: instanceID,
		Status:     status.Message,
		WebURL:     "/workbench/" + instanceID + "/",
	}, nil
}

// StopInstance stops a running instance. Stopping an already stopped instance
// succeeds; a missing instance is reported as 404.
func (a *App) StopInstance(ctx context.Context, instanceID string) error {
	if _, ok := a.store.GetInstance(instanceID); !ok {
		return notFoundError("实例不存在。")
	}
	handle, ok := a.process.Get(instanceID)
	if !ok || handle == nil {
		return nil
	}
	if !a.tryLock() {
		return apierr.Busy()
	}
	defer a.unlock()
	if err := handle.Stop(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		a.logger.Error("停止实例失败", "instance", instanceID, "error", err)
		return internalError("停止实例失败，请稍后重试。")
	}
	a.logger.Info("实例已停止", "instance", instanceID)
	return nil
}

// LiveInstance returns the in-memory process status of an instance.
func (a *App) LiveInstance(instanceID string) (LiveStatus, bool) {
	handle, ok := a.process.Get(instanceID)
	if !ok || handle == nil {
		return LiveStatus{}, false
	}
	status := handle.Status()
	return LiveStatus{
		InstanceID:    instanceID,
		Port:          handle.Port(),
		StatusCode:    status.Code,
		StatusMessage: status.Message,
		Ready:         handle.Ready(),
		Bootstrap:     handle.BootstrapURL(),
	}, true
}

// Running lists tracked instances with live status, sorted by instance id.
func (a *App) Running() []InstanceRuntimeStatus {
	handles := a.process.Running()
	statuses := make([]InstanceRuntimeStatus, 0, len(handles))
	for _, handle := range handles {
		status := handle.Status()
		statuses = append(statuses, InstanceRuntimeStatus{
			InstanceID: handle.InstanceID(),
			Status:     status.Message,
			Ready:      handle.Ready(),
			PID:        status.PID,
		})
	}
	return statuses
}

// ResolveProxy returns the /workbench target of a ready instance. The
// bootstrap query (token) is only kept in memory.
func (a *App) ResolveProxy(_ context.Context, instanceID string) (proxy.Target, error) {
	instance, ok := a.store.GetInstance(instanceID)
	if !ok {
		return proxy.Target{}, proxy.ErrInstanceNotFound
	}
	handle, ok := a.process.Get(instanceID)
	if !ok || handle == nil {
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
