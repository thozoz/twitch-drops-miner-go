package daemon

import (
	"strings"
	"sync"
)

// RingBuffer stores a fixed-capacity in-memory log buffer and fans out writes to active subscribers.
type RingBuffer struct {
	mu          sync.Mutex
	capacity    int
	lines       []string
	subscribers map[chan string]struct{}
}

// NewRingBuffer creates a new RingBuffer holding up to capacity lines.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingBuffer{
		capacity:    capacity,
		lines:       make([]string, 0, capacity),
		subscribers: make(map[chan string]struct{}),
	}
}

// Write splits p on newlines, adds non-empty lines to the ring, and fans them out to subscribers.
func (r *RingBuffer) Write(p []byte) (n int, err error) {
	s := string(p)
	rawLines := strings.Split(s, "\n")

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, raw := range rawLines {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}

		if len(r.lines) >= r.capacity {
			r.lines = r.lines[1:]
		}
		r.lines = append(r.lines, line)

		for ch := range r.subscribers {
			select {
			case ch <- line:
			default:
				// subscriber full, drop rather than stall logger
			}
		}
	}

	return len(p), nil
}

// Lines returns the last limit lines in chronological order. If limit <= 0, returns all lines.
func (r *RingBuffer) Lines(limit int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if limit <= 0 || limit >= len(r.lines) {
		return append([]string(nil), r.lines...)
	}

	start := len(r.lines) - limit
	return append([]string(nil), r.lines[start:]...)
}

// Subscribe returns a channel of live log lines and a cancellation function.
func (r *RingBuffer) Subscribe() (<-chan string, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch := make(chan string, 64)
	r.subscribers[ch] = struct{}{}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subscribers, ch)
			close(ch)
			r.mu.Unlock()
		})
	}

	return ch, cancel
}
