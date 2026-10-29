// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// cohortCacheSchemaVersion is the on-disk schema of a release-cohort file.
	cohortCacheSchemaVersion = 1
	// cohortCacheTTL is how long a verified release cohort may be reused.
	cohortCacheTTL = 30 * 24 * time.Hour
	// maxCohortPackages bounds the number of packages pinned to one release.
	maxCohortPackages = 2048
	// cohortFetchConcurrency bounds parallel registry metadata requests.
	cohortFetchConcurrency = 6
	// cohortClockSkewAllowance allows a cache timestamp slightly in the future.
	cohortClockSkewAllowance = 5 * time.Minute
	// maxCohortCacheBytes bounds the cohort cache file read.
	maxCohortCacheBytes = 8 << 20
)

// cohortCacheFile is the persisted release-cohort metadata
// (docs/DSH_INTEGRATION.md section 3.1).
type cohortCacheFile struct {
	SchemaVersion        int               `json:"schemaVersion"`
	Complete             bool              `json:"complete"`
	Package              string            `json:"package"`
	Version              string            `json:"version"`
	Registry             string            `json:"registry"`
	CheckedAt            epochMillis       `json:"checkedAt"`
	CheckedAtEpochMillis int64             `json:"checkedAtEpochMillis,omitempty"`
	Count                int               `json:"count"`
	Overrides            map[string]string `json:"overrides"`
	SHA256               string            `json:"sha256"`
}

// epochMillis is a cache timestamp accepting both epoch milliseconds (this
// implementation and the reference Java service) and RFC3339 strings (the
// Python/Node installation helpers) so caches are interchangeable.
type epochMillis int64

// UnmarshalJSON accepts a JSON number or an RFC3339 timestamp string.
func (value *epochMillis) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == "" {
		*value = 0
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("无效的缓存时间戳 %q：%w", text, err)
		}
		*value = epochMillis(parsed.UnixMilli())
		return nil
	}
	var millis int64
	if err := json.Unmarshal(data, &millis); err != nil {
		return err
	}
	*value = epochMillis(millis)
	return nil
}

// MarshalJSON writes the timestamp as epoch milliseconds.
func (value epochMillis) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(value))
}

// releaseCohort pins every @deepseek-ai/dsh-* package declared by this release
// to the exact version being installed, so a release candidate cannot mix
// components from different rc builds.
//
// Results are cached per registry under
// <dataRoot>/cache/registry-metadata/<sha256(registry)>/cohort-<version>.json.
func releaseCohort(ctx context.Context, client *http.Client, registry, version, dataRoot string, logf func(string)) (map[string]string, error) {
	if !IsVersion(version) || !versionPathPattern.MatchString(version) {
		return nil, fmt.Errorf("版本号 %q 无效：必须是完整的 SemVer 版本", version)
	}
	base, err := normalizeRegistry(registry)
	if err != nil {
		return nil, err
	}
	if cached, ok := loadCohortCache(dataRoot, base, version, time.Now(), logf); ok {
		emitLogf(logf, "[安装进度] 发行依赖已核对：复用缓存 %d 项，版本与下载源校验通过", len(cached))
		return cached, nil
	}
	emitLogf(logf, "[安装进度] 核对发行依赖：正在读取 %s 的公开清单", version)
	root, err := fetchPackageManifest(ctx, client, base, DSHPackage, version)
	if err != nil {
		return nil, fmt.Errorf("无法取得 Harness 发行清单：%w", err)
	}
	fetch := func(ctx context.Context, packageName string) (packageManifest, error) {
		return fetchPackageManifest(ctx, client, base, packageName, version)
	}
	overrides, err := collectCohort(ctx, fetch, root, version, maxCohortPackages, logf)
	if err != nil {
		return nil, err
	}
	saveCohortCache(dataRoot, base, version, overrides, logf)
	emitLogf(logf, "已固定 %d 个同批次组件；其他依赖继续遵循上游声明。", len(overrides))
	return overrides, nil
}

// cohortDependencies returns the @deepseek-ai/dsh-* packages that this manifest
// declares as exact members of the same release. Only specs equal to the
// version, ^version or ~version qualify; ranges and other protocol specs are
// left to upstream resolution.
func cohortDependencies(manifest packageManifest, version string) []string {
	qualifying := map[string]bool{"^" + version: true, "~" + version: true, version: true}
	found := make(map[string]bool)
	for _, dependencies := range []map[string]string{
		manifest.Dependencies,
		manifest.PeerDependencies,
		manifest.OptionalDependencies,
	} {
		for name, spec := range dependencies {
			if !strings.HasPrefix(name, DSHPackage+"-") {
				continue
			}
			if !validPackageName(name) {
				continue
			}
			if qualifying[spec] {
				found[name] = true
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// collectCohort walks the dependency graph breadth-first and returns the pinned
// override map. fetch is injectable for tests; it must verify the registry
// response itself.
func collectCohort(ctx context.Context, fetch func(context.Context, string) (packageManifest, error), root packageManifest, version string, maxPackages int, logf func(string)) (map[string]string, error) {
	if maxPackages <= 0 {
		return nil, fmt.Errorf("发行批次依赖数量上限无效")
	}
	visited := make(map[string]bool)
	queue := cohortDependencies(root, version)
	for _, name := range queue {
		visited[name] = true
	}
	overrides := make(map[string]string, len(queue))
	emitLogf(logf, "[安装进度] 核对发行依赖：已核对 0 项，当前待核对 %d 项", len(queue))
	for len(queue) > 0 {
		if len(visited) > maxPackages {
			return nil, fmt.Errorf("发行批次依赖数量异常（超过 %d 个），已停止安装", maxPackages)
		}
		batch := queue
		queue = nil
		manifests, err := fetchCohortBatch(ctx, fetch, batch)
		if err != nil {
			return nil, fmt.Errorf("无法取得发行批次依赖元数据（%w）。已完成的公开清单不会被当作完整缓存使用", err)
		}
		for index, name := range batch {
			manifest := manifests[index]
			if manifest.Name != name || manifest.Version != version {
				return nil, fmt.Errorf("%w：发行批次元数据包名或版本不匹配（%s@%s，期望 %s）", ErrUpstream, manifest.Name, manifest.Version, name+"@"+version)
			}
			overrides[name] = version
			for _, dependency := range cohortDependencies(manifest, version) {
				if visited[dependency] {
					continue
				}
				visited[dependency] = true
				queue = append(queue, dependency)
				if len(visited) > maxPackages {
					return nil, fmt.Errorf("发行批次依赖数量异常（超过 %d 个），已停止安装", maxPackages)
				}
			}
		}
		emitLogf(logf, "[安装进度] 核对发行依赖：已核对 %d 项，当前待核对 %d 项", len(overrides), len(queue))
	}
	return overrides, nil
}

// fetchCohortBatch fetches one dependency layer with bounded concurrency while
// keeping errors deterministic (first failing name wins).
func fetchCohortBatch(ctx context.Context, fetch func(context.Context, string) (packageManifest, error), names []string) ([]packageManifest, error) {
	if len(names) == 1 {
		manifest, err := fetch(ctx, names[0])
		if err != nil {
			return nil, err
		}
		return []packageManifest{manifest}, nil
	}
	manifests := make([]packageManifest, len(names))
	errs := make([]error, len(names))
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, cohortFetchConcurrency)
	var wait sync.WaitGroup
	for index, name := range names {
		index, name := index, name
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-batchCtx.Done():
				errs[index] = batchCtx.Err()
				return
			}
			defer func() { <-semaphore }()
			manifest, err := fetch(batchCtx, name)
			if err != nil {
				errs[index] = err
				cancel()
				return
			}
			manifests[index] = manifest
		}()
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return nil, err
		}
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return manifests, nil
}

// cohortCachePath returns the cache file for one registry/version pair.
func cohortCachePath(dataRoot, registry, version string) string {
	digest := sha256.Sum256([]byte(registry))
	return filepath.Join(dataRoot, "cache", "registry-metadata", hex.EncodeToString(digest[:]), "cohort-"+version+".json")
}

// cohortDigest is the canonical fingerprint of a cohort: registry, version and
// sorted name=version pairs. It detects partial or tampered cache files.
func cohortDigest(registry, version string, overrides map[string]string) string {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	canonical := strings.Builder{}
	canonical.WriteString(registry)
	canonical.WriteByte(0x0a)
	canonical.WriteString(version)
	for _, name := range names {
		canonical.WriteByte(0x0a)
		canonical.WriteString(name)
		canonical.WriteString("=")
		canonical.WriteString(overrides[name])
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:])
}

// loadCohortCache returns a verified cache entry. Any structural or age
// problem invalidates the cache and is logged; the caller then rebuilds it.
func loadCohortCache(dataRoot, registry, version string, now time.Time, logf func(string)) (map[string]string, bool) {
	path := cohortCachePath(dataRoot, registry, version)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCohortCacheBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cached cohortCacheFile
	if err := json.Unmarshal(data, &cached); err != nil {
		emitLogf(logf, "[安装进度] 发行依赖缓存已失效，正在重新核对公开元数据")
		return nil, false
	}
	checkedAt := int64(cached.CheckedAt)
	if checkedAt == 0 {
		checkedAt = cached.CheckedAtEpochMillis
	}
	nowMillis := now.UnixMilli()
	invalid := cached.SchemaVersion != cohortCacheSchemaVersion ||
		!cached.Complete ||
		cached.Package != DSHPackage ||
		cached.Version != version ||
		cached.Registry != registry ||
		checkedAt <= 0 ||
		checkedAt > nowMillis+cohortClockSkewAllowance.Milliseconds() ||
		nowMillis-checkedAt > cohortCacheTTL.Milliseconds() ||
		cached.Overrides == nil ||
		cached.Count != len(cached.Overrides) ||
		len(cached.Overrides) > maxCohortPackages ||
		cached.SHA256 == ""
	if !invalid {
		for name, pinned := range cached.Overrides {
			if !strings.HasPrefix(name, DSHPackage+"-") || !validPackageName(name) || pinned != version {
				invalid = true
				break
			}
		}
	}
	if !invalid && cohortDigest(registry, version, cached.Overrides) != cached.SHA256 {
		invalid = true
	}
	if invalid {
		emitLogf(logf, "[安装进度] 发行依赖缓存已失效，正在重新核对公开元数据")
		return nil, false
	}
	overrides := make(map[string]string, len(cached.Overrides))
	for name, pinned := range cached.Overrides {
		overrides[name] = pinned
	}
	return overrides, true
}

// saveCohortCache persists a complete cohort atomically. Failures are non-fatal:
// the install continues and the next run simply re-verifies the metadata.
func saveCohortCache(dataRoot, registry, version string, overrides map[string]string, logf func(string)) {
	path := cohortCachePath(dataRoot, registry, version)
	checkedAt := time.Now().UnixMilli()
	cached := cohortCacheFile{
		SchemaVersion:        cohortCacheSchemaVersion,
		Complete:             true,
		Package:              DSHPackage,
		Version:              version,
		Registry:             registry,
		CheckedAt:            epochMillis(checkedAt),
		CheckedAtEpochMillis: checkedAt,
		Count:                len(overrides),
		Overrides:            overrides,
		SHA256:               cohortDigest(registry, version, overrides),
	}
	data, err := json.MarshalIndent(cached, "", "  ")
	if err == nil {
		err = writeFileAtomically(path, data, 0o600)
	}
	if err != nil {
		emitLogf(logf, "公开发行依赖已核对，但缓存保存失败；本次安装继续，下次会重新核对")
	}
}

// writeFileAtomically writes data via a same-directory temporary file followed
// by an atomic rename, fsyncing the file before the rename.
func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		_ = os.Remove(temporaryName)
		return err
	}
	return nil
}

func emitLogf(logf func(string), format string, args ...any) {
	if logf == nil {
		return
	}
	logf(fmt.Sprintf(format, args...))
}
