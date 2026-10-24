// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"net/http"
)

// APIError is an error that carries an HTTP status, a stable machine-readable
// code and a Chinese, user-facing message. The M0 debug API maps *APIError
// values to JSON error responses; any other error becomes INTERNAL.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewAPIError builds an *APIError.
func NewAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// AsAPIError extracts an *APIError from err. It reports whether err (or a
// wrapped error) is an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// M0 debug API error codes. They are intentionally a subset of the codes in
// docs/IMPLEMENTATION_SPEC.md §4.4; the M1 API replaces this package's routes.
const (
	CodeBadRequest         = "BAD_REQUEST"
	CodeValidation         = "VALIDATION"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeNotFound           = "NOT_FOUND"
	CodeConflictBusy       = "CONFLICT_BUSY"
	CodeConflictRunning    = "CONFLICT_RUNNING_INSTANCE"
	CodeConflictPortBusy   = "CONFLICT_PORT_BUSY"
	CodeInstanceNotReady   = "INSTANCE_NOT_READY"
	CodeUnsupportedRuntime = "UNSUPPORTED_RUNTIME"
	CodeInternal           = "INTERNAL"
)

// httpStatusForCode is used only by tests and documentation helpers; callers
// should set Status explicitly.
func httpStatusForCode(code string) int {
	switch code {
	case CodeBadRequest, CodeValidation, CodeUnsupportedRuntime:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflictBusy, CodeConflictRunning, CodeConflictPortBusy, CodeInstanceNotReady:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
