// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

const (
	maxProfileManifestBytes   = 8 << 20
	maxInstalledManifestBytes = 8 << 20
)

// Plugin source labels returned in DTO.Source.
const (
	sourceBuiltin   = "内置"
	sourceOfficial  = "官方"
	sourceLocal     = "本地"
	sourceCommunity = "社区"
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// validProfileName mirrors the instance-store profile rules and additionally
// rejects "." and "..".
func validProfileName(profile string) bool {
	return profileNamePattern.MatchString(profile) && profile != "." && profile != ".."
}

// profileManifest is the subset of the profile package.json read by List.
type profileManifest struct {
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	DSH                  struct {
		Profile struct {
			Bundles []json.RawMessage `json:"bundles"`
		} `json:"profile"`
	} `json:"dsh"`
}

// installedManifest is the subset of an installed package.json used by List.
type installedManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// pluginCandidate is one package declared by the profile.
type pluginCandidate struct {
	name         string
	declaredSpec string
}

// List returns the plugins of one instance profile
// (docs/M3_INTERFACES.md 5, docs/DSH_INTEGRATION.md 8.1).
//
// A profile that has no package.json yet yields an empty list (not 404). The
// instance must exist, and profile must be valid; both failures are
// *apierr.Error values so the API can map them directly.
func (s *Service) List(ctx context.Context, instanceID, profile string) ([]DTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance, err := s.instance(instanceID)
	if err != nil {
		return nil, err
	}
	profile = strings.TrimSpace(profile)
	if !validProfileName(profile) {
		return nil, apierr.New(http.StatusBadRequest, apierr.CodeValidation, "profile 参数无效。")
	}
	manifestPath := filepath.Join(s.profileDirectory(instance.ID, profile), "package.json")
	manifest, exists, err := readProfileManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []DTO{}, nil
	}
	candidates, bundles := collectPluginCandidates(manifest)
	rows := make([]DTO, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, s.pluginDTO(instance, profile, candidate, bundles[candidate.name]))
	}
	sort.SliceStable(rows, func(left, right int) bool {
		leftRank, rightRank := sourceRank(rows[left].Source), sourceRank(rows[right].Source)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return rows[left].Name < rows[right].Name
	})
	return rows, nil
}

// collectPluginCandidates merges dependencies/optionalDependencies and
// dsh.profile.bundles into a deterministic, deduplicated list.
func collectPluginCandidates(manifest profileManifest) ([]pluginCandidate, map[string]bool) {
	byName := make(map[string]pluginCandidate)
	bundles := make(map[string]bool)
	for _, name := range bundleNames(manifest.DSH.Profile.Bundles) {
		bundles[name] = true
		if _, seen := byName[name]; !seen {
			byName[name] = pluginCandidate{name: name}
		}
	}
	for _, dependencies := range []map[string]string{manifest.Dependencies, manifest.OptionalDependencies} {
		names := make([]string, 0, len(dependencies))
		for name := range dependencies {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			candidate := byName[name]
			candidate.name = name
			candidate.declaredSpec = dependencies[name]
			byName[name] = candidate
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	candidates := make([]pluginCandidate, 0, len(names))
	for _, name := range names {
		candidates = append(candidates, byName[name])
	}
	return candidates, bundles
}

// bundleNames extracts ids from dsh.profile.bundles entries, accepting both
// plain strings and {"name":...}/"package":...} objects.
func bundleNames(raw []json.RawMessage) []string {
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		var text string
		if err := json.Unmarshal(item, &text); err == nil {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				names = append(names, trimmed)
			}
			continue
		}
		var object struct {
			Name    string `json:"name"`
			Package string `json:"package"`
		}
		if err := json.Unmarshal(item, &object); err != nil {
			continue
		}
		id := strings.TrimSpace(object.Name)
		if id == "" {
			id = strings.TrimSpace(object.Package)
		}
		if id != "" {
			names = append(names, id)
		}
	}
	return names
}

// pluginDTO builds one row from the declared profile metadata and the installed
// package metadata (profile/node_modules first for profile dependencies, then
// the runtime's node_modules).
func (s *Service) pluginDTO(instance store.Instance, profile string, candidate pluginCandidate, isBundle bool) DTO {
	version := candidate.declaredSpec
	if version == "" {
		version = sourceBuiltin
	}
	row := DTO{
		Name:    candidate.name,
		Version: version,
		Source:  pluginSource(candidate.name, candidate.declaredSpec, isBundle),
	}
	installedPath := s.resolveInstalledPackage(instance, profile, candidate)
	if installedPath == "" {
		row.Version = "待恢复依赖"
		return row
	}
	metadata, err := readInstalledManifest(installedPath)
	if err != nil {
		row.Version = "待恢复依赖"
		return row
	}
	if trimmed := strings.TrimSpace(metadata.Version); trimmed != "" {
		row.Version = trimmed
	}
	row.Description = strings.TrimSpace(metadata.Description)
	return row
}

// resolveInstalledPackage returns the installed package.json path, or "" when
// the package is not installed.
func (s *Service) resolveInstalledPackage(instance store.Instance, profile string, candidate pluginCandidate) string {
	runtimePackage := filepath.Join(s.dirs.RuntimesDir, instance.Version, "node_modules", filepath.FromSlash(candidate.name), "package.json")
	profilePackage := filepath.Join(s.profileDirectory(instance.ID, profile), "node_modules", filepath.FromSlash(candidate.name), "package.json")
	profileDependency := candidate.declaredSpec != ""
	runtimeExists := isRegularFile(runtimePackage)
	profileExists := isRegularFile(profilePackage)
	switch {
	case !profileDependency && runtimeExists:
		return runtimePackage
	case profileExists:
		return profilePackage
	case runtimeExists:
		return runtimePackage
	default:
		return ""
	}
}

// pluginSource classifies a package for the frontend.
func pluginSource(name, declaredSpec string, isBundle bool) string {
	switch {
	case isBundle:
		return sourceBuiltin
	case strings.HasPrefix(name, "@deepseek-ai/"):
		return sourceOfficial
	case isLocalPluginSpec(declaredSpec):
		return sourceLocal
	default:
		return sourceCommunity
	}
}

// sourceRank orders the list: builtin/official first, then local/community.
func sourceRank(source string) int {
	switch source {
	case sourceBuiltin:
		return 0
	case sourceOfficial:
		return 1
	case sourceLocal:
		return 2
	default:
		return 3
	}
}

// readProfileManifest reads <profile>/package.json. The bool is false when the
// file does not exist (uninitialized profile), which is not an error.
func readProfileManifest(path string) (profileManifest, bool, error) {
	var manifest profileManifest
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return profileManifest{}, false, nil
		}
		return profileManifest{}, false, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "读取 profile 清单失败。")
	}
	if !info.Mode().IsRegular() {
		return profileManifest{}, false, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "profile 清单不是普通文件。")
	}
	if info.Size() > maxProfileManifestBytes {
		return profileManifest{}, false, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "profile 清单过大。")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return profileManifest{}, false, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "读取 profile 清单失败。")
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return profileManifest{}, false, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "profile 清单格式无效。")
	}
	return manifest, true, nil
}

// readInstalledManifest reads one installed package.json.
func readInstalledManifest(path string) (installedManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return installedManifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxInstalledManifestBytes {
		return installedManifest{}, fmt.Errorf("installed manifest %s is not a readable regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return installedManifest{}, err
	}
	var manifest installedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return installedManifest{}, err
	}
	return manifest, nil
}

// isRegularFile reports whether path is an existing regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
