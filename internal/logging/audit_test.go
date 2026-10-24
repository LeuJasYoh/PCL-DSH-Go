// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func listAuditLogs(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "audit-*.log"))
	if err != nil {
		t.Fatalf("Glob audit 日志失败：%v", err)
	}
	return files
}

func TestAuditEventWritesSingleLineJSON(t *testing.T) {
	dir := t.TempDir()
	audit, err := OpenAudit(dir)
	if err != nil {
		t.Fatalf("OpenAudit 失败：%v", err)
	}
	audit.Event("LOGIN_FAILED", "ip", "127.0.0.1", "password", "hunter2", "detail", "token=abc", "attempt", 3)
	if err := audit.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	if err := audit.Close(); err != nil {
		t.Fatalf("重复 Close 失败：%v", err)
	}
	files := listAuditLogs(t, dir)
	if len(files) != 1 {
		t.Fatalf("audit 日志文件数 = %d, 期望 1", len(files))
	}
	lines := readLines(t, files[0])
	if len(lines) != 1 {
		t.Fatalf("审计行数 = %d, 期望 1", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("审计日志不是合法 JSON：%v（%s）", err, lines[0])
	}
	if record["event"] != "LOGIN_FAILED" {
		t.Errorf("event = %v", record["event"])
	}
	if _, ok := record["ts"].(string); !ok || record["ts"] == "" {
		t.Errorf("ts 缺失或类型错误：%v", record["ts"])
	}
	if record["ip"] != "127.0.0.1" {
		t.Errorf("ip = %v", record["ip"])
	}
	if record["password"] != redacted {
		t.Errorf("password = %v, 期望 %q", record["password"], redacted)
	}
	if record["detail"] != "token=***" {
		t.Errorf("detail = %v, 期望 token=***", record["detail"])
	}
	if record["attempt"] != float64(3) {
		t.Errorf("attempt = %v", record["attempt"])
	}
}

func TestAuditMultipleEvents(t *testing.T) {
	dir := t.TempDir()
	audit, err := OpenAudit(dir)
	if err != nil {
		t.Fatalf("OpenAudit 失败：%v", err)
	}
	audit.Event("A")
	audit.Event("B", "n", 1)
	audit.Event("C", "bad", make(chan int)) // 不可序列化值退化为字符串
	if err := audit.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	lines := readLines(t, listAuditLogs(t, dir)[0])
	if len(lines) != 3 {
		t.Fatalf("审计行数 = %d, 期望 3", len(lines))
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("审计行不是合法 JSON：%v（%s）", err, line)
		}
	}
}

func TestAuditConcurrentEvents(t *testing.T) {
	dir := t.TempDir()
	audit, err := OpenAudit(dir)
	if err != nil {
		t.Fatalf("OpenAudit 失败：%v", err)
	}
	const goroutines = 8
	const perGoroutine = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				audit.Event("EVENT", "i", j)
			}
		}()
	}
	wg.Wait()
	if err := audit.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	lines := readLines(t, listAuditLogs(t, dir)[0])
	if len(lines) != goroutines*perGoroutine {
		t.Fatalf("审计行数 = %d, 期望 %d", len(lines), goroutines*perGoroutine)
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("并发写入产生非法 JSON：%v（%s）", err, line)
		}
		if strings.Count(line, "\n") != 0 {
			t.Fatalf("审计日志不是单行：%q", line)
		}
	}
}

func TestAuditEmptyDir(t *testing.T) {
	if _, err := OpenAudit("  "); err == nil {
		t.Fatal("空审计目录应报错")
	}
}

func TestSensitiveKey(t *testing.T) {
	for _, key := range []string{"token", "access_token", "Password", "api_key", "API-KEY", "authorization", "Cookie", "secret"} {
		if !sensitiveKey(key) {
			t.Errorf("sensitiveKey(%q) = false, 期望 true", key)
		}
	}
	for _, key := range []string{"ip", "event", "detail", "attempt", "path"} {
		if sensitiveKey(key) {
			t.Errorf("sensitiveKey(%q) = true, 期望 false", key)
		}
	}
}
