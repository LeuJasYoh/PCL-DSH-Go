// SPDX-License-Identifier: GPL-3.0-or-later

package directaccess

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}

func requireAPIError(t *testing.T, err error, wantStatus int) *apierr.Error {
	t.Helper()
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("error %v is not *apierr.Error", err)
	}
	if apiErr.Status != wantStatus {
		t.Fatalf("status = %d, want %d", apiErr.Status, wantStatus)
	}
	if strings.TrimSpace(apiErr.Message) == "" {
		t.Fatal("error message must not be empty")
	}
	return apiErr
}

func requireNoDirectURL(t *testing.T, dto DTO) {
	t.Helper()
	if dto.DirectURL != nil {
		t.Fatalf("DirectURL = %q, want nil", *dto.DirectURL)
	}
	if dto.Ready {
		t.Fatal("Ready = true, want false")
	}
}

func TestBuildReady(t *testing.T) {
	input := Input{
		Host:      "example.com:18080",
		SSHUser:   "root",
		Port:      3080,
		Ready:     true,
		Bootstrap: mustParseURL(t, "http://127.0.0.1:3080/?token=abc123"),
	}
	dto, err := Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !dto.Ready {
		t.Fatal("Ready = false, want true")
	}
	if dto.Port != 3080 {
		t.Fatalf("Port = %d, want 3080", dto.Port)
	}
	if dto.DirectURL == nil {
		t.Fatal("DirectURL = nil, want the loopback URL")
	}
	if want := "http://127.0.0.1:3080/?token=abc123"; *dto.DirectURL != want {
		t.Fatalf("DirectURL = %q, want %q", *dto.DirectURL, want)
	}
	if want := "ssh -L 3080:127.0.0.1:3080 root@example.com"; dto.TunnelCommand != want {
		t.Fatalf("TunnelCommand = %q, want %q", dto.TunnelCommand, want)
	}
	if dto.SSHHost != "example.com" {
		t.Fatalf("SSHHost = %q, want example.com", dto.SSHHost)
	}
	if dto.SSHUser != "root" {
		t.Fatalf("SSHUser = %q, want root", dto.SSHUser)
	}
	if !strings.Contains(dto.Note, "SSH") || !strings.Contains(dto.Note, "token") {
		t.Fatalf("Note %q should mention the SSH tunnel and token validity", dto.Note)
	}
	if strings.Contains(dto.Note, "abc123") {
		t.Fatal("Note must not contain the bootstrap token value")
	}
}

func TestBuildHostVariants(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantHost string
	}{
		{name: "domain", host: "example.com", wantHost: "example.com"},
		{name: "domain with port", host: "example.com:18080", wantHost: "example.com"},
		{name: "ipv4", host: "203.0.113.7", wantHost: "203.0.113.7"},
		{name: "ipv4 with port", host: "203.0.113.7:18080", wantHost: "203.0.113.7"},
		{name: "localhost", host: "localhost", wantHost: "localhost"},
		{name: "internal underscore host", host: "my_server.internal", wantHost: "my_server.internal"},
		{name: "outer whitespace trimmed", host: "  example.com  ", wantHost: "example.com"},
		{name: "ipv6 literal", host: "[2001:db8::1]", wantHost: "[2001:db8::1]"},
		{name: "ipv6 literal with port", host: "[2001:db8::1]:18080", wantHost: "[2001:db8::1]"},
		{name: "case preserved", host: "EXAMPLE.COM", wantHost: "EXAMPLE.COM"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dto, err := Build(Input{Host: test.host, Port: 3080})
			if err != nil {
				t.Fatalf("Build(host=%q): %v", test.host, err)
			}
			if dto.SSHHost != test.wantHost {
				t.Fatalf("SSHHost = %q, want %q", dto.SSHHost, test.wantHost)
			}
			if want := "ssh -L 3080:127.0.0.1:3080 root@" + test.wantHost; dto.TunnelCommand != want {
				t.Fatalf("TunnelCommand = %q, want %q", dto.TunnelCommand, want)
			}
		})
	}
}

func TestBuildInvalidHost(t *testing.T) {
	invalidHosts := []string{
		"",
		"   ",
		"example.com:",
		"example.com:abc",
		"example.com:0",
		"example.com:70000",
		"example.com:80x",
		"user@example.com",
		"example.com/path",
		"example.com" + string(rune(13)) + string(rune(10)) + "X-Injected: 1",
		"exa mple.com",
		"[::1",
		"[]",
		"[127.0.0.1]",
		"[::1]x",
		"[::1]:abc",
		"::1",
		"example..com",
		".example.com",
		"example.com.",
		"-example.com",
		"example-.com",
		strings.Repeat("a", 254),
		strings.Repeat("a", 64) + ".example.com",
	}
	for index, host := range invalidHosts {
		t.Run("host_case_"+string(rune('a'+index%26)), func(t *testing.T) {
			dto, err := Build(Input{Host: host, Port: 3080})
			if err == nil {
				t.Fatalf("Build(host=%q) succeeded, want a 400 error", host)
			}
			apiErr := requireAPIError(t, err, http.StatusBadRequest)
			if apiErr.Code != apierr.CodeBadRequest {
				t.Fatalf("code = %q, want %q", apiErr.Code, apierr.CodeBadRequest)
			}
			requireNoDirectURL(t, dto)
			if dto.TunnelCommand != "" {
				t.Fatalf("TunnelCommand = %q, want empty on error", dto.TunnelCommand)
			}
		})
	}
}

func TestBuildSSHUser(t *testing.T) {
	valid := []struct {
		name     string
		user     string
		wantUser string
	}{
		{name: "empty defaults to root", user: "", wantUser: "root"},
		{name: "whitespace defaults to root", user: "   ", wantUser: "root"},
		{name: "root", user: "root", wantUser: "root"},
		{name: "custom", user: "ubuntu", wantUser: "ubuntu"},
		{name: "allowed charset", user: "user.name-_1", wantUser: "user.name-_1"},
		{name: "outer whitespace trimmed", user: "  ubuntu  ", wantUser: "ubuntu"},
		{name: "max length", user: strings.Repeat("a", 32), wantUser: strings.Repeat("a", 32)},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			dto, err := Build(Input{Host: "example.com", SSHUser: test.user, Port: 3080})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if dto.SSHUser != test.wantUser {
				t.Fatalf("SSHUser = %q, want %q", dto.SSHUser, test.wantUser)
			}
			if want := "ssh -L 3080:127.0.0.1:3080 " + test.wantUser + "@example.com"; dto.TunnelCommand != want {
				t.Fatalf("TunnelCommand = %q, want %q", dto.TunnelCommand, want)
			}
		})
	}

	invalid := []string{
		"root user",
		"user@host",
		"user;rm -rf /",
		"user|id",
		"user$(id)",
		"user(id)",
		"-root",
		strings.Repeat("a", 33),
		"用户",
		"user name",
	}
	for index, user := range invalid {
		t.Run("invalid_case_"+string(rune('a'+index%26)), func(t *testing.T) {
			_, err := Build(Input{Host: "example.com", SSHUser: user, Port: 3080})
			if err == nil {
				t.Fatalf("Build(sshUser=%q) succeeded, want a 400 error", user)
			}
			requireAPIError(t, err, http.StatusBadRequest)
		})
	}
}

func TestBuildInvalidPort(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		_, err := Build(Input{Host: "example.com", Port: port})
		if err == nil {
			t.Fatalf("Build(port=%d) succeeded, want a 400 error", port)
		}
		apiErr := requireAPIError(t, err, http.StatusBadRequest)
		if apiErr.Code != apierr.CodeValidation {
			t.Fatalf("code = %q, want %q", apiErr.Code, apierr.CodeValidation)
		}
	}
}

func TestBuildNotReady(t *testing.T) {
	tests := []struct {
		name       string
		input      Input
		wantNote   string
		wantSubstr string
	}{
		{
			name: "status note forwarded",
			input: Input{
				Host:       "example.com:18080",
				Port:       3080,
				StatusNote: "网页服务尚未确认就绪，请等待启动完成或检查任务日志。",
			},
			wantNote: "网页服务尚未确认就绪，请等待启动完成或检查任务日志。",
		},
		{
			name:       "default note when empty",
			input:      Input{Host: "example.com", Port: 3080},
			wantSubstr: "请先启动实例",
		},
		{
			name: "bootstrap ignored while not ready",
			input: Input{
				Host:      "example.com",
				Port:      3080,
				Ready:     false,
				Bootstrap: mustParseURL(t, "http://127.0.0.1:3080/?token=abc123"),
			},
			wantSubstr: "请先启动实例",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dto, err := Build(test.input)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			requireNoDirectURL(t, dto)
			if test.wantNote != "" && dto.Note != test.wantNote {
				t.Fatalf("Note = %q, want %q", dto.Note, test.wantNote)
			}
			if test.wantSubstr != "" && !strings.Contains(dto.Note, test.wantSubstr) {
				t.Fatalf("Note = %q, want substring %q", dto.Note, test.wantSubstr)
			}
			if dto.Port != test.input.Port {
				t.Fatalf("Port = %d, want %d", dto.Port, test.input.Port)
			}
			if !strings.Contains(dto.TunnelCommand, "ssh -L 3080:127.0.0.1:3080 root@example.com") {
				t.Fatalf("TunnelCommand = %q, want a copyable tunnel command", dto.TunnelCommand)
			}
			if dto.SSHUser != "root" || dto.SSHHost != "example.com" {
				t.Fatalf("SSHUser/SSHHost = %q/%q", dto.SSHUser, dto.SSHHost)
			}
		})
	}
}

func TestBuildReadyWithoutBootstrap(t *testing.T) {
	dto, err := Build(Input{Host: "example.com", Port: 3080, Ready: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireNoDirectURL(t, dto)
	if dto.TunnelCommand == "" {
		t.Fatal("TunnelCommand must still be available")
	}
	if !strings.Contains(dto.Note, "token") || !strings.Contains(dto.Note, "重新启动") {
		t.Fatalf("Note = %q, want a token/restart hint", dto.Note)
	}
}

func TestBuildBootstrapPathAndQuery(t *testing.T) {
	tests := []struct {
		name      string
		bootstrap *url.URL
		wantURL   string
	}{
		{
			name:      "root path",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/?token=abc"),
			wantURL:   "http://127.0.0.1:3080/?token=abc",
		},
		{
			name:      "empty path defaults to slash",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080?token=abc"),
			wantURL:   "http://127.0.0.1:3080/?token=abc",
		},
		{
			name:      "custom path",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/login?token=abc"),
			wantURL:   "http://127.0.0.1:3080/login?token=abc",
		},
		{
			name:      "escaped path preserved",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/a%20b?token=abc"),
			wantURL:   "http://127.0.0.1:3080/a%20b?token=abc",
		},
		{
			name:      "special query preserved verbatim",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/o/cb?token=a%2Bb&state=%E4%B8%AD&next=%2Fhome"),
			wantURL:   "http://127.0.0.1:3080/o/cb?token=a%2Bb&state=%E4%B8%AD&next=%2Fhome",
		},
		{
			name:      "empty token value preserved",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/?token="),
			wantURL:   "http://127.0.0.1:3080/?token=",
		},
		{
			name:      "no query means no question mark",
			bootstrap: mustParseURL(t, "http://127.0.0.1:3080/health"),
			wantURL:   "http://127.0.0.1:3080/health",
		},
		{
			name:      "relative path gets a leading slash",
			bootstrap: &url.URL{Path: "foo", RawQuery: "token=xyz"},
			wantURL:   "http://127.0.0.1:3080/foo?token=xyz",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dto, err := Build(Input{Host: "example.com", Port: 3080, Ready: true, Bootstrap: test.bootstrap})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if dto.DirectURL == nil || *dto.DirectURL != test.wantURL {
				t.Fatalf("DirectURL = %v, want %q", dto.DirectURL, test.wantURL)
			}
		})
	}
}

func TestBuildDTOJSONShape(t *testing.T) {
	readyDTO, err := Build(Input{
		Host:      "example.com:18080",
		Port:      3080,
		Ready:     true,
		Bootstrap: mustParseURL(t, "http://127.0.0.1:3080/?token=abc"),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	encoded, err := json.Marshal(readyDTO)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"ready", "port", "directUrl", "tunnelCommand", "sshHost", "sshUser", "note"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("JSON field %q missing in %s", key, encoded)
		}
	}
	if fields["ready"] != true {
		t.Fatalf("ready = %v, want true", fields["ready"])
	}
	if fields["port"] != float64(3080) {
		t.Fatalf("port = %v, want 3080", fields["port"])
	}
	if directURL, ok := fields["directUrl"].(string); !ok || !strings.Contains(directURL, "token=abc") {
		t.Fatalf("directUrl = %v, want the token URL", fields["directUrl"])
	}

	notReadyDTO, err := Build(Input{Host: "example.com", Port: 3080})
	if err != nil {
		t.Fatalf("Build not ready: %v", err)
	}
	encoded, err = json.Marshal(notReadyDTO)
	if err != nil {
		t.Fatalf("marshal not ready: %v", err)
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal not ready: %v", err)
	}
	if value, ok := fields["directUrl"]; !ok || value != nil {
		t.Fatalf("directUrl = %v, want JSON null", value)
	}
	if fields["ready"] != false {
		t.Fatalf("ready = %v, want false", fields["ready"])
	}
}

func TestBuildTokenNeverInErrorOrNote(t *testing.T) {
	const token = "SECRET_BOOTSTRAP_TOKEN"
	_, err := Build(Input{
		Host:      "bad host:port",
		Port:      3080,
		Ready:     true,
		Bootstrap: &url.URL{Path: "/", RawQuery: "token=" + token},
	})
	if err == nil {
		t.Fatal("invalid host must fail")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error %q leaked the bootstrap token", err)
	}

	dto, err := Build(Input{
		Host:      "example.com",
		Port:      3080,
		Ready:     true,
		Bootstrap: &url.URL{Path: "/", RawQuery: "token=" + token},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if count := strings.Count(string(encoded), token); count != 1 {
		t.Fatalf("token appears %d times in the DTO, want exactly once (directUrl)", count)
	}
	if strings.Contains(dto.Note, token) {
		t.Fatal("Note must not contain the token")
	}
}
