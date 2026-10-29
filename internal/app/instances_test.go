// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

func freeLoopbackPortExcept(t *testing.T, exclude ...int) int {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		port := freeLoopbackPort(t)
		conflict := false
		for _, taken := range exclude {
			if port == taken {
				conflict = true
				break
			}
		}
		if !conflict {
			return port
		}
	}
	t.Fatal("无法分配与其他实例不同的回环端口")
	return 0
}

func TestCreateInstanceAutoPort(t *testing.T) {
	application, _, _, _ := newTestApp(t)
	first := mustCreateInstance(t, application, "自动端口一", 0)
	if first.Port < autoPortMin || first.Port > autoPortMax {
		t.Fatalf("自动分配端口 %d 超出 %d-%d", first.Port, autoPortMin, autoPortMax)
	}
	if bindable, err := portBindable(context.Background(), first.Port); err != nil || !bindable {
		t.Fatalf("自动分配的端口 %d 不可绑定: %v", first.Port, err)
	}
	second := mustCreateInstance(t, application, "自动端口二", 0)
	if second.Port == first.Port {
		t.Fatalf("两次自动分配得到相同端口 %d", first.Port)
	}
	if list := application.store.ListInstances(); len(list) != 2 {
		t.Fatalf("实例数 = %d，期望 2", len(list))
	}
}

func TestCreateInstanceExplicitPortBusy(t *testing.T) {
	application, _, _, _ := newTestApp(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer listener.Close()
	busyPort := listener.Addr().(*net.TCPAddr).Port
	_, err = application.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: "占用端口", Version: "0.2.1-rc.3", Profile: "web", Port: busyPort,
	})
	requireAPIError(t, err, 409, apierr.CodeConflictPortBusy)

	freePort := freeLoopbackPort(t)
	mustCreateInstance(t, application, "已有实例", freePort)
	_, err = application.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: "重复端口", Version: "0.2.1-rc.3", Profile: "web", Port: freePort,
	})
	requireAPIError(t, err, 409, apierr.CodeConflictPortBusy)
	if list := application.store.ListInstances(); len(list) != 1 {
		t.Fatalf("实例数 = %d，期望 1", len(list))
	}
}

func TestCreateInstanceValidation(t *testing.T) {
	application, _, _, _ := newTestApp(t)
	cases := []struct {
		name string
		in   store.CreateInstanceInput
	}{
		{"名称为空", store.CreateInstanceInput{Name: "", Version: "0.2.1-rc.3", Profile: "web", Port: freeLoopbackPort(t)}},
		{"名称含路径分隔符", store.CreateInstanceInput{Name: "a/b", Version: "0.2.1-rc.3", Profile: "web", Port: freeLoopbackPort(t)}},
		{"版本非精确 SemVer", store.CreateInstanceInput{Name: "x", Version: "0.2", Profile: "web", Port: freeLoopbackPort(t)}},
		{"profile 为空", store.CreateInstanceInput{Name: "x", Version: "0.2.1-rc.3", Profile: "", Port: freeLoopbackPort(t)}},
		{"profile 非法", store.CreateInstanceInput{Name: "x", Version: "0.2.1-rc.3", Profile: "bad/profile", Port: freeLoopbackPort(t)}},
		{"端口过大", store.CreateInstanceInput{Name: "x", Version: "0.2.1-rc.3", Profile: "web", Port: 70000}},
		{"端口为负", store.CreateInstanceInput{Name: "x", Version: "0.2.1-rc.3", Profile: "web", Port: -5}},
		{"端口低于 1024", store.CreateInstanceInput{Name: "x", Version: "0.2.1-rc.3", Profile: "web", Port: 1023}},
	}
	for _, tc := range cases {
		_, err := application.CreateInstance(context.Background(), tc.in)
		requireAPIError(t, err, 400, apierr.CodeValidation)
	}
	if list := application.store.ListInstances(); len(list) != 0 {
		t.Fatalf("非法输入不应创建实例，实际 %d", len(list))
	}
}

func TestUpdateInstance(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	firstPort := freeLoopbackPort(t)
	secondPort := freeLoopbackPortExcept(t, firstPort)
	thirdPort := freeLoopbackPortExcept(t, firstPort, secondPort)
	instance := mustCreateInstance(t, application, "原名", firstPort)
	other := mustCreateInstance(t, application, "其他", secondPort)

	// 运行中修改端口被拒绝。
	fakeProc.add(newRunningFakeHandle(instance.ID, instance.Port))
	_, err := application.UpdateInstance(context.Background(), instance.ID, nil, &thirdPort)
	requireAPIError(t, err, 409, apierr.CodeConflictInstanceRunning)

	// 运行中修改名称允许。
	newName := "新名字"
	updated, err := application.UpdateInstance(context.Background(), instance.ID, &newName, nil)
	if err != nil {
		t.Fatalf("运行中修改名称失败: %v", err)
	}
	if updated.Name != newName || updated.Port != firstPort {
		t.Fatalf("更新结果不符: %+v", updated)
	}

	// 停止后修改端口成功。
	fakeProc.remove(instance.ID)
	updated, err = application.UpdateInstance(context.Background(), instance.ID, nil, &thirdPort)
	if err != nil {
		t.Fatalf("停止后修改端口失败: %v", err)
	}
	if updated.Port != thirdPort {
		t.Fatalf("端口 = %d，期望 %d", updated.Port, thirdPort)
	}

	// 端口冲突与非法输入。
	_, err = application.UpdateInstance(context.Background(), instance.ID, nil, &other.Port)
	requireAPIError(t, err, 409, apierr.CodeConflictPortBusy)
	invalidPort := 0
	_, err = application.UpdateInstance(context.Background(), instance.ID, nil, &invalidPort)
	requireAPIError(t, err, 400, apierr.CodeValidation)
	badName := "a/b"
	_, err = application.UpdateInstance(context.Background(), instance.ID, &badName, nil)
	requireAPIError(t, err, 400, apierr.CodeValidation)

	// 实例不存在。
	_, err = application.UpdateInstance(context.Background(), "i-000000000000", nil, &thirdPort)
	requireAPIError(t, err, 404, apierr.CodeNotFound)
}

func TestDeleteInstance(t *testing.T) {
	application, _, fakeProc, dirs := newTestApp(t)
	instance := mustCreateInstance(t, application, "待删除", freeLoopbackPort(t))
	instanceDir := filepath.Join(dirs.InstancesDir, instance.ID)
	// 目录由 CreateInstance 预建；写入启动残留文件模拟已启动过的实例。
	marker := filepath.Join(instanceDir, "workspace", "launch-marker.txt")
	if err := os.WriteFile(marker, []byte("launched"), 0o600); err != nil {
		t.Fatalf("写入启动残留失败: %v", err)
	}
	if err := application.store.SelectInstance(instance.ID); err != nil {
		t.Fatalf("SelectInstance: %v", err)
	}

	fakeProc.add(newRunningFakeHandle(instance.ID, instance.Port))
	_, err := application.DeleteInstance(context.Background(), instance.ID)
	requireAPIError(t, err, 409, apierr.CodeConflictInstanceRunning)

	fakeProc.remove(instance.ID)
	trashedPath, err := application.DeleteInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("DeleteInstance 失败: %v", err)
	}
	if trashedPath == "" || !strings.Contains(trashedPath, "instance-") {
		t.Fatalf("trashedPath = %q，期望包含 instance-", trashedPath)
	}
	if filepath.Clean(filepath.Dir(trashedPath)) != filepath.Clean(dirs.TrashDir) {
		t.Fatalf("trashedPath = %q，期望位于 %q", trashedPath, dirs.TrashDir)
	}
	if info, err := os.Stat(trashedPath); err != nil || !info.IsDir() {
		t.Fatalf("trash 目录不存在: %v", err)
	}
	// 实例目录内容（含启动残留）整体进入 trash。
	if data, readErr := os.ReadFile(filepath.Join(trashedPath, "workspace", "launch-marker.txt")); readErr != nil || string(data) != "launched" {
		t.Fatalf("启动残留未随目录进入 trash: data=%q err=%v", data, readErr)
	}
	if _, err := os.Stat(instanceDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("实例目录应已移走: %v", err)
	}
	if _, ok := application.store.GetInstance(instance.ID); ok {
		t.Fatal("store 中实例应已删除")
	}
	if got := application.store.SelectedInstanceID(); got != "" {
		t.Fatalf("删除选中实例后选中项应清空，实际 %q", got)
	}

	_, err = application.DeleteInstance(context.Background(), instance.ID)
	requireAPIError(t, err, 404, apierr.CodeNotFound)
}

func TestCreateInstanceCreatesDirectories(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	instance := mustCreateInstance(t, application, "目录实例", 0)
	instanceDir := filepath.Join(dirs.InstancesDir, instance.ID)
	if info, err := os.Stat(instanceDir); err != nil || !info.IsDir() {
		t.Fatalf("实例目录未创建: %v", err)
	}
	for _, sub := range instanceSubdirectories {
		path := filepath.Join(instanceDir, sub)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("实例子目录 %s 未创建: %v", sub, err)
		}
	}
}

func TestDeleteNeverStartedInstanceReturnsTrashPath(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	instance := mustCreateInstance(t, application, "从未启动", 0)
	instanceDir := filepath.Join(dirs.InstancesDir, instance.ID)
	if _, err := os.Stat(instanceDir); err != nil {
		t.Fatalf("CreateInstance 应预建实例目录: %v", err)
	}

	trashedPath, err := application.DeleteInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("DeleteInstance 失败: %v", err)
	}
	if trashedPath == "" {
		t.Fatal("从未启动的实例删除也必须返回非空 trashedPath")
	}
	if filepath.Clean(filepath.Dir(trashedPath)) != filepath.Clean(dirs.TrashDir) {
		t.Fatalf("trashedPath = %q，期望位于 %q", trashedPath, dirs.TrashDir)
	}
	if info, err := os.Stat(trashedPath); err != nil || !info.IsDir() {
		t.Fatalf("trash 目录不存在: %v", err)
	}
	if _, err := os.Stat(instanceDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("实例目录应已移走: %v", err)
	}
	if _, ok := application.store.GetInstance(instance.ID); ok {
		t.Fatal("store 中实例应已删除")
	}
}

func TestDeleteInstanceWithoutDirectoryIsCompatible(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	instance := mustCreateInstance(t, application, "旧数据", 0)
	instanceDir := filepath.Join(dirs.InstancesDir, instance.ID)
	if err := os.RemoveAll(instanceDir); err != nil {
		t.Fatalf("移除实例目录失败: %v", err)
	}

	trashedPath, err := application.DeleteInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("目录缺失时删除应兼容成功: %v", err)
	}
	if trashedPath != "" {
		t.Fatalf("目录缺失时 trashedPath 应为空，实际 %q", trashedPath)
	}
	if _, ok := application.store.GetInstance(instance.ID); ok {
		t.Fatal("store 中实例应已删除")
	}
}

func TestCreateInstanceDirectoryFailureRollsBack(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	// 用普通文件占位 instances 目录，使 MkdirAll 失败。
	if err := os.RemoveAll(dirs.InstancesDir); err != nil {
		t.Fatalf("移除 instances 目录失败: %v", err)
	}
	if err := os.WriteFile(dirs.InstancesDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("写入占位文件失败: %v", err)
	}

	_, err := application.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: "目录失败", Version: "0.2.1-rc.3", Profile: "web", Port: 0,
	})
	requireAPIError(t, err, 500, apierr.CodeInternal)
	if list := application.store.ListInstances(); len(list) != 0 {
		t.Fatalf("目录创建失败应回滚实例记录，实际 %+v", list)
	}
}

func TestLaunchInstanceSuccess(t *testing.T) {
	application, fakeRT, fakeProc, _ := newTestApp(t)
	instance := mustCreateInstance(t, application, "启动实例", freeLoopbackPort(t))
	fakeRT.installed[instance.Version] = true

	var lines []string
	result, err := application.LaunchInstance(context.Background(), instance.ID, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("LaunchInstance 失败: %v", err)
	}
	if result.InstanceID != instance.ID || result.Status != "运行中" || result.WebURL != "/workbench/"+instance.ID+"/" {
		t.Fatalf("LaunchResult 不符: %+v", result)
	}
	if len(fakeProc.launchSpecs) != 1 || fakeProc.launchSpecs[0].Port != instance.Port {
		t.Fatalf("启动参数不符: %+v", fakeProc.launchSpecs)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"正在探测", "正在启动进程", "服务已就绪"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("启动日志缺少 %q：%q", want, joined)
		}
	}
	if strings.Contains(joined, testBootstrapToken) {
		t.Fatalf("启动日志泄漏 bootstrap token：%q", joined)
	}
}

func TestLaunchInstancePrechecks(t *testing.T) {
	// Explicit limit 1 keeps the legacy prechecks; the production default is now
	// 0 (unlimited), covered by TestLaunchInstanceDefaultUnlimitedAllowsSecond.
	application, fakeRT, fakeProc, _ := newTestAppWithLimit(t, intPtr(1))
	ctx := context.Background()

	_, err := application.LaunchInstance(ctx, "i-000000000000", nil)
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	first := mustCreateInstance(t, application, "实例一", freeLoopbackPort(t))
	second := mustCreateInstance(t, application, "实例二", freeLoopbackPortExcept(t, first.Port))
	fakeRT.installed[second.Version] = true
	fakeProc.add(newRunningFakeHandle(first.ID, first.Port))
	_, err = application.LaunchInstance(ctx, second.ID, nil)
	requireAPIError(t, err, 409, apierr.CodeConflictRunningInstance)
	_, err = application.LaunchInstance(ctx, first.ID, nil)
	requireAPIError(t, err, 409, apierr.CodeConflictRunningInstance)
	fakeProc.remove(first.ID)

	// 运行时未安装。
	fakeRT.installed[second.Version] = false
	_, err = application.LaunchInstance(ctx, second.ID, nil)
	requireAPIError(t, err, 409, apierr.CodeInstanceNotReady)

	// 工具链缺失。
	fakeRT.installed[second.Version] = true
	originalTools := application.tools
	application.tools = nil
	_, err = application.LaunchInstance(ctx, second.ID, nil)
	requireAPIError(t, err, 500, apierr.CodeInternal)
	application.tools = originalTools

	// 能力探测失败。
	fakeRT2 := newFakeRuntime()
	fakeRT2.installed[second.Version] = true
	fakeRT2.probeErr = errors.New("probe exploded")
	application2 := newApp(Deps{
		Config: application.cfg, Dirs: application.dirs, Logger: discardLogger(), Store: application.store,
		Tools: originalTools,
	}, fakeRT2, fakeProc)
	_, err = application2.LaunchInstance(ctx, second.ID, nil)
	requireAPIError(t, err, 422, apierr.CodeUnsupportedRuntime)
}

func TestLaunchInstanceProcessErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"端口占用", process.ErrPortBusy, 409, apierr.CodeConflictPortBusy},
		{"已在运行", process.ErrAlreadyRunning, 409, apierr.CodeConflictRunningInstance},
		{"未就绪", process.ErrNotReady, 409, apierr.CodeInstanceNotReady},
		{"提前退出", process.ErrExitedBeforeReady, 409, apierr.CodeInstanceNotReady},
		{"未知失败", errors.New("boom"), 409, apierr.CodeInstanceNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			application, fakeRT, fakeProc, _ := newTestApp(t)
			instance := mustCreateInstance(t, application, "失败实例", freeLoopbackPort(t))
			fakeRT.installed[instance.Version] = true
			fakeProc.launchErr = tc.err
			_, err := application.LaunchInstance(context.Background(), instance.ID, nil)
			requireAPIError(t, err, tc.status, tc.code)
		})
	}
}

func TestLaunchInstanceCanceled(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	instance := mustCreateInstance(t, application, "取消", freeLoopbackPort(t))
	fakeRT.installed[instance.Version] = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := application.LaunchInstance(ctx, instance.ID, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Launched with canceled ctx = %v，期望 context.Canceled", err)
	}
}

func TestStopInstance(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	ctx := context.Background()

	err := application.StopInstance(ctx, "i-000000000000")
	requireAPIError(t, err, 404, apierr.CodeNotFound)

	instance := mustCreateInstance(t, application, "停止实例", freeLoopbackPort(t))
	if err := application.StopInstance(ctx, instance.ID); err != nil {
		t.Fatalf("停止未启动实例应成功: %v", err)
	}

	handle := newRunningFakeHandle(instance.ID, instance.Port)
	fakeProc.add(handle)
	if err := application.StopInstance(ctx, instance.ID); err != nil {
		t.Fatalf("StopInstance 失败: %v", err)
	}
	if handle.stopCalls != 1 {
		t.Fatalf("Stop 调用次数 = %d，期望 1", handle.stopCalls)
	}

	handle.stopErr = errors.New("stop failed")
	err = application.StopInstance(ctx, instance.ID)
	requireAPIError(t, err, 500, apierr.CodeInternal)
}

func TestLiveInstanceAndRunning(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	if _, ok := application.LiveInstance("i-000000000000"); ok {
		t.Fatal("未启动实例 LiveInstance 应返回 false")
	}
	if running := application.Running(); len(running) != 0 {
		t.Fatalf("Running 应为空: %+v", running)
	}

	instance := mustCreateInstance(t, application, "运行实例", freeLoopbackPort(t))
	handle := newRunningFakeHandle(instance.ID, instance.Port)
	fakeProc.add(handle)

	live, ok := application.LiveInstance(instance.ID)
	if !ok {
		t.Fatal("LiveInstance 应返回 true")
	}
	if live.InstanceID != instance.ID || live.Port != instance.Port ||
		live.StatusCode != process.StatusRunning || live.StatusMessage != "运行中" || !live.Ready {
		t.Fatalf("LiveStatus 不符: %+v", live)
	}
	if live.Bootstrap == nil || live.Bootstrap.Query().Get("token") != testBootstrapToken {
		t.Fatalf("Bootstrap 不符: %+v", live.Bootstrap)
	}
	running := application.Running()
	if len(running) != 1 || running[0].InstanceID != instance.ID || !running[0].Ready || running[0].PID != 4242 {
		t.Fatalf("Running 不符: %+v", running)
	}

	handle.mu.Lock()
	handle.status = process.Status{Code: process.StatusNotReady, Message: "进程运行中，服务未就绪", PID: 4242}
	handle.ready = false
	handle.bootstrap = nil
	handle.mu.Unlock()
	live, ok = application.LiveInstance(instance.ID)
	if !ok || live.Ready || live.StatusCode != process.StatusNotReady || live.Bootstrap != nil {
		t.Fatalf("未就绪 LiveStatus 不符: %+v", live)
	}
}

func TestResolveProxy(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	ctx := context.Background()

	if _, err := application.ResolveProxy(ctx, "i-000000000000"); !errors.Is(err, proxy.ErrInstanceNotFound) {
		t.Fatalf("实例不存在应返回 ErrInstanceNotFound，实际 %v", err)
	}
	instance := mustCreateInstance(t, application, "代理实例", freeLoopbackPort(t))
	if _, err := application.ResolveProxy(ctx, instance.ID); !errors.Is(err, proxy.ErrInstanceNotRunning) {
		t.Fatalf("无进程应返回 ErrInstanceNotRunning，实际 %v", err)
	}

	handle := &fakeHandle{
		id:     instance.ID,
		port:   instance.Port,
		status: process.Status{Code: process.StatusNotReady, Message: "进程运行中，服务未就绪", PID: 100},
	}
	fakeProc.add(handle)
	if _, err := application.ResolveProxy(ctx, instance.ID); !errors.Is(err, proxy.ErrInstanceNotReady) {
		t.Fatalf("未就绪应返回 ErrInstanceNotReady，实际 %v", err)
	}

	handle.mu.Lock()
	handle.status = process.Status{Code: process.StatusRunning, Message: "运行中", PID: 100}
	handle.ready = true
	handle.bootstrap = nil
	handle.mu.Unlock()
	if _, err := application.ResolveProxy(ctx, instance.ID); !errors.Is(err, proxy.ErrInstanceNotReady) {
		t.Fatalf("运行中但无 bootstrap 应返回 ErrInstanceNotReady，实际 %v", err)
	}

	bootstrap, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(instance.Port) + "/?token=proxy-token")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	handle.mu.Lock()
	handle.bootstrap = bootstrap
	handle.mu.Unlock()
	target, err := application.ResolveProxy(ctx, instance.ID)
	if err != nil {
		t.Fatalf("ResolveProxy 失败: %v", err)
	}
	if target.InstanceID != instance.ID || target.Port != instance.Port || !target.Ready ||
		target.BootstrapPath != "/" || target.BootstrapQuery != "token=proxy-token" {
		t.Fatalf("Target 不符: %+v", target)
	}
}

func TestMutationTryLockBusy(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	instance := mustCreateInstance(t, application, "忙实例", freeLoopbackPort(t))
	fakeProc.add(newRunningFakeHandle(instance.ID, instance.Port))

	application.mu.Lock()
	defer application.mu.Unlock()
	_, err := application.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: "并发", Version: "0.2.1-rc.3", Profile: "web", Port: freeLoopbackPort(t),
	})
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)
	err = application.StopInstance(context.Background(), instance.ID)
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)
}

func TestShutdown(t *testing.T) {
	application, _, fakeProc, _ := newTestApp(t)
	fakeProc.stopAllErr = errors.New("stop all failed")
	if err := application.Shutdown(context.Background()); err == nil {
		t.Fatal("Shutdown 应传播 StopAll 错误")
	}
	fakeProc.stopAllErr = nil
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}
	if fakeProc.stopAllHits != 2 {
		t.Fatalf("StopAll 调用次数 = %d，期望 2", fakeProc.stopAllHits)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Deps{}); err == nil {
		t.Fatal("New 应要求 logger/store/data root")
	}
	dirs := newTestDirs(t)
	st, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if _, err := New(Deps{Logger: discardLogger(), Store: st}); err == nil {
		t.Fatal("New 应要求 data root")
	}
	application, err := New(Deps{Logger: discardLogger(), Store: st, Dirs: dirs})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got := application.Runtimes(); len(got) != 0 {
		t.Fatalf("新实例 Runtimes 应为空: %+v", got)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
