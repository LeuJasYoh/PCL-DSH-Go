// SPDX-License-Identifier: MIT

package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/directaccess"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/store"
)

func TestDirectAccessRequiresInstance(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodGet, "/api/instances/i-000000000000/direct-access", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, "NOT_FOUND")

	// Anonymous requests are rejected before the instance lookup.
	instance := env.createTestInstance(t, "直连实例", freePort(t))
	rec = env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/direct-access", nil)
	decodeError(t, rec, http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestDirectAccessNotRunning(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "未运行实例", freePort(t))

	rec := env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/direct-access", nil, sess.withSession(),
		func(r *http.Request) { r.Host = "192.168.1.10:18080" })
	dto := decodeJSONResponse[directaccess.DTO](t, rec, http.StatusOK)
	if dto.Ready {
		t.Fatalf("direct access ready = true for a stopped instance: %+v", dto)
	}
	if dto.DirectURL != nil {
		t.Fatalf("directUrl = %v, want null", *dto.DirectURL)
	}
	if dto.Port != instance.Port {
		t.Fatalf("port = %d, want %d", dto.Port, instance.Port)
	}
	if dto.SSHHost != "192.168.1.10" {
		t.Fatalf("sshHost = %q, want 192.168.1.10", dto.SSHHost)
	}
	if dto.SSHUser != "root" {
		t.Fatalf("sshUser = %q, want root", dto.SSHUser)
	}
	port := strconv.Itoa(instance.Port)
	wantTunnel := "ssh -L " + port + ":127.0.0.1:" + port + " root@192.168.1.10"
	if dto.TunnelCommand != wantTunnel {
		t.Fatalf("tunnelCommand = %q, want %q", dto.TunnelCommand, wantTunnel)
	}
	if dto.Note == "" {
		t.Fatal("direct access note is empty")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestDirectAccessReadyCarriesTokenOnlyInBody(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "运行实例", freePort(t))
	env.markRunning(instance.ID, "token=SECRET-TOKEN-123")
	// A custom SSH user comes from settings.
	if err := env.store.UpdateSettings(store.Settings{DirectAccessSSHUser: "deploy"}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	rec := env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/direct-access", nil, sess.withSession(),
		func(r *http.Request) {
			r.Host = "192.168.1.10:18080"
			r.Header.Set("X-Forwarded-Host", "evil.example")
		})
	dto := decodeJSONResponse[directaccess.DTO](t, rec, http.StatusOK)
	if !dto.Ready {
		t.Fatalf("direct access ready = false: %+v", dto)
	}
	if dto.DirectURL == nil {
		t.Fatal("directUrl is null for a ready instance with a bootstrap token")
	}
	wantURL := "http://127.0.0.1:" + strconv.Itoa(instance.Port) + "/?token=SECRET-TOKEN-123"
	if *dto.DirectURL != wantURL {
		t.Fatalf("directUrl = %q, want %q", *dto.DirectURL, wantURL)
	}
	if dto.SSHHost != "192.168.1.10" {
		t.Fatalf("sshHost = %q (X-Forwarded-Host must be ignored)", dto.SSHHost)
	}
	if dto.SSHUser != "deploy" {
		t.Fatalf("sshUser = %q, want deploy", dto.SSHUser)
	}
	if !strings.Contains(dto.TunnelCommand, "deploy@192.168.1.10") {
		t.Fatalf("tunnelCommand = %q", dto.TunnelCommand)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	// The token must only appear in the response body: the manager log (access
	// log + error log) must never contain it.
	if strings.Contains(env.logs.String(), "SECRET-TOKEN-123") {
		t.Fatal("manager log leaked the bootstrap token")
	}
}

func TestDirectAccessInvalidHost(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "主机异常实例", freePort(t))
	rec := env.request(t, http.MethodGet, "/api/instances/"+instance.ID+"/direct-access", nil, sess.withSession(),
		func(r *http.Request) { r.Host = "" })
	decodeError(t, rec, http.StatusBadRequest, "BAD_REQUEST")
}
