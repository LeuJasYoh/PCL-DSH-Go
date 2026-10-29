// SPDX-License-Identifier: MIT

// Package directaccess builds the DTO for the "direct login" flow of the
// console: the SSH tunnel command that maps the instance's loopback port to the
// user's machine, and - when the workbench is ready - the loopback URL carrying
// the one-time DSH bootstrap token.
//
// Contract: docs/M1_INTERFACES.md §9 and docs/DSH_INTEGRATION.md §6.4.
//
// Security notes:
//   - the bootstrap token only ever appears inside DTO.DirectURL. This package
//     never logs, persists or returns it in an error message;
//   - the SSH host and user are validated strictly before being embedded in the
//     tunnel command, so a forged Host header cannot inject shell/ssh options.
package directaccess

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/ports"
)

const (
	// defaultSSHUser is used when the settings value is empty (see
	// store.Settings.DirectAccessSSHUser).
	defaultSSHUser = "root"
	maxSSHUserLen  = 32
	maxSSHHostLen  = 253
	maxDNSLabelLen = 63

	notRunningNote  = "实例未运行，请先启动实例。"
	noBootstrapNote = "实例尚未返回访问 token（可能运行时版本不支持），请停止后重新启动实例再试。"
	readyNote       = "远程访问时 DeepSeek 账号网页登录只接受回环回调地址：先执行上方 SSH 隧道命令，" +
		"再在浏览器打开直连地址完成登录；直连地址中的 token 仅在本次实例运行期间有效，请勿分享或记录。" +
		"登录凭据保存在服务器，之后可继续用工作台访问。"
)

// Input is the raw information needed to build a DirectAccess DTO. It mirrors
// docs/M1_INTERFACES.md §9 and is populated by the API layer from the instance,
// the live process handle and the request.
type Input struct {
	// Host is the request Host header. It may include a port; the port is
	// stripped and the remaining host is validated before use.
	Host string
	// SSHUser is settings.DirectAccessSSHUser ("" defaults to "root").
	SSHUser string
	// Port is the instance workbench port (1024-65535).
	Port int
	// Ready reports whether the workbench is running and confirmed ready.
	Ready bool
	// StatusNote is the Chinese not-ready reason shown to the user.
	StatusNote string
	// Bootstrap is the in-memory DSH bootstrap URL. It holds the one-time
	// token and must never be logged or persisted.
	Bootstrap *url.URL
}

// DTO is the GET /api/instances/{id}/direct-access response body
// (docs/IMPLEMENTATION_SPEC.md §4.3 DirectAccessDto).
type DTO struct {
	Ready         bool    `json:"ready"`
	Port          int     `json:"port"`
	DirectURL     *string `json:"directUrl"`
	TunnelCommand string  `json:"tunnelCommand"`
	SSHHost       string  `json:"sshHost"`
	SSHUser       string  `json:"sshUser"`
	Note          string  `json:"note"`
}

// Build validates the input and generates the direct-access DTO. Invalid input
// (Host, SSH user, port) returns *apierr.Error with HTTP 400. A not-ready
// instance is not an error: the DTO is still returned, with Ready=false, a null
// DirectURL and a Chinese note, so the tunnel command can be copied immediately.
func Build(in Input) (DTO, error) {
	host, err := normalizeSSHHost(in.Host)
	if err != nil {
		return DTO{}, err
	}
	user, err := normalizeSSHUser(in.SSHUser)
	if err != nil {
		return DTO{}, err
	}
	if !ports.ValidInstance(in.Port) {
		return DTO{}, apierr.New(http.StatusBadRequest, apierr.CodeValidation, "实例端口无效，无法生成直连信息。")
	}

	port := strconv.Itoa(in.Port)
	dto := DTO{
		Port:          in.Port,
		TunnelCommand: "ssh -L " + port + ":127.0.0.1:" + port + " " + user + "@" + host,
		SSHHost:       host,
		SSHUser:       user,
	}
	switch {
	case !in.Ready:
		dto.Note = notRunningNote
		if note := strings.TrimSpace(in.StatusNote); note != "" {
			dto.Note = note
		}
	case in.Bootstrap == nil:
		// The instance reports ready but the in-memory bootstrap URL is gone
		// (e.g. the process was restarted without a token): no direct URL.
		dto.Note = noBootstrapNote
	default:
		dto.Ready = true
		dto.DirectURL = loopbackDirectURL(in.Port, in.Bootstrap)
		dto.Note = readyNote
	}
	return dto, nil
}

// loopbackDirectURL builds http://127.0.0.1:<port>/<bootstrap path>?<query>.
// The host is always the fixed loopback address; only the bootstrap path and
// raw query (which carries the token) are taken from memory. The query is
// copied verbatim so opaque OAuth parameters survive unchanged.
func loopbackDirectURL(port int, bootstrap *url.URL) *string {
	path := bootstrap.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	value := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + path
	if bootstrap.RawQuery != "" {
		value += "?" + bootstrap.RawQuery
	}
	return &value
}

// normalizeSSHHost extracts the SSH host from a request Host header: the port
// is stripped, IPv6 literals keep their brackets and everything else must be a
// valid IPv4 address or DNS name.
func normalizeSSHHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", invalidHostError()
	}
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", invalidHostError()
		}
		literal := host[1:end]
		// Only bracketed IPv6 literals are valid in a Host header.
		if net.ParseIP(literal) == nil || !strings.Contains(literal, ":") {
			return "", invalidHostError()
		}
		if rest := host[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") || !validPortNumber(rest[1:]) {
				return "", invalidHostError()
			}
		}
		return "[" + literal + "]", nil
	}
	if index := strings.IndexByte(host, ':'); index >= 0 {
		// A bare IPv6 literal is not allowed in a Host header: either the host
		// part becomes empty or the port part is not numeric.
		if !validPortNumber(host[index+1:]) {
			return "", invalidHostError()
		}
		host = host[:index]
		if host == "" {
			return "", invalidHostError()
		}
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	if !validDNSName(host) {
		return "", invalidHostError()
	}
	return host, nil
}

// normalizeSSHUser validates settings.DirectAccessSSHUser: 1-32 characters from
// [A-Za-z0-9._-]. An empty value defaults to "root". A leading dash is rejected
// because the user is embedded as part of "user@host" and a leading dash would
// be parsed as an ssh option.
func normalizeSSHUser(raw string) (string, error) {
	user := strings.TrimSpace(raw)
	if user == "" {
		user = defaultSSHUser
	}
	if len(user) > maxSSHUserLen {
		return "", apierr.New(http.StatusBadRequest, apierr.CodeValidation, "SSH 用户名不合法：长度需为 1-32 个字符。")
	}
	for index := 0; index < len(user); index++ {
		char := user[index]
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '.', char == '_', char == '-':
		default:
			return "", apierr.New(http.StatusBadRequest, apierr.CodeValidation,
				"SSH 用户名不合法：只允许字母、数字、点、下划线和连字符。")
		}
	}
	if user[0] == '-' {
		return "", apierr.New(http.StatusBadRequest, apierr.CodeValidation, "SSH 用户名不能以连字符开头。")
	}
	return user, nil
}

// validPortNumber reports whether raw is a decimal port in 1-65535.
func validPortNumber(raw string) bool {
	if raw == "" || len(raw) > 5 {
		return false
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return false
		}
	}
	port, err := strconv.Atoi(raw)
	return err == nil && port >= 1 && port <= 65535
}

// validDNSName validates a hostname: at most 253 bytes, dot-separated 1-63 byte
// labels using [A-Za-z0-9_-] that do not start or end with a dash. Underscores
// are tolerated for internal hostnames; the charset is shell/ssh safe.
func validDNSName(name string) bool {
	if name == "" || len(name) > maxSSHHostLen {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > maxDNSLabelLen {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			char := label[index]
			switch {
			case char >= 'a' && char <= 'z':
			case char >= 'A' && char <= 'Z':
			case char >= '0' && char <= '9':
			case char == '-', char == '_':
			default:
				return false
			}
		}
	}
	return true
}

// invalidHostError is the shared 400 for a missing/forged Host header. It never
// includes the raw value (which may contain arbitrary bytes).
func invalidHostError() error {
	return apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "请求 Host 无效，无法生成 SSH 隧道命令。")
}
