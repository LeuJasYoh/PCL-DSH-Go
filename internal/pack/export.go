// SPDX-License-Identifier: MIT

package pack

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// gitExportRE matches a package.json github dependency spec produced by the
// plugin/import materialization: github:owner/repo#ref&path:/pkg.
var gitExportRE = regexp.MustCompile("^github:([^/]+)/([^#]+)#([^&]+)(&path:(.+))?$")

// exportFile is one file selected for export.
type exportFile struct {
	sourcePath string
	relative   string
}

// Export creates a dspack v3 + manifest v5 archive for one profile or the whole
// dsh-home of an instance, stores it under <cache>/exports and registers a 24h
// artifact (docs/M3_INTERFACES.md §3, docs/DSH_INTEGRATION.md §7.5).
func (s *Service) Export(ctx context.Context, in ExportInput, logf func(string)) (ExportResult, error) {
	in.InstanceID = strings.TrimSpace(in.InstanceID)
	in.Mode = strings.ToLower(strings.TrimSpace(in.Mode))
	if in.InstanceID == "" {
		return ExportResult{}, validationError("缺少 instanceId。")
	}
	if in.Mode != "profile" && in.Mode != "home" {
		return ExportResult{}, validationError("导出模式只支持 profile 或 home。")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ExportResult{}, ctxErr
	}
	instance, ok := s.store.GetInstance(in.InstanceID)
	if !ok {
		return ExportResult{}, notFoundError("实例不存在。")
	}
	home := filepath.Join(s.dirs.InstancesDir, instance.ID, "dsh-home")
	if info, statErr := os.Lstat(home); statErr != nil || !info.IsDir() {
		return ExportResult{}, notFoundError("实例 dsh-home 不存在，无法导出。")
	}
	s.emitLogf(logf, "开始导出实例 %s（模式 %s）…", instance.ID, in.Mode)

	profiles, defaultProfile, err := s.exportProfiles(home, instance, in.Mode)
	if err != nil {
		return ExportResult{}, err
	}
	files, skipped, err := s.collectExportFiles(ctx, home, profiles, in.Mode)
	if err != nil {
		return ExportResult{}, err
	}
	if len(files) == 0 {
		return ExportResult{}, validationError("没有可导出的文件。")
	}
	manifest, err := s.buildExportManifest(home, instance, profiles, defaultProfile, in.Mode)
	if err != nil {
		return ExportResult{}, err
	}

	if err := os.MkdirAll(s.exportsDir(), 0o700); err != nil {
		s.logger.Error("创建导出目录失败", "error", err)
		return ExportResult{}, internalError("创建导出目录失败，请检查数据目录权限。")
	}
	temp, err := os.CreateTemp(s.exportsDir(), ".export-*.tmp")
	if err != nil {
		s.logger.Error("创建导出临时文件失败", "error", err)
		return ExportResult{}, internalError("创建导出文件失败，请检查数据目录权限。")
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
		return ExportResult{}, internalError("创建导出文件失败，请检查数据目录权限。")
	}
	if err := s.writeExportZip(ctx, temp, manifest, files, profiles, defaultProfile, in.Mode); err != nil {
		return ExportResult{}, err
	}
	if err := temp.Sync(); err != nil {
		return ExportResult{}, internalError("写入导出文件失败，请检查数据目录权限。")
	}
	if err := temp.Close(); err != nil {
		return ExportResult{}, internalError("写入导出文件失败，请检查数据目录权限。")
	}

	// Producer self-check: the generated archive must pass the same consumer
	// validation as an upload before it is published.
	if _, err := inspectArchive(ctx, tempName, "export.zip", s.limits); err != nil {
		s.logger.Error("导出的整合包未通过校验", "error", err)
		return ExportResult{}, internalError("导出的整合包未通过安全校验，请查看日志。")
	}

	filename := s.exportFilename(instance, in.Mode, defaultProfile)
	finalPath := filepath.Join(s.exportsDir(), filename)
	for attempt := 1; ; attempt++ {
		if _, statErr := os.Lstat(finalPath); os.IsNotExist(statErr) {
			break
		}
		if attempt > 20 {
			return ExportResult{}, internalError("生成导出文件名失败，请稍后重试。")
		}
		filename = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(s.exportFilename(instance, in.Mode, defaultProfile), ".dspack.zip"), attempt, ".dspack.zip")
		finalPath = filepath.Join(s.exportsDir(), filename)
	}
	if err := os.Rename(tempName, finalPath); err != nil {
		s.logger.Error("发布导出文件失败", "error", err)
		return ExportResult{}, internalError("写入导出文件失败，请检查数据目录权限。")
	}
	committed = true
	info, err := os.Stat(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return ExportResult{}, internalError("读取导出文件失败，请稍后重试。")
	}
	artifactMeta, err := s.artifacts.Add(filename, finalPath, "application/zip", info.Size())
	if err != nil {
		_ = os.Remove(finalPath)
		s.logger.Error("登记导出 artifact 失败", "error", err)
		return ExportResult{}, internalError("登记导出文件失败，请稍后重试。")
	}
	s.emitLogf(logf, "导出完成：%s（%d 字节，跳过 %d 项敏感/未选中文件）。", filename, info.Size(), skipped)
	s.logger.Info("整合包导出完成",
		"instanceId", instance.ID, "filename", filename, "size", info.Size(), "mode", in.Mode,
		"profiles", profiles, "files", len(files), "skipped", skipped)
	return ExportResult{ArtifactID: artifactMeta.ID, Filename: filename, Size: info.Size()}, nil
}

// exportProfiles resolves the profiles to export and the default profile.
func (s *Service) exportProfiles(home string, instance store.Instance, mode string) ([]string, string, error) {
	profilesRoot := filepath.Join(home, "profiles")
	info, err := os.Lstat(profilesRoot)
	if err != nil || !info.IsDir() {
		return nil, "", notFoundError("实例没有 profiles 目录，无法导出。")
	}
	if mode == "profile" {
		profile := strings.TrimSpace(instance.Profile)
		if err := validateProfileName(profile); err != nil {
			return nil, "", err
		}
		profileDir := filepath.Join(profilesRoot, profile)
		if dirInfo, statErr := os.Lstat(profileDir); statErr != nil || !dirInfo.IsDir() {
			return nil, "", notFoundError("实例 profile " + profile + " 不存在，无法导出。")
		}
		return []string{profile}, profile, nil
	}
	entries, err := os.ReadDir(profilesRoot)
	if err != nil {
		return nil, "", notFoundError("读取实例 profiles 目录失败，无法导出。")
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "web" || name == "headless" || name == "node_modules" {
			continue
		}
		if err := validateProfileName(name); err != nil {
			s.logger.Warn("跳过非法 profile 名称", "profile", name, "error", err)
			continue
		}
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	if len(profiles) == 0 {
		return nil, "", validationError("实例没有可导出的 profile。")
	}
	defaultProfile := profiles[0]
	for _, profile := range profiles {
		if profile == instance.Profile {
			defaultProfile = profile
			break
		}
	}
	return profiles, defaultProfile, nil
}

// collectExportFiles walks dsh-home and selects shareable files, applying the
// credential/cache/session blacklist and the per-mode profile selection.
func (s *Service) collectExportFiles(ctx context.Context, home string, profiles []string, mode string) ([]exportFile, int, error) {
	files := make([]exportFile, 0, 128)
	skipped := 0
	var total int64
	err := filepath.WalkDir(home, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if filePath == home {
			return nil
		}
		relative, err := filepath.Rel(home, filePath)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relative)
		excluded := excludedPath(rel, true, false)
		selected := selectedProfilePath(rel, profiles)
		if entry.IsDir() {
			if excluded || !selected {
				skipped++
				return filepath.SkipDir
			}
			return nil
		}
		if excluded || !selected {
			skipped++
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return validationError("导出不跟随文件链接或 junction：" + rel)
		}
		if err := validateRelativePath(rel, s.limits); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > s.limits.MaxFileBytes || total+info.Size() > s.limits.MaxUnpackedBytes || len(files) >= s.limits.MaxEntries {
			return validationError("导出体积超过限制。")
		}
		total += info.Size()
		files = append(files, exportFile{sourcePath: filePath, relative: rel})
		return nil
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		if apiErr, ok := asValidationError(err); ok {
			return nil, 0, apiErr
		}
		s.logger.Error("遍历实例 dsh-home 失败", "error", err)
		return nil, 0, internalError("读取实例文件失败，请检查数据目录权限。")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	return files, skipped, nil
}

// selectedProfilePath reports whether a dsh-home path is selected for export:
// root-level files are always selected; under profiles/ only the listed
// profiles are.
func selectedProfilePath(rel string, profiles []string) bool {
	if rel == "profiles" {
		return true
	}
	if !strings.HasPrefix(rel, "profiles/") {
		return true
	}
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 2 {
		return false
	}
	return containsString(profiles, parts[1])
}

// buildExportManifest produces the pretty-printed manifest.json content.
func (s *Service) buildExportManifest(home string, instance store.Instance, profiles []string, defaultProfile, mode string) ([]byte, error) {
	share := map[string]any{
		"omitted": []string{"credentials", "settings", "sessions", "cordis.patch.yml", "cordis.patch.yaml"},
		"warnings": []string{
			"此为分享导出：凭据、全局设置、会话及 cordis.patch 配置未包含；导入后需要重新配置提供商和插件选项。这不是完整备份。",
		},
	}
	manifest := map[string]any{
		"manifestVersion": 5,
		"name":            packageNameFromInstanceName(instance.Name),
		"version":         instance.Version,
		"dshVersion":      instance.Version,
		"hdslShareExport": share,
	}
	if manifest["name"] == "" {
		manifest["name"] = "dsh-instance"
	}
	if mode == "profile" {
		unit, err := s.exportProfileUnit(home, defaultProfile)
		if err != nil {
			return nil, err
		}
		manifest["type"] = "profile"
		manifest["profileName"] = defaultProfile
		for key, value := range unit {
			manifest[key] = value
		}
		if err := validateExportedUnit(unit, "profile", s.limits); err != nil {
			return nil, err
		}
	} else {
		manifest["type"] = "dshhome"
		units := make(map[string]any, len(profiles))
		for _, profile := range profiles {
			unit, err := s.exportProfileUnit(home, profile)
			if err != nil {
				return nil, err
			}
			if err := validateExportedUnit(unit, "profiles."+profile, s.limits); err != nil {
				return nil, err
			}
			units[profile] = unit
		}
		manifest["profiles"] = units
		manifest["defaultProfile"] = defaultProfile
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, internalError("生成整合包清单失败，请稍后重试。")
	}
	return append(data, '\n'), nil
}

// validateExportedUnit runs the consumer-side unit validation on an exported
// manifest unit (fail-closed producer check).
func validateExportedUnit(unit map[string]any, what string, limits safe.Limits) error {
	data, err := json.Marshal(unit)
	if err != nil {
		return internalError("生成整合包清单失败，请稍后重试。")
	}
	object, err := parseJSONObject(data, what)
	if err != nil {
		return err
	}
	return validateManifestUnit(object, what, limits)
}

// exportProfileUnit reads profiles/<profile>/package.json and converts it into
// a manifest unit (bundles + exact dependency versions).
func (s *Service) exportProfileUnit(home, profile string) (map[string]any, error) {
	profileDir := filepath.Join(home, "profiles", profile)
	packageJSONPath := filepath.Join(profileDir, "package.json")
	info, err := os.Lstat(packageJSONPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, validationError("profile " + profile + " 缺少有效 package.json，无法导出。")
	}
	if info.Size() > s.limits.MaxMetadataBytes {
		return nil, validationError("profile " + profile + " 的 package.json 过大。")
	}
	raw, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, internalError("读取 profile package.json 失败，请稍后重试。")
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, validationError("profile " + profile + " 的 package.json 不是合法 JSON。")
	}
	bundles := make([]any, 0)
	if dsh, ok := pkg["dsh"].(map[string]any); ok {
		if profileSection, ok := dsh["profile"].(map[string]any); ok {
			if rawBundles, ok := profileSection["bundles"].([]any); ok {
				bundles = rawBundles
			}
		}
	}
	dependencies := make(map[string]any)
	if rawDependencies, ok := pkg["dependencies"].(map[string]any); ok {
		names := make([]string, 0, len(rawDependencies))
		for name := range rawDependencies {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			spec, _ := rawDependencies[name].(string)
			spec = strings.TrimSpace(spec)
			coordinate := name
			version := spec
			if match := gitExportRE.FindStringSubmatch(spec); match != nil {
				coordinate = "github:" + match[1] + "/" + match[2]
				if match[5] != "" {
					coordinate += "#path:/" + match[5]
				}
				version = match[3]
			} else if !semverExactRE.MatchString(spec) && !strings.Contains(spec, ":") {
				resolved, err := s.installedDependencyVersion(profileDir, name)
				if err != nil {
					return nil, err
				}
				version = resolved
			}
			if strings.HasPrefix(version, "file:") {
				local := strings.TrimPrefix(version, "file:")
				if _, statErr := os.Lstat(filepath.Join(profileDir, filepath.FromSlash(local))); statErr != nil {
					return nil, validationError("本地依赖未包含在可分享文件中：" + name + "（" + profile + "）。")
				}
			}
			dependencies[coordinate] = version
		}
	}
	return map[string]any{
		"bundles":      bundles,
		"dependencies": dependencies,
	}, nil
}

// installedDependencyVersion resolves an exact version of an installed
// dependency from profiles/<profile>/node_modules.
func (s *Service) installedDependencyVersion(profileDir, packageName string) (string, error) {
	installedPath := filepath.Join(profileDir, "node_modules", filepath.FromSlash(packageName), "package.json")
	info, err := os.Lstat(installedPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > s.limits.MaxMetadataBytes {
		return "", validationError("依赖尚未锁定精确版本：" + packageName + "；请先安装依赖。")
	}
	raw, err := os.ReadFile(installedPath)
	if err != nil {
		return "", internalError("读取已安装依赖信息失败，请稍后重试。")
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || !semverExactRE.MatchString(strings.TrimSpace(manifest.Version)) {
		return "", validationError("依赖尚未锁定精确版本：" + packageName + "；请先安装依赖。")
	}
	return strings.TrimSpace(manifest.Version), nil
}

// writeExportZip writes the dspack v3 archive (deterministic timestamps).
func (s *Service) writeExportZip(ctx context.Context, output io.Writer, manifest []byte, files []exportFile, profiles []string, defaultProfile, mode string) error {
	writer := zip.NewWriter(output)
	writeEntry := func(name string, content []byte) error {
		if err := validateRelativePath(name, s.limits); err != nil {
			return err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		header.Modified = time.Unix(0, 0).UTC()
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = entry.Write(content)
		return err
	}
	if err := writeEntry(dspackMarkerFile, []byte("{\"format\":\"dspack\",\"version\":3}")); err != nil {
		return internalError("写入导出文件失败，请稍后重试。")
	}
	if err := writeEntry(manifestFile, manifest); err != nil {
		return internalError("写入导出文件失败，请稍后重试。")
	}
	for _, file := range files {
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = writer.Close()
			return ctxErr
		}
		zipName := ""
		if mode == "home" {
			zipName = "overrides/" + file.relative
		} else if strings.HasPrefix(file.relative, "profiles/"+defaultProfile+"/") {
			short := strings.TrimPrefix(file.relative, "profiles/"+defaultProfile+"/")
			if isMachineFileName(short) {
				zipName = short
			} else {
				zipName = "overrides/" + short
			}
		} else {
			zipName = "home/" + file.relative
		}
		if err := validateRelativePath(zipName, s.limits); err != nil {
			_ = writer.Close()
			return err
		}
		header := &zip.FileHeader{Name: zipName, Method: zip.Deflate}
		header.SetMode(0o600)
		header.Modified = time.Unix(0, 0).UTC()
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return internalError("写入导出文件失败，请稍后重试。")
		}
		source, err := os.Open(file.sourcePath)
		if err != nil {
			_ = writer.Close()
			return internalError("读取实例文件失败，请检查数据目录权限。")
		}
		buffer := make([]byte, uploadBufferBytes)
		for {
			if ctxErr := ctx.Err(); ctxErr != nil {
				_ = source.Close()
				_ = writer.Close()
				return ctxErr
			}
			read, readErr := source.Read(buffer)
			if read > 0 {
				if _, writeErr := entry.Write(buffer[:read]); writeErr != nil {
					_ = source.Close()
					_ = writer.Close()
					return internalError("写入导出文件失败，请稍后重试。")
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = source.Close()
				_ = writer.Close()
				return internalError("读取实例文件失败，请检查数据目录权限。")
			}
		}
		_ = source.Close()
	}
	if err := writer.Close(); err != nil {
		return internalError("写入导出文件失败，请稍后重试。")
	}
	return nil
}

// exportFilename builds a safe download file name.
func (s *Service) exportFilename(instance store.Instance, mode, profile string) string {
	base := packageNameFromInstanceName(instance.Name)
	if base == "" {
		base = "dsh-instance"
	}
	label := profile
	if mode == "home" {
		label = "home"
	}
	label = packageNameFromInstanceName(label)
	if label == "" {
		label = "home"
	}
	name := fmt.Sprintf("%s-%s-%s-%s.dspack.zip",
		base, label, instance.Version, s.now().UTC().Format("20060102-150405"))
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return name
}

// packageNameFromInstanceName converts an instance display name into a
// lower-kebab-case npm package name (reference export name rule).
func packageNameFromInstanceName(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	pendingDash := false
	for _, r := range lowered {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			pendingDash = false
		default:
			if !pendingDash && builder.Len() > 0 {
				builder.WriteByte('-')
				pendingDash = true
			}
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > 60 {
		result = strings.Trim(result[:60], "-")
	}
	return result
}

// asValidationError extracts an *apierr.Error with status 400 from err.
func asValidationError(err error) (*apierr.Error, bool) {
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != 400 {
		return nil, false
	}
	return apiErr, true
}
