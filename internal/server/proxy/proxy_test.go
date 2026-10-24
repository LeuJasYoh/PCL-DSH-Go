// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// resolverFunc adapts a function to the Resolver interface.
type resolverFunc func(ctx context.Context, instanceID string) (Target, error)

func (f resolverFunc) Resolve(ctx context.Context, instanceID string) (Target, error) {
	return f(ctx, instanceID)
}

// recordedRequest is a snapshot of a request received by a mock upstream.
type recordedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Host     string
	Header   http.Header
	Body     string
}

// requestRecorder is a thread-safe recorder of upstream requests.
type requestRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *requestRecorder) handler(w http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	r.mu.Lock()
	r.requests = append(r.requests, recordedRequest{
		Method:   request.Method,
		Path:     request.URL.Path,
		RawQuery: request.URL.RawQuery,
		Host:     request.Host,
		Header:   request.Header.Clone(),
		Body:     string(body),
	})
	r.mu.Unlock()
	w.Header().Set("X-Upstream", "yes")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "upstream-ok")
}

func (r *requestRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *requestRecorder) last(t *testing.T) recordedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		t.Fatal("upstream received no request")
	}
	return r.requests[len(r.requests)-1]
}

func newUpstream(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func upstreamTarget(t *testing.T, server *httptest.Server) Target {
	t.Helper()
	address, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", server.Listener.Addr())
	}
	return Target{InstanceID: "i-test", Port: address.Port}
}

func staticResolver(target Target, targetErr error) Resolver {
	return resolverFunc(func(context.Context, string) (Target, error) {
		return target, targetErr
	})
}

func newProxyHandler(t *testing.T, resolver Resolver, authorize Authorizer) (*Handler, *httptest.Server) {
	t.Helper()
	handler := New(slog.New(slog.DiscardHandler), resolver, authorize)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return handler, server
}

func noRedirectClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sendRequest(t *testing.T, client *http.Client, method, url string, body io.Reader, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

// sendRequestWithHost is sendRequest with an overridden Host header (used to
// exercise default-port Origin normalization without changing the dial target).
func sendRequestWithHost(t *testing.T, client *http.Client, method, url string, body io.Reader, header http.Header, host string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	request.Host = host
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func bodyString(t *testing.T, response *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(data)
}

func decodeProxyError(t *testing.T, response *http.Response) errorResponse {
	t.Helper()
	var parsed errorResponse
	if err := json.Unmarshal([]byte(bodyString(t, response)), &parsed); err != nil {
		t.Fatalf("decode proxy error: %v", err)
	}
	return parsed
}

func assertProxyError(t *testing.T, response *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", response.StatusCode, wantStatus)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
	}
	parsed := decodeProxyError(t, response)
	if parsed.Code != wantCode {
		t.Fatalf("error code = %q, want %q", parsed.Code, wantCode)
	}
	if parsed.Message == "" {
		t.Fatal("error message must not be empty")
	}
	if parsed.Details == nil || len(parsed.Details) != 0 {
		t.Fatalf("details = %v, want an empty object", parsed.Details)
	}
}

func dshAuthHeader() http.Header {
	return http.Header{"Cookie": {"dsh-auth-test=1"}}
}

// synchronizedBuffer collects log output; slog handlers may still write from
// server goroutines while a test reads the buffer.
type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestTrailingSlashRedirect(t *testing.T) {
	resolverCalled := false
	resolver := resolverFunc(func(context.Context, string) (Target, error) {
		resolverCalled = true
		return Target{}, nil
	})
	_, proxyServer := newProxyHandler(t, resolver, nil)
	client := noRedirectClient()

	tests := []struct {
		path     string
		location string
	}{
		{path: "/workbench/i-test", location: "/workbench/i-test/"},
		{path: "/workbench/i-test?a=1&b=2", location: "/workbench/i-test/?a=1&b=2"},
	}
	for _, test := range tests {
		response := sendRequest(t, client, http.MethodGet, proxyServer.URL+test.path, nil, nil)
		if response.StatusCode != http.StatusFound {
			t.Fatalf("%s status = %d, want 302", test.path, response.StatusCode)
		}
		if got := response.Header.Get("Location"); got != test.location {
			t.Fatalf("%s Location = %q, want %q", test.path, got, test.location)
		}
		if got := response.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", test.path, got)
		}
	}
	if resolverCalled {
		t.Fatal("resolver must not be called for a trailing-slash redirect")
	}
}

func TestAuthorizationRunsBeforeTrailingSlashRedirect(t *testing.T) {
	authorize := func(*http.Request) error { return errors.New("not logged in") }
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 1}, nil), authorize)
	response := sendRequest(t, noRedirectClient(), http.MethodGet, proxyServer.URL+"/workbench/i-test", nil, nil)
	assertProxyError(t, response, http.StatusUnauthorized, codeUnauthorized)
}

func TestBootstrapRedirect(t *testing.T) {
	var logBuffer synchronizedBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	target := Target{InstanceID: "i-test", Port: 3080, BootstrapQuery: "token=SUPER_SECRET_BOOTSTRAP_TOKEN"}
	handler := New(logger, staticResolver(target, nil), nil)
	proxyServer := httptest.NewServer(handler)
	t.Cleanup(proxyServer.Close)

	response := sendRequest(t, noRedirectClient(), http.MethodGet, proxyServer.URL+"/workbench/i-test/", nil, nil)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.StatusCode)
	}
	if got, want := response.Header.Get("Location"), "/workbench/i-test/?token=SUPER_SECRET_BOOTSTRAP_TOKEN"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if strings.Contains(logBuffer.String(), "SUPER_SECRET_BOOTSTRAP_TOKEN") {
		t.Fatal("bootstrap token leaked into logs")
	}
}

func TestBootstrapSkipped(t *testing.T) {
	tests := []struct {
		name           string
		bootstrapQuery string
		query          string
		header         http.Header
	}{
		{name: "dsh auth cookie present", bootstrapQuery: "token=bootstrap", header: dshAuthHeader()},
		{name: "token in query", bootstrapQuery: "token=bootstrap", query: "?token=query-token"},
		{name: "cookie and token", bootstrapQuery: "token=bootstrap", query: "?a=1&token=query-token", header: dshAuthHeader()},
		{name: "no bootstrap query avoids redirect loop", bootstrapQuery: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &requestRecorder{}
			upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
			target := upstreamTarget(t, upstream)
			target.BootstrapQuery = test.bootstrapQuery
			_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)

			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test/"+test.query, nil, test.header)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (no bootstrap redirect)", response.StatusCode)
			}
			if location := response.Header.Get("Location"); location != "" {
				t.Fatalf("unexpected Location %q", location)
			}
			if recorder.count() != 1 {
				t.Fatalf("upstream request count = %d, want 1", recorder.count())
			}
		})
	}
}

func TestAuthorizerMapping(t *testing.T) {
	tests := []struct {
		name       string
		authorize  Authorizer
		wantStatus int
		wantCode   string
		wantHit    bool
	}{
		{name: "allowed", authorize: func(*http.Request) error { return nil }, wantStatus: http.StatusOK, wantHit: true},
		{name: "unauthorized", authorize: func(*http.Request) error { return errors.New("no session") },
			wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized},
		{name: "forbidden", authorize: func(*http.Request) error { return ErrForbidden },
			wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "wrapped forbidden", authorize: func(*http.Request) error {
			return fmt.Errorf("policy: %w", ErrForbidden)
		}, wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "nil authorizer allows", authorize: nil, wantStatus: http.StatusOK, wantHit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &requestRecorder{}
			upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
			_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), test.authorize)
			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
			if test.wantHit {
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", response.StatusCode)
				}
				if recorder.count() != 1 {
					t.Fatalf("upstream request count = %d, want 1", recorder.count())
				}
				return
			}
			assertProxyError(t, response, test.wantStatus, test.wantCode)
			if recorder.count() != 0 {
				t.Fatalf("upstream must not be called, got %d requests", recorder.count())
			}
		})
	}
}

func TestResolveErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		targetErr  error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", targetErr: ErrInstanceNotFound, wantStatus: http.StatusNotFound, wantCode: codeInstanceNotFound},
		{name: "wrapped not found", targetErr: fmt.Errorf("lookup: %w", ErrInstanceNotFound),
			wantStatus: http.StatusNotFound, wantCode: codeInstanceNotFound},
		{name: "not running", targetErr: ErrInstanceNotRunning, wantStatus: http.StatusConflict, wantCode: codeInstanceNotReady},
		{name: "not ready", targetErr: ErrInstanceNotReady, wantStatus: http.StatusConflict, wantCode: codeInstanceNotReady},
		{name: "unknown error", targetErr: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: codeInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, test.targetErr), nil)
			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
			assertProxyError(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func TestPathValidation(t *testing.T) {
	paths := []string{
		"/workbench/../etc/passwd",
		"/workbench/%2e%2e/etc/passwd",
		"/workbench/i-test/../secret",
		"/workbench/i-test/a%2F..%2F..%2Fetc",
		"/workbench/i-test/a%00b",
		"/workbench//assets",
		"/workbench/%20/assets",
		"/workbench/i-test/%2e%2e%2fsecret",
	}
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	client := noRedirectClient()
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			response := sendRequest(t, client, http.MethodGet, proxyServer.URL+path, nil, dshAuthHeader())
			assertProxyError(t, response, http.StatusBadRequest, codeBadRequest)
		})
	}
	if recorder.count() != 0 {
		t.Fatalf("upstream must not be called for invalid paths, got %d requests", recorder.count())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), nil)
	client := noRedirectClient()
	for _, method := range []string{http.MethodOptions, http.MethodTrace} {
		response := sendRequest(t, client, method, proxyServer.URL+"/workbench/i-test/page", nil, nil)
		assertProxyError(t, response, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	}
}

func TestNotFoundOutsideWorkbench(t *testing.T) {
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), nil)
	client := noRedirectClient()
	for _, path := range []string{"/", "/api/health", "/workbench"} {
		response := sendRequest(t, client, http.MethodGet, proxyServer.URL+path, nil, nil)
		assertProxyError(t, response, http.StatusNotFound, codeNotFound)
	}
}

func TestHTTPForwarding(t *testing.T) {
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	target := upstreamTarget(t, upstream)
	target.BootstrapPath = "/"
	target.BootstrapQuery = "token=bootstrap-token"
	_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)
	client := noRedirectClient()

	header := http.Header{}
	header.Set("Cookie", "JSESSIONID=console-session; XSRF-TOKEN=console-csrf; dsh-auth-a=1; theme=dark")
	header.Set("Origin", proxyServer.URL) // same-origin write request
	header.Set("Referer", proxyServer.URL+"/")
	header.Set("X-XSRF-TOKEN", "console-csrf")
	header.Set("Forwarded", "for=1.2.3.4")
	header.Set("X-Forwarded-For", "1.2.3.4")
	header.Set("X-Forwarded-Proto", "https")
	header.Set("Accept", "application/json")
	header.Set("Authorization", "Bearer token")
	header.Set("X-Custom", "value")

	response := sendRequest(t, client, http.MethodPost,
		proxyServer.URL+"/workbench/i-test/api/echo?q=1&x=%20", strings.NewReader("hello-body"), header)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := bodyString(t, response); got != "upstream-ok" {
		t.Fatalf("body = %q, want upstream-ok", got)
	}
	if got := response.Header.Get("X-Upstream"); got != "yes" {
		t.Fatalf("X-Upstream = %q, want yes", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	recorded := recorder.last(t)
	if recorded.Method != http.MethodPost {
		t.Fatalf("upstream method = %q, want POST", recorded.Method)
	}
	if recorded.Path != "/api/echo" {
		t.Fatalf("upstream path = %q, want /api/echo", recorded.Path)
	}
	if recorded.RawQuery != "q=1&x=%20" {
		t.Fatalf("upstream raw query = %q, want q=1&x=%%20", recorded.RawQuery)
	}
	if wantHost := fmt.Sprintf("127.0.0.1:%d", target.Port); recorded.Host != wantHost {
		t.Fatalf("upstream Host = %q, want %q", recorded.Host, wantHost)
	}
	if recorded.Body != "hello-body" {
		t.Fatalf("upstream body = %q, want hello-body", recorded.Body)
	}
	if got := recorded.Header.Get("Cookie"); got != "dsh-auth-a=1; theme=dark" {
		t.Fatalf("upstream Cookie = %q", got)
	}
	if wantOrigin := fmt.Sprintf("http://127.0.0.1:%d", target.Port); recorded.Header.Get("Origin") != wantOrigin {
		t.Fatalf("upstream Origin = %q, want %q", recorded.Header.Get("Origin"), wantOrigin)
	}
	for _, name := range []string{"Referer", "X-XSRF-TOKEN", "Forwarded", "X-Forwarded-For", "X-Forwarded-Proto"} {
		if value := recorded.Header.Get(name); value != "" {
			t.Fatalf("upstream received blocked header %s = %q", name, value)
		}
	}
	if got := recorded.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("upstream Accept = %q", got)
	}
	if got := recorded.Header.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("upstream Authorization = %q", got)
	}
	if got := recorded.Header.Get("X-Custom"); got != "value" {
		t.Fatalf("upstream X-Custom = %q", got)
	}
}

func TestResponseHeaderRewrites(t *testing.T) {
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Add("Set-Cookie", "dsh-auth-a=v; Path=/; Domain=127.0.0.1; Secure; HttpOnly; SameSite=Strict; Max-Age=2592000")
		w.Header().Add("Set-Cookie", "other=b; Path=/; SameSite=None")
		w.Header().Set("Location", "http://"+r.Host+"/next?x=1")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "body")
	}))
	target := upstreamTarget(t, upstream)
	_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())

	if got := response.Header.Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Fatalf("Cache-Control = %v, want [no-store]", got)
	}
	if got := response.Header.Get("Expires"); got != "" {
		t.Fatalf("Expires = %q, want empty", got)
	}
	if got := response.Header.Get("Pragma"); got != "" {
		t.Fatalf("Pragma = %q, want empty", got)
	}
	cookies := response.Header.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("Set-Cookie count = %d, want 2: %v", len(cookies), cookies)
	}
	if want := "dsh-auth-a=v; HttpOnly; SameSite=Strict; Max-Age=2592000; Path=/workbench/i-test"; cookies[0] != want {
		t.Fatalf("Set-Cookie[0] = %q, want %q", cookies[0], want)
	}
	if want := "other=b; SameSite=Lax; Path=/workbench/i-test"; cookies[1] != want {
		t.Fatalf("Set-Cookie[1] = %q, want %q", cookies[1], want)
	}
	if want := "/workbench/i-test/next?x=1"; response.Header.Get("Location") != want {
		t.Fatalf("Location = %q, want %q", response.Header.Get("Location"), want)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := bodyString(t, response); got != "body" {
		t.Fatalf("body = %q, want body", got)
	}
}

func TestLocationRewrites(t *testing.T) {
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var location string
		switch r.URL.Path {
		case "/rel":
			location = "./next"
		case "/abs":
			location = "/login?next=%2F"
		case "/loop":
			location = "http://" + r.Host + "/cb?code=1"
		case "/ext":
			location = "https://example.com/x"
		default:
			location = "/"
		}
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	client := noRedirectClient()
	tests := []struct {
		path string
		want string
	}{
		{path: "/workbench/i-test/rel", want: "./next"},
		{path: "/workbench/i-test/abs", want: "/workbench/i-test/login?next=%2F"},
		{path: "/workbench/i-test/loop", want: "/workbench/i-test/cb?code=1"},
		{path: "/workbench/i-test/ext", want: "https://example.com/x"},
	}
	for _, test := range tests {
		response := sendRequest(t, client, http.MethodGet, proxyServer.URL+test.path, nil, dshAuthHeader())
		if response.StatusCode != http.StatusFound {
			t.Fatalf("%s status = %d, want 302", test.path, response.StatusCode)
		}
		if got := response.Header.Get("Location"); got != test.want {
			t.Fatalf("%s Location = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestDefaultTimeoutConfiguration(t *testing.T) {
	handler := New(slog.New(slog.DiscardHandler), staticResolver(Target{Port: 1}, nil), nil)
	if got := handler.transport.ResponseHeaderTimeout; got != 60*time.Second {
		t.Fatalf("default upstream header timeout = %v, want 60s", got)
	}
	if got := handler.wsHandshake; got != defaultWSHandshakeTimeout {
		t.Fatalf("default ws handshake timeout = %v, want %v", got, defaultWSHandshakeTimeout)
	}
	handler.SetUpstreamTimeout(5 * time.Millisecond)
	if got := handler.transport.ResponseHeaderTimeout; got != 5*time.Millisecond {
		t.Fatalf("upstream header timeout after setter = %v, want 5ms", got)
	}
	handler.SetWSHandshakeTimeout(7 * time.Millisecond)
	if got := handler.wsHandshake; got != 7*time.Millisecond {
		t.Fatalf("ws handshake timeout after setter = %v, want 7ms", got)
	}
}

func TestResponseHopByHopHeadersStripped(t *testing.T) {
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Proxy-Authenticate", "Basic realm=test")
		w.Header().Set("X-Kept", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "body")
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
	for _, name := range []string{"Keep-Alive", "Proxy-Authenticate"} {
		if got := response.Header.Get(name); got != "" {
			t.Fatalf("hop-by-hop response header %s leaked as %q", name, got)
		}
	}
	if got := response.Header.Get("X-Kept"); got != "yes" {
		t.Fatalf("end-to-end response header X-Kept = %q, want yes", got)
	}
}

func TestSSEStreaming(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSecond := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseSecond)

	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: one\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-release
		_, _ = io.WriteString(w, "data: two\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)

	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/plugins/events", nil, dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	reader := bufio.NewReader(response.Body)
	lines := make(chan string, 16)
	readErrors := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readErrors <- err
				return
			}
			lines <- line
		}
	}()

	waitLine := func(want string) {
		t.Helper()
		select {
		case got := <-lines:
			if got != want {
				t.Fatalf("SSE line = %q, want %q", got, want)
			}
		case err := <-readErrors:
			t.Fatalf("SSE read failed before %q: %v", want, err)
		case <-time.After(3 * time.Second):
			t.Fatalf("SSE line %q was not flushed in time", want)
		}
	}
	waitLine("data: one\n")
	waitLine("\n")
	// Only release the second event after the first one reached the client:
	// this proves the proxy flushed instead of buffering the whole response.
	releaseSecond()
	waitLine("data: two\n")
}

func TestUpstreamConnectFailure502(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	_, proxyServer := newProxyHandler(t, staticResolver(Target{InstanceID: "i-test", Port: closedPort}, nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/", nil, dshAuthHeader())
	assertProxyError(t, response, http.StatusBadGateway, codeUpstreamError)
}

func TestUpstreamTimeout504(t *testing.T) {
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	handler := New(slog.New(slog.DiscardHandler), staticResolver(upstreamTarget(t, upstream), nil), nil)
	// Configure before the server starts so no request can observe the change.
	handler.SetUpstreamTimeout(50 * time.Millisecond)
	proxyServer := httptest.NewServer(handler)
	t.Cleanup(proxyServer.Close)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/slow", nil, dshAuthHeader())
	assertProxyError(t, response, http.StatusGatewayTimeout, codeUpstreamTimeout)
}

func TestHEADNoBody(t *testing.T) {
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodHead,
		proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if body := bodyString(t, response); body != "" {
		t.Fatalf("HEAD body = %q, want empty", body)
	}
	if got := recorder.last(t).Method; got != http.MethodHead {
		t.Fatalf("upstream method = %q, want HEAD", got)
	}
}

func TestGETBodyDropped(t *testing.T) {
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/page", strings.NewReader("dropped-body"), dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := recorder.last(t).Body; got != "" {
		t.Fatalf("upstream body = %q, want empty (GET bodies are dropped)", got)
	}
}

func TestBootstrapPathJoining(t *testing.T) {
	tests := []struct {
		name          string
		bootstrapPath string
		requestPath   string
		wantPath      string
	}{
		{name: "empty base defaults to root", bootstrapPath: "", requestPath: "/assets/app.js", wantPath: "/assets/app.js"},
		{name: "root base", bootstrapPath: "/", requestPath: "/assets/app.js", wantPath: "/assets/app.js"},
		{name: "base with trailing slash", bootstrapPath: "/app/", requestPath: "/assets/app.js", wantPath: "/app/assets/app.js"},
		{name: "base without trailing slash", bootstrapPath: "/app", requestPath: "/assets/app.js", wantPath: "/app/assets/app.js"},
		{name: "root request with base", bootstrapPath: "/app/", requestPath: "/", wantPath: "/app/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &requestRecorder{}
			upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
			target := upstreamTarget(t, upstream)
			target.BootstrapPath = test.bootstrapPath
			_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)
			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test"+test.requestPath, nil, dshAuthHeader())
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			if got := recorder.last(t).Path; got != test.wantPath {
				t.Fatalf("upstream path = %q, want %q", got, test.wantPath)
			}
		})
	}
}

func TestInvalidTargetPortRejected(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			_, proxyServer := newProxyHandler(t, staticResolver(Target{InstanceID: "i-test", Port: port}, nil), nil)
			response := sendRequest(t, noRedirectClient(), http.MethodGet,
				proxyServer.URL+"/workbench/i-test/", nil, dshAuthHeader())
			assertProxyError(t, response, http.StatusBadGateway, codeUpstreamRejected)
		})
	}
}

// TestRealDSHHTTPFlow mirrors the observed DSH 0.2.1-rc.3 flow: token
// bootstrap redirect, upstream 303 + Set-Cookie, cookie-carrying page load and
// relative static/plugin asset requests (base href="./").
func TestRealDSHHTTPFlow(t *testing.T) {
	pluginQueries := make(chan string, 1)
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.URL.Query().Get("token") != "":
			w.Header().Set("Set-Cookie", "dsh-auth-abc=v1; Path=/; HttpOnly; SameSite=Strict; Max-Age=2592000")
			w.Header().Set("Location", "./")
			w.WriteHeader(http.StatusSeeOther)
		case r.Header.Get("Cookie") == "dsh-auth-abc=v1" && r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "<html>workbench</html>")
		case r.Header.Get("Cookie") == "dsh-auth-abc=v1" && r.URL.Path == "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "console.log(1)")
		case r.Header.Get("Cookie") == "dsh-auth-abc=v1" && r.URL.Path == "/plugins/":
			select {
			case pluginQueries <- r.URL.RawQuery:
			default:
			}
			w.Header().Set("Content-Type", "text/javascript")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "plugin")
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	target := upstreamTarget(t, upstream)
	target.BootstrapQuery = "token=BOOTSTRAP_43_CHARS"
	_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)
	client := noRedirectClient()

	// 1) First navigation: no DSH cookie, no token -> bootstrap redirect.
	bootstrap := sendRequest(t, client, http.MethodGet, proxyServer.URL+"/workbench/i-test/", nil, nil)
	if bootstrap.StatusCode != http.StatusFound {
		t.Fatalf("bootstrap status = %d, want 302", bootstrap.StatusCode)
	}
	if got, want := bootstrap.Header.Get("Location"), "/workbench/i-test/?token=BOOTSTRAP_43_CHARS"; got != want {
		t.Fatalf("bootstrap Location = %q, want %q", got, want)
	}

	// 2) Token is consumed by DSH: 303 Location "./" plus the dsh-auth cookie.
	exchange := sendRequest(t, client, http.MethodGet, proxyServer.URL+bootstrap.Header.Get("Location"), nil, nil)
	if exchange.StatusCode != http.StatusSeeOther {
		t.Fatalf("token exchange status = %d, want 303", exchange.StatusCode)
	}
	if got := exchange.Header.Get("Location"); got != "./" {
		t.Fatalf("token exchange Location = %q, want ./ (relative is kept)", got)
	}
	cookies := exchange.Header.Values("Set-Cookie")
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1: %v", len(cookies), cookies)
	}
	wantCookie := "dsh-auth-abc=v1; HttpOnly; SameSite=Strict; Max-Age=2592000; Path=/workbench/i-test"
	if cookies[0] != wantCookie {
		t.Fatalf("Set-Cookie = %q, want %q", cookies[0], wantCookie)
	}

	cookieHeader := http.Header{"Cookie": {"dsh-auth-abc=v1"}}
	// 3) Page load with the DSH cookie.
	page := sendRequest(t, client, http.MethodGet, proxyServer.URL+"/workbench/i-test/", nil, cookieHeader)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.StatusCode)
	}
	if got := bodyString(t, page); got != "<html>workbench</html>" {
		t.Fatalf("page body = %q (no HTML rewriting expected)", got)
	}
	// 4) Relative static asset.
	asset := sendRequest(t, client, http.MethodGet, proxyServer.URL+"/workbench/i-test/assets/app.js", nil, cookieHeader)
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", asset.StatusCode)
	}
	if got := bodyString(t, asset); got != "console.log(1)" {
		t.Fatalf("asset body = %q", got)
	}
	// 5) Plugin module URLs carry the DSH "??pkg/...&rev=..." query verbatim.
	plugin := sendRequest(t, client, http.MethodGet,
		proxyServer.URL+"/workbench/i-test/plugins/??pkg/client.js&rev=42", nil, cookieHeader)
	if plugin.StatusCode != http.StatusOK {
		t.Fatalf("plugin status = %d, want 200", plugin.StatusCode)
	}
	select {
	case query := <-pluginQueries:
		if query != "?pkg/client.js&rev=42" {
			t.Fatalf("upstream plugin query = %q, want ?pkg/client.js&rev=42", query)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive the plugin request")
	}
	if got := bodyString(t, plugin); got != "plugin" {
		t.Fatalf("plugin body = %q", got)
	}
}

func TestLogsDoNotContainSecrets(t *testing.T) {
	var logBuffer synchronizedBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	target := upstreamTarget(t, upstream)
	target.BootstrapQuery = "token=BOOTSTRAP_SECRET_TOKEN"
	handler := New(logger, staticResolver(target, nil), nil)
	proxyServer := httptest.NewServer(handler)
	t.Cleanup(proxyServer.Close)

	client := noRedirectClient()
	// 1) Token bootstrap redirect (uses the in-memory bootstrap token).
	bootstrap := sendRequest(t, client, http.MethodGet, proxyServer.URL+"/workbench/i-test/", nil, nil)
	if bootstrap.StatusCode != http.StatusFound {
		t.Fatalf("bootstrap status = %d, want 302", bootstrap.StatusCode)
	}
	// 2) Forwarded request carrying a query token plus a DSH cookie.
	requestHeader := http.Header{}
	requestHeader.Set("Cookie", "dsh-auth-a=COOKIE_SECRET_VALUE")
	forwarded := sendRequest(t, client, http.MethodGet,
		proxyServer.URL+"/workbench/i-test/page?token=QUERY_SECRET_TOKEN", nil, requestHeader)
	if forwarded.StatusCode != http.StatusOK {
		t.Fatalf("forwarded status = %d, want 200", forwarded.StatusCode)
	}

	logs := logBuffer.String()
	for _, secret := range []string{"BOOTSTRAP_SECRET_TOKEN", "COOKIE_SECRET_VALUE", "QUERY_SECRET_TOKEN"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log output leaked secret %q:\n%s", secret, logs)
		}
	}
}

// httpCRLF is used by the raw-socket tests below to build HTTP requests and
// responses without relying on net/http rewriting the headers under test.
const httpCRLF = "\r\n"

func TestOriginSameOriginForWriteMethods(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		origin     string
		sameOrigin bool
		host       string
		wantStatus int
		wantCode   string
		wantHit    bool
	}{
		{name: "same-origin POST is forwarded", method: http.MethodPost, sameOrigin: true, wantStatus: http.StatusOK, wantHit: true},
		{name: "same-origin PUT is forwarded", method: http.MethodPut, sameOrigin: true, wantStatus: http.StatusOK, wantHit: true},
		{name: "cross-origin POST is rejected", method: http.MethodPost, origin: "http://evil.example", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "cross-origin PUT is rejected", method: http.MethodPut, origin: "http://evil.example", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "cross-origin PATCH is rejected", method: http.MethodPatch, origin: "http://evil.example", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "cross-origin DELETE is rejected", method: http.MethodDelete, origin: "http://evil.example", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "null origin is rejected", method: http.MethodPost, origin: "null", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "same host different port is rejected", method: http.MethodPost, host: "console.example", origin: "http://console.example:8080", wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{name: "http default port is normalized", method: http.MethodPost, host: "console.example", origin: "http://console.example", wantStatus: http.StatusOK, wantHit: true},
		{name: "https default port is normalized", method: http.MethodPost, host: "console.example", origin: "https://console.example", wantStatus: http.StatusOK, wantHit: true},
		{name: "no origin is allowed", method: http.MethodPost, wantStatus: http.StatusOK, wantHit: true},
		{name: "cross-origin GET is not gated", method: http.MethodGet, origin: "http://evil.example", wantStatus: http.StatusOK, wantHit: true},
		{name: "cross-origin HEAD is not gated", method: http.MethodHead, origin: "http://evil.example", wantStatus: http.StatusOK, wantHit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &requestRecorder{}
			upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
			_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
			header := http.Header{}
			header.Set("Cookie", "dsh-auth-route=1")
			origin := test.origin
			if test.sameOrigin {
				origin = proxyServer.URL
			}
			if origin != "" {
				header.Set("Origin", origin)
			}
			var response *http.Response
			if test.host != "" {
				response = sendRequestWithHost(t, noRedirectClient(), test.method,
					proxyServer.URL+"/workbench/i-test/page", nil, header, test.host)
			} else {
				response = sendRequest(t, noRedirectClient(), test.method,
					proxyServer.URL+"/workbench/i-test/page", nil, header)
			}
			if test.wantHit {
				if response.StatusCode != test.wantStatus {
					t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
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

func TestDynamicHopByHopRequestHeaders(t *testing.T) {
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)

	conn, err := net.Dial("tcp", proxyServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	requestLines := []string{
		"GET /workbench/i-test/page HTTP/1.1",
		"Host: " + proxyServer.Listener.Addr().String(),
		"Cookie: dsh-auth-route=1",
		"Connection: X-Hop-Header, X-Second-Hop",
		"X-Hop-Header: secret-one",
		"X-Second-Hop: secret-two",
		"X-Kept: yes",
		"",
		"",
	}
	if _, err := conn.Write([]byte(strings.Join(requestLines, httpCRLF))); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	_, _ = io.ReadAll(response.Body)
	response.Body.Close()

	recorded := recorder.last(t)
	for _, name := range []string{"Connection", "X-Hop-Header", "X-Second-Hop"} {
		if got := recorded.Header.Get(name); got != "" {
			t.Fatalf("connection-nominated header %s leaked upstream: %q", name, got)
		}
	}
	if got := recorded.Header.Get("X-Kept"); got != "yes" {
		t.Fatalf("X-Kept = %q, want yes", got)
	}
}

func TestDynamicHopByHopResponseHeaders(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			if line == httpCRLF {
				break
			}
		}
		rawResponse := strings.Join([]string{
			"HTTP/1.1 200 OK",
			"Content-Type: text/plain",
			"Content-Length: 2",
			"Connection: X-Hop-Response",
			"X-Hop-Response: secret",
			"X-Kept: yes",
			"",
			"ok",
		}, httpCRLF)
		_, err = conn.Write([]byte(rawResponse))
		serverErr <- err
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	_, proxyServer := newProxyHandler(t, staticResolver(Target{InstanceID: "i-test", Port: port}, nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodGet,
		proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := bodyString(t, response); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
	for _, name := range []string{"Connection", "X-Hop-Response"} {
		if got := response.Header.Get(name); got != "" {
			t.Fatalf("connection-nominated response header %s leaked to the browser: %q", name, got)
		}
	}
	if got := response.Header.Get("X-Kept"); got != "yes" {
		t.Fatalf("X-Kept = %q, want yes", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("raw upstream: %v", err)
	}
}

func TestErrorJSONUnifiedFields(t *testing.T) {
	const traceID = "abc123def456"
	slowUpstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := closedListener.Addr().(*net.TCPAddr).Port
	closedListener.Close()

	tests := []struct {
		name       string
		wantStatus int
		wantCode   string
		setup      func(t *testing.T) (*Handler, string)
	}{
		{
			name: "bad request", wantStatus: http.StatusBadRequest, wantCode: codeBadRequest,
			setup: func(t *testing.T) (*Handler, string) {
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{Port: 3080}, nil), nil),
					"/workbench/i-test/a%00b"
			},
		},
		{
			name: "unauthorized", wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized,
			setup: func(t *testing.T) (*Handler, string) {
				authorize := func(*http.Request) error { return errors.New("no session") }
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{Port: 3080}, nil), authorize),
					"/workbench/i-test/"
			},
		},
		{
			name: "forbidden", wantStatus: http.StatusForbidden, wantCode: codeForbidden,
			setup: func(t *testing.T) (*Handler, string) {
				authorize := func(*http.Request) error { return ErrForbidden }
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{Port: 3080}, nil), authorize),
					"/workbench/i-test/"
			},
		},
		{
			name: "instance not found", wantStatus: http.StatusNotFound, wantCode: codeInstanceNotFound,
			setup: func(t *testing.T) (*Handler, string) {
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{}, ErrInstanceNotFound), nil),
					"/workbench/i-test/"
			},
		},
		{
			name: "instance not ready", wantStatus: http.StatusConflict, wantCode: codeInstanceNotReady,
			setup: func(t *testing.T) (*Handler, string) {
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{}, ErrInstanceNotReady), nil),
					"/workbench/i-test/"
			},
		},
		{
			name: "upstream error", wantStatus: http.StatusBadGateway, wantCode: codeUpstreamError,
			setup: func(t *testing.T) (*Handler, string) {
				return New(slog.New(slog.DiscardHandler), staticResolver(Target{InstanceID: "i-test", Port: closedPort}, nil), nil),
					"/workbench/i-test/"
			},
		},
		{
			name: "upstream timeout", wantStatus: http.StatusGatewayTimeout, wantCode: codeUpstreamTimeout,
			setup: func(t *testing.T) (*Handler, string) {
				handler := New(slog.New(slog.DiscardHandler), staticResolver(upstreamTarget(t, slowUpstream), nil), nil)
				handler.SetUpstreamTimeout(20 * time.Millisecond)
				return handler, "/workbench/i-test/slow"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, path := test.setup(t)
			withTrace := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(traceIDHeader, traceID)
				handler.ServeHTTP(w, r)
			})
			proxyServer := httptest.NewServer(withTrace)
			t.Cleanup(proxyServer.Close)
			response := sendRequest(t, noRedirectClient(), http.MethodGet, proxyServer.URL+path, nil, dshAuthHeader())
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			rawBody := bodyString(t, response)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(rawBody), &fields); err != nil {
				t.Fatalf("decode error body %q: %v", rawBody, err)
			}
			if len(fields) != 4 {
				t.Fatalf("error body has %d fields, want 4: %s", len(fields), rawBody)
			}
			for _, key := range []string{"code", "message", "details", "traceId"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("error body field %q missing: %s", key, rawBody)
				}
			}
			var code, message, gotTrace string
			_ = json.Unmarshal(fields["code"], &code)
			_ = json.Unmarshal(fields["message"], &message)
			_ = json.Unmarshal(fields["traceId"], &gotTrace)
			if code != test.wantCode {
				t.Fatalf("code = %q, want %q", code, test.wantCode)
			}
			if message == "" {
				t.Fatal("message must not be empty")
			}
			if details := string(fields["details"]); details != "{}" {
				t.Fatalf("details = %s, want {}", details)
			}
			if gotTrace != traceID {
				t.Fatalf("traceId = %q, want %q", gotTrace, traceID)
			}
		})
	}
}

func TestErrorJSONEmptyTraceIDWithoutHeader(t *testing.T) {
	// Standalone handlers (unit tests, custom muxes) have no X-Trace-Id response
	// header: the field must still exist as an empty string, never be omitted.
	authorize := func(*http.Request) error { return errors.New("no session") }
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), authorize)
	response := sendRequest(t, noRedirectClient(), http.MethodGet, proxyServer.URL+"/workbench/i-test/", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
	rawBody := bodyString(t, response)
	var parsed errorResponse
	if err := json.Unmarshal([]byte(rawBody), &parsed); err != nil {
		t.Fatalf("decode body %q: %v", rawBody, err)
	}
	if parsed.TraceID != "" {
		t.Fatalf("traceId = %q, want empty", parsed.TraceID)
	}
	if parsed.Details == nil || len(parsed.Details) != 0 {
		t.Fatalf("details = %v, want an empty object", parsed.Details)
	}
}

func TestSSEClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{}, 1)
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		select {
		case upstreamCanceled <- struct{}{}:
		default:
		}
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		proxyServer.URL+"/workbench/i-test/plugins/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Cookie", "dsh-auth-route=1")
	response, err := noRedirectClient().Do(request)
	if err != nil {
		t.Fatalf("GET SSE: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	// Simulate the browser going away: cancel the request context and drop the
	// body. The proxy must cancel the upstream request instead of leaking it.
	cancel()
	response.Body.Close()
	select {
	case <-upstreamCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("client disconnect did not cancel the upstream request")
	}
}

func TestLargeRequestBodyStreaming(t *testing.T) {
	const bodySize = 10 << 20 // 10 MiB, per docs/M2_INTERFACES.md §3.5
	firstRead := make(chan struct{})
	var firstReadOnce sync.Once
	upstreamTotal := make(chan int64, 1)
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var total int64
		buffer := make([]byte, 128<<10)
		for {
			read, err := r.Body.Read(buffer)
			if read > 0 {
				total += int64(read)
				firstReadOnce.Do(func() { close(firstRead) })
			}
			if err != nil {
				break
			}
		}
		upstreamTotal <- total
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)

	reader, writer := io.Pipe()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	writerErr := make(chan error, 1)
	go func() {
		chunk := bytes.Repeat([]byte("x"), 256<<10)
		written := 0
		for written < bodySize {
			size := len(chunk)
			if remaining := bodySize - written; remaining < size {
				size = remaining
			}
			if _, err := writer.Write(chunk[:size]); err != nil {
				writerErr <- err
				return
			}
			written += size
			if written >= 2<<20 {
				// Hold the remaining body until the upstream has already seen
				// streamed data: a buffering proxy would deadlock here.
				<-release
			}
		}
		_ = writer.Close()
		writerErr <- nil
	}()
	t.Cleanup(func() {
		releaseWriter()
		_ = writer.CloseWithError(errors.New("test cleanup"))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		proxyServer.URL+"/workbench/i-test/upload", reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Cookie", "dsh-auth-route=1")
	client := noRedirectClient()
	responseCh := make(chan *http.Response, 1)
	requestErr := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			requestErr <- err
			return
		}
		responseCh <- response
	}()

	select {
	case <-firstRead:
	case err := <-requestErr:
		t.Fatalf("request failed before streaming: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("upstream received no body chunk before the client finished sending (proxy buffered the body)")
	}
	releaseWriter()

	var response *http.Response
	select {
	case response = <-responseCh:
	case err := <-requestErr:
		t.Fatalf("request failed: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("request did not complete after the body was released")
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := bodyString(t, response); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
	select {
	case total := <-upstreamTotal:
		if total != bodySize {
			t.Fatalf("upstream received %d bytes, want %d", total, bodySize)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not finish reading the body")
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("client body writer: %v", err)
	}
}

func TestHEADContentLength(t *testing.T) {
	const contentLength = "12345"
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", contentLength)
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "hello")
		}
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	response := sendRequest(t, noRedirectClient(), http.MethodHead,
		proxyServer.URL+"/workbench/i-test/page", nil, dshAuthHeader())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if response.ContentLength != 12345 {
		t.Fatalf("ContentLength = %d, want 12345", response.ContentLength)
	}
	if body := bodyString(t, response); body != "" {
		t.Fatalf("HEAD body = %q, want empty", body)
	}
}
