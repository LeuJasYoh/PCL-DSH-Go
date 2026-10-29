// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

func TestHealthAnonymous(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/health", nil)
	health := decodeJSONResponse[healthResponse](t, rec, http.StatusOK)
	if health.Status != "UP" {
		t.Fatalf("health status = %q, want UP", health.Status)
	}
	if health.Version != testVersion {
		t.Fatalf("health version = %q, want %q", health.Version, testVersion)
	}
	if health.UptimeSeconds < 89 {
		t.Fatalf("uptimeSeconds = %d, want >= 89", health.UptimeSeconds)
	}
	assertSecurityHeaders(t, rec)
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// assertSecurityHeaders checks the trace id and the fixed security headers.
func assertSecurityHeaders(t *testing.T, rec interface {
	Header() http.Header
}) {
	t.Helper()
	header := rec.Header()
	traceID := header.Get("X-Trace-Id")
	if !regexp.MustCompile("^[0-9a-f]{12}$").MatchString(traceID) {
		t.Fatalf("X-Trace-Id = %q, want 12 lowercase hex characters", traceID)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestUnauthorizedRoutes(t *testing.T) {
	env := newTestEnv(t, false)
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/state"},
		{http.MethodGet, "/api/instances"},
		{http.MethodGet, "/api/instances/i-aaaaaaaaaaaa"},
		{http.MethodGet, "/api/instances/i-aaaaaaaaaaaa/web-url"},
		{http.MethodGet, "/api/instances/i-aaaaaaaaaaaa/direct-access"},
		{http.MethodGet, "/api/runtimes"},
		{http.MethodGet, "/api/runtimes/available"},
		{http.MethodGet, "/api/settings"},
		{http.MethodGet, "/api/logs/tail"},
		{http.MethodGet, "/api/jobs"},
		{http.MethodPost, "/api/instances"},
		{http.MethodPost, "/api/runtimes"},
		{http.MethodPut, "/api/settings"},
		{http.MethodDelete, "/api/instances/i-aaaaaaaaaaaa"},
		{http.MethodPost, "/api/jobs/j-aaaaaaaaaaaa/cancel"},
	}
	for _, route := range routes {
		rec := env.request(t, route.method, route.path, nil)
		body := decodeError(t, rec, http.StatusUnauthorized, apierr.CodeUnauthorized)
		if len(body.Details) != 0 {
			t.Fatalf("%s %s details = %v, want {}", route.method, route.path, body.Details)
		}
		if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("%s %s Content-Type = %q, want JSON", route.method, route.path, contentType)
		}
		assertSecurityHeaders(t, rec)
	}
}

func TestUnknownAPIRouteReturnsJSON404(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodGet, "/api/does-not-exist", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
}

func TestRootPlaceholder(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("root Content-Type = %q, want text/html", contentType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "PCL-DSH-Go") || !strings.Contains(body, testVersion) {
		t.Fatalf("root page missing title/version: %q", body)
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	env := newTestEnv(t, false)
	huge := "{\"password\":\"" + strings.Repeat("a", (1<<20)+16) + "\"}"
	rec := env.request(t, http.MethodPost, "/api/auth/login", []byte(huge))
	decodeError(t, rec, http.StatusRequestEntityTooLarge, apierr.CodePayloadTooLarge)
}

func TestMalformedJSONBodies(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	cases := []struct {
		name string
		body []byte
	}{
		{"not json", []byte("this is not json")},
		{"empty", nil},
		{"unknown field", jsonBody(t, map[string]any{"name": "x", "unknownField": true})},
	}
	for _, testCase := range cases {
		rec := env.request(t, http.MethodPost, "/api/instances", testCase.body, sess.withSession())
		decodeError(t, rec, http.StatusBadRequest, apierr.CodeBadRequest)
	}
}

func TestUnknownErrorIsGeneric500(t *testing.T) {
	env := newTestEnv(t, false)
	env.app.availableErr = errors.New("upstream detail at /secret/internal/path")
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodGet, "/api/runtimes/available", nil, sess.withSession())
	body := decodeError(t, rec, http.StatusInternalServerError, apierr.CodeInternal)
	if strings.Contains(body.Message, "secret") || strings.Contains(body.Message, "upstream") {
		t.Fatalf("500 message leaks internals: %q", body.Message)
	}
	// The original error only goes to the manager log.
	if !strings.Contains(env.logs.String(), "api request failed") {
		t.Fatalf("expected the original error to be logged: %s", env.logs.String())
	}
}

func TestAccessLogHasNoQueryOrToken(t *testing.T) {
	env := newTestEnv(t, false)
	env.request(t, http.MethodGet, "/api/health?token=SECRET-QUERY-VALUE", nil)
	logs := env.logs.String()
	if strings.Contains(logs, "SECRET-QUERY-VALUE") {
		t.Fatalf("access log leaked the query string: %s", logs)
	}
	if !strings.Contains(logs, "\"path\":\"/api/health\"") {
		t.Fatalf("access log missing the request line: %s", logs)
	}
}

func TestMethodNotAllowedOnKnownRoute(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodPost, "/api/auth/me", nil, sess.withSession())
	body := decodeError(t, rec, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow header = %q, want GET", got)
	}
	if !strings.Contains(body.Message, "GET") {
		t.Fatalf("405 message = %q, want the allowed methods", body.Message)
	}
}

func TestResponseCacheControl(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	rec := env.request(t, http.MethodGet, "/api/state", nil, sess.withSession())
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}
