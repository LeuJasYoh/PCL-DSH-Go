// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const (
	// maxRuntimeManifestBytes bounds runtime package.json reads.
	maxRuntimeManifestBytes = 8 << 20
	// installTimeout bounds one pnpm install (docs/DSH_INTEGRATION.md 3.2).
	installTimeout = 20 * time.Minute
	// installHomeDirectoryName is the throw-away HOME inside the staging dir.
	installHomeDirectoryName = ".hdsl-install-home"
)

// InstallResult is the outcome of one runtime installation
// (docs/M0_INTERFACES.md section 7).
type InstallResult struct {
	// Version is the exact installed SemVer version.
	Version string
	// EntryPath is the absolute path of the validated dsh CLI entry
	// (node_modules/@deepseek-ai/dsh/<bin> inside the runtime directory).
	EntryPath string
	// Reused reports that an already installed runtime was returned as-is.
	Reused bool
}

// Service implements DSH runtime discovery, installation, capability probing and
// launch planning. It is safe for concurrent use.
type Service struct {
	logger   *slog.Logger
	tools    *tools.Tools
	dataRoot string

	// installMu serialises installs: pnpm must not run concurrently in the
	// same data root, and a second caller should observe the first result.
	installMu sync.Mutex

	mu           sync.Mutex
	proxy        string
	capabilities map[string]capabilityCacheEntry
}

// capabilityCacheEntry is one cached probe result keyed by version+profile.
type capabilityCacheEntry struct {
	fingerprint  string
	capabilities Capabilities
}

// New creates a runtime service. logger may be nil (slog.Default is used);
// dataRoot is normalised to an absolute path when possible.
func New(logger *slog.Logger, tl *tools.Tools, dataRoot string) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	root := strings.TrimSpace(dataRoot)
	if root != "" {
		if absolute, err := filepath.Abs(root); err == nil {
			root = absolute
		}
	}
	return &Service{
		logger:       logger,
		tools:        tl,
		dataRoot:     root,
		capabilities: make(map[string]capabilityCacheEntry),
	}
}

// SetProxy configures the outbound proxy used for registry metadata queries and
// pnpm installs. The settings layer should call this once its proxy is known.
func (s *Service) SetProxy(proxy string) {
	s.mu.Lock()
	s.proxy = strings.TrimSpace(proxy)
	s.mu.Unlock()
}

func (s *Service) currentProxy() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proxy
}

// AvailableVersions returns the valid SemVer versions of @deepseek-ai/dsh in
// descending precedence order (docs/DSH_INTEGRATION.md 2).
func (s *Service) AvailableVersions(ctx context.Context, registry string) ([]string, error) {
	return fetchAvailableVersions(ctx, newRegistryHTTPClient(s.currentProxy()), registry)
}

// Installed reports whether version is installed and its CLI entry validates.
func (s *Service) Installed(version string) bool {
	_, err := s.executable(version)
	return err == nil
}

// InstalledVersions lists installed, entry-validated runtimes in descending
// SemVer order.
func (s *Service) InstalledVersions() []string {
	if s.dataRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(s.dataRoot, "runtimes"))
	if err != nil {
		return nil
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !IsVersion(entry.Name()) {
			continue
		}
		if _, err := s.executable(entry.Name()); err != nil {
			continue
		}
		versions = append(versions, entry.Name())
	}
	return SortVersionsDescending(versions)
}

// Compatibility returns the M0 compatibility label for a runtime version:
// "verified" after a successful web-profile probe, "unverified" otherwise.
func (s *Service) Compatibility(version string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.capabilities[version+"\x00web"]; ok && entry.capabilities.Supports("--port") {
		return m0CompatibilityLabel
	}
	return "unverified"
}

// runtimeDirectory returns <dataRoot>/runtimes/<version> after validating that
// version is a safe exact SemVer.
func (s *Service) runtimeDirectory(version string) (string, error) {
	if !IsVersion(version) || !versionPathPattern.MatchString(version) {
		return "", fmt.Errorf("请选择完整的 Harness 版本号（例如 0.2.1-rc.3）")
	}
	if s.dataRoot == "" {
		return "", fmt.Errorf("运行时服务未配置数据目录")
	}
	return filepath.Join(s.dataRoot, "runtimes", version), nil
}

// executable resolves and validates the dsh CLI entry of an installed runtime.
func (s *Service) executable(version string) (string, error) {
	directory, err := s.runtimeDirectory(version)
	if err != nil {
		return "", err
	}
	return validateExecutable(directory, version)
}

// runtimeManifest is the subset of the runtime package.json consumed here.
type runtimeManifest struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Bin     json.RawMessage `json:"bin"`
}

// validateExecutable verifies that runtimeDirectory contains a valid
// @deepseek-ai/dsh package and returns the absolute path of its CLI entry.
//
// The entry must be declared by package.json as a string bin or bin.dsh, stay
// inside the package directory (lexically and after symlink resolution) and be
// an existing regular file (docs/DSH_INTEGRATION.md 3.2).
func validateExecutable(runtimeDirectory, version string) (string, error) {
	packageDirectory := filepath.Join(runtimeDirectory, "node_modules", "@deepseek-ai", "dsh")
	manifestPath := filepath.Join(packageDirectory, "package.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("Harness %s 尚未安装", version)
		}
		return "", fmt.Errorf("读取运行时 manifest 失败：%w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("运行时 manifest 不是普通文件：%s", manifestPath)
	}
	if info.Size() > maxRuntimeManifestBytes {
		return "", fmt.Errorf("运行时 manifest 过大：%s", manifestPath)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("读取运行时 manifest 失败：%w", err)
	}
	var manifest runtimeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("运行时 manifest 格式无效：%w", err)
	}
	if manifest.Name != DSHPackage {
		return "", fmt.Errorf("运行时 package.json 的包名是 %q，期望 %q", manifest.Name, DSHPackage)
	}
	if manifest.Version != version {
		return "", fmt.Errorf("运行时 package.json 的版本是 %q，与要求的 %q 不符", manifest.Version, version)
	}
	relative, err := manifestBinPath(manifest.Bin)
	if err != nil {
		return "", err
	}
	if relative == "" {
		return "", fmt.Errorf("此版本未声明 dsh CLI 入口（package.json 的 bin 或 bin.dsh）")
	}
	cleaned := filepath.Clean(relative)
	// Reject absolute paths, volume names, parent escapes and root-relative
	// paths on every OS (npm bin values are package-relative POSIX paths).
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" ||
		cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) ||
		strings.HasPrefix(cleaned, string(os.PathSeparator)) ||
		strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("dsh CLI 入口 %q 无效：必须位于包目录内", relative)
	}
	entry := filepath.Join(packageDirectory, cleaned)
	entryInfo, err := os.Stat(entry)
	if err != nil || !entryInfo.Mode().IsRegular() {
		return "", fmt.Errorf("dsh CLI 入口不存在或不是普通文件：%s", entry)
	}
	resolvedPackage, err := filepath.EvalSymlinks(packageDirectory)
	if err != nil {
		return "", fmt.Errorf("无法解析运行时包目录：%w", err)
	}
	resolvedEntry, err := filepath.EvalSymlinks(entry)
	if err != nil {
		return "", fmt.Errorf("无法解析 dsh CLI 入口：%w", err)
	}
	if !pathWithin(resolvedPackage, resolvedEntry) {
		return "", fmt.Errorf("dsh CLI 入口超出包目录（realpath 校验失败）：%s", entry)
	}
	absolute, err := filepath.Abs(entry)
	if err != nil {
		return "", fmt.Errorf("解析 dsh CLI 入口绝对路径失败：%w", err)
	}
	return absolute, nil
}

// manifestBinPath extracts the CLI entry from the npm bin field, which may be a
// string or an object with a "dsh" key.
func manifestBinPath(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return "", nil
	case strings.HasPrefix(trimmed, `"`):
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("运行时 package.json 的 bin 字段无效：%w", err)
		}
		return value, nil
	case strings.HasPrefix(trimmed, "{"):
		var values map[string]string
		if err := json.Unmarshal(raw, &values); err != nil {
			return "", fmt.Errorf("运行时 package.json 的 bin 字段无效：%w", err)
		}
		return values["dsh"], nil
	default:
		return "", fmt.Errorf("运行时 package.json 的 bin 字段类型无效")
	}
}

// pathWithin reports whether target is base or inside base (cleaned paths).
func pathWithin(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

// Probe inspects an installed runtime and returns its command-line
// capabilities. Results are cached per version+profile until the entry file or
// config/compatibility.json changes. Probe failures are fail-closed.
func (s *Service) Probe(ctx context.Context, version, profile string) (Capabilities, error) {
	entry, err := s.executable(version)
	if err != nil {
		return Capabilities{}, fmt.Errorf("Harness %s 不可用：%w", version, err)
	}
	if profile == "" {
		profile = "web"
	}
	if !validProfileName(profile) {
		return Capabilities{}, fmt.Errorf("profile 名称无效：%q", profile)
	}
	fingerprint, err := s.probeFingerprint(entry)
	if err != nil {
		return Capabilities{}, err
	}
	cacheKey := version + "\x00" + profile
	s.mu.Lock()
	cached, cachedOK := s.capabilities[cacheKey]
	s.mu.Unlock()
	if cachedOK && cached.fingerprint == fingerprint {
		return cached.capabilities, nil
	}
	if s.tools == nil || s.tools.NodePath == "" {
		return Capabilities{}, fmt.Errorf("探测失败：未定位到 Node.js 工具链")
	}
	capabilities, err := probeCapabilities(ctx, probeOptions{
		NodePath:  s.tools.NodePath,
		EntryPath: entry,
		DataRoot:  s.dataRoot,
		Version:   version,
		Profile:   profile,
		Logf:      s.probeLogf(version),
	})
	if err != nil {
		return Capabilities{}, err
	}
	s.mu.Lock()
	if s.capabilities == nil {
		s.capabilities = make(map[string]capabilityCacheEntry)
	}
	s.capabilities[cacheKey] = capabilityCacheEntry{fingerprint: fingerprint, capabilities: capabilities}
	s.mu.Unlock()
	return capabilities, nil
}

// probeFingerprint combines the CLI entry timestamp/size with the manual
// capability declaration timestamp, matching the reference implementation.
func (s *Service) probeFingerprint(entry string) (string, error) {
	info, err := os.Stat(entry)
	if err != nil {
		return "", fmt.Errorf("读取运行时入口信息失败：%w", err)
	}
	compatibilityStamp := "none"
	compatibilityPath := filepath.Join(s.dataRoot, "config", "compatibility.json")
	if compatibilityInfo, statErr := os.Stat(compatibilityPath); statErr == nil {
		compatibilityStamp = fmt.Sprintf("%d:%d", compatibilityInfo.ModTime().UnixNano(), compatibilityInfo.Size())
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("读取 config/compatibility.json 信息失败：%w", statErr)
	}
	return fmt.Sprintf("%d:%d:%s", info.ModTime().UnixNano(), info.Size(), compatibilityStamp), nil
}

// probeLogf forwards probe diagnostics into the manager logger.
func (s *Service) probeLogf(version string) func(string) {
	return func(line string) {
		s.logger.Info(line, "component", "runtime-probe", "version", version)
	}
}

// invalidateCapabilities drops cached probe results of one version.
func (s *Service) invalidateCapabilities(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := version + "\x00"
	for key := range s.capabilities {
		if strings.HasPrefix(key, prefix) {
			delete(s.capabilities, key)
		}
	}
}
