// SPDX-License-Identifier: MIT

package pack

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	goruntime "runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
)

const (
	dspackMarkerFile   = "dspack.json"
	snapshotMarkerFile = "dsh-snapshot.json"
	yamlMarkerFile     = "dsh-pack.yaml"
	manifestFile       = "manifest.json"
	profileYAMLFile    = "profile.yaml"
	lockFileName       = "pnpm-lock.yaml"
)

// inspection is the validated result of inspecting one archive
// (docs/DSH_INTEGRATION.md §7.1/§7.3).
type inspection struct {
	format         Format
	name           string
	version        string
	dshVersion     string
	defaultProfile string
	profiles       []string
	warnings       []string
	unpackedBytes  int64
	manifestRaw    []byte
}

// metadataReader reads bounded metadata files from an archive that
// internal/safe already validated. It is used because internal/safe exposes
// only entry metadata and extraction, not arbitrary file reads.
type metadataReader struct {
	files map[string]*zip.File
}

func newMetadataReader(zr *zip.ReadCloser) *metadataReader {
	files := make(map[string]*zip.File, len(zr.File))
	for _, file := range zr.File {
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		files[file.Name] = file
	}
	return &metadataReader{files: files}
}

func (r *metadataReader) has(name string) bool {
	_, ok := r.files[name]
	return ok
}

func (r *metadataReader) read(name string, maxBytes int64) ([]byte, error) {
	file, ok := r.files[name]
	if !ok {
		return nil, validationError("整合包缺少 " + name + "。")
	}
	if file.UncompressedSize64 > uint64(maxBytes) {
		return nil, validationError(fmt.Sprintf("%s 超过 %d 字节元数据上限。", name, maxBytes))
	}
	reader, err := file.Open()
	if err != nil {
		return nil, validationError("整合包元数据损坏：" + name + "。")
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, validationError("读取整合包元数据失败：" + name + "。")
	}
	if int64(len(data)) > maxBytes {
		return nil, validationError(fmt.Sprintf("%s 超过 %d 字节元数据上限。", name, maxBytes))
	}
	return data, nil
}

// mapSafeError converts internal/safe failures into *apierr.Error.
func mapSafeError(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := apierr.As(err); ok {
		return apiErr
	}
	if kind := safe.KindOf(err); kind != "" {
		if kind == safe.ErrCancelled {
			return context.Canceled
		}
		var safeErr *safe.Error
		message := string(kind)
		if errors.As(err, &safeErr) && strings.TrimSpace(safeErr.Message) != "" {
			message = safeErr.Message
		}
		return validationError("整合包安全校验失败：" + message)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return internalError("检查整合包失败，请稍后重试。")
}

// inspectArchive validates the archive container with internal/safe, detects the
// package format and parses the bounded manifest metadata.
func inspectArchive(ctx context.Context, archivePath, uploadName string, limits safe.Limits) (*inspection, error) {
	limits = effectiveLimits(limits)
	archive, err := safe.OpenZip(archivePath, limits)
	if err != nil {
		return nil, mapSafeError(err)
	}
	entries := archive.Entries()
	if closeErr := archive.Close(); closeErr != nil {
		return nil, internalError("关闭整合包失败，请稍后重试。")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	files := make(map[string]safe.EntryInfo, len(entries))
	names := make(map[string]struct{}, len(entries))
	var total int64
	var hasDSPack, hasSnapshot, hasYAML bool
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name, "/")
		names[name] = struct{}{}
		if entry.IsDir {
			continue
		}
		files[name] = entry
		total += entry.UncompressedSize
		switch name {
		case dspackMarkerFile:
			hasDSPack = true
		case snapshotMarkerFile:
			hasSnapshot = true
		case yamlMarkerFile:
			hasYAML = true
		}
	}

	markerCount := 0
	for _, present := range []bool{hasDSPack, hasSnapshot, hasYAML} {
		if present {
			markerCount++
		}
	}
	if markerCount == 0 {
		return nil, validationError("无法识别整合包；旧 tgz 和缺少版本标记的 ZIP 尚不支持。")
	}
	if markerCount > 1 {
		return nil, validationError("整合包含多个冲突的格式标记。")
	}
	if hasYAML {
		return nil, validationError("Overture YAML 清单格式尚未支持，请导出 format 1 快照 ZIP。")
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, internalError("读取整合包失败，请稍后重试。")
	}
	defer func() { _ = zr.Close() }()
	meta := newMetadataReader(zr)

	if hasDSPack {
		return inspectDSPack(ctx, meta, files, names, total, limits)
	}
	return inspectOverture(ctx, meta, files, entries, total, uploadName, limits)
}

// inspectDSPack validates a dspack v2/v3 + manifest v4/v5 archive.
func inspectDSPack(ctx context.Context, meta *metadataReader, files map[string]safe.EntryInfo, names map[string]struct{}, unpackedBytes int64, limits safe.Limits) (*inspection, error) {
	markerRaw, err := meta.read(dspackMarkerFile, limits.MaxMetadataBytes)
	if err != nil {
		return nil, err
	}
	marker, err := parseJSONObject(markerRaw, dspackMarkerFile)
	if err != nil {
		return nil, err
	}
	formatValue, err := marker.requiredString("format", dspackMarkerFile)
	if err != nil {
		return nil, err
	}
	if formatValue != string(FormatDSPack) {
		return nil, validationError("不支持的 dspack 格式标记：" + formatValue)
	}
	containerVersion, ok, err := marker.integer("version", dspackMarkerFile)
	if err != nil {
		return nil, err
	}
	if !ok || (containerVersion != 2 && containerVersion != 3) {
		return nil, validationError(fmt.Sprintf("不支持该 dspack 容器版本：%d", containerVersion))
	}

	manifestRaw, err := meta.read(manifestFile, limits.MaxMetadataBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := parseJSONObject(manifestRaw, manifestFile)
	if err != nil {
		return nil, err
	}
	manifestVersion, ok, err := manifest.integer("manifestVersion", manifestFile)
	if err != nil {
		return nil, err
	}
	if !ok || (manifestVersion != 4 && manifestVersion != 5) {
		return nil, validationError(fmt.Sprintf("不支持的 manifestVersion=%d；当前支持 4/5。", manifestVersion))
	}
	name, err := manifest.requiredString("name", manifestFile)
	if err != nil {
		return nil, err
	}
	version, err := manifest.requiredString("version", manifestFile)
	if err != nil {
		return nil, err
	}
	dshVersion, hasDSHVersion, err := manifest.optionalString("dshVersion", manifestFile)
	if err != nil {
		return nil, err
	}
	if err := validateDSHVersion(dshVersion, hasDSHVersion); err != nil {
		return nil, err
	}
	if err := validateVersions(dshVersion, hasDSHVersion, manifest); err != nil {
		return nil, err
	}
	typeValue, err := manifest.requiredString("type", manifestFile)
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0, 4)
	var profiles []string
	var units []jsonObject
	var defaultProfile string
	switch typeValue {
	case "profile":
		profileName, hasProfileName, err := manifest.optionalString("profileName", manifestFile)
		if err != nil {
			return nil, err
		}
		defaultProfile = "pack"
		if hasProfileName {
			defaultProfile = profileName
		}
		if err := validateProfileName(defaultProfile); err != nil {
			return nil, err
		}
		if err := validateManifestUnit(manifest, "profile", limits); err != nil {
			return nil, err
		}
		profiles = []string{defaultProfile}
		units = []jsonObject{manifest}
	case "dshhome":
		if containerVersion != 3 || manifestVersion != 5 {
			return nil, validationError("该容器/manifest 不支持 type=dshhome（需要 dspack v3 + manifestVersion 5）。")
		}
		profileUnits, ok, err := manifest.object("profiles", manifestFile)
		if err != nil {
			return nil, err
		}
		if !ok || len(profileUnits) == 0 {
			return nil, validationError("dshhome.profiles 必须为非空对象。")
		}
		seen := make(map[string]struct{}, len(profileUnits))
		for _, profile := range sortedKeys(profileUnits) {
			if err := validateProfileName(profile); err != nil {
				return nil, err
			}
			if profile == "web" || profile == "headless" {
				return nil, validationError("dshhome 含基线模板 profile：" + profile)
			}
			key := strings.ToLower(profile)
			if _, duplicate := seen[key]; duplicate {
				return nil, validationError("dshhome profile 名称冲突：" + profile)
			}
			seen[key] = struct{}{}
			unit, err := objectFromRaw(profileUnits[profile], "profiles."+profile)
			if err != nil {
				return nil, err
			}
			if err := validateManifestUnit(unit, "profiles."+profile, limits); err != nil {
				return nil, err
			}
			profiles = append(profiles, profile)
			units = append(units, unit)
		}
		defaultProfile, err = manifest.requiredString("defaultProfile", manifestFile)
		if err != nil {
			return nil, err
		}
		if !containsString(profiles, defaultProfile) {
			return nil, validationError("defaultProfile 未出现在 profiles 中。")
		}
	default:
		return nil, validationError("该容器/manifest 不支持 type=" + typeValue + "。")
	}

	if containerVersion == 2 && hasEntryPrefix(names, "home/") {
		return nil, validationError("容器 v2 不支持 home/，请使用 v3。")
	}
	if typeValue == "dshhome" && hasEntryPrefix(names, "home/") {
		return nil, validationError("dshhome 不应包含独立 home/ 层。")
	}

	if vendoredRaw, ok := manifest["vendored"]; ok && strings.TrimSpace(string(vendoredRaw)) != "null" {
		lockNames := map[string]struct{}{}
		if _, hasLock := files[lockFileName]; hasLock {
			names, err := lockPackageNames(meta, limits)
			if err != nil {
				return nil, err
			}
			lockNames = names
		}
		if err := validateVendoredShape(manifest, files, units, limits, lockNames); err != nil {
			return nil, err
		}
		warnings = append(warnings, "含 vendored 内嵌 tarball；导入时将校验 SHA-256 并解包，仅直接依赖改为本地 tarball。")
	}
	if _, err := validateResourcesShape(manifest, limits); err != nil {
		return nil, err
	}
	warnings = append(warnings, "导入后需要 pnpm 恢复依赖；内嵌 tarball 不代表完全离线，安装脚本由启动器的依赖策略处理。")
	if manifest.has("launchers") {
		warnings = append(warnings, "包声明的 launchers 兼容信息仅供参考，本启动器未视作已兼容。")
	}
	if manifest.has("dshVersions") {
		warnings = append(warnings, "作者声明的 dshVersions Harness 兼容版本仅供参考。")
	}
	if exportInfo, ok, err := manifest.object("hdslShareExport", manifestFile); err == nil && ok {
		if rawWarnings, present, err := exportInfo.array("warnings", "hdslShareExport"); err == nil && present {
			for _, rawWarning := range rawWarnings {
				text, err := rawString(rawWarning, "hdslShareExport.warnings 项")
				if err != nil {
					return nil, err
				}
				warnings = append(warnings, text)
			}
		}
	}
	appendDesktopWarning(&warnings, profiles)

	return &inspection{
		format:         FormatDSPack,
		name:           name,
		version:        version,
		dshVersion:     dshVersion,
		defaultProfile: defaultProfile,
		profiles:       profiles,
		warnings:       warnings,
		unpackedBytes:  unpackedBytes,
		manifestRaw:    manifestRaw,
	}, nil
}

// inspectOverture validates an Overture snapshot format 1 archive.
func inspectOverture(ctx context.Context, meta *metadataReader, files map[string]safe.EntryInfo, entries []safe.EntryInfo, unpackedBytes int64, uploadName string, limits safe.Limits) (*inspection, error) {
	snapshotRaw, err := meta.read(snapshotMarkerFile, limits.MaxMetadataBytes)
	if err != nil {
		return nil, err
	}
	snapshot, err := parseJSONObject(snapshotRaw, snapshotMarkerFile)
	if err != nil {
		return nil, err
	}
	formatValue, ok, err := snapshot.integer("format", snapshotMarkerFile)
	if err != nil {
		return nil, err
	}
	if !ok || formatValue != 1 {
		return nil, validationError(fmt.Sprintf("不支持 Overture snapshot format=%d。", formatValue))
	}
	platform, err := snapshot.requiredString("platform", snapshotMarkerFile)
	if err != nil {
		return nil, err
	}
	if local := currentPlatform(); platform != local {
		return nil, validationError(fmt.Sprintf("快照平台 %s 与本机 %s 不符；含已安装依赖，不能跨平台恢复。", platform, local))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	var profiles []string
	dshVersion := ""
	hasDSHVersion := false
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		parts := strings.Split(entry.Name, "/")
		if len(parts) != 3 || parts[0] != "profiles" || parts[2] != profileYAMLFile || parts[1] == "node_modules" {
			continue
		}
		profile := parts[1]
		if err := validateProfileName(profile); err != nil {
			return nil, err
		}
		raw, err := meta.read(entry.Name, maxProfileYAMLBytes)
		if err != nil {
			return nil, err
		}
		profileVersion, present, err := parseProfileYAML(raw)
		if err != nil {
			return nil, err
		}
		if present {
			if hasDSHVersion && dshVersion != profileVersion {
				return nil, validationError("快照多个 profile 使用不同 Harness 版本，请分别导出。")
			}
			dshVersion = profileVersion
			hasDSHVersion = true
		}
		profiles = append(profiles, profile)
	}
	if len(profiles) == 0 {
		return nil, validationError("快照缺少 profiles/<id>/profile.yaml。")
	}
	for _, profile := range profiles {
		if err := validateSnapshotProfilePackage(meta, files, profile, limits); err != nil {
			return nil, err
		}
	}

	warnings := []string{
		"同平台快照仍受 Node ABI、CPU 架构和原生依赖影响；元数据未声明这些信息，需要启动验证。",
	}
	if len(profiles) > 1 {
		warnings = append(warnings, "快照未标准化默认 profile；默认选择包内第一个 profile："+profiles[0])
	}
	linkCount, linkWarnings, err := validateSnapshotLinks(snapshot, files, limits)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, linkWarnings...)
	if linkCount > 0 {
		warnings = append(warnings, fmt.Sprintf("快照包含 %d 个内部符号链接；导入时如无链接创建权限将失败。", linkCount))
	}
	if rawWarnings, present, err := snapshot.array("warnings", snapshotMarkerFile); err == nil && present {
		for _, rawWarning := range rawWarnings {
			text, err := rawString(rawWarning, "dsh-snapshot.warnings 项")
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, "源快照提示："+text)
		}
	}
	if snapshot.has("private") {
		warnings = append(warnings, "源包含隐私类别声明；本启动器仍跳过凭据、设置和会话文件。")
	}
	appendDesktopWarning(&warnings, profiles)

	return &inspection{
		format:         FormatOverture,
		name:           snapshotName(uploadName),
		version:        "snapshot-1",
		dshVersion:     dshVersion,
		defaultProfile: profiles[0],
		profiles:       profiles,
		warnings:       warnings,
		unpackedBytes:  unpackedBytes,
		manifestRaw:    snapshotRaw,
	}, nil
}

// parseProfileYAML reads the optional dshVersion from one Overture profile.yaml.
func parseProfileYAML(raw []byte) (string, bool, error) {
	var document struct {
		DSHVersion string `yaml:"dshVersion"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return "", false, validationError("快照 profile.yaml 无法安全解析。")
	}
	version := strings.TrimSpace(document.DSHVersion)
	if version == "" {
		return "", false, nil
	}
	if !semverExactRE.MatchString(version) {
		return "", false, validationError("快照 dshVersion 不是精确版本：" + version)
	}
	return version, true, nil
}

// validateSnapshotProfilePackage validates one snapshot profile package.json
// dependency references.
func validateSnapshotProfilePackage(meta *metadataReader, files map[string]safe.EntryInfo, profile string, limits safe.Limits) error {
	name := "profiles/" + profile + "/package.json"
	if _, ok := files[name]; !ok {
		return validationError("快照 profile 缺少 package.json：" + profile)
	}
	raw, err := meta.read(name, limits.MaxMetadataBytes)
	if err != nil {
		return err
	}
	pkg, err := parseJSONObject(raw, name)
	if err != nil {
		return err
	}
	depsRaw, ok := pkg[dependenciesField]
	if !ok || strings.TrimSpace(string(depsRaw)) == "null" {
		return nil
	}
	var dependencies map[string]json.RawMessage
	if err := json.Unmarshal(depsRaw, &dependencies); err != nil {
		return validationError("快照 package.json dependencies 必须为对象：" + profile)
	}
	for _, coordinate := range sortedKeys(dependencies) {
		spec, err := rawString(dependencies[coordinate], "快照依赖版本")
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(spec, "file:"), strings.HasPrefix(spec, "link:"):
			local := spec[strings.IndexByte(spec, ':')+1:]
			if err := validateRelativePath(local, limits); err != nil {
				return err
			}
			target := "profiles/" + profile + "/" + local
			if !entryOrPrefix(files, target) {
				return validationError("快照本地依赖本体缺失：" + profile + "（" + coordinate + "）")
			}
		case strings.HasPrefix(spec, "/"), strings.HasPrefix(spec, "."), strings.Contains(spec, "\\"), windowsDriveRE.MatchString(spec):
			return validationError("快照依赖仍引用源机器的路径：" + profile + "（" + coordinate + "）")
		}
	}
	return nil
}

// validateSnapshotLinks performs basic snapshot link validation (full graph
// validation and link creation happen at import time).
func validateSnapshotLinks(snapshot jsonObject, files map[string]safe.EntryInfo, limits safe.Limits) (int, []string, error) {
	raw, ok := snapshot["links"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return 0, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return 0, nil, validationError("快照 links 必须是数组。")
	}
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		link, err := objectFromRaw(item, fmt.Sprintf("快照链接 #%d", index+1))
		if err != nil {
			return 0, nil, err
		}
		linkPath, err := link.requiredString("path", "快照链接")
		if err != nil {
			return 0, nil, err
		}
		if err := validateRelativePath(linkPath, limits); err != nil {
			return 0, nil, err
		}
		target, err := link.requiredString("target", "快照链接")
		if err != nil {
			return 0, nil, err
		}
		if strings.HasPrefix(target, "/") || strings.Contains(target, "\\") || strings.Contains(target, ":") || strings.ContainsRune(target, 0) {
			return 0, nil, validationError("快照不允许绝对或外部链接：" + linkPath)
		}
		resolved := path.Clean(path.Join(path.Dir(linkPath), target))
		if err := validateRelativePath(resolved, limits); err != nil {
			return 0, nil, err
		}
		if resolved == linkPath || strings.HasPrefix(resolved, linkPath+"/") || strings.HasPrefix(linkPath, resolved+"/") {
			return 0, nil, validationError("快照链接形成目录循环：" + linkPath)
		}
		key := strings.ToLower(linkPath)
		if _, duplicate := seen[key]; duplicate {
			return 0, nil, validationError("快照链接重复：" + linkPath)
		}
		seen[key] = struct{}{}
		if _, conflict := files[linkPath]; conflict {
			return 0, nil, validationError("快照链接与文件冲突：" + linkPath)
		}
		if !entryOrPrefix(files, resolved) {
			return 0, nil, validationError("快照链接目标缺失：" + linkPath)
		}
	}
	return len(items), nil, nil
}

// lockPackageNames extracts package names from the archive's pnpm-lock.yaml.
func lockPackageNames(meta *metadataReader, limits safe.Limits) (map[string]struct{}, error) {
	raw, err := meta.read(lockFileName, limits.MaxMetadataBytes)
	if err != nil {
		return nil, err
	}
	var lockDocument struct {
		Packages  map[string]json.RawMessage `yaml:"packages"`
		Snapshots map[string]json.RawMessage `yaml:"snapshots"`
	}
	if err := yaml.Unmarshal(raw, &lockDocument); err != nil {
		return nil, validationError("无法解析 pnpm-lock.yaml。")
	}
	names := make(map[string]struct{})
	for _, section := range []map[string]json.RawMessage{lockDocument.Packages, lockDocument.Snapshots} {
		for key := range section {
			name := key
			if index := strings.IndexByte(name, '('); index > 0 {
				name = name[:index]
			}
			if at := strings.LastIndexByte(name, '@'); at > 0 {
				name = name[:at]
			}
			name = strings.TrimPrefix(name, "/")
			if name != "" {
				names[name] = struct{}{}
			}
		}
	}
	return names, nil
}

// currentPlatform maps the Go platform to the Overture snapshot platform names.
func currentPlatform() string {
	switch goruntime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

// snapshotName derives the preview display name from the uploaded file name.
func snapshotName(uploadName string) string {
	base := path.Base(strings.TrimSpace(uploadName))
	if base == "." || base == "/" {
		base = ""
	}
	lowered := strings.ToLower(base)
	if strings.HasSuffix(lowered, ".zip") {
		base = base[:len(base)-len(".zip")]
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." {
		base = "snapshot"
	}
	if len(base) > maxPreviewNameBytes {
		base = base[:maxPreviewNameBytes]
	}
	return base
}

// appendDesktopWarning adds the reference warning for desktop profiles.
func appendDesktopWarning(warnings *[]string, profiles []string) {
	for _, profile := range profiles {
		if strings.EqualFold(profile, "desktop") {
			*warnings = append(*warnings, "部分新版 Harness CLI 将 desktop profile 保留给 Electron 桌面端；本启动器可导入其文件并恢复依赖，但直接通过 CLI 启动可能被拒绝。")
			return
		}
	}
}

func hasEntryPrefix(names map[string]struct{}, prefix string) bool {
	for name := range names {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func entryOrPrefix(files map[string]safe.EntryInfo, target string) bool {
	if _, ok := files[target]; ok {
		return true
	}
	prefix := target + "/"
	for name := range files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func sortedKeys[V any](object map[string]V) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
