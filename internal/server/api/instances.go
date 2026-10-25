// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/ports"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// stopTimeout bounds one synchronous stop request (3s TERM + 4s KILL + port
// polling leaves generous headroom).
const stopTimeout = 30 * time.Second

// portProbeTimeout is how long the launch precheck waits to decide whether a
// loopback port is occupied.
const portProbeTimeout = 150 * time.Millisecond

// createInstanceRequest is the POST /api/instances body.
type createInstanceRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Profile string `json:"profile"`
	Port    int    `json:"port"`
}

// updateInstanceRequest is the PUT /api/instances/{id} body. version/profile
// are decoded only to produce the documented CONFLICT_UNSUPPORTED_CHANGE.
type updateInstanceRequest struct {
	Name    *string `json:"name"`
	Port    *int    `json:"port"`
	Version *string `json:"version"`
	Profile *string `json:"profile"`
}

// deletedResponse is the DELETE bodies of instances and runtimes.
type deletedResponse struct {
	TrashedPath string `json:"trashedPath"`
}

// webURLResponse is the GET /api/instances/{id}/web-url body.
type webURLResponse struct {
	URL string `json:"url"`
}

// launchJobResult is the LAUNCH_INSTANCE job result: it must never contain the
// bootstrap token, only the console path.
type launchJobResult struct {
	InstanceID string `json:"instanceId"`
	WebURL     string `json:"webUrl"`
}

// handleListInstances serves GET /api/instances.
func (a *API) handleListInstances(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.instanceDTOs(a.store.ListInstances()))
}

// handleCreateInstance serves POST /api/instances. Port 0 means "pick a free
// port" (implemented by the app layer).
func (a *API) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var request createInstanceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	instance, err := a.app.CreateInstance(r.Context(), store.CreateInstanceInput{
		Name:    request.Name,
		Version: request.Version,
		Profile: request.Profile,
		Port:    request.Port,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.instanceDTO(instance))
}

// getInstance loads one instance or writes the documented 404.
func (a *API) getInstance(w http.ResponseWriter, r *http.Request) (store.Instance, bool) {
	instance, ok := a.store.GetInstance(r.PathValue("id"))
	if !ok {
		a.writeError(w, r, notFoundError("实例不存在。"))
		return store.Instance{}, false
	}
	return instance, true
}

// handleGetInstance serves GET /api/instances/{id}.
func (a *API) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, a.instanceDTO(instance))
}

// handleUpdateInstance serves PUT /api/instances/{id}: only name and port may
// change, version/profile are immutable.
func (a *API) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	var request updateInstanceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	if request.Version != nil || request.Profile != nil {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeConflictUnsupportedChange,
			"版本与 profile 创建后不可修改。"))
		return
	}
	if request.Name == nil && request.Port == nil {
		a.writeError(w, r, apierr.New(http.StatusBadRequest, apierr.CodeValidation,
			"请至少提供 name 或 port。"))
		return
	}
	instance, err := a.app.UpdateInstance(r.Context(), r.PathValue("id"), request.Name, request.Port)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.instanceDTO(instance))
}

// handleDeleteInstance serves DELETE /api/instances/{id}; running instances are
// rejected by the app layer.
func (a *API) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	trashedPath, err := a.app.DeleteInstance(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deletedResponse{TrashedPath: trashedPath})
}

// handleSelectInstance serves PUT /api/instances/{id}/selection.
func (a *API) handleSelectInstance(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	if err := a.store.SelectInstance(instance.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.writeError(w, r, notFoundError("实例不存在。"))
			return
		}
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.instanceDTO(instance))
}

// handleLaunchInstance serves POST /api/instances/{id}/launch: synchronous
// prechecks first, then a 202 LAUNCH_INSTANCE job (docs/M1_INTERFACES.md 10).
func (a *API) handleLaunchInstance(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	if a.jobs.Busy() {
		a.writeError(w, r, apierr.Busy())
		return
	}
	// Frozen precheck order (docs/M1_INTERFACES.md 10 + Lead 2026-10-24):
	// busy job, running-instance limit, this instance active, port in use,
	// runtime missing. The limit is shared with app.LaunchInstance
	// (--pcl.max-running-instances; default 1, 0 = unlimited).
	if limit := a.app.MaxRunningInstances(); limit > 0 {
		runningOthers := 0
		for _, running := range a.app.Running() {
			if running.InstanceID != instance.ID {
				runningOthers++
			}
		}
		if runningOthers >= limit {
			a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeConflictRunningInstance,
				fmt.Sprintf("当前最多允许同时运行 %d 个 DSH 实例（--pcl.max-running-instances），请先停止其它实例。", limit)))
			return
		}
	}
	if live, tracked := a.app.LiveInstance(instance.ID); tracked && isActiveStatus(live.StatusCode) {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeConflictBusy,
			"该实例已在运行，请先停止后再启动。"))
		return
	}
	if portInUse(instance.Port) {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeConflictPortBusy,
			fmt.Sprintf("端口 %d 已被占用，请修改实例端口后重试。", instance.Port)))
		return
	}
	if !a.runtimeInstalled(instance.Version) {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeInstanceNotReady,
			fmt.Sprintf("Harness %s 尚未安装，请先安装运行时。", instance.Version)))
		return
	}

	instanceID := instance.ID
	view, err := a.jobs.Start(job.KindLaunchInstance, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("启动实例", "正在探测运行时并启动进程。")
		result, err := a.app.LaunchInstance(ctx, instanceID, j.Logf)
		if err != nil {
			return nil, err
		}
		j.Phase("已就绪", "实例已就绪，可以打开工作台。")
		return launchJobResult{
			InstanceID: result.InstanceID,
			WebURL:     "/workbench/" + result.InstanceID + "/",
		}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// handleStopInstance serves POST /api/instances/{id}/stop synchronously.
func (a *API) handleStopInstance(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), stopTimeout)
	defer cancel()
	if err := a.app.StopInstance(ctx, instance.ID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			a.writeError(w, r, apierr.New(http.StatusInternalServerError, apierr.CodeInternal,
				"停止实例超时，请查看实例日志。"))
			return
		}
		a.writeError(w, r, err)
		return
	}
	updated, ok := a.store.GetInstance(instance.ID)
	if !ok {
		a.writeError(w, r, notFoundError("实例不存在。"))
		return
	}
	writeJSON(w, http.StatusOK, a.instanceDTO(updated))
}

// handleWebURL serves GET /api/instances/{id}/web-url: the console path of a
// ready instance, never a token URL.
func (a *API) handleWebURL(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	live, tracked := a.app.LiveInstance(instance.ID)
	if !tracked || !live.Ready {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeInstanceNotReady,
			"实例尚未就绪，请先启动实例。"))
		return
	}
	writeJSON(w, http.StatusOK, webURLResponse{URL: "/workbench/" + instance.ID + "/"})
}

// runtimeInstalled reports whether version is installed (used by the launch
// precheck so a missing runtime fails synchronously instead of as a job error).
func (a *API) runtimeInstalled(version string) bool {
	for _, info := range a.app.Runtimes() {
		if info.Version == version && info.Installed {
			return true
		}
	}
	return false
}

// portInUse reports whether something already listens on 127.0.0.1:port. A
// dial is used instead of a bind to avoid TIME_WAIT false positives (the same
// approach as internal/process).
func portInUse(port int) bool {
	if !ports.ValidInstance(port) {
		return true
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), portProbeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
