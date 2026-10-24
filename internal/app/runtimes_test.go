// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
)

func TestInstallRuntime(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	ctx := context.Background()

	_, err := application.InstallRuntime(ctx, "1.2", nil)
	requireAPIError(t, err, 400, apierr.CodeValidation)

	originalTools := application.tools
	application.tools = nil
	_, err = application.InstallRuntime(ctx, "0.2.1-rc.3", nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	application.tools = originalTools

	var lines []string
	result, err := application.InstallRuntime(ctx, " 0.2.1-rc.3 ", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("InstallRuntime 失败: %v", err)
	}
	if result.Version != "0.2.1-rc.3" || result.EntryPath != "/fake/entry.js" {
		t.Fatalf("InstallResult 不符: %+v", result)
	}
	if fakeRT.installCalls != 1 || len(fakeRT.installArgs) != 1 {
		t.Fatalf("Install 调用次数 = %d，参数 = %v", fakeRT.installCalls, fakeRT.installArgs)
	}
	if want := "0.2.1-rc.3@" + runtime.DefaultRegistry; fakeRT.installArgs[0] != want {
		t.Fatalf("Install 参数 = %q，期望 %q", fakeRT.installArgs[0], want)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"开始安装", "安装完成", "fake pnpm output"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("安装日志缺少 %q：%q", want, joined)
		}
	}
}

func TestInstallRuntimeErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"上游超时", runtime.ErrUpstreamTimeout, 504, apierr.CodeUpstreamTimeout},
		{"上游错误", runtime.ErrUpstream, 502, apierr.CodeUpstreamError},
		{"registry 无效", runtime.ErrInvalidRegistry, 400, apierr.CodeValidation},
		{"安装失败", errors.New("pnpm failed"), 500, apierr.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			application, fakeRT, _, _ := newTestApp(t)
			fakeRT.installErr = tc.err
			_, err := application.InstallRuntime(context.Background(), "0.2.1-rc.3", nil)
			requireAPIError(t, err, tc.status, tc.code)
		})
	}
}

func TestInstallRuntimeCanceledAndBusy(t *testing.T) {
	application, _, _, _ := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := application.InstallRuntime(ctx, "0.2.1-rc.3", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
	application.mu.Lock()
	defer application.mu.Unlock()
	_, err := application.InstallRuntime(context.Background(), "0.2.1-rc.3", nil)
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)
}

func TestProbeRuntime(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	ctx := context.Background()

	_, err := application.ProbeRuntime(ctx, "bad", nil)
	requireAPIError(t, err, 400, apierr.CodeValidation)

	_, err = application.ProbeRuntime(ctx, "0.2.1-rc.3", nil)
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	fakeRT.installed["0.2.1-rc.3"] = true
	var lines []string
	result, err := application.ProbeRuntime(ctx, "0.2.1-rc.3", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("ProbeRuntime 失败: %v", err)
	}
	if result.Version != "0.2.1-rc.3" || result.Compatibility != "verified" {
		t.Fatalf("ProbeResult 不符: %+v", result)
	}
	if fakeRT.probeCalls != 1 || fakeRT.lastProbeProf != "web" {
		t.Fatalf("Probe 调用 = %d，profile = %q", fakeRT.probeCalls, fakeRT.lastProbeProf)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "正在探测") || !strings.Contains(joined, "Harness") {
		t.Fatalf("探测日志不符：%q", joined)
	}

	fakeRT.probeErr = errors.New("probe exploded")
	_, err = application.ProbeRuntime(ctx, "0.2.1-rc.3", nil)
	requireAPIError(t, err, 422, apierr.CodeUnsupportedRuntime)

	originalTools := application.tools
	application.tools = nil
	_, err = application.ProbeRuntime(ctx, "0.2.1-rc.3", nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	application.tools = originalTools

	application.mu.Lock()
	_, err = application.ProbeRuntime(ctx, "0.2.1-rc.3", nil)
	application.mu.Unlock()
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)
}

func TestRuntimes(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	if got := application.Runtimes(); len(got) != 0 {
		t.Fatalf("空运行时列表 = %+v", got)
	}
	fakeRT.entries = []runtime.Entry{
		{Version: "0.2.1-rc.3", Compatibility: "verified", EntryPath: "/fake/entry.js"},
		{Version: "0.2.0", Compatibility: "unverified", EntryPath: "/fake/old.js"},
	}
	infos := application.Runtimes()
	if len(infos) != 2 {
		t.Fatalf("Runtimes 长度 = %d", len(infos))
	}
	if infos[0].Version != "0.2.1-rc.3" || !infos[0].Installed || infos[0].Compatibility != "verified" {
		t.Fatalf("RuntimeInfo[0] 不符: %+v", infos[0])
	}
	if infos[1].Version != "0.2.0" || infos[1].Compatibility != "unverified" {
		t.Fatalf("RuntimeInfo[1] 不符: %+v", infos[1])
	}
}

func TestAvailableVersionsCache(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	ctx := context.Background()

	versions, err := application.AvailableVersions(ctx)
	if err != nil {
		t.Fatalf("AvailableVersions 失败: %v", err)
	}
	if !reflect.DeepEqual(versions, fakeRT.available) || fakeRT.availableCalls != 1 {
		t.Fatalf("返回 %v，调用次数 %d", versions, fakeRT.availableCalls)
	}
	if _, err := application.AvailableVersions(ctx); err != nil {
		t.Fatalf("第二次 AvailableVersions 失败: %v", err)
	}
	if fakeRT.availableCalls != 1 {
		t.Fatalf("60s 内应命中缓存，实际调用 %d 次", fakeRT.availableCalls)
	}
	if cached := application.CachedAvailableVersions(); !reflect.DeepEqual(cached, fakeRT.available) {
		t.Fatalf("CachedAvailableVersions = %v，期望 %v", cached, fakeRT.available)
	}

	application.availableAt = time.Now().Add(-2 * availableVersionsTTL)
	if _, err := application.AvailableVersions(ctx); err != nil {
		t.Fatalf("过期后查询失败: %v", err)
	}
	if fakeRT.availableCalls != 2 {
		t.Fatalf("缓存过期后应重新查询，实际调用 %d 次", fakeRT.availableCalls)
	}
}

func TestAvailableVersionsErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"上游超时", runtime.ErrUpstreamTimeout, 504, apierr.CodeUpstreamTimeout},
		{"上游错误", runtime.ErrUpstream, 502, apierr.CodeUpstreamError},
		{"未知错误", errors.New("boom"), 502, apierr.CodeUpstreamError},
		{"registry 无效", runtime.ErrInvalidRegistry, 400, apierr.CodeValidation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			application, fakeRT, _, _ := newTestApp(t)
			fakeRT.availableErr = tc.err
			_, err := application.AvailableVersions(context.Background())
			requireAPIError(t, err, tc.status, tc.code)
		})
	}
}

func TestDeleteRuntime(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	ctx := context.Background()

	_, err := application.DeleteRuntime(ctx, "bad")
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	instance := mustCreateInstance(t, application, "引用实例", freeLoopbackPort(t))
	_, err = application.DeleteRuntime(ctx, instance.Version)
	requireAPIError(t, err, 409, apierr.CodeConflictInUse)
	if fakeRT.removeCalls != 0 {
		t.Fatalf("被引用时不应调用 Remove，实际 %d", fakeRT.removeCalls)
	}

	fakeRT.removeTrash = "/tmp/trash/runtime-0.2.0-1"
	trash, err := application.DeleteRuntime(ctx, "0.2.0")
	if err != nil {
		t.Fatalf("DeleteRuntime 失败: %v", err)
	}
	if trash != fakeRT.removeTrash || len(fakeRT.removed) != 1 || fakeRT.removed[0] != "0.2.0" {
		t.Fatalf("删除结果不符: trash=%q removed=%v", trash, fakeRT.removed)
	}

	fakeRT.removeErr = apierr.NotFound("运行时 0.2.0 尚未安装。")
	_, err = application.DeleteRuntime(ctx, "0.2.0")
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	fakeRT.removeErr = errors.New("move failed")
	_, err = application.DeleteRuntime(ctx, "0.2.0")
	requireAPIError(t, err, 500, apierr.CodeInternal)

	application.mu.Lock()
	_, err = application.DeleteRuntime(ctx, "0.2.0")
	application.mu.Unlock()
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := application.DeleteRuntime(canceled, "0.2.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
}
