// SPDX-License-Identifier: GPL-3.0-or-later

package pack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildNPMTarball builds a minimal valid npm tarball (package/package.json).
func buildNPMTarball(t *testing.T, packageName, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte(fmt.Sprintf("{\"name\":\"%s\",\"version\":\"%s\"}", packageName, version))
	header := &tar.Header{Name: "package/package.json", Mode: 0o600, Size: int64(len(content))}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("写入 tar header 失败: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("写入 tar 内容失败: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("关闭 tar 失败: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败: %v", err)
	}
	return buffer.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestImportVendoredTarball(t *testing.T) {
	tarball := buildNPMTarball(t, "foo", "1.0.0")
	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"vendored-pack\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"foo\":\"1.0.0\"},\"vendored\":{\"foo\":{\"path\":\"vendor/foo.tgz\",\"version\":\"1.0.0\",\"sha256\":\"%s\",\"size\":%d}}}", sha256Hex(tarball), len(tarball))
	service, dirs, _, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":    []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json":  []byte(manifest),
		"vendor/foo.tgz": tarball,
	})
	var lines []string
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	home := filepath.Join(dirs.InstancesDir, result.InstanceID, "dsh-home")
	pkgRaw, readErr := os.ReadFile(filepath.Join(home, "profiles", "demo", "package.json"))
	if readErr != nil {
		t.Fatalf("profile package.json 缺失: %v", readErr)
	}
	if !bytes.Contains(pkgRaw, []byte("file:vendor-blobs/")) {
		t.Fatalf("vendored 依赖未改写为本地 tarball: %s", pkgRaw)
	}
	blob := filepath.Join(home, "profiles", "demo", "vendor-blobs", stringsLower(sha256Hex(tarball))+".tgz")
	if _, statErr := os.Stat(blob); statErr != nil {
		t.Fatalf("vendored tarball 未复制到 profile: %v", statErr)
	}
	_ = fakeApp
}

func stringsLower(value string) string {
	return strings.ToLower(value)
}

func TestImportVendoredTarballRejectsBadSHA(t *testing.T) {
	tarball := buildNPMTarball(t, "foo", "1.0.0")
	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"bad-vendored\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"foo\":\"1.0.0\"},\"vendored\":{\"foo\":{\"path\":\"vendor/foo.tgz\",\"version\":\"1.0.0\",\"sha256\":\"%s\",\"size\":%d}}}", sha256Hex([]byte("wrong")), len(tarball))
	service, _, _, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":    []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json":  []byte(manifest),
		"vendor/foo.tgz": tarball,
	})
	_, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	requirePackAPIError(t, err, 400, "")
	if len(fakeApp.created) != 0 {
		t.Fatalf("深度校验失败不应创建实例: %v", fakeApp.created)
	}
}

func TestImportFileDependency(t *testing.T) {
	tarball := buildNPMTarball(t, "local-pkg", "1.0.0")
	manifest := "{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"file-dep\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"local-pkg\":\"file:tarballs/local-pkg.tgz\"}}"
	service, dirs, _, _, _ := newImportHarness(t)
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":                      []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json":                    []byte(manifest),
		"overrides/tarballs/local-pkg.tgz": tarball,
	})
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	if err != nil {
		t.Fatalf("file: 依赖导入失败: %v", err)
	}
	home := filepath.Join(dirs.InstancesDir, result.InstanceID, "dsh-home")
	if _, statErr := os.Stat(filepath.Join(home, "profiles", "demo", "tarballs", "local-pkg.tgz")); statErr != nil {
		t.Fatalf("file: 依赖未映射到 profile: %v", statErr)
	}
}

func TestImportFileDependencyMissing(t *testing.T) {
	manifest := "{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"missing-file-dep\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"local-pkg\":\"file:tarballs/local-pkg.tgz\"}}"
	service, _, _, fakeApp, _ := newImportHarness(t)
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":   []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json": []byte(manifest),
	})
	_, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	requirePackAPIError(t, err, 400, "")
	if len(fakeApp.created) != 0 {
		t.Fatalf("缺失 file: 依赖不应创建实例: %v", fakeApp.created)
	}
}

func TestImportDownloadsExternalResources(t *testing.T) {
	content := []byte("remote skill content")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/remote.md" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write(content)
	}))
	defer server.Close()

	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"resource-pack\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"skills/remote.md\",\"sha256\":\"%s\",\"size\":%d,\"urls\":[\"%s\"]}]}",
		sha256Hex(content), len(content), server.URL+"/remote.md")
	service, dirs, _, _, _ := newImportHarness(t)
	service.httpClient = server.Client()
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":   []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json": []byte(manifest),
	})
	result, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	if err != nil {
		t.Fatalf("资源下载导入失败: %v", err)
	}
	resourcePath := filepath.Join(dirs.InstancesDir, result.InstanceID, "dsh-home", "profiles", "demo", "skills", "remote.md")
	data, readErr := os.ReadFile(resourcePath)
	if readErr != nil || !bytes.Equal(data, content) {
		t.Fatalf("外部资源未正确下载: %v %q", readErr, data)
	}
}

func TestImportResourceDownloadFailureRollsBack(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	content := []byte("remote")
	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"profileName\":\"demo\",\"name\":\"resource-fail\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"skills/remote.md\",\"sha256\":\"%s\",\"size\":%d,\"urls\":[\"%s\"]}]}",
		sha256Hex(content), len(content), server.URL+"/remote.md")
	service, _, _, fakeApp, _ := newImportHarness(t)
	service.httpClient = server.Client()
	previewID := previewFixture(t, service, map[string][]byte{
		"dspack.json":   []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json": []byte(manifest),
	})
	_, err := service.Import(context.Background(), ImportInput{PreviewID: previewID}, nil)
	requirePackAPIError(t, err, 502, "")
	if len(fakeApp.deleted) != 1 {
		t.Fatalf("资源下载失败应回滚实例: %v", fakeApp.deleted)
	}
}
