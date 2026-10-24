// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

func TestBusyGate(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "忙碌实例", freePort(t))
	_, stop := env.startBlockingJob(t)
	defer stop()

	// Reads stay available.
	for _, path := range []string{"/api/state", "/api/instances", "/api/jobs", "/api/settings", "/api/runtimes"} {
		rec := env.request(t, http.MethodGet, path, nil, sess.withSession())
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s while busy = %d, want 200", path, rec.Code)
		}
	}

	// Other writes are rejected with 409 CONFLICT_BUSY.
	writeRequests := []struct {
		method string
		path   string
		body   []byte
	}{
		{http.MethodPost, "/api/instances", jsonBody(t, map[string]any{"name": "x", "version": "0.2.1-rc.3", "profile": "web"})},
		{http.MethodPut, "/api/settings", jsonBody(t, map[string]string{"proxy": "", "registry": "", "directAccessSshUser": "root"})},
		{http.MethodDelete, "/api/instances/" + instance.ID, nil},
		{http.MethodPost, "/api/instances/" + instance.ID + "/stop", nil},
	}
	for _, request := range writeRequests {
		rec := env.request(t, request.method, request.path, request.body, sess.withSession())
		decodeError(t, rec, http.StatusConflict, apierr.CodeConflictBusy)
	}

	// Auth endpoints and job cancellation stay usable while busy.
	rec := env.request(t, http.MethodGet, "/api/auth/me", nil, sess.withSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("me while busy = %d, want 200", rec.Code)
	}
	rec = env.request(t, http.MethodPost, "/api/auth/login", jsonBody(t, map[string]string{"password": testPassword}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login while busy = %d, want 204", rec.Code)
	}
	rec = env.request(t, http.MethodPost, "/api/jobs/"+env.lastJobID(t)+"/cancel", nil, sess.withSession())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel while busy = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
}

// lastJobID returns the id of the most recent job of the environment.
func (e *testEnv) lastJobID(t *testing.T) string {
	t.Helper()
	list := e.jobs.List()
	if len(list) == 0 {
		t.Fatal("no job in the manager")
	}
	return list[0].ID
}
