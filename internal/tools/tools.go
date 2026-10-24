// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools 定位内置 Node/pnpm 工具链、探测版本，并为子进程构造命令与环境。
// 接口与语义见 docs/M0_INTERFACES.md 第 4 节与 docs/DSH_INTEGRATION.md 1.1/1.2。
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Tools 描述定位到的 Node/pnpm 工具链；NodePath/PnpmScript 为绝对路径。
type Tools struct {
	NodePath    string
	PnpmScript  string
	NodeVersion string
	PnpmVersion string
	ToolRoot    string // 命中的工具根；空表示 node 来自 PATH 兜底
}

// nodeRelPaths、pnpmRelPaths 是相对 tools 根目录的候选路径（顺序即查找优先级）。
var (
	nodeRelPaths = []string{
		"node/bin/node",
		"node/bin/node.exe",
		"node/node",
		"node/node.exe",
	}
	pnpmRelPaths = []string{
		"pnpm/node_modules/pnpm/bin/pnpm.cjs",
		"pnpm/bin/pnpm.cjs",
		"pnpm/pnpm.cjs",
		"node/node_modules/pnpm/bin/pnpm.cjs",
	}
)

// pathCandidate 是一个待检查的工具路径；root 为空表示 PATH 兜底推导出的候选。
type pathCandidate struct {
	path string
	root string
}

// Locate 按 tool-root、data-root/tools、PATH（仅兜底）的顺序查找 Node 与 pnpm.cjs。
// 必须同时找到 node 与 pnpm，否则返回包含已尝试路径的错误。
func Locate(toolRoot, dataRoot string) (*Tools, error) {
	nodeCandidates, pnpmCandidates, err := toolCandidates(toolRoot, dataRoot)
	if err != nil {
		return nil, err
	}
	node, ok := firstExistingFile(nodeCandidates)
	if !ok {
		return nil, fmt.Errorf("未找到 Node.js 可执行文件；已尝试以下路径：%s\n请使用 --pcl.tool-root 指定包含 node/ 与 pnpm/ 的 tools 目录，或将 Node.js 放入 <data-root>/tools。", formatCandidates(nodeCandidates))
	}
	for _, candidate := range nodeDerivedPnpmCandidates(node.path) {
		pnpmCandidates = appendUniqueCandidate(pnpmCandidates, candidate)
	}
	pnpm, ok := firstExistingFile(pnpmCandidates)
	if !ok {
		return nil, fmt.Errorf("未找到 pnpm.cjs；已尝试以下路径：%s\n请使用 --pcl.tool-root 指定包含 pnpm/ 的 tools 目录（随包 pnpm 位于 tools/pnpm/node_modules/pnpm/bin/pnpm.cjs）。", formatCandidates(pnpmCandidates))
	}
	return &Tools{
		NodePath:   node.path,
		PnpmScript: pnpm.path,
		ToolRoot:   node.root,
	}, nil
}

// NodeCommand 返回执行 Node 的命令；调用方负责设置工作目录与环境。
func (t *Tools) NodeCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, t.NodePath, args...)
}

// PnpmCommand 返回用内置 Node 执行 pnpm.cjs 的命令（node <pnpm.cjs> args...）。
func (t *Tools) PnpmCommand(ctx context.Context, args ...string) *exec.Cmd {
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, t.PnpmScript)
	argv = append(argv, args...)
	return exec.CommandContext(ctx, t.NodePath, argv...)
}

// toolCandidates 按查找顺序生成 node 与 pnpm 候选路径。
func toolCandidates(toolRoot, dataRoot string) (node, pnpm []pathCandidate, err error) {
	var roots []pathCandidate
	if trimmed := strings.TrimSpace(toolRoot); trimmed != "" {
		abs, absErr := filepath.Abs(trimmed)
		if absErr != nil {
			return nil, nil, fmt.Errorf("解析 tool-root %q 失败：%w", toolRoot, absErr)
		}
		roots = append(roots, pathCandidate{path: abs, root: abs})
	}
	if trimmed := strings.TrimSpace(dataRoot); trimmed != "" {
		abs, absErr := filepath.Abs(trimmed)
		if absErr != nil {
			return nil, nil, fmt.Errorf("解析 data-root %q 失败：%w", dataRoot, absErr)
		}
		dir := filepath.Join(abs, "tools")
		roots = append(roots, pathCandidate{path: dir, root: dir})
	}
	for _, root := range roots {
		for _, rel := range nodeRelPaths {
			node = appendUniqueCandidate(node, pathCandidate{
				path: filepath.Join(root.path, filepath.FromSlash(rel)),
				root: root.root,
			})
		}
	}
	for _, root := range roots {
		for _, rel := range pnpmRelPaths {
			pnpm = appendUniqueCandidate(pnpm, pathCandidate{
				path: filepath.Join(root.path, filepath.FromSlash(rel)),
				root: root.root,
			})
		}
	}
	if found := findOnPath(nodeExecutableNames()); found != "" {
		node = appendUniqueCandidate(node, pathCandidate{path: found})
	}
	if found := findOnPath(pnpmExecutableNames()); found != "" {
		for _, candidate := range pnpmCandidatesFromPathExecutable(found) {
			pnpm = appendUniqueCandidate(pnpm, candidate)
		}
	}
	return node, pnpm, nil
}

// nodeDerivedPnpmCandidates 从已定位的 node 路径反推可能的 pnpm.cjs 位置
// （覆盖系统 Node 全局安装与内置 tools/node、tools/pnpm 布局）。
func nodeDerivedPnpmCandidates(nodePath string) []pathCandidate {
	nodeDir := filepath.Dir(nodePath)
	candidates := []pathCandidate{
		{path: filepath.Join(nodeDir, "node_modules", "pnpm", "bin", "pnpm.cjs")},
		{path: filepath.Join(nodeDir, "..", "lib", "node_modules", "pnpm", "bin", "pnpm.cjs")},
	}
	if filepath.Base(nodeDir) == "bin" {
		toolsRoot := filepath.Dir(filepath.Dir(nodeDir))
		candidates = append(candidates,
			pathCandidate{path: filepath.Join(toolsRoot, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")},
			pathCandidate{path: filepath.Join(toolsRoot, "pnpm", "bin", "pnpm.cjs")},
			pathCandidate{path: filepath.Join(toolsRoot, "pnpm", "pnpm.cjs")},
		)
	}
	return candidates
}

// pnpmCandidatesFromPathExecutable 把 PATH 上的 pnpm 命令解析为 pnpm.cjs 候选。
func pnpmCandidatesFromPathExecutable(found string) []pathCandidate {
	if strings.HasSuffix(strings.ToLower(found), ".cjs") {
		return []pathCandidate{{path: found}}
	}
	candidates := []pathCandidate{
		{path: filepath.Join(filepath.Dir(found), "node_modules", "pnpm", "bin", "pnpm.cjs")},
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil && strings.HasSuffix(strings.ToLower(resolved), ".cjs") {
		candidates = append(candidates, pathCandidate{path: resolved})
	}
	return candidates
}

// findOnPath 按 PATH 顺序查找第一个存在的普通文件。
func findOnPath(names []string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.Trim(strings.TrimSpace(dir), "\"")
		if dir == "" {
			continue
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if !isRegularFile(candidate) {
				continue
			}
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

// nodeExecutableNames、pnpmExecutableNames 返回当前平台在 PATH 上查找的可执行文件名。
func nodeExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"node.exe", "node"}
	}
	return []string{"node"}
}

func pnpmExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"pnpm.cmd", "pnpm.exe", "pnpm"}
	}
	return []string{"pnpm"}
}

// appendUniqueCandidate 按清理后的路径去重后追加候选。
func appendUniqueCandidate(list []pathCandidate, candidate pathCandidate) []pathCandidate {
	candidate.path = filepath.Clean(candidate.path)
	for _, existing := range list {
		if existing.path == candidate.path {
			return list
		}
	}
	return append(list, candidate)
}

// firstExistingFile 返回第一个存在的普通文件候选（绝对路径）。
func firstExistingFile(candidates []pathCandidate) (pathCandidate, bool) {
	for _, candidate := range candidates {
		if !isRegularFile(candidate.path) {
			continue
		}
		abs, err := filepath.Abs(candidate.path)
		if err != nil {
			continue
		}
		candidate.path = filepath.Clean(abs)
		return candidate, true
	}
	return pathCandidate{}, false
}

// isRegularFile 判断路径是否为存在的普通文件。
func isRegularFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}

// formatCandidates 把候选路径格式化为错误信息中的列表。
func formatCandidates(candidates []pathCandidate) string {
	if len(candidates) == 0 {
		return "\n  （无候选路径）"
	}
	var builder strings.Builder
	for _, candidate := range candidates {
		builder.WriteString("\n  - ")
		builder.WriteString(candidate.path)
	}
	return builder.String()
}
