// SPDX-License-Identifier: GPL-3.0-or-later

package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// buildArchiveExtra builds a dspack profile archive with extra entries.
func buildArchiveExtra(t *testing.T, manifest string, extra map[string][]byte) []byte {
	t.Helper()
	entries := map[string][]byte{
		"dspack.json":   []byte("{\"format\":\"dspack\",\"version\":2}"),
		"manifest.json": []byte(manifest),
	}
	for name, content := range extra {
		entries[name] = content
	}
	return zipArchive(t, entries)
}

// vendoredTarball returns content + its SHA-256 + its ZIP compressed size for a
// manifest vendored declaration.
func vendoredTarball(t *testing.T, name string, content []byte) (string, int64) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatalf("创建条目失败: %v", err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatalf("写入条目失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 ZIP 失败: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatalf("读取 ZIP 失败: %v", err)
	}
	for _, file := range reader.File {
		if file.Name == name {
			return hex.EncodeToString(sha256Sum(content)), int64(len(content))
		}
	}
	t.Fatalf("未找到条目 %s", name)
	return "", 0
}

func sha256Sum(content []byte) []byte {
	sum := sha256.Sum256(content)
	return sum[:]
}

func TestPreviewDSPackVendored(t *testing.T) {
	content := []byte("fake npm tarball bytes")
	sha, compressed := vendoredTarball(t, "vendor/foo.tgz", content)
	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"vendored\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{\"foo\":\"1.0.0\"},\"vendored\":{\"foo\":{\"path\":\"vendor/foo.tgz\",\"version\":\"1.0.0\",\"sha256\":\"%s\",\"size\":%d}}}", sha, compressed)
	service, _, _, _, _ := newPackTestService(t)
	preview, err := service.Preview(
		context.Background(),
		bytes.NewReader(buildArchiveExtra(t, manifest, map[string][]byte{"vendor/foo.tgz": content})),
		"vendored.zip",
	)
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if !strings.Contains(strings.Join(preview.Warnings, "\n"), "vendored") {
		t.Fatalf("缺少 vendored 警告: %v", preview.Warnings)
	}

	// 大小不符。
	badManifest := strings.Replace(manifest, fmt.Sprintf("\"size\":%d", compressed), fmt.Sprintf("\"size\":%d", compressed+1), 1)
	_, err = service.Preview(
		context.Background(),
		bytes.NewReader(buildArchiveExtra(t, badManifest, map[string][]byte{"vendor/foo.tgz": content})),
		"vendored-bad-size.zip",
	)
	requirePackAPIError(t, err, 400, "vendored 大小不符")

	// 未登记的 vendor 文件。
	_, err = service.Preview(
		context.Background(),
		bytes.NewReader(buildArchiveExtra(t, manifest, map[string][]byte{"vendor/foo.tgz": content, "vendor/bar.tgz": []byte("x")})),
		"vendored-unregistered.zip",
	)
	requirePackAPIError(t, err, 400, "未登记")
}

func TestPreviewDSPackResources(t *testing.T) {
	resourceSHA := strings.Repeat("a", 64)
	manifest := fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"resources\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"skills/demo.md\",\"sha256\":\"%s\",\"size\":4,\"urls\":[\"https://example.com/demo.md\"]}]}", resourceSHA)
	service, _, _, _, _ := newPackTestService(t)
	preview, err := service.Preview(context.Background(), bytes.NewReader(buildArchiveExtra(t, manifest, nil)), "resources.zip")
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if preview.Name != "resources" {
		t.Fatalf("Name = %q", preview.Name)
	}

	cases := []struct {
		name      string
		manifest  string
		substring string
	}{
		{
			"敏感资源落点",
			fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"r\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\".ssh/id_rsa\",\"sha256\":\"%s\",\"size\":4,\"urls\":[\"https://example.com/x\"]}]}", resourceSHA),
			"敏感",
		},
		{
			"HTTP 资源 URL",
			fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"r\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"skills/x.md\",\"sha256\":\"%s\",\"size\":4,\"urls\":[\"http://example.com/x\"]}]}", resourceSHA),
			"HTTPS",
		},
		{
			"资源缺 sha256",
			"{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"r\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"skills/x.md\",\"size\":4,\"urls\":[\"https://example.com/x\"]}]}",
			"sha256",
		},
		{
			"资源覆盖机器文件",
			fmt.Sprintf("{\"manifestVersion\":4,\"type\":\"profile\",\"name\":\"r\",\"version\":\"1.0.0\",\"bundles\":[],\"dependencies\":{},\"files\":[{\"path\":\"package.json\",\"sha256\":\"%s\",\"size\":4,\"urls\":[\"https://example.com/x\"]}]}", resourceSHA),
			"机器文件",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Preview(context.Background(), bytes.NewReader(buildArchiveExtra(t, tc.manifest, nil)), "bad.zip")
			requirePackAPIError(t, err, 400, tc.substring)
		})
	}
}
