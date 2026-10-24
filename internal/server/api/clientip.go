// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net"
	"net/http"
	"strings"
)

// Client-IP resolution for the Nginx production chain
// (docs/M2_INTERFACES.md 2).
const (
	// realIPHeader is set by the trusted local Nginx reverse proxy. It is
	// honoured only for loopback peers; any other source is ignored.
	realIPHeader = "X-Real-IP"
	// maxRealIPBytes bounds the accepted header value. An IPv6 literal is at
	// most 45 bytes; anything longer cannot be a plain address.
	maxRealIPBytes = 64
)

// clientIP returns the client address used for login rate limiting and audit
// events.
//
// The default is r.RemoteAddr. In production the manager is reached through the
// Nginx reverse proxy running on the same host, which sets X-Real-IP to the
// real client address; that header is trusted only when the direct TCP peer
// (RemoteAddr's host) is a loopback address. X-Forwarded-For is deliberately
// ignored: its chain is client-controlled and forgeable.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	if !isLoopbackPeer(remote) {
		return remote
	}
	if realIP := trustedRealIP(r.Header.Get(realIPHeader)); realIP != "" {
		return realIP
	}
	return remote
}

// isLoopbackPeer reports whether a TCP peer address (with or without port)
// belongs to a loopback interface. Hostnames are not resolved: only literal
// loopback IPs are trusted.
func isLoopbackPeer(peer string) bool {
	host := peer
	if parsedHost, _, err := net.SplitHostPort(peer); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// trustedRealIP parses an X-Real-IP value: it must be exactly one plain IP
// address (no port, no zone, no DNS name). Empty, overlong and control-character
// values are rejected, in which case the caller falls back to RemoteAddr.
func trustedRealIP(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maxRealIPBytes {
		return ""
	}
	for index := 0; index < len(value); index++ {
		if value[index] <= 0x20 || value[index] == 0x7f {
			return ""
		}
	}
	if net.ParseIP(value) == nil {
		return ""
	}
	return value
}
