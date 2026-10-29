// SPDX-License-Identifier: MIT

package artifact

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newDownloadServer mounts the artifact routes behind a fake tracing
// middleware, mirroring how the API layer mounts Store.Register.
func newDownloadServer(t *testing.T, s *Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace-Id", "abc123def456")
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// artifactClient is the HTTP client used by the download tests.
func artifactClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

// getArtifact performs GET path and returns the response plus its body.
func getArtifact(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := artifactClient().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return resp, body
}

// assertArtifactNotFound asserts the documented four-field JSON 404.
func assertArtifactNotFound(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d，期望 404（body %s）", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q，期望 no-store", cc)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("解析 404 响应失败: %v (%s)", err, body)
	}
	wantKeys := []string{"code", "message", "details", "traceId"}
	if len(raw) != len(wantKeys) {
		t.Fatalf("404 字段 = %v，期望 %v", raw, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("404 缺少字段 %q: %s", key, body)
		}
	}
	var envelope struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
		TraceID string         `json:"traceId"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("解析 404 响应失败: %v", err)
	}
	if envelope.Code != "NOT_FOUND" {
		t.Fatalf("code = %q，期望 NOT_FOUND", envelope.Code)
	}
	if envelope.Message == "" {
		t.Fatal("404 message 为空")
	}
	if envelope.Details == nil || len(envelope.Details) != 0 {
		t.Fatalf("details = %#v，期望空对象", envelope.Details)
	}
	if envelope.TraceID != "abc123def456" {
		t.Fatalf("traceId = %q，期望 abc123def456", envelope.TraceID)
	}
	if string(raw["details"]) == "null" {
		t.Fatal("details 不能是 null")
	}
	if strings.Contains(string(body), "dspack") {
		t.Fatalf("404 响应泄漏内容: %s", body)
	}
}

func TestDownloadStreamsArtifact(t *testing.T) {
	s, dir, _ := newTestStore(t)
	content := strings.Repeat("dspack-data-", 1024)
	art := mustAdd(t, s, dir, "export.zip", content, "application/zip")
	srv := newDownloadServer(t, s)

	resp, body := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d，期望 200", resp.StatusCode)
	}
	if string(body) != content {
		t.Fatalf("下载内容长度 = %d，期望 %d", len(body), len(content))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "attachment; filename=export.zip" {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(content)) {
		t.Fatalf("Content-Length = %q，期望 %d", cl, len(content))
	}
	if got := resp.Header.Get("X-Trace-Id"); got != "abc123def456" {
		t.Fatalf("X-Trace-Id = %q", got)
	}
}

func TestDownloadUnicodeFilename(t *testing.T) {
	s, dir, _ := newTestStore(t)
	art := mustAdd(t, s, dir, "导出.zip", "data", "application/zip")
	srv := newDownloadServer(t, s)
	resp, body := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d，期望 200", resp.StatusCode)
	}
	if string(body) != "data" {
		t.Fatalf("body = %q", body)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q，期望 attachment", cd)
	}
	if !strings.Contains(cd, "filename*=utf-8''%E5%AF%BC%E5%87%BA.zip") {
		t.Fatalf("Content-Disposition = %q，期望 RFC 5987 编码文件名", cd)
	}
}

func TestDownloadDefaultContentType(t *testing.T) {
	s, dir, _ := newTestStore(t)
	art := mustAdd(t, s, dir, "blob.bin", "raw", "")
	srv := newDownloadServer(t, s)
	resp, _ := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != defaultContentType {
		t.Fatalf("Content-Type = %q，期望 %q", ct, defaultContentType)
	}
}

func TestDownloadUnknownArtifact404(t *testing.T) {
	s, _, _ := newTestStore(t)
	srv := newDownloadServer(t, s)
	resp, body := getArtifact(t, srv, "/api/artifacts/000000000000")
	assertArtifactNotFound(t, resp, body)
}

func TestDownloadExpiredArtifact404(t *testing.T) {
	s, dir, clock := newTestStore(t)
	art := mustAdd(t, s, dir, "expired.zip", "old data", "application/zip")
	srv := newDownloadServer(t, s)
	clock.Advance(TTL + time.Millisecond)
	resp, body := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	assertArtifactNotFound(t, resp, body)
	if _, err := os.Stat(art.Path); err != nil {
		t.Fatalf("未调用 Cleanup 前文件应仍在磁盘: %v", err)
	}
}

func TestDownloadMissingFile404(t *testing.T) {
	s, dir, _ := newTestStore(t)
	art := mustAdd(t, s, dir, "gone.zip", "data", "application/zip")
	if err := os.Remove(art.Path); err != nil {
		t.Fatalf("删除文件失败: %v", err)
	}
	srv := newDownloadServer(t, s)
	resp, body := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	assertArtifactNotFound(t, resp, body)
}

func TestDownloadHead(t *testing.T) {
	s, dir, _ := newTestStore(t)
	content := "head-request-body"
	art := mustAdd(t, s, dir, "head.zip", content, "application/zip")
	srv := newDownloadServer(t, s)

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/api/artifacts/"+art.ID, nil)
	if err != nil {
		t.Fatalf("创建 HEAD 请求失败: %v", err)
	}
	resp, err := artifactClient().Do(req)
	if err != nil {
		t.Fatalf("HEAD 失败: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 HEAD 响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d，期望 200", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD 响应体 = %q，期望空", body)
	}
	if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(content)) {
		t.Fatalf("Content-Length = %q，期望 %d", cl, len(content))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

func TestDownloadSymlinkSwap404(t *testing.T) {
	s, dir, _ := newTestStore(t)
	art := mustAdd(t, s, dir, "swap.zip", "data", "application/zip")
	outside := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("写外部文件失败: %v", err)
	}
	if err := os.Remove(art.Path); err != nil {
		t.Fatalf("删除文件失败: %v", err)
	}
	if err := os.Symlink(outside, art.Path); err != nil {
		t.Skipf("当前平台无法创建符号链接: %v", err)
	}
	srv := newDownloadServer(t, s)
	resp, body := getArtifact(t, srv, "/api/artifacts/"+art.ID)
	assertArtifactNotFound(t, resp, body)
}
