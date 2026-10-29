// SPDX-License-Identifier: MIT

package api

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
)

func TestLoginSuccessAndMe(t *testing.T) {
	env := newTestEnv(t, false)

	rec := env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("login body = %q, want empty", rec.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case security.SessionCookieName:
			sessionCookie = cookie
		case security.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("login cookies = %v, want JSESSIONID + XSRF-TOKEN", rec.Result().Cookies())
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("JSESSIONID must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("JSESSIONID SameSite = %v, want Lax", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/" {
		t.Fatalf("JSESSIONID Path = %q, want /", sessionCookie.Path)
	}
	if sessionCookie.Secure {
		t.Fatal("JSESSIONID must not be Secure over plain HTTP")
	}
	if len(sessionCookie.Value) != 32 {
		t.Fatalf("session id length = %d, want 32 hex characters", len(sessionCookie.Value))
	}
	if csrfCookie.HttpOnly {
		t.Fatal("XSRF-TOKEN must be readable by the SPA")
	}
	if len(csrfCookie.Value) != 32 {
		t.Fatalf("csrf token length = %d, want 32 hex characters", len(csrfCookie.Value))
	}

	// Anonymous /api/auth/me still issues a CSRF cookie and a token.
	anon := env.request(t, http.MethodGet, "/api/auth/me", nil)
	anonMe := decodeJSONResponse[meResponse](t, anon, http.StatusOK)
	if anonMe.Authenticated {
		t.Fatal("anonymous me reports authenticated=true")
	}
	if len(anonMe.CSRFToken) != 32 {
		t.Fatalf("anonymous csrfToken = %q, want 32 hex characters", anonMe.CSRFToken)
	}
	if anonMe.Version != testVersion {
		t.Fatalf("me version = %q, want %q", anonMe.Version, testVersion)
	}
	var anonCookie *http.Cookie
	for _, cookie := range anon.Result().Cookies() {
		if cookie.Name == security.CSRFCookieName {
			anonCookie = cookie
		}
	}
	if anonCookie == nil || anonCookie.Value != anonMe.CSRFToken {
		t.Fatalf("anonymous me did not refresh XSRF-TOKEN: %v", anon.Result().Cookies())
	}

	// Authenticated me reports the session-bound token.
	sess := session{sessionID: sessionCookie.Value, csrf: csrfCookie.Value}
	rec = env.request(t, http.MethodGet, "/api/auth/me", nil, sess.withSession())
	me := decodeJSONResponse[meResponse](t, rec, http.StatusOK)
	if !me.Authenticated {
		t.Fatal("me reports authenticated=false for a valid session")
	}
	if me.CSRFToken != sess.csrf {
		t.Fatalf("me csrfToken = %q, want %q", me.CSRFToken, sess.csrf)
	}
	if me.InitialPasswordPending {
		t.Fatal("me initialPasswordPending = true for a completed setup")
	}
}

func TestLoginWrongPasswordAndLockout(t *testing.T) {
	env := newTestEnv(t, false)
	for attempt := 1; attempt <= 4; attempt++ {
		rec := env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": "wrong-password"}))
		body := decodeError(t, rec, http.StatusUnauthorized, apierr.CodeLoginFailed)
		if len(body.Details) != 0 {
			t.Fatalf("LOGIN_FAILED details = %v, want {}", body.Details)
		}
	}
	rec := env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": "wrong-password"}))
	body := decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)
	retry, ok := body.Details["retryAfterSeconds"].(float64)
	if !ok || retry < 1 || retry > 60 {
		t.Fatalf("retryAfterSeconds = %#v, want 1-60", body.Details["retryAfterSeconds"])
	}

	// The correct password is rejected while the IP is locked.
	rec = env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}))
	decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)

	// Another client address is unaffected.
	rec = env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}),
		func(r *http.Request) { r.RemoteAddr = "10.9.8.7:5555" })
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login from another ip = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

func TestLoginInvalidatesPreviousSession(t *testing.T) {
	env := newTestEnv(t, false)
	first := env.loginAs(t)
	// Logging in again while presenting the old session must invalidate it
	// (session fixation defence).
	rec := env.request(t, http.MethodPost, "/api/auth/login",
		jsonBody(t, map[string]string{"password": testPassword}), first.cookiesOnly())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second login = %d, want 204", rec.Code)
	}
	second := session{}
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case security.SessionCookieName:
			second.sessionID = cookie.Value
		case security.CSRFCookieName:
			second.csrf = cookie.Value
		}
	}
	if second.sessionID == "" || second.csrf == "" {
		t.Fatalf("second login cookies = %v", rec.Result().Cookies())
	}

	rec = env.request(t, http.MethodGet, "/api/state", nil, first.withSession())
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodGet, "/api/state", nil, second.withSession())
	decodeJSONResponse[StateDTO](t, rec, http.StatusOK)
}

func TestLoginBackendErrorIsInternal(t *testing.T) {
	env := newTestEnv(t, false)
	env.creds.mu.Lock()
	env.creds.verifyErr = errTestBackend
	env.creds.mu.Unlock()
	rec := env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}))
	body := decodeError(t, rec, http.StatusInternalServerError, apierr.CodeInternal)
	if strings.Contains(body.Message, "backend") {
		t.Fatalf("500 message leaks internals: %q", body.Message)
	}
	if strings.Contains(env.logs.String(), testPassword) {
		t.Fatal("manager log leaked the submitted password")
	}
}

func TestCSRFDoubleSubmit(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	settingsBody := jsonBody(t, map[string]string{"proxy": "", "registry": "", "directAccessSshUser": "root"})

	// Missing header → 403 CSRF_INVALID.
	rec := env.request(t, http.MethodPut, "/api/settings", settingsBody, sess.cookiesOnly())
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Mismatched header → 403 CSRF_INVALID.
	rec = env.request(t, http.MethodPut, "/api/settings", settingsBody, sess.cookiesOnly(),
		func(r *http.Request) { r.Header.Set(security.CSRFHeaderName, security.NewCSRFToken()) })
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Missing cookie (header only) → 403 CSRF_INVALID.
	rec = env.request(t, http.MethodPut, "/api/settings", settingsBody,
		func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: security.SessionCookieName, Value: sess.sessionID})
			r.Header.Set(security.CSRFHeaderName, sess.csrf)
		})
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// Correct pair → 200.
	rec = env.request(t, http.MethodPut, "/api/settings", settingsBody, sess.withSession())
	settings := decodeJSONResponse[SettingsDTO](t, rec, http.StatusOK)
	if settings.DirectAccessSSHUser != "root" {
		t.Fatalf("settings ssh user = %q, want root", settings.DirectAccessSSHUser)
	}

	// Anonymous writes are 401, not 403 (authentication is checked first).
	rec = env.request(t, http.MethodPut, "/api/settings", settingsBody)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
}

func TestLogout(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodPost, "/api/auth/logout", nil, sess.withSession())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	cleared := map[string]*http.Cookie{}
	for _, cookie := range rec.Result().Cookies() {
		cleared[cookie.Name] = cookie
	}
	for _, name := range []string{security.SessionCookieName, security.CSRFCookieName} {
		cookie, ok := cleared[name]
		if !ok {
			t.Fatalf("logout did not clear %s: %v", name, rec.Result().Cookies())
		}
		if cookie.MaxAge >= 0 {
			t.Fatalf("logout %s MaxAge = %d, want negative", name, cookie.MaxAge)
		}
	}
	rec = env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)

	// Anonymous logout is a CSRF-free no-op 204 (frozen contract).
	rec = env.request(t, http.MethodPost, "/api/auth/logout", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("anonymous logout = %d, want 204", rec.Code)
	}
	// An unknown/expired session cookie is treated as anonymous: no CSRF needed.
	rec = env.request(t, http.MethodPost, "/api/auth/logout", nil, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: security.SessionCookieName, Value: strings.Repeat("0", 32)})
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stale-session logout = %d, want 204", rec.Code)
	}
}

func TestFirstLoginPasswordChangeGate(t *testing.T) {
	env := newTestEnv(t, true)
	sess := env.loginAs(t)

	// Everything except /api/auth/** and /api/health is blocked.
	rec := env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	body := decodeError(t, rec, http.StatusForbidden, apierr.CodePasswordChangeRequired)
	if endpoint, _ := body.Details["endpoint"].(string); endpoint != "/api/auth/password" {
		t.Fatalf("details.endpoint = %#v, want /api/auth/password", body.Details["endpoint"])
	}
	rec = env.request(t, http.MethodGet, "/api/instances", nil, sess.withSession())
	decodeError(t, rec, http.StatusForbidden, apierr.CodePasswordChangeRequired)
	rec = env.request(t, http.MethodGet, "/api/health", nil)
	decodeJSONResponse[healthResponse](t, rec, http.StatusOK)
	rec = env.request(t, http.MethodGet, "/api/auth/me", nil, sess.withSession())
	me := decodeJSONResponse[meResponse](t, rec, http.StatusOK)
	if !me.Authenticated || !me.InitialPasswordPending {
		t.Fatalf("me = %+v, want authenticated + pending", me)
	}

	// /workbench is gated too, and the proxy must not be reached.
	rec = env.request(t, http.MethodGet, "/workbench/i-aaaaaaaaaaaa/", nil, sess.withSession())
	decodeError(t, rec, http.StatusForbidden, apierr.CodePasswordChangeRequired)
	if hits := env.workbench.HitCount(); hits != 0 {
		t.Fatalf("workbench proxy reached %d times while pending", hits)
	}

	// Changing the password requires a session and the old password.
	rec = env.request(t, http.MethodPut, "/api/auth/password",
		jsonBody(t, map[string]string{"oldPassword": testPassword, "newPassword": "new-password-2"}))
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodPut, "/api/auth/password",
		jsonBody(t, map[string]string{"oldPassword": "wrong-old", "newPassword": "new-password-2"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	const newPassword = "new-password-2"
	rec = env.request(t, http.MethodPut, "/api/auth/password",
		jsonBody(t, map[string]string{"oldPassword": testPassword, "newPassword": newPassword}), sess.withSession())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	rec = env.request(t, http.MethodGet, "/api/auth/me", nil, sess.withSession())
	me = decodeJSONResponse[meResponse](t, rec, http.StatusOK)
	if me.InitialPasswordPending {
		t.Fatal("initialPasswordPending still true after changing the password")
	}
	rec = env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	decodeJSONResponse[StateDTO](t, rec, http.StatusOK)

	// The old password is gone; the new one logs in.
	rec = env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}))
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeLoginFailed)
	rec = env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": newPassword}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login with new password = %d, want 204", rec.Code)
	}
}

func TestWorkbenchRequiresSession(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/workbench/i-aaaaaaaaaaaa/", nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	if hits := env.workbench.HitCount(); hits != 0 {
		t.Fatalf("workbench proxy reached %d times anonymously", hits)
	}
	sess := env.loginAs(t)
	rec = env.request(t, http.MethodGet, "/workbench/i-aaaaaaaaaaaa/", nil, sess.withSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("workbench status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if hits := env.workbench.HitCount(); hits != 1 {
		t.Fatalf("workbench proxy hits = %d, want 1", hits)
	}
}

// errTestBackend simulates an unexpected credentials failure.
var errTestBackend = jsonError("credentials backend unavailable")

// jsonError is a minimal error type that is neither *apierr.Error nor a
// sentinel, used to check the 500 fallback.
type jsonError string

func (e jsonError) Error() string { return string(e) }

// TestMeCSRFTokenIsHex guards against regressions in the anonymous token.
func TestMeCSRFTokenIsHex(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/auth/me", nil)
	me := decodeJSONResponse[meResponse](t, rec, http.StatusOK)
	if _, err := hex.DecodeString(me.CSRFToken); err != nil {
		t.Fatalf("anonymous csrfToken = %q is not hex: %v", me.CSRFToken, err)
	}
}
func TestPasswordChangeInvalidatesOtherSessions(t *testing.T) {
	env := newTestEnv(t, true)
	first := env.loginAs(t)
	second := env.login(t, testPassword)

	rec := env.request(t, http.MethodPut, "/api/auth/password",
		jsonBody(t, map[string]string{"oldPassword": testPassword, "newPassword": "new-password-2"}), second.withSession())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	// Every other session is invalidated; the changing session stays valid.
	rec = env.request(t, http.MethodGet, "/api/state", nil, first.withSession())
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodGet, "/api/state", nil, second.withSession())
	decodeJSONResponse[StateDTO](t, rec, http.StatusOK)
}
