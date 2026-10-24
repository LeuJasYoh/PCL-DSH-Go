// SPDX-License-Identifier: GPL-3.0-or-later

// Package logging 提供 slog JSON 日志（stdout 与按日文件）、审计日志与脱敏工具。
// 接口与语义见 docs/M0_INTERFACES.md 第 3 节。
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServiceName 是所有日志记录的固定 service 字段值。
const ServiceName = "pcl-dsh-go"

// EnvLogLevel 是日志级别环境变量名（debug/info/warn/error）。
const EnvLogLevel = "PCL_DSH_LOG_LEVEL"

// dateLayout 是日志文件按日切换使用的日期格式。
const dateLayout = "2006-01-02"

// Setup 初始化 JSON 日志：同时写 stdout 与 logsDir/manager-YYYY-MM-DD.log（按日切换）。
// 返回 logger、幂等的 Close 函数；级别默认 info，可用 PCL_DSH_LOG_LEVEL 覆盖。
// logger 固定携带 service=pcl-dsh-go 与 version 字段。
func Setup(logsDir string, version string) (*slog.Logger, func() error, error) {
	dir := strings.TrimSpace(logsDir)
	if dir == "" {
		return nil, nil, errors.New("日志目录不能为空")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("解析日志目录绝对路径失败：%w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("创建日志目录 %s 失败：%w", dir, err)
	}
	file, err := newDailyFile(dir, "manager", time.Now)
	if err != nil {
		return nil, nil, err
	}
	writer := &teeWriter{stdout: os.Stdout, file: file}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: logLevelFromEnv()})
	logger := slog.New(handler).With("service", ServiceName, "version", version)
	var (
		once     sync.Once
		closeErr error
	)
	closeFn := func() error {
		once.Do(func() {
			closeErr = file.Close()
		})
		return closeErr
	}
	return logger, closeFn, nil
}

// logLevelFromEnv 读取 PCL_DSH_LOG_LEVEL；空值或无法识别时回退 info。
func logLevelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvLogLevel))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// teeWriter 同时写日志文件与 stdout。
// 文件写入失败只在 stderr 提示，不阻断 stdout 输出（避免日志系统整体失效）。
type teeWriter struct {
	stdout io.Writer
	file   *dailyFile
}

// Write 实现 io.Writer。
func (w *teeWriter) Write(p []byte) (int, error) {
	if w.file != nil {
		if _, err := w.file.Write(p); err != nil {
			fmt.Fprintf(os.Stderr, "pcl-dsh-go: 写日志文件失败：%v\n", err)
		}
	}
	return w.stdout.Write(p)
}
