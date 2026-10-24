// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// workbenchPrefix is the mount point handled by Handler.
	workbenchPrefix = "/workbench/"
	// maxInstanceIDLength bounds the instance id accepted in the URL.
	maxInstanceIDLength = 128
	// dshAuthCookiePrefix identifies cookies that belong to the DSH instance
	// and therefore must be forwarded upstream.
	dshAuthCookiePrefix = "dsh-auth-"
	// sessionCookieName / csrfCookieName belong to the console itself and must
	// never reach the DSH process.
	sessionCookieName = "JSESSIONID"
	csrfCookieName    = "XSRF-TOKEN"
)

// hopByHopHeaders are connection-scoped headers that are never forwarded in
// either direction (RFC 7230 §6.1). "Proxy-Connection" is the non-standard
// libcurl variant that some servers still send.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// blockedRequestHeaders are inbound headers the proxy never forwards. Host and
// Content-Length are recomputed by the transport; Cookie and Origin are
// rewritten; CSRF/forwarding headers must not leak into the instance.
var blockedRequestHeaders = map[string]struct{}{
	"host":           {},
	"content-length": {},
	"cookie":         {},
	"origin":         {},
	"referer":        {},
	"x-xsrf-token":   {},
	"x-csrf-token":   {},
	"forwarded":      {},
}

// blockedRequestHeader reports whether an inbound header (lower case) must not
// be forwarded to the DSH instance.
func blockedRequestHeader(name string) bool {
	if _, ok := hopByHopHeaders[name]; ok {
		return true
	}
	if _, ok := blockedRequestHeaders[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "x-forwarded-")
}

// connectionNominatedHeaders returns the header names listed in the request's
// Connection header (RFC 7230 §6.1), lower-cased. A proxy MUST remove those
// headers in addition to the fixed hop-by-hop set; Go's http.Server preserves
// the Connection header, so the proxy has to do this itself.
func connectionNominatedHeaders(header http.Header) map[string]struct{} {
	nominated := make(map[string]struct{})
	for _, value := range header.Values("Connection") {
		for _, option := range strings.Split(value, ",") {
			name := strings.ToLower(strings.TrimSpace(option))
			if name != "" {
				nominated[name] = struct{}{}
			}
		}
	}
	return nominated
}

// parseWorkbenchPath splits a request path into its {instanceID} and remainder.
// matched is false for paths outside the /workbench/ mount point; the caller is
// responsible for validating the instance id.
func parseWorkbenchPath(path string) (instanceID, remainder string, matched bool) {
	if !strings.HasPrefix(path, workbenchPrefix) {
		return "", "", false
	}
	rest := path[len(workbenchPrefix):]
	if index := strings.IndexByte(rest, '/'); index >= 0 {
		return rest[:index], rest[index:], true
	}
	return rest, "", true
}

// validInstanceID accepts the instance ids the manager may generate (i-<hex>)
// plus conservative future formats: 1-128 characters from [A-Za-z0-9._-],
// never starting with a dot. This rejects empty ids, "..", control characters,
// slashes, percent escapes and whitespace before any lookup happens.
func validInstanceID(id string) bool {
	if id == "" || len(id) > maxInstanceIDLength || id[0] == '.' {
		return false
	}
	for index := 0; index < len(id); index++ {
		char := id[index]
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

// unsafeProxyPath rejects paths containing ".." or control characters before
// they are used to build an upstream request (DSH_INTEGRATION §6.1).
func unsafeProxyPath(path string) bool {
	if strings.Contains(path, "..") {
		return true
	}
	for index := 0; index < len(path); index++ {
		if path[index] < 0x20 || path[index] == 0x7f {
			return true
		}
	}
	return false
}

// joinUpstreamPath joins the bootstrap base path with the request remainder.
// An invalid or empty base falls back to "/" so the target path can never
// escape the instance root.
func joinUpstreamPath(base, remainder string) string {
	if base == "" || base[0] != '/' || unsafeProxyPath(base) {
		base = "/"
	}
	if remainder == "" {
		remainder = "/"
	}
	if remainder[0] != '/' {
		remainder = "/" + remainder
	}
	switch {
	case base == "/":
		return remainder
	case strings.HasSuffix(base, "/"):
		return base + remainder[1:]
	default:
		return base + remainder
	}
}

// hasTokenQuery reports whether the raw query string carries a non-empty
// token parameter. It only looks at the raw parameter name/value so a
// percent-encoded "t%6Fken" is not treated as a token.
func hasTokenQuery(rawQuery string) bool {
	for _, part := range strings.Split(rawQuery, "&") {
		name, value, found := strings.Cut(part, "=")
		if found && name == "token" && value != "" {
			return true
		}
	}
	return false
}

// hasDshAuthCookie reports whether the request carries any DSH session cookie
// (dsh-auth-*). Its presence means the token bootstrap redirect is unnecessary.
func hasDshAuthCookie(header http.Header) bool {
	for _, line := range header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if strings.HasPrefix(strings.TrimSpace(name), dshAuthCookiePrefix) {
				return true
			}
		}
	}
	return false
}

// dshCookieHeader rebuilds the Cookie header for the upstream request: the
// console's own session (JSESSIONID) and CSRF token (XSRF-TOKEN) never leave
// the manager; every other cookie (all DSH cookies included) is forwarded
// byte-for-byte.
func dshCookieHeader(header http.Header) string {
	var parts []string
	for _, line := range header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, _, _ := strings.Cut(part, "=")
			if isConsoleCookieName(strings.TrimSpace(name)) {
				continue
			}
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "; ")
}

func isConsoleCookieName(name string) bool {
	return strings.EqualFold(name, sessionCookieName) || strings.EqualFold(name, csrfCookieName)
}

// filteredRequestHeaders builds the upstream request header set. Hop-by-hop,
// console-only and forging-prone headers are dropped; the Cookie header is
// rebuilt from DSH cookies only and a browser Origin is rewritten to the
// loopback instance origin.
func filteredRequestHeaders(in *http.Request, upstreamOrigin string) http.Header {
	nominated := connectionNominatedHeaders(in.Header)
	out := make(http.Header, len(in.Header))
	for name, values := range in.Header {
		lower := strings.ToLower(name)
		if blockedRequestHeader(lower) {
			continue
		}
		if _, dynamic := nominated[lower]; dynamic {
			continue
		}
		for _, value := range values {
			out.Add(name, value)
		}
	}
	if cookie := dshCookieHeader(in.Header); cookie != "" {
		out.Set("Cookie", cookie)
	}
	if in.Header.Get("Origin") != "" {
		out.Set("Origin", upstreamOrigin)
	}
	return out
}

// requestHasBody reports whether the method's request body is forwarded. GET,
// HEAD and DELETE bodies are deliberately dropped (DSH_INTEGRATION §6.2).
func requestHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// isProxyableMethod restricts /workbench to the methods the workbench uses
// (DSH_INTEGRATION §6.1).
func isProxyableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return true
	default:
		return false
	}
}

// rewriteSetCookie re-scopes an upstream Set-Cookie header to the workbench
// prefix: Domain and Secure are dropped (the console may be served over plain
// HTTP), Path becomes /workbench/{id}, and SameSite=None is downgraded to Lax
// because browsers reject SameSite=None cookies without Secure.
func rewriteSetCookie(header, instanceID string) string {
	parts := strings.Split(header, ";")
	first := -1
	for index, part := range parts {
		if strings.TrimSpace(part) != "" {
			first = index
			break
		}
	}
	if first < 0 {
		return header
	}
	attributes := make([]string, 0, len(parts)+1)
	attributes = append(attributes, strings.TrimSpace(parts[first]))
	for _, part := range parts[first+1:] {
		attribute := strings.TrimSpace(part)
		if attribute == "" {
			continue
		}
		lower := strings.ToLower(attribute)
		if strings.HasPrefix(lower, "domain=") || strings.HasPrefix(lower, "secure") || strings.HasPrefix(lower, "path=") {
			continue
		}
		if strings.HasPrefix(lower, "samesite=") && strings.Contains(lower, "none") {
			attributes = append(attributes, "SameSite=Lax")
			continue
		}
		attributes = append(attributes, attribute)
	}
	attributes = append(attributes, "Path="+workbenchPrefix+instanceID)
	return strings.Join(attributes, "; ")
}

// rewriteLocation maps an upstream Location header back into /workbench/{id}:
//   - relative values ("./", "../", "next") are kept as-is;
//   - absolute paths ("/...") get the workbench prefix;
//   - absolute loopback URLs pointing at the instance port get the prefix with
//     their path and query preserved;
//   - external absolute URLs are kept as-is.
func rewriteLocation(location, instanceID string, port int) string {
	value := strings.TrimSpace(location)
	if value == "" {
		return location
	}
	if strings.HasPrefix(value, "//") {
		// Protocol-relative external URL: never point it at the instance.
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.IsAbs() {
		if isLoopbackHTTPURL(parsed, port) {
			return withWorkbenchPrefix(instanceID, parsed.EscapedPath(), parsed.RawQuery)
		}
		return value
	}
	if strings.HasPrefix(value, "/") {
		return withWorkbenchPrefix(instanceID, parsed.EscapedPath(), parsed.RawQuery)
	}
	return value
}

// isLoopbackHTTPURL reports whether the URL is an http(s) URL on a loopback
// host with exactly the given instance port and no userinfo.
func isLoopbackHTTPURL(parsed *url.URL, port int) bool {
	if parsed == nil || parsed.User != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	if !isLoopbackHostname(parsed.Hostname()) {
		return false
	}
	return parsed.Port() == strconv.Itoa(port)
}

func isLoopbackHostname(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func withWorkbenchPrefix(instanceID, rawPath, rawQuery string) string {
	if rawPath == "" {
		rawPath = "/"
	}
	location := workbenchPrefix + instanceID + rawPath
	if rawQuery != "" {
		location += "?" + rawQuery
	}
	return location
}

// rewriteResponseHeaders applies the response-side contract: hop-by-hop
// headers are already stripped by httputil.ReverseProxy, caching headers are
// replaced with no-store, and Set-Cookie/Location values are re-scoped.
func rewriteResponseHeaders(res *http.Response, instanceID string, port int) {
	header := res.Header
	header.Del("Cache-Control")
	header.Del("Expires")
	header.Del("Pragma")
	if cookies := header.Values("Set-Cookie"); len(cookies) > 0 {
		header.Del("Set-Cookie")
		for _, cookie := range cookies {
			header.Add("Set-Cookie", rewriteSetCookie(cookie, instanceID))
		}
	}
	if locations := header.Values("Location"); len(locations) > 0 {
		header.Del("Location")
		for _, location := range locations {
			header.Add("Location", rewriteLocation(location, instanceID, port))
		}
	}
	header.Set("Cache-Control", "no-store")
	if isEventStream(header.Get("Content-Type")) {
		// Nginx buffers proxied responses by default; SSE must bypass buffering
		// even though the proxy flushes every chunk (docs/M2_INTERFACES.md §3.4).
		header.Set("X-Accel-Buffering", "no")
	}
}

// isEventStream reports whether the response Content-Type is text/event-stream.
func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}

// loopbackEndpoint returns the only host:port the proxy will ever dial.
func loopbackEndpoint(port int) (string, bool) {
	if port < 1 || port > 65535 {
		return "", false
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), true
}

// validUpstreamURL re-checks the SSRF whitelist after the upstream URL has been
// constructed from the resolver target (DSH_INTEGRATION §6.1): fixed loopback
// host, resolver-provided port, expected scheme, no userinfo.
func validUpstreamURL(parsed *url.URL, scheme string, port int) bool {
	if parsed == nil || parsed.User != nil {
		return false
	}
	if parsed.Scheme != scheme || parsed.Hostname() != "127.0.0.1" {
		return false
	}
	return parsed.Port() == strconv.Itoa(port)
}

// requiresOriginCheck reports whether the method must pass the same-origin
// check when a browser Origin header is present. GET/HEAD are safe methods and
// are not gated (docs/M2_INTERFACES.md §3.1).
func requiresOriginCheck(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOriginRequest enforces Origin/Host same-origin for browser requests,
// with default-port normalization (http→80, https→443). A Host header without
// a port uses the Origin scheme's default port. Requests without an Origin
// header (non-browser clients) are accepted; GET/HEAD callers are expected to
// skip the check entirely. This is shared by HTTP write-method CSRF hardening
// and the WebSocket handshake (DSH_INTEGRATION §6.3, M2_INTERFACES §3.1/§4).
func sameOriginRequest(r *http.Request) bool {
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
