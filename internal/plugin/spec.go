// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// maxPluginSpecLength is the npm package-name/spec length limit from
// docs/M3_INTERFACES.md 5.
const maxPluginSpecLength = 214

// npmPackageNamePattern matches (optionally scoped) npm package names. It is
// intentionally case tolerant like the reference implementation.
var npmPackageNamePattern = func() func(string) bool {
	return func(name string) bool {
		if len(name) == 0 || len(name) > maxPluginSpecLength {
			return false
		}
		at := strings.IndexByte(name, '@')
		if at > 0 {
			return false
		}
		rest := name
		if at == 0 {
			slash := strings.IndexByte(name, '/')
			if slash < 2 {
				return false
			}
			scope := name[1:slash]
			if !validNameSegment(scope) {
				return false
			}
			rest = name[slash+1:]
			if rest == "" {
				return false
			}
		}
		return validNameSegment(rest)
	}
}()

// validNameSegment reports whether segment is a legal npm name element.
func validNameSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for index := 0; index < len(segment); index++ {
		character := segment[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '_', character == '-':
		default:
			return false
		}
	}
	first := segment[0]
	return !(first == '.' || first == '_' || first == '-')
}

// validPackageName reports whether name is a legal npm package name.
func validPackageName(name string) bool {
	return npmPackageNamePattern(name)
}

// normalizeAction validates a mutation verb and returns its canonical form.
func normalizeAction(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add", "install":
		return ActionAdd, nil
	case "update":
		return ActionUpdate, nil
	case "remove", "uninstall":
		return ActionRemove, nil
	default:
		return "", apierr.New(http.StatusBadRequest, apierr.CodeValidation, "仅支持安装、更新和移除插件。")
	}
}

// validatePluginSpec rejects command-flag injection, control characters and
// oversized specs (docs/M3_INTERFACES.md 5).
func validatePluginSpec(spec string) error {
	if spec == "" {
		return apierr.New(http.StatusBadRequest, apierr.CodeValidation, "请输入插件包名或地址。")
	}
	if len(spec) > maxPluginSpecLength {
		return apierr.New(http.StatusBadRequest, apierr.CodeValidation, "插件坐标过长（最多 214 字符）。")
	}
	if strings.HasPrefix(spec, "-") {
		return apierr.New(http.StatusBadRequest, apierr.CodeValidation, "插件坐标不能是命令参数。")
	}
	for _, character := range spec {
		if character < 0x20 || character == 0x7f {
			return apierr.New(http.StatusBadRequest, apierr.CodeValidation, "插件坐标不能包含控制字符。")
		}
	}
	return nil
}

// splitLocalSpec separates an optional file:/link: prefix from the path value.
func splitLocalSpec(spec string) (prefix, value string) {
	switch {
	case strings.HasPrefix(spec, "file:"):
		return "file:", spec[len("file:"):]
	case strings.HasPrefix(spec, "link:"):
		return "link:", spec[len("link:"):]
	default:
		return "", spec
	}
}

// isLocalPluginSpec reports whether spec is a file:/link: or relative-path
// dependency that must be anchored to the instance workspace.
func isLocalPluginSpec(spec string) bool {
	_, value := splitLocalSpec(spec)
	return isRelativeLocalPath(value)
}

// isRelativeLocalPath reports whether value is ".", ".." or starts with "./" or
// "../" (both slash styles).
func isRelativeLocalPath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	return value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
}

// windowsDrivePath reports whether value starts with a Windows drive letter and
// separator (checked on every OS so Windows-style absolute paths are rejected
// even when the manager runs on Linux).
func windowsDrivePath(value string) bool {
	if len(value) < 3 {
		return false
	}
	letter := value[0]
	if !((letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')) {
		return false
	}
	if value[1] != ':' {
		return false
	}
	return value[2] == '/' || value[2] == '\\'
}

// ValidateMutationInput is the instance-independent part of plugin mutation
// validation (docs/M3_INTERFACES.md 5, IMPLEMENTATION_SPEC 4.4):
//
//   - action must be add/update/remove;
//   - profile must match the instance-store profile rules;
//   - add accepts a strict npm name/name@version, an npm/GitHub/git/URL
//     coordinate, or a local file:/link:/relative path (no absolute paths,
//     drive letters, UNC paths or ".." escapes);
//   - update takes no spec (in-place update);
//   - remove requires a complete npm package name.
//
// All failures are *apierr.Error with 400 VALIDATION so the API can answer
// synchronously before a Job is started. Mutate calls this function too, then
// adds the instance-dependent checks (existence, running state, removable
// dependency, local target filesystem containment).
func ValidateMutationInput(in MutateInput) error {
	if strings.TrimSpace(in.InstanceID) == "" {
		return validationError("实例 ID 不能为空。")
	}
	if !validProfileName(strings.TrimSpace(in.Profile)) {
		return validationError("profile 参数无效。")
	}
	action, err := normalizeAction(in.Action)
	if err != nil {
		return err
	}
	spec := strings.TrimSpace(in.Spec)
	switch action {
	case ActionUpdate:
		if spec != "" {
			return validationError("更新操作不接受包参数（原地更新）。")
		}
		return nil
	case ActionRemove:
		if err := validatePluginSpec(spec); err != nil {
			return err
		}
		if !validPackageName(spec) {
			return validationError("移除操作需要完整 npm 包名。")
		}
		return nil
	case ActionAdd:
		return validateAddSpec(spec)
	default:
		return validationError("仅支持安装、更新和移除插件。")
	}
}

// validationError builds a 400 VALIDATION *apierr.Error.
func validationError(message string) *apierr.Error {
	return apierr.New(http.StatusBadRequest, apierr.CodeValidation, message)
}

// validateAddSpec validates one add package spec.
func validateAddSpec(spec string) error {
	if err := validatePluginSpec(spec); err != nil {
		return err
	}
	if isLocalPluginSpec(spec) {
		return validateLocalSpecLexical(spec)
	}
	if isCoordinateSpec(spec) {
		return validateCoordinateSpec(spec)
	}
	if isGitHubShorthand(spec) {
		return nil
	}
	return validateNPMRegistrySpec(spec)
}

// validateLocalSpecLexical rejects absolute paths, drive letters, UNC paths and
// lexical ".." escapes. Filesystem containment/existence is checked by Mutate.
func validateLocalSpecLexical(spec string) error {
	_, value := splitLocalSpec(spec)
	if value == "" {
		return validationError("本地插件路径不能为空。")
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return validationError("本地插件路径不能是绝对路径。")
	}
	if windowsDrivePath(normalized) {
		return validationError("本地插件路径不能包含盘符。")
	}
	if strings.HasPrefix(normalized, "//") {
		return validationError("本地插件路径不能是 UNC 路径。")
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) ||
		filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" {
		return validationError("本地插件路径不能超出实例 workspace。")
	}
	return nil
}

// coordinatePrefixes lists the package-manager coordinate schemes accepted by
// add. They are passed to the Harness CLI unchanged (no local anchoring).
var coordinatePrefixes = []string{
	"npm:", "github:", "git:", "git+", "https://", "http://", "workspace:", "portal:",
}

// isCoordinateSpec reports whether spec is a remote package-manager coordinate.
func isCoordinateSpec(spec string) bool {
	lowered := strings.ToLower(spec)
	for _, prefix := range coordinatePrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}

// validateCoordinateSpec applies the common safety rules to remote coordinates.
func validateCoordinateSpec(spec string) error {
	if strings.ContainsAny(spec, " \t\r\n") {
		return validationError("插件坐标不能包含空白字符。")
	}
	return nil
}

// isGitHubShorthand reports whether spec is an owner/repo GitHub shorthand.
func isGitHubShorthand(spec string) bool {
	if strings.Contains(spec, "@") {
		return false
	}
	pieces := strings.Split(spec, "/")
	if len(pieces) != 2 {
		return false
	}
	return validNameSegment(pieces[0]) && validNameSegment(pieces[1])
}

// validateNPMRegistrySpec validates a bare npm package name or name@version
// spec. Scoped names must have exactly one slash (rejects @bad//name,
// @scope/name/extra, ...).
func validateNPMRegistrySpec(spec string) error {
	name, version, ok := splitNPMRegistrySpec(spec)
	if !ok || !validPackageName(name) {
		return validationError("插件包名无效：请使用合法的 npm 包名（可带 @版本）。")
	}
	if version != "" && !validNPMVersionSpec(version) {
		return validationError("插件版本范围无效。")
	}
	return nil
}

// splitNPMRegistrySpec splits "name@version" / "@scope/name@version".
func splitNPMRegistrySpec(spec string) (name, version string, ok bool) {
	if strings.HasPrefix(spec, "@") {
		slash := strings.IndexByte(spec, '/')
		if slash < 2 {
			return "", "", false
		}
		rest := spec[slash+1:]
		at := strings.IndexByte(rest, '@')
		if at < 0 {
			return spec, "", true
		}
		return spec[:slash+1+at], rest[at+1:], true
	}
	at := strings.IndexByte(spec, '@')
	if at < 0 {
		return spec, "", true
	}
	return spec[:at], spec[at+1:], true
}

// validNPMVersionSpec reports whether version is a plausible npm version/range
// token (semver, range operators and dist-tags; no path separators/whitespace).
func validNPMVersionSpec(version string) bool {
	if version == "" {
		return false
	}
	for index := 0; index < len(version); index++ {
		character := version[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '-', character == '+', character == '^',
			character == '~', character == '<', character == '>', character == '=',
			character == '*', character == '|':
		default:
			return false
		}
	}
	return true
}

// anchorLocalSpec turns a local plugin coordinate into an absolute path anchored
// at the instance workspace, mirroring the reference implementation
// (PluginService.anchorLocalSpec). Remote/npm coordinates are returned as-is.
func anchorLocalSpec(spec, workspace string) string {
	prefix, value := splitLocalSpec(spec)
	if isRelativeLocalPath(value) {
		return prefix + filepath.Join(workspace, value)
	}
	if prefix != "" && !filepath.IsAbs(value) {
		return prefix + filepath.Join(workspace, value)
	}
	return spec
}

// validateLocalPluginTarget verifies that a local plugin coordinate resolves to
// an existing path inside the instance workspace, including after symlink
// resolution. link: specs therefore need an existing target as well.
func (s *Service) validateLocalPluginTarget(instanceID, spec string) error {
	_, value := splitLocalSpec(spec)
	workspace := s.workspaceDirectory(instanceID)
	candidate := filepath.Clean(filepath.Join(workspace, filepath.FromSlash(value)))
	if !pathInside(workspace, candidate) {
		return validationError("本地插件路径不能超出实例 workspace。")
	}
	if _, err := os.Lstat(candidate); err != nil {
		return validationError("本地插件路径不存在；file:/link: 必须指向实例 workspace 内已存在的路径。")
	}
	workspaceResolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return validationError("实例 workspace 不存在或无法解析。")
	}
	targetResolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return validationError("无法解析本地插件路径。")
	}
	if !pathInside(workspaceResolved, targetResolved) {
		return validationError("本地插件路径不能通过符号链接超出实例 workspace。")
	}
	return nil
}

// pathInside reports whether target is base or inside base.
func pathInside(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
