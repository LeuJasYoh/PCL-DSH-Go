// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/directaccess"
)

// handleDirectAccess serves GET /api/instances/{id}/direct-access. It always
// answers 200 once the instance exists: ready=false carries the Chinese reason
// and a copyable SSH tunnel command. The bootstrap token only ever appears in
// the in-memory response body (never in logs, Job results or the audit trail).
func (a *API) handleDirectAccess(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	live, _ := a.app.LiveInstance(instance.ID)
	statusNote := ""
	if !live.Ready {
		statusNote = "实例未运行，请先启动实例。"
		if live.StatusMessage != "" {
			statusNote = live.StatusMessage
		}
	}
	settings := a.store.Settings()
	dto, err := directaccess.Build(directaccess.Input{
		// Only the request Host header is used; X-Forwarded-Host is ignored in
		// M1 (frozen API contract).
		Host:       r.Host,
		SSHUser:    settings.DirectAccessSSHUser,
		Port:       instance.Port,
		Ready:      live.Ready,
		StatusNote: statusNote,
		Bootstrap:  live.Bootstrap,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	// Extra safety: the body contains the one-time token.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dto)
}
