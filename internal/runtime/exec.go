// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// defaultCommandTimeout bounds helper commands that do not pass a timeout.
	defaultCommandTimeout = 20 * time.Minute
	// maxCommandOutputBytes is the amount of child output kept in memory for
	// diagnostics; live lines are still forwarded to the caller.
	maxCommandOutputBytes = 2 << 20
	// commandWaitDelay bounds how long Wait blocks after a cancelled process
	// before its I/O pipes are closed.
	commandWaitDelay = 5 * time.Second
	// maxPendingLineBytes flushes an unterminated line to the log callback
	// before it can grow without bound.
	maxPendingLineBytes = 64 << 10
)

// commandSpec describes one bounded child-process invocation used by installs
// and capability probing.
type commandSpec struct {
	// Args is the full argv; Args[0] is an absolute executable path.
	Args []string
	// Dir is the working directory (empty means the current directory).
	Dir string
	// Env is the complete environment; nothing is inherited by the child.
	Env []string
	// Timeout bounds the whole process; zero uses defaultCommandTimeout.
	Timeout time.Duration
	// OnLine receives merged stdout/stderr lines as they arrive.
	OnLine func(string)
	// MaxOutput bounds the retained output buffer; zero uses the default.
	MaxOutput int
}

// commandResult is the outcome of a commandSpec invocation.
type commandResult struct {
	// ExitCode is the process exit code (-1 when unknown).
	ExitCode int
	// Output is the retained (possibly tail-truncated) merged output.
	Output string
	// TimedOut reports that the timeout fired and the process tree was killed.
	TimedOut bool
}

// runCommand starts one child process, streams its merged output line by line
// and enforces a hard timeout by killing the whole process group.
func runCommand(ctx context.Context, spec commandSpec) (commandResult, error) {
	if len(spec.Args) == 0 || spec.Args[0] == "" {
		return commandResult{}, fmt.Errorf("命令为空")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = defaultCommandTimeout
	}
	if spec.MaxOutput <= 0 {
		spec.MaxOutput = maxCommandOutputBytes
	}
	if err := ctx.Err(); err != nil {
		return commandResult{}, fmt.Errorf("操作已取消：%w", err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	command := exec.CommandContext(timeoutCtx, spec.Args[0], spec.Args[1:]...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.WaitDelay = commandWaitDelay
	configureCommandProcessGroup(command)
	command.Cancel = func() error { return terminateCommandTree(command) }

	output := &lineCollector{maxBytes: spec.MaxOutput, onLine: spec.OnLine}
	command.Stdout = output
	command.Stderr = output

	if err := command.Start(); err != nil {
		return commandResult{}, fmt.Errorf("无法启动 %s：%w", filepath.Base(spec.Args[0]), err)
	}
	waitErr := command.Wait()
	output.finish()
	result := commandResult{ExitCode: -1, Output: output.String()}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	switch {
	case errors.Is(timeoutCtx.Err(), context.DeadlineExceeded):
		result.TimedOut = true
		return result, fmt.Errorf("命令超过 %s 未完成，已停止本次创建的进程", spec.Timeout)
	case ctx.Err() != nil:
		return result, fmt.Errorf("操作已取消：%w", ctx.Err())
	case waitErr != nil:
		return result, fmt.Errorf("命令退出码 %d：%s", result.ExitCode, tailText(result.Output, 4000))
	case result.ExitCode != 0:
		return result, fmt.Errorf("命令退出码 %d：%s", result.ExitCode, tailText(result.Output, 4000))
	}
	return result, nil
}

// lineCollector captures bounded merged output and forwards complete lines.
// os/exec writes stdout and stderr from different goroutines, so all state is
// mutex protected.
type lineCollector struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maxBytes  int
	truncated bool
	pending   []byte
	onLine    func(string)
}

func (c *lineCollector) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remaining := c.maxBytes - c.buffer.Len(); remaining > 0 {
		if len(data) <= remaining {
			c.buffer.Write(data)
		} else {
			c.buffer.Write(data[:remaining])
			c.truncated = true
		}
	} else {
		c.truncated = true
	}
	if c.onLine == nil {
		return len(data), nil
	}
	c.pending = append(c.pending, data...)
	for {
		index := bytes.IndexByte(c.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(string(c.pending[:index]), "\r")
		c.pending = c.pending[index+1:]
		c.onLine(line)
	}
	if len(c.pending) >= maxPendingLineBytes {
		line := string(c.pending)
		c.pending = nil
		c.onLine(line)
	}
	return len(data), nil
}

// finish flushes a trailing unterminated line and marks the collector complete.
func (c *lineCollector) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onLine != nil && len(c.pending) > 0 {
		line := string(c.pending)
		c.pending = nil
		c.onLine(line)
	}
}

// String returns the retained output followed by a truncation marker.
func (c *lineCollector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	text := c.buffer.String()
	if c.truncated {
		text += "\n[输出过长，已截断]"
	}
	return text
}

// tailText returns the last max bytes of text (used in error messages).
func tailText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[len(text)-max:]
}
