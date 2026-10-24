// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "空字符串", input: "", want: ""},
		{name: "普通文本不变", input: "普通日志：服务已启动，端口 8080", want: "普通日志：服务已启动，端口 8080"},
		{name: "token 键值", input: "token=abc123", want: "token=***"},
		{name: "查询串 token", input: "?token=abc&x=1", want: "?token=***&x=1"},
		{name: "查询串 access_token", input: "&access_token=abc123&next=1", want: "&access_token=***&next=1"},
		{name: "password 键值", input: "password=hunter2,next", want: "password=***,next"},
		{name: "JSON api_key", input: "{\"api_key\":\"secret-value\"}", want: "{\"api_key\":\"***\"}"},
		{name: "JSON 带空格 token", input: "{\"token\": \"abc def\"}", want: "{\"token\": \"***\"}"},
		{name: "Authorization Bearer", input: "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9", want: "Authorization: ***"},
		{name: "Authorization Basic", input: "authorization=Basic dXNlcjpwYXNz", want: "authorization=***"},
		{name: "Cookie 头", input: "Cookie: session=abc; theme=dark", want: "Cookie: ***"},
		{name: "set-cookie 头", input: "set-cookie: a=b; HttpOnly", want: "set-cookie: ***"},
		{name: "URL 编码等号", input: "token%3Dabc123", want: "token%3D***"},
		{name: "URL 双编码查询串", input: "?token%3Dabc%26x%3D1", want: "?token%3D***"},
		{name: "裸 Bearer", input: "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig", want: "Bearer ***"},
		{name: "api_key 键值", input: "api_key=abcdefgh", want: "api_key=***"},
		{name: "x-api-key 头", input: "x-api-key: abcdef", want: "x-api-key: ***"},
		{name: "sk- API Key 形态", input: "sk-1234567890abcdef", want: "***"},
		{name: "空值不处理", input: "token=", want: "token="},
		{name: "一行多个敏感值", input: "token=abc and token=def", want: "token=*** and token=***"},
		{name: "SSE 心跳不变", input: "SSE 心跳: : ping", want: "SSE 心跳: : ping"},
		{name: "完整 URL 查询串", input: "https://example.com/path?token=abc&foo=bar", want: "https://example.com/path?token=***&foo=bar"},
		{name: "URL 编码值", input: "token=abc%20def", want: "token=***"},
		{name: "authorization 无 scheme", input: "authorization: abcdef", want: "authorization: ***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Fatalf("Redact(%q) = %q, 期望 %q", tc.input, got, tc.want)
			}
			if again := Redact(got); again != got {
				t.Fatalf("Redact 不幂等：第一次 %q，第二次 %q", got, again)
			}
		})
	}
}

func TestRedactIdempotentForMixedText(t *testing.T) {
	input := "启动实例 i-0123456789ab token=secret Authorization: Bearer abcdefghijklmn " +
		"Cookie: a=b; c=d url=https://x/?token%3Dzzz&api_key=key123 {\"password\":\"p@ss\"} plain"
	once := Redact(input)
	twice := Redact(once)
	if once != twice {
		t.Fatalf("混合文本脱敏不幂等：\n第一次 %q\n第二次 %q", once, twice)
	}
	for _, leaked := range []string{"secret", "abcdefghijklmn", "a=b; c=d", "zzz", "key123", "p@ss"} {
		if strings.Contains(once, leaked) {
			t.Errorf("脱敏后仍包含敏感值 %q：%q", leaked, once)
		}
	}
}
