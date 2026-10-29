// SPDX-License-Identifier: MIT

package artifact

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is a controllable clock for TTL tests.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestStore opens a store on a temporary directory with a fixed clock.
func newTestStore(t *testing.T) (*Store, string, *testClock) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "exports")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q) 失败: %v", dir, err)
	}
	clock := &testClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	s.now = clock.Now
	return s, dir, clock
}

// writeTestFile creates a file inside dir and returns its absolute path.
func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试文件 %s 失败: %v", path, err)
	}
	return path
}

// mustAdd writes content into dir and registers it as an artifact.
func mustAdd(t *testing.T, s *Store, dir, filename, content, contentType string) Artifact {
	t.Helper()
	path := writeTestFile(t, dir, filename, content)
	art, err := s.Add(filename, path, contentType, int64(len(content)))
	if err != nil {
		t.Fatalf("Add(%q) 失败: %v", filename, err)
	}
	return art
}

// readMetadata decodes artifacts.json from dir.
func readMetadata(t *testing.T, dir string) storeFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, metadataFilename))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", metadataFilename, err)
	}
	var file storeFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("解析 %s 失败: %v", metadataFilename, err)
	}
	return file
}

func TestTTLConstant(t *testing.T) {
	if TTL != 24*time.Hour {
		t.Fatalf("TTL = %v, 期望 24h", TTL)
	}
}

func TestOpenCreatesStoreDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "exports")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q) 失败: %v", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat 存储目录失败: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s 不是目录", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, metadataFilename)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("空存储不应创建 %s: %v", metadataFilename, err)
	}
	if _, ok := s.Get("000000000000", time.Now()); ok {
		t.Fatal("空存储 Get 返回了条目")
	}
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := Open("   "); err == nil {
		t.Fatal("Open(\"\") 未返回错误")
	}
}

func TestAddRoundTrip(t *testing.T) {
	s, dir, clock := newTestStore(t)
	const content = "dspack content"
	art := mustAdd(t, s, dir, "export.zip", content, "application/zip")

	if !artifactIDPattern.MatchString(art.ID) {
		t.Fatalf("artifact id = %q，期望 12 位小写十六进制", art.ID)
	}
	if art.Filename != "export.zip" || art.ContentType != "application/zip" {
		t.Fatalf("artifact = %+v", art)
	}
	if art.Size != int64(len(content)) {
		t.Fatalf("Size = %d，期望 %d", art.Size, len(content))
	}
	if art.CreatedAt != clock.Now().UnixMilli() {
		t.Fatalf("CreatedAt = %d，期望 %d", art.CreatedAt, clock.Now().UnixMilli())
	}
	if art.ExpiresAt-art.CreatedAt != int64(TTL/time.Millisecond) {
		t.Fatalf("TTL = %d ms，期望 %d ms", art.ExpiresAt-art.CreatedAt, int64(TTL/time.Millisecond))
	}
	if !filepath.IsAbs(art.Path) || !strings.HasPrefix(art.Path, dir) {
		t.Fatalf("Path = %q，期望位于 %q 内", art.Path, dir)
	}

	got, ok := s.Get(art.ID, clock.Now())
	if !ok || got != art {
		t.Fatalf("Get(%q) = %+v, %v；期望 %+v", art.ID, got, ok, art)
	}

	// artifacts.json carries exactly the documented fields (no createdAt).
	raw, err := os.ReadFile(filepath.Join(dir, metadataFilename))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", metadataFilename, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("解析 %s 失败: %v", metadataFilename, err)
	}
	if version, _ := document["schemaVersion"].(float64); version != schemaVersion {
		t.Fatalf("schemaVersion = %v，期望 %d", document["schemaVersion"], schemaVersion)
	}
	entries, _ := document["artifacts"].([]any)
	if len(entries) != 1 {
		t.Fatalf("artifacts = %v，期望 1 条", document["artifacts"])
	}
	entry, _ := entries[0].(map[string]any)
	wantKeys := []string{"id", "filename", "path", "size", "contentType", "expiresAt"}
	if len(entry) != len(wantKeys) {
		t.Fatalf("artifacts.json 条目字段 = %v，期望 %v", entry, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := entry[key]; !ok {
			t.Fatalf("artifacts.json 条目缺少 %q: %v", key, entry)
		}
	}
	if _, ok := entry["createdAt"]; ok {
		t.Fatalf("artifacts.json 不应持久化 createdAt: %v", entry)
	}

	// Reload: metadata survives, CreatedAt is derived from ExpiresAt.
	reloaded, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open 失败: %v", err)
	}
	got, ok = reloaded.Get(art.ID, clock.Now())
	if !ok {
		t.Fatalf("重新加载后 Get(%q) 失败", art.ID)
	}
	if got.Filename != art.Filename || got.Path != art.Path || got.Size != art.Size ||
		got.ContentType != art.ContentType || got.ExpiresAt != art.ExpiresAt || got.CreatedAt != art.CreatedAt {
		t.Fatalf("重新加载后条目 = %+v，期望 %+v", got, art)
	}
}

func TestAddDefaultsContentType(t *testing.T) {
	s, dir, _ := newTestStore(t)
	art := mustAdd(t, s, dir, "blob.bin", "raw", "")
	if art.ContentType != defaultContentType {
		t.Fatalf("ContentType = %q，期望 %q", art.ContentType, defaultContentType)
	}
}

func TestGetExpiry(t *testing.T) {
	s, dir, clock := newTestStore(t)
	art := mustAdd(t, s, dir, "a.zip", "data", "application/zip")
	if _, ok := s.Get(art.ID, clock.Now().Add(TTL-time.Millisecond)); !ok {
		t.Fatal("过期前 1ms Get 返回 false")
	}
	if _, ok := s.Get(art.ID, clock.Now().Add(TTL)); ok {
		t.Fatal("恰好过期时 Get 返回 true")
	}
	if _, ok := s.Get(art.ID, clock.Now().Add(TTL+time.Hour)); ok {
		t.Fatal("过期后 Get 返回 true")
	}
	if _, ok := s.Get("000000000000", clock.Now()); ok {
		t.Fatal("不存在的 id Get 返回 true")
	}
}

func TestCleanupRemovesExpiredEntriesAndFiles(t *testing.T) {
	s, dir, clock := newTestStore(t)
	first := mustAdd(t, s, dir, "a.zip", "aaa", "application/zip")
	second := mustAdd(t, s, dir, "b.zip", "bbb", "application/zip")

	if err := s.Cleanup(clock.Now()); err != nil {
		t.Fatalf("未过期时 Cleanup 失败: %v", err)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("未过期文件被删除: %v", err)
	}

	clock.Advance(TTL + time.Millisecond)
	if err := s.Cleanup(clock.Now()); err != nil {
		t.Fatalf("Cleanup 失败: %v", err)
	}
	for _, art := range []Artifact{first, second} {
		if _, ok := s.Get(art.ID, clock.Now()); ok {
			t.Fatalf("过期条目 %s 仍可 Get", art.ID)
		}
		if _, err := os.Stat(art.Path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("过期文件 %s 未删除: %v", art.Path, err)
		}
	}
	if file := readMetadata(t, dir); len(file.Artifacts) != 0 {
		t.Fatalf("artifacts.json 仍有过期条目: %+v", file.Artifacts)
	}
	// Second cleanup with nothing to do must not touch the index.
	before, err := os.Stat(filepath.Join(dir, metadataFilename))
	if err != nil {
		t.Fatalf("stat %s 失败: %v", metadataFilename, err)
	}
	if err := s.Cleanup(clock.Now()); err != nil {
		t.Fatalf("重复 Cleanup 失败: %v", err)
	}
	after, err := os.Stat(filepath.Join(dir, metadataFilename))
	if err != nil {
		t.Fatalf("stat %s 失败: %v", metadataFilename, err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("无可清理条目时 Cleanup 仍重写了 artifacts.json")
	}
}

func TestAddPrunesExpiredEntries(t *testing.T) {
	s, dir, clock := newTestStore(t)
	old := mustAdd(t, s, dir, "old.zip", "old", "application/zip")
	clock.Advance(TTL + time.Millisecond)
	fresh := mustAdd(t, s, dir, "new.zip", "new", "application/zip")

	if _, ok := s.Get(old.ID, clock.Now()); ok {
		t.Fatal("Add 未清理过期条目")
	}
	if _, err := os.Stat(old.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Add 未删除过期文件: %v", err)
	}
	if _, ok := s.Get(fresh.ID, clock.Now()); !ok {
		t.Fatal("Add 后新条目不可获取")
	}
	file := readMetadata(t, dir)
	if len(file.Artifacts) != 1 || file.Artifacts[0].ID != fresh.ID {
		t.Fatalf("artifacts.json = %+v，期望只含新条目", file.Artifacts)
	}
}

func TestAddValidation(t *testing.T) {
	s, dir, _ := newTestStore(t)
	valid := writeTestFile(t, dir, "valid.zip", "data")
	outside := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("写外部文件失败: %v", err)
	}
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("创建子目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "short.zip"), []byte("abc"), 0o600); err != nil {
		t.Fatalf("写大小测试文件失败: %v", err)
	}

	cases := []struct {
		name        string
		filename    string
		path        string
		contentType string
		size        int64
	}{
		{"空文件名", "", valid, "application/zip", 4},
		{"文件名含斜杠", "a/b.zip", valid, "application/zip", 4},
		{"文件名含反斜杠", "a\\b.zip", valid, "application/zip", 4},
		{"文件名为点点", "..", valid, "application/zip", 4},
		{"文件名含控制字符", "a\x01.zip", valid, "application/zip", 4},
		{"文件名超长", strings.Repeat("a", 256), valid, "application/zip", 4},
		{"空路径", "ok.zip", "", "application/zip", 4},
		{"相对路径", "ok.zip", "valid.zip", "application/zip", 4},
		{"目录外路径", "ok.zip", outside, "application/zip", 6},
		{"路径不存在", "ok.zip", filepath.Join(dir, "missing.zip"), "application/zip", 4},
		{"路径是目录", "ok.zip", subdir, "application/zip", 4},
		{"负数大小", "ok.zip", valid, "application/zip", -1},
		{"大小不一致", "ok.zip", filepath.Join(dir, "short.zip"), "application/zip", 99},
		{"非法 Content-Type", "ok.zip", valid, "not a media type;", 4},
		{"含换行 Content-Type", "ok.zip", valid, "application/zip\r\nX: y", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Add(tc.filename, tc.path, tc.contentType, tc.size); err == nil {
				t.Fatalf("Add(%q, %q, %q, %d) 未返回错误", tc.filename, tc.path, tc.contentType, tc.size)
			}
		})
	}
	if _, err := s.Add("ok.zip", valid, "application/zip", 0); err != nil {
		t.Fatalf("size=0 应读取文件大小: %v", err)
	}
}

func TestAddRejectsSymlink(t *testing.T) {
	s, dir, _ := newTestStore(t)
	target := writeTestFile(t, dir, "real.zip", "data")
	link := filepath.Join(dir, "link.zip")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("当前平台无法创建符号链接: %v", err)
	}
	if _, err := s.Add("link.zip", link, "application/zip", 4); err == nil {
		t.Fatal("Add 接受了指向目录内的符号链接")
	}

	outside := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("写外部文件失败: %v", err)
	}
	linkOutside := filepath.Join(dir, "out.zip")
	if err := os.Symlink(outside, linkOutside); err != nil {
		t.Skipf("当前平台无法创建符号链接: %v", err)
	}
	if _, err := s.Add("out.zip", linkOutside, "application/zip", 7); err == nil {
		t.Fatal("Add 接受了指向目录外的符号链接")
	}
}

func TestConcurrentAdd(t *testing.T) {
	s, dir, clock := newTestStore(t)
	const workers = 24
	results := make(chan Artifact, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "concurrent-" + string(rune('a'+i%26)) + ".zip"
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
				errs <- err
				return
			}
			art, err := s.Add(name, path, "application/zip", 4)
			if err != nil {
				errs <- err
				return
			}
			results <- art
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Add 失败: %v", err)
	}
	ids := make(map[string]bool)
	for art := range results {
		if ids[art.ID] {
			t.Fatalf("重复 artifact id: %s", art.ID)
		}
		ids[art.ID] = true
		if _, ok := s.Get(art.ID, clock.Now()); !ok {
			t.Fatalf("并发 Add 后 Get(%s) 失败", art.ID)
		}
	}
	if len(ids) != workers {
		t.Fatalf("成功 Add 数 = %d，期望 %d", len(ids), workers)
	}
	reloaded, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open 失败: %v", err)
	}
	for id := range ids {
		if _, ok := reloaded.Get(id, clock.Now()); !ok {
			t.Fatalf("重新加载后缺少 %s", id)
		}
	}
}

func TestAddSamePathReplacesOlderEntry(t *testing.T) {
	s, dir, clock := newTestStore(t)
	path := writeTestFile(t, dir, "shared.zip", "data")
	first, err := s.Add("first.zip", path, "application/zip", 4)
	if err != nil {
		t.Fatalf("首次 Add 失败: %v", err)
	}
	second, err := s.Add("second.zip", path, "application/zip", 4)
	if err != nil {
		t.Fatalf("重复路径 Add 失败: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("重复路径返回了相同 id: %s", first.ID)
	}
	if _, ok := s.Get(first.ID, clock.Now()); ok {
		t.Fatal("同一路径的旧条目未被替换")
	}
	if got, ok := s.Get(second.ID, clock.Now()); !ok || got.Filename != "second.zip" {
		t.Fatalf("新条目 = %+v, %v", got, ok)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("同一路径的共享文件被删除: %v", err)
	}
	clock.Advance(TTL + time.Millisecond)
	if err := s.Cleanup(clock.Now()); err != nil {
		t.Fatalf("Cleanup 失败: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("过期清理未删除文件: %v", err)
	}
}

func TestOpenRejectsCorruptMetadata(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"非法 JSON", "{\"schemaVersion\":1,"},
		{"不支持的 schemaVersion", "{\"schemaVersion\":2,\"artifacts\":[]}"},
		{"缺少 schemaVersion", "{\"artifacts\":[]}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "exports")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("创建目录失败: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, metadataFilename), []byte(tc.content), 0o600); err != nil {
				t.Fatalf("写损坏元数据失败: %v", err)
			}
			if _, err := Open(dir); err == nil {
				t.Fatalf("Open 未报告损坏的 %s (%s)", metadataFilename, tc.name)
			}
		})
	}
}

func TestOpenDropsUnsafeEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	okPath := writeTestFile(t, dir, "ok.zip", "data")
	outside := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("写外部文件失败: %v", err)
	}
	future := time.Now().Add(TTL).UnixMilli()
	document := map[string]any{
		"schemaVersion": schemaVersion,
		"artifacts": []any{
			map[string]any{"id": "0123456789ab", "filename": "ok.zip", "path": okPath, "size": 4, "contentType": "application/zip", "expiresAt": future},
			map[string]any{"id": "BAD-ID", "filename": "bad.zip", "path": okPath, "size": 4, "contentType": "application/zip", "expiresAt": future},
			map[string]any{"id": "aabbccddeeff", "filename": "out.zip", "path": outside, "size": 7, "contentType": "application/zip", "expiresAt": future},
		},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("编码元数据失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, metadataFilename), raw, 0o600); err != nil {
		t.Fatalf("写元数据失败: %v", err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if _, ok := s.Get("0123456789ab", time.Now()); !ok {
		t.Fatal("合法条目被丢弃")
	}
	for _, id := range []string{"BAD-ID", "aabbccddeeff"} {
		if _, ok := s.Get(id, time.Now()); ok {
			t.Fatalf("不安全条目 %s 未被丢弃", id)
		}
	}
	file := readMetadata(t, dir)
	if len(file.Artifacts) != 1 || file.Artifacts[0].ID != "0123456789ab" {
		t.Fatalf("清理后的 artifacts.json = %+v", file.Artifacts)
	}
}

func TestNoTemporaryFilesLeft(t *testing.T) {
	s, dir, _ := newTestStore(t)
	for i := 0; i < 5; i++ {
		mustAdd(t, s, dir, "file-"+string(rune('a'+i))+".zip", "data", "application/zip")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("残留临时文件: %s", entry.Name())
		}
	}
}
