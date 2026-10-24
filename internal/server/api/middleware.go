// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
)

// maxJSONBodyMethods are the methods whose request bodies are bounded by the
// 1 MiB JSON limit.
func methodHasJSONBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// withTracing assigns a trace id, sets the security response headers, recovers
// panics and writes the access log. Query strings and cookies are never logged
// (they may carry bootstrap tokens).
func (a *API) withTracing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		traceID := newTraceID()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		header := recorder.Header()
		header.Set("X-Trace-Id", traceID)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		if isWorkbenchPath(r.URL.Path) {
			// The proxied workbench keeps the upstream framing policy.
		} else {
			header.Set("X-Frame-Options", "DENY")
		}
		if isAPIPath(r.URL.Path) {
			// Sensitive API responses must never be cached.
			header.Set("Cache-Control", "no-store")
		}

		r = r.WithContext(contextWithTraceID(r.Context(), traceID))

		defer func() {
			a.logger.Info("request",
				"traceId", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"durationMs", time.Since(started).Milliseconds(),
			)
		}()
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}
			if panicValue == http.ErrAbortHandler {
				// Standard signal for an aborted handler (e.g. SSE client
				// disconnected mid-write): not an application failure.
				return
			}
			a.logger.Error("request panic", "traceId", traceID, "panic", fmt.Sprint(panicValue))
			if !recorder.wroteHeader {
				a.writeError(recorder, r, apierr.InternalError())
			}
		}()

		next.ServeHTTP(recorder, r)
	})
}

// withBodyLimit bounds JSON request bodies to maxJSONBody.
func (a *API) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && isAPIPath(r.URL.Path) && methodHasJSONBody(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		}
		next.ServeHTTP(w, r)
	})
}

// withSession resolves the JSESSIONID session (sliding its expiry) and stores
// it on the request context. Anonymous requests continue with no session.
func (a *API) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session, ok := a.security.Authenticate(r); ok {
			r = r.WithContext(contextWithSession(r.Context(), session))
		}
		next.ServeHTTP(w, r)
	})
}

// withGuards enforces authentication, the first-login password-change gate,
// CSRF double-submit validation and the global busy rule
// (docs/M1_INTERFACES.md 10, docs/IMPLEMENTATION_SPEC.md 4.1/5).
func (a *API) withGuards(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		_, authenticated := sessionFrom(r)
		switch {
		case isAPIPath(path):
			// 1. Session requirement (public routes and logout are exempt).
			if !isPublicRoute(r.Method, path) && !isLogoutRoute(r.Method, path) && !authenticated {
				a.writeError(w, r, apierr.Unauthorized())
				return
			}
			// 2. First login: everything except /api/auth/** and /api/health is
			// blocked until the initial password has been changed.
			if !isAuthPath(path) && path != "/api/health" && a.security.InitialPasswordPending() {
				a.writeError(w, r, passwordChangeRequiredError())
				return
			}
			// 3. CSRF double submit for every non-GET write except login.
			// Logout is special: without a valid session it is a no-op 204, so
			// it does not require CSRF (frozen API contract).
			if methodHasJSONBody(r.Method) && path != "/api/auth/login" {
				skipCSRF := isLogoutRoute(r.Method, path) && !authenticated
				if !skipCSRF && !a.security.ValidateCSRF(r) {
					a.writeError(w, r, csrfInvalidError())
					return
				}
			}
			// 4. While a mutating job runs, other writes are rejected. Auth
			// endpoints (login/logout/password) and job cancellation stay
			// usable; see docs/IMPLEMENTATION_SPEC.md 4.5.
			if methodHasJSONBody(r.Method) && !isAuthPath(path) && !isJobCancelRoute(r.Method, path) && a.jobs.Busy() {
				a.writeError(w, r, apierr.Busy())
				return
			}
		case isWorkbenchPath(path):
			if !authenticated {
				a.writeError(w, r, apierr.Unauthorized())
				return
			}
			if a.security.InitialPasswordPending() {
				a.writeError(w, r, passwordChangeRequiredError())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sessionContextKey is the request context key of the authenticated session.
type sessionContextKey struct{}

// contextWithSession stores the authenticated session on ctx.
func contextWithSession(ctx context.Context, session *security.Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, session)
}

// contextWithTraceID stores the trace id on ctx.
func contextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// sessionFrom returns the authenticated session of r, if any.
func sessionFrom(r *http.Request) (*security.Session, bool) {
	if r == nil {
		return nil, false
	}
	session, ok := r.Context().Value(sessionContextKey{}).(*security.Session)
	return session, ok && session != nil
}

// SessionFromContext exposes the authenticated session to the /workbench
// proxy authorizer built by internal/server (defense in depth: the API guards
// have already enforced login and the first-login gate).
func SessionFromContext(r *http.Request) (*security.Session, bool) {
	return sessionFrom(r)
}

// isPublicRoute reports the anonymous API routes
// (docs/IMPLEMENTATION_SPEC.md 4.2).
func isPublicRoute(method, path string) bool {
	isRead := method == http.MethodGet || method == http.MethodHead
	switch {
	case isRead && path == "/api/health":
		return true
	case isRead && path == "/api/auth/me":
		return true
	case method == http.MethodPost && path == "/api/auth/login":
		return true
	default:
		return false
	}
}

// isLogoutRoute reports POST /api/auth/logout.
func isLogoutRoute(method, path string) bool {
	return method == http.MethodPost && path == "/api/auth/logout"
}

// isAuthPath reports whether path is under /api/auth/.
func isAuthPath(path string) bool {
	return strings.HasPrefix(path, "/api/auth/")
}

// isJobCancelRoute reports POST /api/jobs/{id}/cancel.
func isJobCancelRoute(method, path string) bool {
	return method == http.MethodPost &&
		strings.HasPrefix(path, "/api/jobs/") &&
		strings.HasSuffix(path, "/cancel")
}

// passwordChangeRequiredError is the documented 403 first-login gate error.
func passwordChangeRequiredError() *apierr.Error {
	return apierr.New(http.StatusForbidden, apierr.CodePasswordChangeRequired,
		"首次登录请先修改初始密码。").
		WithDetails(map[string]any{"endpoint": "/api/auth/password"})
}

// csrfInvalidError is the documented 403 CSRF failure.
func csrfInvalidError() *apierr.Error {
	return apierr.New(http.StatusForbidden, apierr.CodeCSRFInvalid,
		"CSRF 校验失败，请刷新页面后重试。")
}
