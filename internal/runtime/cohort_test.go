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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const cohortTestVersion = "0.2.1-rc.3"

type cohortTestRegistry struct {
	t         *testing.T
	server    *httptest.Server
	mu        sync.Mutex
	manifests map[string]packageManifest
	failures  map[string]int
	requests  map[string]int
}

func newCohortTestRegistry(t *testing.T) *cohortTestRegistry {
	t.Helper()
	registry := &cohortTestRegistry{
		t:         t,
		manifests: make(map[string]packageManifest),
		failures:  make(map[string]int),
		requests:  make(map[string]int),
	}
	registry.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		registry.mu.Lock()
		registry.requests[path]++
		status := registry.failures[path]
		manifest, ok := registry.manifests[path]
		registry.mu.Unlock()
		switch {
		case status != 0:
			w.WriteHeader(status)
		case !ok:
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(manifest)
		}
	}))
	t.Cleanup(registry.server.Close)
	return registry
}

func (r *cohortTestRegistry) url() string { return r.server.URL }

func (r *cohortTestRegistry) path(name, version string) string {
	return "/" + name + "/" + version
}

func (r *cohortTestRegistry) add(name, version string, dependencies, peerDependencies, optionalDependencies map[string]string) {
	r.manifests[r.path(name, version)] = packageManifest{
		Name:                 name,
		Version:              version,
		Dependencies:         dependencies,
		PeerDependencies:     peerDependencies,
		OptionalDependencies: optionalDependencies,
	}
}

func (r *cohortTestRegistry) addFailures(name, version string, status int) {
	r.failures[r.path(name, version)] = status
}

func (r *cohortTestRegistry) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, count := range r.requests {
		total += count
	}
	return total
}

func (r *cohortTestRegistry) requested(name, version string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[r.path(name, version)]
}

func readCohortCacheFile(t *testing.T, dataRoot, registry, version string) cohortCacheFile {
	t.Helper()
	data, err := os.ReadFile(cohortCachePath(dataRoot, registry, version))
	if err != nil {
		t.Fatalf("read cohort cache: %v", err)
	}
	var cached cohortCacheFile
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("decode cohort cache: %v", err)
	}
	return cached
}

func writeCohortCacheFile(t *testing.T, dataRoot, registry, version string, cached cohortCacheFile) {
	t.Helper()
	path := cohortCachePath(dataRoot, registry, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cohort cache dir: %v", err)
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatalf("encode cohort cache: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write cohort cache: %v", err)
	}
}

func TestCohortDependencies(t *testing.T) {
	t.Parallel()
	version := "0.2.1-rc.3"
	manifest := packageManifest{
		Dependencies: map[string]string{
			"@deepseek-ai/dsh-exact":     version,
			"@deepseek-ai/dsh-caret":     "^" + version,
			"@deepseek-ai/dsh-tilde":     "~" + version,
			"@deepseek-ai/dsh-other-rc":  "0.2.1-rc.2",
			"@deepseek-ai/dsh-range":     ">=0.2.1-rc.1",
			"@deepseek-ai/other-package": version,
			"lodash":                     "^4.17.0",
		},
		PeerDependencies: map[string]string{
			"@deepseek-ai/dsh-peer":  version,
			"@deepseek-ai/dsh-caret": version,
		},
		OptionalDependencies: map[string]string{
			"@deepseek-ai/dsh-optional": "~" + version,
			"@deepseek-ai/dsh-bad name": version,
		},
	}
	got := cohortDependencies(manifest, version)
	want := []string{
		"@deepseek-ai/dsh-caret",
		"@deepseek-ai/dsh-exact",
		"@deepseek-ai/dsh-optional",
		"@deepseek-ai/dsh-peer",
		"@deepseek-ai/dsh-tilde",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("cohortDependencies() = %v, want %v", got, want)
	}
}

func TestReleaseCohortSuccessAndCacheReuse(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.add(DSHPackage, cohortTestVersion, map[string]string{
		"@deepseek-ai/dsh-core":   cohortTestVersion,
		"@deepseek-ai/dsh-web":    "^" + cohortTestVersion,
		"@deepseek-ai/dsh-native": "~" + cohortTestVersion,
		"@deepseek-ai/dsh-skip":   "0.2.1-rc.2",
		"@deepseek-ai/other":      cohortTestVersion,
		"lodash":                  "^4.17.21",
	}, nil, nil)
	registry.add("@deepseek-ai/dsh-core", cohortTestVersion, map[string]string{
		"@deepseek-ai/dsh-util": cohortTestVersion,
	}, nil, nil)
	registry.add("@deepseek-ai/dsh-web", cohortTestVersion, nil, map[string]string{
		"@deepseek-ai/dsh-core": cohortTestVersion,
	}, nil)
	registry.add("@deepseek-ai/dsh-native", cohortTestVersion, nil, nil, map[string]string{
		"@deepseek-ai/dsh-util": "^" + cohortTestVersion,
	})
	registry.add("@deepseek-ai/dsh-util", cohortTestVersion, nil, nil, nil)

	dataRoot := t.TempDir()
	var logs []string
	logf := func(line string) { logs = append(logs, line) }
	overrides, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, logf)
	if err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	for _, name := range []string{
		"@deepseek-ai/dsh-core",
		"@deepseek-ai/dsh-native",
		"@deepseek-ai/dsh-util",
		"@deepseek-ai/dsh-web",
	} {
		if overrides[name] != cohortTestVersion {
			t.Fatalf("overrides[%q] = %q, want %q (overrides=%v)", name, overrides[name], cohortTestVersion, overrides)
		}
	}
	if len(overrides) != 4 {
		t.Fatalf("overrides = %v, want exactly the 4 in-cohort packages", overrides)
	}
	if registry.requested("@deepseek-ai/dsh-skip", "0.2.1-rc.2") != 0 {
		t.Fatalf("out-of-cohort package was requested")
	}
	if registry.requested("@deepseek-ai/other", cohortTestVersion) != 0 {
		t.Fatalf("non-dsh package was requested")
	}

	cached := readCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion)
	if cached.SchemaVersion != 1 || !cached.Complete || cached.Package != DSHPackage ||
		cached.Version != cohortTestVersion || cached.Registry != registry.url() ||
		cached.Count != len(overrides) || cached.CheckedAt <= 0 {
		t.Fatalf("cache file = %+v", cached)
	}
	if cached.SHA256 != cohortDigest(registry.url(), cohortTestVersion, overrides) {
		t.Fatalf("cache digest mismatch")
	}

	requestsBefore := registry.requestCount()
	logs = nil
	overrides2, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, logf)
	if err != nil {
		t.Fatalf("releaseCohort() second call error = %v", err)
	}
	if registry.requestCount() != requestsBefore {
		t.Fatalf("cached cohort still queried the registry: %d -> %d requests", requestsBefore, registry.requestCount())
	}
	if fmt.Sprint(overrides2) != fmt.Sprint(overrides) {
		t.Fatalf("cached overrides = %v, want %v", overrides2, overrides)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "复用缓存") {
		t.Fatalf("logs = %q, want a cache-reuse message", joined)
	}
}

func TestReleaseCohortExpiredCacheIsRebuilt(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.add(DSHPackage, cohortTestVersion, map[string]string{"@deepseek-ai/dsh-core": cohortTestVersion}, nil, nil)
	registry.add("@deepseek-ai/dsh-core", cohortTestVersion, nil, nil, nil)
	dataRoot := t.TempDir()
	if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	cached := readCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion)
	cached.CheckedAt = epochMillis(time.Now().Add(-cohortCacheTTL - time.Hour).UnixMilli())
	writeCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion, cached)

	requestsBefore := registry.requestCount()
	var logs []string
	if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, func(line string) {
		logs = append(logs, line)
	}); err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	if registry.requestCount() <= requestsBefore {
		t.Fatalf("expired cache was reused instead of rebuilt")
	}
	if !strings.Contains(strings.Join(logs, "\n"), "缓存已失效") {
		t.Fatalf("logs = %q, want an invalidation message", logs)
	}
}

func TestReleaseCohortCorruptOrTamperedCacheIsRebuilt(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.add(DSHPackage, cohortTestVersion, map[string]string{"@deepseek-ai/dsh-core": cohortTestVersion}, nil, nil)
	registry.add("@deepseek-ai/dsh-core", cohortTestVersion, nil, nil, nil)
	dataRoot := t.TempDir()
	if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}

	t.Run("garbage", func(t *testing.T) {
		path := cohortCachePath(dataRoot, registry.url(), cohortTestVersion)
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write corrupt cache: %v", err)
		}
		if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
			t.Fatalf("releaseCohort() error = %v", err)
		}
		cached := readCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion)
		if cached.SHA256 != cohortDigest(registry.url(), cohortTestVersion, cached.Overrides) {
			t.Fatalf("corrupt cache was not replaced by a valid one")
		}
	})

	t.Run("digest", func(t *testing.T) {
		cached := readCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion)
		cached.SHA256 = strings.Repeat("0", 64)
		writeCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion, cached)
		requestsBefore := registry.requestCount()
		if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
			t.Fatalf("releaseCohort() error = %v", err)
		}
		if registry.requestCount() <= requestsBefore {
			t.Fatalf("digest mismatch did not trigger a rebuild")
		}
	})

	t.Run("future timestamp", func(t *testing.T) {
		cached := readCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion)
		cached.CheckedAt = epochMillis(time.Now().Add(10 * time.Minute).UnixMilli())
		writeCohortCacheFile(t, dataRoot, registry.url(), cohortTestVersion, cached)
		requestsBefore := registry.requestCount()
		if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
			t.Fatalf("releaseCohort() error = %v", err)
		}
		if registry.requestCount() <= requestsBefore {
			t.Fatalf("future-dated cache was trusted")
		}
	})
}

// TestCohortCacheMatchesRealRC3Fixture validates the cache reader and digest
// algorithm against a real @deepseek-ai/dsh@0.2.1-rc.3 cohort produced by the
// independent Node implementation (295 pinned release packages).
func TestCohortCacheMatchesRealRC3Fixture(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile(filepath.Join("testdata", "cohort-0.2.1-rc.3.json"))
	if err != nil {
		t.Fatalf("read cohort fixture: %v", err)
	}
	var document cohortCacheFile
	if err := json.Unmarshal(fixture, &document); err != nil {
		t.Fatalf("decode cohort fixture: %v", err)
	}
	const registry = "https://registry.npmjs.org"
	const version = "0.2.1-rc.3"
	if document.Registry != registry || document.Version != version {
		t.Fatalf("fixture registry/version = %q/%q", document.Registry, document.Version)
	}
	if document.Count != 295 || len(document.Overrides) != 295 {
		t.Fatalf("fixture count = %d, overrides = %d, want 295", document.Count, len(document.Overrides))
	}
	if got := cohortDigest(registry, version, document.Overrides); got != document.SHA256 {
		t.Fatalf("cohortDigest() = %q, fixture sha256 = %q", got, document.SHA256)
	}

	dataRoot := t.TempDir()
	cachePath := cohortCachePath(dataRoot, registry, version)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, fixture, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	checkedAt := time.UnixMilli(int64(document.CheckedAt))
	overrides, ok := loadCohortCache(dataRoot, registry, version, checkedAt, nil)
	if !ok {
		t.Fatalf("loadCohortCache() rejected the real cohort fixture")
	}
	if len(overrides) != 295 {
		t.Fatalf("loadCohortCache() = %d overrides, want 295", len(overrides))
	}
	if got := overrides["@deepseek-ai/dsh-base"]; got != version {
		t.Fatalf("dsh-base pinned to %q, want %q", got, version)
	}
	if got := overrides["@deepseek-ai/dsh-web"]; got != version {
		t.Fatalf("dsh-web pinned to %q, want %q", got, version)
	}
	if _, present := overrides["@deepseek-ai/dsh"]; present {
		t.Fatalf("root package must not be pinned in the cohort")
	}
}

func TestCohortCacheAcceptsISOTimestamp(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.add(DSHPackage, cohortTestVersion, map[string]string{"@deepseek-ai/dsh-core": cohortTestVersion}, nil, nil)
	registry.add("@deepseek-ai/dsh-core", cohortTestVersion, nil, nil, nil)
	dataRoot := t.TempDir()
	if _, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil); err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	// The Node installation helper writes checkedAt as an RFC3339 string; the
	// Go implementation must keep using such a cache instead of rebuilding it.
	path := cohortCachePath(dataRoot, registry.url(), cohortTestVersion)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	document["checkedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode cache: %v", err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	requestsBefore := registry.requestCount()
	overrides, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, nil)
	if err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	if registry.requestCount() != requestsBefore {
		t.Fatalf("ISO timestamp cache was rejected; requests %d -> %d", requestsBefore, registry.requestCount())
	}
	if len(overrides) != 1 {
		t.Fatalf("overrides = %v, want one pinned package", overrides)
	}
}

func TestReleaseCohortCacheSaveFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.add(DSHPackage, cohortTestVersion, nil, nil, nil)
	dataRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataRoot, []byte("x"), 0o600); err != nil {
		t.Fatalf("write dataRoot file: %v", err)
	}
	var logs []string
	overrides, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, dataRoot, func(line string) {
		logs = append(logs, line)
	})
	if err != nil {
		t.Fatalf("releaseCohort() error = %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("overrides = %v, want empty", overrides)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "缓存保存失败") {
		t.Fatalf("logs = %q, want a cache-save warning", logs)
	}
}

func TestReleaseCohortRegistryFailure(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.addFailures(DSHPackage, cohortTestVersion, http.StatusInternalServerError)
	_, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, t.TempDir(), nil)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
}

func TestReleaseCohortManifestMismatch(t *testing.T) {
	t.Parallel()
	registry := newCohortTestRegistry(t)
	registry.manifests[registry.path(DSHPackage, cohortTestVersion)] = packageManifest{Name: DSHPackage, Version: "9.9.9"}
	_, err := releaseCohort(context.Background(), newRegistryHTTPClient(""), registry.url(), cohortTestVersion, t.TempDir(), nil)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream for a mismatched manifest", err)
	}
}

func TestReleaseCohortInputValidation(t *testing.T) {
	t.Parallel()
	client := newRegistryHTTPClient("")
	if _, err := releaseCohort(context.Background(), client, "http://127.0.0.1:1", "latest", t.TempDir(), nil); err == nil {
		t.Fatalf("invalid version was accepted")
	}
	if _, err := releaseCohort(context.Background(), client, "ftp://registry", cohortTestVersion, t.TempDir(), nil); !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("error = %v, want ErrInvalidRegistry", err)
	}
}

func TestCollectCohortLimit(t *testing.T) {
	t.Parallel()
	root := packageManifest{
		Name:         DSHPackage,
		Version:      "1.0.0",
		Dependencies: map[string]string{"@deepseek-ai/dsh-p0": "1.0.0"},
	}
	chain := func(_ context.Context, name string) (packageManifest, error) {
		index := 0
		_, _ = fmt.Sscanf(name, "@deepseek-ai/dsh-p%d", &index)
		dependencies := map[string]string{}
		if index < 3 {
			dependencies[fmt.Sprintf("@deepseek-ai/dsh-p%d", index+1)] = "1.0.0"
		}
		return packageManifest{Name: name, Version: "1.0.0", Dependencies: dependencies}, nil
	}
	if _, err := collectCohort(context.Background(), chain, root, "1.0.0", 4, nil); err != nil {
		t.Fatalf("collectCohort() at the limit error = %v", err)
	}
	if _, err := collectCohort(context.Background(), chain, root, "1.0.0", 3, nil); err == nil {
		t.Fatalf("collectCohort() above the limit succeeded, want error")
	}
}

func TestCollectCohortDeterministicError(t *testing.T) {
	t.Parallel()
	root := packageManifest{
		Name:    DSHPackage,
		Version: "1.0.0",
		Dependencies: map[string]string{
			"@deepseek-ai/dsh-a": "1.0.0",
			"@deepseek-ai/dsh-b": "1.0.0",
			"@deepseek-ai/dsh-c": "1.0.0",
		},
	}
	boom := errors.New("network down")
	fetch := func(_ context.Context, name string) (packageManifest, error) {
		if name != "@deepseek-ai/dsh-a" {
			return packageManifest{}, boom
		}
		return packageManifest{Name: name, Version: "1.0.0"}, nil
	}
	_, err := collectCohort(context.Background(), fetch, root, "1.0.0", 10, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the injected fetch error", err)
	}
}

func TestCohortDigest(t *testing.T) {
	t.Parallel()
	first := map[string]string{"@deepseek-ai/dsh-b": "1.0.0", "@deepseek-ai/dsh-a": "1.0.0"}
	second := map[string]string{"@deepseek-ai/dsh-a": "1.0.0", "@deepseek-ai/dsh-b": "1.0.0"}
	if cohortDigest("https://r", "1.0.0", first) != cohortDigest("https://r", "1.0.0", second) {
		t.Fatalf("digest depends on map iteration order")
	}
	if cohortDigest("https://r", "1.0.0", first) == cohortDigest("https://r", "1.0.1", first) {
		t.Fatalf("digest does not depend on version")
	}
	if cohortDigest("https://r", "1.0.0", first) == cohortDigest("https://s", "1.0.0", first) {
		t.Fatalf("digest does not depend on registry")
	}
	if cohortDigest("https://r", "1.0.0", first) == cohortDigest("https://r", "1.0.0", map[string]string{"@deepseek-ai/dsh-a": "1.0.0"}) {
		t.Fatalf("digest does not depend on overrides")
	}
}

func TestCohortCachePathUsesRegistryHash(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := cohortCachePath(root, "https://registry.npmjs.org", "1.0.0")
	second := cohortCachePath(root, "https://registry.example.com", "1.0.0")
	if first == second {
		t.Fatalf("different registries share a cache path")
	}
	if !strings.Contains(first, "cohort-1.0.0.json") {
		t.Fatalf("cache path %q does not contain the version", first)
	}
	digest := sha256.Sum256([]byte("https://registry.npmjs.org"))
	if !strings.Contains(first, hex.EncodeToString(digest[:])) {
		t.Fatalf("cache path %q does not contain sha256(registry)", first)
	}
	if !strings.Contains(filepath.ToSlash(first), "/cache/registry-metadata/") {
		t.Fatalf("cache path %q is not under cache/registry-metadata", first)
	}
}
