// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func listManagerLogs(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "manager-*.log"))
	if err != nil {
		t.Fatalf("Glob manager 日志失败：%v", err)
	}
	return files
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestSetupWritesJSONToFile(t *testing.T) {
	dir := t.TempDir()
	logger, closeFn, err := Setup(dir, "0.0.0-test")
	if err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	logger.Info("hello", "k", "v")
	if err := closeFn(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("重复 Close 失败：%v", err)
	}
	files := listManagerLogs(t, dir)
	if len(files) != 1 {
		t.Fatalf("manager 日志文件数 = %d, 期望 1（%v）", len(files), files)
	}
	lines := readLines(t, files[0])
	if len(lines) != 1 {
		t.Fatalf("日志行数 = %d, 期望 1", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("日志不是合法 JSON：%v（%s）", err, lines[0])
	}
	if record["msg"] != "hello" {
		t.Errorf("msg = %v", record["msg"])
	}
	if record["level"] != "INFO" {
		t.Errorf("level = %v", record["level"])
	}
	if record["service"] != ServiceName {
		t.Errorf("service = %v", record["service"])
	}
	if record["version"] != "0.0.0-test" {
		t.Errorf("version = %v", record["version"])
	}
	if record["k"] != "v" {
		t.Errorf("k = %v", record["k"])
	}
}

func TestSetupLevelFromEnv(t *testing.T) {
	t.Setenv(EnvLogLevel, "warn")
	dir := t.TempDir()
	logger, closeFn, err := Setup(dir, "v")
	if err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	logger.Debug("debug-line")
	logger.Info("info-line")
	logger.Warn("warn-line")
	if err := closeFn(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	files := listManagerLogs(t, dir)
	if len(files) != 1 {
		t.Fatalf("manager 日志文件数 = %d, 期望 1", len(files))
	}
	lines := readLines(t, files[0])
	if len(lines) != 1 {
		t.Fatalf("warn 级别下日志行数 = %d, 期望 1（%v）", len(lines), lines)
	}
	if !strings.Contains(lines[0], "\"msg\":\"warn-line\"") {
		t.Errorf("唯一日志行不是 warn-line：%s", lines[0])
	}
}

func TestSetupInvalidLevelFallsBackToInfo(t *testing.T) {
	t.Setenv(EnvLogLevel, "verbose")
	dir := t.TempDir()
	logger, closeFn, err := Setup(dir, "v")
	if err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	logger.Debug("debug-line")
	logger.Info("info-line")
	if err := closeFn(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	lines := readLines(t, listManagerLogs(t, dir)[0])
	if len(lines) != 1 || !strings.Contains(lines[0], "\"msg\":\"info-line\"") {
		t.Fatalf("无效级别应回退 info：%v", lines)
	}
}

func TestSetupRejectsEmptyDir(t *testing.T) {
	if _, _, err := Setup("   ", "v"); err == nil {
		t.Fatal("空日志目录应报错")
	}
}

func TestSetupRejectsFileAsDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs-file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	if _, _, err := Setup(path, "v"); err == nil {
		t.Fatal("日志目录是文件时应报错")
	}
}
