// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countEnvKey 统计环境切片中某个键出现的次数（键名不区分大小写）。
func countEnvKey(env []string, key string) int {
	count := 0
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, key) {
			count++
		}
	}
	return count
}

func TestEnvSetsPathAndPnpmHome(t *testing.T) {
	root := t.TempDir()
	nodePath := filepath.Join(root, "node", "bin", "node")
	pnpmScript := filepath.Join(root, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")
	if err := os.MkdirAll(filepath.Join(root, "pnpm"), 0o700); err != nil {
		t.Fatalf("创建 pnpm 目录失败：%v", err)
	}
	tl := &Tools{NodePath: nodePath, PnpmScript: pnpmScript, ToolRoot: root}

	env := tl.Env([]string{"PATH=/usr/bin:/bin", "FOO=bar", "HOME=/home/test"})
	wantPnpmHome := filepath.Join(root, "pnpm")
	wantPath := filepath.Join(root, "node", "bin") + string(os.PathListSeparator) +
		wantPnpmHome + string(os.PathListSeparator) + "/usr/bin:/bin"
	if got := envValue(env, "PATH"); got != wantPath {
		t.Errorf("PATH = %q, 期望 %q", got, wantPath)
	}
	if got := envValue(env, "PNPM_HOME"); got != wantPnpmHome {
		t.Errorf("PNPM_HOME = %q, 期望 %q", got, wantPnpmHome)
	}
	if got := envValue(env, "FOO"); got != "bar" {
		t.Errorf("FOO = %q，期望保留 base 变量", got)
	}
	if got := envValue(env, "HOME"); got != "/home/test" {
		t.Errorf("HOME = %q，期望保留 base 变量", got)
	}
	if countEnvKey(env, "PATH") != 1 {
		t.Errorf("PATH 出现 %d 次, 期望 1", countEnvKey(env, "PATH"))
	}
	if countEnvKey(env, "PNPM_HOME") != 1 {
		t.Errorf("PNPM_HOME 出现 %d 次, 期望 1", countEnvKey(env, "PNPM_HOME"))
	}
}

func TestEnvWithoutToolRootUsesPnpmPackageDir(t *testing.T) {
	root := t.TempDir()
	nodePath := filepath.Join(root, "node", "bin", "node")
	pnpmScript := filepath.Join(root, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs")
	tl := &Tools{NodePath: nodePath, PnpmScript: pnpmScript}
	env := tl.Env(nil)
	wantPnpmHome := filepath.Join(root, "pnpm", "node_modules", "pnpm")
	if got := envValue(env, "PNPM_HOME"); got != wantPnpmHome {
		t.Errorf("PNPM_HOME = %q, 期望 %q", got, wantPnpmHome)
	}
	wantPath := filepath.Join(root, "node", "bin") + string(os.PathListSeparator) + wantPnpmHome
	if got := envValue(env, "PATH"); got != wantPath {
		t.Errorf("PATH = %q, 期望 %q", got, wantPath)
	}
}

func TestEnvRemovesDuplicatePathEntries(t *testing.T) {
	root := t.TempDir()
	tl := &Tools{
		NodePath:   filepath.Join(root, "node", "bin", "node"),
		PnpmScript: filepath.Join(root, "pnpm", "node_modules", "pnpm", "bin", "pnpm.cjs"),
		ToolRoot:   root,
	}
	env := tl.Env([]string{"Path=C:\\Windows", "PATH=/usr/bin", "PNPM_HOME=/old"})
	if countEnvKey(env, "PATH") != 1 {
		t.Fatalf("PATH 条目数 = %d, 期望 1", countEnvKey(env, "PATH"))
	}
	if countEnvKey(env, "PNPM_HOME") != 1 {
		t.Fatalf("PNPM_HOME 条目数 = %d, 期望 1", countEnvKey(env, "PNPM_HOME"))
	}
	wantPrefix := filepath.Join(root, "node", "bin") + string(os.PathListSeparator)
	if got := envValue(env, "PATH"); !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("PATH = %q, 期望以 %q 开头", got, wantPrefix)
	}
}

func TestEnvEmptyTools(t *testing.T) {
	tl := &Tools{}
	env := tl.Env([]string{"PATH=/bin"})
	if got := envValue(env, "PATH"); got != "/bin" {
		t.Errorf("PATH = %q, 期望保留 base PATH", got)
	}
	if got := envValue(env, "PNPM_HOME"); got != "" {
		t.Errorf("PNPM_HOME = %q, 期望为空", got)
	}
}
