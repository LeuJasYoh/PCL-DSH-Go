// SPDX-License-Identifier: GPL-3.0-or-later

// Package job implements the job manager used by the M1/M3 API: at most one
// globally active long-running task (install / probe / launch / pack import /
// pack export / plugin mutation), an in-memory job history and per-job SSE
// event streams.
//
// The contract is docs/M1_INTERFACES.md §6 and docs/IMPLEMENTATION_SPEC.md
// §4.5. The manager owns:
//
//   - a single global active job (concurrent Start returns 409 CONFLICT_BUSY),
//   - bounded in-memory history (default: last 20 completed jobs),
//   - a bounded ring of the most recent SSE events per job (default 4000),
//   - Last-Event-ID replay and a 15s SSE heartbeat,
//   - cancellation / timeout semantics delivered through the Task context.
//
// Tasks must honour their context: cancellation and the per-task timeout are
// implemented by cancelling the context passed to the Task. The terminal job
// state is produced from the Task's return value (or from its context error),
// so a Task that ignores its context keeps the manager busy until it returns.
//
// Events carry a strictly increasing sequence number starting at 1. Each job
// begins with a "state" event (RUNNING) and ends with a terminal "state" event
// followed by a "result" event (SUCCEEDED) or an "error" event (FAILED).
// Cancelled jobs end with the terminal state event only.
package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/logging"
)

// Kind identifies the long-running operation a job performs.
type Kind string

const (
	// KindLaunchInstance launches an instance and waits until it is ready.
	KindLaunchInstance Kind = "LAUNCH_INSTANCE"
	// KindInstallRuntime installs a DSH runtime.
	KindInstallRuntime Kind = "INSTALL_RUNTIME"
	// KindProbeRuntime probes an installed DSH runtime.
	KindProbeRuntime Kind = "PROBE_RUNTIME"
	// KindImportPack imports a dspack/Overture pack as a new instance.
	KindImportPack Kind = "IMPORT_PACK"
	// KindExportPack exports an instance profile/home as a downloadable pack.
	KindExportPack Kind = "EXPORT_PACK"
	// KindPluginMutate installs, updates or removes a plugin of an instance.
	KindPluginMutate Kind = "PLUGIN_MUTATE"
)

// State is the lifecycle state of a job.
type State string

const (
	// StateRunning means the task is being executed.
	StateRunning State = "RUNNING"
	// StateSucceeded means the task returned a result.
	StateSucceeded State = "SUCCEEDED"
	// StateFailed means the task failed (including timeouts).
	StateFailed State = "FAILED"
	// StateCancelled means the job was cancelled before completion.
	StateCancelled State = "CANCELLED"
)

// phasePreparing is the initial phase of every job.
const phasePreparing = "准备中"

// ErrorView is the JSON representation of a failed job (JobView.error). Details
// is always a JSON object: errors without structured details expose an empty
// object (mirroring the API error body convention).
type ErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// normalizeDetails returns details as a JSON object, substituting an empty
// object when the error carries none. The API error model always exposes a
// details object, so it must never be null or omitted.
func normalizeDetails(details any) any {
	if details == nil {
		return map[string]any{}
	}
	return details
}

// View is the JSON representation of a job (IMPLEMENTATION_SPEC §4.3 JobView,
// M1_INTERFACES §6). All fields are always present in JSON; nullable fields
// are encoded as null.
type View struct {
	ID         string     `json:"id"`
	Type       Kind       `json:"type"`
	State      State      `json:"state"`
	Phase      string     `json:"phase"`
	Message    string     `json:"message"`
	CreatedAt  int64      `json:"createdAt"`
	StartedAt  *int64     `json:"startedAt"`
	FinishedAt *int64     `json:"finishedAt"`
	LastSeq    int64      `json:"lastSeq"`
	Result     any        `json:"result"`
	Error      *ErrorView `json:"error"`
}

// Task is the blocking work executed by a job. It must return when ctx is
// cancelled; the returned error is mapped onto the job outcome:
//
//   - nil                -> SUCCEEDED with result,
//   - *apierr.Error      -> FAILED with its code/message/details,
//   - context deadline    -> FAILED with code TIMEOUT,
//   - context cancelled   -> CANCELLED,
//   - any other error     -> FAILED with code INTERNAL (only logged, never
//     echoed to clients).
type Task func(ctx context.Context, j *Job) (result any, err error)

// Job is the handle passed to a running Task. It is safe for concurrent use:
// Logf and Phase may be called from multiple goroutines.
type Job struct {
	mu         sync.Mutex
	id         string
	kind       Kind
	state      State
	phase      string
	message    string
	createdAt  int64
	createdSeq int64
	startedAt  *int64
	finishedAt *int64
	lastSeq    int64
	result     any
	errView    *ErrorView
	events     *eventRing
	subs       map[*subscriber]struct{}
	ctx        context.Context
	cancelFn   context.CancelFunc
	canceled   bool
	timeout    time.Duration
	terminal   bool
	now        func() time.Time
}

// Logf appends an informational "log" event to the job stream. Log lines are
// redacted defensively: the app layer is already required to keep secrets out
// of progress logs, and a missed secret must not leak through SSE or the job
// history.
func (j *Job) Logf(line string) {
	line = logging.Redact(line)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal {
		return
	}
	j.lastSeq++
	raw, err := json.Marshal(logEventData{
		JobID: j.id,
		Seq:   j.lastSeq,
		TS:    j.now().UnixMilli(),
		Level: levelInfo,
		Line:  line,
	})
	if err != nil {
		// Only plain strings are encoded here; this cannot fail in practice.
		j.lastSeq--
		return
	}
	j.publishLocked(Event{Seq: j.lastSeq, Name: eventLog, Data: raw})
}

// Phase updates the current phase/message (published as a "state" event) and
// thus the phase shown by GET /api/jobs and /api/state.
func (j *Job) Phase(phase, message string) {
	message = logging.Redact(message)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal {
		return
	}
	j.phase = phase
	j.message = message
	j.emitStateLocked()
}

// snapshot returns a consistent copy of the job view.
func (j *Job) snapshot() View {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.viewLocked()
}

// viewLocked returns a View copy; the caller must hold j.mu.
func (j *Job) viewLocked() View {
	v := View{
		ID:        j.id,
		Type:      j.kind,
		State:     j.state,
		Phase:     j.phase,
		Message:   j.message,
		CreatedAt: j.createdAt,
		LastSeq:   j.lastSeq,
		Result:    j.result,
		Error:     j.errView,
	}
	if j.startedAt != nil {
		t := *j.startedAt
		v.StartedAt = &t
	}
	if j.finishedAt != nil {
		t := *j.finishedAt
		v.FinishedAt = &t
	}
	return v
}

// emitStateLocked appends the current state as a "state" event.
func (j *Job) emitStateLocked() {
	j.lastSeq++
	raw, err := json.Marshal(stateEventData{
		JobID:   j.id,
		Seq:     j.lastSeq,
		TS:      j.now().UnixMilli(),
		State:   j.state,
		Phase:   j.phase,
		Message: j.message,
	})
	if err != nil {
		j.lastSeq--
		return
	}
	j.publishLocked(Event{Seq: j.lastSeq, Name: eventState, Data: raw})
}

// emitResultLocked appends the terminal "result" event.
func (j *Job) emitResultLocked(result any) {
	j.lastSeq++
	raw, err := json.Marshal(resultEventData{JobID: j.id, State: j.state, Result: result})
	if err != nil {
		j.lastSeq--
		return
	}
	j.publishLocked(Event{Seq: j.lastSeq, Name: eventResult, Data: raw})
}

// emitErrorLocked appends the terminal "error" event.
func (j *Job) emitErrorLocked(errView *ErrorView) {
	j.lastSeq++
	raw, err := json.Marshal(errorEventData{
		JobID:   j.id,
		State:   j.state,
		Code:    errView.Code,
		Message: errView.Message,
		Details: normalizeDetails(errView.Details),
	})
	if err != nil {
		j.lastSeq--
		return
	}
	j.publishLocked(Event{Seq: j.lastSeq, Name: eventError, Data: raw})
}

// publishLocked stores the event in the ring and fans it out to live
// subscribers. The caller must hold j.mu.
func (j *Job) publishLocked(ev Event) {
	j.events.add(ev)
	for sub := range j.subs {
		select {
		case sub.ch <- ev:
		default:
			// Slow consumer: drop the subscription. The SSE handler
			// terminates and the client reconnects with Last-Event-ID so no
			// event is lost from the client's point of view.
			sub.closed = true
			delete(j.subs, sub)
			close(sub.ch)
		}
	}
}

// closeSubscribersLocked closes every live subscriber channel. The caller
// must hold j.mu.
func (j *Job) closeSubscribersLocked() {
	for sub := range j.subs {
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
	}
	j.subs = nil
}

// subscribe registers a live subscriber and returns the events already cached
// after afterSeq.
func (j *Job) subscribe(afterSeq int64) ([]Event, <-chan Event, func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	history := j.events.after(afterSeq)
	if j.terminal {
		ch := make(chan Event)
		close(ch)
		return history, ch, func() {}, nil
	}
	sub := &subscriber{ch: make(chan Event, subscriberBuffer)}
	j.subs[sub] = struct{}{}
	return history, sub.ch, func() { j.removeSubscriber(sub) }, nil
}

// removeSubscriber unregisters a subscriber; it is safe to call after the job
// closed the subscriber channel and more than once.
func (j *Job) removeSubscriber(sub *subscriber) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	delete(j.subs, sub)
	close(sub.ch)
}

// complete decides the terminal state, emits the terminal events and closes
// the live subscribers. It reports whether this call finalized the job.
func (j *Job) complete(result any, taskErr error, logger *slog.Logger) bool {
	finishedAt := j.now().UnixMilli()
	ctxErr := j.ctx.Err()
	j.cancelFn() // release the context timer; ctxErr was captured first

	j.mu.Lock()
	if j.terminal {
		j.mu.Unlock()
		return false
	}
	j.finishedAt = &finishedAt

	timeoutHit := errors.Is(taskErr, context.DeadlineExceeded) || ctxErr == context.DeadlineExceeded
	var (
		finalState   State
		errView      *ErrorView
		resultAny    any
		finalMessage string
	)
	switch {
	case j.canceled:
		finalState = StateCancelled
		finalMessage = "任务已取消。"
	case timeoutHit:
		finalState = StateFailed
		errView = &ErrorView{
			Code:    apierr.CodeTimeout,
			Message: "任务执行超时（" + j.timeout.String() + "），已中止。",
			Details: normalizeDetails(nil),
		}
		finalMessage = errView.Message
	case taskErr == nil:
		finalState = StateSucceeded
		resultAny = result
		finalMessage = "任务完成。"
	default:
		if apiErr, ok := apierr.As(taskErr); ok {
			finalState = StateFailed
			errView = &ErrorView{Code: apiErr.Code, Message: apiErr.Message, Details: normalizeDetails(apiErr.Details)}
		} else if errors.Is(taskErr, context.Canceled) {
			finalState = StateCancelled
			finalMessage = "任务已取消。"
		} else {
			finalState = StateFailed
			errView = &ErrorView{Code: apierr.CodeInternal, Message: "服务器内部错误。", Details: normalizeDetails(nil)}
		}
		if errView != nil {
			finalMessage = errView.Message
		}
	}

	j.state = finalState
	j.message = finalMessage
	j.result = resultAny
	j.errView = errView
	j.emitStateLocked()
	switch finalState {
	case StateSucceeded:
		j.emitResultLocked(resultAny)
	case StateFailed:
		j.emitErrorLocked(errView)
	}
	j.terminal = true
	j.closeSubscribersLocked()
	durationMs := finishedAt - j.createdAt
	jobID, kind := j.id, j.kind
	j.mu.Unlock()

	// Log after releasing the lock: handlers and List/Get must never block on
	// log I/O.
	switch finalState {
	case StateSucceeded:
		logger.Info("job succeeded", "jobId", jobID, "type", string(kind), "durationMs", durationMs)
	case StateCancelled:
		logger.Info("job cancelled", "jobId", jobID, "type", string(kind), "durationMs", durationMs)
	default:
		logger.Warn("job failed", "jobId", jobID, "type", string(kind), "code", errView.Code, "durationMs", durationMs)
		if errView.Code == apierr.CodeInternal && taskErr != nil {
			var pe *taskPanic
			if errors.As(taskErr, &pe) {
				logger.Error("job task panicked",
					"jobId", jobID,
					"type", string(kind),
					"panic", logging.Redact(pe.Error()),
					"stack", logging.Redact(string(pe.stack)),
				)
			} else {
				logger.Error("job task failed",
					"jobId", jobID,
					"type", string(kind),
					"error", logging.Redact(taskErr.Error()),
				)
			}
		}
	}
	return true
}

// taskPanic wraps a recovered panic so that the original value and stack can
// be logged while clients only see the generic INTERNAL error.
type taskPanic struct {
	value any
	stack []byte
}

func (p *taskPanic) Error() string { return fmt.Sprintf("task panicked: %v", p.value) }

// subscriber is one live SSE consumer. ch and closed are guarded by Job.mu.
type subscriber struct {
	ch     chan Event
	closed bool
}
