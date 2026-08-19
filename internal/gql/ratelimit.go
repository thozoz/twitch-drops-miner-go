package gql

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrRateLimiterClosed is returned when an Acquire is attempted on a closed RateLimiter.
	ErrRateLimiterClosed = errors.New("rate limiter is closed")
)

// RateLimiter enforces Twitch GQL rate limiting matching DevilXD/TwitchDropsMiner semantics:
// gating on max(total, concurrent) < capacity within a rolling window.
type RateLimiter struct {
	capacity   int
	window     time.Duration
	total      int
	concurrent int
	mu         sync.Mutex
	waiters    []chan struct{}
	resetTimer *time.Timer
	closed     bool
}

// NewRateLimiter creates a RateLimiter with the given capacity and window duration.
func NewRateLimiter(capacity int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		capacity: capacity,
		window:   window,
		waiters:  make([]chan struct{}, 0),
	}
}

// Acquire blocks until a slot is available according to max(total, concurrent) < capacity,
// or until ctx is cancelled or the limiter is closed.
func (rl *RateLimiter) Acquire(ctx context.Context) error {
	rl.mu.Lock()

	for {
		if rl.closed {
			rl.mu.Unlock()
			return ErrRateLimiterClosed
		}
		if err := ctx.Err(); err != nil {
			rl.mu.Unlock()
			return err
		}

		if max(rl.total, rl.concurrent) < rl.capacity {
			rl.total++
			rl.concurrent++
			if rl.resetTimer == nil {
				rl.resetTimer = time.AfterFunc(rl.window, rl.onWindowReset)
			}
			rl.mu.Unlock()
			return nil
		}

		// Wait for a slot to free up
		waiter := make(chan struct{}, 1)
		rl.waiters = append(rl.waiters, waiter)
		rl.mu.Unlock()

		select {
		case <-ctx.Done():
			rl.mu.Lock()
			rl.removeWaiter(waiter)
			rl.mu.Unlock()
			return ctx.Err()
		case <-waiter:
			rl.mu.Lock()
		}
	}
}

// Release releases a concurrent slot and notifies any waiting callers.
func (rl *RateLimiter) Release() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.concurrent > 0 {
		rl.concurrent--
	}
	rl.notify()
}

func (rl *RateLimiter) onWindowReset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.total = 0
	rl.resetTimer = nil
	rl.notify()
}

func (rl *RateLimiter) notify() {
	for _, w := range rl.waiters {
		select {
		case w <- struct{}{}:
		default:
		}
	}
	rl.waiters = rl.waiters[:0]
}

func (rl *RateLimiter) removeWaiter(w chan struct{}) {
	for i, waiter := range rl.waiters {
		if waiter == w {
			rl.waiters = append(rl.waiters[:i], rl.waiters[i+1:]...)
			break
		}
	}
}

// Close closes the RateLimiter and unblocks any waiting Acquire calls.
func (rl *RateLimiter) Close() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.closed = true
	if rl.resetTimer != nil {
		rl.resetTimer.Stop()
		rl.resetTimer = nil
	}
	rl.notify()
	return nil
}

// Status returns the current concurrent and total counts.
func (rl *RateLimiter) Status() (concurrent int, total int, capacity int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.concurrent, rl.total, rl.capacity
}
