// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// maxDebugJSONBytes caps M0 debug API request bodies.
const maxDebugJSONBytes = 1 << 20

// launchTimeout bounds a synchronous M0 launch (probe + process start + 90s
// readiness budget leaves headroom).
const launchTimeout = 4 * time.Minute

// installTimeout bounds a synchronous M0 runtime installation.
const installTimeout = 20 * time.Minute

// stopTimeout bounds a synchronous M0 stop (3s TERM + 4s KILL + port poll).
const stopTimeout = 30 * time.Second

// instanceDTO is the M0 instance representation.
type instanceDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Profile   string `json:"profile"`
	Port      int    `json:"port"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Path      string `json:"path"`
}

func (s *Server) instanceDTO(instance store.Instance) instanceDTO {
	return instanceDTO{
		ID:        instance.ID,
		Name:      instance.Name,
		Version:   instance.Version,
		Profile:   instance.Profile,
		Port:      instance.Port,
		CreatedAt: instance.CreatedAt,
		UpdatedAt: instance.UpdatedAt,
		Path:      s.cfg.DataRoot + "/instances/" + instance.ID,
	}
}

// handleM0State returns instances and live process state.
func (s *Server) handleM0State(w http.ResponseWriter, r *http.Request) {
	instances := s.store.ListInstances()
	dtos := make([]instanceDTO, 0, len(instances))
	for _, instance := range instances {
		dtos = append(dtos, s.instanceDTO(instance))
	}
	running := s.orch.Running()
	if running == nil {
		running = []InstanceRuntimeStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":            s.cfg.Version,
		"devMode":            s.cfg.DevMode,
		"selectedInstanceId": s.store.SelectedInstanceID(),
		"instances":          dtos,
		"running":            running,
	})
}

// handleM0ListInstances lists configured instances.
func (s *Server) handleM0ListInstances(w http.ResponseWriter, r *http.Request) {
	instances := s.store.ListInstances()
	dtos := make([]instanceDTO, 0, len(instances))
	for _, instance := range instances {
		dtos = append(dtos, s.instanceDTO(instance))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// createInstanceRequest is the M0 debug create payload.
type createInstanceRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Profile string `json:"profile"`
	Port    int    `json:"port"`
}

// handleM0CreateInstance creates a configured (not started) instance.
func (s *Server) handleM0CreateInstance(w http.ResponseWriter, r *http.Request) {
	var request createInstanceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, err)
		return
	}
	instance, err := s.store.CreateInstance(store.CreateInstanceInput{
		Name:    request.Name,
		Version: request.Version,
		Profile: request.Profile,
		Port:    request.Port,
	})
	if err != nil {
		s.writeError(w, r, NewAPIError(http.StatusBadRequest, CodeValidation, "实例参数无效："+err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, s.instanceDTO(instance))
}

// handleM0Launch starts an instance synchronously (M0 only; M1 returns a Job).
func (s *Server) handleM0Launch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.GetInstance(r.PathValue("id")); !ok {
		s.writeError(w, r, NewAPIError(http.StatusNotFound, CodeNotFound, "实例不存在。"))
		return
	}
	ctx, cancel := detachedTimeout(r, launchTimeout)
	defer cancel()
	result, err := s.orch.LaunchInstance(ctx, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleM0Stop stops a running instance.
func (s *Server) handleM0Stop(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.GetInstance(r.PathValue("id")); !ok {
		s.writeError(w, r, NewAPIError(http.StatusNotFound, CodeNotFound, "实例不存在。"))
		return
	}
	ctx, cancel := detachedTimeout(r, stopTimeout)
	defer cancel()
	if err := s.orch.StopInstance(ctx, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instanceId": r.PathValue("id"),
		"status":     "已停止",
	})
}

// installRuntimeRequest is the M0 debug install payload.
type installRuntimeRequest struct {
	Version string `json:"version"`
}

// handleM0InstallRuntime installs a DSH runtime synchronously.
func (s *Server) handleM0InstallRuntime(w http.ResponseWriter, r *http.Request) {
	var request installRuntimeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, err)
		return
	}
	ctx, cancel := detachedTimeout(r, installTimeout)
	defer cancel()
	result, err := s.orch.InstallRuntime(ctx, request.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// decodeJSON decodes a bounded JSON request body into target.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil {
		return NewAPIError(http.StatusBadRequest, CodeBadRequest, "请求体不能为空。")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDebugJSONBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return NewAPIError(http.StatusBadRequest, CodeBadRequest, "请求体不能为空。")
		}
		return NewAPIError(http.StatusBadRequest, CodeBadRequest, "请求体不是合法 JSON。")
	}
	if decoder.More() {
		return NewAPIError(http.StatusBadRequest, CodeBadRequest, "请求体包含多余内容。")
	}
	return nil
}

// detachedTimeout runs long operations on a context that is not canceled when
// the browser disconnects, so a launch/install settles deterministically.
func detachedTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
}
