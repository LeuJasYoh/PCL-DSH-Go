// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

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
	// maxCommandOutputBytes bounds the retained CLI output for error summaries.
	maxCommandOutputBytes = 2 << 20
	// commandWaitDelay bounds Wait after a cancelled process before its pipes
	// are closed.
	commandWaitDelay = 5 * time.Second
	// maxPendingLineBytes flushes an unterminated log line before it grows
	// without bound.
	maxPendingLineBytes = 64 << 10
)

// commandSpec describes one bounded plugin CLI invocation.
type commandSpec struct {
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
	OnLine  func(string)
}

// commandResult is the outcome of one plugin CLI invocation.
type commandResult struct {
	Output   string
	ExitCode int
}

// runCommand runs the plugin CLI with merged output streaming, a hard timeout
// and process-group termination on cancellation.
func runCommand(ctx context.Context, spec commandSpec) (commandResult, error) {
	if len(spec.Args) == 0 || spec.Args[0] == "" {
		return commandResult{}, fmt.Errorf("插件命令为空")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = mutateTimeout
	}
	if err := ctx.Err(); err != nil {
		return commandResult{}, err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	command := exec.CommandContext(timeoutCtx, spec.Args[0], spec.Args[1:]...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.WaitDelay = commandWaitDelay
	configurePluginProcessGroup(command)
	command.Cancel = func() error { return terminatePluginCommandTree(command) }

	output := &pluginLineCollector{maxBytes: maxCommandOutputBytes, onLine: spec.OnLine}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return commandResult{}, fmt.Errorf("无法启动插件命令 %s：%w", filepath.Base(spec.Args[0]), err)
	}
	waitErr := command.Wait()
	output.finish()
	result := commandResult{ExitCode: -1, Output: output.String()}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	switch {
	case errors.Is(timeoutCtx.Err(), context.DeadlineExceeded):
		return result, fmt.Errorf("插件命令超过 %s 未完成，已停止本次创建的进程", spec.Timeout)
	case ctx.Err() != nil:
		return result, ctx.Err()
	case waitErr != nil:
		return result, fmt.Errorf("插件命令退出码 %d", result.ExitCode)
	case result.ExitCode != 0:
		return result, fmt.Errorf("插件命令退出码 %d", result.ExitCode)
	}
	return result, nil
}

// pluginLineCollector captures bounded merged output and forwards complete lines.
type pluginLineCollector struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maxBytes  int
	truncated bool
	pending   []byte
	onLine    func(string)
}

func (c *pluginLineCollector) Write(data []byte) (int, error) {
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

func (c *pluginLineCollector) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onLine != nil && len(c.pending) > 0 {
		line := string(c.pending)
		c.pending = nil
		c.onLine(line)
	}
}

func (c *pluginLineCollector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	text := c.buffer.String()
	if c.truncated {
		text += "\n[输出过长，已截断]"
	}
	return text
}

// tailText returns the last max bytes of text.
func tailText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[len(text)-max:]
}
