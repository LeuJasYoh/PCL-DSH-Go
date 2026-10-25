// SPDX-License-Identifier: GPL-3.0-or-later

package pack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	profilecheck "github.com/LeuJasYoh/PCL-DSH-Go/internal/profile"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/safe"
)

var (
	// semverExactRE matches an exact SemVer version (no ranges).
	semverExactRE = regexp.MustCompile("^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\\+([0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*))?$")
	// npmPackageRE matches an npm package name.
	npmPackageRE = regexp.MustCompile("^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$")
	// gitCoordRE matches the github:owner/repo#path:/package coordinate form.
	gitCoordRE = regexp.MustCompile("^github:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(#path:/([A-Za-z0-9_./-]+))?$")
	// windowsReservedRE matches Windows reserved device names (optionally with extension).
	windowsReservedRE = regexp.MustCompile("(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\\..*)?$")
	// windowsDriveRE matches a Windows drive prefix.
	windowsDriveRE = regexp.MustCompile("^[A-Za-z]:")
	// sha256RE matches a hex SHA-256 digest.
	sha256RE = regexp.MustCompile("^[A-Fa-f0-9]{64}$")
	// embeddedCredentialRE detects credentials embedded in dependency URLs.
	embeddedCredentialRE = regexp.MustCompile("(?i).*(?:https?|git\\+https?)://[^/]+@.*")
)

// machineFileNames are profile "machine" files that may not be resource targets.
var machineFileNames = map[string]struct{}{
	"package.json":        {},
	"pnpm-lock.yaml":      {},
	"pnpm-workspace.yaml": {},
}

// excludedParts is the reference implementation's credential/cache/session
// blacklist (PackService.excluded); paths are compared lower-case per segment.
var excludedParts = map[string]struct{}{
	".git": {}, ".ssh": {}, ".aws": {}, ".azure": {}, ".gnupg": {}, ".agents": {},
	".credentials.yaml": {}, ".credentials.yml": {}, "credentials": {}, "credentials.json": {},
	"auth.json": {}, "auth.yaml": {}, "hosts.yml": {}, ".npmrc": {}, ".netrc": {}, ".pypirc": {},
	"sessions": {}, "dsh-session-archive": {}, "tokens.json": {}, "token.json": {},
	"oauth.json": {}, "oauth_credentials.json": {}, "secrets": {}, "secrets.json": {},
	"secrets.yaml": {}, "secrets.yml": {}, ".envrc": {}, "attachments": {}, "storages": {},
	"dsh-usage": {}, "task-board": {}, ".anonymous-user-id": {}, ".cache": {}, "cache": {},
	"logs": {}, ".dsh-module-fallback": {}, ".skill-staging": {}, ".preset-staging": {},
	".pack-offline-import": {}, ".dshpkcfg": {}, ".dsh-pack": {}, "settings.yaml": {},
	"settings.yml": {}, "settings.json": {}, ".hdsl-pack-origin.json": {},
}

// excludedPath reports whether a relative path must never be exported (or, when
// snapshot, treated as private snapshot content).
func excludedPath(rel string, exporting, snapshot bool) bool {
	lowered := strings.ToLower(rel)
	parts := strings.Split(lowered, "/")
	for _, part := range parts {
		if _, ok := excludedParts[part]; ok {
			return true
		}
		if part == ".env" || strings.HasPrefix(part, ".env.") ||
			strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") ||
			strings.HasSuffix(part, ".p12") || strings.HasSuffix(part, ".pfx") ||
			strings.HasSuffix(part, ".log") ||
			strings.HasPrefix(part, "id_rsa") || strings.HasPrefix(part, "id_ed25519") {
			return true
		}
		if !snapshot && part == "node_modules" {
			return true
		}
		if exporting && (part == "cordis.patch.yml" || part == "cordis.patch.yaml") {
			return true
		}
	}
	return lowered == "skills/.system" || strings.HasPrefix(lowered, "skills/.system/")
}

// --- JSON helpers ---------------------------------------------------------

// jsonObject is a decoded JSON object with raw members.
type jsonObject map[string]json.RawMessage

func parseJSONObject(raw []byte, what string) (jsonObject, error) {
	var object jsonObject
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, validationError(what + " 必须是合法 JSON 对象。")
	}
	return object, nil
}

func (o jsonObject) has(name string) bool {
	raw, ok := o[name]
	if !ok {
		return false
	}
	return strings.TrimSpace(string(raw)) != "null"
}

func (o jsonObject) requiredString(name, what string) (string, error) {
	raw, ok := o[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return "", validationError(what + " 缺少字符串字段 " + name + "。")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", validationError(what + " 的字段 " + name + " 必须为字符串。")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", validationError(what + " 的字段 " + name + " 不能为空。")
	}
	return value, nil
}

func (o jsonObject) optionalString(name, what string) (string, bool, error) {
	raw, ok := o[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, validationError(what + " 的字段 " + name + " 必须为字符串。")
	}
	return strings.TrimSpace(value), true, nil
}

func (o jsonObject) integer(name, what string) (int64, bool, error) {
	raw, ok := o[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return 0, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, false, validationError(what + " 的字段 " + name + " 必须为整数。")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, false, validationError(what + " 的字段 " + name + " 必须为整数。")
	}
	integer, err := number.Int64()
	if err != nil {
		return 0, false, validationError(what + " 的字段 " + name + " 必须为整数。")
	}
	return integer, true, nil
}

func (o jsonObject) object(name, what string) (jsonObject, bool, error) {
	raw, ok := o[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil, false, nil
	}
	var nested jsonObject
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return nil, false, validationError(what + " 的字段 " + name + " 必须为对象。")
	}
	return nested, true, nil
}

func (o jsonObject) array(name, what string) ([]json.RawMessage, bool, error) {
	raw, ok := o[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil, false, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil || list == nil {
		return nil, false, validationError(what + " 的字段 " + name + " 必须为数组。")
	}
	return list, true, nil
}

func rawString(raw json.RawMessage, what string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", validationError(what + " 必须为字符串。")
	}
	return value, nil
}

// objectFromRaw decodes a raw member that must be a JSON object.
func objectFromRaw(raw json.RawMessage, what string) (jsonObject, error) {
	object, err := parseJSONObject(raw, what)
	if err != nil {
		return nil, err
	}
	return object, nil
}

// --- path validation ------------------------------------------------------

// validateRelativePath applies the archive path portability rules to a path
// declared by a manifest (bundle/resource/dependency/vendored destination),
// since internal/safe only validates archive entry names.
func validateRelativePath(rel string, limits safe.Limits) error {
	if rel == "" {
		return validationError("路径不能为空。")
	}
	if len(rel) > limits.MaxPathLength {
		return validationError(fmt.Sprintf("路径长度超过 %d：%s", limits.MaxPathLength, rel))
	}
	if strings.Contains(rel, "\\") {
		return validationError("路径不能包含反斜杠：" + rel)
	}
	if strings.HasPrefix(rel, "/") {
		return validationError("路径必须是相对路径：" + rel)
	}
	if windowsDriveRE.MatchString(rel) {
		return validationError("路径不能包含盘符：" + rel)
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" {
			return validationError("路径包含空段：" + rel)
		}
		if segment == "." || segment == ".." {
			return validationError("路径不能包含 . 或 .. 段：" + rel)
		}
		if strings.TrimSpace(segment) == "" {
			return validationError("路径包含空白段：" + rel)
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return validationError("路径段不能以点或空格结尾：" + rel)
		}
		if windowsReservedRE.MatchString(segment) {
			return validationError("路径包含 Windows 保留名称：" + rel)
		}
		for _, r := range segment {
			if unicode.IsControl(r) {
				return validationError("路径包含控制字符：" + rel)
			}
			switch r {
			case '<', '>', ':', '"', '|', '?', '*':
				return validationError("路径包含不可移植字符：" + rel)
			}
		}
	}
	return nil
}

// validateProfileName validates a profile name used in package manifests.
func validateProfileName(name string) error {
	if err := profilecheck.Validate(name); err != nil {
		return validationError("非法 profile 名称：" + err.Error())
	}
	return nil
}

// --- manifest unit validation --------------------------------------------

// validateManifestUnit validates a dspack profile unit (bundles + dependencies
// + optional patch) from a manifest object.
func validateManifestUnit(unit jsonObject, what string, limits safe.Limits) error {
	bundlesRaw, ok := unit[bundlesField]
	if !ok || strings.TrimSpace(string(bundlesRaw)) == "null" {
		return validationError(what + " 需要 bundles 数组与 dependencies 对象。")
	}
	var bundles []json.RawMessage
	if err := json.Unmarshal(bundlesRaw, &bundles); err != nil || bundles == nil {
		return validationError(what + " 需要 bundles 数组与 dependencies 对象。")
	}
	for _, item := range bundles {
		value, err := rawString(item, what+" 的 bundles 项")
		if err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return validationError(what + " 的 bundles 必须为非空字符串列表。")
		}
		candidate := strings.TrimPrefix(value, "./")
		if err := validateRelativePath(candidate, limits); err != nil {
			return err
		}
	}

	depsRaw, ok := unit[dependenciesField]
	if !ok || strings.TrimSpace(string(depsRaw)) == "null" {
		return validationError(what + " 需要 bundles 数组与 dependencies 对象。")
	}
	var dependencies map[string]json.RawMessage
	if err := json.Unmarshal(depsRaw, &dependencies); err != nil || dependencies == nil {
		return validationError(what + " 需要 bundles 数组与 dependencies 对象。")
	}
	coordinates := make([]string, 0, len(dependencies))
	for coordinate := range dependencies {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)
	packageNames := make(map[string]string, len(dependencies))
	for _, coordinate := range coordinates {
		spec, err := rawString(dependencies[coordinate], what+" 的依赖版本")
		if err != nil {
			return err
		}
		spec = strings.TrimSpace(spec)
		if spec == "" {
			return validationError(what + " 的依赖版本必须是非空字符串。")
		}
		packageName, err := dependencyPackageName(coordinate)
		if err != nil {
			return err
		}
		key := strings.ToLower(packageName)
		if previous, duplicate := packageNames[key]; duplicate {
			return validationError("多个依赖坐标映射到同一个 npm 包：" + packageName + "（" + previous + " 与 " + coordinate + "）。")
		}
		packageNames[key] = coordinate
		if err := validateDependencySpec(coordinate, spec, limits); err != nil {
			return err
		}
	}

	if patchRaw, ok := unit[patchField]; ok && strings.TrimSpace(string(patchRaw)) != "null" {
		var patch string
		if err := json.Unmarshal(patchRaw, &patch); err != nil {
			return validationError(what + " 的 patch 必须为字符串。")
		}
	}
	return nil
}

const (
	bundlesField      = "bundles"
	dependenciesField = "dependencies"
	patchField        = "patch"
)

// dependencyPackageName resolves a dependency coordinate to its npm package name.
func dependencyPackageName(coordinate string) (string, error) {
	if match := gitCoordRE.FindStringSubmatch(coordinate); match != nil {
		name := match[2]
		if match[4] != "" {
			name = match[4]
		}
		if !npmPackageRE.MatchString(name) {
			return "", validationError("不支持该 git 子包名：" + name)
		}
		return name, nil
	}
	if !npmPackageRE.MatchString(coordinate) {
		return "", validationError("无法解析依赖坐标：" + coordinate)
	}
	return coordinate, nil
}

// validateDependencySpec validates one dependency version/coordinate value.
func validateDependencySpec(coordinate, spec string, limits safe.Limits) error {
	if strings.HasPrefix(spec, "vendor:") || strings.HasPrefix(spec, "link:") || strings.HasPrefix(spec, "workspace:") {
		prefix := strings.SplitN(spec, ":", 2)[0]
		return validationError("尚不支持依赖方言 " + prefix + ":，请导出标准 vendored tarball（" + coordinate + "）。")
	}
	if strings.ContainsAny(spec, "\n\r\x00") {
		return validationError("依赖 " + coordinate + " 的声明包含非法控制字符。")
	}
	if embeddedCredentialRE.MatchString(spec) {
		return validationError("依赖 " + coordinate + " 的声明内嵌凭据。")
	}
	switch {
	case strings.HasPrefix(spec, "file:"):
		return validateRelativePath(spec[len("file:"):], limits)
	case strings.HasPrefix(spec, "/"), strings.HasPrefix(spec, "."), strings.Contains(spec, "\\"), windowsDriveRE.MatchString(spec):
		return validationError("依赖 " + coordinate + " 不能引用源机器的路径。")
	default:
		return nil
	}
}

// validateVersions validates the optional dshVersions enum and dshVersion.
func validateVersions(dshVersion string, hasDSHVersion bool, manifest jsonObject) error {
	raw, ok := manifest["dshVersions"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil || len(entries) == 0 {
		return validationError("dshVersions 必须为非空版本枚举。")
	}
	declared := make(map[string]struct{}, len(entries))
	for _, item := range entries {
		version, err := rawString(item, "dshVersions 的版本")
		if err != nil {
			return err
		}
		version = strings.TrimSpace(version)
		if !semverExactRE.MatchString(version) {
			return validationError("dshVersions 含非法版本：" + version)
		}
		if _, duplicate := declared[version]; duplicate {
			return validationError("dshVersions 含重复版本：" + version)
		}
		declared[version] = struct{}{}
	}
	if hasDSHVersion {
		if _, ok := declared[dshVersion]; !ok {
			return validationError("dshVersion 不在 dshVersions 声明中。")
		}
	}
	return nil
}

// validateDSHVersion validates an optional exact dshVersion string.
func validateDSHVersion(version string, present bool) error {
	if !present || version == "" {
		return nil
	}
	if !semverExactRE.MatchString(version) {
		return validationError("dshVersion 必须为精确版本：" + version)
	}
	return nil
}

// --- vendored / resources ------------------------------------------------

// vendoredEntry is one vendored tarball declaration.
type vendoredEntry struct {
	Path    string
	Version string
	SHA256  string
	Size    int64
}

// validateVendoredShape validates manifest.vendored against the archive entries
// and the dependency coordinates. Tarball contents are verified at import time.
func validateVendoredShape(manifest jsonObject, files map[string]safe.EntryInfo, units []jsonObject, limits safe.Limits, lockNames map[string]struct{}) error {
	raw, ok := manifest["vendored"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	vendors, err := objectFromRaw(raw, "manifest.vendored")
	if err != nil {
		return validationError("vendored 必须为对象。")
	}
	if _, exists := files["vendor/vendor.json"]; exists {
		return validationError("vendor/vendor.json 方言尚未支持，请使用标准 vendored 对象。")
	}
	allowed := make(map[string]struct{})
	for _, unit := range units {
		depsRaw, ok := unit[dependenciesField]
		if !ok {
			continue
		}
		var dependencies map[string]json.RawMessage
		if err := json.Unmarshal(depsRaw, &dependencies); err != nil {
			continue
		}
		for coordinate := range dependencies {
			allowed[coordinate] = struct{}{}
		}
	}
	for name := range lockNames {
		allowed[name] = struct{}{}
	}

	coordinates := make([]string, 0, len(vendors))
	for coordinate := range vendors {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)
	registered := make(map[string]struct{}, len(vendors))
	for _, coordinate := range coordinates {
		if _, ok := allowed[coordinate]; !ok {
			return validationError("vendored 坐标不在依赖或锁文件中：" + coordinate)
		}
		entry, err := objectFromRaw(vendors[coordinate], "vendored."+coordinate)
		if err != nil {
			return err
		}
		vendoredPath, err := entry.requiredString("path", "vendored."+coordinate)
		if err != nil {
			return err
		}
		if err := validateRelativePath(vendoredPath, limits); err != nil {
			return err
		}
		if !strings.HasPrefix(vendoredPath, "vendor/") || !strings.HasSuffix(vendoredPath, ".tgz") {
			return validationError("vendored.path 必须为 vendor/*.tgz：" + vendoredPath)
		}
		if _, duplicate := registered[vendoredPath]; duplicate {
			return validationError("vendored tarball 路径重复：" + vendoredPath)
		}
		registered[vendoredPath] = struct{}{}
		if _, err := entry.requiredString("version", "vendored."+coordinate); err != nil {
			return err
		}
		sha, err := entry.requiredString("sha256", "vendored."+coordinate)
		if err != nil {
			return err
		}
		if !sha256RE.MatchString(sha) {
			return validationError("vendored SHA-256 无效：" + coordinate)
		}
		size, ok, err := entry.integer("size", "vendored."+coordinate)
		if err != nil {
			return err
		}
		if !ok || size < 0 || size > limits.MaxFileBytes {
			return validationError("vendored 大小无效：" + coordinate)
		}
		fileInfo, exists := files[vendoredPath]
		if !exists {
			return validationError("vendored 文件缺失：" + vendoredPath)
		}
		if fileInfo.UncompressedSize != size {
			return validationError(fmt.Sprintf("vendored 大小不符：%s（声明 %d，实际 %d）", vendoredPath, size, fileInfo.UncompressedSize))
		}
	}
	for name := range files {
		if !strings.HasPrefix(name, "vendor/") {
			continue
		}
		if _, ok := registered[name]; !ok {
			return validationError("未登记的 vendor 文件：" + name)
		}
	}
	return nil
}

// resourceSpec is one manifest-declared downloadable resource.
type resourceSpec struct {
	skill  bool
	object jsonObject
}

// validateResourcesShape validates manifest.files/skills structure.
func validateResourcesShape(manifest jsonObject, limits safe.Limits) ([]resourceSpec, error) {
	var resources []resourceSpec
	for _, field := range []string{"files", "skills"} {
		raw, ok := manifest[field]
		if !ok || strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || items == nil {
			return nil, validationError(field + " 必须为数组。")
		}
		for _, item := range items {
			object, err := objectFromRaw(item, field+" 项")
			if err != nil {
				return nil, err
			}
			if field == "skills" && !object.has("urls") && !object.has("sha256") {
				continue
			}
			resources = append(resources, resourceSpec{skill: field == "skills", object: object})
		}
	}
	seen := make(map[string]struct{}, len(resources))
	var total int64
	for index, resource := range resources {
		what := fmt.Sprintf("资源 #%d", index+1)
		resourcePath, err := resource.object.requiredString("path", what)
		if err != nil {
			return nil, err
		}
		if err := validateRelativePath(resourcePath, limits); err != nil {
			return nil, err
		}
		if excludedPath(resourcePath, false, false) {
			return nil, validationError("资源落点敏感：" + resourcePath)
		}
		if _, machine := machineFileNames[path.Base(resourcePath)]; machine {
			return nil, validationError("资源不能覆盖机器文件：" + resourcePath)
		}
		key := strings.ToLower(resourcePath)
		if _, duplicate := seen[key]; duplicate {
			return nil, validationError("资源落点重复：" + resourcePath)
		}
		seen[key] = struct{}{}
		sha, err := resource.object.requiredString("sha256", what)
		if err != nil {
			return nil, err
		}
		if !sha256RE.MatchString(sha) {
			return nil, validationError("资源需要合法 sha256：" + resourcePath)
		}
		size, ok, err := resource.object.integer("size", what)
		if err != nil {
			return nil, err
		}
		if !ok || size < 0 || size > limits.MaxFileBytes {
			return nil, validationError("资源 size 无效：" + resourcePath)
		}
		total += size
		if total > limits.MaxUnpackedBytes {
			return nil, validationError("外部资源总大小超过上限。")
		}
		urls, ok, err := resource.object.array("urls", what)
		if err != nil {
			return nil, err
		}
		if !ok || len(urls) == 0 {
			return nil, validationError("资源 urls 不能为空：" + resourcePath)
		}
		for _, rawURL := range urls {
			text, err := rawString(rawURL, "资源 url")
			if err != nil {
				return nil, err
			}
			if err := validateResourceURL(text); err != nil {
				return nil, err
			}
		}
	}
	return resources, nil
}

// validateResourceURL accepts only credential-free HTTPS URLs.
func validateResourceURL(text string) error {
	parsed, err := url.Parse(strings.TrimSpace(text))
	if err != nil {
		return validationError("资源 url 非法：" + text)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return validationError("资源下载仅支持不含凭据的 HTTPS 地址：" + text)
	}
	return nil
}
