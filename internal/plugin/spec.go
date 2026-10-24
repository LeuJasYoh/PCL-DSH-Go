// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"net/http"
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

// isLocalPluginSpec reports whether spec is a file:/link: or relative-path
// dependency that must be anchored to the instance workspace.
func isLocalPluginSpec(spec string) bool {
	prefix := ""
	switch {
	case strings.HasPrefix(spec, "file:"):
		prefix = "file:"
	case strings.HasPrefix(spec, "link:"):
		prefix = "link:"
	}
	value := strings.TrimPrefix(spec, prefix)
	return isRelativeLocalPath(value)
}

// isRelativeLocalPath reports whether value is ".", ".." or starts with "./" or
// "../" (both slash styles).
func isRelativeLocalPath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	return value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
}

// anchorLocalSpec turns a local plugin coordinate into an absolute path anchored
// at the instance workspace, mirroring the reference implementation
// (PluginService.anchorLocalSpec). Remote/npm coordinates are returned as-is.
func anchorLocalSpec(spec, workspace string) string {
	prefix := ""
	switch {
	case strings.HasPrefix(spec, "file:"):
		prefix = "file:"
	case strings.HasPrefix(spec, "link:"):
		prefix = "link:"
	}
	value := strings.TrimPrefix(spec, prefix)
	if isRelativeLocalPath(value) {
		return prefix + filepath.Join(workspace, value)
	}
	if prefix != "" && !filepath.IsAbs(value) {
		return prefix + filepath.Join(workspace, value)
	}
	return spec
}
