// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// newTestAppWithLimit is newTestApp with an explicit Deps.MaxRunningInstances.
func newTestAppWithLimit(t *testing.T, limit *int) (*App, *fakeRuntime, *fakeProcess, config.Dirs) {
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
		Config:              config.Config{DataRoot: dirs.DataRoot},
		Dirs:                dirs,
		Logger:              discardLogger(),
		Store:               st,
		Tools:               &tools.Tools{NodePath: "/fake/node", PnpmScript: "/fake/pnpm.cjs"},
		MaxRunningInstances: limit,
	}, fakeRT, fakeProc)
	return application, fakeRT, fakeProc, dirs
}

func intPtr(value int) *int { return &value }

func TestMaxRunningInstancesResolution(t *testing.T) {
	application, _, _, _ := newTestApp(t)
	if got := application.MaxRunningInstances(); got != 0 {
		t.Fatalf("默认 MaxRunningInstances = %d，期望 0（不限制）", got)
	}

	two := 2
	custom, _, _, _ := newTestAppWithLimit(t, intPtr(two))
	if got := custom.MaxRunningInstances(); got != 2 {
		t.Fatalf("显式 MaxRunningInstances = %d，期望 2", got)
	}

	unlimited, _, _, _ := newTestAppWithLimit(t, intPtr(0))
	if got := unlimited.MaxRunningInstances(); got != 0 {
		t.Fatalf("0 表示不限制，实际 %d", got)
	}

	negative, _, _, _ := newTestAppWithLimit(t, intPtr(-3))
	if got := negative.MaxRunningInstances(); got != 0 {
		t.Fatalf("负数应回退默认 0（不限制），实际 %d", got)
	}
}

func TestLaunchInstanceAllowsSecondWithLimitTwo(t *testing.T) {
	two := 2
	application, fakeRT, fakeProc, _ := newTestAppWithLimit(t, intPtr(two))
	first := mustCreateInstance(t, application, "实例一", freeLoopbackPort(t))
	second := mustCreateInstance(t, application, "实例二", freeLoopbackPortExcept(t, first.Port))
	fakeRT.installed[first.Version] = true
	fakeRT.installed[second.Version] = true
	fakeProc.add(newRunningFakeHandle(first.ID, first.Port))

	result, err := application.LaunchInstance(context.Background(), second.ID, nil)
	if err != nil {
		t.Fatalf("limit=2 时启动第二个实例应成功: %v", err)
	}
	if result.InstanceID != second.ID {
		t.Fatalf("LaunchResult.InstanceID = %q，期望 %q", result.InstanceID, second.ID)
	}
}

func TestLaunchInstanceLimitZeroMeansUnlimited(t *testing.T) {
	application, fakeRT, fakeProc, _ := newTestAppWithLimit(t, intPtr(0))
	first := mustCreateInstance(t, application, "实例一", freeLoopbackPort(t))
	second := mustCreateInstance(t, application, "实例二", freeLoopbackPortExcept(t, first.Port))
	third := mustCreateInstance(t, application, "实例三", freeLoopbackPortExcept(t, first.Port, second.Port))
	for _, instance := range []store.Instance{first, second, third} {
		fakeRT.installed[instance.Version] = true
	}
	fakeProc.add(newRunningFakeHandle(first.ID, first.Port))
	fakeProc.add(newRunningFakeHandle(second.ID, second.Port))

	result, err := application.LaunchInstance(context.Background(), third.ID, nil)
	if err != nil {
		t.Fatalf("limit=0 时不应限制运行实例数: %v", err)
	}
	if result.InstanceID != third.ID {
		t.Fatalf("LaunchResult.InstanceID = %q，期望 %q", result.InstanceID, third.ID)
	}
}

func TestLaunchInstanceDefaultUnlimitedAllowsSecond(t *testing.T) {
	application, fakeRT, fakeProc, _ := newTestApp(t)
	first := mustCreateInstance(t, application, "实例一", freeLoopbackPort(t))
	second := mustCreateInstance(t, application, "实例二", freeLoopbackPortExcept(t, first.Port))
	fakeRT.installed[second.Version] = true
	fakeProc.add(newRunningFakeHandle(first.ID, first.Port))

	if _, err := application.LaunchInstance(context.Background(), second.ID, nil); err != nil {
		t.Fatalf("默认 0（不限制）时第二个实例应可启动: %v", err)
	}
}

func TestLaunchInstanceExplicitLimitMessage(t *testing.T) {
	application, fakeRT, fakeProc, _ := newTestAppWithLimit(t, intPtr(1))
	first := mustCreateInstance(t, application, "实例一", freeLoopbackPort(t))
	second := mustCreateInstance(t, application, "实例二", freeLoopbackPortExcept(t, first.Port))
	fakeRT.installed[second.Version] = true
	fakeProc.add(newRunningFakeHandle(first.ID, first.Port))

	_, err := application.LaunchInstance(context.Background(), second.ID, nil)
	apiErr := requireAPIError(t, err, 409, "CONFLICT_RUNNING_INSTANCE")
	if !strings.Contains(apiErr.Message, "当前最多允许同时运行 1 个 DSH 实例") {
		t.Fatalf("错误信息未包含当前上限: %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "--pcl.max-running-instances") {
		t.Fatalf("错误信息未提示配置项: %q", apiErr.Message)
	}
}
