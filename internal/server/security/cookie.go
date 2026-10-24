// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"crypto/subtle"
	"net/http"
	"time"
)

// Cookie and header names of the session/CSRF protocol
// (docs/IMPLEMENTATION_SPEC.md 4.1).
const (
	// SessionCookieName carries the opaque session id.
	SessionCookieName = "JSESSIONID"
	// CSRFCookieName is the readable double-submit CSRF cookie.
	CSRFCookieName = "XSRF-TOKEN"
	// CSRFHeaderName is the request header that must echo CSRFCookieName.
	CSRFHeaderName = "X-XSRF-TOKEN"
)

// NewCSRFToken returns a fresh CSRF token: 32 lowercase hex characters
// (16 random bytes from crypto/rand).
func NewCSRFToken() string {
	token, err := randomHex(16)
	if err != nil {
		// crypto/rand never fails on supported platforms; if it somehow does,
		// there is no safe fallback for a security token.
		panic("security: 无法生成 CSRF token: " + err.Error())
	}
	return token
}

// SetSessionCookie writes the HttpOnly session cookie. Secure is deliberately
// left off because the manager is served over plain HTTP on loopback in this
// milestone (docs/IMPLEMENTATION_SPEC.md 4.1).
func SetSessionCookie(w http.ResponseWriter, sessionID string, ttl time.Duration) {
	if w == nil || sessionID == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge(ttl),
	})
}

// ClearSessionCookie asks the browser to delete the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}

// SetCSRFCookie writes the readable CSRF cookie that the SPA echoes back in
// the X-XSRF-TOKEN header (double-submit pattern).
func SetCSRFCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	if w == nil || token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge(ttl),
	})
}

// ClearCSRFCookie removes the CSRF cookie on logout.
func ClearCSRFCookie(w http.ResponseWriter) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}

// ValidateCSRF verifies the double-submit pair: the XSRF-TOKEN cookie must be
// present and equal (in constant time) to the X-XSRF-TOKEN header.
func (m *Manager) ValidateCSRF(r *http.Request) bool {
	if r == nil {
		return false
	}
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFHeaderName)
	if header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}

// CSRFToken returns the CSRF token bound to session ("" for nil sessions).
func (m *Manager) CSRFToken(session *Session) string {
	if session == nil {
		return ""
	}
	return session.CSRF
}

// cookieMaxAge converts a TTL into the integer seconds http.Cookie expects.
func cookieMaxAge(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	seconds := int(ttl / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}
