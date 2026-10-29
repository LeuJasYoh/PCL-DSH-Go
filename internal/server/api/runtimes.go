// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

// installRuntimeRequest is the POST /api/runtimes body.
type installRuntimeRequest struct {
	Version string `json:"version"`
}

// availableVersionsResponse is the GET /api/runtimes/available body.
type availableVersionsResponse struct {
	Versions []string `json:"versions"`
}

// installJobResult is the INSTALL_RUNTIME job result.
type installJobResult struct {
	Version       string `json:"version"`
	Installed     bool   `json:"installed"`
	Compatibility string `json:"compatibility"`
}

// probeJobResult is the PROBE_RUNTIME job result.
type probeJobResult struct {
	Version       string `json:"version"`
	Compatibility string `json:"compatibility"`
}

// invalidVersionError is the shared 400 for malformed SemVer input.
func invalidVersionError() *apierr.Error {
	return apierr.New(http.StatusBadRequest, apierr.CodeValidation,
		"版本号必须是完整的 SemVer（例如 0.2.1-rc.3）。")
}

// handleListRuntimes serves GET /api/runtimes (installed runtimes only).
func (a *API) handleListRuntimes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, runtimeDTOs(a.app.Runtimes()))
}

// handleAvailableRuntimes serves GET /api/runtimes/available (npm registry).
func (a *API) handleAvailableRuntimes(w http.ResponseWriter, r *http.Request) {
	versions, err := a.app.AvailableVersions(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if versions == nil {
		versions = []string{}
	}
	writeJSON(w, http.StatusOK, availableVersionsResponse{Versions: versions})
}

// handleInstallRuntime serves POST /api/runtimes: synchronous SemVer
// validation, then a 202 INSTALL_RUNTIME job.
func (a *API) handleInstallRuntime(w http.ResponseWriter, r *http.Request) {
	var request installRuntimeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	version := strings.TrimSpace(request.Version)
	if !runtime.IsVersion(version) {
		a.writeError(w, r, invalidVersionError())
		return
	}
	view, err := a.jobs.Start(job.KindInstallRuntime, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("安装运行时", "正在下载并安装 Harness "+version+"。")
		result, err := a.app.InstallRuntime(ctx, version, j.Logf)
		if err != nil {
			return nil, err
		}
		compatibility := ""
		for _, info := range a.app.Runtimes() {
			if info.Version == version {
				compatibility = info.Compatibility
				break
			}
		}
		j.Phase("已完成", "Harness "+version+" 安装完成。")
		return installJobResult{
			Version:       result.Version,
			Installed:     true,
			Compatibility: compatibility,
		}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// handleProbeRuntime serves POST /api/runtimes/{version}/probe: malformed
// versions are 400, missing installations 404 (synchronous precheck), then a
// 202 PROBE_RUNTIME job.
func (a *API) handleProbeRuntime(w http.ResponseWriter, r *http.Request) {
	version := strings.TrimSpace(r.PathValue("version"))
	if !runtime.IsVersion(version) {
		a.writeError(w, r, invalidVersionError())
		return
	}
	if !a.runtimeInstalled(version) {
		a.writeError(w, r, notFoundError("运行时未安装，请先安装该版本。"))
		return
	}
	view, err := a.jobs.Start(job.KindProbeRuntime, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("探测运行时", "正在探测 Harness "+version+" 的能力。")
		result, err := a.app.ProbeRuntime(ctx, version, j.Logf)
		if err != nil {
			return nil, err
		}
		j.Phase("已完成", "Harness "+version+" 探测完成。")
		return probeJobResult{Version: result.Version, Compatibility: result.Compatibility}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// handleDeleteRuntime serves DELETE /api/runtimes/{version} synchronously.
func (a *API) handleDeleteRuntime(w http.ResponseWriter, r *http.Request) {
	version := strings.TrimSpace(r.PathValue("version"))
	if !runtime.IsVersion(version) {
		a.writeError(w, r, notFoundError("运行时不存在。"))
		return
	}
	trashedPath, err := a.app.DeleteRuntime(r.Context(), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deletedResponse{TrashedPath: trashedPath})
}
