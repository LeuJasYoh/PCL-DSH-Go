// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

// requestPackUpload sends a multipart preview request carrying one file part.
func (e *testEnv) requestPackUpload(t *testing.T, sess session, target, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartUpload(t, "file", filename, content)
	return e.request(t, http.MethodPost, target, body.Bytes(), sess.withSession(),
		func(r *http.Request) { r.Header.Set("Content-Type", contentType) })
}

func TestPackPreview(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	content := []byte("dspack-zip-bytes")

	rec := env.requestPackUpload(t, sess, "/api/packs/preview", "my-pack.zip", content)
	preview := decodeJSONResponse[packPreviewDTO](t, rec, http.StatusOK)
	if preview.PreviewID != "0123456789ab" {
		t.Fatalf("previewId = %q", preview.PreviewID)
	}
	if preview.Name != "test-pack" || preview.Format != "dspack" {
		t.Fatalf("preview name/format = %q/%q", preview.Name, preview.Format)
	}
	if preview.Version != "1.0.0" || preview.DSHVersion != "0.2.1-rc.3" {
		t.Fatalf("preview version/dshVersion = %q/%q", preview.Version, preview.DSHVersion)
	}
	if len(preview.Profiles) != 1 || preview.Profiles[0] != "web" {
		t.Fatalf("preview profiles = %v", preview.Profiles)
	}
	if preview.UnpackedBytes != int64(len(content)) {
		t.Fatalf("preview unpackedBytes = %d, want %d", preview.UnpackedBytes, len(content))
	}
	if preview.Warnings == nil {
		t.Fatal("preview warnings must be an array, got null")
	}
	env.pack.mu.Lock()
	filename := env.pack.lastFilename
	calls := env.pack.previewCalls
	env.pack.mu.Unlock()
	if filename != "my-pack.zip" {
		t.Fatalf("pack.Preview filename = %q, want the sanitized upload name", filename)
	}
	if calls != 1 {
		t.Fatalf("pack.Preview calls = %d, want 1", calls)
	}

	// The temporary upload file is removed once pack.Preview has consumed it.
	uploadsDir := filepath.Join(env.dirs.CacheDir, "uploads")
	entries, err := os.ReadDir(uploadsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(uploads) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("uploads dir left %d temporary files behind", len(entries))
	}
}

func TestPackPreviewErrors(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Anonymous: 401 before any body handling.
	body, contentType := multipartUpload(t, "file", "pack.zip", []byte("x"))
	rec := env.request(t, http.MethodPost, "/api/packs/preview", body.Bytes(),
		func(r *http.Request) { r.Header.Set("Content-Type", contentType) })
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)

	// Missing CSRF: 403.
	rec = env.request(t, http.MethodPost, "/api/packs/preview", body.Bytes(), sess.cookiesOnly(),
		func(r *http.Request) { r.Header.Set("Content-Type", contentType) })
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Multipart body without a file part: 400.
	emptyForm := &bytes.Buffer{}
	writer := multipart.NewWriter(emptyForm)
	if err := writer.WriteField("name", "x"); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	rec = env.request(t, http.MethodPost, "/api/packs/preview", emptyForm.Bytes(), sess.withSession(),
		func(r *http.Request) { r.Header.Set("Content-Type", writer.FormDataContentType()) })
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)

	// Non-multipart body: 400.
	rec = env.request(t, http.MethodPost, "/api/packs/preview", []byte("{}"), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)

	// A pack-level validation error passes through unchanged.
	env.pack.mu.Lock()
	env.pack.previewErr = apierr.New(http.StatusBadRequest, apierr.CodeValidation, "不支持的整合包格式。")
	env.pack.mu.Unlock()
	rec = env.requestPackUpload(t, sess, "/api/packs/preview", "bad.zip", []byte("not-a-pack"))
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Busy gate: preview does not start a job but is rejected while one runs.
	env.pack.mu.Lock()
	env.pack.previewErr = nil
	env.pack.mu.Unlock()
	_, stop := env.startBlockingJob(t)
	defer stop()
	rec = env.requestPackUpload(t, sess, "/api/packs/preview", "pack.zip", []byte("x"))
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
}

func TestPackImportJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{
		"previewId": "0123456789ab",
		"name":      "导入实例",
		"version":   "0.2.1-rc.3",
	}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindImportPack {
		t.Fatalf("job type = %q, want IMPORT_PACK", view.Type)
	}
	if view.State != job.StateRunning {
		t.Fatalf("job state = %q, want RUNNING", view.State)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("job result = %#v, want object", finished.Result)
	}
	if result["instanceId"] != "i-0123456789ab" {
		t.Fatalf("job result instanceId = %v", result["instanceId"])
	}
	if result["name"] != "导入实例" {
		t.Fatalf("job result name = %v", result["name"])
	}
	env.pack.mu.Lock()
	last := env.pack.lastImport
	env.pack.mu.Unlock()
	if last.PreviewID != "0123456789ab" || last.Name != "导入实例" || last.Version != "0.2.1-rc.3" {
		t.Fatalf("pack.Import input = %+v", last)
	}

	// The SSE stream ends with the result event carrying the import result.
	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID+"/events", nil, sess.withSession())
	events := parseSSE(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("no SSE events for the import job")
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Name != "result" {
		t.Fatalf("last event = %+v, want result", lastEvent)
	}
	payload := decodeEventData(t, lastEvent)
	eventResult, ok := payload["result"].(map[string]any)
	if !ok || eventResult["instanceId"] != "i-0123456789ab" {
		t.Fatalf("result event data = %v", payload)
	}
}

func TestPackImportValidationAndFailure(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Missing previewId → 400 VALIDATION (sync).
	rec := env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Anonymous / missing CSRF.
	rec = env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{"previewId": "0123456789ab"}))
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{"previewId": "0123456789ab"}), sess.cookiesOnly())
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Busy → 409.
	_, stop := env.startBlockingJob(t)
	rec = env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{"previewId": "0123456789ab"}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	stop()

	// Pack-level failure (unknown/expired preview) becomes a failed job error.
	env.pack.mu.Lock()
	env.pack.importErr = apierr.NotFound("预览不存在或已过期。")
	env.pack.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/packs/import", jsonBody(t, map[string]any{"previewId": "0123456789ab"}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeNotFound {
		t.Fatalf("job error = %+v, want NOT_FOUND", finished.Error)
	}
}

func TestPackExportJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "导出实例", freePort(t))

	rec := env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "profile",
	}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindExportPack {
		t.Fatalf("job type = %q, want EXPORT_PACK", view.Type)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("job result = %#v, want object", finished.Result)
	}
	if result["artifactId"] != "0123456789ab" {
		t.Fatalf("job result artifactId = %v", result["artifactId"])
	}
	if result["filename"] != "export.dspack" {
		t.Fatalf("job result filename = %v", result["filename"])
	}
	if size, ok := result["size"].(float64); !ok || size != 128 {
		t.Fatalf("job result size = %v, want 128", result["size"])
	}
	env.pack.mu.Lock()
	last := env.pack.lastExport
	env.pack.mu.Unlock()
	if last.InstanceID != instance.ID || last.Mode != "profile" {
		t.Fatalf("pack.Export input = %+v", last)
	}

	// home mode is accepted too.
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "home",
	}), sess.withSession())
	homeView := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, homeView.ID, job.StateSucceeded)
	env.pack.mu.Lock()
	last = env.pack.lastExport
	env.pack.mu.Unlock()
	if last.Mode != "home" {
		t.Fatalf("pack.Export mode = %q, want home", last.Mode)
	}
}

func TestPackExportValidationAndFailure(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "导出校验实例", freePort(t))

	// Invalid mode → 400 VALIDATION.
	rec := env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "everything",
	}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Missing instanceId → 400 VALIDATION; unknown instance → 404 NOT_FOUND.
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{"mode": "profile"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": "i-000000000000",
		"mode":       "profile",
	}), sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)

	// Anonymous / missing CSRF.
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "profile",
	}))
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "profile",
	}), sess.cookiesOnly())
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Busy → 409.
	_, stop := env.startBlockingJob(t)
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "profile",
	}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	stop()

	// Export failure becomes a failed job error.
	env.pack.mu.Lock()
	env.pack.exportErr = apierr.New(http.StatusBadRequest, apierr.CodeValidation, "实例没有可导出的 profile。")
	env.pack.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/packs/export", jsonBody(t, map[string]any{
		"instanceId": instance.ID,
		"mode":       "profile",
	}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeValidation {
		t.Fatalf("job error = %+v, want VALIDATION", finished.Error)
	}
}
