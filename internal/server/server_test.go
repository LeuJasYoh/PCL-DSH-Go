// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/pack"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/security"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := New(Deps{}); err == nil {
		t.Fatal("New(Deps{}) succeeded, want an error")
	}
}

// stubPack is a no-op api.PackService for the wiring test.
type stubPack struct{}

func (stubPack) Preview(context.Context, io.Reader, string) (pack.Preview, error) {
	return pack.Preview{}, nil
}

func (stubPack) Import(context.Context, pack.ImportInput, func(string)) (pack.ImportResult, error) {
	return pack.ImportResult{}, nil
}

func (stubPack) Export(context.Context, pack.ExportInput, func(string)) (pack.ExportResult, error) {
	return pack.ExportResult{}, nil
}

// stubPlugins is a no-op api.PluginService for the wiring test.
type stubPlugins struct{}

func (stubPlugins) List(context.Context, string, string) ([]plugin.DTO, error) {
	return nil, nil
}

func (stubPlugins) Mutate(context.Context, plugin.MutateInput, func(string)) (plugin.MutateResult, error) {
	return plugin.MutateResult{}, nil
}

// wiredEnv is a fully wired server (real store/admin/security/jobs/app) used to
// prove cmd-level wiring and the api+proxy composition without a network.
type wiredEnv struct {
	server   *Server
	password string
	cleanup  func()
}

func newWiredEnv(t *testing.T) *wiredEnv {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), "data")
	cfg := config.Config{Version: "wired-test-version", DataRoot: dataRoot, ServerAddress: "127.0.0.1"}
	dirs, err := config.InitDataRoot(cfg)
	if err != nil {
		t.Fatalf("InitDataRoot() error = %v", err)
	}
	st, err := store.Open(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	admin, err := store.OpenAdmin(dirs.DataRoot)
	if err != nil {
		t.Fatalf("store.OpenAdmin() error = %v", err)
	}
	raw, err := os.ReadFile(admin.InitialPasswordPath())
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		t.Fatal("initial password file is empty")
	}
	audit, err := logging.OpenAudit(dirs.LogsDir)
	if err != nil {
		t.Fatalf("logging.OpenAudit() error = %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	manager := security.NewManager(security.Config{}, admin, audit, logger)
	jobs := job.NewManager(logger, job.Options{})
	application, err := app.New(app.Deps{Config: cfg, Dirs: dirs, Logger: logger, Store: st})
	if err != nil {
		t.Fatalf("app.New() error = %v", err)
	}
	instance, err := New(Deps{
		Config:    cfg,
		Logger:    logger,
		Dirs:      dirs,
		Store:     st,
		Security:  manager,
		Jobs:      jobs,
		App:       application,
		Pack:      stubPack{},
		Plugins:   stubPlugins{},
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = application.Shutdown(ctx)
		jobs.Shutdown()
		_ = admin.Close()
		_ = st.Close()
		_ = audit.Close()
	}
	return &wiredEnv{server: instance, password: password, cleanup: cleanup}
}

// do performs one request against the wired handler.
func (e *wiredEnv) do(t *testing.T, method, target string, body []byte, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set(security.CSRFHeaderName, csrf)
		request.AddCookie(&http.Cookie{Name: security.CSRFCookieName, Value: csrf})
	}
	recorder := httptest.NewRecorder()
	e.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// login authenticates against the real AdminStore and returns the cookies.
func (e *wiredEnv) login(t *testing.T) (session *http.Cookie, csrfCookie *http.Cookie) {
	t.Helper()
	recorder := e.do(t, http.MethodPost, "/api/auth/login",
		[]byte("{\"password\":\""+e.password+"\"}"), nil, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("login = %d, want 204; body = %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		switch cookie.Name {
		case security.SessionCookieName:
			session = cookie
		case security.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if session == nil || csrfCookie == nil {
		t.Fatalf("login cookies = %v", recorder.Result().Cookies())
	}
	return session, csrfCookie
}

func TestWiredServerAuthFlow(t *testing.T) {
	env := newWiredEnv(t)
	t.Cleanup(env.cleanup)

	// Anonymous health, root and the documented 401s.
	recorder := env.do(t, http.MethodGet, "/api/health", nil, nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("health = %d, want 200", recorder.Code)
	}
	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.Status != "UP" || health.Version != "wired-test-version" {
		t.Fatalf("health = %+v", health)
	}
	recorder = env.do(t, http.MethodGet, "/", nil, nil, "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "PCL-DSH-Go") {
		t.Fatalf("root = %d %q", recorder.Code, recorder.Body.String())
	}
	recorder = env.do(t, http.MethodGet, "/api/state", nil, nil, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous state = %d, want 401", recorder.Code)
	}
	recorder = env.do(t, http.MethodGet, "/workbench/i-000000000000/", nil, nil, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous workbench = %d, want 401", recorder.Code)
	}

	// First login is gated until the initial password is changed.
	session, csrfCookie := env.login(t)
	cookies := []*http.Cookie{session, csrfCookie}
	recorder = env.do(t, http.MethodGet, "/api/state", nil, cookies, "")
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("pending state = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = env.do(t, http.MethodPut, "/api/auth/password",
		[]byte("{\"oldPassword\":\""+env.password+"\",\"newPassword\":\"new-password-2\"}"),
		cookies, csrfCookie.Value)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("change password = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = env.do(t, http.MethodGet, "/api/state", nil, cookies, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("state after change = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var state struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil || state.Version != "wired-test-version" {
		t.Fatalf("state version = %q (err %v)", state.Version, err)
	}

	// The /workbench proxy is mounted behind the session guard: an unknown
	// instance reaches the proxy resolver and becomes 404 NOT_FOUND (not 401).
	recorder = env.do(t, http.MethodGet, "/workbench/i-000000000000/", nil, cookies, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("workbench unknown instance = %d, want proxy 404; body = %s", recorder.Code, recorder.Body.String())
	}

	// Logout invalidates the session.
	recorder = env.do(t, http.MethodPost, "/api/auth/logout", nil, cookies, csrfCookie.Value)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", recorder.Code)
	}
	recorder = env.do(t, http.MethodGet, "/api/state", nil, cookies, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("state after logout = %d, want 401", recorder.Code)
	}
}
