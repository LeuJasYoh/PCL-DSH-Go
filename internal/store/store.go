// SPDX-License-Identifier: GPL-3.0-or-later

// Package store 负责 PCL-DSH-Go 的持久化数据：
//
//   - <data-root>/config/instances.json：实例清单（schemaVersion 1）
//   - <data-root>/config/manager.json：选中实例与管理设置（schemaVersion 1）
//
// 所有写入都使用“同目录临时文件 + fsync + rename”的原子写，配置文件权限 0600；
// Store 的方法可在多 goroutine 下并发调用。损坏的 JSON 或不受支持的 schemaVersion
// 会返回明确错误，绝不静默覆盖用户数据。
package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SchemaVersion 是当前 instances.json / manager.json 的数据版本。
const SchemaVersion = 1

// 存储层可被上层用 errors.Is 识别的错误。
var (
	// ErrNotFound 表示实例不存在。
	ErrNotFound = errors.New("实例不存在")
	// ErrInvalidInput 表示输入校验失败（上层可映射为 HTTP 400）。
	ErrInvalidInput = errors.New("参数校验失败")
)

// Instance 是 instances.json 中的一条实例记录。
type Instance struct {
	ID        string `json:"id"`        // i- + 12 位十六进制
	Name      string `json:"name"`      // 1-40 字符，已去首尾空白
	Version   string `json:"version"`   // 精确 SemVer，创建后不可变
	Profile   string `json:"profile"`   // [A-Za-z0-9._-]+，创建后不可变
	Port      int    `json:"port"`      // 1-65535
	CreatedAt int64  `json:"createdAt"` // 毫秒
	UpdatedAt int64  `json:"updatedAt"` // 毫秒
}

// Settings 是 manager.json 中的管理设置（字段默认值见 defaultSettings）。
type Settings struct {
	Proxy               string `json:"proxy"`
	Registry            string `json:"registry"`
	DirectAccessSSHUser string `json:"directAccessSshUser"`
}

// CreateInstanceInput 是创建实例的输入。
type CreateInstanceInput struct {
	Name    string
	Version string
	Profile string
	Port    int
}

// UpdateInstanceInput 是更新实例的输入；只允许修改 name/port，nil 表示不修改。
type UpdateInstanceInput struct {
	Name *string
	Port *int
}

type instancesFile struct {
	SchemaVersion int        `json:"schemaVersion"`
	Instances     []Instance `json:"instances"`
}

type managerFile struct {
	SchemaVersion      int      `json:"schemaVersion"`
	SelectedInstanceID string   `json:"selectedInstanceId"`
	Settings           Settings `json:"settings"`
}

// Store 是 instances.json / manager.json 的并发安全访问入口。
type Store struct {
	mu sync.RWMutex

	dir           string // <data-root>/config
	instancesPath string
	managerPath   string

	instances []Instance
	selected  string
	settings  Settings

	// now 与 writeFile 用于测试注入（生产环境不修改）。
	now       func() time.Time
	writeFile func(path string, data []byte, perm os.FileMode) error
}

// Open 打开（必要时初始化）<dataRoot>/config 下的配置文件。
func Open(dataRoot string) (*Store, error) {
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
	s := &Store{
		dir:           dir,
		instancesPath: filepath.Join(dir, "instances.json"),
		managerPath:   filepath.Join(dir, "manager.json"),
		now:           time.Now,
		writeFile:     atomicWriteFile,
	}
	if err := s.loadInstances(); err != nil {
		return nil, err
	}
	if err := s.loadManager(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close 释放 Store。所有修改在返回前均已落盘，因此 Close 不执行额外 I/O。
func (s *Store) Close() error { return nil }

func (s *Store) loadInstances() error {
	data, err := os.ReadFile(s.instancesPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.instances = []Instance{}
		if err := s.persistInstancesLocked(); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取实例清单失败: %w", err)
	}
	var file instancesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("实例清单 %s 不是合法 JSON，请修复或备份后删除: %w", s.instancesPath, err)
	}
	if file.SchemaVersion != SchemaVersion {
		return fmt.Errorf("实例清单 %s 的 schemaVersion=%d 不受支持（期望 %d）", s.instancesPath, file.SchemaVersion, SchemaVersion)
	}
	instances := make([]Instance, 0, len(file.Instances))
	seenID := make(map[string]struct{}, len(file.Instances))
	seenPort := make(map[int]struct{}, len(file.Instances))
	for i, raw := range file.Instances {
		inst, err := validateStoredInstance(raw)
		if err != nil {
			return fmt.Errorf("实例清单 %s 第 %d 项无效: %w", s.instancesPath, i+1, err)
		}
		if _, dup := seenID[inst.ID]; dup {
			return fmt.Errorf("实例清单 %s 存在重复实例 ID %q", s.instancesPath, inst.ID)
		}
		if _, dup := seenPort[inst.Port]; dup {
			return fmt.Errorf("实例清单 %s 存在重复端口 %d", s.instancesPath, inst.Port)
		}
		seenID[inst.ID] = struct{}{}
		seenPort[inst.Port] = struct{}{}
		instances = append(instances, inst)
	}
	s.instances = instances
	return nil
}

func (s *Store) loadManager() error {
	data, err := os.ReadFile(s.managerPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.settings = defaultSettings()
		s.selected = ""
		if err := s.persistManagerLocked(); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取管理配置失败: %w", err)
	}
	var file managerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("管理配置 %s 不是合法 JSON，请修复或备份后删除: %w", s.managerPath, err)
	}
	if file.SchemaVersion != SchemaVersion {
		return fmt.Errorf("管理配置 %s 的 schemaVersion=%d 不受支持（期望 %d）", s.managerPath, file.SchemaVersion, SchemaVersion)
	}
	settings, err := normalizeSettings(file.Settings)
	if err != nil {
		return fmt.Errorf("管理配置 %s 无效: %w", s.managerPath, err)
	}
	s.settings = settings
	s.selected = file.SelectedInstanceID
	// 选中实例已被删除（或手工编辑产生悬空引用）时清空，不阻止启动。
	if s.selected != "" && !s.hasInstanceIDLocked(s.selected) {
		s.selected = ""
	}
	return nil
}

func (s *Store) hasInstanceIDLocked(id string) bool {
	for _, inst := range s.instances {
		if inst.ID == id {
			return true
		}
	}
	return false
}

// ListInstances 返回实例拷贝，顺序稳定（创建时间升序，同一毫秒按 ID 升序）。
func (s *Store) ListInstances() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Instance, len(s.instances))
	copy(out, s.instances)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GetInstance 返回指定实例的拷贝。
func (s *Store) GetInstance(id string) (Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.ID == id {
			return inst, true
		}
	}
	return Instance{}, false
}

// CreateInstance 校验并持久化一个新实例。
func (s *Store) CreateInstance(in CreateInstanceInput) (Instance, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return Instance{}, err
	}
	version, err := normalizeVersion(in.Version)
	if err != nil {
		return Instance{}, err
	}
	profile, err := normalizeProfile(in.Profile)
	if err != nil {
		return Instance{}, err
	}
	port, err := normalizePort(in.Port)
	if err != nil {
		return Instance{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.instances {
		if existing.Port == port {
			return Instance{}, fmt.Errorf("%w: 端口 %d 已分配给实例 %q", ErrInvalidInput, port, existing.Name)
		}
	}
	id, err := s.newUniqueIDLocked()
	if err != nil {
		return Instance{}, err
	}
	now := s.now().UnixMilli()
	inst := Instance{
		ID:        id,
		Name:      name,
		Version:   version,
		Profile:   profile,
		Port:      port,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.instances = append(s.instances, inst)
	if err := s.persistInstancesLocked(); err != nil {
		s.instances = s.instances[:len(s.instances)-1]
		return Instance{}, err
	}
	return inst, nil
}

// UpdateInstance 更新实例的 name/port；version/profile 创建后不可变。
func (s *Store) UpdateInstance(id string, in UpdateInstanceInput) (Instance, error) {
	var name *string
	if in.Name != nil {
		normalized, err := normalizeName(*in.Name)
		if err != nil {
			return Instance{}, err
		}
		name = &normalized
	}
	var port *int
	if in.Port != nil {
		normalized, err := normalizePort(*in.Port)
		if err != nil {
			return Instance{}, err
		}
		port = &normalized
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.instances {
		if s.instances[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Instance{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	old := s.instances[idx]
	updated := old
	changed := false
	if name != nil && *name != updated.Name {
		updated.Name = *name
		changed = true
	}
	if port != nil && *port != updated.Port {
		for i, other := range s.instances {
			if i != idx && other.Port == *port {
				return Instance{}, fmt.Errorf("%w: 端口 %d 已分配给实例 %q", ErrInvalidInput, *port, other.Name)
			}
		}
		updated.Port = *port
		changed = true
	}
	if !changed {
		return old, nil
	}
	updated.UpdatedAt = s.now().UnixMilli()
	s.instances[idx] = updated
	if err := s.persistInstancesLocked(); err != nil {
		s.instances[idx] = old
		return Instance{}, err
	}
	return updated, nil
}

// DeleteInstance 仅从持久层删除实例记录；实例目录由调用方负责清理。
func (s *Store) DeleteInstance(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.instances {
		if s.instances[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	oldInstances := s.instances
	oldSelected := s.selected
	s.instances = make([]Instance, 0, len(oldInstances)-1)
	s.instances = append(s.instances, oldInstances[:idx]...)
	s.instances = append(s.instances, oldInstances[idx+1:]...)
	if s.selected == id {
		s.selected = ""
	}
	if err := s.persistInstancesLocked(); err != nil {
		s.instances = oldInstances
		s.selected = oldSelected
		return err
	}
	if s.selected != oldSelected {
		if err := s.persistManagerLocked(); err != nil {
			// 实例记录已删除成功；manager.json 写失败时保持内存中“未选中”，
			// 磁盘上的悬空 selectedInstanceId 会在下次成功写入或重新 Open 时自愈。
			return err
		}
	}
	return nil
}

// SelectedInstanceID 返回当前选中的实例 ID，空字符串表示未选中。
func (s *Store) SelectedInstanceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selected
}

// SelectInstance 选中指定实例；传空字符串表示清除选中。
func (s *Store) SelectInstance(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" && !s.hasInstanceIDLocked(id) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	old := s.selected
	s.selected = id
	if err := s.persistManagerLocked(); err != nil {
		s.selected = old
		return err
	}
	return nil
}

// Settings 返回管理设置拷贝。
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// UpdateSettings 校验并持久化管理设置。
func (s *Store) UpdateSettings(settings Settings) error {
	normalized, err := normalizeSettings(settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.settings
	s.settings = normalized
	if err := s.persistManagerLocked(); err != nil {
		s.settings = old
		return err
	}
	return nil
}

func (s *Store) newUniqueIDLocked() (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := newInstanceID()
		if err != nil {
			return "", err
		}
		if !s.hasInstanceIDLocked(id) {
			return id, nil
		}
	}
	return "", errors.New("无法生成唯一实例 ID")
}

func newInstanceID() (string, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成实例 ID 失败: %w", err)
	}
	return "i-" + hex.EncodeToString(buf[:]), nil
}

func (s *Store) persistInstancesLocked() error {
	file := instancesFile{SchemaVersion: SchemaVersion, Instances: s.instances}
	if file.Instances == nil {
		file.Instances = []Instance{}
	}
	data, err := marshalJSON(file)
	if err != nil {
		return fmt.Errorf("序列化实例清单失败: %w", err)
	}
	if err := s.writeFile(s.instancesPath, data, 0o600); err != nil {
		return fmt.Errorf("写入实例清单失败: %w", err)
	}
	return nil
}

func (s *Store) persistManagerLocked() error {
	file := managerFile{SchemaVersion: SchemaVersion, SelectedInstanceID: s.selected, Settings: s.settings}
	data, err := marshalJSON(file)
	if err != nil {
		return fmt.Errorf("序列化管理配置失败: %w", err)
	}
	if err := s.writeFile(s.managerPath, data, 0o600); err != nil {
		return fmt.Errorf("写入管理配置失败: %w", err)
	}
	return nil
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
