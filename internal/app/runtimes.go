// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
)

// InstallRuntime installs one exact DSH version (blocking). pnpm output and
// progress lines are forwarded through logf after redaction.
func (a *App) InstallRuntime(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error) {
	version = strings.TrimSpace(version)
	if !runtime.IsVersion(version) {
		return runtime.InstallResult{}, validationError("版本号必须是完整的 SemVer（例如 0.2.1-rc.3）。")
	}
	if err := ctx.Err(); err != nil {
		return runtime.InstallResult{}, err
	}
	if !a.tryLock() {
		return runtime.InstallResult{}, apierr.Busy()
	}
	defer a.unlock()

	if a.tools == nil {
		return runtime.InstallResult{}, internalError("未定位到内置 Node/pnpm 工具链，无法安装运行时；请检查 --pcl.tool-root。")
	}
	a.emit(logf, "开始安装 Harness %s（registry=%s）…", version, a.registry())
	result, err := a.runtime.Install(ctx, version, a.registry(), a.forwardLogf(logf))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return runtime.InstallResult{}, ctxErr
		}
		a.logger.Error("运行时安装失败", "version", version, "error", err)
		return runtime.InstallResult{}, a.mapRuntimeFailure("安装 Harness "+version, err)
	}
	a.emit(logf, "Harness %s 安装完成（入口 %s，复用=%v）", result.Version, result.EntryPath, result.Reused)
	a.logger.Info("运行时安装完成", "version", result.Version, "entry", result.EntryPath, "reused", result.Reused)
	return result, nil
}

// ProbeRuntime probes the capabilities of one installed runtime using the
// default "web" profile and reports its compatibility label.
func (a *App) ProbeRuntime(ctx context.Context, version string, logf func(string)) (ProbeResult, error) {
	version = strings.TrimSpace(version)
	if !runtime.IsVersion(version) {
		return ProbeResult{}, validationError("版本号必须是完整的 SemVer（例如 0.2.1-rc.3）。")
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	if !a.tryLock() {
		return ProbeResult{}, apierr.Busy()
	}
	defer a.unlock()

	if !a.runtime.Installed(version) {
		return ProbeResult{}, notFoundError("运行时未安装，请先安装该版本。")
	}
	if a.tools == nil {
		return ProbeResult{}, internalError("未定位到内置 Node/pnpm 工具链，无法探测运行时；请检查 --pcl.tool-root。")
	}
	a.emit(logf, "正在探测 Harness %s 的能力（profile=%s）…", version, defaultProfile)
	capabilities, err := a.runtime.Probe(ctx, version, defaultProfile)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ProbeResult{}, ctxErr
		}
		if apiErr, ok := apierr.As(err); ok {
			return ProbeResult{}, apiErr
		}
		a.logger.Error("运行时能力探测失败", "version", version, "error", err)
		return ProbeResult{}, apierr.New(422, apierr.CodeUnsupportedRuntime,
			"运行时能力探测失败；请查看任务日志或补充 compatibility.json。")
	}
	for _, line := range strings.Split(capabilities.Summary(), "\\n") {
		if line = strings.TrimSpace(line); line != "" {
			a.emit(logf, "%s", line)
		}
	}
	compatibility := a.runtime.CompatibilityOf(version)
	a.logger.Info("运行时能力探测完成", "version", version, "compatibility", compatibility)
	return ProbeResult{Version: version, Compatibility: compatibility}, nil
}

// Runtimes lists installed, entry-validated runtimes in descending SemVer
// order together with their compatibility label.
func (a *App) Runtimes() []RuntimeInfo {
	entries := a.runtime.Entries()
	infos := make([]RuntimeInfo, 0, len(entries))
	for _, entry := range entries {
		infos = append(infos, RuntimeInfo{
			Version:       entry.Version,
			Installed:     true,
			Compatibility: entry.Compatibility,
		})
	}
	return infos
}

// AvailableVersions returns the DSH versions published on the configured npm
// registry. Successful queries are cached for 60s (see CachedAvailableVersions).
func (a *App) AvailableVersions(ctx context.Context) ([]string, error) {
	a.availableMu.Lock()
	if len(a.available) > 0 && time.Since(a.availableAt) < availableVersionsTTL {
		cached := append([]string(nil), a.available...)
		a.availableMu.Unlock()
		return cached, nil
	}
	a.availableMu.Unlock()

	versions, err := a.runtime.AvailableVersions(ctx, a.registry())
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		a.logger.Error("查询可用运行时版本失败", "registry", a.registry(), "error", err)
		return nil, mapAvailableVersionsError(err)
	}
	versions = append([]string(nil), versions...)
	a.availableMu.Lock()
	a.available = versions
	a.availableAt = time.Now()
	a.availableMu.Unlock()
	return append([]string(nil), versions...), nil
}

// mapAvailableVersionsError maps registry discovery failures to the documented
// 502 UPSTREAM_ERROR / 504 UPSTREAM_TIMEOUT responses.
func mapAvailableVersionsError(err error) error {
	if apiErr, ok := apierr.As(err); ok {
		return apiErr
	}
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, runtime.ErrUpstreamTimeout):
		return apierr.New(504, apierr.CodeUpstreamTimeout, "查询可用版本超时，请稍后重试。")
	case errors.Is(err, runtime.ErrInvalidRegistry):
		return apierr.New(400, apierr.CodeValidation, "npm registry 地址无效，请在设置中修正。")
	default:
		return apierr.New(502, apierr.CodeUpstreamError, "查询可用版本失败：npm 上游服务不可用，请稍后重试。")
	}
}

// CachedAvailableVersions returns the most recent successful registry query, or
// an empty slice when nothing has been queried yet (state endpoint).
func (a *App) CachedAvailableVersions() []string {
	a.availableMu.Lock()
	defer a.availableMu.Unlock()
	return append([]string(nil), a.available...)
}

// DeleteRuntime removes an installed runtime. A runtime still referenced by an
// instance is protected with CONFLICT_IN_USE; the runtime directory is moved to
// trash by runtime.Remove.
func (a *App) DeleteRuntime(ctx context.Context, version string) (string, error) {
	version = strings.TrimSpace(version)
	if !runtime.IsVersion(version) {
		return "", notFoundError("运行时不存在。")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !a.tryLock() {
		return "", apierr.Busy()
	}
	defer a.unlock()

	for _, instance := range a.store.ListInstances() {
		if instance.Version == version {
			return "", conflictError(apierr.CodeConflictInUse,
				"运行时 "+version+" 仍被实例「"+instance.Name+"」使用，请先删除相关实例。")
		}
	}
	trashedPath, err := a.runtime.Remove(version)
	if err != nil {
		if apiErr, ok := apierr.As(err); ok {
			return "", apiErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		a.logger.Error("删除运行时失败", "version", version, "error", err)
		return "", internalError("删除运行时失败，请稍后重试。")
	}
	a.logger.Info("运行时已删除", "version", version, "trash", trashedPath)
	return trashedPath, nil
}
