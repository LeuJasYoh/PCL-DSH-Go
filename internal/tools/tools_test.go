// SPDX-License-Identifier: MIT

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile 创建父目录并写入测试文件。
func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("创建目录 %s 失败：%v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

// makeToolsTree 在 root 下创建标准的 node 与 pnpm 布局，返回 node 与 pnpm.cjs 路径。
func makeToolsTree(t *testing.T, root string) (nodePath, pnpmPath string) {
	t.Helper()
	nodePath = filepath.Join(root, "node", "bin", "node")
	pnpmPath = filepath.Join(root, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")
	writeTestFile(t, nodePath)
	writeTestFile(t, pnpmPath)
	return nodePath, pnpmPath
}

// isolatePath 把 PATH 指向空目录，避免测试机器上的真实 node/pnpm 影响定位结果。
func isolatePath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-path"))
}

func TestLocateFromToolRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools")
	nodePath, pnpmPath := makeToolsTree(t, root)
	tl, err := Locate(root, "")
	if err != nil {
		t.Fatalf("Locate 失败：%v", err)
	}
	if tl.NodePath != nodePath {
		t.Errorf("NodePath = %q, 期望 %q", tl.NodePath, nodePath)
	}
	if tl.PnpmScript != pnpmPath {
		t.Errorf("PnpmScript = %q, 期望 %q", tl.PnpmScript, pnpmPath)
	}
	if tl.ToolRoot != filepath.Clean(root) {
		t.Errorf("ToolRoot = %q, 期望 %q", tl.ToolRoot, root)
	}
}

func TestLocateFromDataRoot(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "tools")
	nodePath, pnpmPath := makeToolsTree(t, root)
	tl, err := Locate("", dataRoot)
	if err != nil {
		t.Fatalf("Locate 失败：%v", err)
	}
	if tl.NodePath != nodePath || tl.PnpmScript != pnpmPath {
		t.Errorf("路径不符：node=%q pnpm=%q", tl.NodePath, tl.PnpmScript)
	}
	if tl.ToolRoot != filepath.Clean(root) {
		t.Errorf("ToolRoot = %q, 期望 %q", tl.ToolRoot, root)
	}
}

func TestLocateToolRootPrecedence(t *testing.T) {
	toolRoot := filepath.Join(t.TempDir(), "release-tools")
	toolNode, toolPnpm := makeToolsTree(t, toolRoot)
	dataRoot := t.TempDir()
	dataTools := filepath.Join(dataRoot, "tools")
	makeToolsTree(t, dataTools)
	tl, err := Locate(toolRoot, dataRoot)
	if err != nil {
		t.Fatalf("Locate 失败：%v", err)
	}
	if tl.NodePath != toolNode || tl.PnpmScript != toolPnpm {
		t.Errorf("未优先使用 tool-root：node=%q pnpm=%q", tl.NodePath, tl.PnpmScript)
	}
}

func TestLocateMissingNode(t *testing.T) {
	isolatePath(t)
	toolRoot := filepath.Join(t.TempDir(), "tools")
	_, err := Locate(toolRoot, t.TempDir())
	if err == nil {
		t.Fatal("缺少 node 时应报错")
	}
	if !strings.Contains(err.Error(), "未找到 Node.js") {
		t.Errorf("错误 %q 未说明缺少 Node.js", err.Error())
	}
	want := filepath.Join(toolRoot, "node", "bin", "node")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("错误 %q 未列出尝试路径 %q", err.Error(), want)
	}
	if !strings.Contains(err.Error(), "--pcl.tool-root") {
		t.Errorf("错误 %q 未给出修复提示", err.Error())
	}
}

func TestLocateMissingPnpm(t *testing.T) {
	isolatePath(t)
	toolRoot := filepath.Join(t.TempDir(), "tools")
	writeTestFile(t, filepath.Join(toolRoot, "node", "bin", "node"))
	_, err := Locate(toolRoot, "")
	if err == nil {
		t.Fatal("缺少 pnpm 时应报错")
	}
	if !strings.Contains(err.Error(), "未找到 pnpm.cjs") {
		t.Errorf("错误 %q 未说明缺少 pnpm", err.Error())
	}
	want := filepath.Join(toolRoot, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("错误 %q 未列出尝试路径 %q", err.Error(), want)
	}
}

func TestLocateFromPATHFallback(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	writeTestFile(t, filepath.Join(binDir, "node"))
	pnpmPath := filepath.Join(binDir, "node_modules", "pnpm", "bin", "pnpm.cjs")
	writeTestFile(t, pnpmPath)
	writeTestFile(t, filepath.Join(binDir, "pnpm"))
	t.Setenv("PATH", binDir)

	tl, err := Locate("", "")
	if err != nil {
		t.Fatalf("PATH 兜底 Locate 失败：%v", err)
	}
	if tl.NodePath != filepath.Join(binDir, "node") {
		t.Errorf("NodePath = %q, 期望 PATH 中的 node", tl.NodePath)
	}
	if tl.PnpmScript != pnpmPath {
		t.Errorf("PnpmScript = %q, 期望 %q", tl.PnpmScript, pnpmPath)
	}
	if tl.ToolRoot != "" {
		t.Errorf("PATH 兜底时 ToolRoot 应为空，实际 %q", tl.ToolRoot)
	}
}

func TestLocateDerivesPnpmFromPATHNodeLayout(t *testing.T) {
	// 模拟发布包布局：PATH 只含 tools/node/bin，pnpm 位于 tools/pnpm。
	toolsRoot := filepath.Join(t.TempDir(), "tools")
	nodePath := filepath.Join(toolsRoot, "node", "bin", "node")
	pnpmPath := filepath.Join(toolsRoot, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")
	writeTestFile(t, nodePath)
	writeTestFile(t, pnpmPath)
	t.Setenv("PATH", filepath.Join(toolsRoot, "node", "bin"))

	tl, err := Locate("", "")
	if err != nil {
		t.Fatalf("Locate 失败：%v", err)
	}
	if tl.NodePath != nodePath {
		t.Errorf("NodePath = %q, 期望 %q", tl.NodePath, nodePath)
	}
	if tl.PnpmScript != pnpmPath {
		t.Errorf("PnpmScript = %q, 期望 %q", tl.PnpmScript, pnpmPath)
	}
}

func TestLocateSkipsDirectories(t *testing.T) {
	isolatePath(t)
	toolRoot := filepath.Join(t.TempDir(), "tools")
	if err := os.MkdirAll(filepath.Join(toolRoot, "node", "bin", "node"), 0o700); err != nil {
		t.Fatalf("创建同名目录失败：%v", err)
	}
	_, err := Locate(toolRoot, "")
	if err == nil || !strings.Contains(err.Error(), "未找到 Node.js") {
		t.Fatalf("同名目录不应被当作 Node：%v", err)
	}
}

func TestNodeCommandAndPnpmCommand(t *testing.T) {
	dir := t.TempDir()
	nodePath := filepath.Join(dir, "node")
	pnpmScript := filepath.Join(dir, "pnpm.cjs")
	tl := &Tools{NodePath: nodePath, PnpmScript: pnpmScript}

	nodeCmd := tl.NodeCommand(t.Context(), "-v")
	if nodeCmd.Path != nodePath {
		t.Errorf("NodeCommand.Path = %q, 期望 %q", nodeCmd.Path, nodePath)
	}
	if len(nodeCmd.Args) != 2 || nodeCmd.Args[1] != "-v" {
		t.Errorf("NodeCommand.Args = %v", nodeCmd.Args)
	}

	pnpmCmd := tl.PnpmCommand(t.Context(), "install", "--prod")
	wantArgs := []string{nodePath, pnpmScript, "install", "--prod"}
	if len(pnpmCmd.Args) != len(wantArgs) {
		t.Fatalf("PnpmCommand.Args = %v, 期望 %v", pnpmCmd.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if pnpmCmd.Args[i] != want {
			t.Errorf("PnpmCommand.Args[%d] = %q, 期望 %q", i, pnpmCmd.Args[i], want)
		}
	}
}
