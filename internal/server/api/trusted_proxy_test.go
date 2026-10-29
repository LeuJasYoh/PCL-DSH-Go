// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// loopbackPeers used by the trusted-proxy tests.
const (
	loopbackPeer = "127.0.0.1:5555"
	ipv6Peer     = "[::1]:5555"
)

func TestClientIPTrustedProxy(t *testing.T) {
	base := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	}
	cases := []struct {
		name         string
		remoteAddr   string
		realIP       string
		forwardedFor string
		want         string
	}{
		{
			name:       "loopback without header keeps RemoteAddr",
			remoteAddr: loopbackPeer,
			want:       loopbackPeer,
		},
		{
			name:       "loopback trusts a valid X-Real-IP",
			remoteAddr: loopbackPeer,
			realIP:     "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "loopback trims surrounding whitespace",
			remoteAddr: loopbackPeer,
			realIP:     "  203.0.113.8\t",
			want:       "203.0.113.8",
		},
		{
			name:       "ipv6 loopback trusts an ipv4 X-Real-IP",
			remoteAddr: ipv6Peer,
			realIP:     "198.51.100.4",
			want:       "198.51.100.4",
		},
		{
			name:       "ipv6 loopback trusts an ipv6 X-Real-IP",
			remoteAddr: ipv6Peer,
			realIP:     "2001:db8::99",
			want:       "2001:db8::99",
		},
		{
			name:       "ipv4-mapped loopback peer is trusted",
			remoteAddr: "[::ffff:127.0.0.1]:1234",
			realIP:     "203.0.113.9",
			want:       "203.0.113.9",
		},
		{
			name:       "loopback peer without port is trusted",
			remoteAddr: "127.0.0.1",
			realIP:     "203.0.113.10",
			want:       "203.0.113.10",
		},
		{
			name:       "empty X-Real-IP falls back to RemoteAddr",
			remoteAddr: loopbackPeer,
			want:       loopbackPeer,
		},
		{
			name:       "invalid X-Real-IP falls back to RemoteAddr",
			remoteAddr: loopbackPeer,
			realIP:     "not-an-ip",
			want:       loopbackPeer,
		},
		{
			name:       "X-Real-IP with a port is ignored",
			remoteAddr: loopbackPeer,
			realIP:     "203.0.113.7:5555",
			want:       loopbackPeer,
		},
		{
			name:       "X-Real-IP with a DNS name is ignored",
			remoteAddr: loopbackPeer,
			realIP:     "example.com",
			want:       loopbackPeer,
		},
		{
			name:       "X-Real-IP with a control character is ignored",
			remoteAddr: loopbackPeer,
			realIP:     "203.0.113.7\x00",
			want:       loopbackPeer,
		},
		{
			name:       "X-Real-IP with an internal space is ignored",
			remoteAddr: loopbackPeer,
			realIP:     "203.0.113.7 198.51.100.1",
			want:       loopbackPeer,
		},
		{
			name:       "overlong X-Real-IP is ignored",
			remoteAddr: loopbackPeer,
			realIP:     strings.Repeat("9", 100),
			want:       loopbackPeer,
		},
		{
			name:       "non-loopback peer ignores X-Real-IP",
			remoteAddr: "198.51.100.5:9000",
			realIP:     "203.0.113.7",
			want:       "198.51.100.5:9000",
		},
		{
			name:       "private peer ignores X-Real-IP",
			remoteAddr: "10.0.0.5:9000",
			realIP:     "203.0.113.7",
			want:       "10.0.0.5:9000",
		},
		{
			name:       "wildcard peer ignores X-Real-IP",
			remoteAddr: "0.0.0.0:9000",
			realIP:     "203.0.113.7",
			want:       "0.0.0.0:9000",
		},
		{
			name:         "X-Forwarded-For is ignored for loopback peers",
			remoteAddr:   loopbackPeer,
			forwardedFor: "203.0.113.7",
			want:         loopbackPeer,
		},
		{
			name:         "X-Forwarded-For does not rescue an invalid X-Real-IP",
			remoteAddr:   loopbackPeer,
			realIP:       "bad",
			forwardedFor: "203.0.113.7",
			want:         loopbackPeer,
		},
		{
			name:         "X-Forwarded-For is ignored for remote peers",
			remoteAddr:   "198.51.100.6:9000",
			forwardedFor: "203.0.113.7",
			want:         "198.51.100.6:9000",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := base()
			request.RemoteAddr = testCase.remoteAddr
			if testCase.realIP != "" {
				request.Header.Set(realIPHeader, testCase.realIP)
			}
			if testCase.forwardedFor != "" {
				request.Header.Set("X-Forwarded-For", testCase.forwardedFor)
			}
			if got := clientIP(request); got != testCase.want {
				t.Fatalf("clientIP() = %q, want %q", got, testCase.want)
			}
		})
	}

	if got := clientIP(nil); got != "" {
		t.Fatalf("clientIP(nil) = %q, want empty", got)
	}
}

// loginViaProxy sends a login request from the given TCP peer with an optional
// X-Real-IP (and extra headers).
func loginViaProxy(t *testing.T, env *testEnv, password, remoteAddr, realIP string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return env.request(t, http.MethodPost, "/api/auth/login",
		jsonBody(t, map[string]string{"password": password}),
		func(r *http.Request) {
			if remoteAddr != "" {
				r.RemoteAddr = remoteAddr
			}
			if realIP != "" {
				r.Header.Set(realIPHeader, realIP)
			}
			for name, value := range extra {
				r.Header.Set(name, value)
			}
		})
}

// auditLog returns the concatenated audit log files of the test environment.
func auditLog(t *testing.T, logsDir string) string {
	t.Helper()
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", logsDir, err)
	}
	var builder strings.Builder
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "audit-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(logsDir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", entry.Name(), err)
		}
		builder.Write(data)
	}
	return builder.String()
}

func TestTrustedProxyLoginRateLimitIsolatedByRealIP(t *testing.T) {
	env := newTestEnv(t, false)
	const clientA = "203.0.113.10"
	const clientB = "203.0.113.11"

	// Five failures for client A arrive through the trusted loopback proxy.
	for attempt := 1; attempt <= 4; attempt++ {
		rec := loginViaProxy(t, env, "wrong-password", loopbackPeer, clientA, nil)
		decodeError(t, rec, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	rec := loginViaProxy(t, env, "wrong-password", loopbackPeer, clientA, nil)
	body := decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)
	if retry, ok := body.Details["retryAfterSeconds"].(float64); !ok || retry < 1 || retry > 60 {
		t.Fatalf("retryAfterSeconds = %#v, want 1-60", body.Details["retryAfterSeconds"])
	}

	// Client A stays locked even with the correct password.
	rec = loginViaProxy(t, env, testPassword, loopbackPeer, clientA, nil)
	decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)

	// Client B through the same proxy peer is a separate limiter bucket.
	rec = loginViaProxy(t, env, testPassword, loopbackPeer, clientB, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login for client B = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	// Direct loopback access without X-Real-IP uses the RemoteAddr bucket and
	// is independent of both header identities.
	rec = loginViaProxy(t, env, testPassword, "127.0.0.1:7777", "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("direct loopback login = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	// The audit trail records the real client IPs from X-Real-IP; the direct
	// (non-proxied) login is recorded under its own RemoteAddr.
	audit := auditLog(t, env.dirs.LogsDir)
	for _, want := range []string{"\"ip\":\"" + clientA + "\"", "\"ip\":\"" + clientB + "\""} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit missing %s: %s", want, audit)
		}
	}
	if strings.Contains(audit, "\"ip\":\"127.0.0.1:7777\"") {
		t.Fatalf("audit recorded a raw ip:port value: %s", audit)
	}
}

func TestTrustedProxyAuditCarriesRealIP(t *testing.T) {
	env := newTestEnv(t, false)
	rec := loginViaProxy(t, env, testPassword, loopbackPeer, "203.0.113.42", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login = %d, want 204", rec.Code)
	}
	rec = loginViaProxy(t, env, testPassword, ipv6Peer, "2001:db8::7", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ipv6 login = %d, want 204", rec.Code)
	}
	audit := auditLog(t, env.dirs.LogsDir)
	for _, want := range []string{"\"ip\":\"203.0.113.42\"", "\"ip\":\"2001:db8::7\""} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit missing %s: %s", want, audit)
		}
	}
}

func TestUntrustedPeerIgnoresRealIP(t *testing.T) {
	env := newTestEnv(t, false)
	const peer = "198.51.100.9:4444"

	// Each failed attempt forges a different X-Real-IP: if the header were
	// trusted it would create separate limiter buckets and never lock.
	for attempt := 1; attempt <= 4; attempt++ {
		forged := fmt.Sprintf("203.0.113.%d", 30+attempt)
		rec := loginViaProxy(t, env, "wrong-password", peer, forged, nil)
		decodeError(t, rec, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	rec := loginViaProxy(t, env, "wrong-password", peer, "203.0.113.39", nil)
	decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)

	// A fresh forged header cannot unlock the peer.
	rec = loginViaProxy(t, env, testPassword, peer, "203.0.113.99", nil)
	decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)

	// A different untrusted peer claiming the locked header value is free: the
	// limiter key is the TCP peer, not the header.
	rec = loginViaProxy(t, env, testPassword, "198.51.100.10:4444", "203.0.113.30", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("other peer login = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	// Analysis: the audit trail uses the peer address, never the forged header.
	audit := auditLog(t, env.dirs.LogsDir)
	if !strings.Contains(audit, "\"ip\":\"198.51.100.10\"") {
		t.Fatalf("audit missing the peer address: %s", audit)
	}
	if strings.Contains(audit, "\"ip\":\"203.0.113.30\"") {
		t.Fatalf("audit trusted a forged X-Real-IP from a remote peer: %s", audit)
	}
}

func TestXForwardedForIsIgnored(t *testing.T) {
	env := newTestEnv(t, false)

	// A loopback peer sending only X-Forwarded-For must keep using RemoteAddr.
	rec := loginViaProxy(t, env, testPassword, "127.0.0.1:6000", "",
		map[string]string{"X-Forwarded-For": "203.0.113.50"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login = %d, want 204", rec.Code)
	}
	// Rate limiting is keyed by RemoteAddr as well: five failures with varying
	// X-Forwarded-For still lock the loopback peer.
	for attempt := 1; attempt <= 4; attempt++ {
		rec = loginViaProxy(t, env, "wrong-password", "127.0.0.1:6000", "",
			map[string]string{"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", 60+attempt)})
		decodeError(t, rec, http.StatusUnauthorized, apierr.CodeLoginFailed)
	}
	rec = loginViaProxy(t, env, "wrong-password", "127.0.0.1:6000", "",
		map[string]string{"X-Forwarded-For": "203.0.113.69"})
	body := decodeError(t, rec, http.StatusLocked, apierr.CodeLoginLocked)
	if retry, ok := body.Details["retryAfterSeconds"].(float64); !ok || retry < 1 {
		t.Fatalf("retryAfterSeconds = %#v", body.Details["retryAfterSeconds"])
	}

	audit := auditLog(t, env.dirs.LogsDir)
	if !strings.Contains(audit, "\"ip\":\"127.0.0.1\"") {
		t.Fatalf("audit missing the RemoteAddr ip: %s", audit)
	}
	if strings.Contains(audit, "203.0.113.50") {
		t.Fatalf("X-Forwarded-For leaked into the audit trail: %s", audit)
	}
}
