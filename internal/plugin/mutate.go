// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// Mutate installs, updates or removes one profile plugin through the installed
// Harness CLI and the embedded Node bridge (docs/M3_INTERFACES.md 5,
// docs/DSH_INTEGRATION.md 8.2).
//
// Preconditions: the instance exists and is stopped, the profile is valid, the
// bundled toolchain is available, and the runtime advertises native plugin
// management (Probe pluginManagement=true). Mutations are serialised by the Job
// layer (single global management task).
func (s *Service) Mutate(ctx context.Context, in MutateInput, logf func(string)) (MutateResult, error) {
	if logf == nil {
		logf = func(string) {}
	}
	instanceID := strings.TrimSpace(in.InstanceID)
	profile := strings.TrimSpace(in.Profile)
	if err := ctx.Err(); err != nil {
		return MutateResult{}, err
	}
	instance, err := s.instance(instanceID)
	if err != nil {
		return MutateResult{}, err
	}
	if err := ValidateMutationInput(in); err != nil {
		// The exported validator is shared with the API layer so malicious specs
		// surface as 400 VALIDATION before (and inside) a Job.
		return MutateResult{}, err
	}
	action, err := normalizeAction(in.Action)
	if err != nil {
		return MutateResult{}, err
	}
	inputSpec := strings.TrimSpace(in.Spec)
	anchoredSpec := ""
	if action == ActionRemove {
		if err := s.checkRemovableDependency(instance, profile, inputSpec); err != nil {
			return MutateResult{}, err
		}
	}
	if action == ActionAdd && isLocalPluginSpec(inputSpec) {
		// Instance-dependent filesystem checks: the target must exist inside the
		// instance workspace, also after symlink resolution (no "../" escapes).
		if err := s.validateLocalPluginTarget(instance.ID, inputSpec); err != nil {
			return MutateResult{}, err
		}
	}
	if s.instanceRunning(instance) {
		return MutateResult{}, instanceRunningError()
	}
	if s.tools == nil || strings.TrimSpace(s.tools.NodePath) == "" || strings.TrimSpace(s.tools.PnpmScript) == "" {
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal,
			"未定位到内置 Node/pnpm 工具链，无法变更插件；请检查 --pcl.tool-root。")
	}
	if s.runtime == nil {
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "运行时服务不可用，无法变更插件。")
	}
	// Profile-independent capability check: the reference inspects the launcher
	// CLI, not the instance-specific profile help.
	capabilities, err := s.runtime.Probe(ctx, instance.Version, "web")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MutateResult{}, ctxErr
		}
		s.logger.Warn("插件变更能力探测失败", "instanceId", instance.ID, "version", instance.Version, "error", err)
		return MutateResult{}, apierr.New(422, apierr.CodeUnsupportedRuntime,
			"运行时能力探测失败，无法确认原生插件管理；请查看任务日志或补充 compatibility.json。")
	}
	if !capabilities.PluginManagement {
		return MutateResult{}, apierr.New(422, apierr.CodeUnsupportedRuntime,
			"此 Harness 版本未声明原生 plugin 管理命令，无法变更插件。")
	}
	entry, err := s.runtime.EntryPath(instance.Version)
	if err != nil {
		return MutateResult{}, apierr.New(http.StatusNotFound, apierr.CodeNotFound, "运行时未安装，请先安装运行时。")
	}
	bridge, err := s.ensurePluginBridge()
	if err != nil {
		s.logger.Error("写入插件桥失败", "error", err)
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "写入插件桥失败。")
	}
	// ProfileInstallEnvironment builds the DSH whitelist env (registry/proxy,
	// bundled node/pnpm on PATH, isolated HOME) and ensures the profile dir
	// exists.
	_, environment, err := s.runtime.ProfileInstallEnvironment(s.dirs, instance.ID, profile, s.registry, s.proxy)
	if err != nil {
		s.logger.Error("构建插件变更环境失败", "instanceId", instance.ID, "error", err)
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "构建插件变更环境失败。")
	}
	workspace := s.workspaceDirectory(instance.ID)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		s.logger.Error("创建实例工作目录失败", "instanceId", instance.ID, "error", err)
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "创建实例工作目录失败。")
	}

	command := []string{
		s.tools.NodePath,
		bridge,
		s.tools.PnpmScript,
		entry,
		"plugin",
		"--profile", profile,
		action,
	}
	if inputSpec != "" {
		anchoredSpec = inputSpec
		if action == ActionAdd {
			anchoredSpec = anchorLocalSpec(inputSpec, workspace)
		}
		command = append(command, anchoredSpec)
	}
	command = append(command, "--config.ignore-scripts=true", "--config.ignore-pnpmfile=true")

	logf(fmt.Sprintf("正在通过原生 Harness plugin %s 管理插件；pnpm 使用直接参数，安装脚本默认关闭。", action))
	s.logger.Info("插件变更开始", "instanceId", instance.ID, "version", instance.Version, "profile", profile, "action", action)
	result, runErr := runCommand(ctx, commandSpec{
		Args:    command,
		Dir:     workspace,
		Env:     environment,
		Timeout: mutateTimeout,
		OnLine: func(line string) {
			// Log lines may contain credentials from a registry URL: redact them
			// before they reach the Job log / manager log.
			logf(logging.Redact(line))
		},
	})
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MutateResult{}, ctxErr
		}
		summary := summarizeCommandFailure(result.Output)
		s.logger.Error("插件变更失败", "instanceId", instance.ID, "version", instance.Version,
			"profile", profile, "action", action, "error", runErr, "summary", summary)
		logf("插件变更失败：" + summary)
		return MutateResult{}, apierr.New(http.StatusInternalServerError, apierr.CodeInternal,
			"插件操作失败，请查看任务日志。").WithDetails(map[string]any{"summary": summary})
	}
	name := mutateName(action, inputSpec)
	logf("插件操作完成；重启实例后载入新的组合包。")
	s.logger.Info("插件变更完成", "instanceId", instance.ID, "version", instance.Version,
		"profile", profile, "action", action, "name", name)
	return MutateResult{InstanceID: instance.ID, Profile: profile, Action: action, Name: name}, nil
}

// checkRemovableDependency rejects removing Harness built-in bundles or packages
// that are not independent profile dependencies.
func (s *Service) checkRemovableDependency(instance store.Instance, profile, packageName string) error {
	manifestPath := filepath.Join(s.profileDirectory(instance.ID, profile), "package.json")
	manifest, exists, err := readProfileManifest(manifestPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, ok := manifest.Dependencies[packageName]; ok {
		return nil
	}
	if _, ok := manifest.OptionalDependencies[packageName]; ok {
		return nil
	}
	return apierr.New(http.StatusBadRequest, apierr.CodeValidation,
		"该条目不是此 profile 的独立依赖，不能通过移除插件删除 Harness 内置组合包。")
}

// summarizeCommandFailure builds a redacted, bounded pnpm/CLI summary for the
// Job error details.
func summarizeCommandFailure(output string) string {
	redacted := logging.Redact(output)
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(redacted, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	summary := strings.Join(lines, "\n")
	if len(summary) > 1000 {
		summary = "…" + summary[len(summary)-1000:]
	}
	if summary == "" {
		return "命令未输出诊断信息。"
	}
	return summary
}

// mutateName is the Job-result name of a mutation (empty for in-place updates).
func mutateName(action, spec string) string {
	if action == ActionUpdate {
		return ""
	}
	return spec
}
