// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/runtime"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

func TestListRuntimes(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodGet, "/api/runtimes", nil, sess.withSession())
	list := decodeJSONResponse[[]RuntimeDTO](t, rec, http.StatusOK)
	if len(list) != 0 {
		t.Fatalf("initial runtimes = %v, want empty", list)
	}
	if rec.Body.String() != "[]" {
		t.Fatalf("empty runtime body = %q, want []", rec.Body.String())
	}

	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{
		{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"},
		{Version: "0.2.0", Installed: true, Compatibility: "unverified"},
	}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/runtimes", nil, sess.withSession())
	list = decodeJSONResponse[[]RuntimeDTO](t, rec, http.StatusOK)
	if len(list) != 2 || list[0].Version != "0.2.1-rc.3" || list[1].Compatibility != "unverified" {
		t.Fatalf("runtimes = %+v", list)
	}
	if !list[0].Installed {
		t.Fatalf("runtime installed = false: %+v", list[0])
	}
}

func TestAvailableRuntimes(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodGet, "/api/runtimes/available", nil, sess.withSession())
	body := decodeJSONResponse[availableVersionsResponse](t, rec, http.StatusOK)
	if body.Versions == nil {
		t.Fatal("available versions must be an array, got null")
	}
	if len(body.Versions) != 0 {
		t.Fatalf("available versions = %v, want empty", body.Versions)
	}

	env.app.mu.Lock()
	env.app.available = []string{"0.2.1-rc.3", "0.2.0"}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/runtimes/available", nil, sess.withSession())
	body = decodeJSONResponse[availableVersionsResponse](t, rec, http.StatusOK)
	if len(body.Versions) != 2 || body.Versions[0] != "0.2.1-rc.3" {
		t.Fatalf("available versions = %v", body.Versions)
	}

	// Upstream failures pass through as documented 502/504.
	env.app.mu.Lock()
	env.app.availableErr = apierr.New(http.StatusBadGateway, apierr.CodeUpstreamError, "npm registry 不可达。")
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/runtimes/available", nil, sess.withSession())
	decodeError(t, rec, http.StatusBadGateway, apierr.CodeUpstreamError)
}

func TestInstallRuntimeJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Malformed / missing versions fail synchronously with 400.
	rec := env.request(t, http.MethodPost, "/api/runtimes", jsonBody(t, map[string]string{"version": "not-semver"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, "/api/runtimes", jsonBody(t, map[string]string{"version": ""}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	rec = env.request(t, http.MethodPost, "/api/runtimes", jsonBody(t, map[string]string{"version": "0.2.1-rc.3"}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindInstallRuntime {
		t.Fatalf("install job type = %q, want INSTALL_RUNTIME", view.Type)
	}
	if view.State != job.StateRunning {
		t.Fatalf("install job state = %q, want RUNNING", view.State)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("install result = %#v", finished.Result)
	}
	if result["version"] != "0.2.1-rc.3" {
		t.Fatalf("install result version = %v", result["version"])
	}
	if result["installed"] != true {
		t.Fatalf("install result installed = %v", result["installed"])
	}
	if result["compatibility"] != "verified" {
		t.Fatalf("install result compatibility = %v", result["compatibility"])
	}
	rec = env.request(t, http.MethodGet, "/api/runtimes", nil, sess.withSession())
	list := decodeJSONResponse[[]RuntimeDTO](t, rec, http.StatusOK)
	if len(list) != 1 || list[0].Version != "0.2.1-rc.3" {
		t.Fatalf("runtimes after install = %+v", list)
	}
}

func TestInstallRuntimeJobFailure(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	env.app.mu.Lock()
	env.app.installHook = func(ctx context.Context, version string, logf func(string)) (runtime.InstallResult, error) {
		logf("下载失败")
		return runtime.InstallResult{}, apierr.New(http.StatusBadGateway, apierr.CodeUpstreamError, "npm registry 不可达。")
	}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/runtimes", jsonBody(t, map[string]string{"version": "0.2.1-rc.3"}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeUpstreamError {
		t.Fatalf("install job error = %+v, want UPSTREAM_ERROR", finished.Error)
	}
}

func TestProbeRuntimeJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Malformed version → 400; not installed → 404.
	rec := env.request(t, http.MethodPost, "/api/runtimes/bogus/probe", nil, sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, "/api/runtimes/0.2.1-rc.3/probe", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)

	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/runtimes/0.2.1-rc.3/probe", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindProbeRuntime {
		t.Fatalf("probe job type = %q, want PROBE_RUNTIME", view.Type)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("probe result = %#v", finished.Result)
	}
	if result["version"] != "0.2.1-rc.3" || result["compatibility"] != "verified" {
		t.Fatalf("probe result = %v", result)
	}
}

func TestDeleteRuntime(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	// Malformed version → 404 (it can never be installed).
	rec := env.request(t, http.MethodDelete, "/api/runtimes/bogus", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)

	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodDelete, "/api/runtimes/0.2.1-rc.3", nil, sess.withSession())
	deleted := decodeJSONResponse[deletedResponse](t, rec, http.StatusOK)
	if deleted.TrashedPath == "" {
		t.Fatal("delete runtime returned an empty trashedPath")
	}
	rec = env.request(t, http.MethodGet, "/api/runtimes", nil, sess.withSession())
	if list := decodeJSONResponse[[]RuntimeDTO](t, rec, http.StatusOK); len(list) != 0 {
		t.Fatalf("runtimes after delete = %+v", list)
	}

	// A runtime referenced by an instance is protected (409 CONFLICT_IN_USE).
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.deleteRunErr = apierr.New(http.StatusConflict, apierr.CodeConflictInUse, "仍被实例使用。")
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodDelete, "/api/runtimes/0.2.1-rc.3", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictInUse)
}
