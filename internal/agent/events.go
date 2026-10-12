package agent

import (
	"sync"
	"time"
)

// Event is an entry in the agent's activity log shown on the dashboard.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // alert, webhook_error, rules, error
	Rule    string    `json:"rule,omitempty"`
	File    string    `json:"file,omitempty"`
	Message string    `json:"message"`
}

// ring is a fixed-capacity event log. Old entries are overwritten, so its
// memory footprint never grows regardless of how many alerts fire.
type ring struct {
	mu   sync.Mutex
	buf  []Event
	next int
	full bool
}

// maxEventMessage caps the size of a stored message.
const maxEventMessage = 2048

func newRing(size int) *ring { return &ring{buf: make([]Event, size)} }

func (r *ring) add(e Event) {
	if len(e.Message) > maxEventMessage {
		// Copy, so we do not pin a large backing array.
		e.Message = string([]byte(e.Message[:maxEventMessage])) + "…"
	}
	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

// list returns up to limit events, newest first.
func (r *ring) list(limit int) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.next
	if r.full {
		n = len(r.buf)
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Event, 0, limit)
	for i := 0; i < limit; i++ {
		idx := (r.next - 1 - i + len(r.buf)) % len(r.buf)
		out = append(out, r.buf[idx])
	}
	return out
}
