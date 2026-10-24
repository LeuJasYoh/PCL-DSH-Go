// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
)

// maxJSONBody is the request body limit for JSON API calls
// (docs/IMPLEMENTATION_SPEC.md 4.1: JSON 1 MiB).
const maxJSONBody = 1 << 20

// traceIDKey is the request context key of the X-Trace-Id value.
type traceIDKey struct{}

// traceIDFrom returns the trace id of r ("" when the middleware was skipped,
// which only happens in unit tests that call a handler directly).
func traceIDFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value, ok := r.Context().Value(traceIDKey{}).(string); ok {
		return value
	}
	return ""
}

// errorBody is the documented JSON error body
// (docs/IMPLEMENTATION_SPEC.md 4.4): {code,message,details,traceId}.
// details is always present (an empty object when the error carries none).
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
	TraceID string `json:"traceId"`
}

// writeError maps err to a JSON error response. Unknown errors become INTERNAL
// without echoing their text (it may contain internal paths or secrets); the
// original error is logged redacted.
func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr, ok := apierr.As(err)
	if !ok {
		method, path := "", ""
		if r != nil {
			method, path = r.Method, r.URL.Path
		}
		a.logger.Error("api request failed",
			"traceId", traceIDFrom(r),
			"method", method,
			"path", path,
			"error", logging.Redact(err.Error()),
		)
		apiErr = apierr.InternalError()
	}
	details := apiErr.Details
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, apiErr.Status, errorBody{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: details,
		TraceID: traceIDFrom(r),
	})
}

// writeJSON encodes payload as a JSON response with no-store semantics.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte("{\"code\":\"INTERNAL\",\"message\":\"响应编码失败。\"}")
	}
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeNoContent sends a 204 response with no body.
func writeNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON decodes a bounded JSON request body into target. Every failure is
// an *apierr.Error: 400 BAD_REQUEST for malformed input, 413 PAYLOAD_TOO_LARGE
// when the 1 MiB limit is exceeded.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r == nil || r.Body == nil {
		return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体不能为空。")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			return apierr.New(http.StatusRequestEntityTooLarge, apierr.CodePayloadTooLarge, "请求体超过 1 MiB 上限。")
		case errors.Is(err, io.EOF):
			return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体不能为空。")
		case isUnknownFieldError(err):
			return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体包含未知字段。")
		default:
			return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体不是合法 JSON。")
		}
	}
	if decoder.More() {
		return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求体包含多余内容。")
	}
	return nil
}

// isUnknownFieldError reports whether err comes from DisallowUnknownFields.
// encoding/json exposes no typed error for this case, so the stable message
// prefix is matched instead.
func isUnknownFieldError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "json: unknown field ")
}

// newTraceID returns a 12 hex character trace id.
func newTraceID() string {
	var raw [6]byte
	if _, err := cryptorandRead(raw[:]); err != nil {
		return "000000000000"
	}
	return fmt.Sprintf("%02x%02x%02x%02x%02x%02x", raw[0], raw[1], raw[2], raw[3], raw[4], raw[5])
}

// statusRecorder captures the response status for access logging and forwards
// Flush/Hijack so SSE and WebSocket upgrades keep working.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// clientIP returns the client address of r for login rate limiting and audit
// events. Proxies are not trusted in M1 (loopback deployment), so RemoteAddr is
// used as-is; the security manager normalizes "ip:port" values.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.RemoteAddr
}
