// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/gorilla/websocket"
)

// fakeSessionGate mirrors the M1 Authorizer injected by internal/server:
//
//   - anonymous            -> *apierr.Error 401 (proxy maps to 401 UNAUTHORIZED)
//   - initial password set -> proxy.ErrForbidden (proxy maps to 403 FORBIDDEN)
//   - authenticated        -> nil (forwarded)
//
// The proxy intentionally has no session store: /workbench login enforcement is
// the server's responsibility, injected through proxy.New.
func fakeSessionGate(state string) Authorizer {
	return func(*http.Request) error {
		switch state {
		case "anonymous":
			return apierr.Unauthorized()
		case "pending":
			return ErrForbidden
		default:
			return nil
		}
	}
}

func TestWorkbenchLoginGateHTTP(t *testing.T) {
	tests := []struct {
		name       string
		authorize  Authorizer
		wantStatus int
		wantCode   string
		wantHit    bool
	}{
		{
			name:       "anonymous is rejected with 401",
			authorize:  fakeSessionGate("anonymous"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   codeUnauthorized,
		},
		{
			name:       "plain authorizer error is also 401",
			authorize:  func(*http.Request) error { return errors.New("no session") },
			wantStatus: http.StatusUnauthorized,
			wantCode:   codeUnauthorized,
		},
		{
			name:       "initial password pending is rejected with 403",
			authorize:  fakeSessionGate("pending"),
			wantStatus: http.StatusForbidden,
			wantCode:   codeForbidden,
		},
		{
			name:       "authenticated session is forwarded",
			authorize:  fakeSessionGate("authenticated"),
			wantStatus: http.StatusOK,
			wantHit:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &requestRecorder{}
			upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
			_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), test.authorize)

			sessionHeader := http.Header{
				"Cookie": {"JSESSIONID=console-session; XSRF-TOKEN=csrf-token"},
			}
			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test/", nil, sessionHeader)
			if test.wantHit {
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", response.StatusCode)
				}
				if recorder.count() != 1 {
					t.Fatalf("upstream requests = %d, want 1", recorder.count())
				}
				return
			}
			assertProxyError(t, response, test.wantStatus, test.wantCode)
			if recorder.count() != 0 {
				t.Fatalf("upstream must not be reached, got %d requests", recorder.count())
			}
		})
	}
}

func TestWorkbenchLoginGateAppliesToAllMethods(t *testing.T) {
	// The login gate runs before any forwarding decision (including the
	// trailing-slash redirect), so no HTTP method can bypass it.
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), fakeSessionGate("anonymous"))
	client := noRedirectClient()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := sendRequest(t, client, method, proxyServer.URL+"/workbench/i-test/page", nil, nil)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", method, response.StatusCode)
		}
		if method == http.MethodHead {
			continue
		}
		if got := decodeProxyError(t, response).Code; got != codeUnauthorized {
			t.Fatalf("%s code = %q, want %q", method, got, codeUnauthorized)
		}
	}

	// The same gate also protects the trailing-slash redirect endpoint.
	redirect := sendRequest(t, client, http.MethodGet, proxyServer.URL+"/workbench/i-test", nil, nil)
	if redirect.StatusCode != http.StatusUnauthorized {
		t.Fatalf("redirect endpoint status = %d, want 401 (auth runs first)", redirect.StatusCode)
	}
}

func TestWorkbenchLoginGateIsInjectedByServer(t *testing.T) {
	// Without an Authorizer the proxy forwards requests: the /workbench session
	// check is not implicit in the proxy package, it is injected by the server.
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a nil Authorizer", response.StatusCode)
	}
	if recorder.count() != 1 {
		t.Fatalf("upstream requests = %d, want 1", recorder.count())
	}
}

func TestWorkbenchAuthorizerReceivesFullRequest(t *testing.T) {
	var seenMethod, seenPath, seenQuery, seenHost string
	authorize := func(r *http.Request) error {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenHost = r.Host
		return nil
	}
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), authorize)
	response := sendRequest(t, noRedirectClient(), http.MethodPost,
		proxyServer.URL+"/workbench/i-test/api/echo?token=abc", strings.NewReader("body"), dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if seenMethod != http.MethodPost || seenPath != "/workbench/i-test/api/echo" || seenQuery != "token=abc" {
		t.Fatalf("authorizer saw %s %s?%s", seenMethod, seenPath, seenQuery)
	}
	if seenHost == "" {
		t.Fatal("authorizer must receive the request Host")
	}
}

func TestWorkbenchLoginGateWebSocket(t *testing.T) {
	tests := []struct {
		name       string
		authorize  Authorizer
		wantStatus int
		wantCode   string
		wantOK     bool
	}{
		{
			name:       "anonymous handshake is rejected",
			authorize:  fakeSessionGate("anonymous"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   codeUnauthorized,
		},
		{
			name:       "pending password handshake is forbidden",
			authorize:  fakeSessionGate("pending"),
			wantStatus: http.StatusForbidden,
			wantCode:   codeForbidden,
		},
		{
			name:      "authenticated handshake is upgraded",
			authorize: fakeSessionGate("authenticated"),
			wantOK:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
			_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), test.authorize)
			conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
			if test.wantOK {
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				t.Cleanup(func() { conn.Close() })
				if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
					t.Fatalf("write: %v", err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatalf("deadline: %v", err)
				}
				_, payload, err := conn.ReadMessage()
				if err != nil || string(payload) != "hello" {
					t.Fatalf("echo = %q, %v", payload, err)
				}
				return
			}
			if err == nil {
				conn.Close()
				t.Fatal("handshake must be rejected")
			}
			if response == nil {
				t.Fatalf("expected an HTTP response, got %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			body, _ := io.ReadAll(response.Body)
			if !strings.Contains(string(body), test.wantCode) {
				t.Fatalf("body = %q, want %q", body, test.wantCode)
			}
			if observation.snapshot().upgraded {
				t.Fatal("upstream must not be dialed for a rejected handshake")
			}
		})
	}
}
