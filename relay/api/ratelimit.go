package api

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const (
	rateLimit  = 30
	rateWindow = time.Minute
)

type bucket struct {
	start time.Time
	n     int
}

// Limiter is a fixed-window rate limiter: 30 requests per minute per key.
// State lives in memory only; keys (which may derive from client IPs) are
// never persisted or logged.
type Limiter struct {
	now       func() time.Time
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastPrune time.Time
}

// NewLimiter returns a Limiter using now as its clock.
func NewLimiter(now func() time.Time) *Limiter {
	return &Limiter{now: now, buckets: make(map[string]*bucket)}
}

// current returns the live bucket for key, pruning stale buckets about once
// a window. The caller must hold l.mu.
func (l *Limiter) current(key string) *bucket {
	now := l.now()
	if now.Sub(l.lastPrune) >= rateWindow {
		for k, b := range l.buckets {
			if now.Sub(b.start) >= rateWindow {
				delete(l.buckets, k)
			}
		}
		l.lastPrune = now
	}
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= rateWindow {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	return b
}

// Allow reports whether a request for key is within the limit and counts it.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.current(key)
	if b.n >= rateLimit {
		return false
	}
	b.n++
	return true
}

// Blocked reports whether key has already recorded max hits in the window,
// without counting anything.
func (l *Limiter) Blocked(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current(key).n >= max
}

// Hit records one event for key.
func (l *Limiter) Hit(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current(key).n++
}

// keyHash turns a secret value into an opaque in-memory limiter key.
func keyHash(prefix, v string) string {
	sum := sha256.Sum256([]byte(v))
	return prefix + hex.EncodeToString(sum[:8])
}
