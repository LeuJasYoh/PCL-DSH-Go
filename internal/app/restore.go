// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	profilecheck "github.com/LeuJasYoh/PCL-DSH-Go/internal/profile"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const (
	// pnpmLockFileName is the lock file pnpm reads/writes in a profile directory.
	pnpmLockFileName = "pnpm-lock.yaml"
	// pnpmOriginalLockFileName keeps a copy of the shipped lock when an import
	// has to drop --frozen-lockfile (docs/DSH_INTEGRATION.md §7.4).
	pnpmOriginalLockFileName = ".hdsl-original-pnpm-lock.yaml"
	// pnpmOutdatedLockCode is the pnpm error code of an outdated lock file.
	pnpmOutdatedLockCode = "ERR_PNPM_OUTDATED_LOCKFILE"
	// profileInstallDrainTimeout bounds draining pnpm output after it exits.
	profileInstallDrainTimeout = 10 * time.Second
)

// validateProfileName mirrors the shared profile rules used by store/runtime.
func validateProfileName(profile string) error {
	if err := profilecheck.Validate(profile); err != nil {
		return validationError(err.Error())
	}
	return nil
}

// RuntimeInstalled reports whether a runtime version is installed with a
// validated CLI entry (docs/M3_INTERFACES.md §6).
func (a *App) RuntimeInstalled(version string) bool {
	return a.runtime.Installed(strings.TrimSpace(version))
}

// EntryPath returns the validated absolute CLI entry path of an installed
// runtime (transparent runtime.Service wrapper).
func (a *App) EntryPath(version string) (string, error) {
	version = strings.TrimSpace(version)
	if !runtime.IsVersion(version) {
		return "", validationError("版本号必须是完整的 SemVer（例如 0.2.1-rc.3）。")
	}
	entry, err := a.runtime.EntryPath(version)
	if err != nil {
		if apiErr, ok := apierr.As(err); ok {
			return "", apiErr
		}
		a.logger.Error("读取运行时入口失败", "version", version, "error", err)
		return "", notFoundError(fmt.Sprintf("运行时 %s 未安装或入口无效。", version))
	}
	return entry, nil
}

// installCommand is one pnpm invocation for RestoreProfileDependencies.
type installCommand struct {
	Dir  string
	Env  []string
	Args []string
	Logf func(string)
}

// profileInstaller runs one pnpm install invocation; tests inject a fake so no
// real pnpm is required.
type profileInstaller interface {
	Run(ctx context.Context, command installCommand) (output string, err error)
}

// execProfileInstaller runs node <pnpm.cjs> ... in its own process group.
type execProfileInstaller struct {
	nodePath   string
	pnpmScript string
}

func newExecProfileInstaller(tl *tools.Tools) profileInstaller {
	if tl == nil {
		return execProfileInstaller{}
	}
	return execProfileInstaller{nodePath: tl.NodePath, pnpmScript: tl.PnpmScript}
}

// Run starts pnpm, streams its combined stdout/stderr line by line and returns
// the captured output (used for ERR_PNPM_OUTDATED_LOCKFILE detection).
func (e execProfileInstaller) Run(ctx context.Context, command installCommand) (string, error) {
	argv := make([]string, 0, len(command.Args)+1)
	argv = append(argv, e.pnpmScript)
	argv = append(argv, command.Args...)
	cmd := exec.CommandContext(ctx, e.nodePath, argv...)
	cmd.Dir = command.Dir
	cmd.Env = append([]string(nil), command.Env...)
	prepareInstallCommand(cmd)
	cmd.Cancel = func() error { return terminateInstallCommand(cmd) }
	cmd.WaitDelay = profileInstallDrainTimeout

	reader, writer, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("创建 pnpm 输出管道失败: %w", err)
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return "", fmt.Errorf("启动 pnpm 失败: %w", err)
	}
	_ = writer.Close()

	var output strings.Builder
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteString("\n")
			if command.Logf != nil {
				command.Logf(line)
			}
		}
	}()

	waitErr := cmd.Wait()
	select {
	case <-scanned:
	case <-time.After(profileInstallDrainTimeout):
		_ = reader.Close()
		<-scanned
	}
	return output.String(), waitErr
}

// RestoreProfileDependencies runs pnpm install for one profile directory
// (docs/M3_INTERFACES.md §6):
//
//	node <pnpm.cjs> install --ignore-scripts --config.ignore-pnpmfile=true //	  --registry=<settings.registry> --store-dir=<cache>/pnpm-store [--frozen-lockfile]
//
// When a pnpm-lock.yaml exists the first attempt is frozen; only
// ERR_PNPM_OUTDATED_LOCKFILE triggers a retry after backing the lock up to
// .hdsl-original-pnpm-lock.yaml. Any other failure fails the operation.
func (a *App) RestoreProfileDependencies(ctx context.Context, instanceID, profile string, logf func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := a.store.GetInstance(instanceID); !ok {
		return notFoundError("实例不存在。")
	}
	if err := validateProfileName(profile); err != nil {
		return err
	}
	if a.tools == nil {
		return internalError("未定位到内置 Node/pnpm 工具链，无法恢复 profile 依赖；请检查 --pcl.tool-root。")
	}
	if !a.tryLock() {
		return apierr.Busy()
	}
	defer a.unlock()

	dir, env, err := a.runtime.ProfileInstallEnvironment(a.dirs, instanceID, profile, a.registry(), a.proxyValue())
	if err != nil {
		if apiErr, ok := apierr.As(err); ok {
			return apiErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		a.logger.Error("构造 profile 安装环境失败", "instance", instanceID, "profile", profile, "error", err)
		return internalError("恢复 profile 依赖失败：无法构造 pnpm 安装环境。")
	}

	lockPath := filepath.Join(dir, pnpmLockFileName)
	hasLock := fileExists(lockPath)
	a.emit(logf, "正在恢复 profile %s 的依赖（frozen-lockfile=%v）…", profile, hasLock)
	output, runErr := a.installer.Run(ctx, installCommand{
		Dir:  dir,
		Env:  env,
		Args: a.profileInstallArgs(hasLock),
		Logf: a.forwardLogf(logf),
	})
	if runErr == nil {
		a.logger.Info("profile 依赖恢复完成", "instance", instanceID, "profile", profile)
		a.emit(logf, "profile %s 的依赖恢复完成。", profile)
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	if hasLock && strings.Contains(output, pnpmOutdatedLockCode) {
		a.emit(logf, "pnpm-lock.yaml 已过期，备份为 %s 并去掉 --frozen-lockfile 重试…", pnpmOriginalLockFileName)
		if backupErr := backupFile(lockPath, filepath.Join(dir, pnpmOriginalLockFileName)); backupErr != nil {
			a.logger.Error("备份过期的 pnpm-lock.yaml 失败",
				"instance", instanceID, "profile", profile, "error", backupErr)
			return internalError("恢复 profile 依赖失败：无法备份过期的 pnpm-lock.yaml。")
		}
		output, runErr = a.installer.Run(ctx, installCommand{
			Dir:  dir,
			Env:  env,
			Args: a.profileInstallArgs(false),
			Logf: a.forwardLogf(logf),
		})
		if runErr == nil {
			a.logger.Info("profile 依赖恢复完成（已刷新 lock）", "instance", instanceID, "profile", profile)
			a.emit(logf, "profile %s 的依赖恢复完成（pnpm-lock.yaml 已刷新）。", profile)
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}

	a.logger.Error("恢复 profile 依赖失败",
		"instance", instanceID, "profile", profile, "error", runErr, "outputTail", tailText(output, 4096))
	return apierr.New(500, apierr.CodeInternal,
		fmt.Sprintf("恢复 profile %s 依赖失败，请查看任务日志。", profile))
}

// profileInstallArgs builds the pnpm install arguments from settings
// (docs/M3_INTERFACES.md §6 / docs/DSH_INTEGRATION.md §7.4).
func (a *App) profileInstallArgs(frozen bool) []string {
	args := []string{
		"install",
		"--ignore-scripts",
		"--config.ignore-pnpmfile=true",
		"--registry=" + a.registry(),
		"--store-dir=" + filepath.Join(a.dirs.CacheDir, "pnpm-store"),
	}
	if frozen {
		args = append(args, "--frozen-lockfile")
	}
	return args
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func backupFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// tailText returns at most limit trailing bytes of text for diagnostics.
func tailText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return "…" + text[len(text)-limit:]
}
