// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"strconv"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// logsTailDefault matches logging.TailLatest: 800 lines when unspecified.
const logsTailDefault = 800

// logsTailMax is the documented upper bound of ?lines=.
const logsTailMax = 2000

// settingsRequest is the PUT /api/settings body (same shape as the response).
type settingsRequest struct {
	Proxy               string `json:"proxy"`
	Registry            string `json:"registry"`
	DirectAccessSSHUser string `json:"directAccessSshUser"`
}

// logsTailResponse is the GET /api/logs/tail body.
type logsTailResponse struct {
	Lines []string `json:"lines"`
}

// handleGetSettings serves GET /api/settings.
func (a *API) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, settingsDTO(a.store.Settings()))
}

// handleUpdateSettings serves PUT /api/settings: validation and persistence are
// delegated to the app layer (400 VALIDATION on invalid values).
func (a *API) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var request settingsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	err := a.app.UpdateSettings(r.Context(), store.Settings{
		Proxy:               request.Proxy,
		Registry:            request.Registry,
		DirectAccessSSHUser: request.DirectAccessSSHUser,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsDTO(a.store.Settings()))
}

// handleLogsTail serves GET /api/logs/tail?lines=800. Invalid bounds are a
// documented 400 VALIDATION (never silently clamped).
func (a *API) handleLogsTail(w http.ResponseWriter, r *http.Request) {
	lines := logsTailDefault
	if raw := r.URL.Query().Get("lines"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > logsTailMax {
			a.writeError(w, r, apierr.New(http.StatusBadRequest, apierr.CodeValidation,
				"lines 必须是 1-2000 之间的整数。"))
			return
		}
		lines = parsed
	}
	tail, err := a.app.LogsTail(lines)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if tail == nil {
		tail = []string{}
	}
	writeJSON(w, http.StatusOK, logsTailResponse{Lines: tail})
}
