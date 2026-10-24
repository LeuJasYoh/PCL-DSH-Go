// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// multipartUpload builds a multipart/form-data body with one file field.
func multipartUpload(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write file part error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart writer Close() error = %v", err)
	}
	return body, writer.FormDataContentType()
}

// uploadRequest builds a POST request carrying the multipart body.
func uploadRequest(body io.Reader, contentType string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/packs/preview", body)
	request.Header.Set("Content-Type", contentType)
	return request
}

// dirEntries returns the entries of dir (nil when the directory is missing).
func dirEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}
	return entries
}

// requireNoUploadLeftovers asserts the upload directory holds no temporary file.
func requireNoUploadLeftovers(t *testing.T, dir string) {
	t.Helper()
	if entries := dirEntries(t, dir); len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("upload dir left %v behind", names)
	}
}

func TestReceiveUploadStreamsToTempFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	content := bytes.Repeat([]byte("0123456789abcdef"), 640*1024) // 10 MiB
	body, contentType := multipartUpload(t, "file", "my pack.zip", content)

	upload, err := receiveUpload(context.Background(), uploadRequest(body, contentType), dir, maxPackUploadBytes)
	if err != nil {
		t.Fatalf("receiveUpload() error = %v", err)
	}
	defer func() {
		if removeErr := os.Remove(upload.Path); removeErr != nil {
			t.Errorf("remove upload: %v", removeErr)
		}
	}()

	if upload.Size != int64(len(content)) {
		t.Fatalf("upload size = %d, want %d", upload.Size, len(content))
	}
	if upload.Filename != "my pack.zip" {
		t.Fatalf("upload filename = %q, want the sanitized client name", upload.Filename)
	}
	if filepath.Dir(upload.Path) != dir {
		t.Fatalf("upload path %q is outside %q", upload.Path, dir)
	}
	base := filepath.Base(upload.Path)
	if !strings.HasPrefix(base, "upload-") || !strings.HasSuffix(base, ".pack") {
		t.Fatalf("upload path = %q, want upload-*.pack", base)
	}
	info, err := os.Stat(upload.Path)
	if err != nil {
		t.Fatalf("Stat(upload) error = %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("upload is not a regular file: %v", info.Mode())
	}
	if info.Size() != int64(len(content)) {
		t.Fatalf("file size = %d, want %d", info.Size(), len(content))
	}
	stored, err := os.ReadFile(upload.Path)
	if err != nil {
		t.Fatalf("ReadFile(upload) error = %v", err)
	}
	wantHash := sha256.Sum256(content)
	gotHash := sha256.Sum256(stored)
	if hex.EncodeToString(gotHash[:]) != hex.EncodeToString(wantHash[:]) {
		t.Fatal("uploaded content hash differs from the request body")
	}
	// The directory was created 0700 (mode asserted in the Linux-only test).
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("upload dir = %v (err %v)", info, err)
	}
}

func TestReceiveUploadSizeLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	const limit = int64(1024)
	content := bytes.Repeat([]byte("x"), 2048)
	body, contentType := multipartUpload(t, "file", "big.pack", content)

	_, err := receiveUpload(context.Background(), uploadRequest(body, contentType), dir, limit)
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("error = %v (%T), want *apierr.Error", err, err)
	}
	if apiErr.Status != http.StatusRequestEntityTooLarge || apiErr.Code != apierr.CodePayloadTooLarge {
		t.Fatalf("error = %s(%d), want PAYLOAD_TOO_LARGE(413)", apiErr.Code, apiErr.Status)
	}
	requireNoUploadLeftovers(t, dir)
}

func TestReceiveUploadRejectsMissingField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")

	// Multipart body without a "file" part.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("name", "x"); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := receiveUpload(context.Background(), uploadRequest(body, writer.FormDataContentType()), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
	requireNoUploadLeftovers(t, dir)

	// Empty multipart body (no parts at all).
	empty := &bytes.Buffer{}
	emptyWriter := multipart.NewWriter(empty)
	if err := emptyWriter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err = receiveUpload(context.Background(), uploadRequest(empty, emptyWriter.FormDataContentType()), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
	requireNoUploadLeftovers(t, dir)

	// A "file" form field without a filename is not a file upload.
	textField := &bytes.Buffer{}
	textWriter := multipart.NewWriter(textField)
	if err := textWriter.WriteField("file", "not-a-file"); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	if err := textWriter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err = receiveUpload(context.Background(), uploadRequest(textField, textWriter.FormDataContentType()), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
	requireNoUploadLeftovers(t, dir)

	// Not multipart at all.
	_, err = receiveUpload(context.Background(), uploadRequest(strings.NewReader("{}"), "application/json"), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
	requireNoUploadLeftovers(t, dir)

	// Missing body.
	_, err = receiveUpload(context.Background(), httptest.NewRequest(http.MethodPost, "/api/packs/preview", nil), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
}

func TestReceiveUploadRejectsMultipleFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for index := 0; index < 2; index++ {
		part, err := writer.CreateFormFile("file", "pack.zip")
		if err != nil {
			t.Fatalf("CreateFormFile() error = %v", err)
		}
		if _, err := part.Write([]byte("payload")); err != nil {
			t.Fatalf("write part error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := receiveUpload(context.Background(), uploadRequest(body, writer.FormDataContentType()), dir, maxPackUploadBytes)
	requireAPIStatus(t, err, http.StatusBadRequest, apierr.CodeBadRequest)
	requireNoUploadLeftovers(t, dir)
}

// cancellingReader cancels the context on its first Read.
type cancellingReader struct {
	reader io.Reader
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	r.once.Do(r.cancel)
	return r.reader.Read(p)
}

func TestReceiveUploadCancellationCleansUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	content := bytes.Repeat([]byte("x"), 1<<20)

	// Cancelled before the first read.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	body, contentType := multipartUpload(t, "file", "pack.zip", content)
	_, err := receiveUpload(cancelledCtx, uploadRequest(body, contentType), dir, maxPackUploadBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	requireNoUploadLeftovers(t, dir)

	// Cancelled mid-stream: the helper must stop copying and remove the file.
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	body, contentType = multipartUpload(t, "file", "pack.zip", content)
	request := uploadRequest(&cancellingReader{reader: body, cancel: streamCancel}, contentType)
	_, err = receiveUpload(streamCtx, request, dir, maxPackUploadBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-stream error = %v, want context.Canceled", err)
	}
	requireNoUploadLeftovers(t, dir)
}

func TestSanitizeUploadFilename(t *testing.T) {
	cases := map[string]string{
		"pack.zip":                "pack.zip",
		"  spaced.zip  ":          "spaced.zip",
		"../../etc/passwd":        "passwd",
		"dir\\nested\\\\pack.zip": "pack.zip",
		"C:\\Users\\evil\\p.zip":  "p.zip",
		"a\x00b\x1fc.zip":         "abc.zip",
		"":                        defaultUploadFilename,
		"   ":                     defaultUploadFilename,
		"..\\":                    defaultUploadFilename,
		strings.Repeat("x", 400):  strings.Repeat("x", maxUploadFilenameRunes),
	}
	for raw, want := range cases {
		if got := sanitizeUploadFilename(raw); got != want {
			t.Fatalf("sanitizeUploadFilename(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestReceiveUploadDoesNotTrustFilename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	body, contentType := multipartUpload(t, "file", "../../etc/passwd", []byte("payload"))
	upload, err := receiveUpload(context.Background(), uploadRequest(body, contentType), dir, maxPackUploadBytes)
	if err != nil {
		t.Fatalf("receiveUpload() error = %v", err)
	}
	defer func() { _ = os.Remove(upload.Path) }()
	if upload.Filename != "passwd" {
		t.Fatalf("filename = %q, want sanitized basename", upload.Filename)
	}
	if filepath.Dir(upload.Path) != dir {
		t.Fatalf("upload path %q escaped %q", upload.Path, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "etc", "passwd")); err == nil {
		t.Fatal("upload wrote outside the upload directory")
	}
}

func TestMultipartBodyExemptFromJSONLimit(t *testing.T) {
	env := newTestEnv(t, false)
	size := maxJSONBody + 4096
	payload := bytes.Repeat([]byte("x"), size)

	readAll := func(readErr *error, readBytes *int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := io.ReadAll(r.Body)
			*readErr = err
			*readBytes = len(data)
			w.WriteHeader(http.StatusOK)
		})
	}

	// Multipart uploads are not capped by the 1 MiB JSON limit.
	var multipartErr error
	var multipartBytes int
	multipartHandler := env.api.withBodyLimit(readAll(&multipartErr, &multipartBytes))
	request := uploadRequest(bytes.NewReader(payload), "multipart/form-data; boundary=abcdef")
	recorder := httptest.NewRecorder()
	multipartHandler.ServeHTTP(recorder, request)
	if multipartErr != nil {
		t.Fatalf("multipart body read error = %v", multipartErr)
	}
	if multipartBytes != size {
		t.Fatalf("multipart body read %d bytes, want %d", multipartBytes, size)
	}

	// JSON bodies keep the documented 1 MiB cap.
	var jsonErr error
	var jsonBytes int
	jsonHandler := env.api.withBodyLimit(readAll(&jsonErr, &jsonBytes))
	jsonRequest := httptest.NewRequest(http.MethodPost, "/api/instances", bytes.NewReader(payload))
	jsonRequest.Header.Set("Content-Type", "application/json")
	jsonRecorder := httptest.NewRecorder()
	jsonHandler.ServeHTTP(jsonRecorder, jsonRequest)
	if jsonErr == nil {
		t.Fatal("JSON body larger than 1 MiB was not rejected")
	}
	var maxErr *http.MaxBytesError
	if !errors.As(jsonErr, &maxErr) {
		t.Fatalf("JSON body error = %v, want *http.MaxBytesError", jsonErr)
	}
	if jsonBytes > maxJSONBody {
		t.Fatalf("JSON body read %d bytes, want <= %d", jsonBytes, maxJSONBody)
	}
}

// requireAPIStatus asserts err is an *apierr.Error with the given status/code.
func requireAPIStatus(t *testing.T, err error, status int, code string) {
	t.Helper()
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("error = %v (%T), want *apierr.Error", err, err)
	}
	if apiErr.Status != status || apiErr.Code != code {
		t.Fatalf("error = %s(%d), want %s(%d)", apiErr.Code, apiErr.Status, code, status)
	}
}
