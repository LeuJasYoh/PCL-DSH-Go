// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/passhash"
)

const (
	// AdminSchemaVersion 是 admin.json 的数据版本。
	AdminSchemaVersion = 1
	// adminFileName 是管理员凭据文件名（位于 <data-root>/config/）。
	adminFileName = "admin.json"
	// InitialPasswordFileName 是首次启动生成的初始密码文件名（位于 <data-root>/）。
	InitialPasswordFileName = "initial-password"

	initialPasswordLength  = 24
	adminFilePermission    = 0o600
	initialPasswordNewline = "\n"
)

// initialPasswordAlphabet 是初始密码字符集：大小写字母与数字，排除易混淆字符（0/1/I/O/l/o）。
const initialPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// ErrWrongPassword 表示修改密码时原密码校验失败。
var ErrWrongPassword = errors.New("当前密码不正确。")

// adminFile 是 admin.json 的持久化结构（schemaVersion 1）。
type adminFile struct {
	SchemaVersion          int    `json:"schemaVersion"`
	PasswordHash           string `json:"passwordHash"`
	InitialPasswordPending bool   `json:"initialPasswordPending"`
	CreatedAt              int64  `json:"createdAt"`
}

// AdminStore 是单管理员密码凭据的并发安全访问入口。
// 密码只以加盐哈希（bcrypt-sha256，兼容旧 BCrypt）落盘；初始密码仅写入 initial-password 文件一次。
type AdminStore struct {
	mu sync.RWMutex

	configDir           string // <data-root>/config
	adminPath           string // <data-root>/config/admin.json
	initialPasswordPath string // <data-root>/initial-password

	passwordHash []byte
	pending      bool
	createdAt    int64
}

// OpenAdmin 打开或引导 <data-root>/config/admin.json（schemaVersion 1，0600）。
// 首次（文件不存在）：生成 24 位字母数字初始密码，bcrypt-sha256（cost 10）写入 admin.json
// （initialPasswordPending=true），同时把明文密码写入 <data-root>/initial-password（0600）。
// 已存在且 pending=true：保留 initial-password；pending=false：清理残留 initial-password。
// 损坏的 admin.json 返回明确错误且绝不覆盖。
func OpenAdmin(dataRoot string) (*AdminStore, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return nil, fmt.Errorf("%w: data-root 不能为空", ErrInvalidInput)
	}
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("解析 data-root 失败: %w", err)
	}
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	// data-root/config 必须仅当前用户可访问；已存在时尽力收紧权限。
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("设置配置目录权限失败: %w", err)
	}
	admin := &AdminStore{
		configDir:           dir,
		adminPath:           filepath.Join(dir, adminFileName),
		initialPasswordPath: filepath.Join(root, InitialPasswordFileName),
	}
	if err := admin.loadOrBootstrap(); err != nil {
		return nil, err
	}
	return admin, nil
}

// Close 释放 AdminStore。所有修改在返回前均已落盘，因此 Close 不执行额外 I/O。
func (a *AdminStore) Close() error { return nil }

// InitialPasswordPending 返回管理员是否仍处于“首次登录必须改密”状态。
func (a *AdminStore) InitialPasswordPending() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.pending
}

// InitialPasswordPath 返回初始密码文件路径（绝对路径；文件可能已不存在）。
// 调用方可以记录该路径，但绝不可打印密码内容。
func (a *AdminStore) InitialPasswordPath() string { return a.initialPasswordPath }

// VerifyPassword 用版本化哈希校验明文密码；空密码返回 false。
func (a *AdminStore) VerifyPassword(plain string) (bool, error) {
	if plain == "" {
		return false, nil
	}
	a.mu.RLock()
	hash := append([]byte(nil), a.passwordHash...)
	a.mu.RUnlock()
	if len(hash) == 0 {
		return false, nil
	}
	ok, err := passhash.Verify(string(hash), plain)
	if err != nil {
		return false, fmt.Errorf("校验管理员密码失败: %w", err)
	}
	return ok, nil
}

// ChangePassword 校验原密码后设置新密码：新密码 8-256 字符且不等于旧密码。
// 成功后写回 admin.json（pending=false）并删除 initial-password。
// 任何错误都不会泄漏密码明文。
func (a *AdminStore) ChangePassword(oldPassword, newPassword string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.passwordHash) == 0 {
		return errors.New("管理员凭据尚未初始化，无法修改密码")
	}
	ok, err := passhash.Verify(string(a.passwordHash), oldPassword)
	if err != nil {
		return fmt.Errorf("校验原密码失败: %w", err)
	}
	if !ok {
		return ErrWrongPassword
	}
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}
	if newPassword == oldPassword {
		return fmt.Errorf("%w: 新密码不能与当前密码相同", ErrInvalidInput)
	}
	newHash, err := passhash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("生成新密码哈希失败: %w", err)
	}
	if err := a.persistLocked([]byte(newHash), false); err != nil {
		return err
	}
	a.passwordHash = []byte(newHash)
	a.pending = false
	// 密码已持久化；初始密码文件清理失败不影响改密结果，下次 OpenAdmin 会再次清理。
	_ = os.Remove(a.initialPasswordPath)
	return nil
}

// loadOrBootstrap 读取已有凭据或生成首次启动凭据。
func (a *AdminStore) loadOrBootstrap() error {
	data, err := os.ReadFile(a.adminPath)
	switch {
	case err == nil:
		return a.load(data)
	case errors.Is(err, fs.ErrNotExist):
		return a.bootstrap()
	default:
		return fmt.Errorf("读取管理员凭据 %s 失败: %w", a.adminPath, err)
	}
}

// load 解析并校验磁盘上的 admin.json；损坏时返回明确错误，不覆盖原文件。
func (a *AdminStore) load(data []byte) error {
	var file adminFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("管理员凭据 %s 不是合法 JSON，请修复或备份后删除（删除后重启会重新生成初始密码）: %w", a.adminPath, err)
	}
	if file.SchemaVersion != AdminSchemaVersion {
		return fmt.Errorf("管理员凭据 %s 的 schemaVersion=%d 不受支持（期望 %d）", a.adminPath, file.SchemaVersion, AdminSchemaVersion)
	}
	hash := strings.TrimSpace(file.PasswordHash)
	if hash == "" {
		return fmt.Errorf("管理员凭据 %s 缺少 passwordHash；请修复或备份后删除该文件", a.adminPath)
	}
	if err := passhash.ValidateHash(hash); err != nil {
		return fmt.Errorf("管理员凭据 %s 的 passwordHash 不是受支持的密码哈希: %w", a.adminPath, err)
	}
	if file.CreatedAt < 0 {
		return fmt.Errorf("管理员凭据 %s 的 createdAt 无效", a.adminPath)
	}
	// 已存在文件的权限可能被人工放宽；尽力收紧到 0600（Windows 上为 best-effort）。
	_ = os.Chmod(a.adminPath, adminFilePermission)
	a.passwordHash = []byte(hash)
	a.pending = file.InitialPasswordPending
	a.createdAt = file.CreatedAt
	if a.pending {
		_ = os.Chmod(a.initialPasswordPath, adminFilePermission)
	} else {
		// 已改密：旧初始密码不再有效，清理明文残留文件（失败时下次启动再试）。
		_ = os.Remove(a.initialPasswordPath)
	}
	return nil
}

// bootstrap 生成首次启动的管理员凭据。
func (a *AdminStore) bootstrap() error {
	password, err := generateInitialPassword()
	if err != nil {
		return fmt.Errorf("生成初始管理员密码失败: %w", err)
	}
	hash, err := passhash.Hash(password)
	if err != nil {
		return fmt.Errorf("生成管理员密码哈希失败: %w", err)
	}
	createdAt := time.Now().UnixMilli()
	// 先写初始密码文件、再写 admin.json：中途失败时下次启动会重新生成，
	// 不会出现“已有哈希但无初始密码”从而无法登录的死锁。
	if err := atomicWriteFile(a.initialPasswordPath, []byte(password+initialPasswordNewline), adminFilePermission); err != nil {
		return fmt.Errorf("写入初始密码文件 %s 失败: %w", a.initialPasswordPath, err)
	}
	if err := a.persistLocked([]byte(hash), true, createdAt); err != nil {
		_ = os.Remove(a.initialPasswordPath)
		return err
	}
	a.passwordHash = []byte(hash)
	a.pending = true
	a.createdAt = createdAt
	return nil
}

// persistLocked 原子写入 admin.json；调用方必须持有 a.mu（或处于初始化阶段）。
func (a *AdminStore) persistLocked(hash []byte, pending bool, createdAt ...int64) error {
	created := a.createdAt
	if len(createdAt) > 0 {
		created = createdAt[0]
	}
	file := adminFile{
		SchemaVersion:          AdminSchemaVersion,
		PasswordHash:           string(hash),
		InitialPasswordPending: pending,
		CreatedAt:              created,
	}
	data, err := marshalJSON(file)
	if err != nil {
		return fmt.Errorf("序列化管理员凭据失败: %w", err)
	}
	if err := atomicWriteFile(a.adminPath, data, adminFilePermission); err != nil {
		return fmt.Errorf("写入管理员凭据 %s 失败: %w", a.adminPath, err)
	}
	return nil
}

// validateNewPassword 校验新密码：8-256 字符、不含控制字符。
func validateNewPassword(raw string) error {
	if err := passhash.Validate(raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return nil
}

// generateInitialPassword 生成 24 位字母数字初始密码（crypto/rand 无模偏差采样）。
func generateInitialPassword() (string, error) {
	alphabetLen := len(initialPasswordAlphabet)
	limit := 256 - (256 % alphabetLen) // 拒绝采样上限，避免取模偏差
	out := make([]byte, 0, initialPasswordLength)
	buf := make([]byte, 32)
	for len(out) < initialPasswordLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, initialPasswordAlphabet[int(b)%alphabetLen])
			if len(out) == initialPasswordLength {
				break
			}
		}
	}
	return string(out), nil
}
