// SPDX-License-Identifier: GPL-3.0-or-later

// Package plugin lists the plugins of a DSH instance profile and mutates them
// through the installed DSH CLI using an embedded Node bridge
// (docs/M3_INTERFACES.md 5, docs/DSH_INTEGRATION.md 8).
//
// The package never boots or initializes a profile: List only reads package
// metadata, and Mutate shells out to the installed Harness CLI with the bundled
// node/pnpm. Shell forwarding is rejected by the bridge (fail-closed).
package plugin

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/tools"
)

// Plugin management actions accepted by Mutate.
const (
	ActionAdd    = "add"
	ActionUpdate = "update"
	ActionRemove = "remove"
)

// mutateTimeout mirrors the reference implementation (20 minutes).
const mutateTimeout = 20 * time.Minute

// portProbeTimeout bounds the running-instance pre-check.
const portProbeTimeout = 300 * time.Millisecond

// DTO is one installed-plugin row (docs/M3_INTERFACES.md 5).
type DTO struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// MutateInput describes one plugin mutation.
type MutateInput struct {
	InstanceID string
	Profile    string
	Action     string
	Spec       string
}

// MutateResult is the Job-friendly summary of a successful mutation.
type MutateResult struct {
	InstanceID string
	Profile    string
	Action     string
	Name       string
}

// Options are the plugin service dependencies.
type Options struct {
	Dirs     config.Dirs
	Store    *store.Store
	Tools    *tools.Tools
	Runtime  *runtime.Service
	Registry string
	Proxy    string
	Logger   *slog.Logger
}

// Service implements plugin listing and mutation.
type Service struct {
	dirs     config.Dirs
	store    *store.Store
	tools    *tools.Tools
	runtime  *runtime.Service
	registry string
	proxy    string
	logger   *slog.Logger
}

// New validates the options and builds the plugin service. Tools/Runtime may be
// nil: List keeps working from on-disk metadata, while Mutate reports an
// actionable INTERNAL error until the toolchain/runtime service is available.
func New(opts Options) (*Service, error) {
	if strings.TrimSpace(opts.Dirs.DataRoot) == "" ||
		strings.TrimSpace(opts.Dirs.InstancesDir) == "" ||
		strings.TrimSpace(opts.Dirs.RuntimesDir) == "" {
		return nil, fmt.Errorf("plugin: DataRoot/InstancesDir/RuntimesDir 不能为空")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("plugin: store 不能为空")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		dirs:     opts.Dirs,
		store:    opts.Store,
		tools:    opts.Tools,
		runtime:  opts.Runtime,
		registry: opts.Registry,
		proxy:    opts.Proxy,
		logger:   logger,
	}, nil
}

// instance loads one instance or returns a 404 *apierr.Error.
func (s *Service) instance(instanceID string) (store.Instance, error) {
	instance, ok := s.store.GetInstance(strings.TrimSpace(instanceID))
	if !ok {
		return store.Instance{}, apierr.New(http.StatusNotFound, apierr.CodeNotFound, "实例不存在。")
	}
	return instance, nil
}

// profileDirectory is <instances>/<id>/dsh-home/profiles/<profile>.
func (s *Service) profileDirectory(instanceID, profile string) string {
	return filepath.Join(s.dirs.InstancesDir, instanceID, "dsh-home", "profiles", profile)
}

// workspaceDirectory is <instances>/<id>/workspace.
func (s *Service) workspaceDirectory(instanceID string) string {
	return filepath.Join(s.dirs.InstancesDir, instanceID, "workspace")
}

// instanceRunningProbe is a test seam around the loopback running check; the
// production implementation is probeInstancePort. Tests replace it to keep the
// outcome deterministic (on WSL2 a "free" Linux ephemeral port may be reachable
// through Windows localhost forwarding, which is not a DSH instance).
var instanceRunningProbe = probeInstancePort

// probeInstancePort reports whether something accepts TCP connections on the
// instance loopback port.
func probeInstancePort(port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), portProbeTimeout)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// instanceRunning probes the loopback port of an instance. The authoritative
// running check lives in the API/app layer (process manager state); this is a
// fail-safe so plugin mutations never run while DSH is live.
func (s *Service) instanceRunning(instance store.Instance) bool {
	if instance.Port < 1 || instance.Port > 65535 {
		return false
	}
	return instanceRunningProbe(instance.Port)
}

// instanceRunningError is the 409 returned for a live instance.
func instanceRunningError() *apierr.Error {
	return apierr.New(http.StatusConflict, apierr.CodeConflictInstanceRunning,
		"实例正在运行，请先停止实例后再变更插件。")
}
