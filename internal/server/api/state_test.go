// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
)

func TestStateAggregate(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	first := env.createTestInstance(t, "实例一", freePort(t))
	second := env.createTestInstance(t, "实例二", freePort(t))
	if err := env.store.SelectInstance(first.ID); err != nil {
		t.Fatalf("SelectInstance() error = %v", err)
	}
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.available = []string{"0.2.1-rc.3"}
	env.app.live[second.ID] = app.LiveStatus{
		InstanceID:    second.ID,
		StatusCode:    "NOT_READY",
		StatusMessage: "进程运行中，服务未就绪",
		Ready:         false,
	}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	state := decodeJSONResponse[StateDTO](t, rec, http.StatusOK)
	if state.Version != testVersion {
		t.Fatalf("state version = %q", state.Version)
	}
	if state.CurrentInstanceID == nil || *state.CurrentInstanceID != first.ID {
		t.Fatalf("state currentInstanceId = %v, want %q", state.CurrentInstanceID, first.ID)
	}
	if len(state.Instances) != 2 {
		t.Fatalf("state instances = %+v", state.Instances)
	}
	if state.Instances[0].Status != statusNotStarted {
		t.Fatalf("first instance status = %q, want %q", state.Instances[0].Status, statusNotStarted)
	}
	if state.Instances[1].Status != "进程运行中，服务未就绪" {
		t.Fatalf("second instance status = %q", state.Instances[1].Status)
	}
	if len(state.Runtimes) != 1 || state.Runtimes[0].Compatibility != "verified" {
		t.Fatalf("state runtimes = %+v", state.Runtimes)
	}
	if len(state.AvailableVersions) != 1 {
		t.Fatalf("state availableVersions = %+v", state.AvailableVersions)
	}
	if state.Warnings == nil || len(state.Warnings) != 0 {
		t.Fatalf("state warnings = %#v, want []", state.Warnings)
	}
	if state.Status.Code != "READY" || state.Status.Message != "就绪" {
		t.Fatalf("state status = %+v", state.Status)
	}
	if state.Settings.DirectAccessSSHUser != "root" {
		t.Fatalf("state settings = %+v", state.Settings)
	}
}

func TestStateBusyStatus(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	_, stop := env.startBlockingJob(t)
	defer stop()

	// Wait until the blocking task has entered its phase (the job goroutine
	// may still be in the initial "准备中" phase for a few microseconds).
	deadline := time.Now().Add(2 * time.Second)
	for {
		views := env.jobs.List()
		if len(views) > 0 && views[0].Phase == "阻塞中" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("blocking job did not enter its phase: %+v", views)
		}
		time.Sleep(2 * time.Millisecond)
	}

	rec := env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	state := decodeJSONResponse[StateDTO](t, rec, http.StatusOK)
	if state.Status.Code != "RUNNING" {
		t.Fatalf("busy status code = %q, want RUNNING", state.Status.Code)
	}
	if state.Status.Message != "进行中：阻塞中" {
		t.Fatalf("busy status message = %q, want 进行中：阻塞中", state.Status.Message)
	}
}
