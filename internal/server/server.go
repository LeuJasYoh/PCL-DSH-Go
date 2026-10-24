// SPDX-License-Identifier: GPL-3.0-or-later

// Package server implements the minimal M0 management surface used to verify
// the vertical slice (create instance -> launch -> /workbench proxy -> stop).
//
// The full v1 REST API (auth/session/CSRF/jobs/SSE) is M1 work; this package
// deliberately exposes only:
//
//	GET  /api/health                 anonymous
//	GET  /                           plain placeholder page
//	GET  /api/m0/state               dev mode only
//	GET  /api/m0/instances           dev mode only
//	POST /api/m0/instances           dev mode only
//	POST /api/m0/instances/{id}/launch
//	POST /api/m0/instances/{id}/stop
//	POST /api/m0/runtimes/install
//	ANY  /workbench/{id}/**          reverse proxy (dev mode: no session yet)
//
// The dev routes are registered only when Config.DevMode is true and the
// caller is expected to bind 127.0.0.1; this is an internal milestone surface,
// not the shipped API.
package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

// Orchestrator is the M0 process/runtime orchestration used by the debug API.
// internal/app provides the production implementation (runtime.Service +
// process.Manager); tests provide fakes.
type Orchestrator interface {
	// InstallRuntime installs the requested DSH version (blocking until done).
	InstallRuntime(ctx context.Context, version string) (RuntimeInstallResult, error)
	// LaunchInstance starts the instance and blocks until it is ready (or fails).
	LaunchInstance(ctx context.Context, instanceID string) (InstanceLaunchResult, error)
	// StopInstance stops the instance and releases its port.
	StopInstance(ctx context.Context, instanceID string) error
	// ResolveProxy returns the live /workbench target of an instance.
	ResolveProxy(ctx context.Context, instanceID string) (proxy.Target, error)
	// Running returns the currently tracked instances.
	Running() []InstanceRuntimeStatus
}

// RuntimeInstallResult is the outcome of a runtime installation.
type RuntimeInstallResult struct {
	Version   string `json:"version"`
	EntryPath string `json:"entryPath"`
	Reused    bool   `json:"reused"`
}

// InstanceLaunchResult is returned once an instance is ready.
type InstanceLaunchResult struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	WebURL     string `json:"webUrl"`
}

// InstanceRuntimeStatus is the live process state of one tracked instance.
type InstanceRuntimeStatus struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	Ready      bool   `json:"ready"`
	PID        int    `json:"pid"`
}

// Deps are the server dependencies.
type Deps struct {
	Config       config.Config
	Logger       *slog.Logger
	Store        *store.Store
	Orchestrator Orchestrator
}

// Server holds the HTTP handler and its dependencies.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	store  *store.Store
	orch   Orchestrator
	start  time.Time
	proxy  *proxy.Handler
}

// New validates dependencies and builds the server.
func New(deps Deps) (*Server, error) {
	if deps.Logger == nil {
		return nil, errors.New("server: logger is required")
	}
	if deps.Store == nil {
		return nil, errors.New("server: store is required")
	}
	if deps.Orchestrator == nil {
		return nil, errors.New("server: orchestrator is required")
	}
	s := &Server{
		cfg:    deps.Config,
		logger: deps.Logger,
		store:  deps.Store,
		orch:   deps.Orchestrator,
		start:  time.Now(),
	}
	s.proxy = proxy.New(deps.Logger, resolverAdapter{orch: deps.Orchestrator}, s.authorizeWorkbench)
	return s, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("/workbench/", s.proxy.ServeHTTP)
	if s.cfg.DevMode {
		mux.HandleFunc("GET /api/m0/state", s.handleM0State)
		mux.HandleFunc("GET /api/m0/instances", s.handleM0ListInstances)
		mux.HandleFunc("POST /api/m0/instances", s.handleM0CreateInstance)
		mux.HandleFunc("POST /api/m0/instances/{id}/launch", s.handleM0Launch)
		mux.HandleFunc("POST /api/m0/instances/{id}/stop", s.handleM0Stop)
		mux.HandleFunc("POST /api/m0/runtimes/install", s.handleM0InstallRuntime)
	}
	return s.withMiddleware(mux)
}

// withMiddleware adds trace ids, security headers, panic recovery and access
// logging. Query strings are never logged (they may carry bootstrap tokens).
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		traceID := newTraceID()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		header := rec.Header()
		header.Set("X-Trace-Id", traceID)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		// X-Frame-Options is only for the manager's own pages: the proxied DSH
		// workbench keeps whatever framing policy upstream sends.
		if !strings.HasPrefix(r.URL.Path, "/workbench/") {
			header.Set("X-Frame-Options", "DENY")
		}
		defer func() {
			if panicValue := recover(); panicValue != nil {
				// http.ErrAbortHandler is the standard signal for an aborted
				// handler (e.g. the SSE client disconnected mid-write); it is not
				// an application failure, so keep the access log clean.
				if panicValue == http.ErrAbortHandler {
					return
				}
				s.logger.Error("request panic", "traceId", traceID, "panic", panicValue)
				if !rec.wroteHeader {
					writeJSON(rec, http.StatusInternalServerError, errorBody(CodeInternal, "服务器内部错误。", traceID))
				}
			}
			s.logger.Info("request",
				"traceId", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"durationMs", time.Since(started).Milliseconds(),
			)
		}()
		next.ServeHTTP(rec, r)
	})
}

// authorizeWorkbench guards /workbench. M1 wires the real session/CSRF check
// here; M0 only serves the workbench in dev mode and fails closed otherwise.
func (s *Server) authorizeWorkbench(*http.Request) error {
	if s.cfg.DevMode {
		return nil
	}
	return errors.New("workbench: session authentication is not implemented in M0")
}

// resolverAdapter adapts Orchestrator to proxy.Resolver.
type resolverAdapter struct{ orch Orchestrator }

func (a resolverAdapter) Resolve(ctx context.Context, instanceID string) (proxy.Target, error) {
	return a.orch.ResolveProxy(ctx, instanceID)
}

// handleHealth is the anonymous health endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "UP",
		"version":       s.cfg.Version,
		"uptimeSeconds": int64(time.Since(s.start) / time.Second),
	})
}

// handleRoot serves a tiny placeholder page so the manager has a visible root
// until the Vue WebUI is wired in M4.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>PCL-DSH-Go</title></head>" +
		"<body><h1>PCL-DSH-Go</h1><p>M0 垂直切片：管理服务已运行（版本 " + s.cfg.Version + "）。</p>" +
		"<p>健康检查：<a href=\"/api/health\">/api/health</a></p></body></html>"))
}

// writeJSON encodes payload as a JSON response with no-store semantics.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"code":"INTERNAL","message":"响应编码失败。"}`)
	}
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// errorResponse is the JSON error body of the M0 debug API.
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"traceId,omitempty"`
}

func errorBody(code, message, traceID string) errorResponse {
	return errorResponse{Code: code, Message: message, TraceID: traceID}
}

// writeError maps an error to a JSON response. Unknown errors are reported as
// INTERNAL without echoing their text (it may contain internal paths).
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if apiErr, ok := AsAPIError(err); ok {
		writeJSON(w, apiErr.Status, errorBody(apiErr.Code, apiErr.Message, ""))
		return
	}
	s.logger.Warn("request failed", "path", r.URL.Path, "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody(CodeInternal, "服务器内部错误。", ""))
}

func newTraceID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(buf[:])
}

// statusRecorder captures the response status for access logging and forwards
// Flush/Hijack so streaming and WebSocket upgrades keep working.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
