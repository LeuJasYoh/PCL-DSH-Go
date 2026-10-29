// SPDX-License-Identifier: MIT

package process

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const bootstrapMarker = "dsh web:"

var (
	// ANSI 控制序列（CSI/OSC/两字节转义），用于剥色后再解析 token 和写日志。
	ansiCSIRE   = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	ansiOSCRE   = regexp.MustCompile("(?s)\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)")
	ansiOtherRE = regexp.MustCompile("\x1b[@-_]")

	// 脱敏规则（与 docs/M0_INTERFACES.md §3 logging.Redact 语义一致的最小本地实现，
	// process 包不依赖 internal/logging，避免跨包耦合/循环依赖）。
	authorizationRE = regexp.MustCompile("(?i)(authorization\\s*[:=]\\s*)(bearer\\s+|basic\\s+)?[^\\s,;]+")
	sensitiveKVRE   = regexp.MustCompile("(?i)([\"']?(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|cookie)[\"']?\\s*[:=]\\s*[\"']?)[^\\s,\"'&}]+")
	querySecretRE   = regexp.MustCompile("([?&](token|access_token|refresh_token|password|passwd|api_key|apikey|secret|key)=)[^&\\s#]+")
	apiKeyRE        = regexp.MustCompile("sk-[A-Za-z0-9_-]{12,}")

	// 环境变量/URL query 中视为敏感的键名。
	sensitiveNameRE = regexp.MustCompile("(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|passwd|cookie|authorization|credential)")
)

func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	s = ansiOSCRE.ReplaceAllString(s, "")
	s = ansiCSIRE.ReplaceAllString(s, "")
	return ansiOtherRE.ReplaceAllString(s, "")
}

// parseBootstrapURI 从一行（已去 ANSI）中解析 bootstrap URI。
// 规则（安全关键，docs/DSH_INTEGRATION.md §5.3）：
//   - 找 “dsh web:” 标记，取其后第一个空白分隔 token；
//   - 只接受 scheme=http、host ∈ {127.0.0.1, localhost, [::1]}、port == 实例端口、无 userinfo。
func parseBootstrapURI(line string, port int) *url.URL {
	index := strings.Index(line, bootstrapMarker)
	if index < 0 {
		return nil
	}
	rest := strings.TrimSpace(line[index+len(bootstrapMarker):])
	if rest == "" {
		return nil
	}
	if cut := strings.IndexAny(rest, " \t\r\n"); cut >= 0 {
		rest = rest[:cut]
	}
	parsed, err := url.Parse(rest)
	if err != nil {
		return nil
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" {
		return nil
	}
	if parsed.Port() != strconv.Itoa(port) {
		return nil
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return parsed
	default:
		return nil
	}
}

// redactor 负责把字节流中的敏感值替换为 ***。仅在单个读取 goroutine 中使用。
type redactor struct {
	values []string
	seen   map[string]struct{}
}

func newRedactor(env []string) *redactor {
	r := &redactor{seen: make(map[string]struct{})}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || len(value) < 4 || !sensitiveNameRE.MatchString(key) {
			continue
		}
		r.add(value)
	}
	return r
}

func (r *redactor) add(value string) {
	if r == nil || len(value) < 4 {
		return
	}
	if _, ok := r.seen[value]; ok {
		return
	}
	r.seen[value] = struct{}{}
	r.values = append(r.values, value)
	sort.SliceStable(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

// addURLSecrets 把 bootstrap URI query 中的敏感值加入脱敏列表（token 只存内存且不外泄）。
func (r *redactor) addURLSecrets(u *url.URL) {
	if r == nil || u == nil {
		return
	}
	for key, values := range u.Query() {
		if !sensitiveNameRE.MatchString(key) {
			continue
		}
		for _, value := range values {
			r.add(value)
		}
	}
}

func (r *redactor) redact(text string) string {
	if r != nil {
		for _, value := range r.values {
			text = strings.ReplaceAll(text, value, "***")
		}
	}
	text = authorizationRE.ReplaceAllString(text, "$1***")
	text = sensitiveKVRE.ReplaceAllString(text, "$1***")
	text = querySecretRE.ReplaceAllString(text, "$1***")
	text = apiKeyRE.ReplaceAllString(text, "***")
	return text
}
