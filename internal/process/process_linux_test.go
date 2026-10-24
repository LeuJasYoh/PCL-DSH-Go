//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// 真实进程测试（仅 Linux/WSL）：通过重新执行测试二进制模拟 DSH 进程。
// 使用方：GOOS=linux go test -c -o bin/tests/process.test ./internal/process
//
//	wsl -d Ubuntu -- .../process.test -test.v
const (
	helperEnv      = "PCL_PROCESS_TEST_HELPER"
	helperModeEnv  = "PCL_PROCESS_TEST_MODE"
	helperPortEnv  = "PCL_PROCESS_TEST_PORT"
	helperTokenEnv = "PCL_PROCESS_TEST_TOKEN"
	helperChildEnv = "PCL_PROCESS_TEST_CHILD_PID_FILE"

	testToken       = "test-token-abc123-secret"
	testPassword    = "hunter2-password"
	testBearerValue = "bearer-secret-value"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	runFakeDSH()
}

func runFakeDSH() {
	mode := os.Getenv(helperModeEnv)
	port, _ := strconv.Atoi(os.Getenv(helperPortEnv))
	token := os.Getenv(helperTokenEnv)
	if token == "" {
		token = testToken
	}

	switch mode {
	case "child-stubborn":
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	case "early-exit":
		fmt.Println("\x1b[31mdsh web: http://127.0.0.1:1/?token=bad-token\x1b[0m")
		os.Exit(3)
	case "bad-token":
		printBadTokenLines(port)
		serveFakeDSH(port, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		return
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
	}

	printTokenLine(port, token)
	// 给读取 goroutine 一点时间解析 token，避免“先就绪、后拿到 token”的测试抖动。
	time.Sleep(100 * time.Millisecond)
	if mode == "tree" {
		spawnStubbornChild()
	}
	if mode == "exit-with-child" {
		spawnStubbornChild()
		go func() {
			time.Sleep(400 * time.Millisecond)
			os.Exit(9)
		}()
	}
	if mode == "exit-after-ready" {
		go func() {
			time.Sleep(800 * time.Millisecond)
			os.Exit(7)
		}()
	}

	var lateRequests atomic.Int32
	serveFakeDSH(port, func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ready-401":
			w.WriteHeader(http.StatusUnauthorized)
		case "late-200":
			if lateRequests.Add(1) <= 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
}

func printTokenLine(port int, token string) {
	fmt.Printf("\x1b[36mINFO\x1b[0m fake dsh starting on port %d\n", port)
	fmt.Printf("dsh web: http://127.0.0.1:%d/?token=%s\n", port, token)
	fmt.Printf("debug password=%s authorization: Bearer %s\n", testPassword, testBearerValue)
	fmt.Println("plain log line")
}

func printBadTokenLines(port int) {
	wrongPort := port + 1
	if wrongPort > 65535 {
		wrongPort = port - 1
	}
	lines := []string{
		"dsh web: http://example.com:" + strconv.Itoa(port) + "/?token=evil1",
		"dsh web: https://127.0.0.1:" + strconv.Itoa(port) + "/?token=evil2",
		"dsh web: http://127.0.0.1:" + strconv.Itoa(wrongPort) + "/?token=evil3",
		"dsh web: http://user:pass@127.0.0.1:" + strconv.Itoa(port) + "/?token=evil4",
		"dsh web: http://127.0.0.1/?token=evil5",
		"dsh web: ftp://127.0.0.1:" + strconv.Itoa(port) + "/?token=evil6",
	}
	for _, line := range lines {
		fmt.Println(line)
	}
}

func serveFakeDSH(port int, handler http.HandlerFunc) {
	server := &http.Server{
		Addr:              "127.0.0.1:" + strconv.Itoa(port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fake dsh server error:", err)
		os.Exit(4)
	}
}

func spawnStubbornChild() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "os.Executable:", err)
		return
	}
	child := exec.Command(executable, "-test.run=^TestHelperProcess$")
	child.Env = append(os.Environ(), helperEnv+"=1", helperModeEnv+"=child-stubborn")
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "spawn child:", err)
		return
	}
	pidFile := os.Getenv(helperChildEnv)
	if pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	}
}

type fakeDSH struct {
	spec         LaunchSpec
	logPath      string
	childPIDFile string
	port         int
}

func newFakeDSH(t *testing.T, instanceID, mode string) *fakeDSH {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	port := freeLoopbackPort(t)
	root := t.TempDir()
	return &fakeDSH{
		port: port,
		spec: LaunchSpec{
			InstanceID: instanceID,
			Command:    []string{executable, "-test.run=^TestHelperProcess$"},
			Env: []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				"HOME=" + filepath.Join(root, "home"),
				helperEnv + "=1",
				helperModeEnv + "=" + mode,
				helperPortEnv + "=" + strconv.Itoa(port),
				helperTokenEnv + "=" + testToken,
				helperChildEnv + "=" + filepath.Join(root, "child.pid"),
			},
			Dir:           filepath.Join(root, "workspace"),
			Port:          port,
			LogPath:       filepath.Join(root, "logs", instanceID+".log"),
			ReadyTimeout:  5 * time.Second,
			ReadyInterval: 100 * time.Millisecond,
		},
		logPath:      filepath.Join(root, "logs", instanceID+".log"),
		childPIDFile: filepath.Join(root, "child.pid"),
	}
}

// freeLoopbackPort 选一个空闲回环端口。
// WSL2 的 localhost 转发在监听 socket 刚关闭后的几毫秒内可能仍接受连接，
// 因此关闭后需轮询到“连接被拒绝”才算真正可用，避免 Launch 误报 ErrPortBusy。
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("监听临时端口失败: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		deadline := time.Now().Add(500 * time.Millisecond)
		for !portFree(port) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if portFree(port) {
			return port
		}
	}
	t.Fatal("无法找到稳定的空闲回环端口")
	return 0
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitForCondition(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待超时（%s）: %s", timeout, description)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func aliveLinux(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	stat, ok := parseProcStat(string(data))
	if !ok {
		return true
	}
	return stat.state != 'Z' && stat.state != 'X'
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志 %s 失败: %v", path, err)
	}
	return string(data)
}

func launchFakeDSH(t *testing.T, manager *Manager, fake *fakeDSH) *Handle {
	t.Helper()
	handle, err := manager.Launch(context.Background(), fake.spec)
	if errors.Is(err, ErrPortBusy) {
		// WSL2 localhost 转发的极短残留：换一个端口重试一次，避免环境抖动导致误报。
		fake.port = freeLoopbackPort(t)
		fake.spec.Port = fake.port
		handle, err = manager.Launch(context.Background(), fake.spec)
	}
	if err != nil {
		t.Fatalf("Launch 失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
	return handle
}

func TestParseProcStatLinux(t *testing.T) {
	const line = "1234 (my proc (x)) S 1 1234 1234 0 -1 4194560 100 0 0 0 0 0 0 0 20 0 1 0 987654 0 0"
	stat, ok := parseProcStat(line)
	if !ok {
		t.Fatal("parseProcStat 应解析成功")
	}
	if stat.state != 'S' || stat.startTime != 987654 {
		t.Fatalf("parseProcStat 结果不符: %+v", stat)
	}
	for _, invalid := range []string{
		"",
		"1234",
		"1234 (x) S",
		"1234 (x) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18",
		"1234 (x) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 notanumber 0 0",
	} {
		if _, ok := parseProcStat(invalid); ok {
			t.Fatalf("非法 stat 内容不应解析成功: %q", invalid)
		}
	}
	current, ok := readProcStat(os.Getpid())
	if !ok {
		t.Fatal("读取当前进程 stat 失败")
	}
	if current.startTime == 0 || current.state == 'Z' || current.state == 'X' {
		t.Fatalf("当前进程 stat 可疑: %+v", current)
	}
	startTime, ok := processStartTime(os.Getpid())
	if !ok || startTime != current.startTime {
		t.Fatalf("processStartTime 与 readProcStat 不一致: %d vs %d (%v)", startTime, current.startTime, ok)
	}
	if processAlive(os.Getpid(), current.startTime) != true {
		t.Fatal("processAlive(当前进程, 正确 starttime) 应为 true")
	}
	if processAlive(os.Getpid(), current.startTime+1) {
		t.Fatal("processAlive(starttime 不匹配) 应为 false")
	}
}

func TestLinuxLaunchReadyAndStop(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000001", "ready")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)

	if !handle.Ready() {
		t.Fatalf("服务应就绪，状态: %+v", handle.Status())
	}
	status := handle.Status()
	if status.Code != StatusRunning || status.Message != "运行中" || status.PID <= 0 {
		t.Fatalf("运行状态不符: %+v", status)
	}
	if status.StartedAt.IsZero() {
		t.Fatal("StartedAt 不应为零值")
	}
	waitForCondition(t, 3*time.Second, "解析 bootstrap token", func() bool { return handle.BootstrapURL() != nil })
	bootstrap := handle.BootstrapURL()
	if bootstrap.Scheme != "http" || bootstrap.Hostname() != "127.0.0.1" || bootstrap.Port() != strconv.Itoa(fake.port) {
		t.Fatalf("bootstrap URI 不符: %s", bootstrap.String())
	}
	if got := bootstrap.Query().Get("token"); got != testToken {
		t.Fatalf("bootstrap token = %q，期望 %q", got, testToken)
	}
	if got, ok := manager.Get(fake.spec.InstanceID); !ok || got != handle {
		t.Fatal("Manager.Get 应返回当前 Handle")
	}
	if running := manager.Running(); len(running) != 1 || running[0] != handle {
		t.Fatalf("Manager.Running 不符: %+v", running)
	}
	if portFree(fake.port) {
		t.Fatalf("就绪后端口 %d 应被监听", fake.port)
	}

	waitForCondition(t, 3*time.Second, "日志写入 dsh web 行", func() bool {
		data, err := os.ReadFile(fake.logPath)
		return err == nil && strings.Contains(string(data), "dsh web:")
	})
	logText := readLogFile(t, fake.logPath)
	for _, secret := range []string{testToken, testPassword, testBearerValue} {
		if strings.Contains(logText, secret) {
			t.Fatalf("日志泄漏敏感值 %q:\n%s", secret, logText)
		}
	}
	if !strings.Contains(logText, "***") {
		t.Fatalf("日志应包含脱敏结果 ***:\n%s", logText)
	}
	if !strings.Contains(logText, "plain log line") {
		t.Fatalf("普通日志行不应被丢弃:\n%s", logText)
	}
	if info, err := os.Stat(fake.logPath); err != nil {
		t.Fatalf("stat 日志失败: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("日志文件权限 = %04o，期望 0600", perm)
	}

	rootPID := handle.Status().PID
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	select {
	case <-handle.Wait():
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 后 Wait channel 应已关闭")
	}
	if aliveLinux(rootPID) {
		t.Fatalf("Stop 后根进程 %d 仍存活", rootPID)
	}
	status = handle.Status()
	if status.Code != StatusStopped || status.Message != "未启动" {
		t.Fatalf("Stop 后状态不符: %+v", status)
	}
	if _, ok := manager.Get(fake.spec.InstanceID); ok {
		t.Fatal("Stop 成功后 Manager.Get 应返回 false")
	}
	if running := manager.Running(); len(running) != 0 {
		t.Fatalf("Stop 后 Running 应为空: %+v", running)
	}
	waitForCondition(t, 3*time.Second, "端口释放", func() bool { return portFree(fake.port) })
}

func TestLinuxReadinessAccepts401WithBootstrap(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000002", "ready-401")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)
	if !handle.Ready() {
		t.Fatalf("401 + bootstrap 应判定就绪，状态: %+v", handle.Status())
	}
	if handle.BootstrapURL() == nil {
		t.Fatal("bootstrap URI 应已捕获")
	}
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
}

func TestLinuxReadinessRetries404(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000003", "late-200")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)
	if !handle.Ready() {
		t.Fatalf("404 后应继续轮询并最终就绪，状态: %+v", handle.Status())
	}
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
}

func TestLinuxRejectsNonLoopbackToken(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000004", "bad-token")
	fake.spec.ReadyTimeout = 1200 * time.Millisecond
	fake.spec.ReadyInterval = 100 * time.Millisecond
	manager := NewManager(discardLogger())
	handle, err := manager.Launch(context.Background(), fake.spec)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("非回环 token 应导致 NOT_READY，实际 %v", err)
	}
	if handle == nil {
		t.Fatal("NOT_READY 时应返回仍运行的 Handle")
	}
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
	if handle.BootstrapURL() != nil {
		t.Fatalf("非回环/端口不符/userinfo 的 token 不应被接受: %s", handle.BootstrapURL().String())
	}
	status := handle.Status()
	if status.Code != StatusNotReady || status.Message != "进程运行中，服务未就绪" {
		t.Fatalf("NOT_READY 状态不符: %+v", status)
	}
	if !aliveLinux(status.PID) {
		t.Fatalf("NOT_READY 时进程应仍在运行（PID %d）", status.PID)
	}
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if handle.BootstrapURL() != nil {
		t.Fatal("Stop 后 bootstrap URI 应清除")
	}
	waitForCondition(t, 3*time.Second, "端口释放", func() bool { return portFree(fake.port) })
}

func TestLinuxExitedBeforeReady(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000005", "early-exit")
	manager := NewManager(discardLogger())
	handle, err := manager.Launch(context.Background(), fake.spec)
	if !errors.Is(err, ErrExitedBeforeReady) {
		t.Fatalf("提前退出应返回 ErrExitedBeforeReady，实际 %v", err)
	}
	if handle == nil {
		t.Fatal("进程已退出时应返回 Handle")
	}
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
	waitForCondition(t, 2*time.Second, "状态变为 EXITED", func() bool {
		return handle.Status().Code == StatusExited
	})
	if status := handle.Status(); status.Message != "已停止" {
		t.Fatalf("EXITED 状态 Message 不符: %+v", status)
	}
	select {
	case <-handle.Wait():
	case <-time.After(2 * time.Second):
		t.Fatal("进程退出后 Wait 应关闭")
	}
	if running := manager.Running(); len(running) != 0 {
		t.Fatalf("退出后 Running 应为空: %+v", running)
	}
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("清理已退出实例失败: %v", err)
	}
	if _, ok := manager.Get(fake.spec.InstanceID); ok {
		t.Fatal("Stop 清理后 Manager.Get 应返回 false")
	}
}

func TestLinuxPortBusy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	fake := newFakeDSH(t, "i-000000000006", "ready")
	fake.spec.Port = port
	manager := NewManager(discardLogger())
	handle, err := manager.Launch(context.Background(), fake.spec)
	if !errors.Is(err, ErrPortBusy) {
		t.Fatalf("端口占用应返回 ErrPortBusy，实际 %v", err)
	}
	if handle != nil {
		t.Fatal("端口占用时不应返回 Handle")
	}
	if _, ok := manager.Get(fake.spec.InstanceID); ok {
		t.Fatal("端口占用失败不应注册 Handle")
	}
}

func TestLinuxAlreadyRunning(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000007", "ready")
	manager := NewManager(discardLogger())
	first := launchFakeDSH(t, manager, fake)
	second, err := manager.Launch(context.Background(), fake.spec)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("重复启动应返回 ErrAlreadyRunning，实际 %v", err)
	}
	if second != nil {
		t.Fatal("重复启动不应返回 Handle")
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
}

func TestLinuxStopKillsDescendantsButNotOthers(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000008", "tree")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)

	waitForCondition(t, 5*time.Second, "helper 写出子进程 PID", func() bool {
		data, err := os.ReadFile(fake.childPIDFile)
		return err == nil && len(strings.TrimSpace(string(data))) > 0
	})
	childPID, err := strconv.Atoi(strings.TrimSpace(string(readLogFile(t, fake.childPIDFile))))
	if err != nil {
		t.Fatalf("解析子进程 PID 失败: %v", err)
	}
	if !aliveLinux(childPID) {
		t.Fatalf("子进程 %d 应存活", childPID)
	}

	unrelated := exec.Command("sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("启动无关进程失败: %v", err)
	}
	unrelatedPID := unrelated.Process.Pid
	t.Cleanup(func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})

	rootPID := handle.Status().PID
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	waitForCondition(t, 3*time.Second, "子孙进程被清理", func() bool { return !aliveLinux(childPID) })
	if aliveLinux(rootPID) {
		t.Fatalf("根进程 %d 仍存活", rootPID)
	}
	if !aliveLinux(unrelatedPID) {
		t.Fatalf("无关进程 %d 不应被杀死（只杀自有进程树）", unrelatedPID)
	}
	waitForCondition(t, 3*time.Second, "端口释放", func() bool { return portFree(fake.port) })
}

func TestLinuxStopEscalatesToSIGKILL(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000009", "stubborn")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)
	rootPID := handle.Status().PID

	startedAt := time.Now()
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	elapsed := time.Since(startedAt)
	if elapsed < 2500*time.Millisecond {
		t.Fatalf("忽略 SIGTERM 的进程应先等待约 3s 再 SIGKILL，实际耗时 %s", elapsed)
	}
	if aliveLinux(rootPID) {
		t.Fatalf("SIGKILL 后进程 %d 仍存活", rootPID)
	}
	status := handle.Status()
	if status.Code != StatusStopped {
		t.Fatalf("强制停止后状态应为 STOPPED，实际 %+v", status)
	}
	waitForCondition(t, 3*time.Second, "端口释放", func() bool { return portFree(fake.port) })
}

func TestLinuxSelfExitState(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000010", "exit-after-ready")
	manager := NewManager(discardLogger())
	handle := launchFakeDSH(t, manager, fake)

	waitForCondition(t, 4*time.Second, "进程自行退出", func() bool {
		return handle.Status().Code == StatusExited
	})
	select {
	case <-handle.Wait():
	case <-time.After(2 * time.Second):
		t.Fatal("自行退出后 Wait 应关闭")
	}
	if status := handle.Status(); status.Message != "已停止" {
		t.Fatalf("自行退出状态 Message 不符: %+v", status)
	}
	if running := manager.Running(); len(running) != 0 {
		t.Fatalf("退出后 Running 应为空: %+v", running)
	}
	if _, ok := manager.Get(fake.spec.InstanceID); !ok {
		t.Fatal("自行退出后 Handle 应保留到 Stop/重启")
	}
	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 清理失败: %v", err)
	}
	if _, ok := manager.Get(fake.spec.InstanceID); ok {
		t.Fatal("Stop 后 Manager.Get 应返回 false")
	}
}

func TestLinuxExitedProcessWithLiveDescendant(t *testing.T) {
	fake := newFakeDSH(t, "i-000000000013", "exit-with-child")
	manager := NewManager(discardLogger())
	handle, err := manager.Launch(context.Background(), fake.spec)
	if err != nil && !errors.Is(err, ErrExitedBeforeReady) {
		t.Fatalf("Launch 失败: %v", err)
	}
	if handle == nil {
		t.Fatal("Launch 应返回 Handle")
	}
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })

	waitForCondition(t, 5*time.Second, "根进程退出", func() bool {
		return handle.Status().Code == StatusExited
	})
	waitForCondition(t, 5*time.Second, "helper 写出子进程 PID", func() bool {
		data, readErr := os.ReadFile(fake.childPIDFile)
		return readErr == nil && len(strings.TrimSpace(string(data))) > 0
	})
	childPID, convErr := strconv.Atoi(strings.TrimSpace(string(readLogFile(t, fake.childPIDFile))))
	if convErr != nil {
		t.Fatalf("解析子进程 PID 失败: %v", convErr)
	}
	if !aliveLinux(childPID) {
		t.Fatalf("根进程退出后子进程 %d 应仍存活（用于验证不覆盖旧进程树）", childPID)
	}

	// 根进程已退出但子孙存活：仍视为运行中，重复启动必须被拒绝（参考实现 running(id)）。
	if running := manager.Running(); len(running) != 1 || running[0] != handle {
		t.Fatalf("存在存活子孙时 Running 应包含该实例，实际 %+v", running)
	}
	second, launchErr := manager.Launch(context.Background(), fake.spec)
	if !errors.Is(launchErr, ErrAlreadyRunning) {
		t.Fatalf("旧进程树仍有子孙时应拒绝重复启动，实际 %v", launchErr)
	}
	if second != nil {
		t.Fatal("拒绝启动时不应返回 Handle")
	}

	if err := handle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop 清理失败: %v", err)
	}
	waitForCondition(t, 3*time.Second, "残存子孙被清理", func() bool { return !aliveLinux(childPID) })
	if _, ok := manager.Get(fake.spec.InstanceID); ok {
		t.Fatal("Stop 清理后 Manager.Get 应返回 false")
	}
}

func TestLinuxStopAll(t *testing.T) {
	manager := NewManager(discardLogger())
	first := newFakeDSH(t, "i-000000000011", "ready")
	second := newFakeDSH(t, "i-000000000012", "ready-401")
	launchFakeDSH(t, manager, first)
	launchFakeDSH(t, manager, second)
	if running := manager.Running(); len(running) != 2 {
		t.Fatalf("Running 应为 2，实际 %d", len(running))
	}
	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll 失败: %v", err)
	}
	if running := manager.Running(); len(running) != 0 {
		t.Fatalf("StopAll 后 Running 应为空: %+v", running)
	}
	for _, fake := range []*fakeDSH{first, second} {
		if _, ok := manager.Get(fake.spec.InstanceID); ok {
			t.Fatalf("StopAll 后实例 %s 应被移除", fake.spec.InstanceID)
		}
		waitForCondition(t, 3*time.Second, "端口释放", func() bool { return portFree(fake.port) })
	}
}
