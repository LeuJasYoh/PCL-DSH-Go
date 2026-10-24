// SPDX-License-Identifier: GPL-3.0-or-later

package api

import "net/http"

// handleState serves GET /api/state: the aggregate snapshot the SPA polls
// (docs/IMPLEMENTATION_SPEC.md 4.3).
func (a *API) handleState(w http.ResponseWriter, _ *http.Request) {
	available := a.app.CachedAvailableVersions()
	if available == nil {
		available = []string{}
	}
	var currentInstanceID *string
	if selected := a.store.SelectedInstanceID(); selected != "" {
		id := selected
		currentInstanceID = &id
	}
	writeJSON(w, http.StatusOK, StateDTO{
		Version:           a.cfg.Version,
		CurrentInstanceID: currentInstanceID,
		Instances:         a.instanceDTOs(a.store.ListInstances()),
		Runtimes:          runtimeDTOs(a.app.Runtimes()),
		Status:            a.statusDTO(),
		Settings:          settingsDTO(a.store.Settings()),
		AvailableVersions: available,
		Warnings:          []string{},
	})
}
