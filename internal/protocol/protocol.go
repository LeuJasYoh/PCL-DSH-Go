// SPDX-License-Identifier: GPL-3.0-or-later

// Package protocol holds cookie and header names shared by the console,
// the workbench proxy and the web client. Keep web/src/config.ts in sync.
package protocol

const (
	// SessionCookieName carries the manager's opaque session id.
	SessionCookieName = "JSESSIONID"
	// CSRFCookieName is the readable double-submit CSRF cookie.
	CSRFCookieName = "XSRF-TOKEN"
	// CSRFHeaderName is the request header that echoes CSRFCookieName.
	CSRFHeaderName = "X-XSRF-TOKEN"
	// DSHAuthCookiePrefix identifies cookies owned by the DSH instance.
	DSHAuthCookiePrefix = "dsh-auth-"
)
