// SPDX-License-Identifier: MIT

package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

// Documented defaults (docs/M1_INTERFACES.md §6).
const (
	defaultHeartbeat   = 15 * time.Second
	defaultMaxEvents   = 4000
	defaultMaxHistory  = 20
	defaultMaxDuration = 20 * time.Minute

	// shutdownGrace bounds how long Shutdown waits for a cancelled Task to
	// return. Tasks are expected to honour their context; the bound only keeps
	// a buggy task from blocking process exit forever.
	shutdownGrace = 5 * time.Second
)

// Options configures a Manager. Zero values select the documented defaults.
type Options struct {
	// Heartbeat is the SSE heartbeat interval (": ping" comment lines).
	Heartbeat time.Duration
	// MaxEvents is the per-job ring buffer size used for Last-Event-ID replay.
	MaxEvents int
	// MaxHistory is the number of finished jobs kept in memory and returned by
	// List (newest first).
	MaxHistory int
	// MaxDuration is the default per-task timeout.
	MaxDuration time.Duration
	// Now is the clock used for timestamps (millisecond fields). Timeouts use
	// the real clock. Defaults to time.Now.
	Now func() time.Time
}

// withDefaults copies o and fills zero values with the documented defaults.
func (o Options) withDefaults() Options {
	if o.Heartbeat <= 0 {
		o.Heartbeat = defaultHeartbeat
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = defaultMaxEvents
	}
	if o.MaxHistory <= 0 {
		o.MaxHistory = defaultMaxHistory
	}
	if o.MaxDuration <= 0 {
		o.MaxDuration = defaultMaxDuration
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Manager owns the single active job, the bounded in-memory history and the
// SSE subscribers. It is safe for concurrent use.
type Manager struct {
	logger *slog.Logger
	opts   Options

	mu      sync.Mutex
	nextSeq int64 // creation sequence used to order jobs created in the same ms
	active  *Job
	history []*Job // finished jobs, oldest first
	jobs    map[string]*Job
	closed  bool
	wg      sync.WaitGroup
}

// NewManager builds a job manager. A nil logger discards manager log output;
// zero option values select the documented defaults.
func NewManager(logger *slog.Logger, opts Options) *Manager {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Manager{
		logger: logger,
		opts:   opts.withDefaults(),
		jobs:   make(map[string]*Job),
	}
}

// Start registers a new job and runs task in its own goroutine. Only one job
// may be active at a time: when another job is running the call fails with
// *apierr.Error 409 CONFLICT_BUSY.
//
// timeout <= 0 selects Options.MaxDuration. The timeout and cancellation are
// delivered by cancelling the context passed to the task; a timeout ends in
// FAILED with code TIMEOUT and a cancellation ends in CANCELLED.
func (m *Manager) Start(kind Kind, timeout time.Duration, task Task) (*View, error) {
	if task == nil {
		return nil, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "任务执行器未配置。")
	}
	if timeout <= 0 {
		timeout = m.opts.MaxDuration
	}
	id, err := newJobID()
	if err != nil {
		m.logger.Error("job id generation failed", "error", err)
		return nil, apierr.InternalError()
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, apierr.New(http.StatusInternalServerError, apierr.CodeInternal, "服务正在关闭，无法启动新任务。")
	}
	if m.active != nil {
		m.mu.Unlock()
		return nil, apierr.Busy()
	}
	j := newJob(id, kind, timeout, m.opts)
	m.nextSeq++
	j.createdSeq = m.nextSeq
	m.jobs[id] = j
	m.active = j
	// Add under the lock so a concurrent Shutdown can never call Wait on a
	// zero counter while this job is still starting.
	m.wg.Add(1)
	m.mu.Unlock()

	view := j.snapshot()
	m.logger.Info("job started",
		"jobId", id,
		"type", string(kind),
		"timeout", timeout.String(),
	)
	go func() {
		defer m.wg.Done()
		m.run(j, task)
	}()
	return &view, nil
}

// run executes the task and finalizes the job.
func (m *Manager) run(j *Job, task Task) {
	result, taskErr := invokeTask(task, j)
	if finalized := j.complete(result, taskErr, m.logger); finalized {
		m.finish(j)
	}
}

// invokeTask calls task, converting a panic into an error that is logged but
// never exposed to clients.
func invokeTask(task Task, j *Job) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &taskPanic{value: recovered, stack: debug.Stack()}
		}
	}()
	return task(j.ctx, j)
}

// finish moves a finalized job from the active slot into the bounded history
// and drops the oldest jobs beyond Options.MaxHistory.
func (m *Manager) finish(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == j {
		m.active = nil
	}
	j.mu.Lock()
	terminal := j.terminal
	j.mu.Unlock()
	if !terminal {
		return
	}
	m.history = append(m.history, j)
	if maxHistory := m.opts.MaxHistory; len(m.history) > maxHistory {
		drop := len(m.history) - maxHistory
		for _, old := range m.history[:drop] {
			delete(m.jobs, old.id)
		}
		m.history = append([]*Job(nil), m.history[drop:]...)
	}
}

// Busy reports whether a job is currently running.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active != nil
}

// List returns at most Options.MaxHistory views, newest first. The running job,
// if any, is included.
func (m *Manager) List() []View {
	m.mu.Lock()
	jobs := make([]*Job, 0, len(m.history)+1)
	if m.active != nil {
		jobs = append(jobs, m.active)
	}
	jobs = append(jobs, m.history...)
	m.mu.Unlock()

	type entry struct {
		view View
		seq  int64
	}
	entries := make([]entry, 0, len(jobs))
	for _, j := range jobs {
		entries = append(entries, entry{view: j.snapshot(), seq: j.createdSeq})
	}
	// createdSeq is immutable after creation and is read here after the
	// manager lock was acquired, so it is free of data races.
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].view.CreatedAt != entries[b].view.CreatedAt {
			return entries[a].view.CreatedAt > entries[b].view.CreatedAt
		}
		return entries[a].seq > entries[b].seq
	})
	if maxHistory := m.opts.MaxHistory; len(entries) > maxHistory {
		entries = entries[:maxHistory]
	}
	views := make([]View, len(entries))
	for i := range entries {
		views[i] = entries[i].view
	}
	return views
}

// Get returns the view of one job.
func (m *Manager) Get(id string) (View, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return View{}, false
	}
	return j.snapshot(), true
}

// Cancel requests cancellation of a running job. It returns 404 NOT_FOUND for
// an unknown job and 409 CONFLICT_JOB_FINISHED for an already finished one.
// Cancellation is cooperative: the task's context is cancelled and the final
// state becomes CANCELLED once the task returns.
func (m *Manager) Cancel(id string) (View, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return View{}, apierr.NotFound("任务不存在。")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal {
		return View{}, apierr.New(http.StatusConflict, apierr.CodeConflictJobFinished, "任务已结束，无法取消。")
	}
	if !j.canceled {
		j.canceled = true
		j.cancelFn()
	}
	return j.viewLocked(), nil
}

// Subscribe returns the cached events after afterSeq and a channel of live
// events. The channel is closed when the job reaches its terminal state or when
// cancel is called. cancel is idempotent.
func (m *Manager) Subscribe(id string, afterSeq int64) ([]Event, <-chan Event, func(), error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return nil, nil, nil, apierr.NotFound("任务不存在。")
	}
	return j.subscribe(afterSeq)
}

// Shutdown cancels the active job (if any) and waits up to shutdownGrace for
// its task to return. It is idempotent and makes further Start calls fail.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	active := m.active
	m.mu.Unlock()

	if active != nil {
		active.mu.Lock()
		if !active.canceled {
			active.canceled = true
			active.cancelFn()
		}
		active.mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		m.logger.Warn("job shutdown timed out waiting for the active task to return")
	}
}

// newJob builds a RUNNING job and emits its initial state event.
func newJob(id string, kind Kind, timeout time.Duration, opts Options) *Job {
	now := opts.Now()
	createdAt := now.UnixMilli()
	startedAt := createdAt
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	j := &Job{
		id:        id,
		kind:      kind,
		state:     StateRunning,
		phase:     phasePreparing,
		message:   "任务已开始。",
		createdAt: createdAt,
		startedAt: &startedAt,
		events:    newEventRing(opts.MaxEvents),
		subs:      make(map[*subscriber]struct{}),
		ctx:       ctx,
		cancelFn:  cancel,
		timeout:   timeout,
		now:       opts.Now,
	}
	j.emitStateLocked()
	return j
}

// newJobID returns a fresh job id: "j-" followed by 12 lowercase hex digits.
func newJobID() (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "j-" + hex.EncodeToString(raw[:]), nil
}
