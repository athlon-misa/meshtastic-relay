// Package ratelimit provides per-key token bucket rate limiting for the relay daemon.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// bucket tracks token state for a single key.
type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// Limiter manages per-key token bucket rate limits.
type Limiter struct {
	mu              sync.Mutex
	buckets         map[string]*bucket
	rate            float64
	burst           int
	cleanupInterval time.Duration
}

// NewLimiter creates a new rate limiter with the given token refill rate
// (tokens per second) and burst size (maximum tokens).
func NewLimiter(rate float64, burst int) *Limiter {
	return &Limiter{
		buckets:         make(map[string]*bucket),
		rate:            rate,
		burst:           burst,
		cleanupInterval: 5 * time.Minute,
	}
}

// Allow reports whether the given key has tokens remaining. It refills tokens
// based on elapsed time since the last refill, caps at burst, and consumes
// one token if available.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{
			tokens:     float64(l.burst),
			lastRefill: now,
		}
		l.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.lastRefill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Cleanup removes buckets that have not been accessed in the last 10 minutes.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-10 * time.Minute)
	for key, b := range l.buckets {
		if b.lastRefill.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// StartCleanup launches a background goroutine that calls Cleanup at the
// configured cleanup interval. It stops when the context is canceled.
func (l *Limiter) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(l.cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				l.Cleanup()
			case <-ctx.Done():
				return
			}
		}
	}()
}
