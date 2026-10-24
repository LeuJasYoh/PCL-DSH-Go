// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// verifyTimeout 是单条版本探测命令的超时上限。
const verifyTimeout = 15 * time.Second

// versionPattern 匹配 node -v（v24.22.1）与 pnpm -v（11.28.5）的输出。
var versionPattern = regexp.MustCompile("^v?(\\d+\\.\\d+\\.\\d+(?:[-+][0-9A-Za-z.-]+)?)$")

// Verify 依次执行 node -v 与 node <pnpm.cjs> -v（各 15 秒超时），解析版本写回
// NodeVersion/PnpmVersion；任一失败返回包含退出码与输出摘要的错误。
func (t *Tools) Verify(ctx context.Context) error {
	if t == nil {
		return errors.New("tools 为 nil，无法探测版本")
	}
	if t.NodePath == "" {
		return errors.New("未定位到 Node.js，无法探测版本")
	}
	if t.PnpmScript == "" {
		return errors.New("未定位到 pnpm.cjs，无法探测版本")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	output, err := runVersionCommand(ctx, "node -v", func(cmdCtx context.Context) *exec.Cmd {
		return t.NodeCommand(cmdCtx, "-v")
	})
	if err != nil {
		return err
	}
	nodeVersion, err := parseVersion(string(output))
	if err != nil {
		return fmt.Errorf("解析 Node.js 版本失败：%w", err)
	}
	t.NodeVersion = nodeVersion

	output, err = runVersionCommand(ctx, "node pnpm.cjs -v", func(cmdCtx context.Context) *exec.Cmd {
		return t.PnpmCommand(cmdCtx, "-v")
	})
	if err != nil {
		return err
	}
	pnpmVersion, err := parseVersion(string(output))
	if err != nil {
		return fmt.Errorf("解析 pnpm 版本失败：%w", err)
	}
	t.PnpmVersion = pnpmVersion
	return nil
}

// runVersionCommand 在 15 秒超时内执行版本探测命令并返回合并输出。
func runVersionCommand(ctx context.Context, label string, build func(context.Context) *exec.Cmd) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	output, err := build(cmdCtx).CombinedOutput()
	if err != nil {
		return nil, commandError(label, cmdCtx, err, output)
	}
	return output, nil
}

// commandError 把命令失败转换为包含原因与输出摘要的错误。
func commandError(label string, ctx context.Context, err error, output []byte) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("%s 执行超时（上限 %s）", label, verifyTimeout)
		}
		return fmt.Errorf("%s 已取消：%w", label, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%s 失败（退出码 %d）：%s", label, exitErr.ExitCode(), outputTail(output))
	}
	return fmt.Errorf("%s 启动失败：%w", label, err)
}

// outputTail 返回输出末尾最多 2000 字符，用于错误摘要。
func outputTail(output []byte) string {
	text := strings.TrimSpace(string(output))
	const maxTail = 2000
	if len(text) > maxTail {
		text = text[len(text)-maxTail:]
	}
	if text == "" {
		return "（无输出）"
	}
	return text
}

// parseVersion 从命令输出中解析版本号（取第一行，去掉可选的 v 前缀）。
func parseVersion(output string) (string, error) {
	line := strings.TrimSpace(output)
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = strings.TrimSpace(line[:index])
	}
	if line == "" {
		return "", errors.New("版本输出为空")
	}
	match := versionPattern.FindStringSubmatch(line)
	if match == nil {
		return "", fmt.Errorf("无法解析版本号：%q", line)
	}
	return match[1], nil
}
