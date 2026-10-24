// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// maxCloseReasonBytes is the maximum payload of a WebSocket close frame.
const maxCloseReasonBytes = 123

// wsCloseGrace is how long a relay waits for the peer's closing handshake
// before closing the underlying sockets.
const wsCloseGrace = 250 * time.Millisecond

// serveWebSocket proxies one WebSocket connection: validate the origin, upgrade
// the browser connection, dial the loopback instance and relay frames in both
// directions. WebSocket traffic never goes through httputil.ReverseProxy.
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, route workbenchRoute, upstream *url.URL, port int) {
	if !sameOriginWebSocket(r) {
		h.logger.Warn("workbench ws cross-origin rejected", "instance", route.instanceID)
		writeJSONError(w, r.Method, http.StatusForbidden, codeForbidden, "跨源 WebSocket 握手被拒绝。")
		return
	}
	wsURL := &url.URL{
		Scheme:   "ws",
		Host:     upstream.Host,
		Path:     upstream.Path,
		RawQuery: r.URL.RawQuery,
	}
	if !validUpstreamURL(wsURL, "ws", port) {
		h.logger.Warn("workbench ws target rejected", "instance", route.instanceID)
		writeJSONError(w, r.Method, http.StatusBadGateway, codeUpstreamRejected, "反代目标无效。")
		return
	}

	// Forward the browser's offered subprotocols upstream. The client handshake
	// echoes the first supported offer; when the upstream selects the same
	// protocol both legs agree, and a mismatch is only reported at debug level
	// (frames are opaque to the proxy).
	protocols := websocket.Subprotocols(r)
	upgrader := h.upgrader
	upgrader.CheckOrigin = func(*http.Request) bool { return true }
	if len(protocols) > 0 {
		upgrader.Subprotocols = protocols
	}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written an HTTP error response.
		h.logger.Debug("workbench ws client upgrade failed", "instance", route.instanceID, "kind", errorKind(err))
		return
	}
	defer client.Close()
	client.SetReadLimit(maxWSMessageBytes)

	headers := make(http.Header)
	if cookie := dshCookieHeader(r.Header); cookie != "" {
		headers.Set("Cookie", cookie)
	}
	if r.Header.Get("Origin") != "" {
		// Upstream only ever sees the loopback origin.
		headers.Set("Origin", "http://"+upstream.Host)
	}
	dialer := *h.dialer
	if len(protocols) > 0 {
		dialer.Subprotocols = protocols
	}
	handshakeTimeout := h.wsHandshake
	if handshakeTimeout <= 0 {
		handshakeTimeout = defaultWSHandshakeTimeout
	}
	// The HTTP request context is tied to the (now hijacked) connection; dial
	// with an independent timeout so a slow upstream cannot wedge the handler.
	dialCtx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	upstreamConn, resp, err := dialer.DialContext(dialCtx, wsURL.String(), headers)
	if err != nil {
		h.logger.Warn("workbench ws upstream dial failed", "instance", route.instanceID,
			"kind", errorKind(err), "status", responseStatusCode(resp))
		closeClientAfterUpstreamFailure(client, resp)
		return
	}
	defer upstreamConn.Close()
	upstreamConn.SetReadLimit(maxWSMessageBytes)
	if chosen := client.Subprotocol(); chosen != "" && upstreamConn.Subprotocol() != chosen {
		h.logger.Debug("workbench ws subprotocol mismatch", "instance", route.instanceID)
	}
	h.relayWebSocket(client, upstreamConn)
}

// relayWebSocket copies text/binary frames in both directions until one side
// closes, then forwards the normalized close code and tears both connections
// down. A ping ticker keeps idle sessions alive; gorilla answers pings with
// pongs automatically.
func (h *Handler) relayWebSocket(client, upstream *websocket.Conn) {
	done := make(chan struct{})
	interval := h.wsPing
	if interval <= 0 {
		interval = wsPingInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				deadline := now.Add(wsControlTimeout)
				_ = client.WriteControl(websocket.PingMessage, nil, deadline)
				_ = upstream.WriteControl(websocket.PingMessage, nil, deadline)
			}
		}
	}()

	var wait sync.WaitGroup
	results := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		results <- relayFrames(client, upstream)
	}()
	go func() {
		defer wait.Done()
		results <- relayFrames(upstream, client)
	}()
	firstErr := <-results
	close(done)
	if isCloseHandshakeError(firstErr) {
		// The close frame was already forwarded; give the peer a short moment to
		// complete the closing handshake before the sockets are torn down.
		select {
		case <-results:
		case <-time.After(wsCloseGrace):
		}
	}
	// Closing the underlying connections unblocks the remaining read.
	_ = client.Close()
	_ = upstream.Close()
	wait.Wait()
}

// relayFrames forwards data frames from source to target and forwards the
// normalized close frame when the source terminates.
func relayFrames(source, target *websocket.Conn) error {
	for {
		messageType, payload, err := source.ReadMessage()
		if err != nil {
			forwardClose(target, err)
			return err
		}
		_ = target.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if err := target.WriteMessage(messageType, payload); err != nil {
			return err
		}
	}
}

// isCloseHandshakeError reports whether a relay ended because of a normal close
// handshake (or a read limit violation that gorilla already reports with a
// close frame), so the other direction may still complete quickly.
func isCloseHandshakeError(err error) bool {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return true
	}
	return errors.Is(err, websocket.ErrReadLimit)
}

// forwardClose translates a read error into a close frame for the peer.
func forwardClose(target *websocket.Conn, err error) {
	code, reason := closeFrameForError(err)
	_ = target.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), time.Now().Add(wsControlTimeout))
}

func closeFrameForError(err error) (int, string) {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return normalizeCloseCode(closeErr.Code), normalizeCloseText(closeErr.Text)
	}
	if errors.Is(err, websocket.ErrReadLimit) {
		return websocket.CloseMessageTooBig, ""
	}
	// Abnormal transport failure: tell the peer we are going away.
	return websocket.CloseGoingAway, ""
}

// normalizeCloseCode keeps application/reserved-pending codes and valid
// protocol codes, replacing codes that must never appear on the wire
// (1004/1005/1006/1015 and out-of-range values) with a normal closure.
func normalizeCloseCode(code int) int {
	switch {
	case code >= 3000 && code <= 4999:
		return code
	case code >= 1000 && code <= 1014:
		switch code {
		case 1004, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure:
			return websocket.CloseNormalClosure
		}
		return code
	default:
		return websocket.CloseNormalClosure
	}
}

// normalizeCloseText truncates a close reason to the 123-byte control frame
// limit without splitting a UTF-8 rune.
func normalizeCloseText(text string) string {
	if len(text) <= maxCloseReasonBytes {
		return text
	}
	trimmed := []byte(text)[:maxCloseReasonBytes]
	for len(trimmed) > 0 && !utf8.Valid(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return string(trimmed)
}

// closeClientAfterUpstreamFailure closes an already upgraded client connection
// with a close code that mirrors the HTTP failure semantics (502/409): the
// client was upgraded before the upstream dial, so a JSON error is impossible.
func closeClientAfterUpstreamFailure(client *websocket.Conn, resp *http.Response) {
	code, reason := websocket.CloseTryAgainLater, "upstream unavailable"
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			code, reason = websocket.ClosePolicyViolation, "upstream rejected the session"
		case http.StatusNotFound:
			code, reason = websocket.CloseInternalServerErr, "upstream not found"
		case http.StatusConflict:
			code, reason = websocket.CloseTryAgainLater, "upstream not ready"
		case http.StatusBadRequest:
			code, reason = websocket.CloseInternalServerErr, "upstream rejected the handshake"
		default:
			if resp.StatusCode >= 500 {
				code, reason = websocket.CloseInternalServerErr, "upstream error"
			}
		}
	}
	_ = client.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), time.Now().Add(wsControlTimeout))
}

func responseStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// sameOriginWebSocket enforces Origin/Host same-origin for browser WebSocket
// handshakes (DSH_INTEGRATION §6.3). Default ports are normalized (http→80,
// https→443); a Host header without a port uses the origin scheme's default
// port. Requests without an Origin header (non-browser clients) are accepted:
// they are not subject to browser cross-site WebSocket hijacking.
func sameOriginWebSocket(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	var defaultPort int
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		defaultPort = 80
	case "https":
		defaultPort = 443
	default:
		return false
	}
	originPort := defaultPort
	if rawPort := parsed.Port(); rawPort != "" {
		port, convErr := strconv.Atoi(rawPort)
		if convErr != nil || port < 1 || port > 65535 {
			return false
		}
		originPort = port
	}
	host, port, ok := splitHostHeader(r.Host, defaultPort)
	if !ok {
		return false
	}
	return strings.EqualFold(host, parsed.Hostname()) && port == originPort
}

// splitHostHeader splits a Host header into host and port, falling back to
// defaultPort when the header carries no port.
func splitHostHeader(hostHeader string, defaultPort int) (host string, port int, ok bool) {
	if hostHeader == "" {
		return "", 0, false
	}
	if parsedHost, rawPort, err := net.SplitHostPort(hostHeader); err == nil {
		parsedPort, convErr := strconv.Atoi(rawPort)
		if convErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", 0, false
		}
		return parsedHost, parsedPort, true
	}
	if strings.HasPrefix(hostHeader, "[") && strings.HasSuffix(hostHeader, "]") {
		return hostHeader[1 : len(hostHeader)-1], defaultPort, true
	}
	if strings.Contains(hostHeader, ":") {
		return "", 0, false
	}
	return hostHeader, defaultPort, true
}
