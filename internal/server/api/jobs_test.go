// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

// sseEvent is one parsed Server-Sent Event.
type sseEvent struct {
	ID    string
	Name  string
	Data  string
	Extra string
}

// parseSSE parses an SSE response body (id/event/data records separated by a
// blank line).
func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	records := strings.Split(body, "\n\n")
	events := make([]sseEvent, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		event := sseEvent{}
		for _, line := range strings.Split(record, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				event.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				event.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				event.Data = strings.TrimPrefix(line, "data: ")
			default:
				event.Extra += line
			}
		}
		events = append(events, event)
	}
	return events
}

// decodeEventData unmarshals one event data payload.
func decodeEventData(t *testing.T, event sseEvent) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		t.Fatalf("event %q data %q: %v", event.Name, event.Data, err)
	}
	return payload
}

func TestLaunchSSEEvents(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "SSE 实例", freePort(t))
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.launchHook = func(ctx context.Context, id string, logf func(string)) (app.LaunchResult, error) {
		logf("正在启动实例进程…")
		logf("服务已就绪")
		return app.LaunchResult{InstanceID: id, Status: "运行中"}, nil
	}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if view.ID == "" || !strings.HasPrefix(view.ID, "j-") {
		t.Fatalf("job id = %q, want j- + 12 hex", view.ID)
	}

	// The job result is reachable through GET /api/jobs/{id}.
	env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID, nil, sess.withSession())
	finished := decodeJSONResponse[job.View](t, rec, http.StatusOK)
	if finished.State != job.StateSucceeded {
		t.Fatalf("job state = %q, want SUCCEEDED", finished.State)
	}
	// Recent jobs list includes it.
	rec = env.request(t, http.MethodGet, "/api/jobs", nil, sess.withSession())
	list := decodeJSONResponse[[]job.View](t, rec, http.StatusOK)
	if len(list) == 0 || list[0].ID != view.ID {
		t.Fatalf("jobs list = %+v, want the new job first", list)
	}

	// Read the SSE stream after completion: the ring buffer replays it.
	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID+"/events", nil, sess.withSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("events Content-Type = %q, want text/event-stream", contentType)
	}
	if cacheControl := rec.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-cache") {
		t.Fatalf("events Cache-Control = %q, want no-cache", cacheControl)
	}
	events := parseSSE(t, rec.Body.String())
	if len(events) < 4 {
		t.Fatalf("events = %+v, want at least state/log/result", events)
	}
	if events[0].Name != "state" {
		t.Fatalf("first event = %+v, want state", events[0])
	}
	firstData := decodeEventData(t, events[0])
	if firstData["state"] != "RUNNING" || firstData["jobId"] != view.ID {
		t.Fatalf("first state data = %v", firstData)
	}
	if _, ok := firstData["seq"].(float64); !ok {
		t.Fatalf("state event missing numeric seq: %v", firstData)
	}
	if _, ok := firstData["ts"].(float64); !ok {
		t.Fatalf("state event missing numeric ts: %v", firstData)
	}
	last := events[len(events)-1]
	if last.Name != "result" {
		t.Fatalf("last event = %+v, want result", last)
	}
	lastData := decodeEventData(t, last)
	result, ok := lastData["result"].(map[string]any)
	if !ok || result["instanceId"] != instance.ID {
		t.Fatalf("result event data = %v", lastData)
	}
	// Sequence numbers strictly increase and match the id line.
	lastSeq := 0
	sawLog := false
	for _, event := range events {
		data := decodeEventData(t, event)
		seq, ok := data["seq"].(float64)
		if !ok {
			if event.Name == "result" {
				continue
			}
			t.Fatalf("event %q missing seq: %v", event.Name, data)
		}
		if int(seq) <= lastSeq {
			t.Fatalf("event seq %v does not increase after %d", seq, lastSeq)
		}
		if event.ID != "" && event.ID != strings.TrimSpace(event.ID) {
			t.Fatalf("event id %q contains whitespace", event.ID)
		}
		lastSeq = int(seq)
		if event.Name == "log" {
			sawLog = true
			if data["level"] != "info" {
				t.Fatalf("log event level = %v, want info", data["level"])
			}
			if _, ok := data["line"].(string); !ok {
				t.Fatalf("log event missing line: %v", data)
			}
		}
	}
	if !sawLog {
		t.Fatalf("no log event in %+v", events)
	}

	// Last-Event-ID replays only the events after it.
	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID+"/events", nil, sess.withSession(),
		func(r *http.Request) { r.Header.Set("Last-Event-ID", "2") })
	replayed := parseSSE(t, rec.Body.String())
	if len(replayed) == 0 {
		t.Fatal("Last-Event-ID replay returned no events")
	}
	if replayed[0].ID != "3" {
		t.Fatalf("replay first id = %q, want 3", replayed[0].ID)
	}
}

func TestJobCancel(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "取消实例", freePort(t))
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.launchHook = func(ctx context.Context, id string, logf func(string)) (app.LaunchResult, error) {
		logf("等待取消…")
		<-ctx.Done()
		return app.LaunchResult{}, ctx.Err()
	}
	env.app.mu.Unlock()

	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)

	rec = env.request(t, http.MethodPost, "/api/jobs/"+view.ID+"/cancel", nil, sess.withSession())
	canceled := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	if canceled.ID != view.ID {
		t.Fatalf("cancel view = %+v", canceled)
	}
	finished := env.waitForJobState(t, sess, view.ID, job.StateCancelled)
	if finished.State != job.StateCancelled {
		t.Fatalf("job state = %q, want CANCELLED", finished.State)
	}

	// Cancelling a finished job → 409 CONFLICT_JOB_FINISHED.
	rec = env.request(t, http.MethodPost, "/api/jobs/"+view.ID+"/cancel", nil, sess.withSession())
	decodeError(t, rec, http.StatusConflict, apierr.CodeConflictJobFinished)

	// Unknown job → 404 NOT_FOUND.
	rec = env.request(t, http.MethodPost, "/api/jobs/j-000000000000/cancel", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
	rec = env.request(t, http.MethodGet, "/api/jobs/j-000000000000", nil, sess.withSession())
	decodeError(t, rec, http.StatusNotFound, apierr.CodeNotFound)
}

func TestJobSSEClientDisconnectDoesNotBreakLogs(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "断线实例", freePort(t))
	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.launchHook = func(ctx context.Context, id string, logf func(string)) (app.LaunchResult, error) {
		logf("启动中")
		return app.LaunchResult{InstanceID: id, Status: "运行中"}, nil
	}
	env.app.mu.Unlock()
	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, view.ID, job.StateSucceeded)

	// A canceled SSE request must not panic or corrupt the event stream.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/jobs/"+view.ID+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	sess.withSession()(request)
	env.api.Handler().ServeHTTP(newDiscardRecorder(), request)

	rec = env.request(t, http.MethodGet, "/api/jobs/"+view.ID, nil, sess.withSession())
	decodeJSONResponse[job.View](t, rec, http.StatusOK)
}

// newDiscardRecorder returns a recorder that discards the body.
func newDiscardRecorder() *discardRecorder {
	return &discardRecorder{header: http.Header{}, status: http.StatusOK}
}

type discardRecorder struct {
	header http.Header
	status int
}

func (r *discardRecorder) Header() http.Header         { return r.header }
func (r *discardRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (r *discardRecorder) WriteHeader(status int)      { r.status = status }
func (r *discardRecorder) Flush()                      {}
