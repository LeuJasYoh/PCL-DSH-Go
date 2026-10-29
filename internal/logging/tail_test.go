// SPDX-License-Identifier: MIT

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeManagerLog 写入一个指定日期的 manager 日志文件。
func writeManagerLog(t *testing.T, dir, date, content string) string {
	t.Helper()
	path := filepath.Join(dir, "manager-"+date+".log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入日志文件 %s 失败: %v", path, err)
	}
	return path
}

func TestTailLatestPicksLatestByDateAndRedacts(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeManagerLog(t, dir, "2026-10-23", "old-1\nold-2\nold-3\n")
	writeManagerLog(t, dir, "2026-10-24", "line1\nline2\ntoken=abc123\nline4\n")
	// 旧文件的 mtime 更新，验证按文件名日期而不是 mtime 选择“最新”。
	now := time.Now()
	if err := os.Chtimes(oldPath, now, now); err != nil {
		t.Fatalf("设置 mtime 失败: %v", err)
	}

	lines, err := TailLatest(dir, 2)
	if err != nil {
		t.Fatalf("TailLatest 失败: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("行数 = %d，期望 2（%v）", len(lines), lines)
	}
	if lines[0] != "token=***" {
		t.Fatalf("敏感行未脱敏: %q", lines[0])
	}
	if lines[1] != "line4" {
		t.Fatalf("最后一行 = %q，期望 line4", lines[1])
	}
}

func TestTailLatestDefaultsAndClamp(t *testing.T) {
	dir := t.TempDir()
	var builder strings.Builder
	for i := 1; i <= 2100; i++ {
		fmt.Fprintf(&builder, "log-%d\n", i)
	}
	writeManagerLog(t, dir, "2026-10-24", builder.String())

	cases := []struct {
		name  string
		lines int
		want  int
		first string
	}{
		{name: "0 默认 800", lines: 0, want: 800, first: "log-1301"},
		{name: "负数默认 800", lines: -5, want: 800, first: "log-1301"},
		{name: "超过上限截断 2000", lines: 5000, want: 2000, first: "log-101"},
		{name: "1 行", lines: 1, want: 1, first: "log-2100"},
		{name: "500 行", lines: 500, want: 500, first: "log-1601"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := TailLatest(dir, tc.lines)
			if err != nil {
				t.Fatalf("TailLatest 失败: %v", err)
			}
			if len(lines) != tc.want {
				t.Fatalf("行数 = %d，期望 %d", len(lines), tc.want)
			}
			if lines[0] != tc.first {
				t.Fatalf("第一行 = %q，期望 %q", lines[0], tc.first)
			}
		})
	}
}

func TestTailLatestRedactsEveryLine(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"token=secret-token-1",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9",
		"Cookie: session=abc; theme=dark",
		"{\"password\":\"p@ssw0rd\"}",
		"normal line",
	}, "\n") + "\n"
	writeManagerLog(t, dir, "2026-10-24", content)
	lines, err := TailLatest(dir, 10)
	if err != nil {
		t.Fatalf("TailLatest 失败: %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, leaked := range []string{"secret-token-1", "eyJhbGciOiJIUzI1NiJ9", "session=abc", "p@ssw0rd"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("tail 输出泄漏敏感值 %q：\n%s", leaked, joined)
		}
	}
	if lines[4] != "normal line" {
		t.Fatalf("普通行被改变: %q", lines[4])
	}
}

func TestTailLatestNoLogFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := TailLatest(dir, 10); err == nil {
		t.Fatal("目录中没有 manager 日志时应返回错误")
	}
	if _, err := TailLatest("  ", 10); err == nil {
		t.Fatal("空日志目录应返回错误")
	}
	if _, err := TailLatest(filepath.Join(dir, "missing"), 10); err == nil {
		t.Fatal("不存在的日志目录应返回错误")
	}
}

func TestTailLatestEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeManagerLog(t, dir, "2026-10-24", "")
	lines, err := TailLatest(dir, 10)
	if err != nil {
		t.Fatalf("空日志文件不应报错: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("空日志文件应返回 0 行，实际 %d", len(lines))
	}
}

func TestTailLatestCRLFAndNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeManagerLog(t, dir, "2026-10-24", "a\r\nb\r\nc")
	lines, err := TailLatest(dir, 10)
	if err != nil {
		t.Fatalf("TailLatest 失败: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(lines) != len(want) {
		t.Fatalf("行数 = %d，期望 %d（%q）", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("第 %d 行 = %q，期望 %q", i, lines[i], want[i])
		}
	}
}

func TestTailLatestLongLine(t *testing.T) {
	dir := t.TempDir()
	longLine := strings.Repeat("x", 100*1024)
	writeManagerLog(t, dir, "2026-10-24", longLine+"\ntail-line\n")
	lines, err := TailLatest(dir, 2)
	if err != nil {
		t.Fatalf("TailLatest 失败: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("行数 = %d，期望 2", len(lines))
	}
	if lines[0] != longLine {
		t.Fatalf("超长行读取不完整：长度 %d，期望 %d", len(lines[0]), len(longLine))
	}
	if lines[1] != "tail-line" {
		t.Fatalf("最后一行 = %q", lines[1])
	}
}

func TestTailLatestIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	writeManagerLog(t, dir, "2026-10-24", "manager-line\n")
	writeManagerLog(t, dir, "2026-10-25", "should-be-older-ignored\n")
	if err := os.WriteFile(filepath.Join(dir, "audit-2026-10-25.log"), []byte("audit-line\n"), 0o600); err != nil {
		t.Fatalf("写入其他文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manager-bad-name.log"), []byte("bad\n"), 0o600); err != nil {
		t.Fatalf("写入其他文件失败: %v", err)
	}
	lines, err := TailLatest(dir, 10)
	if err != nil {
		t.Fatalf("TailLatest 失败: %v", err)
	}
	if len(lines) != 1 || lines[0] != "should-be-older-ignored" {
		t.Fatalf("应只识别 manager-YYYY-MM-DD.log 且选 2026-10-25：%q", lines)
	}
}
