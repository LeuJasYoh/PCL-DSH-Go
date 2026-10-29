// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectAccessSSHUserValidation(t *testing.T) {
	s, _ := newTestStore(t)

	// 空值恢复默认 root。
	if err := s.UpdateSettings(Settings{}); err != nil {
		t.Fatalf("空设置应恢复默认: %v", err)
	}
	if got := s.Settings().DirectAccessSSHUser; got != "root" {
		t.Fatalf("默认 directAccessSshUser = %q，期望 root", got)
	}

	valid := []string{
		"root",
		"ubuntu",
		"user.name-_1",
		"a",
		strings.Repeat("a", 32),
		"12345678901234567890123456789012",
	}
	for _, user := range valid {
		if err := s.UpdateSettings(Settings{DirectAccessSSHUser: user}); err != nil {
			t.Fatalf("合法 sshUser %q 被拒绝: %v", user, err)
		}
		if got := s.Settings().DirectAccessSSHUser; got != user {
			t.Fatalf("sshUser 保存 = %q，期望 %q", got, user)
		}
	}

	invalid := []string{
		strings.Repeat("a", 33),
		"user name",
		"root@host",
		"root;rm -rf /",
		"网页",
		"line\nbreak",
		"user/name",
	}
	for _, user := range invalid {
		err := s.UpdateSettings(Settings{DirectAccessSSHUser: user})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("非法 sshUser %q 应返回 ErrInvalidInput，实际 %v", user, err)
		}
	}
}

func TestStoredManagerInvalidSSHUserRejected(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	configDir := filepath.Join(dataRoot, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("创建 config 目录失败: %v", err)
	}
	manager := "{\"schemaVersion\":1,\"selectedInstanceId\":\"\",\"settings\":{\"proxy\":\"\",\"registry\":\"https://registry.npmjs.org\",\"directAccessSshUser\":\"" + strings.Repeat("a", 33) + "\"}}"
	if err := os.WriteFile(filepath.Join(configDir, "manager.json"), []byte(manager), 0o600); err != nil {
		t.Fatalf("写入 manager.json 失败: %v", err)
	}
	if _, err := Open(dataRoot); err == nil {
		t.Fatal("manager.json 中非法 directAccessSshUser 应导致 Open 失败")
	}
}
