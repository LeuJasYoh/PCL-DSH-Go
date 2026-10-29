// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// m0CompatibilityLabel is the M0 compatibility string reported for installed
// runtimes until the M1 compatibility matrix lands.
const m0CompatibilityLabel = "verified"

// pnpmWorkspaceYAML is the base block written verbatim into the staging
// directory (docs/DSH_INTEGRATION.md 3.1); it must not be reformatted.
// Release-cohort overrides are appended after it by
// pnpmWorkspaceYAMLWithOverrides.
const pnpmWorkspaceYAML = `autoInstallPeers: true
nodeLinker: hoisted
resolutionMode: time-based
strictPeerDependencies: false
dangerouslyAllowAllBuilds: true
`

// pnpmWorkspaceYAMLWithOverrides renders the staging pnpm-workspace.yaml.
//
// pnpm 11 no longer reads package.json "pnpm.overrides"; its settings moved to
// pnpm-workspace.yaml. The cohort is therefore written to both places: the
// workspace file is authoritative for pnpm 11, the package.json copy keeps
// older pnpm versions working.
//
// Package names and versions are YAML single-quoted (sorted by name, which is
// deterministic), so arbitrary npm scoped names cannot break the document.
func pnpmWorkspaceYAMLWithOverrides(overrides map[string]string) ([]byte, error) {
	var builder strings.Builder
	builder.WriteString(pnpmWorkspaceYAML)
	if len(overrides) == 0 {
		return []byte(builder.String()), nil
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	builder.WriteString("overrides:\n")
	for _, name := range names {
		version := overrides[name]
		if !validPackageName(name) {
			return nil, fmt.Errorf("发行批次包含无效包名 %q", name)
		}
		if !IsVersion(version) || !versionPathPattern.MatchString(version) {
			return nil, fmt.Errorf("发行批次包含无效版本 %q（包 %s）", version, name)
		}
		// Single quotes only need doubling for embedded quotes; npm names and
		// SemVer versions cannot contain a single quote, but escape anyway.
		fmt.Fprintf(&builder, "  '%s': '%s'\n", strings.ReplaceAll(name, "'", "''"), strings.ReplaceAll(version, "'", "''"))
	}
	return []byte(builder.String()), nil
}

// installPackageJSON is the staged package.json (docs/DSH_INTEGRATION.md 3.1).
type installPackageJSON struct {
	Name         string             `json:"name"`
	Private      bool               `json:"private"`
	Dependencies map[string]string  `json:"dependencies"`
	PNPM         *installPNPMConfig `json:"pnpm,omitempty"`
}

type installPNPMConfig struct {
	Overrides map[string]string `json:"overrides"`
}

// Install installs one exact DSH version from npm into
// <dataRoot>/runtimes/<version> using the bundled Node/pnpm.
//
// The install is staged in <dataRoot>/runtimes/.install-<version>-<random>,
// validated and then moved into place atomically. An already installed and
// validated runtime is reused. logf receives live pnpm output and progress
// lines (nil is allowed).
//
// A failed pnpm run keeps its staging directory for diagnosis, as required by
// docs/DSH_INTEGRATION.md 3.2; failures before staging is populated clean up.
func (s *Service) Install(ctx context.Context, version, registry string, logf func(string)) (InstallResult, error) {
	if logf == nil {
		logf = func(string) {}
	}
	target, err := s.runtimeDirectory(version)
	if err != nil {
		return InstallResult{}, err
	}
	if entry, installedErr := validateExecutable(target, version); installedErr == nil {
		emitLogf(logf, "已安装 Harness %s，保留现有运行时。", version)
		return InstallResult{Version: version, EntryPath: entry, Reused: true}, nil
	}
	if s.tools == nil || s.tools.NodePath == "" || s.tools.PnpmScript == "" {
		return InstallResult{}, fmt.Errorf("安装失败：未定位到 Node/pnpm 工具链")
	}
	base, err := normalizeRegistry(registry)
	if err != nil {
		return InstallResult{}, err
	}
	if info, statErr := os.Stat(target); statErr == nil {
		if !info.IsDir() {
			return InstallResult{}, fmt.Errorf("目标版本路径已存在但不是目录：%s", target)
		}
		return InstallResult{}, fmt.Errorf("目标版本目录已存在但入口校验失败：%s；请检查或手动清理后重试（管理器不会覆盖）", target)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return InstallResult{}, fmt.Errorf("检查目标版本目录失败：%w", statErr)
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()
	// A concurrent Install may have completed while we waited.
	if entry, installedErr := validateExecutable(target, version); installedErr == nil {
		emitLogf(logf, "已安装 Harness %s，保留现有运行时。", version)
		return InstallResult{Version: version, EntryPath: entry, Reused: true}, nil
	}
	runtimesDirectory := filepath.Dir(target)
	if err := os.MkdirAll(runtimesDirectory, 0o700); err != nil {
		return InstallResult{}, fmt.Errorf("创建 runtimes 目录失败：%w", err)
	}
	suffix, err := randomHex(8)
	if err != nil {
		return InstallResult{}, err
	}
	staging := filepath.Join(runtimesDirectory, ".install-"+version+"-"+suffix)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return InstallResult{}, fmt.Errorf("创建安装临时目录失败：%w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(staging)
		}
	}()

	client := newRegistryHTTPClient(s.currentProxy())
	overrides, err := releaseCohort(ctx, client, base, version, s.dataRoot, logf)
	if err != nil {
		return InstallResult{}, fmt.Errorf("安装失败：核对发行批次依赖未完成：%w", err)
	}
	emitLogf(logf, "使用 pnpm 安装官方 npm 包 %s@%s，完整解析 peer 依赖。安装脚本正常执行，已有版本不受影响。", DSHPackage, version)
	manifest := installPackageJSON{
		Name:         "hdsl-runtime",
		Private:      true,
		Dependencies: map[string]string{DSHPackage: version},
	}
	if len(overrides) > 0 {
		manifest.PNPM = &installPNPMConfig{Overrides: overrides}
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return InstallResult{}, fmt.Errorf("生成 package.json 失败：%w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(staging, "package.json"), manifestData, 0o600); err != nil {
		return InstallResult{}, fmt.Errorf("写入 package.json 失败：%w", err)
	}
	workspaceData, err := pnpmWorkspaceYAMLWithOverrides(overrides)
	if err != nil {
		return InstallResult{}, fmt.Errorf("生成 pnpm-workspace.yaml 失败：%w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "pnpm-workspace.yaml"), workspaceData, 0o600); err != nil {
		return InstallResult{}, fmt.Errorf("写入 pnpm-workspace.yaml 失败：%w", err)
	}

	installHome := filepath.Join(staging, installHomeDirectoryName)
	agentsHome := filepath.Join(staging, "agents-home")
	for _, directory := range []string{installHome, agentsHome} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return InstallResult{}, fmt.Errorf("创建安装临时 HOME 失败：%w", err)
		}
	}
	environment, err := buildLaunchEnvironment(launchEnvOptions{
		DataRoot:       s.dataRoot,
		NodePath:       s.tools.NodePath,
		PnpmHome:       derivePnpmHome(s.tools.ToolRoot, s.dataRoot),
		ShimsDirectory: filepath.Join(s.dataRoot, "tools", "shims"),
		DSHHome:        installHome,
		AgentsHome:     agentsHome,
		Registry:       base,
		Proxy:          s.currentProxy(),
		Home:           installHome,
		HostPath:       os.Getenv("PATH"),
	})
	if err != nil {
		return InstallResult{}, fmt.Errorf("构建安装环境失败：%w", err)
	}
	command := []string{
		s.tools.NodePath,
		s.tools.PnpmScript,
		"install",
		"--prod",
		"--no-frozen-lockfile",
		"--registry=" + base,
		"--store-dir=" + filepath.Join(s.dataRoot, "cache", "pnpm-store"),
		"--reporter=append-only",
	}
	emitLogf(logf, "[安装进度] 下载并安装依赖：pnpm 会复用之前已下载的缓存")
	keepStaging = true // from here on keep the staging directory for diagnosis
	if _, runErr := runCommand(ctx, commandSpec{
		Args:    command,
		Dir:     staging,
		Env:     environment,
		Timeout: installTimeout,
		OnLine:  logf,
	}); runErr != nil {
		emitLogf(logf, "安装未完成，诊断目录已保留：%s", staging)
		return InstallResult{}, fmt.Errorf("安装失败：%w（诊断目录 %s）", runErr, staging)
	}
	emitLogf(logf, "[安装进度] 依赖安装完成，正在检查运行时入口并保存")
	if _, err := validateExecutable(staging, version); err != nil {
		emitLogf(logf, "安装未完成，诊断目录已保留：%s", staging)
		return InstallResult{}, fmt.Errorf("安装失败：运行时入口校验未通过：%w", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		emitLogf(logf, "安装未完成，诊断目录已保留：%s", staging)
		return InstallResult{}, fmt.Errorf("安装失败：目标版本目录在安装期间已存在：%s", target)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return InstallResult{}, fmt.Errorf("检查目标版本目录失败：%w", statErr)
	}
	if err := os.Rename(staging, target); err != nil {
		emitLogf(logf, "安装未完成，诊断目录已保留：%s", staging)
		return InstallResult{}, fmt.Errorf("安装失败：无法原子移动到 %s：%w", target, err)
	}
	keepStaging = true // the staging path no longer exists after the rename
	entry, err := validateExecutable(target, version)
	if err != nil {
		return InstallResult{}, fmt.Errorf("安装完成但入口复验失败：%w", err)
	}
	s.invalidateCapabilities(version)
	emitLogf(logf, "Harness %s 安装完成；依赖版本已记录在 pnpm-lock.yaml。", version)
	emitLogf(logf, "[安装进度] Harness %s 安装完成", version)
	return InstallResult{Version: version, EntryPath: entry, Reused: false}, nil
}
