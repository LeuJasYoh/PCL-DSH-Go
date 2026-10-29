// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

const serviceTestVersion = "0.2.1-rc.3"

// writeRuntimePackage writes one runtime package.json (+ optional entry) under
// packageDirectory, which must already be node_modules/@deepseek-ai/dsh.
func writeRuntimePackage(t *testing.T, packageDirectory, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(packageDirectory, "lib"), 0o700); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
}

func TestValidateExecutable(t *testing.T) {
	t.Parallel()
	const version = "0.2.1-rc.3"
	validManifest := func(bin string) string {
		return fmt.Sprintf(`{"name":"@deepseek-ai/dsh","version":"0.2.1-rc.3","bin":%s}`, bin)
	}
	cases := []struct {
		name       string
		manifest   string
		entryPath  string
		wantErr    bool
		errKeyword string
	}{
		{
			name:      "bin object",
			manifest:  validManifest(`{"dsh":"lib/bin.js"}`),
			entryPath: "lib/bin.js",
		},
		{
			name:      "bin string",
			manifest:  validManifest(`"lib/bin.js"`),
			entryPath: "lib/bin.js",
		},
		{
			name:       "no dsh key",
			manifest:   validManifest(`{"other":"lib/bin.js"}`),
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "入口",
		},
		{
			name:       "missing bin",
			manifest:   `{"name":"@deepseek-ai/dsh","version":"0.2.1-rc.3"}`,
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "入口",
		},
		{
			name:       "absolute bin",
			manifest:   validManifest(`"/etc/passwd"`),
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "包目录",
		},
		{
			name:       "parent escape",
			manifest:   validManifest(`"../../outside.js"`),
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "包目录",
		},
		{
			name:       "entry missing",
			manifest:   validManifest(`{"dsh":"lib/missing.js"}`),
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "不存在",
		},
		{
			name:       "wrong name",
			manifest:   `{"name":"@deepseek-ai/other","version":"0.2.1-rc.3","bin":"lib/bin.js"}`,
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "包名",
		},
		{
			name:       "wrong version",
			manifest:   `{"name":"@deepseek-ai/dsh","version":"0.2.1","bin":"lib/bin.js"}`,
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "版本",
		},
		{
			name:       "malformed json",
			manifest:   `{not json`,
			entryPath:  "lib/bin.js",
			wantErr:    true,
			errKeyword: "格式无效",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runtimeDirectory := t.TempDir()
			packageDirectory := filepath.Join(runtimeDirectory, "node_modules", "@deepseek-ai", "dsh")
			writeRuntimePackage(t, packageDirectory, tc.manifest)
			if tc.entryPath != "" && tc.entryPath == "lib/bin.js" {
				if err := os.WriteFile(filepath.Join(packageDirectory, tc.entryPath), []byte("entry"), 0o600); err != nil {
					t.Fatalf("write entry: %v", err)
				}
			}
			entry, err := validateExecutable(runtimeDirectory, version)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateExecutable() succeeded, want error")
				}
				if !strings.Contains(err.Error(), tc.errKeyword) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.errKeyword)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateExecutable() error = %v", err)
			}
			want := filepath.Join(packageDirectory, "lib", "bin.js")
			if entry != want {
				t.Fatalf("entry = %q, want %q", entry, want)
			}
		})
	}
}

func TestValidateExecutableSymlinkEscape(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privileges on Windows")
	}
	runtimeDirectory := t.TempDir()
	packageDirectory := filepath.Join(runtimeDirectory, "node_modules", "@deepseek-ai", "dsh")
	manifest := `{"name":"@deepseek-ai/dsh","version":"0.2.1-rc.3","bin":{"dsh":"lib/link.js"}}`
	writeRuntimePackage(t, packageDirectory, manifest)
	outside := filepath.Join(runtimeDirectory, "outside.js")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(packageDirectory, "lib", "link.js")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	_, err := validateExecutable(runtimeDirectory, "0.2.1-rc.3")
	if err == nil || !strings.Contains(err.Error(), "realpath") {
		t.Fatalf("error = %v, want a realpath escape rejection", err)
	}
}

func TestInstalledVersions(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	writeRuntimeAt(t, runtimesDirectory, "0.2.1-rc.10")
	writeRuntimeAt(t, runtimesDirectory, "0.2.1")
	writeRuntimeAt(t, runtimesDirectory, "0.2.0")
	// Invalid/incomplete entries must be ignored.
	if err := os.MkdirAll(filepath.Join(runtimesDirectory, "0.1.0"), 0o700); err != nil {
		t.Fatalf("mkdir incomplete runtime: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(runtimesDirectory, ".install-0.3.0-abc"), 0o700); err != nil {
		t.Fatalf("mkdir staging dir: %v", err)
	}
	service := New(nil, nil, dataRoot)
	got := service.InstalledVersions()
	want := []string{"0.2.1", "0.2.1-rc.10", "0.2.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("InstalledVersions() = %v, want %v", got, want)
	}
	if !service.Installed("0.2.1") || service.Installed("0.1.0") || service.Installed("latest") {
		t.Fatalf("Installed() returned wrong values")
	}
}

func TestServiceProbeCachingAndInvalidation(t *testing.T) {
	t.Parallel()
	dataRoot := filepath.Join(t.TempDir(), scenarioOK)
	entry := writeRuntimeAt(t, filepath.Join(dataRoot, "runtimes"), serviceTestVersion)
	service := New(nil, &tools.Tools{NodePath: testNodePath(t)}, dataRoot)

	if got := service.Compatibility(serviceTestVersion); got != "unverified" {
		t.Fatalf("Compatibility() before probe = %q, want unverified", got)
	}
	capabilities, err := service.Probe(context.Background(), serviceTestVersion, "web")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !capabilities.ProfileSelection() || !capabilities.Supports("--port") || !capabilities.AppHelpVerified() {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	if capabilities.EntryPath != entry || capabilities.Version != serviceTestVersion || capabilities.Profile != "web" {
		t.Fatalf("capabilities metadata = %+v", capabilities)
	}
	if got := service.Compatibility(serviceTestVersion); got != m0CompatibilityLabel {
		t.Fatalf("Compatibility() after probe = %q, want %q", got, m0CompatibilityLabel)
	}

	// A cached probe must not need the toolchain again.
	service.tools.NodePath = ""
	cached, err := service.Probe(context.Background(), serviceTestVersion, "web")
	if err != nil {
		t.Fatalf("cached Probe() error = %v", err)
	}
	if cached.EntryPath != capabilities.EntryPath {
		t.Fatalf("cached capabilities = %+v", cached)
	}
	service.invalidateCapabilities(serviceTestVersion)
	if _, err := service.Probe(context.Background(), serviceTestVersion, "web"); err == nil {
		t.Fatalf("Probe() succeeded after cache invalidation without a toolchain")
	}
}

func TestServiceProbeCompatibilityChangeInvalidatesCache(t *testing.T) {
	t.Parallel()
	dataRoot := filepath.Join(t.TempDir(), scenarioOK)
	writeRuntimeAt(t, filepath.Join(dataRoot, "runtimes"), serviceTestVersion)
	service := New(nil, &tools.Tools{NodePath: testNodePath(t)}, dataRoot)
	first, err := service.Probe(context.Background(), serviceTestVersion, "web")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if first.Supports("--public-url") {
		t.Fatalf("--public-url was detected without a declaration")
	}
	writeCompatibility(t, dataRoot, `{"versions":{"0.2.1-rc.3":{"appOptions":["--public-url"]}}}`)
	compatibilityPath := filepath.Join(dataRoot, "config", "compatibility.json")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(compatibilityPath, future, future); err != nil {
		t.Fatalf("chtimes compatibility.json: %v", err)
	}
	second, err := service.Probe(context.Background(), serviceTestVersion, "web")
	if err != nil {
		t.Fatalf("Probe() after compatibility change error = %v", err)
	}
	if !second.Supports("--public-url") {
		t.Fatalf("compatibility.json change did not invalidate the probe cache: %+v", second)
	}
	if joined := strings.Join(second.Warnings(), "\n"); !strings.Contains(joined, "人工能力声明") {
		t.Fatalf("warnings = %v", second.Warnings())
	}
}

func TestServiceProbeNotInstalled(t *testing.T) {
	t.Parallel()
	service := New(nil, &tools.Tools{NodePath: testNodePath(t)}, t.TempDir())
	if _, err := service.Probe(context.Background(), serviceTestVersion, "web"); err == nil {
		t.Fatalf("Probe() succeeded for a missing runtime")
	}
	if _, err := service.Probe(context.Background(), serviceTestVersion, "bad/profile"); err == nil {
		t.Fatalf("Probe() accepted an invalid profile")
	}
}

func TestServiceAvailableVersions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"@deepseek-ai/dsh","versions":{"0.2.1-rc.3":{},"0.2.1":{},"0.2.0":{},"latest":{}}}`))
	}))
	defer server.Close()
	service := New(nil, nil, t.TempDir())
	versions, err := service.AvailableVersions(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("AvailableVersions() error = %v", err)
	}
	want := []string{"0.2.1", "0.2.1-rc.3", "0.2.0"}
	if strings.Join(versions, ",") != strings.Join(want, ",") {
		t.Fatalf("AvailableVersions() = %v, want %v", versions, want)
	}
}

func TestServiceNewNormalizesDataRoot(t *testing.T) {
	t.Parallel()
	service := New(nil, nil, t.TempDir())
	if service.dataRoot == "" || !filepath.IsAbs(service.dataRoot) {
		t.Fatalf("dataRoot = %q, want an absolute path", service.dataRoot)
	}
}
