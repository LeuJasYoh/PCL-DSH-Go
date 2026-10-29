// SPDX-License-Identifier: MIT

package job

import (
	"encoding/json"
	"net/http"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
)

// Register mounts the M1 job routes on mux:
//
//	GET  /api/jobs              recent jobs, newest first
//	GET  /api/jobs/{id}         one job
//	GET  /api/jobs/{id}/events  SSE event stream
//	POST /api/jobs/{id}/cancel  request cancellation (202)
//
// Authentication, CSRF and trace/security headers are the API layer's
// responsibility; this package only implements the job contract and emits JSON
// error bodies in the shape of docs/IMPLEMENTATION_SPEC.md §4.4
// ({code,message,details,traceId}, all four fields always present, with details
// normalized to an empty object when the error carries none). The trace id is
// copied from the X-Trace-Id response header set by the API middleware.
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jobs", m.handleList)
	mux.HandleFunc("GET /api/jobs/{id}", m.handleGet)
	mux.HandleFunc("GET /api/jobs/{id}/events", m.handleEvents)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", m.handleCancel)
}

// handleList serves GET /api/jobs (200 JobView[]).
func (m *Manager) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.List())
}

// handleGet serves GET /api/jobs/{id} (200 JobView, 404 NOT_FOUND).
func (m *Manager) handleGet(w http.ResponseWriter, r *http.Request) {
	view, ok := m.Get(r.PathValue("id"))
	if !ok {
		m.writeError(w, r, apierr.NotFound("任务不存在。"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleCancel serves POST /api/jobs/{id}/cancel (202 JobView, 404, 409
// CONFLICT_JOB_FINISHED).
func (m *Manager) handleCancel(w http.ResponseWriter, r *http.Request) {
	view, err := m.Cancel(r.PathValue("id"))
	if err != nil {
		m.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// errorResponse is the JSON error body of the job API: all four documented
// fields are always present, details is an object (never null/omitted) and
// traceId is copied from the X-Trace-Id response header set by the API
// middleware.
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
	TraceID string `json:"traceId"`
}

// writeError maps an error to a JSON response. Unknown errors are reported as
// INTERNAL without echoing their text (it may contain internal paths or
// secrets); the original error is logged (redacted).
func (m *Manager) writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr, ok := apierr.As(err)
	if !ok {
		m.logger.Error("job api error",
			"method", r.Method,
			"path", r.URL.Path,
			"error", logging.Redact(err.Error()),
		)
		apiErr = apierr.InternalError()
	}
	writeJSON(w, apiErr.Status, errorResponse{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: normalizeDetails(apiErr.Details),
		TraceID: w.Header().Get("X-Trace-Id"),
	})
}

// writeJSON encodes payload as a JSON response with no-store semantics.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"code":"INTERNAL","message":"响应编码失败。"}`)
	}
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
