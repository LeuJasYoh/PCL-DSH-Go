// SPDX-License-Identifier: MIT

package api

import (
	"path/filepath"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/process"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// statusNotStarted is the Chinese instance status shown before any launch.
const statusNotStarted = "未启动"

// InstanceDTO is the API representation of one configured instance
// (docs/IMPLEMENTATION_SPEC.md 4.3 InstanceDto).
type InstanceDTO struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Profile  string   `json:"profile"`
	Port     int      `json:"port"`
	Status   string   `json:"status"`
	Path     string   `json:"path"`
	Profiles []string `json:"profiles"`
}

// RuntimeDTO is one installed runtime (RuntimeDto).
type RuntimeDTO struct {
	Version       string `json:"version"`
	Installed     bool   `json:"installed"`
	Compatibility string `json:"compatibility"`
}

// StatusDTO is the aggregate busy/idle indicator of GET /api/state.
type StatusDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Phase   string `json:"phase,omitempty"`
}

// SettingsDTO is the manager settings payload (GET/PUT /api/settings).
type SettingsDTO struct {
	Proxy               string `json:"proxy"`
	Registry            string `json:"registry"`
	DirectAccessSSHUser string `json:"directAccessSshUser"`
}

// StateDTO is the GET /api/state response body (docs/IMPLEMENTATION_SPEC.md 4.3).
type StateDTO struct {
	Version           string        `json:"version"`
	CurrentInstanceID *string       `json:"currentInstanceId"`
	Instances         []InstanceDTO `json:"instances"`
	Runtimes          []RuntimeDTO  `json:"runtimes"`
	Status            StatusDTO     `json:"status"`
	Settings          SettingsDTO   `json:"settings"`
	AvailableVersions []string      `json:"availableVersions"`
	Warnings          []string      `json:"warnings"`
}

// instanceDTO maps a stored instance to its DTO, including the live process
// status when a handle is tracked.
func (a *API) instanceDTO(instance store.Instance) InstanceDTO {
	status := statusNotStarted
	if live, ok := a.app.LiveInstance(instance.ID); ok {
		if live.StatusMessage != "" {
			status = live.StatusMessage
		} else if live.StatusCode != "" {
			status = live.StatusCode
		}
	}
	return InstanceDTO{
		ID:       instance.ID,
		Name:     instance.Name,
		Version:  instance.Version,
		Profile:  instance.Profile,
		Port:     instance.Port,
		Status:   status,
		Path:     filepath.Join(a.dirs.InstancesDir, instance.ID),
		Profiles: []string{instance.Profile},
	}
}

// instanceDTOs maps a list of stored instances, always returning a non-nil
// slice so JSON encodes [] instead of null.
func (a *API) instanceDTOs(instances []store.Instance) []InstanceDTO {
	out := make([]InstanceDTO, 0, len(instances))
	for _, instance := range instances {
		out = append(out, a.instanceDTO(instance))
	}
	return out
}

// runtimeDTOs maps the installed runtimes, always returning a non-nil slice.
func runtimeDTOs(infos []app.RuntimeInfo) []RuntimeDTO {
	out := make([]RuntimeDTO, 0, len(infos))
	for _, info := range infos {
		out = append(out, RuntimeDTO{
			Version:       info.Version,
			Installed:     info.Installed,
			Compatibility: info.Compatibility,
		})
	}
	return out
}

// settingsDTO maps store settings to the API payload.
func settingsDTO(settings store.Settings) SettingsDTO {
	return SettingsDTO{
		Proxy:               settings.Proxy,
		Registry:            settings.Registry,
		DirectAccessSSHUser: settings.DirectAccessSSHUser,
	}
}

// statusDTO reports READY or, while a job runs, RUNNING with its phase.
func (a *API) statusDTO() StatusDTO {
	if !a.jobs.Busy() {
		return StatusDTO{Code: "READY", Message: "就绪"}
	}
	for _, view := range a.jobs.List() {
		if view.State == job.StateRunning {
			phase := view.Phase
			if phase == "" {
				phase = "任务执行中"
			}
			return StatusDTO{Code: "RUNNING", Message: "进行中：" + phase, Phase: phase}
		}
	}
	return StatusDTO{Code: "RUNNING", Message: "进行中：任务执行中", Phase: "任务执行中"}
}

// isActiveStatus reports whether a process status code represents an active
// instance (starting, running, not ready or stopping): such an instance cannot
// be launched again.
func isActiveStatus(code string) bool {
	switch code {
	case process.StatusStarting, process.StatusRunning, process.StatusNotReady, process.StatusStopping:
		return true
	default:
		return false
	}
}
