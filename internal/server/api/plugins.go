// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// pluginAddRequest is the POST /api/instances/{id}/plugins body.
type pluginAddRequest struct {
	Spec string `json:"spec"`
}

// pluginJobResult is the PLUGIN_MUTATE job result
// (docs/IMPLEMENTATION_SPEC.md 4.3).
type pluginJobResult struct {
	InstanceID string `json:"instanceId"`
	Profile    string `json:"profile"`
	Action     string `json:"action"`
	Name       string `json:"name"`
}

// handleListPlugins serves GET /api/instances/{id}/plugins?profile=...
func (a *API) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	profile := pluginProfile(r, instance)
	plugins, err := a.plugins.List(r.Context(), instance.ID, profile)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if plugins == nil {
		plugins = []plugin.DTO{}
	}
	writeJSON(w, http.StatusOK, plugins)
}

// handleAddPlugin serves POST /api/instances/{id}/plugins (202 PLUGIN_MUTATE).
func (a *API) handleAddPlugin(w http.ResponseWriter, r *http.Request) {
	var request pluginAddRequest
	if err := decodeJSON(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	a.startPluginJob(w, r, plugin.ActionAdd, strings.TrimSpace(request.Spec))
}

// handleUpdatePlugin serves PATCH /api/instances/{id}/plugins/{name...}: the
// native CLI performs an in-place update, so the path name is informational and
// no package spec is forwarded (plugin.Mutate rejects specs for updates).
func (a *API) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	a.startPluginJob(w, r, plugin.ActionUpdate, "")
}

// handleRemovePlugin serves DELETE /api/instances/{id}/plugins/{name...}: the
// path name must be the full npm package name of an independent dependency.
func (a *API) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	a.startPluginJob(w, r, plugin.ActionRemove, strings.TrimSpace(r.PathValue("name")))
}

// startPluginJob performs the synchronous prechecks and wraps the mutation in a
// PLUGIN_MUTATE job.
//
// plugin.ValidateMutationInput runs BEFORE Jobs.Start: malformed actions,
// profiles or specs are rejected synchronously with 400 VALIDATION instead of
// failing a job (docs/M3_INTERFACES.md 8, G7). Instance-dependent checks
// (existence, running state, removable dependency, local target containment)
// stay authoritative in the API/app and in plugin.Mutate.
func (a *API) startPluginJob(w http.ResponseWriter, r *http.Request, action, spec string) {
	instance, ok := a.getInstance(w, r)
	if !ok {
		return
	}
	profile := pluginProfile(r, instance)
	input := plugin.MutateInput{
		InstanceID: instance.ID,
		Profile:    profile,
		Action:     action,
		Spec:       spec,
	}
	if err := plugin.ValidateMutationInput(input); err != nil {
		a.writeError(w, r, err)
		return
	}
	if a.instanceActive(instance.ID) {
		a.writeError(w, r, apierr.New(http.StatusConflict, apierr.CodeConflictInstanceRunning,
			"实例正在运行，请先停止实例后再变更插件。"))
		return
	}
	view, err := a.jobs.Start(job.KindPluginMutate, 0, func(ctx context.Context, j *job.Job) (any, error) {
		j.Phase("变更插件", "正在通过原生 Harness CLI "+action+" 插件。")
		result, err := a.plugins.Mutate(ctx, input, j.Logf)
		if err != nil {
			return nil, err
		}
		j.Phase("已完成", "插件操作完成；重启实例后生效。")
		return pluginJobResult{
			InstanceID: result.InstanceID,
			Profile:    result.Profile,
			Action:     result.Action,
			Name:       result.Name,
		}, nil
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// pluginProfile returns the ?profile= query value, defaulting to the instance's
// own profile.
func pluginProfile(r *http.Request, instance store.Instance) string {
	if profile := strings.TrimSpace(r.URL.Query().Get("profile")); profile != "" {
		return profile
	}
	return instance.Profile
}

// instanceActive reports whether a process handle is tracked for the instance
// in an active state (starting, running, not ready or stopping).
func (a *API) instanceActive(instanceID string) bool {
	live, tracked := a.app.LiveInstance(instanceID)
	return tracked && isActiveStatus(live.StatusCode)
}
