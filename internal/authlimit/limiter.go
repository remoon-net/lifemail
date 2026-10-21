// Package authlimit throttles repeated authentication failures so that
// password guessing cannot keep expensive bcrypt comparisons running.
package authlimit

import (
	"sync"
	"time"
)

// Limiter counts failures per key inside a sliding window and locks the key
// once the budget is exhausted. The zero value is not usable; call New.
type Limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	lockout time.Duration
	now     func() time.Time
	entries map[string]*entry
}

type entry struct {
	failures int
	first    time.Time
	lockedTo time.Time
}

// New returns a limiter that locks a key for lockout after max failures inside
// window.
func New(max int, window, lockout time.Duration) *Limiter {
	if max <= 0 {
		max = 5
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	if lockout <= 0 {
		lockout = 5 * time.Minute
	}
	return &Limiter{
		max:     max,
		window:  window,
		lockout: lockout,
		now:     time.Now,
		entries: make(map[string]*entry),
	}
}

// Locked reports whether key is currently blocked, and for how much longer.
func (l *Limiter) Locked(key string) (bool, time.Duration) {
	if l == nil || key == "" {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return false, 0
	}
	now := l.now()
	if now.Before(e.lockedTo) {
		return true, e.lockedTo.Sub(now)
	}
	if e.lockedTo.IsZero() && now.Sub(e.first) > l.window {
		delete(l.entries, key)
	}
	return false, 0
}

// Fail records a failure for key and reports whether it just became locked.
func (l *Limiter) Fail(key string) bool {
	if l == nil || key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.entries) > 4096 {
		l.pruneLocked(now)
	}
	e := l.entries[key]
	if e == nil || (!e.lockedTo.IsZero() && !now.Before(e.lockedTo)) || now.Sub(e.first) > l.window {
		e = &entry{first: now}
		l.entries[key] = e
	}
	e.failures++
	if e.failures >= l.max {
		e.lockedTo = now.Add(l.lockout)
		e.failures = 0
		e.first = now
		return true
	}
	return false
}

// Reset clears the failure history for key after a successful login.
func (l *Limiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *Limiter) pruneLocked(now time.Time) {
	for key, e := range l.entries {
		if now.Before(e.lockedTo) {
			continue
		}
		if now.Sub(e.first) > l.window {
			delete(l.entries, key)
		}
	}
}
