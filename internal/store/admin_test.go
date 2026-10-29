// SPDX-License-Identifier: MIT

package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/passhash"
	"golang.org/x/crypto/bcrypt"
)

// readInitialPassword 读取初始密码文件内容（去掉文件末尾换行）。
func readInitialPassword(t *testing.T, path string) string {
	t.Helper()
	return strings.TrimSpace(string(readFile(t, path)))
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func TestOpenAdminBootstrap(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	defer func() {
		if err := admin.Close(); err != nil {
			t.Fatalf("Close 失败: %v", err)
		}
	}()

	wantInitialPath := filepath.Join(dataRoot, InitialPasswordFileName)
	if got := admin.InitialPasswordPath(); got != wantInitialPath {
		t.Fatalf("InitialPasswordPath = %q，期望 %q", got, wantInitialPath)
	}
	if !admin.InitialPasswordPending() {
		t.Fatal("首次启动后 initialPasswordPending 应为 true")
	}

	password := readInitialPassword(t, wantInitialPath)
	if len(password) != 24 {
		t.Fatalf("初始密码长度 = %d，期望 24", len(password))
	}
	if !isAlphanumeric(password) {
		t.Fatalf("初始密码应只含字母数字: %q", password)
	}

	adminPath := filepath.Join(dataRoot, "config", "admin.json")
	var file adminFile
	if err := json.Unmarshal(readFile(t, adminPath), &file); err != nil {
		t.Fatalf("admin.json 不是合法 JSON: %v", err)
	}
	if file.SchemaVersion != AdminSchemaVersion {
		t.Fatalf("schemaVersion = %d，期望 %d", file.SchemaVersion, AdminSchemaVersion)
	}
	if !file.InitialPasswordPending {
		t.Fatal("admin.json 中 initialPasswordPending 应为 true")
	}
	if file.CreatedAt <= 0 {
		t.Fatalf("createdAt = %d，期望正数毫秒时间戳", file.CreatedAt)
	}
	if err := passhash.ValidateHash(file.PasswordHash); err != nil {
		t.Fatalf("passwordHash 不是受支持的哈希: %v", err)
	}
	if !strings.HasPrefix(file.PasswordHash, passhash.HashPrefix) {
		t.Fatalf("passwordHash = %q, want prefix %q", file.PasswordHash, passhash.HashPrefix)
	}
	cost, err := bcrypt.Cost([]byte(strings.TrimPrefix(file.PasswordHash, passhash.HashPrefix)))
	if err != nil {
		t.Fatalf("passwordHash 不是合法 BCrypt: %v", err)
	}
	if cost != passhash.BcryptCost {
		t.Fatalf("BCrypt cost = %d，期望 %d", cost, passhash.BcryptCost)
	}

	ok, err := admin.VerifyPassword(password)
	if err != nil || !ok {
		t.Fatalf("初始密码校验 = %v, %v；期望 true, nil", ok, err)
	}
	if ok, err := admin.VerifyPassword("wrong-password"); err != nil || ok {
		t.Fatalf("错误密码校验 = %v, %v；期望 false, nil", ok, err)
	}
	if ok, err := admin.VerifyPassword(""); err != nil || ok {
		t.Fatalf("空密码校验 = %v, %v；期望 false, nil", ok, err)
	}
}

func TestOpenAdminReopenPreservesCredentials(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	password := readInitialPassword(t, admin.InitialPasswordPath())
	adminPath := filepath.Join(dataRoot, "config", "admin.json")
	before := string(readFile(t, adminPath))
	if err := admin.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	reopened, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("重新 OpenAdmin 失败: %v", err)
	}
	defer reopened.Close()
	if !reopened.InitialPasswordPending() {
		t.Fatal("重新打开后 pending 应保持 true")
	}
	if ok, err := reopened.VerifyPassword(password); err != nil || !ok {
		t.Fatalf("重新打开后初始密码校验 = %v, %v", ok, err)
	}
	if got := string(readFile(t, adminPath)); got != before {
		t.Fatalf("重新打开不应改写 admin.json:\n got=%q\nwant=%q", got, before)
	}
}

func TestOpenAdminChangePassword(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	defer admin.Close()
	oldPassword := readInitialPassword(t, admin.InitialPasswordPath())
	initialPath := admin.InitialPasswordPath()
	var beforeFile adminFile
	if err := json.Unmarshal(readFile(t, filepath.Join(dataRoot, "config", "admin.json")), &beforeFile); err != nil {
		t.Fatalf("解析初始 admin.json 失败: %v", err)
	}
	beforeHash := beforeFile.PasswordHash

	if err := admin.ChangePassword("wrong-old", "newPassword1"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("原密码错误应返回 ErrWrongPassword，实际 %v", err)
	}
	if !admin.InitialPasswordPending() {
		t.Fatal("改密失败后 pending 应保持 true")
	}
	if _, err := os.Stat(initialPath); err != nil {
		t.Fatalf("改密失败后 initial-password 不应被删除: %v", err)
	}

	invalidNews := []string{
		"",
		"short7",                 // 7 字符
		strings.Repeat("a", 257), // 257 字符
		"line\nbreak-password",   // 控制字符
		oldPassword,              // 与旧密码相同
	}
	for _, newPassword := range invalidNews {
		if err := admin.ChangePassword(oldPassword, newPassword); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("非法新密码 %q 应返回 ErrInvalidInput，实际 %v", newPassword, err)
		}
	}

	newPassword := strings.Repeat("a", passhash.MaxRunes)
	if err := admin.ChangePassword(oldPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword 失败: %v", err)
	}
	if admin.InitialPasswordPending() {
		t.Fatal("改密成功后 pending 应为 false")
	}
	if _, err := os.Stat(initialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("改密成功后 initial-password 应被删除，Stat err = %v", err)
	}
	if ok, err := admin.VerifyPassword(newPassword); err != nil || !ok {
		t.Fatalf("新密码校验 = %v, %v", ok, err)
	}
	if ok, err := admin.VerifyPassword(oldPassword); err != nil || ok {
		t.Fatalf("旧密码应失效，校验 = %v, %v", ok, err)
	}

	var file adminFile
	if err := json.Unmarshal(readFile(t, filepath.Join(dataRoot, "config", "admin.json")), &file); err != nil {
		t.Fatalf("admin.json 解析失败: %v", err)
	}
	if file.InitialPasswordPending {
		t.Fatal("admin.json 中 pending 应为 false")
	}
	if file.PasswordHash == beforeHash {
		t.Fatal("admin.json 中 passwordHash 应变化")
	}

	// 改密后再次改密（pending 已为 false）。
	if err := admin.ChangePassword("bad", "anotherPassword2"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("已改密后原密码错误应返回 ErrWrongPassword，实际 %v", err)
	}
	if err := admin.ChangePassword(newPassword, "anotherPassword2"); err != nil {
		t.Fatalf("第二次改密失败: %v", err)
	}
	if ok, _ := admin.VerifyPassword("anotherPassword2"); !ok {
		t.Fatal("第二次改密后新密码应可用")
	}
}

func TestOpenAdminCleansStaleInitialPassword(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	oldPassword := readInitialPassword(t, admin.InitialPasswordPath())
	if err := admin.ChangePassword(oldPassword, "afterChangePass1"); err != nil {
		t.Fatalf("ChangePassword 失败: %v", err)
	}
	if err := admin.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	// 模拟残留的初始密码文件（例如旧版本或异常中断留下）。
	stalePath := filepath.Join(dataRoot, InitialPasswordFileName)
	if err := os.WriteFile(stalePath, []byte("stale-password\n"), 0o600); err != nil {
		t.Fatalf("写入残留文件失败: %v", err)
	}
	reopened, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("重新 OpenAdmin 失败: %v", err)
	}
	defer reopened.Close()
	if reopened.InitialPasswordPending() {
		t.Fatal("pending 应为 false")
	}
	if _, err := os.Stat(stalePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending=false 时残留 initial-password 应被清理，Stat err = %v", err)
	}
}

func TestOpenAdminCorruptFileNotOverwritten(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"非法 JSON", "{not json"},
		{"schemaVersion 不支持", "{\"schemaVersion\":2,\"passwordHash\":\"x\"}"},
		{"缺 passwordHash", "{\"schemaVersion\":1,\"passwordHash\":\"\",\"initialPasswordPending\":true}"},
		{"passwordHash 非 BCrypt", "{\"schemaVersion\":1,\"passwordHash\":\"not-a-bcrypt-hash\",\"initialPasswordPending\":true}"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot := filepath.Join(t.TempDir(), "data")
			configDir := filepath.Join(dataRoot, "config")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatalf("创建 config 目录失败: %v", err)
			}
			path := filepath.Join(configDir, "admin.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("写入损坏文件失败: %v", err)
			}
			if _, err := OpenAdmin(dataRoot); err == nil {
				t.Fatal("损坏的 admin.json 应导致 OpenAdmin 失败")
			}
			if got := string(readFile(t, path)); got != tc.content {
				t.Fatalf("OpenAdmin 失败后不应改写损坏文件:\n got=%q\nwant=%q", got, tc.content)
			}
			if _, err := os.Stat(filepath.Join(dataRoot, InitialPasswordFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("损坏文件不应触发初始密码生成，Stat err = %v", err)
			}
		})
	}
}

func TestOpenAdminEmptyDataRoot(t *testing.T) {
	if _, err := OpenAdmin("   "); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空 data-root 应返回 ErrInvalidInput，实际 %v", err)
	}
}

func TestAdminLegacyBcryptHashAndUpgrade(t *testing.T) {
	dataRoot := t.TempDir()
	configDir := filepath.Join(dataRoot, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("创建 config 目录失败: %v", err)
	}
	legacyHash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-1"), passhash.BcryptCost)
	if err != nil {
		t.Fatalf("生成旧 BCrypt 哈希失败: %v", err)
	}
	legacyFile := adminFile{
		SchemaVersion:          AdminSchemaVersion,
		PasswordHash:           string(legacyHash),
		InitialPasswordPending: false,
		CreatedAt:              1,
	}
	data, err := json.Marshal(legacyFile)
	if err != nil {
		t.Fatalf("序列化 admin.json 失败: %v", err)
	}
	adminPath := filepath.Join(configDir, adminFileName)
	if err := os.WriteFile(adminPath, data, 0o600); err != nil {
		t.Fatalf("写入 admin.json 失败: %v", err)
	}

	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin(旧 BCrypt) 失败: %v", err)
	}
	defer admin.Close()
	if admin.InitialPasswordPending() {
		t.Fatal("旧 admin.json 的 pending 应保持 false")
	}
	if ok, err := admin.VerifyPassword("legacy-password-1"); err != nil || !ok {
		t.Fatalf("旧 BCrypt 密码校验 = %v, %v; want true, nil", ok, err)
	}
	if err := admin.ChangePassword("legacy-password-1", "new-password-2"); err != nil {
		t.Fatalf("旧 BCrypt 改密失败: %v", err)
	}
	updatedData, err := os.ReadFile(adminPath)
	if err != nil {
		t.Fatalf("读取更新后的 admin.json 失败: %v", err)
	}
	var updated adminFile
	if err := json.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("解析更新后的 admin.json 失败: %v", err)
	}
	if !strings.HasPrefix(updated.PasswordHash, passhash.HashPrefix) {
		t.Fatalf("改密后 passwordHash = %q, want prefix %q", updated.PasswordHash, passhash.HashPrefix)
	}
	if ok, err := admin.VerifyPassword("new-password-2"); err != nil || !ok {
		t.Fatalf("改密后新密码校验 = %v, %v; want true, nil", ok, err)
	}
}

func TestAdminConcurrentVerifyAndChange(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	admin, err := OpenAdmin(dataRoot)
	if err != nil {
		t.Fatalf("OpenAdmin 失败: %v", err)
	}
	defer admin.Close()
	oldPassword := readInitialPassword(t, admin.InitialPasswordPath())

	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := admin.VerifyPassword(oldPassword); err != nil {
					errCh <- err
					return
				}
				_ = admin.InitialPasswordPending()
			}
		}()
	}
	if err := admin.ChangePassword(oldPassword, "concurrentPass9"); err != nil {
		t.Fatalf("并发场景下 ChangePassword 失败: %v", err)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发校验出错: %v", err)
	}
	if ok, err := admin.VerifyPassword("concurrentPass9"); err != nil || !ok {
		t.Fatalf("并发改密后新密码不可用: %v, %v", ok, err)
	}
}
