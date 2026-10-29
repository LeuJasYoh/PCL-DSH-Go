// SPDX-License-Identifier: MIT

package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

type fakePackRuntime struct {
	installed map[string]bool
	installs  int
}

func (f *fakePackRuntime) Installed(version string) bool { return f.installed[version] }

func (f *fakePackRuntime) Install(_ context.Context, version string, _ func(string)) (runtime.InstallResult, error) {
	f.installs++
	return runtime.InstallResult{Version: version, EntryPath: "/fake/entry.js"}, nil
}

type fakePackApp struct {
	created []store.CreateInstanceInput
	deleted []string
}

func (f *fakePackApp) CreateInstance(_ context.Context, in store.CreateInstanceInput) (store.Instance, error) {
	f.created = append(f.created, in)
	return store.Instance{ID: "i-000000000000", Name: in.Name, Version: in.Version, Profile: in.Profile, Port: in.Port}, nil
}

func (f *fakePackApp) DeleteInstance(_ context.Context, instanceID string) (string, error) {
	f.deleted = append(f.deleted, instanceID)
	return "/tmp/trash/" + instanceID, nil
}

func (f *fakePackApp) RestoreProfileDependencies(context.Context, string, string, func(string)) error {
	return nil
}

func (f *fakePackApp) EntryPath(string) (string, error) { return "/fake/entry.js", nil }

type fakePackArtifacts struct {
	files []string
}

func (f *fakePackArtifacts) Add(filename, path, contentType string, size int64) (artifact.Artifact, error) {
	f.files = append(f.files, filename)
	return artifact.Artifact{ID: "0123456789ab", Filename: filename, Path: path, ContentType: contentType, Size: size}, nil
}

func newPackTestService(t *testing.T) (*Service, config.Dirs, *fakePackRuntime, *fakePackApp, *fakePackArtifacts) {
	t.Helper()
	root := t.TempDir()
	dirs := config.Dirs{
		DataRoot:     root,
		ConfigDir:    filepath.Join(root, "config"),
		InstancesDir: filepath.Join(root, "instances"),
		RuntimesDir:  filepath.Join(root, "runtimes"),
		CacheDir:     filepath.Join(root, "cache"),
		LogsDir:      filepath.Join(root, "logs"),
		TrashDir:     filepath.Join(root, "trash"),
	}
	for _, dir := range []string{dirs.ConfigDir, dirs.InstancesDir, dirs.RuntimesDir, dirs.CacheDir, dirs.LogsDir, dirs.TrashDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("创建目录 %s 失败: %v", dir, err)
		}
	}
	instanceStore, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = instanceStore.Close() })
	fakeRuntime := &fakePackRuntime{installed: map[string]bool{"0.2.1-rc.3": true}}
	fakeApp := &fakePackApp{}
	fakeArtifacts := &fakePackArtifacts{}
	service, err := New(Options{
		DataRoot:  dirs.DataRoot,
		Dirs:      dirs,
		Store:     instanceStore,
		Runtime:   fakeRuntime,
		App:       fakeApp,
		Artifacts: fakeArtifacts,
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	if err != nil {
		t.Fatalf("pack.New: %v", err)
	}
	return service, dirs, fakeRuntime, fakeApp, fakeArtifacts
}

func zipArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("创建 ZIP 条目 %q 失败: %v", name, err)
		}
		if _, err := entry.Write(entries[name]); err != nil {
			t.Fatalf("写入 ZIP 条目 %q 失败: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 ZIP 失败: %v", err)
	}
	return buffer.Bytes()
}

func previewArchive(t *testing.T, service *Service, name string, entries map[string][]byte) (Preview, error) {
	t.Helper()
	return service.Preview(context.Background(), bytes.NewReader(zipArchive(t, entries)), name)
}

func requirePackAPIError(t *testing.T, err error, wantStatus int, wantSubstring string) {
	t.Helper()
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("错误不是 *apierr.Error: %v", err)
	}
	if apiErr.Status != wantStatus {
		t.Fatalf("状态码 = %d，期望 %d（错误 %v）", apiErr.Status, wantStatus, err)
	}
	if wantSubstring != "" && !strings.Contains(apiErr.Message, wantSubstring) {
		t.Fatalf("错误消息 %q 不含 %q", apiErr.Message, wantSubstring)
	}
}

func validDSPackProfile() map[string][]byte {
	return map[string][]byte{
		"dspack.json":         []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json":       []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"demo-pack\",\"version\":\"1.2.3\",\"dshVersion\":\"0.2.1-rc.3\",\"profileName\":\"demo\",\"bundles\":[],\"dependencies\":{}}"),
		"overrides/notes.txt": []byte("hello"),
	}
}

func validDSPackHome() map[string][]byte {
	return map[string][]byte{
		"dspack.json":        []byte("{\"format\":\"dspack\",\"version\":3}"),
		"manifest.json":      []byte("{\"manifestVersion\":5,\"type\":\"dshhome\",\"name\":\"home-pack\",\"version\":\"2.0.0\",\"profiles\":{\"work\":{\"bundles\":[],\"dependencies\":{}},\"play\":{\"bundles\":[],\"dependencies\":{}}},\"defaultProfile\":\"work\"}"),
		"overrides/work.txt": []byte("work"),
	}
}

func validOvertureSnapshot() map[string][]byte {
	return map[string][]byte{
		"dsh-snapshot.json":                     []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"),
		"profiles/work/profile.yaml":            []byte("dshVersion: 0.2.1-rc.3"),
		"profiles/work/package.json":            []byte("{\"dependencies\":{}}"),
		"profiles/work/node_modules/x/index.js": []byte("module.exports = 1"),
	}
}

func TestPreviewDSPackProfile(t *testing.T) {
	service, dirs, _, _, _ := newPackTestService(t)
	preview, err := previewArchive(t, service, "demo.dspack.zip", validDSPackProfile())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if preview.Format != FormatDSPack || preview.Name != "demo-pack" || preview.Version != "1.2.3" {
		t.Fatalf("Preview 字段不符: %+v", preview)
	}
	if preview.DSHVersion != "0.2.1-rc.3" || len(preview.Profiles) != 1 || preview.Profiles[0] != "demo" {
		t.Fatalf("Preview profile/dshVersion 不符: %+v", preview)
	}
	if preview.UnpackedBytes <= 0 {
		t.Fatalf("UnpackedBytes = %d，期望 > 0", preview.UnpackedBytes)
	}
	if preview.ExpiresAt.Sub(preview.CreatedAt) != previewTTL {
		t.Fatalf("TTL = %v，期望 %v", preview.ExpiresAt.Sub(preview.CreatedAt), previewTTL)
	}
	if !previewIDPattern.MatchString(preview.ID) {
		t.Fatalf("preview ID 格式不符: %q", preview.ID)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("dspack 预览应包含依赖恢复警告")
	}
	record := lookupPreview(t, service, preview.ID)
	if record.archiveSHA256 == "" {
		t.Fatal("应记录归档 SHA-256")
	}
	data, err := os.ReadFile(record.archivePath)
	if err != nil {
		t.Fatalf("读取上传归档失败: %v", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != record.archiveSHA256 {
		t.Fatalf("SHA-256 不符: %s vs %s", hex.EncodeToString(sum[:]), record.archiveSHA256)
	}
	if !strings.HasPrefix(record.archivePath, filepath.Join(dirs.CacheDir, "uploads")) {
		t.Fatalf("上传归档应位于 cache/uploads: %s", record.archivePath)
	}
}

func TestPreviewDSPackHome(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	preview, err := previewArchive(t, service, "home.dspack.zip", validDSPackHome())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if preview.Format != FormatDSPack || preview.Name != "home-pack" || preview.Version != "2.0.0" {
		t.Fatalf("Preview 字段不符: %+v", preview)
	}
	if len(preview.Profiles) != 2 || preview.Profiles[0] != "play" || preview.Profiles[1] != "work" {
		t.Fatalf("Profiles 不符（dshhome 按名称排序）: %v", preview.Profiles)
	}
	if !containsString(preview.Profiles, "work") {
		t.Fatalf("Profiles 缺少 defaultProfile work: %v", preview.Profiles)
	}
}

func TestPreviewOvertureSnapshot(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	preview, err := previewArchive(t, service, "my-snapshot.zip", validOvertureSnapshot())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if preview.Format != FormatOverture || preview.Name != "my-snapshot" || preview.Version != "snapshot-1" {
		t.Fatalf("Preview 字段不符: %+v", preview)
	}
	if preview.DSHVersion != "0.2.1-rc.3" || len(preview.Profiles) != 1 || preview.Profiles[0] != "work" {
		t.Fatalf("快照字段不符: %+v", preview)
	}
}

func TestPreviewRejectsInvalidPackages(t *testing.T) {
	valid := validDSPackProfile()
	cases := []struct {
		name      string
		entries   map[string][]byte
		substring string
	}{
		{"无格式标记", map[string][]byte{"readme.txt": []byte("x")}, "无法识别"},
		{"Overture YAML 清单", map[string][]byte{"dsh-pack.yaml": []byte("name: x")}, "尚未支持"},
		{
			"多个冲突标记",
			map[string][]byte{
				"dspack.json":       []byte("{\"format\":\"dspack\",\"version\":2}"),
				"dsh-snapshot.json": []byte("{\"format\":1}"),
				"manifest.json":     []byte("{}"),
			},
			"多个冲突",
		},
		{"容器版本不支持", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":1}"), "manifest.json": []byte("{}")}, "容器版本"},
		{"manifest 版本不支持", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":3}"), "manifest.json": []byte("{\"manifestVersion\":3}")}, "manifestVersion"},
		{"缺少 name", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{}}")}, "name"},
		{"dshVersion 非精确", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"dshVersion\":\"1.2\",\"bundles\":[],\"dependencies\":{}}")}, "dshVersion"},
		{"dshVersion 不在枚举", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"dshVersion\":\"1.0.0\",\"dshVersions\":[\"2.0.0\"],\"bundles\":[],\"dependencies\":{}}")}, "dshVersions"},
		{"type 不支持", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"weird\",\"name\":\"x\",\"version\":\"1.0.0\"}")}, "type="},
		{"v2 含 home", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{}}"), "home/x.txt": []byte("x")}, "home/"},
		{"dshhome 用 v2", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":5,\"type\":\"dshhome\",\"name\":\"x\",\"version\":\"1.0.0\",\"profiles\":{\"work\":{\"bundles\":[],\"dependencies\":{}}},\"defaultProfile\":\"work\"}")}, "dshhome"},
		{"dshhome defaultProfile 缺失", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":3}"), "manifest.json": []byte("{\"manifestVersion\":5,\"type\":\"dshhome\",\"name\":\"x\",\"version\":\"1.0.0\",\"profiles\":{\"work\":{\"bundles\":[],\"dependencies\":{}}},\"defaultProfile\":\"play\"}")}, "defaultProfile"},
		{"dshhome 含基线 profile", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":3}"), "manifest.json": []byte("{\"manifestVersion\":5,\"type\":\"dshhome\",\"name\":\"x\",\"version\":\"1.0.0\",\"profiles\":{\"web\":{\"bundles\":[],\"dependencies\":{}}},\"defaultProfile\":\"web\"}")}, "基线模板"},
		{"bundles 类型错误", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"bundles\":\"nope\",\"dependencies\":{}}")}, "bundles"},
		{"dependencies 类型错误", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":[]}")}, "dependencies"},
		{"不支持的依赖方言", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"foo\":\"vendor:foo.tgz\"}}")}, "方言"},
		{"依赖引用源路径", map[string][]byte{"dspack.json": []byte("{\"format\":\"dspack\",\"version\":2}"), "manifest.json": []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"x\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"foo\":\"/tmp/foo\"}}")}, "源机器"},
		{"快照平台不符", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"plan9\"}"), "profiles/work/profile.yaml": []byte("dshVersion: 0.2.1-rc.3"), "profiles/work/package.json": []byte("{}")}, "平台"},
		{"快照缺 profile", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"), "readme.txt": []byte("x")}, "profile.yaml"},
		{"快照 profile 缺 package.json", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"), "profiles/work/profile.yaml": []byte("dshVersion: 0.2.1-rc.3")}, "package.json"},
		{"快照 YAML 非法", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"), "profiles/work/profile.yaml": []byte("["), "profiles/work/package.json": []byte("{}")}, "profile.yaml"},
		{"快照版本冲突", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"), "profiles/a/profile.yaml": []byte("dshVersion: 0.2.1-rc.3"), "profiles/a/package.json": []byte("{}"), "profiles/b/profile.yaml": []byte("dshVersion: 0.2.0"), "profiles/b/package.json": []byte("{}")}, "不同 Harness 版本"},
		{"快照依赖源路径", map[string][]byte{"dsh-snapshot.json": []byte("{\"format\":1,\"platform\":\"" + currentPlatform() + "\"}"), "profiles/work/profile.yaml": []byte("dshVersion: 0.2.1-rc.3"), "profiles/work/package.json": []byte("{\"dependencies\":{\"foo\":\"/tmp/foo\"}}")}, "源机器"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, _, _, _, _ := newPackTestService(t)
			_, err := previewArchive(t, service, "bad.zip", tc.entries)
			requirePackAPIError(t, err, 400, tc.substring)
		})
	}
	_ = valid
}

func TestPreviewRejectsUnsafeArchive(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	entries := validDSPackProfile()
	entries["../evil.txt"] = []byte("evil")
	_, err := previewArchive(t, service, "evil.zip", entries)
	requirePackAPIError(t, err, 400, "")
}

func TestPreviewRejectsOversizeAndEmpty(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	service.limits = effectiveLimits(safe.Limits{MaxArchiveBytes: 32})
	big := validDSPackProfile()
	_, err := previewArchive(t, service, "big.zip", big)
	requirePackAPIError(t, err, 413, "")

	emptyService, _, _, _, _ := newPackTestService(t)
	_, err = emptyService.Preview(context.Background(), bytes.NewReader(nil), "empty.zip")
	requirePackAPIError(t, err, 400, "空")
}

func TestPreviewCanceledAndNilReader(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Preview(ctx, bytes.NewReader(zipArchive(t, validDSPackProfile())), "x.zip")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应返回 context.Canceled，实际 %v", err)
	}
	_, err = service.Preview(context.Background(), nil, "x.zip")
	requirePackAPIError(t, err, 400, "")
}

func TestPreviewCleanupExpired(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	base := time.Unix(1750000000, 0)
	service.now = func() time.Time { return base }
	preview, err := previewArchive(t, service, "demo.zip", validDSPackProfile())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	record := lookupPreview(t, service, preview.ID)
	if _, err := os.Stat(record.archivePath); err != nil {
		t.Fatalf("上传归档不存在: %v", err)
	}

	service.now = func() time.Time { return base.Add(previewTTL + time.Minute) }
	if _, err := service.previewByID(context.Background(), preview.ID); err == nil {
		t.Fatal("过期预览应返回 404")
	} else {
		requirePackAPIError(t, err, 404, "")
	}
	if _, err := os.Stat(record.archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("过期后归档文件应被删除: %v", err)
	}

	service.now = func() time.Time { return base }
	another, err := previewArchive(t, service, "demo2.zip", validDSPackProfile())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	anotherRecord := lookupPreview(t, service, another.ID)
	service.CleanupExpired(base.Add(previewTTL + time.Minute))
	if _, err := os.Stat(anotherRecord.archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CleanupExpired 应删除过期归档: %v", err)
	}
	if _, err := service.previewByID(context.Background(), another.ID); err == nil {
		t.Fatal("CleanupExpired 后预览应不存在")
	}
}

func TestPreviewBadIDs(t *testing.T) {
	service, _, _, _, _ := newPackTestService(t)
	for _, id := range []string{"", "xyz", "0123456789abcdef"} {
		if _, err := service.previewByID(context.Background(), id); err == nil {
			t.Fatalf("非法 ID %q 应返回 404", id)
		} else {
			requirePackAPIError(t, err, 404, "")
		}
	}
	preview, err := previewArchive(t, service, "demo.zip", validDSPackProfile())
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	record, err := service.previewByID(context.Background(), preview.ID)
	if err != nil || record.preview.ID != preview.ID {
		t.Fatalf("previewByID = %v, %v", record, err)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	root := t.TempDir()
	instanceStore, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer instanceStore.Close()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if _, err := New(Options{}); err == nil {
		t.Fatal("空 Options 应返回错误")
	}
	base := Options{DataRoot: root, Store: instanceStore, Runtime: &fakePackRuntime{}, App: &fakePackApp{}, Artifacts: &fakePackArtifacts{}, Logger: logger}
	if _, err := New(base); err != nil {
		t.Fatalf("合法 Options 被拒绝: %v", err)
	}
}

func lookupPreview(t *testing.T, service *Service, id string) *previewRecord {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.previews[id]
	if !ok {
		t.Fatalf("预览 %q 不存在", id)
	}
	return record
}
