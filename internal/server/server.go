// SPDX-License-Identifier: GPL-3.0-or-later

// Package server wires the manager's HTTP surface: the authenticated REST API
// v1 (internal/server/api) and the /workbench reverse proxy
// (internal/server/proxy).
//
// Contract: docs/M1_INTERFACES.md 10, docs/IMPLEMENTATION_SPEC.md 4/5.
// cmd/pcl-dsh-go builds the dependencies (store, admin, security, app, jobs)
// and passes them in through Deps.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/artifact"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/api"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// errNotAuthenticated is returned by the workbench authorizer when the API
// session middleware did not attach a session (the API guards normally answer
// 401 long before the proxy runs; this is defense in depth).
var errNotAuthenticated = errors.New("workbench: not authenticated")

// Deps are the server dependencies (docs/M1_INTERFACES.md 10).
type Deps struct {
	Config   config.Config
	Logger   *slog.Logger
	Dirs     config.Dirs
	Store    *store.Store
	Security *security.Manager
	Jobs     *job.Manager
	App      *app.App
	// Pack backs the /api/packs/** endpoints (preview/import/export).
	Pack api.PackService
	// Plugins backs the /api/instances/{id}/plugins endpoints.
	Plugins api.PluginService
	// Artifacts serves pack export downloads (GET /api/artifacts/{id}).
	Artifacts *artifact.Store
	StartedAt time.Time
}

// Server holds the fully wrapped HTTP handler.
type Server struct {
	handler http.Handler
}

// New validates the dependencies and builds the HTTP handler.
func New(deps Deps) (*Server, error) {
	switch {
	case deps.Logger == nil:
		return nil, errors.New("server: logger is required")
	case deps.Store == nil:
		return nil, errors.New("server: store is required")
	case deps.Security == nil:
		return nil, errors.New("server: security manager is required")
	case deps.Jobs == nil:
		return nil, errors.New("server: job manager is required")
	case deps.App == nil:
		return nil, errors.New("server: application is required")
	case deps.Pack == nil:
		return nil, errors.New("server: pack service is required")
	case deps.Plugins == nil:
		return nil, errors.New("server: plugin service is required")
	}
	workbench := proxy.New(
		deps.Logger,
		proxyResolver{application: deps.App},
		workbenchAuthorizer(deps.Security),
	)
	handler, err := api.New(api.Deps{
		Config:    deps.Config,
		Logger:    deps.Logger,
		Dirs:      deps.Dirs,
		Store:     deps.Store,
		Security:  deps.Security,
		Jobs:      deps.Jobs,
		App:       deps.App,
		Pack:      deps.Pack,
		Plugins:   deps.Plugins,
		StartedAt: deps.StartedAt,
		Workbench: workbench,
		Artifacts: deps.Artifacts,
	})
	if err != nil {
		return nil, err
	}
	return &Server{handler: handler.Handler()}, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// proxyResolver adapts *app.App to proxy.Resolver.
type proxyResolver struct {
	application *app.App
}

// Resolve implements proxy.Resolver.
func (r proxyResolver) Resolve(ctx context.Context, instanceID string) (proxy.Target, error) {
	return r.application.ResolveProxy(ctx, instanceID)
}

// workbenchAuthorizer enforces the session + first-login gate for /workbench.
// The API middleware has already applied both rules, so this is defense in
// depth: the session must be on the request context and the initial password
// must have been changed.
func workbenchAuthorizer(manager *security.Manager) proxy.Authorizer {
	return func(r *http.Request) error {
		if _, ok := api.SessionFromContext(r); !ok {
			return errNotAuthenticated
		}
		if manager.InitialPasswordPending() {
			return proxy.ErrForbidden
		}
		return nil
	}
}
