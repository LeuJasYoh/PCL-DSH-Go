// SPDX-License-Identifier: MIT

package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// prepareExportInstance creates one instance and a realistic dsh-home tree.
func prepareExportInstance(t *testing.T, fakeApp *fakeImportApp, dirs config.Dirs, name, profile string) store.Instance {
	t.Helper()
	instance := mustImportInstanceWithProfile(t, fakeApp, name, profile)
	home := filepath.Join(dirs.InstancesDir, instance.ID, "dsh-home")
	writeTestFile(t, filepath.Join(home, "profiles", profile, "package.json"), `{
  "name": "dsh-profile-work",
  "private": true,
  "dependencies": {"left-pad": "1.3.0"},
  "dsh": {"profile": {"bundles": ["@deepseek-ai/base"]}}
}`)
	writeTestFile(t, filepath.Join(home, "profiles", profile, "skills", "demo.md"), "demo skill")
	writeTestFile(t, filepath.Join(home, "skills", "root-skill.md"), "root skill")
	writeTestFile(t, filepath.Join(home, "profiles", profile, "node_modules", "left-pad", "index.js"), "module")
	writeTestFile(t, filepath.Join(home, "profiles", profile, ".ssh", "id_rsa"), "PRIVATE KEY")
	writeTestFile(t, filepath.Join(home, "profiles", profile, "logs", "run.log"), "log line")
	writeTestFile(t, filepath.Join(home, ".hdsl-pack-origin.json"), "{}")
	workspace := filepath.Join(dirs.InstancesDir, instance.ID, "workspace")
	writeTestFile(t, filepath.Join(workspace, "notes.txt"), "workspace content")
	return instance
}

func mustImportInstanceWithProfile(t *testing.T, fakeApp *fakeImportApp, name, profile string) store.Instance {
	t.Helper()
	instance, err := fakeApp.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: name, Version: "0.2.1-rc.3", Profile: profile, Port: 0,
	})
	if err != nil {
		t.Fatalf("创建实例失败: %v", err)
	}
	return instance
}

func writeTestFile(t *testing.T, filePath, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}

func readZipEntries(t *testing.T, archivePath string) (map[string][]byte, *zip.Reader) {
	t.Helper()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("打开导出 ZIP 失败: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatalf("打开 ZIP 条目 %s 失败: %v", file.Name, err)
		}
		data, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatalf("读取 ZIP 条目 %s 失败: %v", file.Name, err)
		}
		entries[file.Name] = data
	}
	return entries, &reader.Reader
}

func TestExportProfileAndRoundtrip(t *testing.T) {
	service, dirs, fakeRuntime, fakeApp, fakeArtifacts := newImportHarness(t)
	instance := prepareExportInstance(t, fakeApp, dirs, "Demo 实例", "work")

	result, err := service.Export(context.Background(), ExportInput{InstanceID: instance.ID, Mode: "profile"}, nil)
	if err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	if result.ArtifactID == "" || result.Filename == "" || result.Size <= 0 {
		t.Fatalf("ExportResult 不符: %+v", result)
	}
	if len(fakeArtifacts.files) != 1 || fakeArtifacts.files[0] != result.Filename {
		t.Fatalf("Artifact.Add 调用不符: %v", fakeArtifacts.files)
	}
	if fakeArtifacts.sizes[0] != result.Size {
		t.Fatalf("Artifact size = %d，期望 %d", fakeArtifacts.sizes[0], result.Size)
	}
	if !strings.HasPrefix(fakeArtifacts.paths[0], filepath.Join(dirs.CacheDir, "exports")) {
		t.Fatalf("artifact 文件应位于 cache/exports: %s", fakeArtifacts.paths[0])
	}

	entries, _ := readZipEntries(t, fakeArtifacts.paths[0])
	if _, ok := entries["dspack.json"]; !ok {
		t.Fatal("导出缺少 dspack.json")
	}
	var manifest map[string]any
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("导出 manifest 非法: %v", err)
	}
	if manifest["manifestVersion"] != float64(5) || manifest["type"] != "profile" || manifest["name"] != "demo" {
		t.Fatalf("manifest 字段不符: %v", manifest)
	}
	if manifest["version"] != "0.2.1-rc.3" || manifest["dshVersion"] != "0.2.1-rc.3" || manifest["profileName"] != "work" {
		t.Fatalf("manifest 版本字段不符: %v", manifest)
	}
	bundles, _ := manifest["bundles"].([]any)
	if len(bundles) != 1 || bundles[0] != "@deepseek-ai/base" {
		t.Fatalf("manifest bundles 不符: %v", manifest["bundles"])
	}
	dependencies, _ := manifest["dependencies"].(map[string]any)
	if dependencies["left-pad"] != "1.3.0" {
		t.Fatalf("manifest dependencies 不符: %v", dependencies)
	}
	// Machine file at archive root, other profile files under overrides/, root
	// files under home/.
	if _, ok := entries["package.json"]; !ok {
		t.Fatalf("导出缺少 machine package.json: %v", keysOf(entries))
	}
	if string(entries["overrides/skills/demo.md"]) != "demo skill" {
		t.Fatalf("overrides 映射不符: %v", keysOf(entries))
	}
	if string(entries["home/skills/root-skill.md"]) != "root skill" {
		t.Fatalf("home 映射不符: %v", keysOf(entries))
	}
	for name := range entries {
		lowered := strings.ToLower(name)
		if strings.Contains(lowered, "node_modules") || strings.Contains(lowered, ".ssh") ||
			strings.Contains(lowered, "id_rsa") || strings.Contains(lowered, "logs/") ||
			strings.Contains(lowered, "origin") || strings.Contains(lowered, "workspace") {
			t.Fatalf("导出不应包含敏感/未选中文件: %s", name)
		}
	}
	if strings.Contains(result.Filename, " ") {
		t.Fatalf("导出文件名不应包含空格: %q", result.Filename)
	}

	// Roundtrip: the exported archive must preview as a valid dspack.
	archiveData, err := os.ReadFile(fakeArtifacts.paths[0])
	if err != nil {
		t.Fatalf("读取导出文件失败: %v", err)
	}
	preview, err := service.Preview(context.Background(), bytes.NewReader(archiveData), result.Filename)
	if err != nil {
		t.Fatalf("导出产物预览失败（往返失败）: %v", err)
	}
	if preview.Format != FormatDSPack || preview.Name != "demo" || preview.Version != "0.2.1-rc.3" || preview.DSHVersion != "0.2.1-rc.3" {
		t.Fatalf("往返预览字段不符: %+v", preview)
	}
	if len(preview.Profiles) != 1 || preview.Profiles[0] != "work" {
		t.Fatalf("往返 preview profiles 不符: %v", preview.Profiles)
	}

	// Full roundtrip: import the exported archive into a second instance.
	fakeRuntime.installed["0.2.1-rc.3"] = true
	importResult, err := service.Import(context.Background(), ImportInput{PreviewID: preview.ID, Name: "往返实例"}, nil)
	if err != nil {
		t.Fatalf("导出产物导入失败（往返失败）: %v", err)
	}
	imported, ok := fakeApp.store.GetInstance(importResult.InstanceID)
	if !ok || imported.Name != "往返实例" {
		t.Fatalf("往返导入实例不符: %+v", imported)
	}
	importedHome := filepath.Join(dirs.InstancesDir, imported.ID, "dsh-home")
	if _, statErr := os.Stat(filepath.Join(importedHome, "profiles", "work", "package.json")); statErr != nil {
		t.Fatalf("往返导入缺少 profile package.json: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(importedHome, "profiles", "work", "skills", "demo.md")); statErr != nil {
		t.Fatalf("往返导入缺少 overrides 文件: %v", statErr)
	}
}

func TestExportHomeMode(t *testing.T) {
	service, dirs, _, fakeApp, fakeArtifacts := newImportHarness(t)
	instance := prepareExportInstance(t, fakeApp, dirs, "Home 实例", "work")
	// Second profile and a baseline profile that must never be exported.
	home := filepath.Join(dirs.InstancesDir, instance.ID, "dsh-home")
	writeTestFile(t, filepath.Join(home, "profiles", "play", "package.json"), `{"dependencies":{}}`)
	writeTestFile(t, filepath.Join(home, "profiles", "web", "package.json"), `{"dependencies":{}}`)

	result, err := service.Export(context.Background(), ExportInput{InstanceID: instance.ID, Mode: "home"}, nil)
	if err != nil {
		t.Fatalf("Export home 失败: %v", err)
	}
	entries, _ := readZipEntries(t, fakeArtifacts.paths[0])
	var manifest map[string]any
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest 非法: %v", err)
	}
	if manifest["type"] != "dshhome" || manifest["defaultProfile"] != "work" {
		t.Fatalf("dshhome manifest 不符: %v", manifest)
	}
	units, _ := manifest["profiles"].(map[string]any)
	if _, ok := units["play"]; !ok {
		t.Fatalf("dshhome 应包含 play profile: %v", units)
	}
	if _, ok := units["web"]; ok {
		t.Fatalf("dshhome 不应包含基线 web profile: %v", units)
	}
	if _, ok := entries["overrides/profiles/work/skills/demo.md"]; !ok {
		t.Fatalf("home 模式文件映射不符: %v", keysOf(entries))
	}
	if strings.Contains(string(entries["manifest.json"]), "node_modules") {
		t.Fatal("manifest 不应包含 node_modules 引用")
	}
	_ = result
}

func TestExportErrors(t *testing.T) {
	service, dirs, _, fakeApp, fakeArtifacts := newImportHarness(t)
	if _, err := service.Export(context.Background(), ExportInput{InstanceID: "i-000000000000", Mode: "profile"}, nil); err == nil {
		t.Fatal("不存在的实例应失败")
	} else {
		requirePackAPIError(t, err, 404, "")
	}
	instance := prepareExportInstance(t, fakeApp, dirs, "错误实例", "work")
	if _, err := service.Export(context.Background(), ExportInput{InstanceID: instance.ID, Mode: "weird"}, nil); err == nil {
		t.Fatal("非法模式应失败")
	} else {
		requirePackAPIError(t, err, 400, "")
	}

	// Artifact registration failure cleans up the generated file.
	fakeArtifacts.addErr = errors.New("artifact store down")
	if _, err := service.Export(context.Background(), ExportInput{InstanceID: instance.ID, Mode: "profile"}, nil); err == nil {
		t.Fatal("artifact 登记失败应返回错误")
	} else {
		requirePackAPIError(t, err, 500, "")
	}
	exportEntries, readErr := os.ReadDir(filepath.Join(dirs.CacheDir, "exports"))
	if readErr != nil {
		t.Fatalf("读取导出目录失败: %v", readErr)
	}
	for _, entry := range exportEntries {
		if strings.HasSuffix(entry.Name(), ".dspack.zip") {
			t.Fatalf("artifact 失败后导出文件未清理: %s", entry.Name())
		}
	}
}

func keysOf(entries map[string][]byte) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	return keys
}
