// SPDX-License-Identifier: MIT

package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

var jobIDPattern = regexp.MustCompile(`^j-[0-9a-f]{12}$`)

// waitIdle waits until no job holds the single active slot (finish has already
// moved the finished job into the history by then).
func waitIdle(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !m.Busy() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job manager did not become idle")
}

func TestDefaultOptions(t *testing.T) {
	m := NewManager(nil, Options{})
	defer m.Shutdown()
	if m.opts.Heartbeat != defaultHeartbeat {
		t.Fatalf("Heartbeat = %v, want %v", m.opts.Heartbeat, defaultHeartbeat)
	}
	if m.opts.MaxEvents != defaultMaxEvents {
		t.Fatalf("MaxEvents = %d, want %d", m.opts.MaxEvents, defaultMaxEvents)
	}
	if m.opts.MaxHistory != defaultMaxHistory {
		t.Fatalf("MaxHistory = %d, want %d", m.opts.MaxHistory, defaultMaxHistory)
	}
	if m.opts.MaxDuration != defaultMaxDuration {
		t.Fatalf("MaxDuration = %v, want %v", m.opts.MaxDuration, defaultMaxDuration)
	}
	if m.opts.Now == nil {
		t.Fatal("Now is nil")
	}
}

func TestStartReturnsRunningView(t *testing.T) {
	m, _ := newManager(t, Options{})
	release := make(chan struct{})
	view, err := m.Start(KindLaunchInstance, 0, func(_ context.Context, _ *Job) (any, error) {
		<-release
		return map[string]any{"instanceId": "i-abc"}, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !jobIDPattern.MatchString(view.ID) {
		t.Fatalf("job id = %q, want j- + 12 hex", view.ID)
	}
	if view.Type != KindLaunchInstance {
		t.Fatalf("Type = %q", view.Type)
	}
	if view.State != StateRunning {
		t.Fatalf("State = %q, want RUNNING", view.State)
	}
	if view.Phase == "" || view.Message == "" {
		t.Fatalf("initial phase/message empty: %+v", view)
	}
	if view.CreatedAt <= 0 || view.StartedAt == nil || *view.StartedAt <= 0 {
		t.Fatalf("timestamps = %+v", view)
	}
	if view.FinishedAt != nil {
		t.Fatalf("FinishedAt = %v, want nil", *view.FinishedAt)
	}
	if view.Result != nil || view.Error != nil {
		t.Fatalf("result/error should be nil while running: %+v", view)
	}
	if view.LastSeq < 1 {
		t.Fatalf("LastSeq = %d, want >= 1", view.LastSeq)
	}

	if !m.Busy() {
		t.Fatal("Busy() = false while a job is running")
	}
	list := m.List()
	if len(list) != 1 || list[0].ID != view.ID {
		t.Fatalf("List() = %+v, want the running job", list)
	}
	if list[0].State != StateRunning {
		t.Fatalf("List()[0].State = %q", list[0].State)
	}

	close(release)
	final := waitState(t, m, view.ID, StateSucceeded)
	if final.FinishedAt == nil {
		t.Fatalf("FinishedAt = nil after completion")
	}
	result, ok := final.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result = %T, want map", final.Result)
	}
	if result["instanceId"] != "i-abc" {
		t.Fatalf("Result = %v", result)
	}
	if final.Error != nil {
		t.Fatalf("Error = %+v, want nil", final.Error)
	}
	waitIdle(t, m)
	if m.Busy() {
		t.Fatal("Busy() = true after completion")
	}
}

func TestStartBusyConflict(t *testing.T) {
	m, _ := newManager(t, Options{})
	running := startBlockingJob(t, m, KindProbeRuntime)

	blockingTask := func(ctx context.Context, _ *Job) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := m.Start(KindInstallRuntime, 0, blockingTask)
	if err == nil {
		t.Fatal("second Start succeeded while a job was running")
	}
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("error type = %T, want *apierr.Error", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != apierr.CodeConflictBusy {
		t.Fatalf("error = %d %s, want 409 CONFLICT_BUSY", apiErr.Status, apiErr.Code)
	}

	if _, err := m.Cancel(running.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitState(t, m, running.ID, StateCancelled)
	waitIdle(t, m)

	// A finished job frees the slot.
	next := startBlockingJob(t, m, KindProbeRuntime)
	if _, err := m.Cancel(next.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitState(t, m, next.ID, StateCancelled)
}

func TestConcurrentStart(t *testing.T) {
	m, _ := newManager(t, Options{})
	const workers = 16
	type startResult struct {
		view *View
		err  error
	}
	results := make(chan startResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, err := m.Start(KindProbeRuntime, 0, func(ctx context.Context, _ *Job) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			})
			results <- startResult{view: view, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var winner *View
	successes, busy := 0, 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.view
			continue
		}
		apiErr, ok := apierr.As(result.err)
		if !ok || apiErr.Status != http.StatusConflict || apiErr.Code != apierr.CodeConflictBusy {
			t.Fatalf("unexpected Start error: %v", result.err)
		}
		busy++
	}
	if successes != 1 {
		t.Fatalf("successful Start calls = %d, want 1", successes)
	}
	if busy != workers-1 {
		t.Fatalf("busy errors = %d, want %d", busy, workers-1)
	}
	if _, err := m.Cancel(winner.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitState(t, m, winner.ID, StateCancelled)
	waitIdle(t, m)
}

func TestCancelSemantics(t *testing.T) {
	m, _ := newManager(t, Options{})
	running := startBlockingJob(t, m, KindInstallRuntime)

	view, err := m.Cancel(running.ID)
	if err != nil {
		t.Fatalf("Cancel running job: %v", err)
	}
	if view.ID != running.ID || view.State != StateRunning {
		t.Fatalf("Cancel view = %+v, want the RUNNING job", view)
	}
	final := waitState(t, m, running.ID, StateCancelled)
	if final.Error != nil || final.Result != nil {
		t.Fatalf("cancelled job result/error = %+v", final)
	}

	_, err = m.Cancel(running.ID)
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != http.StatusConflict || apiErr.Code != apierr.CodeConflictJobFinished {
		t.Fatalf("Cancel finished job error = %v, want 409 CONFLICT_JOB_FINISHED", err)
	}

	_, err = m.Cancel("j-000000000000")
	apiErr, ok = apierr.As(err)
	if !ok || apiErr.Status != http.StatusNotFound || apiErr.Code != apierr.CodeNotFound {
		t.Fatalf("Cancel unknown job error = %v, want 404 NOT_FOUND", err)
	}

	if _, _, _, err := m.Subscribe("j-000000000000", 0); err == nil {
		t.Fatal("Subscribe(unknown) succeeded")
	} else if apiErr, ok := apierr.As(err); !ok || apiErr.Code != apierr.CodeNotFound {
		t.Fatalf("Subscribe(unknown) error = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	m, _ := newManager(t, Options{})
	const timeout = 40 * time.Millisecond
	view, err := m.Start(KindInstallRuntime, timeout, func(ctx context.Context, _ *Job) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	if final.State != StateFailed {
		t.Fatalf("State = %q, want FAILED", final.State)
	}
	if final.Error == nil || final.Error.Code != apierr.CodeTimeout {
		t.Fatalf("Error = %+v, want code TIMEOUT", final.Error)
	}
	if !strings.Contains(final.Error.Message, "超时") {
		t.Fatalf("timeout message = %q, want Chinese timeout hint", final.Error.Message)
	}
	if details, ok := final.Error.Details.(map[string]any); !ok || details == nil {
		t.Fatalf("timeout Error.Details = %#v, want JSON object", final.Error.Details)
	}
	if final.FinishedAt == nil || final.Result != nil {
		t.Fatalf("final view = %+v", final)
	}
	waitIdle(t, m)

	// The slot is free again after a timeout.
	next, err := m.Start(KindProbeRuntime, 0, func(context.Context, *Job) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Start after timeout: %v", err)
	}
	waitState(t, m, next.ID, StateSucceeded)
}

func TestDefaultTimeoutFromOptions(t *testing.T) {
	m, _ := newManager(t, Options{MaxDuration: 30 * time.Millisecond})
	view, err := m.Start(KindProbeRuntime, 0, func(ctx context.Context, _ *Job) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != apierr.CodeTimeout {
		t.Fatalf("final = %+v, want FAILED/TIMEOUT", final)
	}
}

func TestAPIErrorMapping(t *testing.T) {
	m, _ := newManager(t, Options{})
	details := map[string]any{"instanceId": "i-1"}
	taskErr := apierr.New(http.StatusConflict, apierr.CodeConflictInstanceRunning, "已有实例在运行。").
		WithDetails(details)
	view, err := m.Start(KindLaunchInstance, 0, func(context.Context, *Job) (any, error) {
		return nil, taskErr
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	if final.State != StateFailed {
		t.Fatalf("State = %q, want FAILED", final.State)
	}
	if final.Error == nil {
		t.Fatal("Error = nil, want *ErrorView")
	}
	if final.Error.Code != apierr.CodeConflictInstanceRunning || final.Error.Message != "已有实例在运行。" {
		t.Fatalf("Error = %+v", final.Error)
	}
	got, ok := final.Error.Details.(map[string]any)
	if !ok || got["instanceId"] != "i-1" {
		t.Fatalf("Error.Details = %#v", final.Error.Details)
	}
}

func TestUnknownErrorIsInternalAndRedacted(t *testing.T) {
	m, logs := newManager(t, Options{})
	view, err := m.Start(KindInstallRuntime, 0, func(context.Context, *Job) (any, error) {
		return nil, errors.New("boom token=supersecret123")
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	if final.State != StateFailed {
		t.Fatalf("State = %q, want FAILED", final.State)
	}
	if final.Error == nil || final.Error.Code != apierr.CodeInternal {
		t.Fatalf("Error = %+v, want INTERNAL", final.Error)
	}
	if final.Error.Message != "服务器内部错误。" {
		t.Fatalf("Error.Message = %q, want generic message", final.Error.Message)
	}
	if details, ok := final.Error.Details.(map[string]any); !ok || details == nil {
		t.Fatalf("INTERNAL Error.Details = %#v, want JSON object", final.Error.Details)
	}
	output := waitLogContains(t, logs, "boom", "token=***")
	if strings.Contains(output, "supersecret123") {
		t.Fatalf("log output leaks the secret token: %s", output)
	}
}

func TestTaskPanicBecomesInternal(t *testing.T) {
	m, logs := newManager(t, Options{})
	view, err := m.Start(KindProbeRuntime, 0, func(context.Context, *Job) (any, error) {
		panic("kaboom")
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != apierr.CodeInternal {
		t.Fatalf("final = %+v, want FAILED/INTERNAL", final)
	}
	waitLogContains(t, logs, "job task panicked", "kaboom")
	waitIdle(t, m)
	if m.Busy() {
		t.Fatal("manager still busy after a panicking task")
	}
	next, err := m.Start(KindProbeRuntime, 0, func(context.Context, *Job) (any, error) { return nil, nil })
	if err != nil {
		t.Fatalf("Start after panic: %v", err)
	}
	waitState(t, m, next.ID, StateSucceeded)
}

func TestTimeoutEndsInTimeoutErrorEvent(t *testing.T) {
	m, _ := newManager(t, Options{})
	view, err := m.Start(KindProbeRuntime, 30*time.Millisecond, func(ctx context.Context, _ *Job) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitTerminal(t, m, view.ID)
	history, events, cancel, err := m.Subscribe(view.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("live channel of a finished job should be closed")
		}
	default:
		t.Fatal("live channel of a finished job is not closed")
	}
	if len(history) == 0 {
		t.Fatal("no history events")
	}
	last := history[len(history)-1]
	if last.Name != eventError {
		t.Fatalf("last event = %q, want error", last.Name)
	}
	var data errorEventData
	if err := decodeEventData(last, &data); err != nil {
		t.Fatalf("decode error event: %v", err)
	}
	if data.Code != apierr.CodeTimeout || data.State != StateFailed {
		t.Fatalf("error event = %+v", data)
	}
	if details, ok := data.Details.(map[string]any); !ok || details == nil {
		t.Fatalf("error event details = %#v, want JSON object", data.Details)
	}
	if final.LastSeq != last.Seq {
		t.Fatalf("LastSeq = %d, last event seq = %d", final.LastSeq, last.Seq)
	}
}

func TestHistoryLimitAndOrdering(t *testing.T) {
	m, _ := newManager(t, Options{})
	const total = 25
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		view, err := m.Start(KindProbeRuntime, 0, func(context.Context, *Job) (any, error) {
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Start #%d: %v", i, err)
		}
		waitState(t, m, view.ID, StateSucceeded)
		waitIdle(t, m)
		ids = append(ids, view.ID)
	}
	list := m.List()
	if len(list) != defaultMaxHistory {
		t.Fatalf("List() length = %d, want %d", len(list), defaultMaxHistory)
	}
	for i := range list {
		want := ids[total-1-i]
		if list[i].ID != want {
			t.Fatalf("List()[%d].ID = %s, want %s (newest first)", i, list[i].ID, want)
		}
		if i > 0 && list[i].CreatedAt > list[i-1].CreatedAt {
			t.Fatalf("List() is not sorted by createdAt desc: %d > %d", list[i].CreatedAt, list[i-1].CreatedAt)
		}
	}
	for i := 0; i < total-defaultMaxHistory; i++ {
		if _, ok := m.Get(ids[i]); ok {
			t.Fatalf("job %s should have been pruned from the history", ids[i])
		}
	}
	for i := total - defaultMaxHistory; i < total; i++ {
		if _, ok := m.Get(ids[i]); !ok {
			t.Fatalf("job %s missing from the history", ids[i])
		}
	}
}

func TestConcurrentLogfKeepsSeqMonotonic(t *testing.T) {
	m, _ := newManager(t, Options{})
	const (
		writers   = 10
		perWriter = 5
	)
	view, err := m.Start(KindInstallRuntime, 0, func(_ context.Context, j *Job) (any, error) {
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					j.Logf(fmt.Sprintf("line %d-%d", w, i))
				}
			}(w)
		}
		wg.Wait()
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitState(t, m, view.ID, StateSucceeded)
	waitIdle(t, m)

	if want := int64(1 + writers*perWriter + 1 + 1); final.LastSeq != want {
		t.Fatalf("LastSeq = %d, want %d", final.LastSeq, want)
	}
	history, _, cancel, err := m.Subscribe(view.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	if len(history) != int(final.LastSeq) {
		t.Fatalf("history length = %d, want %d", len(history), final.LastSeq)
	}
	assertSequentialIDs(t, toParsedEvents(t, history), 1)
	seen := make(map[string]bool)
	for _, ev := range history {
		if ev.Name == eventLog {
			var data logEventData
			if err := decodeEventData(ev, &data); err != nil {
				t.Fatalf("decode log event: %v", err)
			}
			seen[data.Line] = true
		}
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("distinct log lines = %d, want %d", len(seen), writers*perWriter)
	}
}

func TestRingOverflowReplay(t *testing.T) {
	m, _ := newManager(t, Options{MaxEvents: 10})
	const lines = 30
	view, err := m.Start(KindInstallRuntime, 0, func(_ context.Context, j *Job) (any, error) {
		for i := 0; i < lines; i++ {
			j.Logf(fmt.Sprintf("line %d", i))
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitState(t, m, view.ID, StateSucceeded)
	waitIdle(t, m)
	wantTotal := int64(1 + lines + 1 + 1)
	if final.LastSeq != wantTotal {
		t.Fatalf("LastSeq = %d, want %d", final.LastSeq, wantTotal)
	}

	history, _, cancel, err := m.Subscribe(view.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	if len(history) != 10 {
		t.Fatalf("replayed events = %d, want 10 (ring size)", len(history))
	}
	parsed := toParsedEvents(t, history)
	assertSequentialIDs(t, parsed, wantTotal-9)

	replay, _, cancel2, err := m.Subscribe(view.ID, 25)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel2()
	if want := int(wantTotal - 25); len(replay) != want {
		t.Fatalf("events after seq 25 = %d, want %d", len(replay), want)
	}
}

func TestSubscribeHistoryPlusLive(t *testing.T) {
	m, _ := newManager(t, Options{})
	proceed := make(chan struct{})
	view, err := m.Start(KindLaunchInstance, 0, func(_ context.Context, j *Job) (any, error) {
		j.Logf("before subscribe")
		<-proceed
		j.Logf("after subscribe")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait until the first log event is buffered.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := m.Get(view.ID); ok && v.LastSeq >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	history, events, cancel, err := m.Subscribe(view.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	if len(history) < 2 {
		t.Fatalf("history events = %d, want >= 2", len(history))
	}
	close(proceed)

	var live []Event
	for ev := range events {
		live = append(live, ev)
	}
	all := append(append([]Event{}, history...), live...)
	if len(all) < 4 {
		t.Fatalf("total events = %d, want >= 4", len(all))
	}
	seqs := make([]int64, len(all))
	for i, ev := range all {
		seqs[i] = ev.Seq
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatalf("sequence gap/duplicate: %v", seqs)
		}
	}
	if seqs[0] != 1 {
		t.Fatalf("first seq = %d, want 1", seqs[0])
	}
	if last := all[len(all)-1]; last.Name != eventResult {
		t.Fatalf("last event = %q, want result", last.Name)
	}
}

func TestJobIgnoresEventsAfterTerminal(t *testing.T) {
	m, _ := newManager(t, Options{})
	var (
		mu       sync.Mutex
		captured *Job
	)
	view, err := m.Start(KindProbeRuntime, 0, func(_ context.Context, j *Job) (any, error) {
		mu.Lock()
		captured = j
		mu.Unlock()
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	final := waitState(t, m, view.ID, StateSucceeded)
	waitIdle(t, m)

	mu.Lock()
	j := captured
	mu.Unlock()
	if j == nil {
		t.Fatal("task did not receive the job handle")
	}
	j.Logf("late log")
	j.Phase("late", "late message")
	after, _ := m.Get(view.ID)
	if after.LastSeq != final.LastSeq {
		t.Fatalf("LastSeq changed after terminal: %d -> %d", final.LastSeq, after.LastSeq)
	}
	if after.Phase != final.Phase {
		t.Fatalf("Phase changed after terminal: %q -> %q", final.Phase, after.Phase)
	}
}

func TestShutdownCancelsActiveJob(t *testing.T) {
	m, _ := newManager(t, Options{})
	view := startBlockingJob(t, m, KindInstallRuntime)
	m.Shutdown()

	final, ok := m.Get(view.ID)
	if !ok {
		t.Fatalf("job %s disappeared", view.ID)
	}
	if final.State != StateCancelled {
		t.Fatalf("State = %q, want CANCELLED", final.State)
	}
	if m.Busy() {
		t.Fatal("Busy() = true after Shutdown")
	}
	if _, err := m.Start(KindProbeRuntime, 0, func(context.Context, *Job) (any, error) {
		return nil, nil
	}); err == nil {
		t.Fatal("Start succeeded after Shutdown")
	} else if apiErr, ok := apierr.As(err); !ok || apiErr.Code != apierr.CodeInternal {
		t.Fatalf("Start after Shutdown error = %v", err)
	}
	m.Shutdown() // idempotent
}

func TestEventRing(t *testing.T) {
	ring := newEventRing(3)
	for seq := int64(1); seq <= 5; seq++ {
		ring.add(Event{Seq: seq, Name: eventLog, Data: []byte("{}")})
	}
	got := make([]int64, 0, 3)
	for _, ev := range ring.after(0) {
		got = append(got, ev.Seq)
	}
	if fmt.Sprint(got) != "[3 4 5]" {
		t.Fatalf("after(0) = %v, want [3 4 5]", got)
	}
	got = got[:0]
	for _, ev := range ring.after(3) {
		got = append(got, ev.Seq)
	}
	if fmt.Sprint(got) != "[4 5]" {
		t.Fatalf("after(3) = %v, want [4 5]", got)
	}
	if events := ring.after(5); events != nil {
		t.Fatalf("after(5) = %v, want nil", events)
	}
	if events := ring.after(10); events != nil {
		t.Fatalf("after(10) = %v, want nil", events)
	}
}

// toParsedEvents converts internal events into the shared SSE test shape.
func toParsedEvents(t *testing.T, events []Event) []parsedEvent {
	t.Helper()
	parsed := make([]parsedEvent, len(events))
	for i, ev := range events {
		parsed[i] = parsedEvent{ID: ev.Seq, Name: ev.Name}
		if err := json.Unmarshal(ev.Data, &parsed[i].Data); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
	}
	return parsed
}

// decodeEventData decodes one event payload into target.
func decodeEventData(ev Event, target any) error {
	return json.Unmarshal(ev.Data, target)
}

// TestKindConstants locks the frozen Kind strings (docs/M3_INTERFACES.md §7) and
// checks that the M3 kinds flow through Start/View like the M1 kinds.
func TestKindConstants(t *testing.T) {
	want := map[Kind]string{
		KindLaunchInstance: "LAUNCH_INSTANCE",
		KindInstallRuntime: "INSTALL_RUNTIME",
		KindProbeRuntime:   "PROBE_RUNTIME",
		KindImportPack:     "IMPORT_PACK",
		KindExportPack:     "EXPORT_PACK",
		KindPluginMutate:   "PLUGIN_MUTATE",
	}
	for kind, value := range want {
		if string(kind) != value {
			t.Fatalf("Kind = %q，期望 %q", string(kind), value)
		}
	}
	m, _ := newManager(t, Options{})
	for _, kind := range []Kind{KindImportPack, KindExportPack, KindPluginMutate} {
		view, err := m.Start(kind, 0, func(context.Context, *Job) (any, error) {
			return map[string]any{"ok": true}, nil
		})
		if err != nil {
			t.Fatalf("Start(%s) 失败: %v", kind, err)
		}
		final := waitState(t, m, view.ID, StateSucceeded)
		if final.Type != kind {
			t.Fatalf("View.Type = %q，期望 %q", final.Type, kind)
		}
		waitIdle(t, m)
	}
}
