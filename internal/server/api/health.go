// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// healthResponse is the anonymous GET /api/health body.
type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
}

// handleHealth serves the anonymous health endpoint.
func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:        "UP",
		Version:       a.cfg.Version,
		UptimeSeconds: int64(time.Since(a.startedAt) / time.Second),
	})
}

// handleRoot serves a tiny placeholder page until the Vue WebUI is wired in M4.
func (a *API) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	version := a.cfg.Version
	if version == "" {
		version = "dev"
	}
	page := "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">" +
		"<title>PCL-DSH-Go</title></head><body><h1>PCL-DSH-Go</h1>" +
		"<p>DeepSeek Harness 管理器已运行（版本 " + version + "）。</p>" +
		"<p>健康检查：<a href=\"/api/health\">/api/health</a></p></body></html>"
	_, _ = w.Write([]byte(page))
}

// unauthorizedError is the shared 401 UNAUTHORIZED error.
func unauthorizedError() error {
	return apierr.Unauthorized()
}
