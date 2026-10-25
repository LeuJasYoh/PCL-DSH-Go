// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// fakeProfileInstaller records pnpm invocations and returns scripted results.
type fakeProfileInstaller struct {
	calls    []installCommand
	results  []fakeInstallResult
	logLines []string // emitted through command.Logf on every Run
}

type fakeInstallResult struct {
	output string
	err    error
}

func (f *fakeProfileInstaller) Run(_ context.Context, command installCommand) (string, error) {
	f.calls = append(f.calls, command)
	for _, line := range f.logLines {
		if command.Logf != nil {
			command.Logf(line)
		}
	}
	index := len(f.calls) - 1
	if index < len(f.results) {
		return f.results[index].output, f.results[index].err
	}
	return "", nil
}

func newRestoreApp(t *testing.T) (*App, *fakeRuntime, *fakeProfileInstaller, store.Instance, config.Dirs) {
	t.Helper()
	application, fakeRT, _, dirs := newTestApp(t)
	installer := &fakeProfileInstaller{}
	application.installer = installer
	instance := mustCreateInstance(t, application, "恢复实例", 0)
	fakeRT.installed[instance.Version] = true
	fakeRT.profileEnv = []string{"PATH=/fake/node/bin", "DSH_HOME=/fake/home"}
	fakeRT.entryPaths = map[string]string{instance.Version: "/fake/runtime/entry.js"}
	return application, fakeRT, installer, instance, dirs
}

func testProfileDir(dirs config.Dirs, instanceID, profile string) string {
	return filepath.Join(dirs.InstancesDir, instanceID, "dsh-home", "profiles", profile)
}

func TestRuntimeInstalledAndEntryPath(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)

	if application.RuntimeInstalled("0.2.1-rc.3") {
		t.Fatal("未安装版本 RuntimeInstalled 应为 false")
	}
	fakeRT.installed["0.2.1-rc.3"] = true
	if !application.RuntimeInstalled(" 0.2.1-rc.3 ") {
		t.Fatal("已安装版本 RuntimeInstalled 应为 true（去首尾空白）")
	}

	fakeRT.entryPaths = map[string]string{"0.2.1-rc.3": "/fake/entry.js"}
	entry, err := application.EntryPath("0.2.1-rc.3")
	if err != nil || entry != "/fake/entry.js" {
		t.Fatalf("EntryPath = %q, %v", entry, err)
	}
	_, err = application.EntryPath("0.2")
	requireAPIError(t, err, 400, apierr.CodeValidation)
	_, err = application.EntryPath("0.2.0")
	requireAPIError(t, err, 404, apierr.CodeNotFound)
	fakeRT.entryPathErr = apierr.NotFound("运行时不存在。")
	_, err = application.EntryPath("0.2.1-rc.3")
	requireAPIError(t, err, 404, apierr.CodeNotFound)
}

func TestRestoreProfileDependenciesWithoutLock(t *testing.T) {
	application, fakeRT, installer, instance, dirs := newRestoreApp(t)
	var lines []string
	err := application.RestoreProfileDependencies(context.Background(), instance.ID, "web", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RestoreProfileDependencies 失败: %v", err)
	}
	if len(installer.calls) != 1 {
		t.Fatalf("pnpm 调用次数 = %d，期望 1", len(installer.calls))
	}
	call := installer.calls[0]
	if want := testProfileDir(dirs, instance.ID, "web"); call.Dir != want {
		t.Fatalf("工作目录 = %q，期望 %q", call.Dir, want)
	}
	if !reflect.DeepEqual(call.Env, fakeRT.profileEnv) {
		t.Fatalf("env = %v，期望 %v", call.Env, fakeRT.profileEnv)
	}
	wantArgs := []string{
		"install",
		"--ignore-scripts",
		"--config.ignore-pnpmfile=true",
		"--registry=https://registry.npmjs.org",
		"--store-dir=" + filepath.Join(dirs.CacheDir, "pnpm-store"),
	}
	if !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("args = %v，期望 %v", call.Args, wantArgs)
	}
	if fakeRT.lastProfileEnv.InstanceID != instance.ID || fakeRT.lastProfileEnv.Profile != "web" ||
		fakeRT.lastProfileEnv.Registry != "https://registry.npmjs.org" || fakeRT.lastProfileEnv.Proxy != "" {
		t.Fatalf("ProfileInstallEnvironment 参数不符: %+v", fakeRT.lastProfileEnv)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "正在恢复 profile web 的依赖") || !strings.Contains(joined, "依赖恢复完成") {
		t.Fatalf("日志不符: %q", joined)
	}
}

func TestRestoreProfileDependenciesFrozenLockRetry(t *testing.T) {
	application, _, installer, instance, dirs := newRestoreApp(t)
	dir := testProfileDir(dirs, instance.ID, "web")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("创建 profile 目录失败: %v", err)
	}
	lockPath := filepath.Join(dir, pnpmLockFileName)
	originalLock := "lockfileVersion: '9.0'\n"
	if err := os.WriteFile(lockPath, []byte(originalLock), 0o600); err != nil {
		t.Fatalf("写入 pnpm-lock.yaml 失败: %v", err)
	}
	installer.results = []fakeInstallResult{
		{output: "ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with frozen-lockfile", err: errors.New("exit status 1")},
		{output: "", err: nil},
	}
	var lines []string
	err := application.RestoreProfileDependencies(context.Background(), instance.ID, "web", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RestoreProfileDependencies 失败: %v", err)
	}
	if len(installer.calls) != 2 {
		t.Fatalf("pnpm 调用次数 = %d，期望 2", len(installer.calls))
	}
	if !containsArg(installer.calls[0].Args, "--frozen-lockfile") {
		t.Fatalf("首次调用应带 --frozen-lockfile: %v", installer.calls[0].Args)
	}
	if containsArg(installer.calls[1].Args, "--frozen-lockfile") {
		t.Fatalf("重试调用不应带 --frozen-lockfile: %v", installer.calls[1].Args)
	}
	backupPath := filepath.Join(dir, pnpmOriginalLockFileName)
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("备份锁文件不存在: %v", err)
	}
	if string(backup) != originalLock {
		t.Fatalf("备份内容 = %q，期望 %q", backup, originalLock)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "备份为") || !strings.Contains(joined, "重试") {
		t.Fatalf("重试日志不符: %q", joined)
	}
}

func TestRestoreProfileDependenciesFrozenLockOtherErrorFails(t *testing.T) {
	application, _, installer, instance, dirs := newRestoreApp(t)
	dir := testProfileDir(dirs, instance.ID, "web")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("创建 profile 目录失败: %v", err)
	}
	lockPath := filepath.Join(dir, pnpmLockFileName)
	if err := os.WriteFile(lockPath, []byte("lockfileVersion: 9\n"), 0o600); err != nil {
		t.Fatalf("写入 pnpm-lock.yaml 失败: %v", err)
	}
	installer.results = []fakeInstallResult{
		{output: "ERR_PNPM_SOMETHING_ELSE network failure", err: errors.New("exit status 1")},
	}
	err := application.RestoreProfileDependencies(context.Background(), instance.ID, "web", nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	if len(installer.calls) != 1 {
		t.Fatalf("非过期锁错误不应重试，实际调用 %d 次", len(installer.calls))
	}
	if _, statErr := os.Stat(filepath.Join(dir, pnpmOriginalLockFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("非过期锁错误不应生成备份: %v", statErr)
	}
}

func TestRestoreProfileDependenciesCanceled(t *testing.T) {
	application, _, _, instance, _ := newRestoreApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := application.RestoreProfileDependencies(ctx, instance.ID, "web", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}

	// 运行中被取消：安装器返回错误前 ctx 已取消 → 透传 ctx 错误。
	application2, _, _, instance2, _ := newRestoreApp(t)
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	application2.installer = cancelingInstaller{cancel: cancelFunc}
	err = application2.RestoreProfileDependencies(cancelCtx, instance2.ID, "web", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("运行中取消应返回 context.Canceled，实际 %v", err)
	}
}

type cancelingInstaller struct {
	cancel context.CancelFunc
}

func (c cancelingInstaller) Run(context.Context, installCommand) (string, error) {
	c.cancel()
	return "", errors.New("killed")
}

func TestRestoreProfileDependenciesErrors(t *testing.T) {
	application, fakeRT, installer, instance, _ := newRestoreApp(t)
	ctx := context.Background()

	err := application.RestoreProfileDependencies(ctx, "i-000000000000", "web", nil)
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	err = application.RestoreProfileDependencies(ctx, instance.ID, "bad/profile", nil)
	requireAPIError(t, err, 400, apierr.CodeValidation)

	fakeRT.profileEnvErr = errors.New("env boom")
	err = application.RestoreProfileDependencies(ctx, instance.ID, "web", nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	fakeRT.profileEnvErr = apierr.Validation("profile 非法")
	err = application.RestoreProfileDependencies(ctx, instance.ID, "web", nil)
	requireAPIError(t, err, 400, apierr.CodeValidation)
	fakeRT.profileEnvErr = nil

	originalTools := application.tools
	application.tools = nil
	err = application.RestoreProfileDependencies(ctx, instance.ID, "web", nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	application.tools = originalTools

	application.mu.Lock()
	err = application.RestoreProfileDependencies(ctx, instance.ID, "web", nil)
	application.mu.Unlock()
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)

	if len(installer.calls) != 0 {
		t.Fatalf("错误路径不应调用 pnpm，实际 %d 次", len(installer.calls))
	}
}

func TestRestoreProfileDependenciesRedactsLogf(t *testing.T) {
	application, _, installer, instance, _ := newRestoreApp(t)
	installer.logLines = []string{
		"registry=https://registry.npmjs.org ok",
		"token=super-secret-value should not leak",
	}
	var lines []string
	err := application.RestoreProfileDependencies(context.Background(), instance.ID, "web", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RestoreProfileDependencies 失败: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "super-secret-value") {
		t.Fatalf("日志泄漏 token: %q", joined)
	}
	if !strings.Contains(joined, "token=***") {
		t.Fatalf("日志应包含脱敏结果: %q", joined)
	}
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}
func TestRuntimeServiceExposed(t *testing.T) {
	application, _, _, _, _ := newRestoreApp(t)
	if application.RuntimeService() != nil {
		t.Fatal("fake backend App 的 RuntimeService 应为 nil")
	}

	dirs := newTestDirs(t)
	instanceStore, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer instanceStore.Close()
	production, err := New(Deps{Logger: discardLogger(), Store: instanceStore, Dirs: dirs})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	service := production.RuntimeService()
	if service == nil {
		t.Fatal("生产 App 的 RuntimeService 不应为 nil")
	}
	if again := production.RuntimeService(); again != service {
		t.Fatal("RuntimeService 应返回同一实例")
	}
	// The exposed service is the same instance driving runtime helpers.
	if service.Installed("0.2.1-rc.3") {
		t.Fatal("空 data-root 下不应有已安装运行时")
	}
	if compatibility := service.CompatibilityOf("0.2.1-rc.3"); compatibility != "unverified" {
		t.Fatalf("CompatibilityOf = %q，期望 unverified", compatibility)
	}
}
