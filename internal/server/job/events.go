// SPDX-License-Identifier: MIT

package job

// SSE event names (docs/IMPLEMENTATION_SPEC.md §4.5) and payload identifiers.
const (
	eventState  = "state"
	eventLog    = "log"
	eventResult = "result"
	eventError  = "error"

	levelInfo = "info"
)

// Event is one SSE event of a job.
type Event struct {
	Seq  int64
	Name string
	Data []byte // JSON-encoded payload
}

// stateEventData is the data of a "state" event.
type stateEventData struct {
	JobID   string `json:"jobId"`
	Seq     int64  `json:"seq"`
	TS      int64  `json:"ts"`
	State   State  `json:"state"`
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

// logEventData is the data of a "log" event.
type logEventData struct {
	JobID string `json:"jobId"`
	Seq   int64  `json:"seq"`
	TS    int64  `json:"ts"`
	Level string `json:"level"`
	Line  string `json:"line"`
}

// resultEventData is the data of the terminal "result" event.
type resultEventData struct {
	JobID  string `json:"jobId"`
	State  State  `json:"state"`
	Result any    `json:"result"`
}

// errorEventData is the data of the terminal "error" event.
type errorEventData struct {
	JobID   string `json:"jobId"`
	State   State  `json:"state"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// subscriberBuffer is the per-connection live event queue. A subscriber that
// cannot keep up is dropped and reconnects with Last-Event-ID; the ring buffer
// keeps the replay window much larger than this queue.
const subscriberBuffer = 512

// eventRing is a fixed-size FIFO ring of events used for Last-Event-ID replay.
type eventRing struct {
	buf   []Event
	start int // index of the oldest event once the ring is full
	count int
}

// newEventRing allocates a ring for at most size events.
func newEventRing(size int) *eventRing {
	if size <= 0 {
		size = 1
	}
	return &eventRing{buf: make([]Event, size)}
}

// add appends an event, overwriting the oldest one when the ring is full.
func (r *eventRing) add(ev Event) {
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = ev
		r.count++
		return
	}
	r.buf[r.start] = ev
	r.start = (r.start + 1) % len(r.buf)
}

// after returns a copy of the cached events whose sequence number is greater
// than seq, in ascending sequence order.
func (r *eventRing) after(seq int64) []Event {
	out := make([]Event, 0, r.count)
	for i := 0; i < r.count; i++ {
		ev := r.buf[(r.start+i)%len(r.buf)]
		if ev.Seq > seq {
			out = append(out, ev)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
