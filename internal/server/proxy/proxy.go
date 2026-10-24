// SPDX-License-Identifier: GPL-3.0-or-later

// Package proxy implements the /workbench reverse proxy that exposes a
// running DSH instance (loopback port) to the console for HTTP, SSE and
// WebSocket traffic.
//
// Contract: docs/DSH_INTEGRATION.md §6, docs/M0_INTERFACES.md §8 and the M2
// hardening decisions in docs/M2_INTERFACES.md §3/§4 (Origin same-origin checks,
// dynamic hop-by-hop headers, unified four-field error bodies, SSE buffering /
// cancellation, streaming request bodies and the dial-first WebSocket flow).
//
// The package depends only on the standard library and gorilla/websocket. Its
// upstream host is always 127.0.0.1 and its upstream port is always the port
// carried by Target, so no request input can influence the backend address
// (SSRF whitelist).
package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Target describes the workbench endpoint of one running DSH instance.
type Target struct {
	// InstanceID is the instance the target belongs to.
	InstanceID string
	// Port is the loopback port the DSH instance listens on (1-65535).
	Port int
	// BootstrapPath is the path of the in-memory DSH bootstrap URL. It is used
	// as the upstream base path; an empty value means "/".
	BootstrapPath string
	// BootstrapQuery is the query of the in-memory DSH bootstrap URL, usually
	// "token=...". It is only used for the one-time bootstrap redirect and must
	// never be logged or persisted.
	BootstrapQuery string
	// Ready reports whether the workbench is ready. Readiness is enforced
	// through ErrInstanceNotReady: callers that do not populate Ready still get
	// correct behavior as long as the resolver returns the documented errors.
	Ready bool
}

// ErrInstanceNotFound reports a missing instance (mapped to 404).
var ErrInstanceNotFound = errors.New("instance not found")

// ErrInstanceNotReady reports a running instance whose workbench is not ready
// yet (mapped to 409 INSTANCE_NOT_READY).
var ErrInstanceNotReady = errors.New("instance not ready")

// ErrInstanceNotRunning reports an instance that is not running (mapped to 409
// INSTANCE_NOT_READY).
var ErrInstanceNotRunning = errors.New("instance not running")

// ErrForbidden lets an Authorizer request a 403 response. Any other non-nil
// Authorizer error maps to 401 UNAUTHORIZED.
var ErrForbidden = errors.New("proxy: forbidden")

// Resolver maps an instance id to its workbench Target. It returns
// ErrInstanceNotFound, ErrInstanceNotRunning or ErrInstanceNotReady so the
// proxy can map failures to 404/409.
type Resolver interface {
	Resolve(ctx context.Context, instanceID string) (Target, error)
}

// Authorizer verifies the console session before a /workbench request is
// proxied. A nil return allows the request; ErrForbidden maps to 403 and any
// other error maps to 401.
type Authorizer func(r *http.Request) error

const (
	defaultUpstreamTimeout    = 60 * time.Second
	defaultWSHandshakeTimeout = 10 * time.Second
	upstreamDialTimeout       = 5 * time.Second
	// maxWSMessageBytes caps a single WebSocket message in both directions.
	maxWSMessageBytes = 1 << 20
	wsPingInterval    = 20 * time.Second
	wsControlTimeout  = 5 * time.Second
	wsWriteTimeout    = 30 * time.Second
)

// Handler is the /workbench reverse proxy http.Handler.
type Handler struct {
	logger    *slog.Logger
	resolver  Resolver
	authorize Authorizer

	transport *http.Transport

	upgrader websocket.Upgrader
	dialer   *websocket.Dialer

	wsHandshake time.Duration
	wsPing      time.Duration
}

// New creates a workbench proxy handler. logger may be nil (a discard logger is
// used); logging never includes query strings, tokens, cookies or raw headers.
func New(logger *slog.Logger, resolver Resolver, authorize Authorizer) *Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dialer := &net.Dialer{Timeout: upstreamDialTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Never route loopback upstreams through an environment proxy.
		Proxy: nil,
		// Disable transparent gzip: the proxy must be a byte-transparent stream
		// (SSE/HEAD semantics depend on it).
		DisableCompression:    true,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: defaultUpstreamTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &Handler{
		logger:      logger,
		resolver:    resolver,
		authorize:   authorize,
		transport:   transport,
		wsHandshake: defaultWSHandshakeTimeout,
		wsPing:      wsPingInterval,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Origin is validated by the proxy (sameOriginRequest) before the
			// upstream dial, so gorilla's own check must not reject valid requests.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: defaultWSHandshakeTimeout,
			// SSRF: never dial loopback upstreams through an environment proxy.
			Proxy:           nil,
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
	}
}

// SetUpstreamTimeout overrides how long the proxy waits for upstream response
// headers (default 60s). It must be called before the handler starts serving
// requests.
func (h *Handler) SetUpstreamTimeout(timeout time.Duration) {
	if timeout > 0 {
		h.transport.ResponseHeaderTimeout = timeout
	}
}

// SetWSHandshakeTimeout overrides the WebSocket upstream handshake timeout
// (default 10s). It must be called before the handler starts serving requests.
func (h *Handler) SetWSHandshakeTimeout(timeout time.Duration) {
	if timeout > 0 {
		h.wsHandshake = timeout
		h.dialer.HandshakeTimeout = timeout
	}
}

// workbenchRoute is the parsed /workbench path.
type workbenchRoute struct {
	instanceID string
	remainder  string
}

// ServeHTTP handles /workbench/{id} and /workbench/{id}/** (full path).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	instanceID, remainder, matched := parseWorkbenchPath(r.URL.Path)
	if !matched {
		writeJSONError(w, r.Method, http.StatusNotFound, codeNotFound, "资源不存在。")
		return
	}
	route := workbenchRoute{instanceID: instanceID, remainder: remainder}
	if !validInstanceID(route.instanceID) {
		writeJSONError(w, r.Method, http.StatusBadRequest, codeBadRequest, "工作台路径无效。")
		return
	}
	if !isProxyableMethod(r.Method) {
		writeJSONError(w, r.Method, http.StatusMethodNotAllowed, codeMethodNotAllowed, "不支持的请求方法。")
		return
	}
	if unsafeProxyPath(r.URL.Path) || unsafeProxyPath(r.URL.EscapedPath()) {
		writeJSONError(w, r.Method, http.StatusBadRequest, codeBadRequest, "工作台路径无效。")
		return
	}
	if h.authorize != nil {
		if err := h.authorize(r); err != nil {
			if errors.Is(err, ErrForbidden) {
				writeJSONError(w, r.Method, http.StatusForbidden, codeForbidden, "没有访问该工作台的权限。")
			} else {
				writeJSONError(w, r.Method, http.StatusUnauthorized, codeUnauthorized, "请先登录。")
			}
			return
		}
	}
	if requiresOriginCheck(r.Method) && !sameOriginRequest(r) {
		// Cross-site write requests are rejected even when a session cookie is
		// present (CSRF hardening, docs/M2_INTERFACES.md §3.1). GET/HEAD and
		// requests without an Origin header are not gated.
		h.logger.Warn("workbench cross-origin write rejected", "instance", route.instanceID, "method", r.Method)
		writeJSONError(w, r.Method, http.StatusForbidden, codeForbidden, "跨源请求被拒绝。")
		return
	}
	if route.remainder == "" {
		// Missing trailing slash breaks the HTML base href of the workbench, so
		// redirect before doing any instance lookup (query is preserved).
		location := workbenchPrefix + route.instanceID + "/"
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		writeRedirect(w, location)
		return
	}
	target, err := h.resolver.Resolve(r.Context(), route.instanceID)
	if err != nil {
		h.handleResolveError(w, r, route.instanceID, err)
		return
	}
	upstreamHost, ok := loopbackEndpoint(target.Port)
	if !ok {
		h.logger.Warn("workbench target rejected", "instance", route.instanceID)
		writeJSONError(w, r.Method, http.StatusBadGateway, codeUpstreamRejected, "反代目标无效。")
		return
	}
	upstream := &url.URL{
		Scheme: "http",
		Host:   upstreamHost,
		Path:   joinUpstreamPath(target.BootstrapPath, route.remainder),
	}
	if !validUpstreamURL(upstream, "http", target.Port) {
		h.logger.Warn("workbench target rejected", "instance", route.instanceID)
		writeJSONError(w, r.Method, http.StatusBadGateway, codeUpstreamRejected, "反代目标无效。")
		return
	}
	if websocket.IsWebSocketUpgrade(r) {
		// WebSocket handshakes do not use the token bootstrap redirect: the page
		// load already obtained the dsh-auth cookie, and a 302 is not a valid
		// WebSocket handshake answer.
		h.serveWebSocket(w, r, route, upstream, target.Port)
		return
	}
	if !hasDshAuthCookie(r.Header) && !hasTokenQuery(r.URL.RawQuery) {
		if location := bootstrapLocation(route.instanceID, target.BootstrapQuery); location != "" {
			writeRedirect(w, location)
			return
		}
	}
	h.serveHTTP(w, r, route, upstream, target.Port)
}

// bootstrapLocation builds the one-time token bootstrap redirect. An empty
// bootstrap query yields "" so the caller forwards the request instead of
// redirecting to itself forever.
func bootstrapLocation(instanceID, bootstrapQuery string) string {
	if bootstrapQuery == "" {
		return ""
	}
	return workbenchPrefix + instanceID + "/?" + bootstrapQuery
}

// handleResolveError maps resolver failures to 404/409 JSON responses.
func (h *Handler) handleResolveError(w http.ResponseWriter, r *http.Request, instanceID string, err error) {
	switch {
	case errors.Is(err, ErrInstanceNotFound):
		writeJSONError(w, r.Method, http.StatusNotFound, codeInstanceNotFound, "实例不存在。")
	case errors.Is(err, ErrInstanceNotRunning):
		writeJSONError(w, r.Method, http.StatusConflict, codeInstanceNotReady, "实例未运行，请先启动实例。")
	case errors.Is(err, ErrInstanceNotReady):
		writeJSONError(w, r.Method, http.StatusConflict, codeInstanceNotReady, "网页服务尚未确认就绪，请等待启动完成或检查实例日志。")
	default:
		// Never log the resolver error text: it may embed instance paths.
		h.logger.Warn("workbench resolve failed", "instance", instanceID, "kind", errorKind(err))
		writeJSONError(w, r.Method, http.StatusInternalServerError, codeInternal, "实例状态查询失败。")
	}
}

// serveHTTP forwards a regular HTTP/SSE request through httputil.ReverseProxy.
func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request, route workbenchRoute, upstream *url.URL, port int) {
	start := time.Now()
	h.reverseProxy(route.instanceID, upstream, port).ServeHTTP(w, r)
	h.logger.Debug("workbench proxy", "instance", route.instanceID, "method", r.Method,
		"duration_ms", time.Since(start).Milliseconds())
}

// reverseProxy builds the per-request ReverseProxy. FlushInterval=-1 makes
// every write flush immediately, which is required for SSE and other streaming
// responses. The ErrorLog is discarded because net/http errors may embed the
// upstream URL (and therefore the bootstrap token).
func (h *Handler) reverseProxy(instanceID string, upstream *url.URL, port int) *httputil.ReverseProxy {
	upstreamOrigin := upstream.Scheme + "://" + upstream.Host
	return &httputil.ReverseProxy{
		Transport:     h.transport,
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = upstream.Scheme
			out.URL.Host = upstream.Host
			out.URL.Path = upstream.Path
			out.URL.RawPath = ""
			// The inbound Host must never be forwarded: the upstream sees the
			// loopback authority only.
			out.Host = upstream.Host
			out.Header = filteredRequestHeaders(pr.In, upstreamOrigin)
			if !requestHasBody(pr.In.Method) {
				out.Body = nil
				out.ContentLength = 0
				out.TransferEncoding = nil
			}
		},
		ModifyResponse: func(res *http.Response) error {
			rewriteResponseHeaders(res, instanceID, port)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.handleUpstreamError(w, r, instanceID, err)
		},
	}
}

// handleUpstreamError maps transport failures to 504/502 JSON responses.
func (h *Handler) handleUpstreamError(w http.ResponseWriter, r *http.Request, instanceID string, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		// The browser went away; there is nobody left to answer.
		h.logger.Debug("workbench proxy canceled", "instance", instanceID, "kind", errorKind(err))
		return
	}
	if isUpstreamTimeout(err) {
		h.logger.Warn("workbench upstream timeout", "instance", instanceID)
		writeJSONError(w, r.Method, http.StatusGatewayTimeout, codeUpstreamTimeout, "工作台响应超时，请稍后重试。")
		return
	}
	h.logger.Warn("workbench upstream error", "instance", instanceID, "kind", errorKind(err))
	writeJSONError(w, r.Method, http.StatusBadGateway, codeUpstreamError, "无法连接实例工作台，请确认实例仍在运行。")
}

// isUpstreamTimeout distinguishes "waited too long for response headers" (504)
// from dial/connection failures (502). Dial errors are *net.OpError values;
// the response header timeout is a bare net.Error with Timeout()==true.
func isUpstreamTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		return false
	}
	var opErr *net.OpError
	return !errors.As(err, &opErr)
}

// errorKind classifies an error for logging without ever printing its message
// (messages may embed upstream URLs carrying bootstrap tokens).
func errorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "network"
	}
	return "other"
}
