// SPDX-License-Identifier: GPL-3.0-or-later

package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseWorkbenchPath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		instanceID string
		remainder  string
		matched    bool
	}{
		{name: "root with trailing slash", path: "/workbench/i-abc/", instanceID: "i-abc", remainder: "/", matched: true},
		{name: "root without trailing slash", path: "/workbench/i-abc", instanceID: "i-abc", remainder: "", matched: true},
		{name: "nested", path: "/workbench/i-abc/assets/app.js", instanceID: "i-abc", remainder: "/assets/app.js", matched: true},
		{name: "empty id", path: "/workbench/", instanceID: "", remainder: "", matched: true},
		{name: "prefix only", path: "/workbench", matched: false},
		{name: "api path", path: "/api/health", matched: false},
		{name: "root", path: "/", matched: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instanceID, remainder, matched := parseWorkbenchPath(test.path)
			if matched != test.matched || instanceID != test.instanceID || remainder != test.remainder {
				t.Fatalf("parseWorkbenchPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
					test.path, instanceID, remainder, matched, test.instanceID, test.remainder, test.matched)
			}
		})
	}
}

func TestValidInstanceID(t *testing.T) {
	valid := []string{
		"i-0123456789ab",
		"abc",
		"A.b_c-9",
		strings.Repeat("a", maxInstanceIDLength),
		"-",
	}
	for _, id := range valid {
		if !validInstanceID(id) {
			t.Errorf("validInstanceID(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"",
		".",
		"..",
		"../x",
		"i abc",
		"i/abc",
		"i%2Fabc",
		"i" + string(rune(92)) + "abc",
		"i" + string(rune(0)) + "abc",
		"i" + string(rune(10)) + "abc",
		strings.Repeat("a", maxInstanceIDLength+1),
	}
	for _, id := range invalid {
		if validInstanceID(id) {
			t.Errorf("validInstanceID(%q) = true, want false", id)
		}
	}
}

func TestUnsafeProxyPath(t *testing.T) {
	safe := []string{"/", "/foo", "/assets/app.js", "/a/./b", "/a/b."}
	for _, path := range safe {
		if unsafeProxyPath(path) {
			t.Errorf("unsafeProxyPath(%q) = true, want false", path)
		}
	}
	unsafe := []string{
		"/../etc/passwd",
		"/a/../../b",
		"/a/..",
		"/a" + string(rune(0)) + "b",
		"/a" + string(rune(31)) + "b",
		"/a" + string(rune(127)) + "b",
	}
	for _, path := range unsafe {
		if !unsafeProxyPath(path) {
			t.Errorf("unsafeProxyPath(%q) = false, want true", path)
		}
	}
}

func TestJoinUpstreamPath(t *testing.T) {
	tests := []struct {
		base      string
		remainder string
		want      string
	}{
		{base: "", remainder: "/", want: "/"},
		{base: "/", remainder: "/", want: "/"},
		{base: "/", remainder: "/assets/app.js", want: "/assets/app.js"},
		{base: "/app/", remainder: "/assets/app.js", want: "/app/assets/app.js"},
		{base: "/app", remainder: "/assets/app.js", want: "/app/assets/app.js"},
		{base: "/app/", remainder: "/", want: "/app/"},
		{base: "app", remainder: "/x", want: "/x"},
		{base: "/app/../etc", remainder: "/x", want: "/x"},
		{base: "/app" + string(rune(0)), remainder: "/x", want: "/x"},
	}
	for _, test := range tests {
		if got := joinUpstreamPath(test.base, test.remainder); got != test.want {
			t.Errorf("joinUpstreamPath(%q, %q) = %q, want %q", test.base, test.remainder, got, test.want)
		}
	}
}

func TestHasTokenQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{query: "", want: false},
		{query: "a=1", want: false},
		{query: "token=", want: false},
		{query: "token", want: false},
		{query: "token=abc", want: true},
		{query: "a=1&token=abc", want: true},
		{query: "a=1&token&b=2", want: false},
		{query: "token=abc&token=", want: true},
		{query: "a=token=abc", want: false},
		{query: "Token=abc", want: false},
	}
	for _, test := range tests {
		if got := hasTokenQuery(test.query); got != test.want {
			t.Errorf("hasTokenQuery(%q) = %v, want %v", test.query, got, test.want)
		}
	}
}

func TestHasDshAuthCookie(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "none"},
		{name: "console only", values: []string{"JSESSIONID=a; XSRF-TOKEN=b"}},
		{name: "dsh cookie", values: []string{"JSESSIONID=a; dsh-auth-x=v"}, want: true},
		{name: "dsh cookie second header", values: []string{"JSESSIONID=a", "dsh-auth-x=v"}, want: true},
		{name: "prefix requires dash", values: []string{"dsh-authx=v"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			for _, value := range test.values {
				header.Add("Cookie", value)
			}
			if got := hasDshAuthCookie(header); got != test.want {
				t.Fatalf("hasDshAuthCookie(%v) = %v, want %v", test.values, got, test.want)
			}
		})
	}
}

func TestDshCookieHeader(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "empty"},
		{name: "console cookies dropped", values: []string{"JSESSIONID=app; XSRF-TOKEN=csrf"}, want: ""},
		{name: "console cookie lowercase dropped", values: []string{"jsessionid=app; xsrf-token=csrf"}, want: ""},
		{name: "dsh and other cookies kept", values: []string{"JSESSIONID=app; dsh-auth-a=1; theme=dark"}, want: "dsh-auth-a=1; theme=dark"},
		{name: "multiple header lines", values: []string{"JSESSIONID=app; dsh-auth-a=1", "theme=dark"}, want: "dsh-auth-a=1; theme=dark"},
		{name: "value with equals kept", values: []string{"dsh-auth-a=abc=="}, want: "dsh-auth-a=abc=="},
		{name: "spaces trimmed", values: []string{"  JSESSIONID=app ;  dsh-auth-a=1  "}, want: "dsh-auth-a=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			for _, value := range test.values {
				header.Add("Cookie", value)
			}
			if got := dshCookieHeader(header); got != test.want {
				t.Fatalf("dshCookieHeader(%v) = %q, want %q", test.values, got, test.want)
			}
		})
	}
}

func TestRewriteSetCookie(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "real dsh cookie keeps strict and max-age",
			input: "dsh-auth-x=abc; Path=/; HttpOnly; SameSite=Strict; Max-Age=2592000",
			want:  "dsh-auth-x=abc; HttpOnly; SameSite=Strict; Max-Age=2592000; Path=/workbench/i-abc",
		},
		{
			name:  "domain and secure dropped",
			input: "dsh-auth-x=abc; Domain=example.com; Secure; Path=/; HttpOnly",
			want:  "dsh-auth-x=abc; HttpOnly; Path=/workbench/i-abc",
		},
		{
			name:  "same site none downgraded",
			input: "session=abc; Path=/; SameSite=None",
			want:  "session=abc; SameSite=Lax; Path=/workbench/i-abc",
		},
		{
			name:  "name value only",
			input: "dsh-auth-x=abc",
			want:  "dsh-auth-x=abc; Path=/workbench/i-abc",
		},
		{
			name:  "leading separators skipped",
			input: " ; dsh-auth-x=abc ; Path=/x ; ",
			want:  "dsh-auth-x=abc; Path=/workbench/i-abc",
		},
		{
			name:  "secure flag without value dropped",
			input: "dsh-auth-x=abc; Secure; Path=/",
			want:  "dsh-auth-x=abc; Path=/workbench/i-abc",
		},
		{
			name:  "empty header unchanged",
			input: "",
			want:  "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rewriteSetCookie(test.input, "i-abc"); got != test.want {
				t.Fatalf("rewriteSetCookie(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestRewriteLocation(t *testing.T) {
	const instanceID = "i-abc"
	const port = 3080
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{name: "relative dot slash unchanged", location: "./", want: "./"},
		{name: "relative parent unchanged", location: "../next", want: "../next"},
		{name: "relative path unchanged", location: "next", want: "next"},
		{name: "empty unchanged", location: "", want: ""},
		{name: "absolute path prefixed", location: "/", want: "/workbench/i-abc/"},
		{name: "absolute path with query prefixed", location: "/login?x=1", want: "/workbench/i-abc/login?x=1"},
		{name: "escaped absolute path preserved", location: "/a%20b?q=%26", want: "/workbench/i-abc/a%20b?q=%26"},
		{name: "loopback absolute url prefixed", location: "http://127.0.0.1:3080/cb?code=1", want: "/workbench/i-abc/cb?code=1"},
		{name: "localhost absolute url prefixed", location: "http://localhost:3080/", want: "/workbench/i-abc/"},
		{name: "loopback url without path", location: "http://127.0.0.1:3080", want: "/workbench/i-abc/"},
		{name: "loopback wrong port unchanged", location: "http://127.0.0.1:9999/x", want: "http://127.0.0.1:9999/x"},
		{name: "loopback no port unchanged", location: "http://127.0.0.1/cb", want: "http://127.0.0.1/cb"},
		{name: "external absolute unchanged", location: "https://example.com/x", want: "https://example.com/x"},
		{name: "protocol relative unchanged", location: "//cdn.example.com/x", want: "//cdn.example.com/x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rewriteLocation(test.location, instanceID, port); got != test.want {
				t.Fatalf("rewriteLocation(%q) = %q, want %q", test.location, got, test.want)
			}
		})
	}
}

func TestRewriteResponseHeaders(t *testing.T) {
	res := &http.Response{Header: http.Header{}}
	res.Header.Set("Cache-Control", "public, max-age=60")
	res.Header.Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")
	res.Header.Set("Pragma", "no-cache")
	res.Header.Set("Content-Type", "text/html")
	res.Header.Add("Set-Cookie", "dsh-auth-a=v; Path=/; HttpOnly")
	res.Header.Add("Set-Cookie", "other=b; Path=/; SameSite=None")
	res.Header.Set("Location", "http://127.0.0.1:3080/next?x=1")

	rewriteResponseHeaders(res, "i-abc", 3080)

	if got := res.Header.Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Fatalf("Cache-Control = %v, want [no-store]", got)
	}
	if got := res.Header.Get("Expires"); got != "" {
		t.Fatalf("Expires = %q, want empty", got)
	}
	if got := res.Header.Get("Pragma"); got != "" {
		t.Fatalf("Pragma = %q, want empty", got)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html" {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
	cookies := res.Header.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("Set-Cookie count = %d, want 2", len(cookies))
	}
	if cookies[0] != "dsh-auth-a=v; HttpOnly; Path=/workbench/i-abc" {
		t.Fatalf("Set-Cookie[0] = %q", cookies[0])
	}
	if cookies[1] != "other=b; SameSite=Lax; Path=/workbench/i-abc" {
		t.Fatalf("Set-Cookie[1] = %q", cookies[1])
	}
	if got := res.Header.Get("Location"); got != "/workbench/i-abc/next?x=1" {
		t.Fatalf("Location = %q", got)
	}
	if got := res.Header.Get("X-Accel-Buffering"); got != "" {
		t.Fatalf("non-SSE X-Accel-Buffering = %q, want empty", got)
	}

	sse := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}}
	rewriteResponseHeaders(sse, "i-abc", 3080)
	if got := sse.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("SSE X-Accel-Buffering = %q, want no", got)
	}
}

func TestSameOriginRequest(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "same host and port", host: "127.0.0.1:18080", origin: "http://127.0.0.1:18080", want: true},
		{name: "host without port uses default", host: "example.com", origin: "http://example.com", want: true},
		{name: "https host without port uses 443", host: "example.com", origin: "https://example.com", want: true},
		{name: "explicit matching port", host: "example.com:443", origin: "https://example.com", want: true},
		{name: "different host", host: "127.0.0.1:18080", origin: "http://evil.example:18080", want: false},
		{name: "different port", host: "127.0.0.1:18080", origin: "http://127.0.0.1:18081", want: false},
		{name: "origin default port mismatch", host: "127.0.0.1:18080", origin: "http://127.0.0.1", want: false},
		{name: "ipv6 with port", host: "[::1]:18080", origin: "http://[::1]:18080", want: true},
		{name: "ipv6 without port", host: "[::1]", origin: "http://[::1]:80", want: true},
		{name: "missing origin allowed", host: "127.0.0.1:18080", origin: "", want: true},
		{name: "missing host rejected", host: "", origin: "http://127.0.0.1:18080", want: false},
		{name: "null origin rejected", host: "127.0.0.1:18080", origin: "null", want: false},
		{name: "non http scheme rejected", host: "127.0.0.1:18080", origin: "ftp://127.0.0.1:18080", want: false},
		{name: "userinfo rejected", host: "127.0.0.1:18080", origin: "http://user@127.0.0.1:18080", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/workbench/i-abc/ws", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if got := sameOriginRequest(request); got != test.want {
				t.Fatalf("sameOriginRequest(host=%q, origin=%q) = %v, want %v",
					test.host, test.origin, got, test.want)
			}
		})
	}
}

func TestNormalizeCloseCode(t *testing.T) {
	tests := []struct {
		code int
		want int
	}{
		{code: 1000, want: 1000},
		{code: 1001, want: 1001},
		{code: 1003, want: 1003},
		{code: 1004, want: 1000},
		{code: 1005, want: 1000},
		{code: 1006, want: 1000},
		{code: 1009, want: 1009},
		{code: 1011, want: 1011},
		{code: 1013, want: 1013},
		{code: 1015, want: 1000},
		{code: 3000, want: 3000},
		{code: 4999, want: 4999},
		{code: 5000, want: 1000},
		{code: 0, want: 1000},
	}
	for _, test := range tests {
		if got := normalizeCloseCode(test.code); got != test.want {
			t.Errorf("normalizeCloseCode(%d) = %d, want %d", test.code, got, test.want)
		}
	}
}

func TestNormalizeCloseText(t *testing.T) {
	if got := normalizeCloseText("bye"); got != "bye" {
		t.Fatalf("normalizeCloseText(bye) = %q", got)
	}
	ascii := strings.Repeat("a", 200)
	if got := normalizeCloseText(ascii); len(got) != maxCloseReasonBytes {
		t.Fatalf("ascii close text length = %d, want %d", len(got), maxCloseReasonBytes)
	}
	multibyte := strings.Repeat("中", 100)
	got := normalizeCloseText(multibyte)
	if len(got) > maxCloseReasonBytes || !utf8.ValidString(got) {
		t.Fatalf("multibyte close text invalid: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	got = normalizeCloseText(strings.Repeat("é", 100))
	if len(got) > maxCloseReasonBytes || !utf8.ValidString(got) {
		t.Fatalf("two-byte close text invalid: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestFilteredRequestHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://console.example/workbench/i-abc/echo", nil)
	request.Header.Set("Cookie", "JSESSIONID=sess; XSRF-TOKEN=csrf; dsh-auth-a=1; theme=dark")
	request.Header.Set("Origin", "http://console.example")
	request.Header.Set("Referer", "http://console.example/")
	request.Header.Set("X-XSRF-TOKEN", "csrf")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.Header.Set("Forwarded", "for=1.2.3.4")
	request.Header.Set("X-Forwarded-For", "1.2.3.4")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Connection", "keep-alive, X-Hop-One, X-Hop-Two")
	request.Header.Set("Upgrade", "h2c")
	request.Header.Set("Content-Length", "10")
	request.Header.Set("X-Hop-One", "secret-one")
	request.Header.Set("X-Hop-Two", "secret-two")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer test")
	request.Header.Set("X-Custom", "value")

	filtered := filteredRequestHeaders(request, "http://127.0.0.1:3080")

	if got := filtered.Get("Cookie"); got != "dsh-auth-a=1; theme=dark" {
		t.Fatalf("Cookie = %q", got)
	}
	if got := filtered.Get("Origin"); got != "http://127.0.0.1:3080" {
		t.Fatalf("Origin = %q", got)
	}
	for _, name := range []string{"Referer", "X-XSRF-TOKEN", "X-CSRF-Token", "Forwarded",
		"X-Forwarded-For", "X-Forwarded-Proto", "Connection", "Upgrade", "Content-Length",
		"X-Hop-One", "X-Hop-Two"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("header %q must not be forwarded", name)
		}
	}
	if got := filtered.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := filtered.Get("Authorization"); got != "Bearer test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := filtered.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q", got)
	}
}

func TestIsProxyableMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead} {
		if !isProxyableMethod(method) {
			t.Errorf("isProxyableMethod(%q) = false, want true", method)
		}
	}
	for _, method := range []string{http.MethodOptions, http.MethodTrace, http.MethodConnect} {
		if isProxyableMethod(method) {
			t.Errorf("isProxyableMethod(%q) = true, want false", method)
		}
	}
}

func TestRequestHasBody(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		if !requestHasBody(method) {
			t.Errorf("requestHasBody(%q) = false, want true", method)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodDelete} {
		if requestHasBody(method) {
			t.Errorf("requestHasBody(%q) = true, want false", method)
		}
	}
}

func TestValidUpstreamURL(t *testing.T) {
	tests := []struct {
		name       string
		urlScheme  string
		wantScheme string
		host       string
		port       int
		want       bool
	}{
		{name: "loopback http", urlScheme: "http", wantScheme: "http", host: "127.0.0.1:3080", port: 3080, want: true},
		{name: "loopback ws", urlScheme: "ws", wantScheme: "ws", host: "127.0.0.1:3080", port: 3080, want: true},
		{name: "wrong host", urlScheme: "http", wantScheme: "http", host: "localhost:3080", port: 3080, want: false},
		{name: "wrong port", urlScheme: "http", wantScheme: "http", host: "127.0.0.1:3081", port: 3080, want: false},
		{name: "wrong scheme", urlScheme: "https", wantScheme: "http", host: "127.0.0.1:3080", port: 3080, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := &url.URL{Scheme: test.urlScheme, Host: test.host}
			if got := validUpstreamURL(parsed, test.wantScheme, test.port); got != test.want {
				t.Fatalf("validUpstreamURL(%s://%s, %q, %d) = %v, want %v",
					test.urlScheme, test.host, test.wantScheme, test.port, got, test.want)
			}
		})
	}
	if validUpstreamURL(nil, "http", 1) {
		t.Fatal("nil URL must be rejected")
	}
	withUser := &url.URL{Scheme: "http", Host: "127.0.0.1:3080", User: url.User("u")}
	if validUpstreamURL(withUser, "http", 3080) {
		t.Fatal("URL with userinfo must be rejected")
	}
}

func TestRequiresOriginCheck(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !requiresOriginCheck(method) {
			t.Errorf("requiresOriginCheck(%q) = false, want true", method)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if requiresOriginCheck(method) {
			t.Errorf("requiresOriginCheck(%q) = true, want false", method)
		}
	}
}
