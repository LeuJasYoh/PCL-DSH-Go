// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load 默认参数失败：%v", err)
	}
	if cfg.ServerAddress != DefaultServerAddress {
		t.Errorf("ServerAddress = %q, 期望 %q", cfg.ServerAddress, DefaultServerAddress)
	}
	if cfg.ServerPort != DefaultServerPort {
		t.Errorf("ServerPort = %d, 期望 %d", cfg.ServerPort, DefaultServerPort)
	}
	if !filepath.IsAbs(cfg.DataRoot) || filepath.Base(cfg.DataRoot) != "data" {
		t.Errorf("DataRoot = %q, 期望绝对路径且以 data 结尾", cfg.DataRoot)
	}
	if cfg.ToolRoot != "" {
		t.Errorf("ToolRoot = %q, 期望空", cfg.ToolRoot)
	}
	if cfg.DevMode {
		t.Error("DevMode 默认应为 false")
	}
	if cfg.Version != "" {
		t.Errorf("Version = %q, Load 不应设置 Version", cfg.Version)
	}
}

func TestLoadEnvDataRootFallback(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "env-data")
	cfg, err := Load(nil, func(key string) string {
		if key == EnvDataRoot {
			return envRoot
		}
		return "ignored"
	})
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if cfg.DataRoot != filepath.Clean(envRoot) {
		t.Errorf("DataRoot = %q, 期望环境变量值 %q", cfg.DataRoot, envRoot)
	}
}

func TestLoadBlankEnvFallsBackToDefault(t *testing.T) {
	cfg, err := Load(nil, func(string) string { return "   " })
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if filepath.Base(cfg.DataRoot) != "data" {
		t.Errorf("DataRoot = %q, 期望回退到 ./data", cfg.DataRoot)
	}
}

func TestLoadFlagsOverrideEnv(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "env-data")
	flagRoot := filepath.Join(t.TempDir(), "flag-data")
	toolDir := filepath.Join(t.TempDir(), "tools")
	cfg, err := Load([]string{
		"--server.address=0.0.0.0",
		"--server.port=18080",
		"--pcl.data-root", flagRoot,
		"--pcl.tool-root", toolDir,
		"--pcl.dev-mode",
	}, func(string) string { return envRoot })
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if cfg.ServerAddress != "0.0.0.0" {
		t.Errorf("ServerAddress = %q", cfg.ServerAddress)
	}
	if cfg.ServerPort != 18080 {
		t.Errorf("ServerPort = %d", cfg.ServerPort)
	}
	if cfg.DataRoot != filepath.Clean(flagRoot) {
		t.Errorf("DataRoot = %q, 期望 %q", cfg.DataRoot, flagRoot)
	}
	if cfg.ToolRoot != filepath.Clean(toolDir) {
		t.Errorf("ToolRoot = %q, 期望 %q", cfg.ToolRoot, toolDir)
	}
	if !cfg.DevMode {
		t.Error("DevMode 应为 true")
	}
}

func TestLoadInvalidArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "端口为 0", args: []string{"--server.port=0"}, want: "1-65535"},
		{name: "端口过大", args: []string{"--server.port=65536"}, want: "1-65535"},
		{name: "端口非数字", args: []string{"--server.port=abc"}, want: "invalid value"},
		{name: "地址为空", args: []string{"--server.address="}, want: "server.address"},
		{name: "地址含空白", args: []string{"--server.address= 127.0.0.1"}, want: "空白"},
		{name: "data-root 显式空", args: []string{"--pcl.data-root="}, want: "data-root 不能为空"},
		{name: "未知参数", args: []string{"--nope"}, want: "flag provided but not defined"},
		{name: "位置参数", args: []string{"serve"}, want: "位置参数"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args, func(string) string { return "" })
			if err == nil {
				t.Fatalf("Load(%v) 期望报错", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误 %q 不包含 %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		if _, err := Load([]string{arg}, func(string) string { return "" }); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("Load(%q) 错误 = %v, 期望 flag.ErrHelp", arg, err)
		}
	}
}

func TestLoadVersion(t *testing.T) {
	for _, arg := range []string{"--version", "-version"} {
		cfg, err := Load([]string{arg}, func(string) string { return "" })
		if err != nil {
			t.Fatalf("Load(%q) 失败：%v", arg, err)
		}
		if !cfg.ShowVersion {
			t.Errorf("Load(%q).ShowVersion = false, 期望 true", arg)
		}
	}
}

func TestLoadNilGetenv(t *testing.T) {
	cfg, err := Load(nil, nil)
	if err != nil {
		t.Fatalf("Load(nil getenv) 失败：%v", err)
	}
	if cfg.DataRoot == "" {
		t.Error("DataRoot 不应为空")
	}
}

func TestUsageTextContainsFlags(t *testing.T) {
	text := UsageText()
	for _, want := range []string{"--server.address", "--server.port", "--pcl.data-root", "--pcl.tool-root", "--server.static-dir", "--pcl.dev-mode", "--version", EnvDataRoot} {
		if !strings.Contains(text, want) {
			t.Errorf("UsageText 缺少 %q", want)
		}
	}
}
func TestLoadStaticDir(t *testing.T) {
	// Default: not configured.
	cfg, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load 默认参数失败：%v", err)
	}
	if cfg.StaticDir != "" {
		t.Errorf("StaticDir = %q, 期望空", cfg.StaticDir)
	}

	// Absolute paths are kept (cleaned).
	absolute := t.TempDir()
	cfg, err = Load([]string{"--server.static-dir", absolute}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if cfg.StaticDir != filepath.Clean(absolute) {
		t.Errorf("StaticDir = %q, 期望 %q", cfg.StaticDir, absolute)
	}

	// Relative paths are resolved against the working directory.
	cfg, err = Load([]string{"--server.static-dir", "web/dist"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load 相对路径失败：%v", err)
	}
	if !filepath.IsAbs(cfg.StaticDir) || filepath.Base(cfg.StaticDir) != "dist" {
		t.Errorf("StaticDir = %q, 期望绝对路径且以 dist 结尾", cfg.StaticDir)
	}

	// An explicit empty value keeps the placeholder page (no error).
	cfg, err = Load([]string{"--server.static-dir="}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load 空 static-dir 失败：%v", err)
	}
	if cfg.StaticDir != "" {
		t.Errorf("StaticDir = %q, 期望空", cfg.StaticDir)
	}
}
