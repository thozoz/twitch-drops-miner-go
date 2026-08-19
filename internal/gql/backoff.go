package gql

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// BackoffOption configures an ExponentialBackoff.
type BackoffOption func(*ExponentialBackoff)

// WithBackoffBase sets the exponential growth base.
func WithBackoffBase(base float64) BackoffOption {
	return func(b *ExponentialBackoff) {
		b.base = base
	}
}

// WithBackoffVariance sets the +/- variance percentage (e.g. 0.1 for +/- 10%).
func WithBackoffVariance(variance float64) BackoffOption {
	return func(b *ExponentialBackoff) {
		b.varianceMin = 1.0 - variance
		b.varianceMax = 1.0 + variance
	}
}

// WithBackoffShift sets an additive shift value in seconds.
func WithBackoffShift(shift float64) BackoffOption {
	return func(b *ExponentialBackoff) {
		b.shift = shift
	}
}

// WithBackoffMaximum sets the maximum backoff ceiling in seconds.
func WithBackoffMaximum(maximum float64) BackoffOption {
	return func(b *ExponentialBackoff) {
		b.maximum = maximum
	}
}

// ExponentialBackoff computes exponentially increasing delays with jitter.
// Ported directly from DevilXD/TwitchDropsMiner utils.py:ExponentialBackoff.
type ExponentialBackoff struct {
	base        float64
	varianceMin float64
	varianceMax float64
	shift       float64
	maximum     float64
	steps       int
	mu          sync.Mutex
}

// NewExponentialBackoff creates an ExponentialBackoff with default values
// (base=2, variance=+/-10%, shift=0, maximum=300s).
func NewExponentialBackoff(opts ...BackoffOption) *ExponentialBackoff {
	b := &ExponentialBackoff{
		base:        2.0,
		varianceMin: 0.9,
		varianceMax: 1.1,
		shift:       0.0,
		maximum:     300.0,
		steps:       0,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Next calculates the next backoff duration.
func (b *ExponentialBackoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	r := b.varianceMin + rand.Float64()*(b.varianceMax-b.varianceMin)
	val := math.Pow(b.base, float64(b.steps))*r + b.shift

	if val > b.maximum {
		return time.Duration(b.maximum * float64(time.Second))
	}
	b.steps++
	return time.Duration(val * float64(time.Second))
}

// Reset resets the exponential step counter back to 0.
func (b *ExponentialBackoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.steps = 0
}

// Steps returns the current step count.
func (b *ExponentialBackoff) Steps() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.steps
}
