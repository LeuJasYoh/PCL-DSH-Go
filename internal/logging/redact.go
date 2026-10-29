// SPDX-License-Identifier: MIT

package logging

import "regexp"

// redacted 是脱敏后的占位文本。
const redacted = "***"

// 脱敏规则（顺序敏感）：先处理完整的 authorization/cookie 头与 JSON 引号值，再处理键值对。
var (
	reAuthorization = regexp.MustCompile("(?i)(authorization\\s*[:=]\\s*)(?:(?:bearer|basic|token|digest)\\s+)?[^\\s,;]+")
	reCookie        = regexp.MustCompile("(?i)((?:set-)?cookie\\s*[:=]\\s*)[^\\r\\n]+")
	reSecretJSON    = regexp.MustCompile("(?i)([\"']?(?:password|passwd|pwd|access_token|refresh_token|id_token|client_secret|token|api[_-]?key|apikey|secret)[\"']?\\s*:\\s*\")((?:[^\"\\\\]|\\\\.)*)(\")")
	reBearer        = regexp.MustCompile("(?i)\\b(bearer\\s+)[A-Za-z0-9._~+/-]{8,}=*")
	reSecretKV      = regexp.MustCompile("(?i)([\"']?(?:password|passwd|pwd|access_token|refresh_token|id_token|client_secret|token|api[_-]?key|apikey|secret)[\"']?\\s*(?::|%3d|=)\\s*[\"']?)[^\\s,;\"'&}]+")
	reAPIKeyShape   = regexp.MustCompile("sk-[A-Za-z0-9_-]{12,}")
)

// Redact 对日志文本脱敏：token=、password=、api_key=、authorization/cookie 头、URL 查询串中的
// 敏感值（含 %3D 形式的 URL 编码）以及 sk- 形态的 API Key 替换为 "***"，其余文本保持不变。
// Redact 幂等：对结果再次调用不会再改写。
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = reAuthorization.ReplaceAllString(s, "$1"+redacted)
	s = reCookie.ReplaceAllString(s, "$1"+redacted)
	s = reSecretJSON.ReplaceAllString(s, "$1"+redacted+"$3")
	s = reBearer.ReplaceAllString(s, "$1"+redacted)
	s = reSecretKV.ReplaceAllString(s, "$1"+redacted)
	s = reAPIKeyShape.ReplaceAllString(s, redacted)
	return s
}
