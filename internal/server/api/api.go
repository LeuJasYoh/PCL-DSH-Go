// SPDX-License-Identifier: GPL-3.0-or-later

// Package api implements the manager's authenticated REST API v1: middleware
// (trace ids, security headers, sessions, first-login gate, CSRF, busy gate),
// JSON DTOs and every handler listed in docs/IMPLEMENTATION_SPEC.md 4.2.
//
// internal/server.New wires the package to the real application, job manager
// and /workbench proxy; tests replace the App/Jobs interfaces with fakes.
//
// Contract: docs/M1_INTERFACES.md 10, docs/IMPLEMENTATION_SPEC.md 4/5.
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/pack"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// cryptorandRead is crypto/rand.Read, indirected so newTraceID has one call
// site (and tests could stub it if ever needed).
var cryptorandRead = rand.Read

// Compile-time contract checks: the production services must keep satisfying the
// API interfaces (docs/M3_INTERFACES.md 8).
var (
	_ PackService   = (*pack.Service)(nil)
	_ PluginService = (*plugin.Service)(nil)
)

// App is the subset of *app.App used by the HTTP handlers. *app.App implements
// it; tests provide an in-memory fake.
type App interface {
	CreateInstance(ctx context.Context, in store.CreateInstanceInput) (store.Instance, error)
	UpdateInstance(ctx context.Context, id string, name *string, port *int) (store.Instance, error)
	DeleteInstance(ctx context.Context, id string) (string, error)
	LaunchInstance(ctx context.Context, instanceID string, logf func(string)) (app.LaunchResult, error)
	StopInstance(ctx context.Context, instanceID string) error
	LiveInstance(instanceID string) (app.LiveStatus, bool)
	Running() []app.InstanceRuntimeStatus
	InstallRuntime(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error)
	ProbeRuntime(ctx context.Context, version string, logf func(string)) (app.ProbeResult, error)
	DeleteRuntime(ctx context.Context, version string) (string, error)
	Runtimes() []app.RuntimeInfo
	AvailableVersions(ctx context.Context) ([]string, error)
	CachedAvailableVersions() []string
	UpdateSettings(ctx context.Context, settings store.Settings) error
	LogsTail(lines int) ([]string, error)
}

// Jobs is the subset of *job.Manager used by the API. *job.Manager implements
// it; tests may use the real manager or a fake.
type Jobs interface {
	Start(kind job.Kind, timeout time.Duration, task job.Task) (*job.View, error)
	Busy() bool
	List() []job.View
	Register(mux *http.ServeMux)
}

// PackService is the subset of *pack.Service used by the API: integration-pack
// preview, import and export (docs/M3_INTERFACES.md 3/8). *pack.Service
// implements it; tests provide a fake.
type PackService interface {
	Preview(ctx context.Context, reader io.Reader, filename string) (pack.Preview, error)
	Import(ctx context.Context, in pack.ImportInput, logf func(string)) (pack.ImportResult, error)
	Export(ctx context.Context, in pack.ExportInput, logf func(string)) (pack.ExportResult, error)
}

// PluginService is the subset of *plugin.Service used by the API: installed
// plugin listing and mutations (docs/M3_INTERFACES.md 5/8). *plugin.Service
// implements it; tests provide a fake.
type PluginService interface {
	List(ctx context.Context, instanceID, profile string) ([]plugin.DTO, error)
	Mutate(ctx context.Context, in plugin.MutateInput, logf func(string)) (plugin.MutateResult, error)
}

// Deps are the API dependencies. Every field is required except Workbench
// (a default 404 handler is used when nil), Artifacts (the download route stays
// unmounted when nil) and StartedAt (defaults to now).
type Deps struct {
	Config   config.Config
	Logger   *slog.Logger
	Dirs     config.Dirs
	Store    *store.Store
	Security *security.Manager
	Jobs     Jobs
	App      App
	// Pack backs the /api/packs/** endpoints (preview/import/export).
	Pack PackService
	// Plugins backs the /api/instances/{id}/plugins endpoints.
	Plugins   PluginService
	StartedAt time.Time
	// Workbench is the /workbench reverse-proxy handler. The API mounts it
	// after the session/first-login guards, so the proxy itself never has to
	// produce the PASSWORD_CHANGE_REQUIRED response.
	Workbench http.Handler
	// Artifacts serves GET /api/artifacts/{id} (pack export downloads,
	// docs/M3_INTERFACES.md 4). Optional: nil leaves the route unmounted.
	Artifacts *artifact.Store
}

// API serves the manager's REST API v1.
type API struct {
	cfg       config.Config
	logger    *slog.Logger
	dirs      config.Dirs
	store     *store.Store
	security  *security.Manager
	jobs      Jobs
	app       App
	pack      PackService
	plugins   PluginService
	startedAt time.Time
	workbench http.Handler
	artifacts *artifact.Store
	handler   http.Handler
}

// New validates deps and builds the API handler.
func New(deps Deps) (*API, error) {
	if deps.Logger == nil {
		return nil, errors.New("api: logger is required")
	}
	if deps.Store == nil {
		return nil, errors.New("api: store is required")
	}
	if deps.Security == nil {
		return nil, errors.New("api: security manager is required")
	}
	if deps.Jobs == nil {
		return nil, errors.New("api: job manager is required")
	}
	if deps.App == nil {
		return nil, errors.New("api: application is required")
	}
	if deps.Pack == nil {
		return nil, errors.New("api: pack service is required")
	}
	if deps.Plugins == nil {
		return nil, errors.New("api: plugin service is required")
	}
	if deps.StartedAt.IsZero() {
		deps.StartedAt = time.Now()
	}
	if deps.Workbench == nil {
		deps.Workbench = http.NotFoundHandler()
	}
	a := &API{
		cfg:       deps.Config,
		logger:    deps.Logger,
		dirs:      deps.Dirs,
		store:     deps.Store,
		security:  deps.Security,
		jobs:      deps.Jobs,
		app:       deps.App,
		pack:      deps.Pack,
		plugins:   deps.Plugins,
		startedAt: deps.StartedAt,
		workbench: deps.Workbench,
		artifacts: deps.Artifacts,
	}
	a.handler = a.buildHandler()
	return a, nil
}

// Handler returns the fully wrapped HTTP handler.
func (a *API) Handler() http.Handler { return a.handler }

// apiRoute is one path of the API with its per-method handlers.
type apiRoute struct {
	path    string
	methods map[string]http.HandlerFunc
}

// buildHandler registers every route and wraps the mux with the middleware
// chain (outermost first): trace/security headers/recover/access log, workbench
// raw-path guard (dot segments must be a 400, not a ServeMux redirect), session
// lookup, authorization/CSRF/busy guards, JSON body limit.
func (a *API) buildHandler() http.Handler {
	mux := http.NewServeMux()

	routes := []apiRoute{
		// Authentication (public where documented).
		{"/api/auth/login", map[string]http.HandlerFunc{http.MethodPost: a.handleLogin}},
		{"/api/auth/logout", map[string]http.HandlerFunc{http.MethodPost: a.handleLogout}},
		{"/api/auth/me", map[string]http.HandlerFunc{http.MethodGet: a.handleMe}},
		{"/api/auth/password", map[string]http.HandlerFunc{http.MethodPut: a.handleChangePassword}},
		// Health is anonymous.
		{"/api/health", map[string]http.HandlerFunc{http.MethodGet: a.handleHealth}},
		// Aggregate state.
		{"/api/state", map[string]http.HandlerFunc{http.MethodGet: a.handleState}},
		// Instances.
		{"/api/instances", map[string]http.HandlerFunc{
			http.MethodGet:  a.handleListInstances,
			http.MethodPost: a.handleCreateInstance,
		}},
		{"/api/instances/{id}", map[string]http.HandlerFunc{
			http.MethodGet:    a.handleGetInstance,
			http.MethodPut:    a.handleUpdateInstance,
			http.MethodDelete: a.handleDeleteInstance,
		}},
		{"/api/instances/{id}/selection", map[string]http.HandlerFunc{http.MethodPut: a.handleSelectInstance}},
		{"/api/instances/{id}/launch", map[string]http.HandlerFunc{http.MethodPost: a.handleLaunchInstance}},
		{"/api/instances/{id}/stop", map[string]http.HandlerFunc{http.MethodPost: a.handleStopInstance}},
		{"/api/instances/{id}/web-url", map[string]http.HandlerFunc{http.MethodGet: a.handleWebURL}},
		{"/api/instances/{id}/direct-access", map[string]http.HandlerFunc{http.MethodGet: a.handleDirectAccess}},
		// Runtimes.
		{"/api/runtimes", map[string]http.HandlerFunc{
			http.MethodGet:  a.handleListRuntimes,
			http.MethodPost: a.handleInstallRuntime,
		}},
		{"/api/runtimes/available", map[string]http.HandlerFunc{http.MethodGet: a.handleAvailableRuntimes}},
		{"/api/runtimes/{version}/probe", map[string]http.HandlerFunc{http.MethodPost: a.handleProbeRuntime}},
		{"/api/runtimes/{version}", map[string]http.HandlerFunc{http.MethodDelete: a.handleDeleteRuntime}},
		// Settings and logs.
		{"/api/settings", map[string]http.HandlerFunc{
			http.MethodGet: a.handleGetSettings,
			http.MethodPut: a.handleUpdateSettings,
		}},
		{"/api/logs/tail", map[string]http.HandlerFunc{http.MethodGet: a.handleLogsTail}},
		// Integration packs (M3): preview is synchronous; import/export are jobs.
		{"/api/packs/preview", map[string]http.HandlerFunc{http.MethodPost: a.handlePackPreview}},
		{"/api/packs/import", map[string]http.HandlerFunc{http.MethodPost: a.handlePackImport}},
		{"/api/packs/export", map[string]http.HandlerFunc{http.MethodPost: a.handlePackExport}},
		// Installed plugins of one instance (M3).
		{"/api/instances/{id}/plugins", map[string]http.HandlerFunc{
			http.MethodGet:  a.handleListPlugins,
			http.MethodPost: a.handleAddPlugin,
		}},
		{"/api/instances/{id}/plugins/{name...}", map[string]http.HandlerFunc{
			http.MethodPatch:  a.handleUpdatePlugin,
			http.MethodDelete: a.handleRemovePlugin,
		}},
	}
	for _, route := range routes {
		a.registerRoute(mux, route)
	}

	// Jobs (/api/jobs, /api/jobs/{id}, /api/jobs/{id}/events, cancel) are
	// registered by the job manager itself.
	a.jobs.Register(mux)

	// Artifact downloads (GET /api/artifacts/{id}) are mounted inside the
	// guarded mux so they inherit sessions, the first-login gate and the
	// security headers (docs/M3_INTERFACES.md 4).
	if a.artifacts != nil {
		a.artifacts.Register(mux)
	}

	// JSON 404 for unknown API paths.
	mux.HandleFunc("/api", a.handleAPINotFound)
	mux.HandleFunc("/api/", a.handleAPINotFound)

	// Workbench reverse proxy and the placeholder root page.
	mux.Handle("/workbench/", a.workbench)
	mux.HandleFunc("GET /{$}", a.handleRoot)

	return a.withTracing(
		a.withWorkbenchRawPathGuard(
			a.withSession(
				a.withGuards(
					a.withBodyLimit(mux),
				),
			),
		),
	)
}

// standardMethods are the HTTP methods that get an explicit JSON 405 handler
// on a route they are not implemented for. Explicit per-method registration
// keeps the ServeMux pattern set conflict-free (a method-agnostic fallback would
// conflict with method-specific wildcard siblings such as DELETE
// /api/runtimes/{version} vs /api/runtimes/available). HEAD is omitted: ServeMux
// treats GET patterns as serving HEAD, and a HEAD-specific 405 pattern would be
// an incomparable (method subset, path superset) conflict; a HEAD request to a
// non-GET route therefore falls through to the JSON 404 handler.
var standardMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

// registerRoute mounts the per-method handlers plus a JSON 405 handler for the
// other standard methods (the ServeMux default writes plain text, which would
// break the documented error contract).
func (a *API) registerRoute(mux *http.ServeMux, route apiRoute) {
	allowed := make([]string, 0, len(route.methods))
	for method, handler := range route.methods {
		mux.HandleFunc(method+" "+route.path, handler)
		allowed = append(allowed, method)
	}
	sort.Strings(allowed)
	notAllowed := a.methodNotAllowed(allowed)
	for _, method := range standardMethods {
		if _, ok := route.methods[method]; ok {
			continue
		}
		mux.HandleFunc(method+" "+route.path, notAllowed)
	}
}

// methodNotAllowed builds the 405 JSON handler for one route.
func (a *API) methodNotAllowed(allowed []string) http.HandlerFunc {
	allowHeader := strings.Join(allowed, ", ")
	message := "不支持的请求方法，允许：" + allowHeader + "。"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowHeader)
		a.writeError(w, r, apierr.New(http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed, message))
	}
}

// handleAPINotFound returns the documented JSON error for unknown API routes.
func (a *API) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	a.writeError(w, r, notFoundError("请求的接口不存在。"))
}

// notFoundError is the shared 404 NOT_FOUND error.
func notFoundError(message string) error {
	return apierr.New(http.StatusNotFound, apierr.CodeNotFound, message)
}

// isAPIPath reports whether path belongs to the JSON API.
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// isWorkbenchPath reports whether path belongs to the /workbench proxy.
func isWorkbenchPath(path string) bool {
	return path == "/workbench" || strings.HasPrefix(path, "/workbench/")
}
