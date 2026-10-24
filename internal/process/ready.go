// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// readyFromStatus 判定一次 HTTP 探测是否代表服务就绪（docs/DSH_INTEGRATION.md §5.4）：
//   - 2xx/3xx → 就绪；
//   - 401/403 → 仅在已捕获 bootstrap URI 时就绪（新版 DSH 先 401，带 token 后才可访问）；
//   - 404 及其他（含 5xx）→ 继续等待。
func readyFromStatus(statusCode int, hasBootstrap bool) bool {
	switch {
	case statusCode >= 200 && statusCode < 400:
		return true
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return hasBootstrap
	default:
		return false
	}
}

// newProbeClient 创建就绪探测客户端：HTTP/1.1、显式禁用代理（防止环境代理劫持回环）、
// connectTimeout 1s、请求超时 2s、不跟随重定向。
func newProbeClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil, // 显式不走代理（不读取 HTTP(S)_PROXY）
		DialContext: (&net.Dialer{
			Timeout:   1 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   false, // 只用 HTTP/1.1
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// awaitReady 轮询 http://127.0.0.1:<port>/ 直到就绪、进程退出、超时或 ctx 取消。
func (h *Handle) awaitReady(ctx context.Context) error {
	deadline := time.Now().Add(h.readyTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if h.exitedNow() {
			return ErrExitedBeforeReady
		}
		if !time.Now().Before(deadline) {
			return ErrNotReady
		}
		if h.probeOnce() {
			return nil
		}
		timer := time.NewTimer(h.readyInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-h.done:
			timer.Stop()
			return ErrExitedBeforeReady
		case <-timer.C:
		}
	}
}

func (h *Handle) probeOnce() bool {
	probeURL := "http://127.0.0.1:" + strconv.Itoa(h.port) + "/"
	request, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "pcl-dsh-go-readiness")
	response, err := h.probeClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	return readyFromStatus(response.StatusCode, h.hasBootstrap())
}

// portFree 通过“能否连接 127.0.0.1:port”判断端口是否空闲。能连接视为被占用。
// 与参考实现一致：不做独占 bind 预检，避免 TIME_WAIT 误判。
func portFree(port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 150*time.Millisecond)
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}
