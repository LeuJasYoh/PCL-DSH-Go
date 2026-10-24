// SPDX-License-Identifier: GPL-3.0-or-later

// Package process 负责启动、就绪探测、状态查询与停止由本管理器创建的 DSH 子进程。
//
// 语义严格遵循 docs/DSH_INTEGRATION.md §5 与 docs/M0_INTERFACES.md §6：
//   - 使用独立进程组（Unix Setpgid）启动，只停止自有进程树；
//   - 合并 stdout/stderr 逐行读取、ANSI 去色、解析 “dsh web:” bootstrap URI（仅回环 + 实例端口 +
//     无 userinfo，只存内存），写日志前脱敏；
//   - 每 100ms 记录 descendants（PID + /proc/<pid>/stat starttime），防止只杀自有进程/PID 复用误杀；
//   - 就绪轮询 HTTP/1.1、显式禁用代理（2xx/3xx，或 401/403 且已捕获 bootstrap URI）；
//   - 停止时先子孙后根：SIGTERM → 3s → SIGKILL → 4s，最后轮询端口释放 2s。
package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 状态码。字符串值被 HTTP API 复用，前端 M1 依赖中文 Message。
const (
	StatusStopped  = "STOPPED"   // 未启动
	StatusStarting = "STARTING"  // 启动中
	StatusRunning  = "RUNNING"   // 运行中
	StatusStopping = "STOPPING"  // 停止中
	StatusNotReady = "NOT_READY" // 进程运行中，服务未就绪
	StatusPortBusy = "PORT_BUSY" // 端口被占用
	StatusExited   = "EXITED"    // 已停止（进程已退出）
)

// 就绪等待默认值（docs/DSH_INTEGRATION.md §5.4）。
const (
	DefaultReadyTimeout  = 90 * time.Second
	DefaultReadyInterval = 400 * time.Millisecond
)

// 停止流程时间常量（docs/DSH_INTEGRATION.md §5.5）。
const (
	stopGracePeriod  = 3 * time.Second
	stopKillWait     = 4 * time.Second
	descendantWait   = 2 * time.Second
	portReleaseWait  = 2 * time.Second
	descendantPollMS = 100
)

// 可被 errors.Is 识别的错误。
var (
	// ErrInvalidSpec 表示 LaunchSpec 校验失败。
	ErrInvalidSpec = errors.New("启动参数无效")
	// ErrAlreadyRunning 表示同一实例已有活跃进程。
	ErrAlreadyRunning = errors.New("实例进程已在运行")
	// ErrPortBusy 表示端口已被其他程序占用（本程序不会停止它）。
	ErrPortBusy = errors.New("端口已被占用")
	// ErrNotReady 表示进程仍在运行但服务在超时内未就绪；此时 Handle 仍可在调用方 Stop。
	ErrNotReady = errors.New("进程运行中，服务未就绪")
	// ErrExitedBeforeReady 表示进程在服务就绪前退出。
	ErrExitedBeforeReady = errors.New("进程在服务就绪前退出")
	// ErrProcessNotExited 表示停止等待超时，进程树仍有存活进程。
	ErrProcessNotExited = errors.New("进程尚未退出，暂不能修改该实例")
)

// LaunchSpec 描述一次 DSH 进程启动。Command[0] 必须是绝对路径（node），Env 是完整环境
// （不从调用方继承；nil 等价于空环境），Dir 是工作目录，LogPath 是合并 stdout/stderr 的落盘路径。
type LaunchSpec struct {
	InstanceID    string
	Command       []string
	Env           []string
	Dir           string
	Port          int
	LogPath       string
	ReadyTimeout  time.Duration // 0 = 90s
	ReadyInterval time.Duration // 0 = 400ms
}

// Status 是实例进程状态快照。
type Status struct {
	Code      string // STOPPED|STARTING|RUNNING|STOPPING|NOT_READY|PORT_BUSY|EXITED
	Message   string // 中文状态（前端依赖）
	PID       int
	StartedAt time.Time
}

// descendant 记录一个自有子进程的身份：PID + /proc/<pid>/stat 第 22 字段 starttime。
type descendant struct {
	pid       int
	startTime uint64
	depth     int
}

// Manager 管理本进程创建的 DSH 实例进程（一个 instanceID 至多一个 Handle）。
type Manager struct {
	mu      sync.Mutex
	logger  *slog.Logger
	handles map[string]*Handle
}

// NewManager 创建进程管理器；logger 为 nil 时使用 slog.Default()。
func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{logger: logger, handles: make(map[string]*Handle)}
}

// Launch 启动实例并阻塞到服务就绪、进程退出或超时。
//
// 错误语义：
//   - 返回 nil Handle + error：参数无效/端口占用/重复启动/spawn 失败，未产生自有进程；
//   - 返回非 nil Handle + ErrNotReady：进程仍在运行但服务未就绪，调用方应 Stop；
//   - 返回非 nil Handle + ErrExitedBeforeReady：进程已退出（状态 EXITED），调用方可 Stop 清理；
//   - ctx 取消：尽力停止本次启动的进程后返回 ctx 错误。
func (m *Manager) Launch(ctx context.Context, spec LaunchSpec) (*Handle, error) {
	if err := validateLaunchSpec(&spec); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("启动实例已取消: %w", err)
	}
	if m.activeHandle(spec.InstanceID) != nil {
		return nil, fmt.Errorf("%w：实例 %s，请先停止", ErrAlreadyRunning, spec.InstanceID)
	}
	// 端口被占用只报错，绝不停止其他程序。
	if !portFree(spec.Port) {
		return nil, fmt.Errorf("%w：端口 %d 已被占用（本程序不会停止其他进程）", ErrPortBusy, spec.Port)
	}

	m.mu.Lock()
	if existing, ok := m.handles[spec.InstanceID]; ok && existing.isActive() {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w：实例 %s，请先停止", ErrAlreadyRunning, spec.InstanceID)
	}
	handle, err := m.spawn(spec)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.handles[spec.InstanceID] = handle
	m.mu.Unlock()

	m.logger.Info("DSH 实例进程已启动", "instanceId", spec.InstanceID, "pid", handle.rootPID, "port", spec.Port)
	if readyErr := handle.awaitReady(ctx); readyErr != nil {
		switch {
		case errors.Is(readyErr, ErrNotReady):
			handle.setStatusIfNotExited(StatusNotReady)
			m.logger.Warn("DSH 服务在超时内未就绪，进程仍在运行", "instanceId", spec.InstanceID, "pid", handle.rootPID)
			return handle, fmt.Errorf("%w：等待 %s 超时，进程仍在运行（PID %d），请查看日志 %s",
				ErrNotReady, spec.ReadyTimeout, handle.rootPID, spec.LogPath)
		case errors.Is(readyErr, ErrExitedBeforeReady):
			return handle, fmt.Errorf("%w：退出码 %d，请查看日志 %s",
				ErrExitedBeforeReady, handle.exitCodeNow(), spec.LogPath)
		default:
			_ = handle.Stop(context.Background())
			return handle, fmt.Errorf("启动实例已取消: %w", readyErr)
		}
	}
	if !handle.setStatusIfNotExited(StatusRunning) {
		return handle, fmt.Errorf("%w：进程在就绪后立即退出，请查看日志 %s", ErrExitedBeforeReady, spec.LogPath)
	}
	m.logger.Info("DSH 实例服务就绪", "instanceId", spec.InstanceID, "pid", handle.rootPID, "port", spec.Port)
	return handle, nil
}

// Get 返回指定实例最近的 Handle（进程可能已退出，调用方应检查 Status）。
func (m *Manager) Get(instanceID string) (*Handle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	handle, ok := m.handles[instanceID]
	return handle, ok
}

// Running 返回当前活跃（进程可能存活）的 Handle，按 instanceID 稳定排序。
func (m *Manager) Running() []*Handle {
	m.mu.Lock()
	all := make([]*Handle, 0, len(m.handles))
	for _, handle := range m.handles {
		all = append(all, handle)
	}
	m.mu.Unlock()

	running := make([]*Handle, 0, len(all))
	for _, handle := range all {
		if handle.isActive() {
			running = append(running, handle)
		}
	}
	sort.Slice(running, func(i, j int) bool { return running[i].instanceID < running[j].instanceID })
	return running
}

// StopAll 停止本管理器创建的所有实例进程树；返回各实例停止错误的聚合。
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	handles := make([]*Handle, 0, len(m.handles))
	for _, handle := range m.handles {
		handles = append(handles, handle)
	}
	m.mu.Unlock()

	var errs []error
	for _, handle := range handles {
		if err := handle.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("实例 %s: %w", handle.instanceID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) activeHandle(instanceID string) *Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	handle, ok := m.handles[instanceID]
	if !ok || !handle.isActive() {
		return nil
	}
	return handle
}

func (m *Manager) remove(handle *Handle) {
	m.mu.Lock()
	if current, ok := m.handles[handle.instanceID]; ok && current == handle {
		delete(m.handles, handle.instanceID)
	}
	m.mu.Unlock()
}

// spawn 创建日志文件、启动进程并开始后台 goroutine；调用方必须持有 m.mu。
func (m *Manager) spawn(spec LaunchSpec) (*Handle, error) {
	logFile, err := openInstanceLog(spec.LogPath)
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("创建进程输出管道失败: %w", err)
	}

	handle := &Handle{
		manager:       m,
		logger:        m.logger.With("instanceId", spec.InstanceID),
		instanceID:    spec.InstanceID,
		port:          spec.Port,
		logPath:       spec.LogPath,
		startedAt:     time.Now(),
		code:          StatusStarting,
		done:          make(chan struct{}),
		readerDone:    make(chan struct{}),
		desc:          make(map[int]descendant),
		redactor:      newRedactor(spec.Env),
		logFile:       logFile,
		probeClient:   newProbeClient(),
		readyTimeout:  spec.ReadyTimeout,
		readyInterval: spec.ReadyInterval,
	}

	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = spec.Dir
	if spec.Env == nil {
		cmd.Env = []string{}
	} else {
		cmd.Env = append([]string(nil), spec.Env...)
	}
	configureProcessGroup(cmd)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		handle.closeLog()
		return nil, fmt.Errorf("启动进程失败（%s）: %w", spec.Command[0], err)
	}
	// 父进程关闭写端；读端在所有子进程关闭写端后 EOF。
	_ = writer.Close()

	rootPID := cmd.Process.Pid
	handle.mu.Lock()
	handle.cmd = cmd
	handle.rootPID = rootPID
	handle.mu.Unlock()
	handle.writeLogLine(fmt.Sprintf("[pcl-dsh-go] 启动实例 %s，PID %d，端口 %d", spec.InstanceID, rootPID, spec.Port))

	go handle.readOutput(reader)
	go handle.trackDescendants(rootPID)
	go handle.waitLoop(cmd)
	return handle, nil
}

// Handle 是一个 DSH 实例进程的句柄。
type Handle struct {
	manager *Manager
	logger  *slog.Logger

	instanceID string
	port       int
	logPath    string
	startedAt  time.Time

	done          chan struct{} // 进程退出时关闭
	readerDone    chan struct{} // stdout/stderr 读取 goroutine 结束时关闭
	probeClient   *http.Client
	readyTimeout  time.Duration
	readyInterval time.Duration

	stopMu sync.Mutex

	mu        sync.Mutex
	cmd       *exec.Cmd
	rootPID   int
	code      string
	bootstrap *url.URL
	exited    bool
	stopped   bool
	exitCode  int
	desc      map[int]descendant

	logMu   sync.Mutex
	logFile *os.File

	redactor *redactor
}

// InstanceID 返回实例 ID。
func (h *Handle) InstanceID() string { return h.instanceID }

// Port 返回启动参数中登记的实例端口（M1 app 用于 LiveStatus）。
func (h *Handle) Port() int { return h.port }

// Status 返回当前状态快照。
func (h *Handle) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	pid := 0
	if h.cmd != nil {
		pid = h.rootPID
	}
	return Status{Code: h.code, Message: statusMessage(h.code), PID: pid, StartedAt: h.startedAt}
}

// Ready 表示服务已就绪（状态 RUNNING）。
func (h *Handle) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.code == StatusRunning
}

// BootstrapURL 返回内存中的 bootstrap 地址（含 token query）。未就绪时为 nil。
// 返回值是拷贝；调用方禁止写日志/序列化该 URL。
func (h *Handle) BootstrapURL() *url.URL {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.code != StatusRunning || h.bootstrap == nil {
		return nil
	}
	copied := *h.bootstrap
	return &copied
}

// Wait 返回一个在进程退出时关闭的 channel。
func (h *Handle) Wait() <-chan struct{} { return h.done }

// isActive 表示该实例仍"运行中"：根进程存活，或根进程已退出但仍有存活的子孙进程
// （与参考实现 running(id) 一致，避免旧进程树的子进程被新实例覆盖而漏杀）。
func (h *Handle) isActive() bool {
	h.mu.Lock()
	cmd := h.cmd
	exited := h.exited
	stopped := h.stopped
	h.mu.Unlock()
	if cmd == nil || stopped {
		return false
	}
	if !exited {
		return true
	}
	return h.anyDescendantAlive()
}

func (h *Handle) setStatus(code string) {
	h.mu.Lock()
	h.code = code
	h.mu.Unlock()
}

// setStatusIfNotExited 只在进程尚未退出/停止时更新状态，返回是否更新成功。
func (h *Handle) setStatusIfNotExited(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exited || h.stopped {
		return false
	}
	h.code = code
	return true
}

func (h *Handle) exitedNow() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exited
}

func (h *Handle) exitCodeNow() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitCode
}

func (h *Handle) hasBootstrap() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bootstrap != nil
}

func (h *Handle) setBootstrap(u *url.URL) {
	h.mu.Lock()
	if h.bootstrap == nil {
		h.bootstrap = u
	}
	h.mu.Unlock()
	h.redactor.addURLSecrets(u)
}

// Stop 停止本实例进程树：先子孙后根 SIGTERM，等待 3s，再 SIGKILL，最多再等 4s；
// 之后轮询端口释放最多 2s。ctx 被取消时仍会尽力清理，并返回明确错误。
func (h *Handle) Stop(ctx context.Context) error {
	h.stopMu.Lock()
	defer h.stopMu.Unlock()

	h.mu.Lock()
	rootPID := h.rootPID
	cmd := h.cmd
	exited := h.exited
	stopped := h.stopped
	previousCode := h.code
	h.mu.Unlock()

	if cmd == nil {
		h.mu.Lock()
		h.stopped = true
		h.mu.Unlock()
		h.closeLog()
		h.manager.remove(h)
		return nil
	}
	if stopped {
		h.closeLog()
		h.manager.remove(h)
		return nil
	}

	if !exited {
		h.setStatus(StatusStopping)
	}
	h.logger.Info("正在停止 DSH 实例进程树", "pid", rootPID)

	targets := h.collectStopTargets()
	for _, target := range targets {
		_ = terminatePID(target.pid, target.startTime)
	}
	if !exited {
		_ = terminateProcess(cmd.Process)
	}

	interrupted := false
	rootExited := exited
	if !rootExited {
		select {
		case <-h.done:
			rootExited = true
		case <-ctx.Done():
			interrupted = true
		case <-time.After(stopGracePeriod):
		}
	}
	if !rootExited || interrupted {
		for _, target := range targets {
			_ = killPID(target.pid, target.startTime)
		}
		if !rootExited {
			_ = killProcess(cmd.Process)
		}
		rootExited = h.waitExit(stopKillWait)
	}

	// 根进程退出后也要强制清理仍存活的子孙（例如忽略 SIGTERM 的孙进程）。
	for _, target := range h.trackedTargets() {
		if processAlive(target.pid, target.startTime) {
			_ = killPID(target.pid, target.startTime)
		}
	}
	descendantsGone := h.waitDescendantsGone(descendantWait)

	// 等待输出读取结束，尽量保留最后几行日志（最多 1s）。
	select {
	case <-h.readerDone:
	case <-time.After(time.Second):
	}

	if !rootExited || !descendantsGone {
		h.writeLogLine("[pcl-dsh-go] 停止超时，进程树尚未完全退出")
		h.closeLog()
		h.mu.Lock()
		if !h.stopped {
			switch {
			case rootExited:
				h.code = StatusExited
			default:
				h.code = previousCode
			}
		}
		h.mu.Unlock()
		return fmt.Errorf("%w：PID %d 或其子孙进程在超时内未退出", ErrProcessNotExited, rootPID)
	}

	h.writeLogLine("[pcl-dsh-go] 实例已停止")
	h.closeLog()
	h.mu.Lock()
	h.stopped = true
	h.bootstrap = nil
	h.code = StatusStopped
	h.mu.Unlock()
	h.manager.remove(h)

	if !h.waitPortRelease(portReleaseWait) {
		h.logger.Warn("实例端口在停止后仍被占用", "port", h.port)
	}
	h.logger.Info("已停止 DSH 实例进程树", "pid", rootPID, "port", h.port)

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("停止操作被中断，已尽力清理: %w", err)
	}
	return nil
}

func (h *Handle) waitExit(timeout time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (h *Handle) waitPortRelease(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if portFree(h.port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(80 * time.Millisecond)
	}
}

// collectStopTargets 合并已记录的 descendants 与当前快照，按深度从深到浅排序。
func (h *Handle) collectStopTargets() []descendant {
	h.mu.Lock()
	rootPID := h.rootPID
	exited := h.exited
	merged := make(map[int]descendant, len(h.desc))
	for pid, item := range h.desc {
		merged[pid] = item
	}
	h.mu.Unlock()

	if !exited && rootPID > 0 {
		for _, item := range snapshotDescendants(rootPID) {
			merged[item.pid] = item
		}
	}
	targets := make([]descendant, 0, len(merged))
	for _, item := range merged {
		targets = append(targets, item)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].depth != targets[j].depth {
			return targets[i].depth > targets[j].depth
		}
		return targets[i].pid > targets[j].pid
	})

	h.mu.Lock()
	if h.desc == nil {
		h.desc = make(map[int]descendant)
	}
	for _, item := range targets {
		h.desc[item.pid] = item
	}
	h.mu.Unlock()
	return targets
}

func (h *Handle) trackedTargets() []descendant {
	h.mu.Lock()
	defer h.mu.Unlock()
	targets := make([]descendant, 0, len(h.desc))
	for _, item := range h.desc {
		targets = append(targets, item)
	}
	return targets
}

func (h *Handle) anyDescendantAlive() bool {
	for _, target := range h.trackedTargets() {
		if processAlive(target.pid, target.startTime) {
			return true
		}
	}
	return false
}

func (h *Handle) waitDescendantsGone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !h.anyDescendantAlive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Handle) trackDescendants(rootPID int) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			// 根进程退出前再抓一次，减少漏掉“退出前刚 fork”的子孙。
			h.refreshDescendants(rootPID)
			return
		case <-ticker.C:
			h.refreshDescendants(rootPID)
		}
	}
}

func (h *Handle) refreshDescendants(rootPID int) {
	fresh := snapshotDescendants(rootPID)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.desc == nil {
		h.desc = make(map[int]descendant)
	}
	for _, item := range fresh {
		h.desc[item.pid] = item
	}
	for pid, item := range h.desc {
		if !processAlive(pid, item.startTime) {
			delete(h.desc, pid)
		}
	}
}

func (h *Handle) processLine(raw string) {
	plain := stripANSI(raw)
	if bootstrap := parseBootstrapURI(plain, h.port); bootstrap != nil {
		h.setBootstrap(bootstrap)
	}
	h.writeLogLine(h.redactor.redact(plain))
}

func (h *Handle) readOutput(reader *os.File) {
	defer close(h.readerDone)
	defer h.closeLog()
	defer reader.Close()

	const maxLineBytes = 256 * 1024
	buf := make([]byte, 0, 64*1024)
	buffered := bufio.NewReaderSize(reader, 64*1024)
	truncated := false
	for {
		chunk, err := buffered.ReadSlice('\n')
		if len(chunk) > 0 {
			if len(buf)+len(chunk) > maxLineBytes {
				if remaining := maxLineBytes - len(buf); remaining > 0 {
					buf = append(buf, chunk[:remaining]...)
				}
				truncated = true
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(buf) > 0 {
			line := strings.TrimRight(string(buf), "\r\n")
			if truncated {
				line += " …（输出过长已截断）"
			}
			h.processLine(line)
		}
		buf = buf[:0]
		truncated = false
		if err != nil {
			return
		}
	}
}

func (h *Handle) waitLoop(cmd *exec.Cmd) {
	waitErr := cmd.Wait()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	rootPID := cmd.Process.Pid

	h.mu.Lock()
	h.exited = true
	h.exitCode = exitCode
	h.bootstrap = nil
	if !h.stopped {
		h.code = StatusExited
	}
	h.mu.Unlock()
	close(h.done)
	h.probeClient.CloseIdleConnections()

	attrs := []any{"pid", rootPID, "exitCode", exitCode}
	if waitErr != nil {
		attrs = append(attrs, "error", waitErr.Error())
	}
	h.logger.Info("DSH 实例进程已退出", attrs...)
}

func (h *Handle) writeLogLine(line string) {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	if h.logFile == nil {
		return
	}
	_, _ = h.logFile.WriteString(line + "\n")
}

func (h *Handle) closeLog() {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	if h.logFile != nil {
		_ = h.logFile.Sync()
		_ = h.logFile.Close()
		h.logFile = nil
	}
}

func openInstanceLog(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建实例日志目录失败: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开实例日志失败: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("设置实例日志权限失败: %w", err)
	}
	return file, nil
}

func validateLaunchSpec(spec *LaunchSpec) error {
	if strings.TrimSpace(spec.InstanceID) == "" {
		return fmt.Errorf("%w: InstanceID 不能为空", ErrInvalidSpec)
	}
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return fmt.Errorf("%w: Command 不能为空", ErrInvalidSpec)
	}
	if !filepath.IsAbs(spec.Command[0]) {
		return fmt.Errorf("%w: Command[0] 必须是绝对路径（当前 %q）", ErrInvalidSpec, spec.Command[0])
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return fmt.Errorf("%w: 端口须在 1-65535 之间（当前 %d）", ErrInvalidSpec, spec.Port)
	}
	if strings.TrimSpace(spec.LogPath) == "" {
		return fmt.Errorf("%w: LogPath 不能为空", ErrInvalidSpec)
	}
	if spec.ReadyTimeout <= 0 {
		spec.ReadyTimeout = DefaultReadyTimeout
	}
	if spec.ReadyInterval <= 0 {
		spec.ReadyInterval = DefaultReadyInterval
	}
	if spec.Dir != "" {
		if err := os.MkdirAll(spec.Dir, 0o700); err != nil {
			return fmt.Errorf("创建工作目录失败: %w", err)
		}
	}
	return nil
}

// statusMessage 返回前端依赖的中文状态字符串（docs/DSH_INTEGRATION.md §5.6）。
func statusMessage(code string) string {
	switch code {
	case StatusStopped:
		return "未启动"
	case StatusStarting:
		return "启动中"
	case StatusRunning:
		return "运行中"
	case StatusStopping:
		return "停止中"
	case StatusNotReady:
		return "进程运行中，服务未就绪"
	case StatusPortBusy:
		return "端口被占用"
	case StatusExited:
		return "已停止"
	default:
		return "未知状态"
	}
}
