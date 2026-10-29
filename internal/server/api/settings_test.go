// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

func TestSettings(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)

	rec := env.request(t, http.MethodGet, "/api/settings", nil, sess.withSession())
	settings := decodeJSONResponse[SettingsDTO](t, rec, http.StatusOK)
	if settings.Registry != "https://registry.npmjs.org" {
		t.Fatalf("default registry = %q", settings.Registry)
	}
	if settings.DirectAccessSSHUser != "root" {
		t.Fatalf("default ssh user = %q, want root", settings.DirectAccessSSHUser)
	}
	if settings.Proxy != "" {
		t.Fatalf("default proxy = %q, want empty", settings.Proxy)
	}

	// Update all fields.
	rec = env.request(t, http.MethodPut, "/api/settings", jsonBody(t, map[string]string{
		"proxy":               "",
		"registry":            "https://registry.example.test",
		"directAccessSshUser": "deploy",
	}), sess.withSession())
	updated := decodeJSONResponse[SettingsDTO](t, rec, http.StatusOK)
	if updated.Registry != "https://registry.example.test" || updated.DirectAccessSSHUser != "deploy" {
		t.Fatalf("updated settings = %+v", updated)
	}
	rec = env.request(t, http.MethodGet, "/api/settings", nil, sess.withSession())
	persisted := decodeJSONResponse[SettingsDTO](t, rec, http.StatusOK)
	if persisted.DirectAccessSSHUser != "deploy" {
		t.Fatalf("settings not persisted: %+v", persisted)
	}

	// Validation failures surface as 400 VALIDATION.
	env.app.mu.Lock()
	env.app.settingsErr = apierr.New(http.StatusBadRequest, apierr.CodeValidation, "directAccessSshUser 不合法。")
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodPut, "/api/settings", jsonBody(t, map[string]string{
		"proxy":               "",
		"registry":            "https://registry.npmjs.org",
		"directAccessSshUser": "bad user",
	}), sess.withSession())
	decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
}

func TestLogsTail(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	env.app.mu.Lock()
	env.app.logs = []string{"line-1", "line-2"}
	env.app.mu.Unlock()

	// Default lines = 800.
	rec := env.request(t, http.MethodGet, "/api/logs/tail", nil, sess.withSession())
	body := decodeJSONResponse[logsTailResponse](t, rec, http.StatusOK)
	if len(body.Lines) != 2 || body.Lines[0] != "line-1" {
		t.Fatalf("tail lines = %v", body.Lines)
	}
	env.app.mu.Lock()
	seen := append([]int(nil), env.app.logsTailSeen...)
	env.app.mu.Unlock()
	if len(seen) != 1 || seen[0] != logsTailDefault {
		t.Fatalf("LogsTail lines = %v, want [%d]", seen, logsTailDefault)
	}

	// Explicit lines value is forwarded.
	rec = env.request(t, http.MethodGet, "/api/logs/tail?lines=5", nil, sess.withSession())
	decodeJSONResponse[logsTailResponse](t, rec, http.StatusOK)
	env.app.mu.Lock()
	seen = append([]int(nil), env.app.logsTailSeen...)
	env.app.mu.Unlock()
	if len(seen) != 2 || seen[1] != 5 {
		t.Fatalf("LogsTail lines = %v, want second call 5", seen)
	}

	// Out-of-range / malformed values are 400 VALIDATION (never clamped).
	for _, query := range []string{"?lines=0", "?lines=-1", "?lines=2001", "?lines=abc"} {
		rec = env.request(t, http.MethodGet, "/api/logs/tail"+query, nil, sess.withSession())
		decodeError(t, rec, http.StatusBadRequest, apierr.CodeValidation)
	}

	// Internal failures are generic 500s.
	env.app.mu.Lock()
	env.app.logsErr = errors.New("open /secret/logs: permission denied")
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodGet, "/api/logs/tail", nil, sess.withSession())
	body500 := decodeError(t, rec, http.StatusInternalServerError, apierr.CodeInternal)
	if body500.Message != "服务器内部错误。" {
		t.Fatalf("500 message = %q", body500.Message)
	}
}
