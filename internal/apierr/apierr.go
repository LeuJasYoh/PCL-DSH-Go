// SPDX-License-Identifier: MIT

// Package apierr defines the HTTP error model shared by the API, the
// orchestrator and the job manager: an HTTP status plus a stable machine code,
// a Chinese user-facing message and optional structured details.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an application error that maps directly to an HTTP JSON response
// following docs/IMPLEMENTATION_SPEC.md 4.4.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s(%d): %s", e.Code, e.Status, e.Message)
}

// WithDetails returns a copy of e carrying details (safe for shared sentinels).
func (e *Error) WithDetails(details any) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Details = details
	return &clone
}

// New builds an *Error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Newf builds an *Error with a formatted message.
func Newf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// As extracts an *Error from err (including wrapped errors).
func As(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// From returns err as an *Error, falling back to a generic INTERNAL error for
// unknown failures. The caller is responsible for logging the original error.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	if apiErr, ok := As(err); ok {
		return apiErr
	}
	return New(http.StatusInternalServerError, CodeInternal, "服务器内部错误。")
}

// InternalError is the generic fallback error for unexpected failures.
func InternalError() *Error {
	return New(http.StatusInternalServerError, CodeInternal, "服务器内部错误。")
}

// Error codes (docs/IMPLEMENTATION_SPEC.md 4.4).
const (
	CodeUnauthorized              = "UNAUTHORIZED"
	CodeLoginFailed               = "LOGIN_FAILED"
	CodeLoginLocked               = "LOGIN_LOCKED"
	CodeForbidden                 = "FORBIDDEN"
	CodeCSRFInvalid               = "CSRF_INVALID"
	CodePasswordChangeRequired    = "PASSWORD_CHANGE_REQUIRED"
	CodeBadRequest                = "BAD_REQUEST"
	CodeValidation                = "VALIDATION"
	CodeMethodNotAllowed          = "METHOD_NOT_ALLOWED"
	CodeNotFound                  = "NOT_FOUND"
	CodeConflictBusy              = "CONFLICT_BUSY"
	CodeConflictInstanceRunning   = "CONFLICT_INSTANCE_RUNNING"
	CodeConflictRunningInstance   = "CONFLICT_RUNNING_INSTANCE"
	CodeConflictPortBusy          = "CONFLICT_PORT_BUSY"
	CodeConflictInUse             = "CONFLICT_IN_USE"
	CodeConflictUnsupportedChange = "CONFLICT_UNSUPPORTED_CHANGE"
	CodeConflictJobFinished       = "CONFLICT_JOB_FINISHED"
	CodeInstanceNotReady          = "INSTANCE_NOT_READY"
	CodePayloadTooLarge           = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedRuntime        = "UNSUPPORTED_RUNTIME"
	CodeInternal                  = "INTERNAL"
	CodeUpstreamError             = "UPSTREAM_ERROR"
	CodeUpstreamTargetRejected    = "UPSTREAM_TARGET_REJECTED"
	CodeUpstreamTimeout           = "UPSTREAM_TIMEOUT"
	CodeTimeout                   = "TIMEOUT"
	CodeInterrupted               = "INTERRUPTED"
)

// Convenience constructors used by multiple packages.

// Unauthorized reports a missing or expired session.
func Unauthorized() *Error {
	return New(http.StatusUnauthorized, CodeUnauthorized, "未登录或会话已过期。")
}

// Forbidden reports a permission failure.
func Forbidden() *Error {
	return New(http.StatusForbidden, CodeForbidden, "没有访问权限。")
}

// NotFound reports a missing resource.
func NotFound(message string) *Error {
	return New(http.StatusNotFound, CodeNotFound, message)
}

// Validation reports invalid input.
func Validation(message string) *Error {
	return New(http.StatusBadRequest, CodeValidation, message)
}

// Busy reports that another mutating task is running.
func Busy() *Error {
	return New(http.StatusConflict, CodeConflictBusy, "已有任务正在进行中，请稍后再试。")
}
