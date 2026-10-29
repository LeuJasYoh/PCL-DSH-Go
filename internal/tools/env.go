// SPDX-License-Identifier: MIT

package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// Env 在调用方给出的基准环境 base 上设置 PATH（node bin、pnpm home、原 PATH）与 PNPM_HOME，
// 其余变量按 base 原样保留。调用方应传入环境白名单，本方法不会读取当前进程的额外变量。
func (t *Tools) Env(base []string) []string {
	nodeBin := ""
	if t.NodePath != "" {
		nodeBin = filepath.Dir(t.NodePath)
	}
	pnpmHome := t.pnpmHome()
	basePath := envValue(base, "PATH")

	parts := make([]string, 0, 3)
	if nodeBin != "" && nodeBin != "." {
		parts = append(parts, nodeBin)
	}
	if pnpmHome != "" {
		parts = append(parts, pnpmHome)
	}
	if basePath != "" {
		parts = append(parts, basePath)
	}
	newPath := strings.Join(parts, string(os.PathListSeparator))

	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(key, "PATH") || strings.EqualFold(key, "PNPM_HOME") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+newPath)
	if pnpmHome != "" {
		out = append(out, "PNPM_HOME="+pnpmHome)
	}
	return out
}

// pnpmHome 计算 PNPM_HOME：命中内置 tools 根时使用 <tools>/pnpm（与发布包布局一致），
// 否则退回 pnpm 包目录。
func (t *Tools) pnpmHome() string {
	if t.ToolRoot != "" {
		candidate := filepath.Join(t.ToolRoot, "pnpm")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
	}
	if t.PnpmScript == "" {
		return ""
	}
	dir := filepath.Dir(t.PnpmScript)
	if filepath.Base(dir) == "bin" {
		return filepath.Dir(dir)
	}
	return dir
}

// envValue 读取环境切片中指定键的值（键名比较不区分大小写，取最后出现的值）。
func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
