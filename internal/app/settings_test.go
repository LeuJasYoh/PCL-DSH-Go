// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

func TestUpdateSettings(t *testing.T) {
	application, fakeRT, _, _ := newTestApp(t)
	ctx := context.Background()
	want := store.Settings{
		Proxy:               "http://127.0.0.1:7890",
		Registry:            "https://registry.npmmirror.com",
		DirectAccessSSHUser: "ubuntu",
	}
	if err := application.UpdateSettings(ctx, want); err != nil {
		t.Fatalf("UpdateSettings 失败: %v", err)
	}
	if got := application.store.Settings(); got != want {
		t.Fatalf("持久化设置 = %+v，期望 %+v", got, want)
	}
	if fakeRT.proxy != want.Proxy {
		t.Fatalf("runtime.SetProxy = %q，期望 %q", fakeRT.proxy, want.Proxy)
	}

	invalid := []struct {
		name     string
		settings store.Settings
	}{
		{"registry 非法", store.Settings{Registry: "ftp://example.com"}},
		{"proxy 非法", store.Settings{Proxy: "127.0.0.1:7890"}},
		{"sshUser 过长", store.Settings{DirectAccessSSHUser: strings.Repeat("a", 33)}},
	}
	for _, tc := range invalid {
		err := application.UpdateSettings(ctx, tc.settings)
		requireAPIError(t, err, 400, apierr.CodeValidation)
		if got := application.store.Settings(); got != want {
			t.Fatalf("%s: 非法设置不应持久化，实际 %+v", tc.name, got)
		}
	}

	application.mu.Lock()
	err := application.UpdateSettings(ctx, want)
	application.mu.Unlock()
	requireAPIError(t, err, 409, apierr.CodeConflictBusy)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.UpdateSettings(canceled, want); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
}

func TestLogsTail(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	logPath := filepath.Join(dirs.LogsDir, "manager-2026-01-02.log")
	content := "2026-01-02 line one\nline token=secret-token\nline password=hunter2\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}
	lines, err := application.LogsTail(10)
	if err != nil {
		t.Fatalf("LogsTail 失败: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("日志行数 = %d，期望 3: %v", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "token=***") || !strings.Contains(joined, "password=***") {
		t.Fatalf("日志应脱敏: %q", joined)
	}
	if strings.Contains(joined, "secret-token") || strings.Contains(joined, "hunter2") {
		t.Fatalf("日志泄漏敏感值: %q", joined)
	}

	if err := os.Remove(logPath); err != nil {
		t.Fatalf("删除日志失败: %v", err)
	}
	_, err = application.LogsTail(10)
	requireAPIError(t, err, 500, apierr.CodeInternal)
}
