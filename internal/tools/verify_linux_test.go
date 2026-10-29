// SPDX-License-Identifier: MIT

//go:build linux

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVerifyWithFakeShellScripts 在 Linux 下用真实子进程验证版本解析流程。
func TestVerifyWithFakeShellScripts(t *testing.T) {
	dir := t.TempDir()
	nodeScript := filepath.Join(dir, "node")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-v\" ]; then echo v24.22.1; exit 0; fi\n" +
		"if [ \"$2\" = \"-v\" ]; then echo 11.28.5; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(nodeScript, []byte(script), 0o755); err != nil {
		t.Fatalf("写入 fake node 失败：%v", err)
	}
	pnpmScript := filepath.Join(dir, "pnpm.cjs")
	if err := os.WriteFile(pnpmScript, []byte("// fake pnpm\n"), 0o644); err != nil {
		t.Fatalf("写入 fake pnpm.cjs 失败：%v", err)
	}
	tl := &Tools{NodePath: nodeScript, PnpmScript: pnpmScript}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tl.Verify(ctx); err != nil {
		t.Fatalf("Verify 失败：%v", err)
	}
	if tl.NodeVersion != "24.22.1" {
		t.Errorf("NodeVersion = %q, 期望 24.22.1", tl.NodeVersion)
	}
	if tl.PnpmVersion != "11.28.5" {
		t.Errorf("PnpmVersion = %q, 期望 11.28.5", tl.PnpmVersion)
	}
}

// TestVerifyRealToolchain 在 WSL/Linux 上对本机真实 Node/pnpm 执行版本探测。
func TestVerifyRealToolchain(t *testing.T) {
	var (
		tl  *Tools
		err error
	)
	for _, root := range []string{"/opt/pcl-dsh-go/tools"} {
		if stat, statErr := os.Stat(root); statErr != nil || !stat.IsDir() {
			continue
		}
		tl, err = Locate(root, t.TempDir())
		if err == nil {
			break
		}
	}
	if tl == nil {
		tl, err = Locate("", t.TempDir())
	}
	if err != nil {
		t.Skipf("当前 Linux 环境未提供可定位的 node/pnpm：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := tl.Verify(ctx); err != nil {
		t.Fatalf("真实工具链 Verify 失败：%v", err)
	}
	if tl.NodeVersion == "" || tl.PnpmVersion == "" {
		t.Fatalf("版本为空：node=%q pnpm=%q", tl.NodeVersion, tl.PnpmVersion)
	}
	t.Logf("真实工具链：node=%s path=%s；pnpm=%s path=%s；toolRoot=%q",
		tl.NodeVersion, tl.NodePath, tl.PnpmVersion, tl.PnpmScript, tl.ToolRoot)
}

// TestLocatePathFallbackRealEnvironment 验证 PATH 兜底能推导出发布包 tools 布局中的 pnpm。
func TestLocatePathFallbackRealEnvironment(t *testing.T) {
	tl, err := Locate("", t.TempDir())
	if err != nil {
		t.Skipf("当前 Linux 环境 PATH 中没有 node/pnpm：%v", err)
	}
	if tl.ToolRoot != "" {
		t.Errorf("PATH 兜底时 ToolRoot 应为空，实际 %q", tl.ToolRoot)
	}
	if !strings.HasSuffix(tl.PnpmScript, ".cjs") {
		t.Errorf("PnpmScript = %q, 期望 pnpm.cjs", tl.PnpmScript)
	}
	if !filepath.IsAbs(tl.NodePath) || !filepath.IsAbs(tl.PnpmScript) {
		t.Errorf("路径必须是绝对路径：node=%q pnpm=%q", tl.NodePath, tl.PnpmScript)
	}
}
