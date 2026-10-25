// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/ports"
	profilecheck "github.com/LeuJasYoh/PCL-DSH-Go/internal/profile"
)

const (
	instanceIDPattern = `^i-[0-9a-f]{12}$`
	// 精确 SemVer（含可选 prerelease/build），与 https://semver.org/spec/v2.0.0.html 一致。
	semverPattern  = `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$`
	sshUserPattern = `^[A-Za-z0-9._-]{1,32}$`

	maxNameRunes    = 160
	maxVersionBytes = 128

	defaultRegistry            = "https://registry.npmjs.org"
	defaultDirectAccessSSHUser = "root"
)

var (
	instanceIDRE = regexp.MustCompile(instanceIDPattern)
	semverRE     = regexp.MustCompile(semverPattern)
	sshUserRE    = regexp.MustCompile(sshUserPattern)
)

func defaultSettings() Settings {
	return Settings{
		Proxy:               "",
		Registry:            defaultRegistry,
		DirectAccessSSHUser: defaultDirectAccessSSHUser,
	}
}

// normalizeName 去除首尾空白，校验 1-160 字符、禁止控制字符与路径分隔符。
func normalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: 实例名称不能为空", ErrInvalidInput)
	}
	if n := utf8.RuneCountInString(name); n > maxNameRunes {
		return "", fmt.Errorf("%w: 实例名称不能超过 %d 个字符（当前 %d）", ErrInvalidInput, maxNameRunes, n)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: 实例名称不能包含控制字符", ErrInvalidInput)
		}
		if r == '/' || r == '\\' {
			return "", fmt.Errorf("%w: 实例名称不能包含路径分隔符", ErrInvalidInput)
		}
	}
	return name, nil
}

// normalizeVersion 校验精确 SemVer（本地最小实现，不依赖 internal/runtime，避免循环依赖）。
func normalizeVersion(raw string) (string, error) {
	version := raw
	if version == "" {
		return "", fmt.Errorf("%w: 版本号不能为空，请输入精确 SemVer（例如 0.2.1-rc.3）", ErrInvalidInput)
	}
	if len(version) > maxVersionBytes {
		return "", fmt.Errorf("%w: 版本号过长", ErrInvalidInput)
	}
	if !semverRE.MatchString(version) {
		return "", fmt.Errorf("%w: 版本号 %q 不是精确 SemVer（例如 0.2.1-rc.3）", ErrInvalidInput, version)
	}
	return version, nil
}

// normalizeProfile 校验 profile 名称。
func normalizeProfile(raw string) (string, error) {
	if err := profilecheck.Validate(raw); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return raw, nil
}

func normalizePort(raw int) (int, error) {
	if !ports.ValidInstance(raw) {
		return 0, fmt.Errorf("%w: 端口须在 %d-%d 之间（当前 %d）", ErrInvalidInput, ports.InstanceMin, ports.Max, raw)
	}
	return raw, nil
}

// validateStoredInstance 校验从磁盘读出的实例记录；顺带规范化名称首尾空白。
func validateStoredInstance(raw Instance) (Instance, error) {
	if !instanceIDRE.MatchString(raw.ID) {
		return Instance{}, fmt.Errorf("实例 ID %q 格式无效（应为 i- + 12 位小写十六进制）", raw.ID)
	}
	name, err := normalizeName(raw.Name)
	if err != nil {
		return Instance{}, fmt.Errorf("实例 %s: %w", raw.ID, err)
	}
	version, err := normalizeVersion(raw.Version)
	if err != nil {
		return Instance{}, fmt.Errorf("实例 %s: %w", raw.ID, err)
	}
	profile, err := normalizeProfile(raw.Profile)
	if err != nil {
		return Instance{}, fmt.Errorf("实例 %s: %w", raw.ID, err)
	}
	port, err := normalizePort(raw.Port)
	if err != nil {
		return Instance{}, fmt.Errorf("实例 %s: %w", raw.ID, err)
	}
	if raw.CreatedAt < 0 || raw.UpdatedAt < 0 {
		return Instance{}, fmt.Errorf("实例 %s 的时间戳无效", raw.ID)
	}
	raw.Name = name
	raw.Version = version
	raw.Profile = profile
	raw.Port = port
	return raw, nil
}

// normalizeSettings 校验并补全默认值。
// 空 registry/sshUser 视为恢复默认值，空 proxy 表示不使用代理。
func normalizeSettings(in Settings) (Settings, error) {
	out := defaultSettings()

	registry := strings.TrimSpace(in.Registry)
	if registry == "" {
		registry = defaultRegistry
	}
	if err := validateRegistryURL(registry); err != nil {
		return Settings{}, err
	}
	out.Registry = registry

	proxy := strings.TrimSpace(in.Proxy)
	if proxy != "" {
		if err := validateProxyURL(proxy); err != nil {
			return Settings{}, err
		}
		out.Proxy = proxy
	}

	sshUser := strings.TrimSpace(in.DirectAccessSSHUser)
	if sshUser == "" {
		sshUser = defaultDirectAccessSSHUser
	}
	if !sshUserRE.MatchString(sshUser) {
		return Settings{}, fmt.Errorf("%w: directAccessSshUser %q 只允许 1-32 位字母、数字、点、下划线和连字符", ErrInvalidInput, sshUser)
	}
	out.DirectAccessSSHUser = sshUser
	return out, nil
}

func validateRegistryURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: registry 不是合法 URL: %v", ErrInvalidInput, err)
	}
	invalid := fmt.Errorf("%w: registry 必须是 http(s) URL 且不含用户信息", ErrInvalidInput)
	if u.Opaque != "" || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return invalid
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return invalid
	}
	return nil
}

func validateProxyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: proxy 不是合法 URL: %v", ErrInvalidInput, err)
	}
	invalid := fmt.Errorf("%w: proxy 必须形如 http://host:port 且不含用户信息", ErrInvalidInput)
	if u.Opaque != "" || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return invalid
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return invalid
	}
	if u.Path != "" && u.Path != "/" {
		return invalid
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return invalid
	}
	port := u.Port()
	if port == "" {
		return fmt.Errorf("%w: proxy 缺少端口", ErrInvalidInput)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%w: proxy 端口无效", ErrInvalidInput)
	}
	return nil
}
