//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// configureProcessGroup 让子进程拥有独立进程组（Setpgid），便于识别/停止自有进程树。
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

type procStat struct {
	state     byte
	startTime uint64
}

func readProcStat(pid int) (procStat, bool) {
	if pid <= 0 {
		return procStat{}, false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	return parseProcStat(string(data))
}

// parseProcStat 解析 /proc/<pid>/stat：第 1 字段 pid、第 2 字段 comm（可能含空格/括号，
// 所以用最后一个 ')' 定位）、第 3 字段 state、第 22 字段 starttime。
func parseProcStat(raw string) (procStat, bool) {
	end := strings.LastIndexByte(raw, ')')
	if end < 0 || end+2 >= len(raw) {
		return procStat{}, false
	}
	// fields[0] 是第 3 字段（state）；starttime 是第 22 字段 → fields[19]。
	fields := strings.Fields(raw[end+2:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return procStat{}, false
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, false
	}
	return procStat{state: fields[0][0], startTime: startTime}, true
}

// processAlive 用 PID + starttime 校验进程是否仍是我们记录的那个；僵尸/已消失视为退出。
func processAlive(pid int, startTime uint64) bool {
	if pid <= 0 || startTime == 0 {
		return false
	}
	stat, ok := readProcStat(pid)
	if !ok || stat.state == 'Z' || stat.state == 'X' {
		return false
	}
	return stat.startTime == startTime
}

func terminatePID(pid int, startTime uint64) error {
	if !processAlive(pid, startTime) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func killPID(pid int, startTime uint64) error {
	if !processAlive(pid, startTime) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func terminateProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func killProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// snapshotDescendants 快照 rootPID 的全部子孙（BFS；读取 /proc/<pid>/task/<tid>/children），
// 每个进程记录 starttime 防 PID 复用。最多 4096 个，避免异常进程树拖垮管理器。
func snapshotDescendants(rootPID int) []descendant {
	if rootPID <= 0 {
		return nil
	}
	type node struct {
		pid   int
		depth int
	}
	const maxDescendants = 4096
	seen := map[int]struct{}{rootPID: struct{}{}}
	queue := []node{{pid: rootPID, depth: 0}}
	result := make([]descendant, 0, 8)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, childPID := range childPIDs(current.pid) {
			if _, duplicate := seen[childPID]; duplicate {
				continue
			}
			seen[childPID] = struct{}{}
			startTime, ok := processStartTime(childPID)
			if !ok {
				continue
			}
			result = append(result, descendant{pid: childPID, startTime: startTime, depth: current.depth + 1})
			queue = append(queue, node{pid: childPID, depth: current.depth + 1})
			if len(result) >= maxDescendants {
				return result
			}
		}
	}
	return result
}

func processStartTime(pid int) (uint64, bool) {
	stat, ok := readProcStat(pid)
	if !ok {
		return 0, false
	}
	return stat.startTime, true
}

func childPIDs(pid int) []int {
	taskDir := "/proc/" + strconv.Itoa(pid) + "/task"
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return nil
	}
	seen := make(map[int]struct{})
	var children []int
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(taskDir, entry.Name(), "children"))
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(field)
			if err != nil || child <= 0 {
				continue
			}
			if _, duplicate := seen[child]; duplicate {
				continue
			}
			seen[child] = struct{}{}
			children = append(children, child)
		}
	}
	return children
}
