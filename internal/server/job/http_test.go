// SPDX-License-Identifier: MIT

package job

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// apiClient is the small HTTP client used by the JSON route tests.
func apiClient() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

// doRequest performs a request and returns the response plus its body.
func doRequest(t *testing.T, method, rawURL string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := apiClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

// jobViewKeys is the complete JSON field set of a JobView.
var jobViewKeys = map[string]bool{
	"id": true, "type": true, "state": true, "phase": true, "message": true,
	"createdAt": true, "startedAt": true, "finishedAt": true, "lastSeq": true,
	"result": true, "error": true,
}

func TestListJobsEmptyIsJSONArray(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/api/jobs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
}

func TestListAndGetJobJSON(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	running := startBlockingJob(t, m, KindLaunchInstance)

	// GET /api/jobs
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/api/jobs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var list []View
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	if len(list) != 1 || list[0].ID != running.ID || list[0].State != StateRunning {
		t.Fatalf("list = %+v", list)
	}

	// GET /api/jobs/{id}
	resp, body = doRequest(t, http.MethodGet, srv.URL+"/api/jobs/"+running.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if len(raw) != len(jobViewKeys) {
		t.Fatalf("job JSON fields = %v, want %v", keys(raw), jobViewKeys)
	}
	for key := range jobViewKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("job JSON is missing %q: %s", key, body)
		}
	}
	var view View
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if view.State != StateRunning || view.Type != KindLaunchInstance {
		t.Fatalf("view = %+v", view)
	}
	if string(raw["finishedAt"]) != "null" || string(raw["result"]) != "null" || string(raw["error"]) != "null" {
		t.Fatalf("running job nullable fields = %s %s %s", raw["finishedAt"], raw["result"], raw["error"])
	}
	if string(raw["startedAt"]) == "null" {
		t.Fatalf("startedAt = null while running")
	}

	// Cancel and verify the finished view.
	if _, err := m.Cancel(running.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitState(t, m, running.ID, StateCancelled)
	waitIdle(t, m)
	resp, body = doRequest(t, http.MethodGet, srv.URL+"/api/jobs/"+running.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get finished status = %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode finished view: %v", err)
	}
	if view.State != StateCancelled || view.FinishedAt == nil {
		t.Fatalf("finished view = %+v", view)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// assertStrictErrorBody asserts the frozen M1 error body contract:
// {code,message,details,traceId} with all four fields always present, details
// always a JSON object (empty object when the error carries no details) and a
// traceId matching the X-Trace-Id response header when the middleware set one.
func assertStrictErrorBody(t *testing.T, resp *http.Response, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (body = %s)", resp.StatusCode, wantStatus, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	wantKeys := []string{"code", "message", "details", "traceId"}
	if len(raw) != len(wantKeys) {
		t.Fatalf("error body fields = %v, want exactly %v", keys(raw), wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("error body is missing %q: %s", key, body)
		}
	}
	var envelope struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
		TraceID string         `json:"traceId"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope %q: %v", body, err)
	}
	if envelope.Code != wantCode {
		t.Fatalf("error code = %q, want %q", envelope.Code, wantCode)
	}
	if envelope.Message == "" {
		t.Fatal("error message is empty")
	}
	if envelope.Details == nil {
		t.Fatalf("error details = null, want object: %s", body)
	}
	if header := resp.Header.Get("X-Trace-Id"); header != "" && envelope.TraceID != header {
		t.Fatalf("body traceId = %q, X-Trace-Id header = %q", envelope.TraceID, header)
	}
	if string(raw["details"]) != "{}" {
		t.Fatalf("details = %s, want {} for an error without structured details", raw["details"])
	}
}

func TestGetUnknownJobErrorJSON(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Trace-Id", "abc123def456")
			next.ServeHTTP(w, r)
		})
	})
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/api/jobs/j-000000000000")
	assertStrictErrorBody(t, resp, body, http.StatusNotFound, "NOT_FOUND")
	var envelope struct {
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode traceId: %v", err)
	}
	if envelope.TraceID != "abc123def456" {
		t.Fatalf("traceId = %q, want the X-Trace-Id header value", envelope.TraceID)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestCancelRoute(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	running := startBlockingJob(t, m, KindInstallRuntime)

	resp, body := doRequest(t, http.MethodPost, srv.URL+"/api/jobs/"+running.ID+"/cancel")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body = %s", resp.StatusCode, body)
	}
	var view View
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode cancel view: %v", err)
	}
	if view.ID != running.ID || view.State != StateRunning {
		t.Fatalf("cancel view = %+v", view)
	}

	waitState(t, m, running.ID, StateCancelled)
	waitIdle(t, m)

	resp, body = doRequest(t, http.MethodPost, srv.URL+"/api/jobs/"+running.ID+"/cancel")
	assertStrictErrorBody(t, resp, body, http.StatusConflict, "CONFLICT_JOB_FINISHED")

	resp, body = doRequest(t, http.MethodPost, srv.URL+"/api/jobs/j-000000000000/cancel")
	assertStrictErrorBody(t, resp, body, http.StatusNotFound, "NOT_FOUND")
}
