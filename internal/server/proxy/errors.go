// SPDX-License-Identifier: MIT

package proxy

import (
	"encoding/json"
	"net/http"
)

// traceIDHeader is set by the server middleware on every response; proxy error
// bodies echo it so a client can correlate the failure with the access log.
const traceIDHeader = "X-Trace-Id"

// Error codes returned by the /workbench proxy. They follow the frozen
// interface in docs/M0_INTERFACES.md §8, docs/IMPLEMENTATION_SPEC.md §4.4 and
// docs/M2_INTERFACES.md §3.3.
const (
	codeBadRequest       = "BAD_REQUEST"
	codeUnauthorized     = "UNAUTHORIZED"
	codeForbidden        = "FORBIDDEN"
	codeNotFound         = "NOT_FOUND"
	codeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	codeInstanceNotFound = "INSTANCE_NOT_FOUND"
	codeInstanceNotReady = "INSTANCE_NOT_READY"
	codeUpstreamError    = "UPSTREAM_ERROR"
	codeUpstreamTimeout  = "UPSTREAM_TIMEOUT"
	codeUpstreamRejected = "UPSTREAM_TARGET_REJECTED"
	codeInternal         = "INTERNAL"
)

// errorResponse is the unified JSON body for proxy failures (M2 hardening):
// {code,message,details,traceId}. Details is always an empty object so clients
// can rely on the shape; traceId comes from the X-Trace-Id response header set
// by the server middleware and is "" when the handler is used standalone.
type errorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	TraceID string         `json:"traceId"`
}

// writeJSONError writes the unified JSON error body. The caller must not have
// written a response before. HEAD responses never carry a body.
func writeJSONError(w http.ResponseWriter, method string, status int, code, message string) {
	traceID := w.Header().Get(traceIDHeader)
	header := w.Header()
	header.Del("Content-Length")
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(errorResponse{
		Code:    code,
		Message: message,
		Details: map[string]any{},
		TraceID: traceID,
	})
}

// writeRedirect writes a 302 response that is never cached: both the trailing
// slash redirect and the token bootstrap redirect carry query strings that
// must not be replayed from a cache.
func writeRedirect(w http.ResponseWriter, location string) {
	header := w.Header()
	header.Set("Location", location)
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusFound)
}
