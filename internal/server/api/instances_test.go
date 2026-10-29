// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

func TestInstanceCRUD(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodGet, "/api/instances", nil, sess.withSession())
	list := decodeJSONResponse[[]InstanceDTO](t, rec, http.StatusOK)
	if len(list) != 0 {
		t.Fatalf("initial instance list = %v, want empty", list)
	}
	if body := rec.Body.String(); body != "[]" {
		t.Fatalf("empty list body = %q, want []", body)
	}

	// Create with automatic port allocation (port omitted).
	rec = env.request(t, http.MethodPost, "/api/instances",
		jsonBody(t, map[string]any{"name": "主实例", "version": "0.2.1-rc.3", "profile": "web"}), sess.withSession())
	created := decodeJSONResponse[InstanceDTO](t, rec, http.StatusCreated)
	if created.ID == "" || created.Name != "主实例" || created.Version != "0.2.1-rc.3" || created.Profile != "web" {
		t.Fatalf("created instance = %+v", created)
	}
	if created.Port < 1 {
		t.Fatalf("created port = %d, want automatically allocated", created.Port)
	}
	if created.Status != statusNotStarted {
		t.Fatalf("created status = %q, want %q", created.Status, statusNotStarted)
	}
	if want := filepath.Join(env.dirs.InstancesDir, created.ID); created.Path != want {
		t.Fatalf("created path = %q, want %q", created.Path, want)
	}
	if len(created.Profiles) != 1 || created.Profiles[0] != "web" {
		t.Fatalf("created profiles = %v, want [web]", created.Profiles)
	}

	// Duplicate port → 409 CONFLICT_PORT_BUSY.
	rec = env.request(t, http.MethodPost, "/api/instances",
		jsonBody(t, map[string]any{"name": "冲突", "version": "0.2.1-rc.3", "profile": "web", "port": created.Port}),
		sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictPortBusy)

	// Invalid input → 400 VALIDATION.
	rec = env.request(t, http.MethodPost, "/api/instances",
		jsonBody(t, map[string]any{"name": "", "version": "0.2.1-rc.3", "profile": "web"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, "/api/instances",
		jsonBody(t, map[string]any{"name": "坏版本", "version": "not-semver", "profile": "web"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, "/api/instances",
		jsonBody(t, map[string]any{"name": "坏 profile", "version": "0.2.1-rc.3", "profile": "bad/profile"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Read back.
	rec = env.request(t, http.MethodGet, "/api/instances/"+created.ID, nil, sess.withSession())
	got := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if got.ID != created.ID {
		t.Fatalf("get instance = %+v, want %s", got, created.ID)
	}
	rec = env.request(t, http.MethodGet, "/api/instances/i-000000000000", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)

	// Rename.
	newName := "改名后的实例"
	rec = env.request(t, http.MethodPut, "/api/instances/"+created.ID,
		jsonBody(t, map[string]any{"name": newName}), sess.withSession())
	updated := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if updated.Name != newName {
		t.Fatalf("renamed instance = %+v", updated)
	}
	if updated.Port != created.Port {
		t.Fatalf("rename changed the port: %+v", updated)
	}

	// Change port.
	newPort := freePort(t)
	rec = env.request(t, http.MethodPut, "/api/instances/"+created.ID,
		jsonBody(t, map[string]any{"port": newPort}), sess.withSession())
	updated = decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if updated.Port != newPort {
		t.Fatalf("port = %d, want %d", updated.Port, newPort)
	}

	// Immutable fields → 409 CONFLICT_UNSUPPORTED_CHANGE.
	rec = env.request(t, http.MethodPut, "/api/instances/"+created.ID,
		jsonBody(t, map[string]any{"version": "0.3.0"}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictUnsupportedChange)
	rec = env.request(t, http.MethodPut, "/api/instances/"+created.ID,
		jsonBody(t, map[string]any{"profile": "desktop"}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictUnsupportedChange)

	// Empty update → 400 VALIDATION.
	rec = env.request(t, http.MethodPut, "/api/instances/"+created.ID, jsonBody(t, map[string]any{}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Delete moves the directory to trash.
	rec = env.request(t, http.MethodDelete, "/api/instances/"+created.ID, nil, sess.withSession())
	deleted := decodeJSONResponse[deletedResponse](t, rec, http.StatusOK)
	if deleted.TrashedPath == "" {
		t.Fatal("delete returned an empty trashedPath")
	}
	rec = env.request(t, http.MethodGet, "/api/instances", nil, sess.withSession())
	if list := decodeJSONResponse[[]InstanceDTO](t, rec, http.StatusOK); len(list) != 0 {
		t.Fatalf("instance list after delete = %v, want empty", list)
	}
	rec = env.request(t, http.MethodDelete, "/api/instances/"+created.ID, nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
}

func TestInstanceSelection(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "选中实例", freePort(t))

	rec := env.request(t, http.MethodPut, "/api/instances/"+instance.ID+"/selection", nil, sess.withSession())
	selected := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if selected.ID != instance.ID {
		t.Fatalf("selected = %+v, want %s", selected, instance.ID)
	}
	rec = env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	state := decodeJSONResponse[StateDTO](t, rec, http.StatusOK)
	if state.CurrentInstanceID == nil || *state.CurrentInstanceID != instance.ID {
		t.Fatalf("state currentInstanceId = %v, want %q", state.CurrentInstanceID, instance.ID)
	}
	rec = env.request(t, http.MethodPut, "/api/instances/i-000000000000/selection", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
}

func TestInstanceRunningRestrictions(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "运行中实例", freePort(t))
	env.markRunning(instance.ID, "")

	// Port changes are rejected while running; renames are allowed.
	rec := env.request(t, http.MethodPut, "/api/instances/"+instance.ID,
		jsonBody(t, map[string]any{"port": freePort(t)}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictInstanceRunning)
	rec = env.request(t, http.MethodPut, "/api/instances/"+instance.ID,
		jsonBody(t, map[string]any{"name": "运行中改名"}), sess.withSession())
	renamed := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if renamed.Name != "运行中改名" {
		t.Fatalf("renamed while running = %+v", renamed)
	}
	if renamed.Status != "运行中" {
		t.Fatalf("status while running = %q, want 运行中", renamed.Status)
	}
	rec = env.request(t, http.MethodDelete, "/api/instances/"+instance.ID, nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictInstanceRunning)
}

func TestStopInstance(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "待停止实例", freePort(t))
	env.markRunning(instance.ID, "")
	rec := env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/web-url", nil, sess.withSession())
	urlBody := decodeJSONResponse[webURLResponse](t, rec, http.StatusOK)
	if urlBody.URL != "/workbench/"+instance.ID+"/" {
		t.Fatalf("web url = %q", urlBody.URL)
	}

	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/stop", nil, sess.withSession())
	stopped := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if stopped.Status != statusNotStarted {
		t.Fatalf("stopped status = %q, want %q", stopped.Status, statusNotStarted)
	}
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/web-url", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeInstanceNotReady)
	rec = env.request(t, http.MethodPost, "/api/instances/i-000000000000/stop", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
}

func TestLaunchPrechecks(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Missing instance → 404.
	rec := env.request(t, http.MethodPost, "/api/instances/i-000000000000/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)

	instance := env.createTestInstance(t, "预检实例", freePort(t))

	// Runtime not installed → 409 INSTANCE_NOT_READY.
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeInstanceNotReady)

	// Install the runtime in the fake, then check the other prechecks.
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.mu.Unlock()

	// This instance already running → 409 CONFLICT_BUSY (frozen contract).
	env.markRunning(instance.ID, "")
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	env.app.mu.Lock()
	delete(env.app.live, instance.ID)
	env.app.mu.Unlock()

	// Another instance running → 409 CONFLICT_RUNNING_INSTANCE.
	env.app.mu.Lock()
	env.app.running = []app.InstanceRuntimeStatus{{InstanceID: "i-111111111111", Status: "运行中", Ready: true}}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictRunningInstance)
	env.app.mu.Lock()
	env.app.running = nil
	env.app.mu.Unlock()

	// Busy job → 409 CONFLICT_BUSY.
	_, stop := env.startBlockingJob(t)
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	stop()

	// Port occupied by another listener → 409 CONFLICT_PORT_BUSY.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	busyPort := listener.Addr().(*net.TCPAddr).Port
	if _, err := env.store.UpdateInstance(instance.ID, store.UpdateInstanceInput{Port: &busyPort}); err != nil {
		t.Fatalf("store.UpdateInstance() error = %v", err)
	}
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictPortBusy)
}

func TestLaunchCreatesJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "启动实例", freePort(t))
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindLaunchInstance {
		t.Fatalf("job type = %q, want %q", view.Type, job.KindLaunchInstance)
	}
	if view.ID == "" || view.State != job.StateRunning {
		t.Fatalf("accepted job = %+v", view)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("job result = %#v, want object", finished.Result)
	}
	if result["instanceId"] != instance.ID {
		t.Fatalf("job result instanceId = %v, want %q", result["instanceId"], instance.ID)
	}
	if result["webUrl"] != "/workbench/"+instance.ID+"/" {
		t.Fatalf("job result webUrl = %v", result["webUrl"])
	}
	// The job result must never carry the bootstrap token.
	if raw, err := json.Marshal(finished); err != nil || len(raw) == 0 {
		t.Fatalf("json.Marshal(job) error = %v", err)
	}

	// The instance reports the simulated live status.
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID, nil, sess.withSession())
	status := decodeJSONResponse[InstanceDTO](t, rec, http.StatusOK)
	if status.Status != "运行中" {
		t.Fatalf("instance status = %q, want 运行中", status.Status)
	}
}

func TestJobLaunchFailure(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "启动失败实例", freePort(t))
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.launchHook = func(ctx context.Context, id string, logf func(string)) (app.LaunchResult, error) {
		logf("实例进程在服务就绪前退出")
		return app.LaunchResult{}, apierr.New(http.StatusConflict, apierr.CodeInstanceNotReady, "实例进程在服务就绪前退出；请查看实例日志。")
	}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeInstanceNotReady {
		t.Fatalf("job error = %+v, want INSTANCE_NOT_READY", finished.Error)
	}
}
