// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"bytes"
	"context"
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
// echoes every message; onConnect replaces the echo loop (used for close/limit
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

func TestWebSocketMessageTooLarge(t *testing.T) {
	// The mock upstream sends a message larger than the 1 MiB read limit; the
	// proxy must disconnect with close code 1009 (message too big).
	onConnect := func(conn *websocket.Conn) {
		oversized := bytes.Repeat([]byte("a"), maxWSMessageBytes+1024)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = conn.WriteMessage(websocket.BinaryMessage, oversized)
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
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseMessageTooBig {
		t.Fatalf("read error = %v, want close code %d", err, websocket.CloseMessageTooBig)
	}
}

func TestWebSocketClientMessageTooLarge(t *testing.T) {
	upstream, _ := newWSEchoUpstream(t, testWebSocketUpgrader(), nil)
	_, proxyServer := newProxyHandler(t, staticResolver(upstreamTarget(t, upstream), nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	oversized := bytes.Repeat([]byte("b"), maxWSMessageBytes+1024)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteMessage(websocket.BinaryMessage, oversized)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseMessageTooBig {
		t.Fatalf("read error = %v, want close code %d", err, websocket.CloseMessageTooBig)
	}
}

func TestWebSocketUpstreamUnavailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	_, proxyServer := newProxyHandler(t, staticResolver(Target{InstanceID: "i-test", Port: closedPort}, nil), nil)
	conn, _, err := dialProxyWebSocket(t, proxyServer, "/workbench/i-test/api/remote.mux", nil)
	if err != nil {
		t.Fatalf("client handshake should succeed before the upstream dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("read error = %v, want a close error", err)
	}
	if closeErr.Code != websocket.CloseTryAgainLater && closeErr.Code != websocket.CloseInternalServerErr {
		t.Fatalf("close code = %d, want 1013 or 1011", closeErr.Code)
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
