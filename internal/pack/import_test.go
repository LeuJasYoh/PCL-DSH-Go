// SPDX-License-Identifier: MIT

package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	coreruntime "github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// fakeImportRuntime records runtime resolution/installation for import tests.
type fakeImportRuntime struct {
	installed  map[string]bool
	versions   []string
	installErr error
	installs   []string
}

func (f *fakeImportRuntime) Installed(version string) bool { return f.installed[version] }

func (f *fakeImportRuntime) Install(_ context.Context, version string, logf func(string)) (coreruntime.InstallResult, error) {
	f.installs = append(f.installs, version)
	if logf != nil {
		logf("fake pnpm install " + version)
	}
	if f.installErr != nil {
		return coreruntime.InstallResult{}, f.installErr
	}
	f.installed[version] = true
	f.versions = append(f.versions, version)
	return coreruntime.InstallResult{Version: version, EntryPath: "/fake/entry.js"}, nil
}

// InstalledVersions implements the optional runtimeLister capability.
func (f *fakeImportRuntime) InstalledVersions() []string {
	return append([]string(nil), f.versions...)
}

// fakeImportApp is a stateful AppAccess backed by a real store and data root.
type fakeImportApp struct {
	t            *testing.T
	store        *store.Store
	dirs         config.Dirs
	created      []store.CreateInstanceInput
	deleted      []string
	restore      []string
	restoreErr   error
	entryPathErr error
	createdDirs  bool
	// portCursor is the next candidate instance port (>= 3080).
	portCursor int
}

func (f *fakeImportApp) CreateInstance(_ context.Context, in store.CreateInstanceInput) (store.Instance, error) {
	if in.Port == 0 {
		in.Port = f.nextFreePort()
	}
	instance, err := f.store.CreateInstance(in)
	if err != nil {
		return store.Instance{}, err
	}
	for _, sub := range []string{"dsh-home", "workspace", "agents-home"} {
		if mkdirErr := os.MkdirAll(filepath.Join(f.dirs.InstancesDir, instance.ID, sub), 0o700); mkdirErr != nil {
			return store.Instance{}, mkdirErr
		}
	}
	f.created = append(f.created, in)
	return instance, nil
}

func (f *fakeImportApp) DeleteInstance(_ context.Context, instanceID string) (string, error) {
	f.deleted = append(f.deleted, instanceID)
	instanceDir := filepath.Join(f.dirs.InstancesDir, instanceID)
	trashed := ""
	if _, err := os.Lstat(instanceDir); err == nil {
		if mkdirErr := os.MkdirAll(f.dirs.TrashDir, 0o700); mkdirErr == nil {
			target := filepath.Join(f.dirs.TrashDir, "instance-"+instanceID)
			if renameErr := os.Rename(instanceDir, target); renameErr == nil {
				trashed = target
			}
		}
	}
	if err := f.store.DeleteInstance(instanceID); err != nil {
		return trashed, err
	}
	return trashed, nil
}

func (f *fakeImportApp) RestoreProfileDependencies(_ context.Context, instanceID, profile string, logf func(string)) error {
	f.restore = append(f.restore, instanceID+"/"+profile)
	if f.restoreErr != nil {
		return f.restoreErr
	}
	if logf != nil {
		logf("fake pnpm restored " + profile)
	}
	return nil
}

func (f *fakeImportApp) EntryPath(string) (string, error) {
	if f.entryPathErr != nil {
		return "", f.entryPathErr
	}
	return "/fake/entry.js", nil
}

// fakeImportArtifacts records artifact registration; addErr simulates failure.
type fakeImportArtifacts struct {
	addErr error
	files  []string
	paths  []string
	sizes  []int64
}

func (f *fakeImportArtifacts) Add(filename, path, contentType string, size int64) (artifact.Artifact, error) {
	if f.addErr != nil {
		return artifact.Artifact{}, f.addErr
	}
	f.files = append(f.files, filename)
	f.paths = append(f.paths, path)
	f.sizes = append(f.sizes, size)
	return artifact.Artifact{ID: "0123456789ab", Filename: filename, Path: path, ContentType: contentType, Size: size}, nil
}

func newImportHarness(t *testing.T) (*Service, config.Dirs, *fakeImportRuntime, *fakeImportApp, *fakeImportArtifacts) {
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
	fakeRuntime := &fakeImportRuntime{installed: map[string]bool{"0.2.1-rc.3": true}, versions: []string{"0.2.1-rc.3"}}
	fakeApp := &fakeImportApp{t: t, store: instanceStore, dirs: dirs}
	fakeArtifacts := &fakeImportArtifacts{}
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

// nextFreePort returns a free loopback instance port >= 3080.
//
// Tests must not derive instance ports from "127.0.0.1:0": Windows' dynamic
// port range is 1024-15000, so the OS may hand out a port below the manager's
// automatic-allocation floor (3080) and the import test would fail on Windows
// only. Explicitly probing from 3080 upwards keeps Windows and Linux stable;
// the cursor avoids reusing a port already assigned to an instance in the same
// test store (the store enforces unique instance ports).
func (f *fakeImportApp) nextFreePort() int {
	f.t.Helper()
	start := f.portCursor
	if start < 3080 {
		start = 3080
	}
	for port := start; port <= 65535; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = listener.Close()
		f.portCursor = port + 1
		return port
	}
	f.t.Fatalf("无法分配 >=3080 的空闲回环端口")
	return 0
}

func importProfileFixture() map[string][]byte {
	return map[string][]byte{
		"dspack.json":               []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json":             []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"demo-pack\",\"version\":\"1.0.0\",\"dshVersion\":\"0.2.1-rc.3\",\"profileName\":\"demo\",\"bundles\":[\"@deepseek-ai/base\"],\"dependencies\":{}}"),
		"package.json":              []byte("{\"name\":\"existing-profile\",\"dependencies\":{}}"),
		"pnpm-lock.yaml":            []byte("lockfileVersion: '9.0'"),
		"overrides/skills/hello.md": []byte("hello skill"),
	}
}

func importSnapshotFixture() map[string][]byte {
	return map[string][]byte{
		"dsh-snapshot.json":                       []byte(fmt.Sprintf("{\"format\":1,\"platform\":\"%s\"}", currentPlatform())),
		"profiles/work/profile.yaml":              []byte("dshVersion: 0.2.1-rc.3"),
		"profiles/work/package.json":              []byte("{\"name\":\"work\",\"dependencies\":{}}"),
		"profiles/work/node_modules/mod/index.js": []byte("module.exports = 1"),
	}
}

func previewFixture(t *testing.T, service *Service, entries map[string][]byte) string {
	t.Helper()
	preview, err := service.Preview(context.Background(), bytes.NewReader(zipArchive(t, entries)), "fixture.zip")
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	return preview.ID
}

func TestImportProfilePackage(t *testing.T) {
	service, dirs, fakeRuntime, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, importProfileFixture())

	var lines []string
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID, Name: "导入实例"}, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	if result.InstanceID == "" || result.Name != "导入实例" {
		t.Fatalf("ImportResult 不符: %+v", result)
	}
	instance, ok := fakeApp.store.GetInstance(result.InstanceID)
	if !ok {
		t.Fatal("实例未写入 store")
	}
	// The fake app above must assign an explicit valid instance port (>= 3080)
	// instead of relying on the OS ephemeral range (1024-15000 on Windows).
	if instance.Version != "0.2.1-rc.3" || instance.Profile != "demo" ||
		instance.Port < 3080 || instance.Port > 65535 {
		t.Fatalf("实例字段不符: %+v", instance)
	}
	if len(fakeRuntime.installs) != 0 {
		t.Fatalf("已安装运行时不应重复安装: %v", fakeRuntime.installs)
	}

	home := filepath.Join(dirs.InstancesDir, instance.ID, "dsh-home")
	packageJSONPath := filepath.Join(home, "profiles", "demo", "package.json")
	raw, readErr := os.ReadFile(packageJSONPath)
	if readErr != nil {
		t.Fatalf("profile package.json 未生成: %v", readErr)
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("package.json 非法: %v", err)
	}
	if pkg["private"] != true {
		t.Fatalf("private 应为 true: %v", pkg["private"])
	}
	dsh, _ := pkg["dsh"].(map[string]any)
	profileSection, _ := dsh["profile"].(map[string]any)
	bundles, _ := profileSection["bundles"].([]any)
	if len(bundles) != 1 || bundles[0] != "@deepseek-ai/base" {
		t.Fatalf("bundles 不符: %v", bundles)
	}
	if _, statErr := os.Stat(filepath.Join(home, "profiles", "demo", "skills", "hello.md")); statErr != nil {
		t.Fatalf("overrides 文件未映射: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "profiles", "demo", "pnpm-lock.yaml")); statErr != nil {
		t.Fatalf("pnpm-lock.yaml 未映射: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, originFileName)); statErr != nil {
		t.Fatalf("来源清单未写入: %v", statErr)
	}
	if len(fakeApp.restore) != 1 || fakeApp.restore[0] != instance.ID+"/demo" {
		t.Fatalf("RestoreProfileDependencies 调用不符: %v", fakeApp.restore)
	}
	stagingEntries, readDirErr := os.ReadDir(filepath.Join(dirs.CacheDir, "pack-staging"))
	if readDirErr != nil {
		t.Fatalf("读取 staging 目录失败: %v", readDirErr)
	}
	if len(stagingEntries) != 0 {
		t.Fatalf("staging 目录未清理: %v", stagingEntries)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "整合包导入完成") {
		t.Fatalf("导入日志不完整: %q", joined)
	}
}

func TestImportSnapshotPackage(t *testing.T) {
	service, dirs, _, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, importSnapshotFixture())
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	instance, _ := fakeApp.store.GetInstance(result.InstanceID)
	if instance.Profile != "work" || instance.Version != "0.2.1-rc.3" {
		t.Fatalf("快照实例字段不符: %+v", instance)
	}
	home := filepath.Join(dirs.InstancesDir, instance.ID, "dsh-home")
	if _, statErr := os.Stat(filepath.Join(home, "profiles", "work", "node_modules", "mod", "index.js")); statErr != nil {
		t.Fatalf("快照 node_modules 未恢复: %v", statErr)
	}
	if len(fakeApp.restore) != 1 || fakeApp.restore[0] != instance.ID+"/work" {
		t.Fatalf("快照 profile 依赖恢复调用不符: %v", fakeApp.restore)
	}
}

func TestImportPreviewValidation(t *testing.T) {
	service, _, _, _, _ := newImportHarness(t)
	_, err := service.Import(context.Background(), ImportInput{PreviewID: "unknown"}, nil)
	requirePackAPIError(t, err, 404, "")

	previewID := previewFixture(t, service, importProfileFixture())
	record := lookupPreview(t, service, previewID)
	if err := os.WriteFile(record.archivePath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("篡改归档失败: %v", err)
	}
	_, err = service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	requirePackAPIError(t, err, 400, "发生变化")

	previewID2 := previewFixture(t, service, importProfileFixture())
	service.now = func() time.Time { return time.Now().Add(2 * previewTTL) }
	_, err = service.Import(context.Background(), ImportInput{PreviewID: previewID2}, nil)
	requirePackAPIError(t, err, 404, "")
}

func TestImportRuntimeResolution(t *testing.T) {
	// Requested version wins and is installed when missing.
	service, _, fakeRuntime, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, importProfileFixture())
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID, Version: "0.2.0"}, nil)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	instance, _ := fakeApp.store.GetInstance(result.InstanceID)
	if instance.Version != "0.2.0" {
		t.Fatalf("请求版本未生效: %+v", instance)
	}
	if len(fakeRuntime.installs) != 1 || fakeRuntime.installs[0] != "0.2.0" {
		t.Fatalf("安装调用不符: %v", fakeRuntime.installs)
	}

	// Installed compatible version from manifest dshVersions.
	service2, _, fakeRuntime2, fakeApp2, _ := newImportHarness(t)
	fakeRuntime2.installed = map[string]bool{"0.2.0": true}
	fakeRuntime2.versions = []string{"0.2.0"}
	entries := importProfileFixture()
	entries["manifest.json"] = []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"compat-pack\",\"version\":\"1.0.0\",\"dshVersions\":[\"0.2.1-rc.3\",\"0.2.0\"],\"profileName\":\"demo\",\"bundles\":[],\"dependencies\":{}}")
	previewID2 := previewFixture(t, service2, entries)
	if _, err := service2.Import(context.Background(), ImportInput{PreviewID: previewID2}, nil); err != nil {
		t.Fatalf("兼容版本导入失败: %v", err)
	}
	if len(fakeRuntime2.installs) != 0 || len(fakeApp2.created) != 1 {
		t.Fatalf("应使用已安装兼容版本，实际 installs=%v", fakeRuntime2.installs)
	}

	// Installed latest fallback (manifest without dshVersion).
	service3, _, fakeRuntime3, fakeApp3, _ := newImportHarness(t)
	fakeRuntime3.installed = map[string]bool{"0.2.1-rc.3": true}
	fakeRuntime3.versions = []string{"0.2.1-rc.0", "0.2.1-rc.3"}
	entries3 := importProfileFixture()
	entries3["manifest.json"] = []byte("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"latest-pack\",\"version\":\"1.0.0\",\"profileName\":\"demo\",\"bundles\":[],\"dependencies\":{}}")
	previewID3 := previewFixture(t, service3, entries3)
	if _, err := service3.Import(context.Background(), ImportInput{PreviewID: previewID3}, nil); err != nil {
		t.Fatalf("已安装最新版本导入失败: %v", err)
	}
	if len(fakeApp3.created) != 1 {
		t.Fatalf("应创建一个实例: %v", fakeApp3.created)
	}
	if fakeApp3.created[0].Version != "0.2.1-rc.3" {
		t.Fatalf("已安装最新版本应被选用: %v", fakeApp3.created[0])
	}

	// No runtime determinable → 422.
	service4, _, fakeRuntime4, _, _ := newImportHarness(t)
	fakeRuntime4.installed = map[string]bool{}
	fakeRuntime4.versions = nil
	previewID4 := previewFixture(t, service4, entries3)
	_, err = service4.Import(context.Background(), ImportInput{PreviewID: previewID4}, nil)
	requirePackAPIError(t, err, 422, "")

	// Install failure → 500.
	service5, _, fakeRuntime5, _, _ := newImportHarness(t)
	fakeRuntime5.installErr = errors.New("pnpm failed")
	previewID5 := previewFixture(t, service5, importProfileFixture())
	_, err = service5.Import(context.Background(), ImportInput{PreviewID: previewID5, Version: "0.3.0"}, nil)
	requirePackAPIError(t, err, 500, "")
}

func TestImportRollback(t *testing.T) {
	service, dirs, _, fakeApp, _ := newImportHarness(t)
	existing := mustImportInstance(t, fakeApp, "已有实例")
	existingHome := filepath.Join(dirs.InstancesDir, existing.ID, "dsh-home")
	if err := os.WriteFile(filepath.Join(existingHome, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("准备既有实例失败: %v", err)
	}
	fakeApp.restoreErr = errors.New("restore boom")
	previewID := previewFixture(t, service, importProfileFixture())
	_, err := service.Import(context.Background(), ImportInput{PreviewID: previewID, Name: "回滚实例"}, nil)
	requirePackAPIError(t, err, 500, "")
	if len(fakeApp.deleted) != 1 {
		t.Fatalf("回滚应删除新实例: %v", fakeApp.deleted)
	}
	if _, ok := fakeApp.store.GetInstance(fakeApp.deleted[0]); ok {
		t.Fatal("回滚后 store 不应保留新实例")
	}
	trashEntries, readErr := os.ReadDir(dirs.TrashDir)
	if readErr != nil {
		t.Fatalf("读取 trash 失败: %v", readErr)
	}
	if len(trashEntries) == 0 {
		t.Fatal("回滚应将实例目录移入 trash")
	}
	if data, readErr := os.ReadFile(filepath.Join(existingHome, "keep.txt")); readErr != nil || string(data) != "keep" {
		t.Fatalf("既有实例不应被污染: %v", readErr)
	}
	stagingEntries, _ := os.ReadDir(filepath.Join(dirs.CacheDir, "pack-staging"))
	if len(stagingEntries) != 0 {
		t.Fatalf("回滚后 staging 未清理: %v", stagingEntries)
	}
}

func mustImportInstance(t *testing.T, fakeApp *fakeImportApp, name string) store.Instance {
	t.Helper()
	instance, err := fakeApp.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: name, Version: "0.2.1-rc.3", Profile: "work", Port: 0,
	})
	if err != nil {
		t.Fatalf("创建实例失败: %v", err)
	}
	return instance
}

func TestImportCanceled(t *testing.T) {
	service, _, _, _, _ := newImportHarness(t)
	previewID := previewFixture(t, service, importProfileFixture())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Import(ctx, ImportInput{PreviewID: previewID}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
}
