// SPDX-License-Identifier: GPL-3.0-or-later

// Package config 提供管理器启动配置解析、data-root 目录初始化与单实例文件锁。
// 接口与语义见 docs/M0_INTERFACES.md 第 2 节。
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 默认值与约定，与 docs/M0_INTERFACES.md、DEVELOPMENT_PLAN 8.3 保持一致。
const (
	// DefaultServerAddress 是默认监听地址：仅回环，避免直接暴露到公网。
	DefaultServerAddress = "127.0.0.1"
	// DefaultServerPort 是默认监听端口。
	DefaultServerPort = 8080
	// DefaultDataRoot 是未指定 data-root 时使用的目录。
	DefaultDataRoot = "./data"
	// EnvDataRoot 是 data-root 的环境变量兜底名。
	EnvDataRoot = "PCL_DSH_DATA"
)

// Config 是管理器启动配置。
// Version 由 main 通过 -ldflags "-X main.version=..." 注入后填入。
type Config struct {
	Version       string
	ServerAddress string
	ServerPort    int
	DataRoot      string
	ToolRoot      string
	// StaticDir is the optional absolute directory of the built WebUI
	// (docs/M4_INTERFACES.md 2). Empty keeps the built-in placeholder page.
	StaticDir string
	DevMode   bool
}

// Load 解析命令行参数（args 不含 argv[0]），并应用 PCL_DSH_DATA 兜底与基础校验。
// 参数解析错误返回 error；-h/--help 返回 flag.ErrHelp（调用方负责打印 UsageText）。
func Load(args []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	fs := flag.NewFlagSet("pcl-dsh-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // 由调用方统一打印错误，避免重复输出
	var (
		address   string
		port      int
		dataRoot  string
		toolRoot  string
		staticDir string
		devMode   bool
	)
	fs.StringVar(&address, "server.address", DefaultServerAddress, "HTTP 监听地址")
	fs.IntVar(&port, "server.port", DefaultServerPort, "HTTP 监听端口（1-65535）")
	fs.StringVar(&dataRoot, "pcl.data-root", "", "数据目录（默认 ./data，环境变量 PCL_DSH_DATA 兜底）")
	fs.StringVar(&toolRoot, "pcl.tool-root", "", "内置 Node/pnpm 的 tools 目录（空表示自动查找）")
	fs.StringVar(&staticDir, "server.static-dir", "", "WebUI 静态资源目录（构建产物 dist；空表示使用内置占位页）")
	fs.BoolVar(&devMode, "pcl.dev-mode", false, "开发模式（M0 调试 API 开关）")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("不支持的位置参数：%s", strings.Join(fs.Args(), " "))
	}
	if err := validateServerAddress(address); err != nil {
		return Config{}, err
	}
	if port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("--server.port 必须在 1-65535 之间（当前 %d）", port)
	}

	dataRootSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "pcl.data-root" {
			dataRootSet = true
		}
	})
	if dataRootSet {
		dataRoot = strings.TrimSpace(dataRoot)
		if dataRoot == "" {
			return Config{}, errors.New("--pcl.data-root 不能为空")
		}
	} else {
		dataRoot = strings.TrimSpace(getenv(EnvDataRoot))
		if dataRoot == "" {
			dataRoot = DefaultDataRoot
		}
	}
	dataRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return Config{}, fmt.Errorf("解析 data-root 绝对路径失败：%w", err)
	}

	toolRoot = strings.TrimSpace(toolRoot)
	if toolRoot != "" {
		toolRoot, err = filepath.Abs(toolRoot)
		if err != nil {
			return Config{}, fmt.Errorf("解析 tool-root 绝对路径失败：%w", err)
		}
	}

	// The static directory is optional. Relative values are resolved against
	// the process working directory; existence is validated at startup by the
	// server (docs/M4_INTERFACES.md 2).
	staticDir = strings.TrimSpace(staticDir)
	if staticDir != "" {
		staticDir, err = filepath.Abs(staticDir)
		if err != nil {
			return Config{}, fmt.Errorf("解析 --server.static-dir 绝对路径失败：%w", err)
		}
	}

	return Config{
		ServerAddress: address,
		ServerPort:    port,
		DataRoot:      dataRoot,
		ToolRoot:      toolRoot,
		StaticDir:     staticDir,
		DevMode:       devMode,
	}, nil
}

// validateServerAddress 拒绝空地址、首尾空白与控制字符，避免把非法值传给 net.Listen。
func validateServerAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("--server.address 不能为空")
	}
	if strings.TrimSpace(address) != address {
		return fmt.Errorf("--server.address 不能包含首尾空白：%q", address)
	}
	for _, r := range address {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("--server.address 包含非法字符：%q", address)
		}
	}
	return nil
}

// UsageText 返回命令行帮助文本；-h/--help 或参数错误时由 main 打印。
func UsageText() string {
	lines := []string{
		"pcl-dsh-go - PCL-DSH-Go DSH 管理器（M0 骨架）",
		"",
		"用法：",
		"  pcl-dsh-go [选项]",
		"",
		"选项：",
		"  --server.address string   HTTP 监听地址（默认 " + DefaultServerAddress + "）",
		"  --server.port int         HTTP 监听端口（默认 " + fmt.Sprintf("%d", DefaultServerPort) + "，范围 1-65535）",
		"  --pcl.data-root string    数据目录（默认 ./data；环境变量 " + EnvDataRoot + " 兜底）",
		"  --pcl.tool-root string    内置 Node/pnpm 的 tools 目录（空表示依次查找 tool-root、data-root/tools、PATH）",
		"  --server.static-dir string WebUI 静态资源目录（构建产物 dist；空表示使用内置占位页，相对路径按当前目录解析）",
		"  --pcl.dev-mode            M0 调试模式（默认 false）",
		"  -h, --help                显示本帮助",
		"",
	}
	return strings.Join(lines, "\n")
}
