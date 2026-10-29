// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Audit 写 logsDir/audit-YYYY-MM-DD.log 审计日志：每行一个 JSON 对象。
// 调用方保证传入内容已脱敏；这里仍做一次防御性脱敏与敏感键掩码。
type Audit struct {
	mu   sync.Mutex
	file *dailyFile
}

// OpenAudit 打开（必要时创建）审计日志目录与当日文件。
func OpenAudit(logsDir string) (*Audit, error) {
	dir := strings.TrimSpace(logsDir)
	if dir == "" {
		return nil, errors.New("审计日志目录不能为空")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析审计日志目录绝对路径失败：%w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建审计日志目录 %s 失败：%w", dir, err)
	}
	file, err := newDailyFile(dir, "audit", time.Now)
	if err != nil {
		return nil, err
	}
	return &Audit{file: file}, nil
}

// Event 追加一条审计事件。attrs 为成对的 key/value（slog 风格）；
// 字符串值会经 Redact 脱敏，敏感键（token/password/api_key/authorization/cookie 等）的值一律写为 "***"。
func (a *Audit) Event(event string, attrs ...any) {
	if a == nil {
		return
	}
	line := buildAuditLine(event, attrs)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return
	}
	if _, err := a.file.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "pcl-dsh-go: 写审计日志失败：%v\n", err)
	}
}

// Close 关闭审计日志；可重复调用。
func (a *Audit) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

// buildAuditLine 生成单行 JSON：{"ts":...,"event":...,<attrs...>} 并以换行结尾。
func buildAuditLine(event string, attrs []any) []byte {
	var buf bytes.Buffer
	buf.WriteString("{\"ts\":")
	writeJSONValue(&buf, time.Now().Format(time.RFC3339Nano))
	buf.WriteString(",\"event\":")
	writeJSONValue(&buf, Redact(event))
	for i := 0; i+1 < len(attrs); i += 2 {
		key := fmt.Sprint(attrs[i])
		if key == "" {
			continue
		}
		buf.WriteByte(',')
		writeJSONValue(&buf, key)
		buf.WriteByte(':')
		writeJSONValue(&buf, auditValue(key, attrs[i+1]))
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

// auditValue 对属性值做防御性处理：敏感键直接掩码，字符串值走 Redact。
func auditValue(key string, value any) any {
	if sensitiveKey(key) {
		return redacted
	}
	if s, ok := value.(string); ok {
		return Redact(s)
	}
	return value
}

// writeJSONValue 写一个 JSON 值；不可序列化的值退化为脱敏后的字符串。
func writeJSONValue(buf *bytes.Buffer, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded, _ = json.Marshal(Redact(fmt.Sprint(value)))
	}
	buf.Write(encoded)
}

// sensitiveKey 判断审计属性键是否属于敏感信息。
func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "-", "_")
	switch k {
	case "token", "access_token", "refresh_token", "id_token", "auth_token",
		"password", "passwd", "pwd",
		"api_key", "apikey", "secret", "client_secret",
		"authorization", "cookie", "set_cookie",
		"session", "session_id", "csrf_token", "xsrf_token":
		return true
	}
	return false
}
