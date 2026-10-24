// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/config"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/proxy"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

type fakeOrchestrator struct {
	installResult RuntimeInstallResult
	installErr    error
	launchResult  InstanceLaunchResult
	launchErr     error
	stopErr       error
	resolveTarget proxy.Target
	resolveErr    error
	running       []InstanceRuntimeStatus

	launchedID string
	stoppedID  string
	installed  string
}

func (f *fakeOrchestrator) InstallRuntime(_ context.Context, version string) (RuntimeInstallResult, error) {
	f.installed = version
	return f.installResult, f.installErr
}

func (f *fakeOrchestrator) LaunchInstance(_ context.Context, instanceID string) (InstanceLaunchResult, error) {
	f.launchedID = instanceID
	return f.launchResult, f.launchErr
}

func (f *fakeOrchestrator) StopInstance(_ context.Context, instanceID string) error {
	f.stoppedID = instanceID
	return f.stopErr
}

func (f *fakeOrchestrator) ResolveProxy(_ context.Context, instanceID string) (proxy.Target, error) {
	if f.resolveErr != nil {
		return proxy.Target{}, f.resolveErr
	}
	target := f.resolveTarget
	if target.InstanceID == "" {
		target.InstanceID = instanceID
	}
	return target, nil
}

func (f *fakeOrchestrator) Running() []InstanceRuntimeStatus { return f.running }

// newTestServer builds a server backed by a real (temporary) store.
func newTestServer(t *testing.T, devMode bool, orch Orchestrator) (*Server, *store.Store) {
	t.Helper()
	dataRoot := t.TempDir()
	st, err := store.Open(dataRoot)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if orch == nil {
		orch = &fakeOrchestrator{}
	}
	srv, err := New(Deps{
		Config: config.Config{
			Version:       "test-version",
			ServerAddress: "127.0.0.1",
			DataRoot:      dataRoot,
			DevMode:       devMode,
		},
		Logger:       slog.New(slog.DiscardHandler),
		Store:        st,
		Orchestrator: orch,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, st
}

func do(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func TestHealthIsAnonymous(t *testing.T) {
	srv, _ := newTestServer(t, false, nil)
	rec := do(t, srv, http.MethodGet, "/api/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		UptimeSeconds int64  `json:"uptimeSeconds"`
	}
	decodeBody(t, rec, &payload)
	if payload.Status != "UP" || payload.Version != "test-version" {
		t.Fatalf("unexpected health payload: %+v", payload)
	}
	if rec.Header().Get("X-Trace-Id") == "" {
		t.Fatal("X-Trace-Id header missing")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing")
	}
}

func TestRootPlaceholder(t *testing.T) {
	srv, _ := newTestServer(t, false, nil)
	rec := do(t, srv, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PCL-DSH-Go") {
		t.Fatalf("root body missing brand: %q", rec.Body.String())
	}
	rec = do(t, srv, http.MethodGet, "/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", rec.Code)
	}
}

func TestDevAPIRequiresDevMode(t *testing.T) {
	srv, _ := newTestServer(t, false, nil)
	for _, path := range []string{"/api/m0/state", "/api/m0/instances"} {
		rec := do(t, srv, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 when dev mode is off", path, rec.Code)
		}
	}
	rec := do(t, srv, http.MethodGet, "/workbench/i-0123456789ab/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("workbench status = %d, want 401 when dev mode is off", rec.Code)
	}
}

func TestCreateListState(t *testing.T) {
	srv, _ := newTestServer(t, true, nil)
	rec := do(t, srv, http.MethodPost, "/api/m0/instances",
		`{"name":"工作实例","version":"0.2.1-rc.3","profile":"web","port":3080}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created instanceDTO
	decodeBody(t, rec, &created)
	if created.ID == "" || created.Port != 3080 {
		t.Fatalf("unexpected created instance: %+v", created)
	}
	if !strings.Contains(created.Path, "instances") {
		t.Fatalf("instance path = %q", created.Path)
	}

	rec = do(t, srv, http.MethodGet, "/api/m0/instances", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []instanceDTO
	decodeBody(t, rec, &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	rec = do(t, srv, http.MethodGet, "/api/m0/state", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d", rec.Code)
	}
	var state struct {
		Version   string                  `json:"version"`
		DevMode   bool                    `json:"devMode"`
		Instances []instanceDTO           `json:"instances"`
		Running   []InstanceRuntimeStatus `json:"running"`
	}
	decodeBody(t, rec, &state)
	if !state.DevMode || len(state.Instances) != 1 || state.Running == nil {
		t.Fatalf("state = %+v", state)
	}
}

func TestCreateInstanceValidation(t *testing.T) {
	srv, _ := newTestServer(t, true, nil)
	cases := []struct {
		name string
		body string
	}{
		{"bad json", "{"},
		{"empty body", ""},
		{"bad port", `{"name":"a","version":"0.2.1-rc.3","profile":"web","port":0}`},
		{"bad version", `{"name":"a","version":"latest","profile":"web","port":3080}`},
		{"unknown field", `{"name":"a","version":"0.2.1-rc.3","profile":"web","port":3080,"x":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/m0/instances", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLaunchMapsResultsAndErrors(t *testing.T) {
	orch := &fakeOrchestrator{
		launchResult: InstanceLaunchResult{Status: "运行中", WebURL: "/workbench/placeholder/"},
	}
	srv, st := newTestServer(t, true, orch)
	inst := requireInstance(t, st, "0.2.1-rc.3", 3080)
	orch.launchResult.InstanceID = inst.ID
	orch.launchResult.WebURL = "/workbench/" + inst.ID + "/"

	rec := do(t, srv, http.MethodPost, "/api/m0/instances/"+inst.ID+"/launch", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("launch status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result InstanceLaunchResult
	decodeBody(t, rec, &result)
	if orch.launchedID != inst.ID || result.WebURL != "/workbench/"+inst.ID+"/" {
		t.Fatalf("launch result = %+v launchedID=%q", result, orch.launchedID)
	}

	rec = do(t, srv, http.MethodPost, "/api/m0/instances/i-ffffffffffff/launch", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance status = %d, want 404", rec.Code)
	}

	orch.launchErr = NewAPIError(http.StatusConflict, CodeConflictRunning, "已有实例在运行。")
	rec = do(t, srv, http.MethodPost, "/api/m0/instances/"+inst.ID+"/launch", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("launch conflict status = %d, want 409", rec.Code)
	}
	var apiErr errorResponse
	decodeBody(t, rec, &apiErr)
	if apiErr.Code != CodeConflictRunning {
		t.Fatalf("error code = %q", apiErr.Code)
	}

	orch.launchErr = errors.New("boom")
	rec = do(t, srv, http.MethodPost, "/api/m0/instances/"+inst.ID+"/launch", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("internal status = %d, want 500", rec.Code)
	}
}

func TestStopAndInstall(t *testing.T) {
	orch := &fakeOrchestrator{
		installResult: RuntimeInstallResult{Version: "0.2.1-rc.3", EntryPath: "/data/runtimes/0.2.1-rc.3/node_modules/@deepseek-ai/dsh/lib/bin.js", Reused: true},
	}
	srv, st := newTestServer(t, true, orch)
	inst := requireInstance(t, st, "0.2.1-rc.3", 3080)

	rec := do(t, srv, http.MethodPost, "/api/m0/runtimes/install", `{"version":"0.2.1-rc.3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("install status = %d body=%s", rec.Code, rec.Body.String())
	}
	if orch.installed != "0.2.1-rc.3" {
		t.Fatalf("installed = %q", orch.installed)
	}
	var install RuntimeInstallResult
	decodeBody(t, rec, &install)
	if !install.Reused || install.EntryPath == "" {
		t.Fatalf("install result = %+v", install)
	}

	rec = do(t, srv, http.MethodPost, "/api/m0/instances/"+inst.ID+"/stop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s", rec.Code, rec.Body.String())
	}
	if orch.stoppedID != inst.ID {
		t.Fatalf("stoppedID = %q", orch.stoppedID)
	}
}

func TestWorkbenchDevModeMapsResolverErrors(t *testing.T) {
	orch := &fakeOrchestrator{resolveErr: proxy.ErrInstanceNotFound}
	srv, st := newTestServer(t, true, orch)
	inst := requireInstance(t, st, "0.2.1-rc.3", 3080)
	rec := do(t, srv, http.MethodGet, "/workbench/"+inst.ID+"/", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
	orch.resolveErr = proxy.ErrInstanceNotReady
	rec = do(t, srv, http.MethodGet, "/workbench/"+inst.ID+"/", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
}

func requireInstance(t *testing.T, st *store.Store, version string, port int) store.Instance {
	t.Helper()
	inst, err := st.CreateInstance(store.CreateInstanceInput{Name: "工作实例", Version: version, Profile: "web", Port: port})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return inst
}
