// SPDX-License-Identifier: GPL-3.0-or-later

package pack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// runtimeLister is an optional RuntimeAccess capability (*runtime.Service
// implements it) used to resolve the "installed latest" runtime version.
type runtimeLister interface {
	InstalledVersions() []string
}

// importRollbackTimeout bounds best-effort rollback after a failed import.
const importRollbackTimeout = 60 * time.Second

// Import restores a previewed integration package as a new stopped instance
// (docs/M3_INTERFACES.md §3, docs/DSH_INTEGRATION.md §7.4).
//
// Steps: verify preview + SHA-256, resolve/install the runtime, safely extract
// into <cache>/pack-staging, deep-validate the extracted package, create the
// instance, restore dsh-home content + profile dependencies. Any failure rolls
// back the instance (directory to trash + store record) and the staging dir.
func (s *Service) Import(ctx context.Context, in ImportInput, logf func(string)) (ImportResult, error) {
	in.PreviewID = strings.TrimSpace(in.PreviewID)
	if in.PreviewID == "" {
		return ImportResult{}, validationError("缺少 previewId，请先上传整合包预览。")
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	record, err := s.previewByID(ctx, in.PreviewID)
	if err != nil {
		return ImportResult{}, err
	}
	s.emitLogf(logf, "开始导入整合包（preview %s，格式 %s）…", in.PreviewID, record.preview.Format)

	// 1) Recompute the archive hash: the preview must still describe the bytes.
	actualSHA, err := sha256File(ctx, record.archivePath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ImportResult{}, ctxErr
		}
		s.logger.Error("导入时读取归档失败", "previewId", in.PreviewID, "error", err)
		return ImportResult{}, internalError("读取整合包失败，请重新上传预览。")
	}
	if actualSHA != record.archiveSHA256 {
		return ImportResult{}, validationError("整合包在预览后发生变化，请重新上传预览。")
	}

	// 2) Re-inspect the archive (cheap metadata validation; same limits).
	inspection, err := inspectArchive(ctx, record.archivePath, record.preview.Name, s.limits)
	if err != nil {
		return ImportResult{}, err
	}
	if inspection.format != record.preview.Format {
		return ImportResult{}, validationError("整合包格式在预览后发生变化，请重新上传预览。")
	}

	// 3) Resolve the runtime version (request > manifest dshVersion > installed
	// compatible > installed latest), installing it when needed.
	version, err := s.resolveRuntimeVersion(ctx, in.Version, inspection, logf)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err := s.app.EntryPath(version); err != nil {
		if apiErr, ok := apierr.As(err); ok {
			return ImportResult{}, apiErr
		}
		s.logger.Error("运行时入口校验失败", "version", version, "error", err)
		return ImportResult{}, apierr.New(422, apierr.CodeUnsupportedRuntime,
			"运行时 "+version+" 的入口校验失败，无法导入。")
	}
	s.emitLogf(logf, "使用 Harness 运行时 %s。", version)

	// 4) Safe extraction into a fresh staging directory.
	stagingRoot := s.stagingDir()
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		s.logger.Error("创建 pack staging 目录失败", "error", err)
		return ImportResult{}, internalError("创建解包目录失败，请检查数据目录权限。")
	}
	stagingDir, err := os.MkdirTemp(stagingRoot, "import-*")
	if err != nil {
		s.logger.Error("创建 staging 临时目录失败", "error", err)
		return ImportResult{}, internalError("创建解包目录失败，请检查数据目录权限。")
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	archive, err := safe.OpenZip(record.archivePath, s.limits)
	if err != nil {
		return ImportResult{}, mapSafeError(err)
	}
	extractErr := archive.Extract(ctx, stagingDir)
	_ = archive.Close()
	if extractErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ImportResult{}, ctxErr
		}
		return ImportResult{}, mapSafeError(extractErr)
	}
	s.emitLogf(logf, "整合包已安全解包到隔离目录。")

	// 5) Map + deep-validate the extracted tree (vendored tarballs, file:
	// dependencies) before creating the instance, so a failure leaves no record.
	var manifest jsonObject
	if inspection.format == FormatDSPack {
		manifest, err = parseJSONObject(inspection.manifestRaw, manifestFile)
		if err != nil {
			return ImportResult{}, err
		}
	}
	extractedFiles, err := walkExtractedFiles(stagingDir)
	if err != nil {
		s.logger.Error("遍历解包目录失败", "error", err)
		return ImportResult{}, internalError("读取解包目录失败，请稍后重试。")
	}
	mappings, excluded, err := s.importMappings(inspection, manifest, stagingDir, extractedFiles)
	if err != nil {
		return ImportResult{}, err
	}
	var resources []resourceSpec
	if inspection.format == FormatDSPack {
		resources, err = s.validateExtractedPackage(ctx, inspection, stagingDir, manifest, mappings)
		if err != nil {
			return ImportResult{}, err
		}
	}

	// 6) Create the instance record (version/profile/port auto).
	instanceName := importInstanceName(in.Name, inspection.name, record.preview.Name)
	instance, err := s.app.CreateInstance(ctx, store.CreateInstanceInput{
		Name:    instanceName,
		Version: version,
		Profile: inspection.defaultProfile,
		Port:    0,
	})
	if err != nil {
		if apiErr, ok := apierr.As(err); ok {
			return ImportResult{}, apiErr
		}
		s.logger.Error("导入时创建实例失败", "error", err)
		return ImportResult{}, internalError("创建实例失败，请稍后重试。")
	}
	rollback := func(cause error) (ImportResult, error) {
		s.logger.Error("整合包导入失败，开始回滚",
			"instanceId", instance.ID, "name", instance.Name, "error", cause)
		s.rollbackInstance(instance.ID)
		return ImportResult{}, cause
	}

	// 7) Restore dsh-home content (mapping, materialize, resources, origin).
	if err := s.restoreInstanceFiles(ctx, instance, inspection, manifest, resources, mappings, excluded, stagingDir, logf); err != nil {
		return rollback(err)
	}

	// 8) Restore profile dependencies with the bundled pnpm.
	for _, profile := range inspection.profiles {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return rollback(ctxErr)
		}
		s.emitLogf(logf, "正在恢复 profile %s 的依赖…", profile)
		if err := s.app.RestoreProfileDependencies(ctx, instance.ID, profile, s.forwardLogf(logf)); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return rollback(ctxErr)
			}
			if apiErr, ok := apierr.As(err); ok {
				return rollback(apiErr)
			}
			s.logger.Error("恢复 profile 依赖失败", "instanceId", instance.ID, "profile", profile, "error", err)
			return rollback(internalError("恢复 profile 依赖失败，请查看任务日志。"))
		}
	}

	_ = os.RemoveAll(stagingDir)
	s.emitLogf(logf, "整合包导入完成：实例 %s（%s，Harness %s）。", instance.ID, instance.Name, version)
	s.logger.Info("整合包导入完成",
		"instanceId", instance.ID, "name", instance.Name, "version", version,
		"profiles", inspection.profiles, "format", string(inspection.format))
	return ImportResult{InstanceID: instance.ID, Name: instance.Name}, nil
}

// rollbackInstance removes a partially imported instance: directory to trash +
// store record, with a direct fallback when the app layer is unavailable.
func (s *Service) rollbackInstance(instanceID string) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), importRollbackTimeout)
	defer cancel()
	if trashed, err := s.app.DeleteInstance(rollbackCtx, instanceID); err != nil {
		s.logger.Error("导入回滚删除实例失败，尝试直接清理", "instanceId", instanceID, "error", err)
		_ = s.store.DeleteInstance(instanceID)
		_ = os.RemoveAll(filepath.Join(s.dirs.InstancesDir, instanceID))
		return
	} else if trashed != "" {
		s.logger.Info("导入回滚：实例目录已移入 trash", "instanceId", instanceID, "trash", trashed)
	}
}

// --- runtime version resolution ------------------------------------------

// resolveRuntimeVersion chooses the Harness version for an import
// (docs/DSH_INTEGRATION.md §7.4 step 2) and installs it when missing:
// request > manifest dshVersion > installed compatible > installed latest; when
// nothing usable is installed at all, the preferred declared version is
// installed.
func (s *Service) resolveRuntimeVersion(ctx context.Context, requested string, inspection *inspection, logf func(string)) (string, error) {
	ensureInstalled := func(version string) (string, error) {
		if s.runtime.Installed(version) {
			return version, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		s.emitLogf(logf, "未安装 Harness %s，开始安装运行时…", version)
		if _, err := s.runtime.Install(ctx, version, s.forwardLogf(logf)); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			if apiErr, ok := apierr.As(err); ok {
				return "", apiErr
			}
			s.logger.Error("导入时安装运行时失败", "version", version, "error", err)
			return "", internalError("安装运行时 " + version + " 失败，请查看任务日志。")
		}
		if !s.runtime.Installed(version) {
			return "", internalError("运行时 " + version + " 安装后仍不可用，请查看任务日志。")
		}
		return version, nil
	}

	requested = strings.TrimSpace(requested)
	if requested != "" {
		if !runtime.IsVersion(requested) {
			return "", validationError("运行时版本必须是完整的 SemVer（例如 0.2.1-rc.3）。")
		}
		return ensureInstalled(requested)
	}
	if dshVersion := strings.TrimSpace(inspection.dshVersion); dshVersion != "" {
		return ensureInstalled(dshVersion)
	}
	declared := declaredDSHVersions(inspection)
	for _, version := range declared {
		if s.runtime.Installed(version) {
			return version, nil
		}
	}
	if lister, ok := s.runtime.(runtimeLister); ok {
		for _, version := range runtime.SortVersionsDescending(lister.InstalledVersions()) {
			if s.runtime.Installed(version) {
				return version, nil
			}
		}
	}
	if len(declared) > 0 {
		return ensureInstalled(declared[0])
	}
	return "", apierr.New(422, apierr.CodeUnsupportedRuntime,
		"整合包未声明 Harness 版本且未安装任何运行时，请先在请求中指定 version 或安装运行时。")
}

// declaredDSHVersions returns manifest.dshVersions sorted by descending
// SemVer precedence (empty for Overture snapshots).
func declaredDSHVersions(inspection *inspection) []string {
	if inspection.format != FormatDSPack || len(inspection.manifestRaw) == 0 {
		return nil
	}
	manifest, err := parseJSONObject(inspection.manifestRaw, manifestFile)
	if err != nil {
		return nil
	}
	raw, ok := manifest["dshVersions"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	versions := make([]string, 0, len(items))
	for _, item := range items {
		version, err := rawString(item, "dshVersions 项")
		if err == nil && runtime.IsVersion(version) {
			versions = append(versions, version)
		}
	}
	return runtime.SortVersionsDescending(versions)
}

// --- extracted-tree validation -------------------------------------------

// validateExtractedPackage performs the deep checks that require the extracted
// files: vendored tarball SHA-256/size/TAR safety and file: dependency presence.
// It returns the manifest object and the resource list for the import steps.
func (s *Service) validateExtractedPackage(ctx context.Context, inspection *inspection, stagingDir string, manifest jsonObject, mappings []importMapping) ([]resourceSpec, error) {
	if inspection.format != FormatDSPack {
		return nil, nil
	}
	typeValue, err := manifest.requiredString("type", manifestFile)
	if err != nil {
		return nil, err
	}
	units, err := manifestUnits(manifest, typeValue, inspection.profiles)
	if err != nil {
		return nil, err
	}
	if err := s.validateExtractedVendored(ctx, manifest, stagingDir); err != nil {
		return nil, err
	}
	targetSources := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		targetSources[strings.ToLower(mapping.target)] = mapping.source
	}
	for index, profile := range inspection.profiles {
		if err := validateExtractedFileDependencies(ctx, stagingDir, profile, units[index], targetSources, s.limits); err != nil {
			return nil, err
		}
	}
	resources, err := validateResourcesShape(manifest, s.limits)
	if err != nil {
		return nil, err
	}
	return resources, nil
}

// validateExtractedVendored verifies each vendored tarball in the staging tree:
// size, SHA-256 and TAR safety (contents are never executed).
func (s *Service) validateExtractedVendored(ctx context.Context, manifest jsonObject, stagingDir string) error {
	raw, ok := manifest["vendored"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	vendors, err := objectFromRaw(raw, "manifest.vendored")
	if err != nil {
		return err
	}
	for _, coordinate := range sortedKeys(vendors) {
		entry, err := objectFromRaw(vendors[coordinate], "vendored."+coordinate)
		if err != nil {
			return err
		}
		vendoredPath, err := entry.requiredString("path", "vendored."+coordinate)
		if err != nil {
			return err
		}
		if err := validateRelativePath(vendoredPath, s.limits); err != nil {
			return err
		}
		sha, err := entry.requiredString("sha256", "vendored."+coordinate)
		if err != nil {
			return err
		}
		size, ok, err := entry.integer("size", "vendored."+coordinate)
		if err != nil {
			return err
		}
		if !ok || size < 0 {
			return validationError("vendored 大小无效：" + coordinate)
		}
		filePath := filepath.Join(stagingDir, filepath.FromSlash(vendoredPath))
		info, statErr := os.Lstat(filePath)
		if statErr != nil || !info.Mode().IsRegular() {
			return validationError("vendored tarball 缺失或不是普通文件：" + vendoredPath)
		}
		if info.Size() != size {
			return validationError(fmt.Sprintf("vendored tarball 大小不符：%s（声明 %d，实际 %d）", vendoredPath, size, info.Size()))
		}
		actualSHA, hashErr := sha256File(ctx, filePath)
		if hashErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return internalError("读取 vendored tarball 失败，请稍后重试。")
		}
		if !strings.EqualFold(actualSHA, sha) {
			return validationError("vendored tarball SHA-256 校验失败：" + vendoredPath)
		}
		if err := validateNPMTarball(ctx, filePath, s.limits); err != nil {
			return err
		}
	}
	return nil
}

// validateNPMTarball validates a vendored npm tarball with internal/safe TAR
// rules (no extraction, no code execution) and bounds its expanded size.
func validateNPMTarball(ctx context.Context, tarballPath string, limits safe.Limits) error {
	archive, err := safe.OpenTarGz(tarballPath, limits)
	if err != nil {
		return mapSafeError(err)
	}
	defer func() { _ = archive.Close() }()
	var expanded int64
	for _, entry := range archive.Entries() {
		if entry.IsDir {
			continue
		}
		expanded += entry.UncompressedSize
		if expanded > limits.MaxUnpackedBytes {
			return validationError("内嵌 tarball 展开后超过解包总量上限。")
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return nil
}

// validateExtractedFileDependencies verifies file: dependencies of one profile
// unit exist in the extracted package (and validates embedded tarballs).
func validateExtractedFileDependencies(ctx context.Context, stagingDir, profile string, unit jsonObject, targetSources map[string]string, limits safe.Limits) error {
	depsRaw, ok := unit[dependenciesField]
	if !ok || strings.TrimSpace(string(depsRaw)) == "null" {
		return nil
	}
	var dependencies map[string]json.RawMessage
	if err := json.Unmarshal(depsRaw, &dependencies); err != nil {
		return validationError("dependencies 必须为对象。")
	}
	coordinates := make([]string, 0, len(dependencies))
	for coordinate := range dependencies {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)
	for _, coordinate := range coordinates {
		spec, err := rawString(dependencies[coordinate], "依赖版本")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(spec, "file:") {
			continue
		}
		local := spec[len("file:"):]
		if err := validateRelativePath(local, limits); err != nil {
			return err
		}
		target := strings.ToLower("profiles/" + profile + "/" + local)
		source, direct := targetSources[target]
		exists := direct
		if !exists {
			for targetKey := range targetSources {
				if strings.HasPrefix(targetKey, target+"/") {
					exists = true
					break
				}
			}
		}
		if !exists {
			return validationError("file: 依赖未包含在包内：" + coordinate + "（" + profile + "）")
		}
		if strings.HasSuffix(local, ".tgz") {
			if !direct {
				return validationError("file: 依赖必须为包内 .tgz 文件：" + coordinate + "（" + profile + "）")
			}
			tarballPath := filepath.Join(stagingDir, filepath.FromSlash(source))
			if err := validateNPMTarball(ctx, tarballPath, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

// manifestUnits returns one unit object per profile.
func manifestUnits(manifest jsonObject, typeValue string, profiles []string) ([]jsonObject, error) {
	if typeValue == "profile" {
		return []jsonObject{manifest}, nil
	}
	profileUnits, ok, err := manifest.object("profiles", manifestFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, validationError("dshhome 缺少 profiles 对象。")
	}
	units := make([]jsonObject, 0, len(profiles))
	for _, profile := range profiles {
		unit, err := objectFromRaw(profileUnits[profile], "profiles."+profile)
		if err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	return units, nil
}

// --- instance content restoration ----------------------------------------

// restoreInstanceFiles fills instances/<id>/dsh-home from the staging tree:
// path mapping, profile package.json materialization, external resources,
// snapshot links and the origin manifest.
func (s *Service) restoreInstanceFiles(ctx context.Context, instance store.Instance, inspection *inspection, manifest jsonObject, resources []resourceSpec, mappings []importMapping, excluded int, stagingDir string, logf func(string)) error {
	home := filepath.Join(s.dirs.InstancesDir, instance.ID, "dsh-home")
	for _, dir := range []string{home, filepath.Join(s.dirs.InstancesDir, instance.ID, "workspace"), filepath.Join(s.dirs.InstancesDir, instance.ID, "agents-home")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			s.logger.Error("创建实例目录失败", "dir", dir, "error", err)
			return internalError("创建实例目录失败，请检查数据目录权限。")
		}
	}
	for _, mapping := range mappings {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		source := filepath.Join(stagingDir, filepath.FromSlash(mapping.source))
		target := filepath.Join(home, filepath.FromSlash(mapping.target))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			s.logger.Error("创建实例子目录失败", "target", mapping.target, "error", err)
			return internalError("写入实例文件失败，请检查数据目录权限。")
		}
		if err := copyFileCtx(ctx, source, target); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			s.logger.Error("复制整合包文件失败", "source", mapping.source, "target", mapping.target, "error", err)
			return internalError("写入实例文件失败，请检查数据目录权限。")
		}
	}
	s.emitLogf(logf, "已恢复 %d 个文件到实例 dsh-home（跳过 %d 项敏感/未映射文件）。", len(mappings), excluded)

	if inspection.format == FormatDSPack {
		units, err := manifestUnits(manifest, mustManifestType(manifest), inspection.profiles)
		if err != nil {
			return err
		}
		for index, profile := range inspection.profiles {
			if err := s.materializeProfile(ctx, home, stagingDir, profile, units[index], manifest, logf); err != nil {
				return err
			}
		}
		if err := s.downloadResources(ctx, home, inspection, manifest, resources, logf); err != nil {
			return err
		}
	} else if err := s.restoreSnapshotLinks(ctx, home, inspection); err != nil {
		return err
	}

	if len(inspection.manifestRaw) > 0 {
		if err := os.WriteFile(filepath.Join(home, originFileName), inspection.manifestRaw, 0o600); err != nil {
			s.logger.Error("写入整合包来源清单失败", "error", err)
			return internalError("写入实例文件失败，请检查数据目录权限。")
		}
	}
	return nil
}

const originFileName = ".hdsl-pack-origin.json"

// mustManifestType returns the manifest type (validated during preview).
func mustManifestType(manifest jsonObject) string {
	typeValue, err := manifest.requiredString("type", manifestFile)
	if err != nil {
		return "profile"
	}
	return typeValue
}

// importMapping maps one archive entry to one dsh-home relative target.
type importMapping struct {
	source string // path inside the staging dir
	target string // path relative to instanced dsh-home
}

// importMappings computes the dsh-home mapping for dspack profile/dshhome and
// Overture snapshots (docs/DSH_INTEGRATION.md §7.4, reference PackService.
// mappings). Sensitive/non-mapped entries are skipped and counted.
func (s *Service) importMappings(inspection *inspection, manifest jsonObject, stagingDir string, extractedFiles []string) ([]importMapping, int, error) {
	extractedSet := make(map[string]struct{}, len(extractedFiles))
	for _, file := range extractedFiles {
		extractedSet[file] = struct{}{}
	}
	profiles := make(map[string]struct{}, len(inspection.profiles))
	for _, profile := range inspection.profiles {
		profiles[profile] = struct{}{}
	}
	typeValue := mustManifestType(manifest)
	snapshot := inspection.format == FormatOverture
	defaultProfile := inspection.defaultProfile

	mappings := make([]importMapping, 0, len(extractedFiles))
	targets := make(map[string]string, len(extractedFiles))
	excluded := 0
	for _, source := range extractedFiles {
		target := ""
		switch {
		case snapshot:
			if source == snapshotMarkerFile {
				continue
			}
			target = source
		case source == dspackMarkerFile || source == manifestFile:
			continue
		case strings.HasPrefix(source, "overrides/"):
			rest := strings.TrimPrefix(source, "overrides/")
			if typeValue == "dshhome" {
				target = rest
			} else {
				target = "profiles/" + defaultProfile + "/" + rest
			}
		case strings.HasPrefix(source, "home/"):
			target = strings.TrimPrefix(source, "home/")
		case isMachineFileName(source):
			target = "profiles/" + defaultProfile + "/" + source
			if typeValue == "dshhome" {
				if _, overridden := extractedSet["overrides/"+target]; overridden {
					continue
				}
			}
		default:
			// vendor/, resources and unknown top-level files are handled by
			// materialize/download steps or intentionally ignored.
			continue
		}
		if err := validateRelativePath(target, s.limits); err != nil {
			return nil, 0, err
		}
		if excludedPath(target, false, snapshot) {
			excluded++
			continue
		}
		if strings.HasPrefix(target, "profiles/") {
			parts := strings.SplitN(target, "/", 3)
			if len(parts) < 2 {
				return nil, 0, validationError("非法 profile 目标路径：" + target)
			}
			if _, declared := profiles[parts[1]]; !declared {
				return nil, 0, validationError("包引用未声明 profile：" + target)
			}
		}
		key := strings.ToLower(target)
		if previous, duplicate := targets[key]; duplicate {
			return nil, 0, validationError("整合包映射后目标冲突：" + target + "（与 " + previous + " 冲突）")
		}
		targets[key] = target
		mappings = append(mappings, importMapping{source: source, target: target})
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].target < mappings[j].target })
	// Files must not be ancestors of other files after mapping.
	for i := 1; i < len(mappings); i++ {
		previous := strings.ToLower(mappings[i-1].target)
		if strings.HasPrefix(strings.ToLower(mappings[i].target), previous+"/") {
			return nil, 0, validationError("整合包映射后的文件/目录冲突：" + mappings[i].target)
		}
	}
	return mappings, excluded, nil
}

func isMachineFileName(rel string) bool {
	_, ok := machineFileNames[rel]
	return ok
}

// materializeProfile rebuilds profiles/<profile>/package.json from the manifest
// unit (reference PackService.materialize: bundles + dependencies + patch) and
// copies vendored tarballs into profiles/<profile>/vendor-blobs/.
func (s *Service) materializeProfile(ctx context.Context, home, stagingDir, profile string, unit jsonObject, manifest jsonObject, logf func(string)) error {
	profileDir := filepath.Join(home, "profiles", profile)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		s.logger.Error("创建 profile 目录失败", "profile", profile, "error", err)
		return internalError("写入实例文件失败，请检查数据目录权限。")
	}
	pkg := make(map[string]any)
	packageJSONPath := filepath.Join(profileDir, "package.json")
	if raw, readErr := os.ReadFile(packageJSONPath); readErr == nil {
		_ = json.Unmarshal(raw, &pkg)
	}
	if pkg == nil {
		pkg = make(map[string]any)
	}
	if name, _ := pkg["name"].(string); strings.TrimSpace(name) == "" {
		pkg["name"] = "dsh-profile-" + profile
	}
	pkg["private"] = true

	dependencies := make(map[string]any)
	var rawDependencies map[string]json.RawMessage
	if depsRaw, ok := unit[dependenciesField]; ok {
		if err := json.Unmarshal(depsRaw, &rawDependencies); err != nil {
			return validationError("dependencies 必须为对象。")
		}
	}
	coordinates := make([]string, 0, len(rawDependencies))
	for coordinate := range rawDependencies {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)
	vendors, _, _ := manifest.object("vendored", manifestFile)
	for _, coordinate := range coordinates {
		spec, err := rawString(rawDependencies[coordinate], "依赖版本")
		if err != nil {
			return err
		}
		packageName, err := dependencyPackageName(coordinate)
		if err != nil {
			return err
		}
		finalSpec := spec
		vendored := false
		if vendors != nil {
			if vendorRaw, ok := vendors[coordinate]; ok {
				vendorEntry, err := objectFromRaw(vendorRaw, "vendored."+coordinate)
				if err != nil {
					return err
				}
				vendoredPath, err := vendorEntry.requiredString("path", "vendored."+coordinate)
				if err != nil {
					return err
				}
				sha, err := vendorEntry.requiredString("sha256", "vendored."+coordinate)
				if err != nil {
					return err
				}
				blob := "vendor-blobs/" + strings.ToLower(sha) + ".tgz"
				source := filepath.Join(stagingDir, filepath.FromSlash(vendoredPath))
				target := filepath.Join(profileDir, filepath.FromSlash(blob))
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					return internalError("写入实例文件失败，请检查数据目录权限。")
				}
				if err := copyFileCtx(ctx, source, target); err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return ctxErr
					}
					s.logger.Error("复制 vendored tarball 失败", "path", vendoredPath, "error", err)
					return internalError("写入实例文件失败，请检查数据目录权限。")
				}
				finalSpec = "file:" + blob
				vendored = true
			}
		}
		if !vendored {
			if match := gitCoordRE.FindStringSubmatch(coordinate); match != nil {
				finalSpec = "github:" + match[1] + "/" + match[2] + "#" + spec
				if match[3] != "" {
					finalSpec += "&path:" + match[4]
				}
			}
		}
		dependencies[packageName] = finalSpec
	}
	pkg["dependencies"] = dependencies

	dsh, _ := pkg["dsh"].(map[string]any)
	if dsh == nil {
		dsh = make(map[string]any)
		pkg["dsh"] = dsh
	}
	profileSection, _ := dsh["profile"].(map[string]any)
	if profileSection == nil {
		profileSection = make(map[string]any)
		dsh["profile"] = profileSection
	}
	bundles := make([]any, 0)
	if bundlesRaw, ok := unit[bundlesField]; ok {
		var items []json.RawMessage
		if err := json.Unmarshal(bundlesRaw, &items); err == nil {
			for _, item := range items {
				if text, err := rawString(item, "bundle"); err == nil {
					bundles = append(bundles, text)
				}
			}
		}
	}
	profileSection["bundles"] = bundles
	if err := writeJSONFile(packageJSONPath, pkg); err != nil {
		s.logger.Error("写入 profile package.json 失败", "profile", profile, "error", err)
		return internalError("写入实例文件失败，请检查数据目录权限。")
	}

	if patchRaw, ok := unit[patchField]; ok && strings.TrimSpace(string(patchRaw)) != "null" {
		patch, err := rawString(patchRaw, "patch")
		if err != nil {
			return err
		}
		if strings.TrimSpace(patch) != "" {
			patchPath := filepath.Join(profileDir, "cordis.patch.yml")
			if _, statErr := os.Lstat(patchPath); errors.Is(statErr, os.ErrNotExist) {
				if writeErr := os.WriteFile(patchPath, []byte(patch), 0o600); writeErr != nil {
					return internalError("写入实例文件失败，请检查数据目录权限。")
				}
			}
		}
	}
	s.emitLogf(logf, "已按 manifest 重建 profile %s 的 package.json。", profile)
	return nil
}

// writeJSONFile writes an indented JSON file with 0600 permissions.
func writeJSONFile(filePath string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filePath, data, 0o600)
}

// downloadResources fetches and verifies manifest-declared external resources
// (never executes them).
func (s *Service) downloadResources(ctx context.Context, home string, inspection *inspection, manifest jsonObject, resources []resourceSpec, logf func(string)) error {
	if len(resources) == 0 {
		return nil
	}
	typeValue := mustManifestType(manifest)
	for _, resource := range resources {
		resourcePath, err := resource.object.requiredString("path", "资源")
		if err != nil {
			return err
		}
		targetRel := resourcePath
		if !resource.skill && typeValue == "profile" {
			targetRel = path.Join("profiles", inspection.defaultProfile, resourcePath)
		}
		if err := validateRelativePath(targetRel, s.limits); err != nil {
			return err
		}
		if excludedPath(targetRel, false, false) {
			return validationError("外部资源落点敏感：" + resourcePath)
		}
		destination := filepath.Join(home, filepath.FromSlash(targetRel))
		if _, statErr := os.Lstat(destination); statErr == nil {
			return validationError("外部资源与归档内容冲突：" + resourcePath)
		}
		sha, err := resource.object.requiredString("sha256", "资源")
		if err != nil {
			return err
		}
		size, ok, err := resource.object.integer("size", "资源")
		if err != nil {
			return err
		}
		if !ok {
			return validationError("资源缺少 size：" + resourcePath)
		}
		urls, ok, err := resource.object.array("urls", "资源")
		if err != nil {
			return err
		}
		if !ok || len(urls) == 0 {
			return validationError("资源 urls 不能为空：" + resourcePath)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return internalError("写入实例文件失败，请检查数据目录权限。")
		}
		var lastErr error
		downloaded := false
		for _, rawURL := range urls {
			text, err := rawString(rawURL, "资源 url")
			if err != nil {
				return err
			}
			if downloadErr := s.downloadResource(ctx, text, destination, size, sha); downloadErr != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				lastErr = downloadErr
				s.logger.Warn("外部资源下载失败，尝试下一个地址", "path", resourcePath, "error", downloadErr)
				continue
			}
			downloaded = true
			break
		}
		if !downloaded {
			s.logger.Error("外部资源下载失败", "path", resourcePath, "error", lastErr)
			return apierr.New(502, apierr.CodeUpstreamError, "下载整合包外部资源失败："+resourcePath)
		}
		s.emitLogf(logf, "已下载并校验外部资源 %s。", resourcePath)
	}
	return nil
}

// downloadResource downloads one HTTPS resource to destination and verifies its
// size and SHA-256.
func (s *Service) downloadResource(ctx context.Context, rawURL, destination string, size int64, sha string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".download-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temp.Close()
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(response.Body, size+1))
	if err != nil {
		return err
	}
	if written != size {
		return fmt.Errorf("资源大小不符：声明 %d，实际 %d", size, written)
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), sha) {
		return errors.New("资源 SHA-256 校验失败")
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, destination); err != nil {
		return err
	}
	committed = true
	return nil
}

// restoreSnapshotLinks recreates the internal symlinks declared by an Overture
// snapshot manifest (targets stay inside dsh-home; validated during preview).
func (s *Service) restoreSnapshotLinks(ctx context.Context, home string, inspection *inspection) error {
	manifest, err := parseJSONObject(inspection.manifestRaw, snapshotMarkerFile)
	if err != nil {
		return err
	}
	raw, ok := manifest["links"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil
	}
	for index, item := range items {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		link, err := objectFromRaw(item, fmt.Sprintf("快照链接 #%d", index+1))
		if err != nil {
			return err
		}
		linkPath, err := link.requiredString("path", "快照链接")
		if err != nil {
			return err
		}
		target, err := link.requiredString("target", "快照链接")
		if err != nil {
			return err
		}
		source := filepath.Join(home, filepath.FromSlash(linkPath))
		resolved := path.Clean(path.Join(path.Dir(linkPath), target))
		targetPath := filepath.Join(home, filepath.FromSlash(resolved))
		if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
			return internalError("写入实例文件失败，请检查数据目录权限。")
		}
		relative, err := filepath.Rel(filepath.Dir(source), targetPath)
		if err != nil {
			return validationError("快照链接目标非法：" + linkPath)
		}
		if err := os.Symlink(relative, source); err != nil {
			s.logger.Error("创建快照内部链接失败", "path", linkPath, "error", err)
			return validationError("无法创建快照内部链接（Windows 需开发者模式）：" + linkPath)
		}
	}
	return nil
}

// walkExtractedFiles lists the regular files under a staging directory as
// slash-separated relative paths (sorted).
func walkExtractedFiles(root string) ([]string, error) {
	files := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("staging 目录含非普通文件: %s", filePath)
		}
		if filePath == root {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// copyFileCtx copies one regular file with 0600 permissions, checking ctx
// between chunks.
func copyFileCtx(ctx context.Context, source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("源文件不是普通文件: %s", source)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = output.Close()
			_ = os.Remove(target)
		}
	}()
	buffer := make([]byte, uploadBufferBytes)
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			if _, writeErr := output.Write(buffer[:read]); writeErr != nil {
				return writeErr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

// sha256File hashes a file with ctx cancellation checks.
func sha256File(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	buffer := make([]byte, uploadBufferBytes)
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hasher.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// importInstanceName derives a valid instance display name (1-40 characters).
func importInstanceName(requested, packageName, previewName string) string {
	name := strings.TrimSpace(requested)
	if name == "" {
		name = strings.TrimSpace(packageName)
	}
	if name == "" {
		name = strings.TrimSpace(previewName)
	}
	if name == "" {
		name = "整合包实例"
	}
	runes := []rune(name)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	return string(runes)
}

// emitLogf sends one redacted progress line to the job log and manager log.
func (s *Service) emitLogf(logf func(string), format string, args ...any) {
	line := strings.TrimSpace(logging.Redact(fmt.Sprintf(format, args...)))
	if line == "" {
		return
	}
	if logf != nil {
		logf(line)
	}
	s.logger.Info(line, "component", "pack")
}

// forwardLogf returns a redacting sink for high-volume pnpm output.
func (s *Service) forwardLogf(logf func(string)) func(string) {
	return func(line string) {
		line = strings.TrimSpace(logging.Redact(line))
		if line == "" {
			return
		}
		if logf != nil {
			logf(line)
			return
		}
		s.logger.Debug(line, "component", "pack")
	}
}
