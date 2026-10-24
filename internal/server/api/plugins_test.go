// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/plugin"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

func TestListPlugins(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件列表实例", freePort(t))

	// Empty list is an array, not null.
	rec := env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/plugins", nil, sess.withSession())
	plugins := decodeJSONResponse[[]plugin.DTO](t, rec, http.StatusOK)
	if plugins == nil || len(plugins) != 0 {
		t.Fatalf("plugins = %#v, want []", plugins)
	}
	env.plugins.mu.Lock()
	defaultProfile := env.plugins.lastProfile
	seenInstance := env.plugins.lastInstanceID
	env.plugins.mu.Unlock()
	if seenInstance != instance.ID {
		t.Fatalf("plugin List instance = %q, want %q", seenInstance, instance.ID)
	}
	if defaultProfile != "web" {
		t.Fatalf("plugin List default profile = %q, want web", defaultProfile)
	}

	// Rows are passed through with the documented PluginDto shape.
	env.plugins.mu.Lock()
	env.plugins.list = []plugin.DTO{
		{Name: "@scope/alpha", Version: "1.2.3", Description: "alpha plugin", Source: "社区"},
		{Name: "dsh-ui", Version: "内置", Description: "内置 bundle", Source: "内置"},
	}
	env.plugins.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/plugins?profile=custom", nil, sess.withSession())
	plugins = decodeJSONResponse[[]plugin.DTO](t, rec, http.StatusOK)
	if len(plugins) != 2 || plugins[0].Name != "@scope/alpha" || plugins[1].Source != "内置" {
		t.Fatalf("plugins = %+v", plugins)
	}
	env.plugins.mu.Lock()
	queryProfile := env.plugins.lastProfile
	env.plugins.mu.Unlock()
	if queryProfile != "custom" {
		t.Fatalf("plugin List query profile = %q, want custom", queryProfile)
	}

	// Instance missing → 404; invalid profile comes from the plugin service → 400.
	rec = env.request(t, http.MethodGet, "/api/instances/i-000000000000/plugins", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
	env.plugins.mu.Lock()
	env.plugins.listErr = apierr.New(http.StatusBadRequest, apierr.CodeValidation, "profile 参数无效。")
	env.plugins.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/plugins?profile=../x", nil, sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Anonymous → 401.
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/plugins", nil)
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
}

func TestAddPluginJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件安装实例", freePort(t))

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/plugins",
		jsonBody(t, map[string]string{"spec": "foo@1.2.3"}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindPluginMutate {
		t.Fatalf("job type = %q, want PLUGIN_MUTATE", view.Type)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok {
		t.Fatalf("job result = %#v, want object", finished.Result)
	}
	if result["instanceId"] != instance.ID || result["profile"] != "web" || result["action"] != "add" {
		t.Fatalf("job result = %v", result)
	}
	if result["name"] != "test-plugin" {
		t.Fatalf("job result name = %v", result["name"])
	}
	env.plugins.mu.Lock()
	lastMutate := env.plugins.lastMutate
	env.plugins.mu.Unlock()
	if lastMutate.InstanceID != instance.ID || lastMutate.Profile != "web" ||
		lastMutate.Action != "add" || lastMutate.Spec != "foo@1.2.3" {
		t.Fatalf("plugin Mutate input = %+v", lastMutate)
	}

	// The SSE stream ends with the result event.
	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID+"/events", nil, sess.withSession())
	events := parseSSE(t, rec.Body.String())
	if len(events) == 0 || events[len(events)-1].Name != "result" {
		t.Fatalf("SSE events = %+v, want a terminal result event", events)
	}
	payload := decodeEventData(t, events[len(events)-1])
	eventResult, ok := payload["result"].(map[string]any)
	if !ok || eventResult["action"] != "add" {
		t.Fatalf("result event data = %v", payload)
	}
}

func TestUpdateAndRemovePluginJobs(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件更新实例", freePort(t))

	// PATCH performs an in-place update: the native CLI gets no package spec.
	rec := env.request(t, http.MethodPatch, "/api/instances/"+instance.ID+"/plugins/foo", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.Type != job.KindPluginMutate {
		t.Fatalf("job type = %q, want PLUGIN_MUTATE", view.Type)
	}
	env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	env.plugins.mu.Lock()
	lastMutate := env.plugins.lastMutate
	env.plugins.mu.Unlock()
	if lastMutate.Action != "update" || lastMutate.Spec != "" || lastMutate.Profile != "web" {
		t.Fatalf("update Mutate input = %+v", lastMutate)
	}

	// DELETE removes the full npm package name from the path.
	rec = env.request(t, http.MethodDelete, "/api/instances/"+instance.ID+"/plugins/left-pad", nil, sess.withSession())
	view = decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	result, ok := finished.Result.(map[string]any)
	if !ok || result["action"] != "remove" {
		t.Fatalf("remove job result = %#v", finished.Result)
	}
	env.plugins.mu.Lock()
	lastMutate = env.plugins.lastMutate
	env.plugins.mu.Unlock()
	if lastMutate.Action != "remove" || lastMutate.Spec != "left-pad" {
		t.Fatalf("remove Mutate input = %+v", lastMutate)
	}

	// Scoped package names keep their slash in the {name...} wildcard.
	rec = env.request(t, http.MethodDelete, "/api/instances/"+instance.ID+"/plugins/@scope/pkg", nil, sess.withSession())
	scopedView := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, scopedView.ID, job.StateSucceeded)
	env.plugins.mu.Lock()
	lastMutate = env.plugins.lastMutate
	env.plugins.mu.Unlock()
	if lastMutate.Spec != "@scope/pkg" {
		t.Fatalf("scoped remove spec = %q, want @scope/pkg", lastMutate.Spec)
	}

	// Percent-encoded scoped names are decoded before they reach the service.
	rec = env.request(t, http.MethodDelete, "/api/instances/"+instance.ID+"/plugins/%40scope%2Fencoded", nil, sess.withSession())
	encodedView := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, encodedView.ID, job.StateSucceeded)
	env.plugins.mu.Lock()
	lastMutate = env.plugins.lastMutate
	env.plugins.mu.Unlock()
	if lastMutate.Spec != "@scope/encoded" {
		t.Fatalf("percent-encoded remove spec = %q, want @scope/encoded", lastMutate.Spec)
	}
}

func TestPluginMutationPrechecks(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件校验实例", freePort(t))
	base := "/api/instances/" + instance.ID + "/plugins"

	// Empty / command-like / oversized specs fail synchronously (no job).
	rec := env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": "-evil"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": strings.Repeat("x", 215)}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	rec = env.request(t, http.MethodDelete, base+"/-evil", nil, sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Unknown instance → 404; anonymous → 401; missing CSRF → 403.
	rec = env.request(t, http.MethodPost, "/api/instances/i-000000000000/plugins",
		jsonBody(t, map[string]string{"spec": "foo"}), sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": "foo"}))
	decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": "foo"}), sess.cookiesOnly())
	decodeError(t, rec, http.StatusForbidden, apierr.CodeCSRFInvalid)

	// A running instance is rejected synchronously with 409.
	env.markRunning(instance.ID, "")
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": "foo"}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictInstanceRunning)
	rec = env.request(t, http.MethodDelete, base+"/foo", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictInstanceRunning)
	env.app.mu.Lock()
	delete(env.app.live, instance.ID)
	env.app.mu.Unlock()

	// Busy gate: plugin mutations are jobs, other writes are rejected.
	_, stop := env.startBlockingJob(t)
	defer stop()
	rec = env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": "foo"}), sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	rec = env.request(t, http.MethodPatch, base+"/foo", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
}

func TestPluginMutationFailure(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件失败实例", freePort(t))
	env.plugins.mu.Lock()
	env.plugins.mutateErr = apierr.New(http.StatusUnprocessableEntity, apierr.CodeUnsupportedRuntime,
		"此 Harness 版本未声明原生 plugin 管理命令。")
	env.plugins.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/plugins",
		jsonBody(t, map[string]string{"spec": "foo"}), sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeUnsupportedRuntime {
		t.Fatalf("job error = %+v, want UNSUPPORTED_RUNTIME", finished.Error)
	}
	if finished.Error.Details == nil {
		t.Fatal("job error details must be an object")
	}
}

// maliciousPluginSpecs are the instance-independent invalid mutations that must
// be rejected synchronously with 400 VALIDATION before a Job is created
// (testdata/plugin/malicious-specs.json and docs/M3_INTERFACES.md 5).
func TestPluginSpecValidationIsSynchronous(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件负向校验实例", freePort(t))
	base := "/api/instances/" + instance.ID + "/plugins"

	beforeJobs := len(env.jobs.List())
	env.plugins.mu.Lock()
	beforeMutates := env.plugins.mutateCalls
	env.plugins.mu.Unlock()

	addCases := []string{
		"-evil",
		"evil\x01pkg",
		strings.Repeat("a", 215),
		"@bad//name",
		"file:../../outside",
		"file:/etc/passwd",
		"link:../outside",
		"file:C:/Windows/win.ini",
		"file://server/share",
		"bad name with space",
	}
	for _, spec := range addCases {
		rec := env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": spec}), sess.withSession())
		body := decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
		if body.TraceID == "" {
			t.Fatalf("spec %q: missing traceId", spec)
		}
		if len(env.jobs.List()) != beforeJobs {
			t.Fatalf("spec %q: a job was created for an invalid spec", spec)
		}
		if env.jobs.Busy() {
			t.Fatalf("spec %q: job manager busy after invalid spec", spec)
		}
	}

	// Invalid profile is rejected synchronously too.
	rec := env.request(t, http.MethodPost, base+"?profile=..%2Fevil",
		jsonBody(t, map[string]string{"spec": "@acme/plugin"}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)

	// Remove requires a complete npm package name.
	removeCases := []string{"/@bad", "/not%20a%20package", "/file%3A%2Fetc%2Fpasswd"}
	for _, suffix := range removeCases {
		rec := env.request(t, http.MethodDelete, base+suffix, nil, sess.withSession())
		decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	}

	if len(env.jobs.List()) != beforeJobs {
		t.Fatal("invalid mutations created jobs")
	}
	env.plugins.mu.Lock()
	afterMutates := env.plugins.mutateCalls
	env.plugins.mu.Unlock()
	if afterMutates != beforeMutates {
		t.Fatalf("plugin.Mutate calls = %d, want %d (no Job for invalid input)", afterMutates, beforeMutates)
	}
}

func TestPluginValidSpecsStillAccepted(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件正向校验实例", freePort(t))
	base := "/api/instances/" + instance.ID + "/plugins"

	validAdds := []string{
		"@acme/plugin-demo",
		"lodash@4.17.21",
		"github:acme/plugin",
		"npm:lodash@4",
		"acme/plugin",
		"file:./local-plugin",
		"link:./local-plugin",
	}
	for _, spec := range validAdds {
		rec := env.request(t, http.MethodPost, base, jsonBody(t, map[string]string{"spec": spec}), sess.withSession())
		view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
		if view.Type != job.KindPluginMutate {
			t.Fatalf("spec %q: job type = %q", spec, view.Type)
		}
		env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	}

	// Update (no spec) and remove (full npm name) keep working.
	rec := env.request(t, http.MethodPatch, base+"/@acme/plugin-demo", nil, sess.withSession())
	updateView := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, updateView.ID, job.StateSucceeded)

	rec = env.request(t, http.MethodDelete, base+"/dep-one", nil, sess.withSession())
	removeView := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, removeView.ID, job.StateSucceeded)
}

// TestPluginInstanceDependentValidationStaysInJob documents the boundary: checks
// that need the instance filesystem (built-in bundle protection, removable
// dependency) still fail the Job with error.code=VALIDATION, not INTERNAL.
func TestPluginInstanceDependentValidationStaysInJob(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "插件实例依赖校验", freePort(t))
	env.plugins.mu.Lock()
	env.plugins.mutateErr = apierr.New(http.StatusBadRequest, apierr.CodeValidation, "内置 bundle 不能移除。")
	env.plugins.mu.Unlock()

	rec := env.request(t, http.MethodDelete,
		"/api/instances/"+instance.ID+"/plugins/@deepseek-ai/dsh-base", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	finished := env.waitForJobState(t, sess, view.ID, job.StateFailed)
	if finished.Error == nil || finished.Error.Code != apierr.CodeValidation {
		t.Fatalf("job error = %+v, want VALIDATION", finished.Error)
	}
}
