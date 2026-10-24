// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
)

// loginRequest is the POST /api/auth/login body.
type loginRequest struct {
	Password string `json:"password"`
}

// changePasswordRequest is the PUT /api/auth/password body.
type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// meResponse is the GET /api/auth/me body.
type meResponse struct {
	Authenticated          bool   `json:"authenticated"`
	CSRFToken              string `json:"csrfToken"`
	Version                string `json:"version"`
	InitialPasswordPending bool   `json:"initialPasswordPending"`
}

// handleLogin verifies the password, invalidates any previous session presented
// by the client (session fixation defence) and issues the session + CSRF
// cookies. It is the only write exempt from CSRF validation.
func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	if previous, ok := sessionFrom(r); ok {
		a.security.Logout(previous.ID)
	}
	result, err := a.security.Login(request.Password, clientIP(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	ttl := a.security.SessionTTL()
	security.SetSessionCookie(w, result.SessionID, ttl)
	security.SetCSRFCookie(w, result.CSRFToken, ttl)
	writeNoContent(w)
}

// handleLogout invalidates the current session (anonymous requests are a
// successful no-op) and clears both cookies.
func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if session, ok := sessionFrom(r); ok {
		a.security.Logout(session.ID)
	}
	security.ClearSessionCookie(w)
	security.ClearCSRFCookie(w)
	writeNoContent(w)
}

// handleMe reports the authentication state. The XSRF-TOKEN cookie is always
// (re)issued: the session-bound token when logged in, a fresh token otherwise,
// so a login page can send the double-submit pair as soon as it renders.
func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	response := meResponse{
		Version:                a.cfg.Version,
		InitialPasswordPending: a.security.InitialPasswordPending(),
	}
	if session, ok := sessionFrom(r); ok {
		response.Authenticated = true
		response.CSRFToken = a.security.CSRFToken(session)
	} else {
		response.CSRFToken = security.NewCSRFToken()
	}
	security.SetCSRFCookie(w, response.CSRFToken, a.security.SessionTTL())
	writeJSON(w, http.StatusOK, response)
}

// handleChangePassword changes the administrator password. The current session
// stays valid; the next request sees initialPasswordPending=false.
func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r)
	if !ok {
		a.writeError(w, r, unauthorizedError())
		return
	}
	var request changePasswordRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	if err := a.security.ChangePassword(session.ID, request.OldPassword, request.NewPassword); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeNoContent(w)
}
