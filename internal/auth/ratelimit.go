package auth

import (
	"sync"

	"golang.org/x/time/rate"
)

// PerIPLimiter is a generic per-IP token bucket used for rate limiting the
// login and passkey endpoints (see login.go). State is kept in memory only,
// so a restart clears all limits.
type PerIPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
}

// NewPerIPLimiter creates a limiter allowing `burst` requests immediately
// and refilling at `perMinute` requests/minute thereafter.
func NewPerIPLimiter(perMinute, burst int) *PerIPLimiter {
	return &PerIPLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        rate.Limit(float64(perMinute) / 60.0),
		burst:    burst,
	}
}

// Allow reports whether a request from ip is within the limit.
func (l *PerIPLimiter) Allow(ip string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.r, l.burst)
		l.limiters[ip] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}
