// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), "data")
	s, err := Open(dataRoot)
	if err != nil {
		t.Fatalf("Open(%q) 失败: %v", dataRoot, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dataRoot
}

// assertStableOrder 校验 ListInstances 的稳定顺序：创建时间升序，同一时间按 ID 升序。
func assertStableOrder(t *testing.T, list []Instance) {
	t.Helper()
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt < list[i-1].CreatedAt {
			t.Fatalf("ListInstances 未按创建时间升序: %+v", list)
		}
		if list[i].CreatedAt == list[i-1].CreatedAt && list[i].ID < list[i-1].ID {
			t.Fatalf("同一创建时间未按 ID 升序: %+v", list)
		}
	}
}

// instanceByID 从列表找实例，避免依赖同一毫秒创建时的相对顺序。
func instanceByID(t *testing.T, list []Instance, id string) Instance {
	t.Helper()
	for _, inst := range list {
		if inst.ID == id {
			return inst
		}
	}
	t.Fatalf("列表中没有实例 %s: %+v", id, list)
	return Instance{}
}

func mustCreate(t *testing.T, s *Store, name string, port int) Instance {
	t.Helper()
	inst, err := s.CreateInstance(CreateInstanceInput{
		Name:    name,
		Version: "0.2.1-rc.3",
		Profile: "web",
		Port:    port,
	})
	if err != nil {
		t.Fatalf("CreateInstance(%q, %d) 失败: %v", name, port, err)
	}
	return inst
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return data
}

func TestOpenInitializesDefaults(t *testing.T) {
	s, dataRoot := newTestStore(t)
	configDir := filepath.Join(dataRoot, "config")
	for _, name := range []string{"instances.json", "manager.json"} {
		if _, err := os.Stat(filepath.Join(configDir, name)); err != nil {
			t.Fatalf("%s 未被初始化: %v", name, err)
		}
	}
	if got := s.ListInstances(); len(got) != 0 {
		t.Fatalf("新库应有 0 个实例，实际 %d", len(got))
	}
	want := Settings{Proxy: "", Registry: "https://registry.npmjs.org", DirectAccessSSHUser: "root"}
	if got := s.Settings(); got != want {
		t.Fatalf("默认设置 = %+v，期望 %+v", got, want)
	}
	if got := s.SelectedInstanceID(); got != "" {
		t.Fatalf("默认选中 = %q，期望空", got)
	}
	var instances instancesFile
	if err := json.Unmarshal(readFile(t, filepath.Join(configDir, "instances.json")), &instances); err != nil {
		t.Fatalf("instances.json 不是合法 JSON: %v", err)
	}
	if instances.SchemaVersion != SchemaVersion || instances.Instances == nil || len(instances.Instances) != 0 {
		t.Fatalf("instances.json 初值不符: %+v", instances)
	}
}

func TestOpenEmptyDataRoot(t *testing.T) {
	if _, err := Open("   "); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Open(空白) 应返回 ErrInvalidInput，实际 %v", err)
	}
}

func TestCreateGetListRoundtrip(t *testing.T) {
	s, _ := newTestStore(t)
	first := mustCreate(t, s, "  工作实例 ", 3080)
	if first.Name != "工作实例" {
		t.Fatalf("名称应去除首尾空白，实际 %q", first.Name)
	}
	if !regexp.MustCompile(`^i-[0-9a-f]{12}$`).MatchString(first.ID) {
		t.Fatalf("实例 ID %q 不符合 i-+12hex", first.ID)
	}
	if first.Version != "0.2.1-rc.3" || first.Profile != "web" || first.Port != 3080 {
		t.Fatalf("字段不符: %+v", first)
	}
	if first.CreatedAt <= 0 || first.UpdatedAt != first.CreatedAt {
		t.Fatalf("时间戳不符: %+v", first)
	}
	second := mustCreate(t, s, "第二个", 3081)

	got, ok := s.GetInstance(first.ID)
	if !ok || !reflect.DeepEqual(got, first) {
		t.Fatalf("GetInstance(%q) = %+v, %v；期望 %+v", first.ID, got, ok, first)
	}
	if _, ok := s.GetInstance("i-000000000000"); ok {
		t.Fatal("不存在的实例应返回 false")
	}
	list := s.ListInstances()
	if len(list) != 2 {
		t.Fatalf("ListInstances 长度 = %d，期望 2", len(list))
	}
	assertStableOrder(t, list)
	if got := instanceByID(t, list, first.ID); !reflect.DeepEqual(got, first) {
		t.Fatalf("ListInstances 中 first 不符: %+v", got)
	}
	if got := instanceByID(t, list, second.ID); !reflect.DeepEqual(got, second) {
		t.Fatalf("ListInstances 中 second 不符: %+v", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	s, err := Open(dataRoot)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	one := mustCreate(t, s, "甲", 3080)
	two := mustCreate(t, s, "乙", 3081)
	if err := s.SelectInstance(two.ID); err != nil {
		t.Fatalf("SelectInstance 失败: %v", err)
	}
	if err := s.UpdateSettings(Settings{
		Proxy:               "http://127.0.0.1:7890",
		Registry:            "https://registry.npmmirror.com",
		DirectAccessSSHUser: "ubuntu",
	}); err != nil {
		t.Fatalf("UpdateSettings 失败: %v", err)
	}
	if _, err := s.UpdateInstance(one.ID, UpdateInstanceInput{Name: strPtr("甲改名")}); err != nil {
		t.Fatalf("UpdateInstance 失败: %v", err)
	}
	_ = s.Close()

	reopened, err := Open(dataRoot)
	if err != nil {
		t.Fatalf("重新 Open 失败: %v", err)
	}
	defer reopened.Close()
	list := reopened.ListInstances()
	if len(list) != 2 {
		t.Fatalf("重开后实例数 = %d，期望 2", len(list))
	}
	assertStableOrder(t, list)
	gotOne := instanceByID(t, list, one.ID)
	if gotOne.Name != "甲改名" || gotOne.Version != "0.2.1-rc.3" || gotOne.Profile != "web" || gotOne.Port != 3080 {
		t.Fatalf("重开后实例不符: %+v", gotOne)
	}
	if gotTwo := instanceByID(t, list, two.ID); gotTwo.Name != "乙" || gotTwo.Port != 3081 {
		t.Fatalf("重开后实例不符: %+v", gotTwo)
	}
	if got := reopened.SelectedInstanceID(); got != two.ID {
		t.Fatalf("重开后选中 = %q，期望 %q", got, two.ID)
	}
	wantSettings := Settings{Proxy: "http://127.0.0.1:7890", Registry: "https://registry.npmmirror.com", DirectAccessSSHUser: "ubuntu"}
	if got := reopened.Settings(); got != wantSettings {
		t.Fatalf("重开后设置 = %+v，期望 %+v", got, wantSettings)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestUpdateInstance(t *testing.T) {
	s, _ := newTestStore(t)
	inst := mustCreate(t, s, "原名", 3080)
	other := mustCreate(t, s, "其他", 3081)

	if _, err := s.UpdateInstance("i-000000000000", UpdateInstanceInput{Name: strPtr("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在实例应返回 ErrNotFound，实际 %v", err)
	}

	updated, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Name: strPtr(" 新名 "), Port: intPtr(3082)})
	if err != nil {
		t.Fatalf("UpdateInstance 失败: %v", err)
	}
	if updated.Name != "新名" || updated.Port != 3082 {
		t.Fatalf("更新结果不符: %+v", updated)
	}
	if updated.Version != inst.Version || updated.Profile != inst.Profile || updated.ID != inst.ID || updated.CreatedAt != inst.CreatedAt {
		t.Fatalf("更新不应改变不可变字段: %+v", updated)
	}
	if updated.UpdatedAt < inst.UpdatedAt {
		t.Fatalf("UpdatedAt 应不小于原值: %d < %d", updated.UpdatedAt, inst.UpdatedAt)
	}

	// name/port 未变化时不修改 UpdatedAt，也不写盘。
	before := updated.UpdatedAt
	same, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Name: strPtr("新名"), Port: intPtr(3082)})
	if err != nil {
		t.Fatalf("无变化更新失败: %v", err)
	}
	if same.UpdatedAt != before {
		t.Fatalf("无变化更新不应刷新 UpdatedAt: %d != %d", same.UpdatedAt, before)
	}

	// 端口冲突。
	if _, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Port: intPtr(other.Port)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("端口冲突应返回 ErrInvalidInput，实际 %v", err)
	}
	got, _ := s.GetInstance(inst.ID)
	if got.Port != 3082 {
		t.Fatalf("端口冲突后端口应保持不变，实际 %d", got.Port)
	}
	if _, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Port: intPtr(65536)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("非法端口应返回 ErrInvalidInput，实际 %v", err)
	}
	if _, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Name: strPtr("a/b")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("非法名称应返回 ErrInvalidInput，实际 %v", err)
	}
}

func TestDeleteInstance(t *testing.T) {
	s, _ := newTestStore(t)
	one := mustCreate(t, s, "一", 3080)
	two := mustCreate(t, s, "二", 3081)
	if err := s.SelectInstance(two.ID); err != nil {
		t.Fatalf("SelectInstance 失败: %v", err)
	}
	if err := s.DeleteInstance(two.ID); err != nil {
		t.Fatalf("DeleteInstance 失败: %v", err)
	}
	if _, ok := s.GetInstance(two.ID); ok {
		t.Fatal("删除后 GetInstance 应返回 false")
	}
	if got := s.SelectedInstanceID(); got != "" {
		t.Fatalf("删除选中实例后选中应清空，实际 %q", got)
	}
	if err := s.DeleteInstance("i-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在实例应返回 ErrNotFound，实际 %v", err)
	}
	if list := s.ListInstances(); len(list) != 1 || list[0].ID != one.ID {
		t.Fatalf("删除后列表不符: %+v", list)
	}
}

func TestSelectInstance(t *testing.T) {
	s, _ := newTestStore(t)
	inst := mustCreate(t, s, "一", 3080)
	if err := s.SelectInstance("i-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("选中不存在实例应返回 ErrNotFound，实际 %v", err)
	}
	if err := s.SelectInstance(inst.ID); err != nil {
		t.Fatalf("SelectInstance 失败: %v", err)
	}
	if got := s.SelectedInstanceID(); got != inst.ID {
		t.Fatalf("选中 = %q，期望 %q", got, inst.ID)
	}
	if err := s.SelectInstance(""); err != nil {
		t.Fatalf("清除选中失败: %v", err)
	}
	if got := s.SelectedInstanceID(); got != "" {
		t.Fatalf("清除后选中 = %q，期望空", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	s, _ := newTestStore(t)

	// 空值恢复默认。
	if err := s.UpdateSettings(Settings{}); err != nil {
		t.Fatalf("空设置应恢复默认: %v", err)
	}
	if got := s.Settings(); got != defaultSettings() {
		t.Fatalf("空设置 = %+v，期望默认 %+v", got, defaultSettings())
	}

	valid := []Settings{
		{},
		{Registry: "https://registry.npmmirror.com"},
		{Registry: "http://registry.example.com:8080/path", DirectAccessSSHUser: "user.name-_1"},
		{Proxy: "http://127.0.0.1:7890", Registry: "https://registry.npmjs.org", DirectAccessSSHUser: "root"},
		{Proxy: "HTTP://proxy.local:3128"},
		{Proxy: "http://[::1]:3128"},
	}
	for _, settings := range valid {
		if err := s.UpdateSettings(settings); err != nil {
			t.Fatalf("合法设置 %+v 被拒绝: %v", settings, err)
		}
	}

	beforeInvalid := s.Settings()
	invalid := []Settings{
		{Registry: "ftp://registry.example.com"},
		{Registry: "registry.example.com"},
		{Registry: "https://user:pass@registry.example.com"},
		{Registry: "https://"},
		{Proxy: "https://127.0.0.1:7890"}, // 规范要求 http://host:port
		{Proxy: "http://127.0.0.1"},       // 缺端口
		{Proxy: "http://user:pass@127.0.0.1:7890"},
		{Proxy: "http://127.0.0.1:99999"},
		{Proxy: "socks5://127.0.0.1:7890"},
		{Proxy: "http://127.0.0.1:7890/path"},
		{DirectAccessSSHUser: "root;rm -rf /"},
		{DirectAccessSSHUser: "root user"},
	}
	for _, settings := range invalid {
		if err := s.UpdateSettings(settings); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("非法设置 %+v 应返回 ErrInvalidInput，实际 %v", settings, err)
		}
	}
	if got := s.Settings(); got != beforeInvalid {
		t.Fatalf("非法更新后设置应保持为 %+v，实际 %+v", beforeInvalid, got)
	}
}

func TestCreateValidationTable(t *testing.T) {
	s, _ := newTestStore(t)
	longName := strings.Repeat("名", 41)
	exactName := strings.Repeat("名", 40)

	validVersions := []string{"0.0.0", "0.2.1-rc.3", "1.2.3-0", "1.2.3-alpha.1", "1.2.3-alpha.1+build.5", "10.20.30"}
	for i, version := range validVersions {
		inst, err := s.CreateInstance(CreateInstanceInput{
			Name:    fmt.Sprintf("v%d", i),
			Version: version,
			Profile: "web",
			Port:    30000 + i,
		})
		if err != nil {
			t.Fatalf("合法版本 %q 被拒绝: %v", version, err)
		}
		if inst.Version != version {
			t.Fatalf("版本保存不符: %q != %q", inst.Version, version)
		}
	}

	tests := []struct {
		name  string
		input CreateInstanceInput
	}{
		{"名称空", CreateInstanceInput{Name: "", Version: "1.0.0", Profile: "web", Port: 31000}},
		{"名称全空白", CreateInstanceInput{Name: "   ", Version: "1.0.0", Profile: "web", Port: 31001}},
		{"名称超长", CreateInstanceInput{Name: longName, Version: "1.0.0", Profile: "web", Port: 31002}},
		{"名称含斜杠", CreateInstanceInput{Name: "a/b", Version: "1.0.0", Profile: "web", Port: 31003}},
		{"名称含反斜杠", CreateInstanceInput{Name: `a\b`, Version: "1.0.0", Profile: "web", Port: 31004}},
		{"名称含控制字符", CreateInstanceInput{Name: "a\x00b", Version: "1.0.0", Profile: "web", Port: 31005}},
		{"名称含换行", CreateInstanceInput{Name: "a\nb", Version: "1.0.0", Profile: "web", Port: 31006}},
		{"版本空", CreateInstanceInput{Name: "x", Version: "", Profile: "web", Port: 31007}},
		{"版本两位", CreateInstanceInput{Name: "x", Version: "1.2", Profile: "web", Port: 31008}},
		{"版本前导零", CreateInstanceInput{Name: "x", Version: "01.2.3", Profile: "web", Port: 31009}},
		{"版本带v", CreateInstanceInput{Name: "x", Version: "v1.2.3", Profile: "web", Port: 31010}},
		{"版本带空格", CreateInstanceInput{Name: "x", Version: " 1.2.3", Profile: "web", Port: 31011}},
		{"版本空 prerelease", CreateInstanceInput{Name: "x", Version: "1.2.3-", Profile: "web", Port: 31012}},
		{"版本数字 prerelease 前导零", CreateInstanceInput{Name: "x", Version: "1.2.3-01", Profile: "web", Port: 31013}},
		{"版本 build 空段", CreateInstanceInput{Name: "x", Version: "1.2.3+bad..x", Profile: "web", Port: 31014}},
		{"profile 空", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "", Port: 31015}},
		{"profile 含空格", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web profile", Port: 31016}},
		{"profile 含斜杠", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web/x", Port: 31017}},
		{"profile 含冒号", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web:x", Port: 31018}},
		{"profile 含中文", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "网页", Port: 31019}},
		{"端口 0", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web", Port: 0}},
		{"端口负数", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web", Port: -1}},
		{"端口过大", CreateInstanceInput{Name: "x", Version: "1.2.3", Profile: "web", Port: 65536}},
	}
	for _, tc := range tests {
		if _, err := s.CreateInstance(tc.input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: 应返回 ErrInvalidInput，实际 %v", tc.name, err)
		}
	}

	// 边界合法值：40 个字符的名称与端口 1/65535。
	if _, err := s.CreateInstance(CreateInstanceInput{Name: exactName, Version: "1.0.0", Profile: "p-1_2.3", Port: 1}); err != nil {
		t.Fatalf("40 字符名称/端口 1 应合法: %v", err)
	}
	if _, err := s.CreateInstance(CreateInstanceInput{Name: "p", Version: "1.0.0", Profile: "web", Port: 65535}); err != nil {
		t.Fatalf("端口 65535 应合法: %v", err)
	}
}

func TestDuplicatePortRejected(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreate(t, s, "一", 3080)
	if _, err := s.CreateInstance(CreateInstanceInput{Name: "二", Version: "1.0.0", Profile: "web", Port: 3080}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("重复端口应返回 ErrInvalidInput，实际 %v", err)
	}
	if list := s.ListInstances(); len(list) != 1 {
		t.Fatalf("重复端口不应创建实例，实际 %d", len(list))
	}
}

func TestCorruptFilesRejectedNotOverwritten(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
	}{
		{"instances 非法 JSON", "instances.json", "{not json"},
		{"instances schemaVersion 2", "instances.json", `{"schemaVersion":2,"instances":[]}`},
		{"instances 缺 schemaVersion", "instances.json", `{"instances":[]}`},
		{"instances 字段类型错误", "instances.json", `{"schemaVersion":1,"instances":[{"id":"i-000000000000","name":"x","version":1,"profile":"web","port":3080}]}`},
		{"instances 重复端口", "instances.json", `{"schemaVersion":1,"instances":[
			{"id":"i-000000000000","name":"a","version":"1.0.0","profile":"web","port":3080,"createdAt":1,"updatedAt":1},
			{"id":"i-000000000001","name":"b","version":"1.0.0","profile":"web","port":3080,"createdAt":1,"updatedAt":1}]}`},
		{"instances 非法 ID", "instances.json", `{"schemaVersion":1,"instances":[{"id":"bad","name":"a","version":"1.0.0","profile":"web","port":3080}]}`},
		{"manager 非法 JSON", "manager.json", "nope"},
		{"manager schemaVersion 2", "manager.json", `{"schemaVersion":2}`},
		{"manager registry 非法", "manager.json", `{"schemaVersion":1,"selectedInstanceId":"","settings":{"proxy":"","registry":"ftp://x","directAccessSshUser":"root"}}`},
		{"manager proxy 非法", "manager.json", `{"schemaVersion":1,"selectedInstanceId":"","settings":{"proxy":"127.0.0.1:7890","registry":"https://registry.npmjs.org","directAccessSshUser":"root"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot := filepath.Join(t.TempDir(), "data")
			configDir := filepath.Join(dataRoot, "config")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatalf("创建目录失败: %v", err)
			}
			path := filepath.Join(configDir, tc.file)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("写入损坏文件失败: %v", err)
			}
			if _, err := Open(dataRoot); err == nil {
				t.Fatalf("损坏的 %s 应导致 Open 失败", tc.file)
			}
			if got := string(readFile(t, path)); got != tc.content {
				t.Fatalf("Open 失败后不应改写损坏文件：\n got=%q\nwant=%q", got, tc.content)
			}
		})
	}
}

func TestDanglingSelectionClearedOnOpen(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	configDir := filepath.Join(dataRoot, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	instances := `{"schemaVersion":1,"instances":[{"id":"i-000000000000","name":"a","version":"1.0.0","profile":"web","port":3080,"createdAt":1,"updatedAt":1}]}`
	manager := `{"schemaVersion":1,"selectedInstanceId":"i-999999999999","settings":{"proxy":"","registry":"https://registry.npmjs.org","directAccessSshUser":"root"}}`
	if err := os.WriteFile(filepath.Join(configDir, "instances.json"), []byte(instances), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "manager.json"), []byte(manager), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dataRoot)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()
	if got := s.SelectedInstanceID(); got != "" {
		t.Fatalf("悬空选中应被清空，实际 %q", got)
	}
}

func TestAtomicWriteFailurePreservesOldData(t *testing.T) {
	s, dataRoot := newTestStore(t)
	inst := mustCreate(t, s, "一", 3080)
	instancesPath := filepath.Join(dataRoot, "config", "instances.json")
	managerPath := filepath.Join(dataRoot, "config", "manager.json")
	beforeInstances := readFile(t, instancesPath)
	beforeManager := readFile(t, managerPath)

	s.writeFile = func(string, []byte, os.FileMode) error { return errors.New("磁盘已满") }

	if _, err := s.CreateInstance(CreateInstanceInput{Name: "二", Version: "1.0.0", Profile: "web", Port: 3081}); err == nil {
		t.Fatal("写入失败时 CreateInstance 应返回错误")
	}
	if _, err := s.UpdateInstance(inst.ID, UpdateInstanceInput{Name: strPtr("新名")}); err == nil {
		t.Fatal("写入失败时 UpdateInstance 应返回错误")
	}
	if err := s.DeleteInstance(inst.ID); err == nil {
		t.Fatal("写入失败时 DeleteInstance 应返回错误")
	}
	if err := s.SelectInstance(inst.ID); err == nil {
		t.Fatal("写入失败时 SelectInstance 应返回错误")
	}
	if err := s.UpdateSettings(Settings{Registry: "https://registry.npmmirror.com"}); err == nil {
		t.Fatal("写入失败时 UpdateSettings 应返回错误")
	}

	if got := readFile(t, instancesPath); string(got) != string(beforeInstances) {
		t.Fatalf("写失败后 instances.json 被破坏:\n got=%s\nwant=%s", got, beforeInstances)
	}
	if got := readFile(t, managerPath); string(got) != string(beforeManager) {
		t.Fatalf("写失败后 manager.json 被破坏:\n got=%s\nwant=%s", got, beforeManager)
	}
	list := s.ListInstances()
	if len(list) != 1 || list[0] != inst {
		t.Fatalf("写失败后内存状态应回滚，实际 %+v", list)
	}
	if got := s.SelectedInstanceID(); got != "" {
		t.Fatalf("写失败后选中状态应回滚，实际 %q", got)
	}
	if got := s.Settings(); got != defaultSettings() {
		t.Fatalf("写失败后设置应回滚，实际 %+v", got)
	}

	// 恢复写能力后旧数据仍可正常使用。
	s.writeFile = atomicWriteFile
	if _, err := s.CreateInstance(CreateInstanceInput{Name: "二", Version: "1.0.0", Profile: "web", Port: 3081}); err != nil {
		t.Fatalf("恢复写入后创建失败: %v", err)
	}
	if list := s.ListInstances(); len(list) != 2 {
		t.Fatalf("恢复后应有 2 个实例，实际 %d", len(list))
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	_, dataRoot := newTestStore(t)
	s, err := Open(dataRoot)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()
	mustCreate(t, s, "一", 3080)
	entries, err := os.ReadDir(filepath.Join(dataRoot, "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("原子写残留临时文件: %s", entry.Name())
		}
	}
	for _, entry := range entries {
		data := readFile(t, filepath.Join(dataRoot, "config", entry.Name()))
		if !json.Valid(data) {
			t.Fatalf("%s 不是合法 JSON: %s", entry.Name(), data)
		}
	}
}

func TestListStableOrderAndCopy(t *testing.T) {
	s, _ := newTestStore(t)
	fixed := time.UnixMilli(1750000000000)
	s.now = func() time.Time { return fixed }
	first := mustCreate(t, s, "一", 3080)
	second := mustCreate(t, s, "二", 3081)
	third := mustCreate(t, s, "三", 3082)

	list1 := s.ListInstances()
	list2 := s.ListInstances()
	if !reflect.DeepEqual(list1, list2) {
		t.Fatalf("同一数据两次 ListInstances 不一致: %+v vs %+v", list1, list2)
	}
	if !(list1[0].ID < list1[1].ID && list1[1].ID < list1[2].ID) {
		t.Fatalf("创建时间相同时应按 ID 升序: %+v", list1)
	}
	list1[0].Name = "被外部修改"
	if got, _ := s.GetInstance(first.ID); got.Name != "一" {
		t.Fatalf("ListInstances 应返回拷贝，实际 %q", got.Name)
	}
	// 重新排序检查 CreatedAt 升序。
	s.now = func() time.Time { return fixed.Add(time.Second) }
	fourth := mustCreate(t, s, "四", 3083)
	ordered := s.ListInstances()
	last := ordered[len(ordered)-1]
	if last.ID != fourth.ID {
		t.Fatalf("应按创建时间升序，最后一项=%+v，期望 %s", last, fourth.ID)
	}
	if second.ID == third.ID || first.ID == second.ID {
		t.Fatal("ID 不应重复")
	}
}

func TestIDFormatAndUniqueness(t *testing.T) {
	s, _ := newTestStore(t)
	seen := make(map[string]struct{}, 30)
	for i := 0; i < 30; i++ {
		inst := mustCreate(t, s, fmt.Sprintf("实例%d", i), 32000+i)
		if !regexp.MustCompile(`^i-[0-9a-f]{12}$`).MatchString(inst.ID) {
			t.Fatalf("实例 ID %q 格式无效", inst.ID)
		}
		if _, dup := seen[inst.ID]; dup {
			t.Fatalf("实例 ID 重复: %s", inst.ID)
		}
		seen[inst.ID] = struct{}{}
	}
}

func TestConcurrentAccess(t *testing.T) {
	s, _ := newTestStore(t)
	const writers = 4
	const perWriter = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter+16)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_, err := s.CreateInstance(CreateInstanceInput{
					Name:    fmt.Sprintf("g%d-%d", w, i),
					Version: "0.2.1-rc.3",
					Profile: "web",
					Port:    33000 + w*perWriter + i,
				})
				if err != nil {
					errs <- err
				}
				_ = s.ListInstances()
				_ = s.Settings()
				_ = s.SelectedInstanceID()
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for _, inst := range s.ListInstances() {
					if _, ok := s.GetInstance(inst.ID); !ok {
						errs <- fmt.Errorf("已存在实例 %s 在 GetInstance 中缺失", inst.ID)
					}
				}
				_ = s.SelectInstance("")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发访问出错: %v", err)
	}
	list := s.ListInstances()
	if len(list) != writers*perWriter {
		t.Fatalf("并发创建后实例数 = %d，期望 %d", len(list), writers*perWriter)
	}
	ports := make(map[int]struct{}, len(list))
	for _, inst := range list {
		if _, dup := ports[inst.Port]; dup {
			t.Fatalf("并发创建出现重复端口 %d", inst.Port)
		}
		ports[inst.Port] = struct{}{}
	}
}
