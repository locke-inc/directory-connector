package scim

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu         sync.Mutex
	maxPerWin  int
	window     time.Duration
	timestamps []time.Time
}

func newRateLimiter(maxPerWindow int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		maxPerWin:  maxPerWindow,
		window:     window,
		timestamps: make([]time.Time, 0, maxPerWindow),
	}
}

func (r *rateLimiter) Wait() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Prune old timestamps
	valid := r.timestamps[:0]
	for _, ts := range r.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	r.timestamps = valid

	if len(r.timestamps) >= r.maxPerWin {
		// Wait until the oldest timestamp expires
		waitUntil := r.timestamps[0].Add(r.window)
		r.mu.Unlock()
		time.Sleep(time.Until(waitUntil))
		r.mu.Lock()
		// Re-prune after sleep
		now = time.Now()
		cutoff = now.Add(-r.window)
		valid = r.timestamps[:0]
		for _, ts := range r.timestamps {
			if ts.After(cutoff) {
				valid = append(valid, ts)
			}
		}
		r.timestamps = valid
	}

	r.timestamps = append(r.timestamps, time.Now())
}
