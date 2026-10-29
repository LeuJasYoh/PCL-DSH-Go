// SPDX-License-Identifier: MIT

package process

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStripANSI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"无控制序列", "plain text", "plain text"},
		{"CSI 颜色", "\x1b[31mred\x1b[0m", "red"},
		{"CSI 多参数", "\x1b[1;32mOK\x1b[0m", "OK"},
		{"CSI 私有标记", "a\x1b[?25lb", "ab"},
		{"OSC 标题", "a\x1b]0;title\x07b", "ab"},
		{"中文", "中文\x1b[33m黄色\x1b[0m", "中文黄色"},
		{"无 ESC 不扫描", "no escape", "no escape"},
	}
	for _, tc := range cases {
		if got := stripANSI(tc.in); got != tc.want {
			t.Fatalf("%s: stripANSI(%q) = %q，期望 %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestParseBootstrapURI(t *testing.T) {
	const port = 3080
	valid := []struct {
		name  string
		line  string
		token string
	}{
		{"标准回环", "dsh web: http://127.0.0.1:3080/?token=abc123", "abc123"},
		{"localhost", "INFO dsh web: http://localhost:3080/?token=local", "local"},
		{"IPv6 回环", "dsh web:http://[::1]:3080/?token=ipv6", "ipv6"},
		{"带 ANSI 颜色", "\x1b[32mdsh web: http://127.0.0.1:3080/\x1b[0m", ""},
		{"行尾附加文本", "dsh web: http://127.0.0.1:3080/ trailing text", ""},
	}
	for _, tc := range valid {
		plain := stripANSI(tc.line)
		parsed := parseBootstrapURI(plain, port)
		if parsed == nil {
			t.Fatalf("%s: 应解析出 bootstrap URI: %q", tc.name, plain)
		}
		if parsed.Scheme != "http" || parsed.Port() != "3080" || parsed.Hostname() != hostnameOf(tc.line) {
			t.Fatalf("%s: 解析结果不符: %s", tc.name, parsed.String())
		}
		if got := parsed.Query().Get("token"); got != tc.token {
			t.Fatalf("%s: token = %q，期望 %q", tc.name, got, tc.token)
		}
	}

	invalid := []struct {
		name string
		line string
	}{
		{"无标记", "no marker here"},
		{"空 token", "dsh web:"},
		{"全空白", "dsh web:    "},
		{"https", "dsh web: https://127.0.0.1:3080/?token=x"},
		{"非回环域名", "dsh web: http://example.com:3080/?token=x"},
		{"0.0.0.0", "dsh web: http://0.0.0.0:3080/?token=x"},
		{"端口不匹配", "dsh web: http://127.0.0.1:3081/?token=x"},
		{"无端口", "dsh web: http://127.0.0.1/?token=x"},
		{"带 userinfo", "dsh web: http://user:pass@127.0.0.1:3080/?token=x"},
		{"错误 scheme", "dsh web: ftp://127.0.0.1:3080/?token=x"},
		{"相对 URL", "dsh web: //127.0.0.1:3080/?token=x"},
		{"其他 IPv6", "dsh web: http://[::2]:3080/?token=x"},
		{"域名后缀伪装", "dsh web: http://127.0.0.1:3080.evil.com/?token=x"},
	}
	for _, tc := range invalid {
		if parsed := parseBootstrapURI(stripANSI(tc.line), port); parsed != nil {
			t.Fatalf("%s: 不应接受 %q（得到 %s）", tc.name, tc.line, parsed.String())
		}
	}
}

func hostnameOf(line string) string {
	switch {
	case strings.Contains(line, "[::1]"):
		return "::1"
	case strings.Contains(line, "localhost"):
		return "localhost"
	default:
		return "127.0.0.1"
	}
}

func TestRedactor(t *testing.T) {
	redactor := newRedactor([]string{
		"PATH=/usr/bin",
		"DSH_TOKEN=env-secret-value",
		"NPM_CONFIG_REGISTRY=https://registry.npmjs.org",
	})
	cases := []struct {
		name           string
		in             string
		mustContain    []string
		mustNotContain []string
	}{
		{"token key=value", "token=abc123", []string{"token=***"}, []string{"abc123"}},
		{"password 冒号", "password: hunter2", []string{"***"}, []string{"hunter2"}},
		{"Authorization", "Authorization: Bearer abcdef", []string{"Authorization: ***"}, []string{"abcdef"}},
		{"URL query token", "url http://127.0.0.1:3080/?token=query-secret&x=1", []string{"token=***", "x=1"}, []string{"query-secret"}},
		{"api_key", "api_key=XYZ123", []string{"***"}, []string{"XYZ123"}},
		{"环境变量值", "leaked env-secret-value here", []string{"***"}, []string{"env-secret-value"}},
		{"sk key", "sk-abcdefghijklmnop", []string{"***"}, []string{"sk-abcdefghijklmnop"}},
	}
	for _, tc := range cases {
		got := redactor.redact(tc.in)
		for _, want := range tc.mustContain {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: 脱敏结果 %q 应包含 %q", tc.name, got, want)
			}
		}
		for _, secret := range tc.mustNotContain {
			if strings.Contains(got, secret) {
				t.Fatalf("%s: 脱敏结果泄漏 %q: %q", tc.name, secret, got)
			}
		}
		if twice := redactor.redact(got); twice != got {
			t.Fatalf("%s: 脱敏应幂等: %q vs %q", tc.name, got, twice)
		}
	}

	plain := "普通日志行，含 registry=https://registry.npmjs.org 与 PATH"
	if got := redactor.redact(plain); got != plain {
		t.Fatalf("普通文本不应被脱敏: %q", got)
	}
}

func TestRedactorAddsBootstrapToken(t *testing.T) {
	redactor := newRedactor(nil)
	parsed, err := url.Parse("http://127.0.0.1:3080/?token=uri-secret-value")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	redactor.addURLSecrets(parsed)
	got := redactor.redact("token=uri-secret-value 出现在别处")
	if strings.Contains(got, "uri-secret-value") {
		t.Fatalf("bootstrap token 值应被脱敏: %q", got)
	}
}

func TestReadyFromStatus(t *testing.T) {
	cases := []struct {
		status       int
		hasBootstrap bool
		want         bool
	}{
		{200, false, true},
		{204, false, true},
		{301, false, true},
		{302, false, true},
		{304, false, true},
		{401, false, false},
		{401, true, true},
		{403, true, true},
		{403, false, false},
		{404, true, false},
		{404, false, false},
		{500, true, false},
		{0, false, false},
	}
	for _, tc := range cases {
		if got := readyFromStatus(tc.status, tc.hasBootstrap); got != tc.want {
			t.Fatalf("readyFromStatus(%d, %v) = %v，期望 %v", tc.status, tc.hasBootstrap, got, tc.want)
		}
	}
}

func TestStatusMessages(t *testing.T) {
	cases := map[string]string{
		StatusStopped:  "未启动",
		StatusStarting: "启动中",
		StatusRunning:  "运行中",
		StatusStopping: "停止中",
		StatusNotReady: "进程运行中，服务未就绪",
		StatusPortBusy: "端口被占用",
		StatusExited:   "已停止",
	}
	for code, want := range cases {
		if got := statusMessage(code); got != want {
			t.Fatalf("statusMessage(%s) = %q，期望 %q", code, got, want)
		}
	}
	if got := statusMessage("UNKNOWN"); got != "未知状态" {
		t.Fatalf("未知状态 Message = %q", got)
	}
}

func TestHandleAccessors(t *testing.T) {
	startedAt := time.Unix(1750000000, 0)
	handle := &Handle{instanceID: "i-000000000000", port: 3080, startedAt: startedAt, code: StatusStarting, desc: map[int]descendant{}}
	if handle.InstanceID() != "i-000000000000" || handle.Port() != 3080 {
		t.Fatalf("InstanceID/Port accessor 不符: %q %d", handle.InstanceID(), handle.Port())
	}
	status := handle.Status()
	if status.Code != StatusStarting || status.Message != "启动中" || status.PID != 0 || !status.StartedAt.Equal(startedAt) {
		t.Fatalf("初始 Status 不符: %+v", status)
	}
	if handle.Ready() {
		t.Fatal("STARTING 不应 Ready")
	}
	if handle.BootstrapURL() != nil {
		t.Fatal("未就绪时 BootstrapURL 应为 nil")
	}
	if handle.isActive() {
		t.Fatal("无 cmd 时不应 active")
	}

	parsed, err := url.Parse("http://127.0.0.1:3080/?token=running-token")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	handle.mu.Lock()
	handle.code = StatusRunning
	handle.bootstrap = parsed
	handle.mu.Unlock()
	if !handle.Ready() {
		t.Fatal("RUNNING 应 Ready")
	}
	bootstrap := handle.BootstrapURL()
	if bootstrap == nil {
		t.Fatal("运行中 BootstrapURL 不应为 nil")
	}
	if bootstrap.Query().Get("token") != "running-token" || bootstrap.Port() != "3080" {
		t.Fatalf("BootstrapURL 不符: %s", bootstrap.String())
	}
	bootstrap.RawQuery = "token=tampered"
	if again := handle.BootstrapURL(); again.Query().Get("token") != "running-token" {
		t.Fatal("BootstrapURL 应返回拷贝，不能被调用方改写")
	}

	handle.mu.Lock()
	handle.exited = true
	handle.bootstrap = nil
	handle.code = StatusExited
	handle.mu.Unlock()
	if handle.Ready() {
		t.Fatal("EXITED 不应 Ready")
	}
	if handle.BootstrapURL() != nil {
		t.Fatal("退出后 BootstrapURL 应为 nil")
	}
	if status := handle.Status(); status.Code != StatusExited || status.Message != "已停止" {
		t.Fatalf("退出后 Status 不符: %+v", status)
	}
}

func TestManagerEmpty(t *testing.T) {
	manager := NewManager(nil)
	if got := manager.Running(); len(got) != 0 {
		t.Fatalf("空 Manager Running = %d", len(got))
	}
	if _, ok := manager.Get("i-000000000000"); ok {
		t.Fatal("空 Manager Get 应返回 false")
	}
	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatalf("空 Manager StopAll 应为 nil，实际 %v", err)
	}
	if manager.activeHandle("i-000000000000") != nil {
		t.Fatal("空 Manager activeHandle 应为 nil")
	}
}

func TestValidateLaunchSpec(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	base := func() LaunchSpec {
		root := t.TempDir()
		return LaunchSpec{
			InstanceID: "i-000000000000",
			Command:    []string{executable},
			Port:       3080,
			LogPath:    filepath.Join(root, "logs", "instance.log"),
			Dir:        filepath.Join(root, "workspace"),
		}
	}
	invalid := []struct {
		name   string
		mutate func(*LaunchSpec)
	}{
		{"InstanceID 空", func(s *LaunchSpec) { s.InstanceID = "  " }},
		{"Command 空", func(s *LaunchSpec) { s.Command = nil }},
		{"Command[0] 相对路径", func(s *LaunchSpec) { s.Command = []string{"node"} }},
		{"端口 0", func(s *LaunchSpec) { s.Port = 0 }},
		{"端口过大", func(s *LaunchSpec) { s.Port = 70000 }},
		{"LogPath 空", func(s *LaunchSpec) { s.LogPath = " " }},
	}
	for _, tc := range invalid {
		spec := base()
		tc.mutate(&spec)
		if err := validateLaunchSpec(&spec); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("%s: 应返回 ErrInvalidSpec，实际 %v", tc.name, err)
		}
	}

	spec := base()
	if err := validateLaunchSpec(&spec); err != nil {
		t.Fatalf("合法 spec 被拒绝: %v", err)
	}
	if spec.ReadyTimeout != DefaultReadyTimeout || spec.ReadyInterval != DefaultReadyInterval {
		t.Fatalf("默认就绪参数未填充: %+v", spec)
	}
	if info, err := os.Stat(spec.Dir); err != nil || !info.IsDir() {
		t.Fatalf("工作目录未被创建: %v", err)
	}
}

func TestPortFree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("无法监听回环端口（环境受限）: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if portFree(port) {
		t.Fatalf("端口 %d 正在监听，portFree 应返回 false", port)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("关闭监听失败: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !portFree(port) {
		if time.Now().After(deadline) {
			t.Fatalf("端口 %d 关闭后仍被视为占用", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if portFree(0) || portFree(70000) {
		t.Fatal("非法端口不应被视为空闲")
	}
}
