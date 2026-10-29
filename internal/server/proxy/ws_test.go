// SPDX-License-Identifier: MIT

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

	"github.com/gorilla/websocket"
)

// wsUpstreamState is a concurrency-safe snapshot of a mock WebSocket upstream.
type wsUpstreamState struct {
	upgraded  bool
	cookie    string
	origin    string
	protocol  string
	messages  []string
	closeCode int
	closeText string
	writeErr  string
}

type wsUpstreamObservation struct {
	mu    sync.Mutex
	state wsUpstreamState
}

func (o *wsUpstreamObservation) setHandshake(request *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.cookie = request.Header.Get("Cookie")
	o.state.origin = request.Header.Get("Origin")
}

func (o *wsUpstreamObservation) setUpgraded(protocol string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.upgraded = true
	o.state.protocol = protocol
}

func (o *wsUpstreamObservation) addMessage(payload []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.messages = append(o.state.messages, string(payload))
}

func (o *wsUpstreamObservation) setClose(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		o.state.closeCode = closeErr.Code
		o.state.closeText = closeErr.Text
	}
}

func (o *wsUpstreamObservation) setWriteError(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.state.writeErr = err.Error()
	}
}

func (o *wsUpstreamObservation) snapshot() wsUpstreamState {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.state
	state.messages = append([]string(nil), o.state.messages...)
	return state
}

func testWebSocketUpgrader(subprotocols ...string) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(*http.Request) bool { return true },
		Subprotocols:    subprotocols,
	}
}

// newWSEchoUpstream starts a mock DSH WebSocket endpoint. Without onConnect it
// echoes every message; onConnect replaces the echo loop (used for close
// scenarios).
func newWSEchoUpstream(t *testing.T, upgrader websocket.Upgrader, onConnect func(*websocket.Conn)) (*httptest.Server, *wsUpstreamObservation) {
	t.Helper()
	observation := &wsUpstreamObservation{}
	server := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observation.setHandshake(r)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		observation.setUpgraded(conn.Subprotocol())
		defer conn.Close()
		if onConnect != nil {
			onConnect(conn)
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				observation.setClose(err)
				return
			}
			observation.addMessage(payload)
			if err := conn.WriteMessage(messageType, payload); err != nil {
				observation.setWriteError(err)
				return
			}
		}
	}))
	return server, observation
}

func dialProxyWebSocket(t *testing.T, proxyServer *httptest.Server, path string, header http.Header, subprotocols ...string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.HandshakeTimeout = 5 * time.Second
	dialer.Subprotocols = subprotocols
	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http") + path
	return dialer.Dial(wsURL, header)
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestWebSocketEcho(t *testing.T) {
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write text: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echoed text: %v", err)
	}
	if messageType != websocket.TextMessage || string(payload) != "hello" {
		t.Fatalf("echo = (%d, %q), want (text, hello)", messageType, payload)
	}

	binaryPayload := []byte{0x00, 0x01, 0xff, 0x7f}
	if err := conn.WriteMessage(websocket.BinaryMessage, binaryPayload); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	messageType, payload, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echoed binary: %v", err)
	}
	if messageType != websocket.BinaryMessage || !bytes.Equal(payload, binaryPayload) {
		t.Fatalf("binary echo = (%d, %v), want binary payload", messageType, payload)
	}

	if err := conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(4002, "client-done"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write close: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		return observation.snapshot().closeCode == 4002
	})
	if got := observation.snapshot().closeText; got != "client-done" {
		t.Fatalf("upstream close text = %q, want client-done", got)
	}
}

func TestWebSocketSubprotocol(t *testing.T) {
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader("dsh-test"), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil, "dsh-test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if got := conn.Subprotocol(); got != "dsh-test" {
		t.Fatalf("client subprotocol = %q, want dsh-test", got)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		return observation.snapshot().protocol == "dsh-test"
	})

	if err := conn.WriteMessage(websocket.TextMessage, []byte("sp")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "sp" {
		t.Fatalf("echo = %q, %v", payload, err)
	}
}

func TestWebSocketCookieAndOriginRewrite(t *testing.T) {
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	target := upstreamTarget(t, upstream)
	_, proxyServer := newProxyHandler(t, staticResolver(target, nil), nil)

	header := http.Header{}
	header.Set("Origin", proxyServer.URL)
	header.Set("Cookie", "JSESSIONID=console; XSRF-TOKEN=csrf; dsh-auth-a=SECRET; theme=dark")
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	waitForCondition(t, 5*time.Second, func() bool { return observation.snapshot().cookie != "" })
	state := observation.snapshot()
	if want := "dsh-auth-a=SECRET; theme=dark"; state.cookie != want {
		t.Fatalf("upstream Cookie = %q, want %q", state.cookie, want)
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d", target.Port); state.origin != want {
		t.Fatalf("upstream Origin = %q, want %q", state.origin, want)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "ping" {
		t.Fatalf("echo = %q, %v", payload, err)
	}
}

func TestWebSocketCrossOriginRejected(t *testing.T) {
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", header)
	if err == nil {
		conn.Close()
		t.Fatal("cross-origin WebSocket handshake must be rejected")
	}
	if response == nil {
		t.Fatalf("expected an HTTP response for the rejected handshake, got error %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), codeForbidden) {
		t.Fatalf("body = %q, want FORBIDDEN", body)
	}
	if observation.snapshot().upgraded {
		t.Fatal("upstream must not be dialed for a cross-origin handshake")
	}
}

func TestWebSocketWithoutOriginAllowed(t *testing.T) {
	// Non-browser clients do not send Origin; they are accepted because they are
	// not subject to cross-site WebSocket hijacking.
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial without Origin: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	waitForCondition(t, 5*time.Second, func() bool { return observation.snapshot().upgraded })
	if got := observation.snapshot().origin; got != "" {
		t.Fatalf("upstream Origin = %q, want empty", got)
	}
}

func TestWebSocketUpstreamCloseCodeForwarded(t *testing.T) {
	onConnect := func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4001, "upstream-bye"), time.Now().Add(time.Second))
	}
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), onConnect)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("read error = %v, want a close error", err)
	}
	if closeErr.Code != 4001 || closeErr.Text != "upstream-bye" {
		t.Fatalf("close = (%d, %q), want (4001, upstream-bye)", closeErr.Code, closeErr.Text)
	}
}

func TestWebSocketRelaysLargeUpstreamMessage(t *testing.T) {
	largePayload := bytes.Repeat([]byte("a"), 4<<20)
	onConnect := func(conn *websocket.Conn) {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = conn.WriteMessage(websocket.TextMessage, largePayload)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), onConnect)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read large upstream message: %v", err)
	}
	if messageType != websocket.TextMessage || !bytes.Equal(payload, largePayload) {
		t.Fatalf("large upstream message was not relayed intact: type=%d bytes=%d", messageType, len(payload))
	}
}

func TestWebSocketRelaysLargeClientMessage(t *testing.T) {
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	largePayload := bytes.Repeat([]byte("b"), 4<<20)
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(websocket.BinaryMessage, largePayload); err != nil {
		t.Fatalf("write large client message: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read large echoed message: %v", err)
	}
	if messageType != websocket.BinaryMessage || !bytes.Equal(payload, largePayload) {
		t.Fatalf("large client message was not relayed intact: type=%d bytes=%d", messageType, len(payload))
	}
}

func TestWebSocketUpstreamUnavailable(t *testing.T) {
	// Dial-first: an unreachable upstream rejects the browser handshake with a
	// normal HTTP 502 JSON error instead of an upgraded-then-closed connection.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	_, proxyServer := newProxyHandler(t, staticResolver(Target{InstanceID: "i-test", Port: closedPort}, nil), nil)
	conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err == nil {
		conn.Close()
		t.Fatal("browser handshake must be rejected when the upstream is unreachable")
	}
	if response == nil {
		t.Fatalf("expected an HTTP error response, got %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode 502 body %q: %v", body, err)
	}
	if parsed.Code != codeUpstreamError {
		t.Fatalf("code = %q, want %q", parsed.Code, codeUpstreamError)
	}
	if parsed.Details == nil || len(parsed.Details) != 0 {
		t.Fatalf("details = %v, want an empty object", parsed.Details)
	}
}

func TestWebSocketResolverErrorBeforeUpgrade(t *testing.T) {
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, ErrInstanceNotReady), nil)
	conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err == nil {
		conn.Close()
		t.Fatal("handshake must be rejected when the instance is not ready")
	}
	if response == nil {
		t.Fatalf("expected HTTP response, got %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), codeInstanceNotReady) {
		t.Fatalf("body = %q, want INSTANCE_NOT_READY", body)
	}
}

func TestWebSocketAuthorizerRejected(t *testing.T) {
	authorize := func(*http.Request) error { return context.Canceled }
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), authorize)
	conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err == nil {
		conn.Close()
		t.Fatal("handshake must be rejected when not authorized")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401 (err %v)", response, err)
	}
	response.Body.Close()
}

func TestWebSocketTrailingSlashRedirect(t *testing.T) {
	_, proxyServer := newProxyHandler(t, staticResolver(Target{Port: 3080}, nil), nil)
	conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test", nil)
	if err == nil {
		conn.Close()
		t.Fatal("handshake to a path without trailing slash must not upgrade")
	}
	if response == nil {
		t.Fatalf("expected redirect response, got %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.StatusCode)
	}
	if got := response.Header.Get("Location"); got != "/workbench/i-test/" {
		t.Fatalf("Location = %q, want /workbench/i-test/", got)
	}
}

func TestWebSocketPingKeepAlive(t *testing.T) {
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	handler := New(slog.New(slog.DiscardHandler), staticResolver(upstreamTarget(t, upstream), nil), nil)
	handler.wsPing = 50 * time.Millisecond
	proxyServer := httptest.NewServer(handler)
	t.Cleanup(proxyServer.Close)

	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	pingReceived := make(chan struct{}, 1)
	conn.SetPingHandler(func(string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		for {
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				return
			}
		}
	}()
	select {
	case <-pingReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not send a keepalive ping")
	}
}

func TestWebSocketUpstreamStatusMapping(t *testing.T) {
	tests := []struct {
		upstreamStatus int
		wantStatus     int
		wantCode       string
	}{
		{upstreamStatus: http.StatusBadRequest, wantStatus: http.StatusBadRequest, wantCode: codeBadRequest},
		{upstreamStatus: http.StatusUnauthorized, wantStatus: http.StatusUnauthorized, wantCode: codeUnauthorized},
		{upstreamStatus: http.StatusForbidden, wantStatus: http.StatusForbidden, wantCode: codeForbidden},
		{upstreamStatus: http.StatusNotFound, wantStatus: http.StatusNotFound, wantCode: codeInstanceNotFound},
		{upstreamStatus: http.StatusConflict, wantStatus: http.StatusConflict, wantCode: codeInstanceNotReady},
		{upstreamStatus: http.StatusInternalServerError, wantStatus: http.StatusBadGateway, wantCode: codeUpstreamError},
		{upstreamStatus: http.StatusBadGateway, wantStatus: http.StatusBadGateway, wantCode: codeUpstreamError},
		{upstreamStatus: http.StatusServiceUnavailable, wantStatus: http.StatusBadGateway, wantCode: codeUpstreamError},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("upstream_%d", test.upstreamStatus), func(t *testing.T) {
			upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.upstreamStatus)
				_, _ = io.WriteString(w, "rejected")
			}))
			_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
			conn, response, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
			if err == nil {
				conn.Close()
				t.Fatalf("handshake must be rejected for upstream status %d", test.upstreamStatus)
			}
			if response == nil {
				t.Fatalf("expected an HTTP error response, got %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			body, _ := io.ReadAll(response.Body)
			var parsed errorResponse
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("decode body %q: %v", body, err)
			}
			if parsed.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", parsed.Code, test.wantCode)
			}
			if parsed.Details == nil || len(parsed.Details) != 0 {
				t.Fatalf("details = %v, want an empty object", parsed.Details)
			}
		})
	}
}

func TestWebSocketSubprotocolSelectionDifference(t *testing.T) {
	// Client offers "first" then "second"; the upstream only supports "second".
	// The browser handshake must echo "second" (the upstream selection), not the
	// client's first offer "first".
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader("second"), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil, "first", "second")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if got := conn.Subprotocol(); got != "second" {
		t.Fatalf("browser subprotocol = %q, want the upstream selection %q", got, "second")
	}
	waitForCondition(t, 5*time.Second, func() bool {
		return observation.snapshot().protocol == "second"
	})
	if err := conn.WriteMessage(websocket.TextMessage, []byte("sp")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "sp" {
		t.Fatalf("echo = %q, %v", payload, err)
	}
}

func TestWebSocketNoSubprotocol(t *testing.T) {
	upstream, observation := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if got := conn.Subprotocol(); got != "" {
		t.Fatalf("browser subprotocol = %q, want empty", got)
	}
	waitForCondition(t, 5*time.Second, func() bool { return observation.snapshot().upgraded })
	if err := conn.WriteMessage(websocket.TextMessage, []byte("plain")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "plain" {
		t.Fatalf("echo = %q, %v", payload, err)
	}
}

func TestWebSocketClientDisconnectClosesUpstream(t *testing.T) {
	upstreamRead := make(chan error, 1)
	upgrader := testWebSocketUpgrader()
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			upstreamRead <- err
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		upstreamRead <- err
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Abrupt client disconnect (no close frame): the proxy must tear down the
	// upstream connection as well.
	conn.Close()
	select {
	case err := <-upstreamRead:
		if err == nil {
			t.Fatal("upstream connection should have been closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream connection was not closed after the client disconnected")
	}
}

func TestWebSocketUpstreamDisconnectClosesClient(t *testing.T) {
	upgrader := testWebSocketUpgrader()
	upstream := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Abrupt upstream disconnect after the 101 handshake.
		_ = conn.Close()
	}))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("client connection should have been closed after the upstream disconnected")
	}
}

func TestWebSocketMalformedHandshakeJSON400(t *testing.T) {
	recorder := &requestRecorder{}
	upstream := newUpstream(t, http.HandlerFunc(recorder.handler))
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, err := net.Dial("tcp", proxyServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	requestLines := []string{
		"GET /workbench/i-test/api/remote.mux HTTP/1.1",
		"Host: " + proxyServer.Listener.Addr().String(),
		"Cookie: dsh-auth-route=1",
		"Connection: Upgrade",
		"Upgrade: websocket",
		"",
		"",
	}
	if _, err := conn.Write([]byte(strings.Join(requestLines, httpCRLF))); err != nil {
		t.Fatalf("write raw handshake: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	if parsed.Code != codeBadRequest {
		t.Fatalf("code = %q, want %q", parsed.Code, codeBadRequest)
	}
	if parsed.Details == nil || len(parsed.Details) != 0 {
		t.Fatalf("details = %v, want an empty object", parsed.Details)
	}
	if recorder.count() != 0 {
		t.Fatalf("upstream must not be dialed for a malformed handshake, got %d requests", recorder.count())
	}
}

func TestWebSocketNoStatusCloseNormalized(t *testing.T) {
	// An empty close frame arrives as CloseNoStatusReceived (1005), which must
	// never be forwarded on the wire: the peer must see a normal closure (1000).
	onConnect := func(conn *websocket.Conn) {
		_ = conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second))
	}
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), onConnect)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("read error = %v, want a close error", err)
	}
	if closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("close code = %d, want %d", closeErr.Code, websocket.CloseNormalClosure)
	}
}
