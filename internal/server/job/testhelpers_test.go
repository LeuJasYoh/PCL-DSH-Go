// SPDX-License-Identifier: MIT

package job

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe bytes.Buffer used to capture slog output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitLogContains polls the captured log output until every part is present.
func waitLogContains(t *testing.T, logs *syncBuffer, parts ...string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		output := logs.String()
		all := true
		for _, part := range parts {
			if !strings.Contains(output, part) {
				all = false
				break
			}
		}
		if all {
			return output
		}
		time.Sleep(time.Millisecond)
	}
	output := logs.String()
	t.Fatalf("log output does not contain %v: %s", parts, output)
	return output
}

// newManager builds a manager that is shut down when the test ends and
// returns the captured log output.
func newManager(t *testing.T, opts Options) (*Manager, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(logger, opts)
	t.Cleanup(m.Shutdown)
	return m, logs
}

// newTestServer registers the job routes on a fresh mux and serves them.
// wrap may decorate the handler (e.g. to inject X-Trace-Id).
func newTestServer(t *testing.T, m *Manager, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	m.Register(mux)
	var handler http.Handler = mux
	if wrap != nil {
		handler = wrap(handler)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		m.Shutdown()
		srv.Close()
	})
	return srv
}

// waitTerminal polls until the job leaves the RUNNING state.
func waitTerminal(t *testing.T, m *Manager, id string) View {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := m.Get(id); ok && v.State != StateRunning {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	v, found := m.Get(id)
	t.Fatalf("job %s did not reach a terminal state (last view %+v, found=%v)", id, v, found)
	return View{}
}

// waitState polls until the job reaches want.
func waitState(t *testing.T, m *Manager, id string, want State) View {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := m.Get(id); ok && v.State == want {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	v, _ := m.Get(id)
	t.Fatalf("job %s did not reach state %s (last view %+v)", id, want, v)
	return View{}
}

// startBlockingJob starts a job whose task blocks until its context is
// cancelled and then returns the context error.
func startBlockingJob(t *testing.T, m *Manager, kind Kind) *View {
	t.Helper()
	started := make(chan struct{})
	view, err := m.Start(kind, 0, func(ctx context.Context, _ *Job) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatalf("job %s did not start executing", view.ID)
	}
	return view
}

// subscriberCount returns the number of live SSE subscribers of the active job.
func subscriberCount(m *Manager) int {
	m.mu.Lock()
	active := m.active
	m.mu.Unlock()
	if active == nil {
		return 0
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	return len(active.subs)
}

// parsedEvent is one decoded SSE event.
type parsedEvent struct {
	ID   int64
	Name string
	Data map[string]any
}

// sseHTTPClient is the client used for streaming requests. The timeout is a
// safety net for tests: every stream in these tests ends quickly.
func sseHTTPClient() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

// openSSE issues GET /api/jobs/{id}/events (optionally with Last-Event-ID).
func openSSE(t *testing.T, srv *httptest.Server, jobID string, lastEventID int64) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/jobs/"+jobID+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(lastEventID, 10))
	}
	resp, err := sseHTTPClient().Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("SSE status = %d, body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("SSE Content-Type = %q", ct)
	}
	return resp
}

// readAllSSE reads an SSE stream until EOF and returns the events and comment
// lines (heartbeats).
func readAllSSE(t *testing.T, resp *http.Response) ([]parsedEvent, []string) {
	t.Helper()
	defer resp.Body.Close()
	events, comments, err := parseSSE(resp.Body)
	if err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	return events, comments
}

// parseSSE decodes id/event/data blocks plus ":" comment lines.
func parseSSE(r io.Reader) ([]parsedEvent, []string, error) {
	reader := bufio.NewReader(r)
	var (
		events   []parsedEvent
		comments []string
		current  parsedEvent
		hasEvent bool
	)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if hasEvent {
					events = append(events, current)
				}
				return events, comments, nil
			}
			return events, comments, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if hasEvent {
				events = append(events, current)
				current = parsedEvent{}
				hasEvent = false
			}
		case strings.HasPrefix(line, ":"):
			comments = append(comments, strings.TrimSpace(strings.TrimPrefix(line, ":")))
		case strings.HasPrefix(line, "id: "):
			seq, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid event id line %q: %w", line, err)
			}
			current.ID = seq
			hasEvent = true
		case strings.HasPrefix(line, "event: "):
			current.Name = strings.TrimPrefix(line, "event: ")
			hasEvent = true
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.Data); err != nil {
				return nil, nil, fmt.Errorf("invalid event data %q: %w", line, err)
			}
			hasEvent = true
		}
	}
}

// readUntilCommentFrom reads lines until the given comment appears
// (e.g. ": ping"), leaving the reader positioned after it.
func readUntilCommentFrom(reader *bufio.Reader, want string) (bool, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}
		if strings.TrimRight(line, "\r\n") == want {
			return true, nil
		}
	}
}

// readUntilComment is readUntilCommentFrom over a fresh response reader.
func readUntilComment(resp *http.Response, want string) (bool, error) {
	return readUntilCommentFrom(bufio.NewReader(resp.Body), want)
}

// assertEventNames checks the SSE event name sequence.
func assertEventNames(t *testing.T, events []parsedEvent, want ...string) {
	t.Helper()
	got := make([]string, len(events))
	for i, ev := range events {
		got[i] = ev.Name
	}
	if len(got) != len(want) {
		t.Fatalf("event names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event names = %v, want %v", got, want)
		}
	}
}

// assertSequentialIDs checks that event ids are strictly increasing and
// contiguous starting at wantFirst.
func assertSequentialIDs(t *testing.T, events []parsedEvent, wantFirst int64) {
	t.Helper()
	for i, ev := range events {
		want := wantFirst + int64(i)
		if ev.ID != want {
			t.Fatalf("event[%d].id = %d, want %d (events: %v)", i, ev.ID, want, eventIDs(events))
		}
	}
}

func eventIDs(events []parsedEvent) []int64 {
	ids := make([]int64, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	return ids
}

// mustString reads a string field from decoded SSE data.
func mustString(t *testing.T, data map[string]any, key string) string {
	t.Helper()
	value, ok := data[key]
	if !ok {
		t.Fatalf("event data is missing %q: %v", key, data)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("event data %q = %T, want string (%v)", key, value, data)
	}
	return text
}

// mustFloat reads a number field from decoded SSE data.
func mustFloat(t *testing.T, data map[string]any, key string) float64 {
	t.Helper()
	value, ok := data[key]
	if !ok {
		t.Fatalf("event data is missing %q: %v", key, data)
	}
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("event data %q = %T, want number (%v)", key, value, data)
	}
	return number
}
