// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
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

// serveWebSocket proxies one WebSocket connection using the dial-first order
// required by docs/M2_INTERFACES.md §4: validate the Origin and loopback target,
// dial the upstream instance first, surface upstream handshake rejections as
// normal HTTP JSON errors, and only then upgrade the browser connection. When
// the browser is upgraded, the response echoes the subprotocol the upstream
// actually selected (never the client's first offer).
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, route workbenchRoute, upstream *url.URL, port int) {
	if !sameOriginRequest(r) {
		h.logger.Warn("workbench ws cross-origin rejected", "instance", route.instanceID)
		writeJSONError(w, r.Method, http.StatusForbidden, codeForbidden, "跨源 WebSocket 握手被拒绝。")
		return
	}
	if !validWebSocketHandshake(r) {
		// Validate before dialing the upstream so malformed handshakes also get
		// the unified JSON 400 body instead of gorilla's plain-text rejection.
		writeJSONError(w, r.Method, http.StatusBadRequest, codeBadRequest, "WebSocket 握手请求无效。")
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

	// Forward the browser's offered subprotocol list, the DSH cookies and a
	// loopback Origin to the upstream; the upstream decides what it accepts.
	protocols := websocket.Subprotocols(r)
	headers := make(http.Header)
	if cookie := dshCookieHeader(r.Header); cookie != "" {
		headers.Set("Cookie", cookie)
	}
	if r.Header.Get("Origin") != "" {
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
	dialCtx, cancel := context.WithTimeout(r.Context(), handshakeTimeout)
	defer cancel()
	upstreamConn, resp, err := dialer.DialContext(dialCtx, wsURL.String(), headers)
	if err != nil {
		h.logger.Warn("workbench ws upstream dial failed", "instance", route.instanceID,
			"kind", errorKind(err), "status", responseStatusCode(resp))
		h.writeWebSocketDialError(w, r, resp)
		return
	}
	defer upstreamConn.Close()

	// Upstream accepted the handshake: now upgrade the browser. The gorilla
	// Upgrader is given the upstream-selected subprotocol through
	// responseHeader; without that, gorilla would fall back to the first
	// client offer (forbidden by the M2 contract).
	upgrader := h.upgrader
	upgrader.CheckOrigin = func(*http.Request) bool { return true }
	upgrader.Subprotocols = nil
	var responseHeader http.Header
	if selected := upstreamConn.Subprotocol(); selected != "" {
		responseHeader = http.Header{"Sec-Websocket-Protocol": {selected}}
	}
	client, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		// Upgrade has already written an HTTP error response; the upstream
		// connection is closed by the deferred Close.
		h.logger.Debug("workbench ws client upgrade failed", "instance", route.instanceID, "kind", errorKind(err))
		return
	}
	defer client.Close()
	// DSH sends a full history snapshot as one message, so messages pass without a fixed size cap.
	h.relayWebSocket(client, upstreamConn)
}

// writeWebSocketDialError turns an upstream handshake rejection into the
// matching HTTP JSON error (dial-first contract). A connection-level failure (no
// HTTP response) maps to 502 UPSTREAM_ERROR.
func (h *Handler) writeWebSocketDialError(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	if resp != nil {
		_ = resp.Body.Close()
		status, code, message := mapWebSocketUpstreamStatus(resp.StatusCode)
		writeJSONError(w, r.Method, status, code, message)
		return
	}
	writeJSONError(w, r.Method, http.StatusBadGateway, codeUpstreamError, "无法连接实例工作台，请确认实例仍在运行。")
}

// validWebSocketHandshake validates the parts of the browser handshake that
// gorilla.Upgrade would check (method, version and challenge key). Running it
// before the upstream dial keeps every client-facing failure in the unified
// JSON error model.
func validWebSocketHandshake(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") ||
		!headerContainsToken(r.Header, "Upgrade", "websocket") {
		return false
	}
	if !headerContainsToken(r.Header, "Sec-Websocket-Version", "13") {
		return false
	}
	challengeKey := r.Header.Get("Sec-Websocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(challengeKey)
	return err == nil && len(decoded) == 16
}

// headerContainsToken reports whether a comma-separated header contains token
// (case-insensitive).
func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// mapWebSocketUpstreamStatus maps a non-101 upstream handshake status to the
// /workbench error model (docs/M2_INTERFACES.md §4); anything else is a bad
// gateway.
func mapWebSocketUpstreamStatus(status int) (int, string, string) {
	switch status {
	case http.StatusBadRequest:
		return http.StatusBadRequest, codeBadRequest, "WebSocket 握手请求无效。"
	case http.StatusUnauthorized:
		return http.StatusUnauthorized, codeUnauthorized, "请先登录。"
	case http.StatusForbidden:
		return http.StatusForbidden, codeForbidden, "WebSocket 握手被拒绝。"
	case http.StatusNotFound:
		return http.StatusNotFound, codeInstanceNotFound, "实例不存在。"
	case http.StatusConflict:
		return http.StatusConflict, codeInstanceNotReady, "网页服务尚未确认就绪，请等待启动完成或检查实例日志。"
	default:
		return http.StatusBadGateway, codeUpstreamError, "无法连接实例工作台，请确认实例仍在运行。"
	}
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
// handshake, so the other direction may still complete quickly.
func isCloseHandshakeError(err error) bool {
	var closeErr *websocket.CloseError
	return errors.As(err, &closeErr)
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

func responseStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
