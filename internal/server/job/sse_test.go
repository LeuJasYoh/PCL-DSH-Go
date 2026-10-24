// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLastEventIDParsing(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   int64
	}{
		{name: "absent", header: "", want: 0},
		{name: "valid", header: "17", want: 17},
		{name: "spaces", header: " 42 ", want: 42},
		{name: "zero", header: "0", want: 0},
		{name: "negative", header: "-3", want: 0},
		{name: "garbage", header: "not-a-number", want: 0},
		{name: "overflow", header: "999999999999999999999999", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/jobs/j-000000000000/events", nil)
			if tc.header != "" {
				req.Header.Set("Last-Event-ID", tc.header)
			}
			if got := lastEventID(req); got != tc.want {
				t.Fatalf("lastEventID(%q) = %d, want %d", tc.header, got, tc.want)
			}
		})
	}
}

func TestSSEStateLogResultOrder(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	view, err := m.Start(KindLaunchInstance, 0, func(_ context.Context, j *Job) (any, error) {
		close(started)
		<-release
		j.Phase("启动进程", "正在启动")
		j.Logf("first line")
		j.Phase("等待就绪", "等待就绪")
		j.Logf("second line")
		return map[string]any{"instanceId": "i-abc", "webUrl": "/workbench/i-abc/"}, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	resp := openSSE(t, srv, view.ID, 0)
	close(release)
	events, comments := readAllSSE(t, resp)
	if len(comments) != 0 {
		t.Fatalf("unexpected heartbeat comments: %v", comments)
	}
	assertEventNames(t, events,
		eventState, eventState, eventLog, eventState, eventLog, eventState, eventResult)
	assertSequentialIDs(t, events, 1)

	first := events[0].Data
	if got := mustString(t, first, "state"); got != string(StateRunning) {
		t.Fatalf("first state = %q", got)
	}
	if got := mustString(t, first, "jobId"); got != view.ID {
		t.Fatalf("jobId = %q, want %q", got, view.ID)
	}
	if got := mustString(t, first, "phase"); got != "准备中" {
		t.Fatalf("initial phase = %q", got)
	}
	if got := mustString(t, first, "message"); got == "" {
		t.Fatalf("initial message is empty")
	}
	if got := mustFloat(t, first, "seq"); got != 1 {
		t.Fatalf("first seq = %v", got)
	}
	if got := mustFloat(t, first, "ts"); got <= 0 {
		t.Fatalf("first ts = %v", got)
	}

	if got := mustString(t, events[1].Data, "phase"); got != "启动进程" {
		t.Fatalf("phase event = %q", got)
	}
	if got := mustString(t, events[1].Data, "message"); got != "正在启动" {
		t.Fatalf("phase message = %q", got)
	}

	logEvent := events[2].Data
	if got := mustString(t, logEvent, "level"); got != "info" {
		t.Fatalf("log level = %q", got)
	}
	if got := mustString(t, logEvent, "line"); got != "first line" {
		t.Fatalf("log line = %q", got)
	}
	if got := mustFloat(t, logEvent, "seq"); got != 3 {
		t.Fatalf("log seq = %v", got)
	}
	if got := mustFloat(t, logEvent, "ts"); got <= 0 {
		t.Fatalf("log ts = %v", got)
	}
	if got := mustString(t, logEvent, "jobId"); got != view.ID {
		t.Fatalf("log jobId = %q", got)
	}

	finalState := events[5].Data
	if got := mustString(t, finalState, "state"); got != string(StateSucceeded) {
		t.Fatalf("final state = %q", got)
	}
	if got := mustString(t, finalState, "message"); got != "任务完成。" {
		t.Fatalf("final message = %q", got)
	}

	resultEvent := events[6].Data
	if got := mustString(t, resultEvent, "state"); got != string(StateSucceeded) {
		t.Fatalf("result state = %q", got)
	}
	if _, ok := resultEvent["seq"]; ok {
		t.Fatalf("result event must not carry seq: %v", resultEvent)
	}
	result, ok := resultEvent["result"].(map[string]any)
	if !ok {
		t.Fatalf("result payload = %T", resultEvent["result"])
	}
	if result["instanceId"] != "i-abc" {
		t.Fatalf("result = %v", result)
	}

	finalView := waitState(t, m, view.ID, StateSucceeded)
	if finalView.LastSeq != int64(len(events)) {
		t.Fatalf("LastSeq = %d, SSE events = %d", finalView.LastSeq, len(events))
	}
}

func TestSSELastEventIDReplay(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	view, err := m.Start(KindProbeRuntime, 0, func(_ context.Context, j *Job) (any, error) {
		j.Logf("one")
		j.Logf("two")
		j.Logf("three")
		return "done", nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitState(t, m, view.ID, StateSucceeded)
	waitIdle(t, m)
	if final.LastSeq != 6 {
		t.Fatalf("LastSeq = %d, want 6", final.LastSeq)
	}

	full, _ := readAllSSE(t, openSSE(t, srv, view.ID, 0))
	if len(full) != 6 {
		t.Fatalf("full replay = %d events, want 6", len(full))
	}
	assertSequentialIDs(t, full, 1)

	cut := full[2].ID
	replay, _ := readAllSSE(t, openSSE(t, srv, view.ID, cut))
	if len(replay) != len(full)-3 {
		t.Fatalf("replay after %d = %d events, want %d", cut, len(replay), len(full)-3)
	}
	assertSequentialIDs(t, replay, cut+1)
	for i := range replay {
		if replay[i].Name != full[i+3].Name {
			t.Fatalf("replay[%d] name = %q, want %q", i, replay[i].Name, full[i+3].Name)
		}
	}

	beyond, _ := readAllSSE(t, openSSE(t, srv, view.ID, 1000))
	if len(beyond) != 0 {
		t.Fatalf("replay beyond last seq = %d events, want 0", len(beyond))
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/jobs/"+view.ID+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Last-Event-ID", "not-a-number")
	resp, err := sseHTTPClient().Do(req)
	if err != nil {
		t.Fatalf("invalid Last-Event-ID request: %v", err)
	}
	invalid, _ := readAllSSE(t, resp)
	if len(invalid) != len(full) {
		t.Fatalf("invalid Last-Event-ID replay = %d events, want full %d", len(invalid), len(full))
	}
}

func TestSSEHeartbeat(t *testing.T) {
	m, _ := newManager(t, Options{Heartbeat: 20 * time.Millisecond})
	srv := newTestServer(t, m, nil)
	view := startBlockingJob(t, m, KindInstallRuntime)
	resp := openSSE(t, srv, view.ID, 0)
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	found, err := readUntilCommentFrom(reader, ": ping")
	if err != nil {
		t.Fatalf("waiting for heartbeat: %v", err)
	}
	if !found {
		t.Fatal("heartbeat comment not found")
	}
	if _, err := m.Cancel(view.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	remaining, comments, err := parseSSE(reader)
	if err != nil {
		t.Fatalf("read SSE after heartbeat: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("events after heartbeat = %d, want 1: %+v", len(remaining), remaining)
	}
	last := remaining[0]
	if last.Name != eventState || last.ID != 2 {
		t.Fatalf("terminal event = %+v, want state seq 2", last)
	}
	if got := mustString(t, last.Data, "state"); got != string(StateCancelled) {
		t.Fatalf("terminal state = %q, want CANCELLED", got)
	}
	if len(comments) != 0 {
		t.Fatalf("unexpected extra comments: %v", comments)
	}
}

func TestSSEDisconnectCleansUpSubscribers(t *testing.T) {
	m, _ := newManager(t, Options{Heartbeat: time.Hour})
	srv := newTestServer(t, m, nil)
	view := startBlockingJob(t, m, KindInstallRuntime)
	resp := openSSE(t, srv, view.ID, 0)
	if got := subscriberCount(m); got != 1 {
		t.Fatalf("subscribers = %d, want 1", got)
	}
	resp.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if subscriberCount(m) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := subscriberCount(m); got != 0 {
		t.Fatalf("subscribers after disconnect = %d, want 0 (handler leak)", got)
	}
	if _, err := m.Cancel(view.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitState(t, m, view.ID, StateCancelled)
}

func TestSSECancelEndsWithCancelledState(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	view := startBlockingJob(t, m, KindInstallRuntime)
	resp := openSSE(t, srv, view.ID, 0)
	if _, err := m.Cancel(view.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	events, _ := readAllSSE(t, resp)
	assertEventNames(t, events, eventState, eventState)
	assertSequentialIDs(t, events, 1)
	if got := mustString(t, events[1].Data, "state"); got != string(StateCancelled) {
		t.Fatalf("terminal state = %q", got)
	}
	if got := mustString(t, events[1].Data, "message"); got != "任务已取消。" {
		t.Fatalf("terminal message = %q", got)
	}
	for _, ev := range events {
		if ev.Name == eventResult || ev.Name == eventError {
			t.Fatalf("cancelled job must not emit %q", ev.Name)
		}
	}
}

func TestSSEUnknownJob404(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/api/jobs/j-000000000000/events")
	assertStrictErrorBody(t, resp, body, http.StatusNotFound, "NOT_FOUND")
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestSSEHeaders(t *testing.T) {
	m, _ := newManager(t, Options{})
	srv := newTestServer(t, m, nil)
	view, err := m.Start(KindProbeRuntime, 0, func(_ context.Context, j *Job) (any, error) {
		j.Logf("hi")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitState(t, m, view.ID, StateSucceeded)
	waitIdle(t, m)
	resp := openSSE(t, srv, view.ID, 0)
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache, no-transform" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if xab := resp.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Fatalf("X-Accel-Buffering = %q", xab)
	}
	events, _ := readAllSSE(t, resp)
	assertEventNames(t, events, eventState, eventLog, eventState, eventResult)
	assertSequentialIDs(t, events, 1)
}
