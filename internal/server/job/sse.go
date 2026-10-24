// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sseMaxDuration is the maximum lifetime of one SSE connection. Clients
// reconnect with Last-Event-ID; EventSource does so automatically.
const sseMaxDuration = 30 * time.Minute

// handleEvents serves GET /api/jobs/{id}/events as a Server-Sent Events stream
// (docs/IMPLEMENTATION_SPEC.md §4.5).
func (m *Manager) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		setSSEHeaders(w.Header())
		w.WriteHeader(http.StatusOK)
		return
	}

	history, events, cancel, err := m.Subscribe(r.PathValue("id"), lastEventID(r))
	if err != nil {
		m.writeError(w, r, err)
		return
	}
	defer cancel()

	setSSEHeaders(w.Header())
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	// Flush the headers immediately so the client sees an open stream even
	// when no event follows right away.
	if !flushSSE(controller) {
		return
	}

	for _, ev := range history {
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
	}
	if len(history) > 0 && !flushSSE(controller) {
		return
	}

	heartbeat := time.NewTicker(m.opts.Heartbeat)
	defer heartbeat.Stop()
	connectionLimit := time.NewTimer(sseMaxDuration)
	defer connectionLimit.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected (or the server is shutting down).
			return
		case <-connectionLimit.C:
			// Maximum connection lifetime reached; the client reconnects.
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			if !flushSSE(controller) {
				return
			}
		case ev, ok := <-events:
			if !ok {
				// The job reached its terminal state; all events were sent.
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			if !flushSSE(controller) {
				return
			}
		}
	}
}

// flushSSE flushes buffered bytes. A response writer chain without Flush
// support (http.ErrNotSupported) is not fatal: writes still reach the client
// buffered, so streaming continues instead of aborting the event stream.
func flushSSE(controller *http.ResponseController) bool {
	err := controller.Flush()
	return err == nil || errors.Is(err, http.ErrNotSupported)
}

// setSSEHeaders sets the SSE response headers required by the contract.
func setSSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("X-Accel-Buffering", "no")
}

// writeSSEEvent serializes one event as id/event/data + blank line.
func writeSSEEvent(w io.Writer, ev Event) error {
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Name, ev.Data)
	return err
}

// lastEventID parses the Last-Event-ID request header. An absent or invalid
// value replays the whole ring buffer (seq 0).
func lastEventID(r *http.Request) int64 {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		return 0
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0
	}
	return seq
}
