package ratelimit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limit represents a rate limit: count requests per window duration.
type Limit struct {
	Count  int
	Window time.Duration
}

// ParseLimit parses strings like "60/min", "1000/hour", "10/s", "100/30s".
func ParseLimit(s string) (Limit, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Limit{}, fmt.Errorf("empty rate limit")
	}

	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return Limit{}, fmt.Errorf("invalid rate limit %q: expected format 'count/window' (e.g. 60/min)", s)
	}

	count, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || count <= 0 {
		return Limit{}, fmt.Errorf("invalid rate limit %q: count must be a positive integer", s)
	}

	window, err := parseWindow(strings.TrimSpace(parts[1]))
	if err != nil {
		return Limit{}, fmt.Errorf("invalid rate limit %q: %w", s, err)
	}

	return Limit{Count: count, Window: window}, nil
}

var durationPattern = regexp.MustCompile(`^(\d+)(s|ms)$`)

func parseWindow(s string) (time.Duration, error) {
	switch s {
	case "s", "sec", "second":
		return time.Second, nil
	case "min", "minute":
		return time.Minute, nil
	case "h", "hour":
		return time.Hour, nil
	case "d", "day":
		return 24 * time.Hour, nil
	}

	// Try numeric durations like "30s", "500ms"
	if m := durationPattern.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "s":
			return time.Duration(n) * time.Second, nil
		case "ms":
			return time.Duration(n) * time.Millisecond, nil
		}
	}

	return 0, fmt.Errorf("unknown window %q (use: s, min, h, d, or Ns/Nms)", s)
}

// Limiter tracks per-key rate limits using a sliding window counter.
type Limiter struct {
	mu      sync.Mutex
	limit   Limit
	windows map[string]*window
	now     func() time.Time // injectable for testing
}

type window struct {
	count   int
	resetAt time.Time
}

// NewLimiter creates a Limiter for the given limit.
func NewLimiter(limit Limit) *Limiter {
	return &Limiter{
		limit:   limit,
		windows: make(map[string]*window),
		now:     time.Now,
	}
}

// Result is returned by Allow().
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration // >0 when not allowed
}

// Allow checks if a request from the given key is allowed.
// If allowed, it consumes one token from the bucket.
func (l *Limiter) Allow(key string) Result {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	w, ok := l.windows[key]
	if !ok || now.After(w.resetAt) {
		// New window
		w = &window{
			count:   0,
			resetAt: now.Add(l.limit.Window),
		}
		l.windows[key] = w
	}

	if w.count >= l.limit.Count {
		return Result{
			Allowed:    false,
			Limit:      l.limit.Count,
			Remaining:  0,
			RetryAfter: w.resetAt.Sub(now),
		}
	}

	w.count++
	return Result{
		Allowed:   true,
		Limit:     l.limit.Count,
		Remaining: l.limit.Count - w.count,
	}
}

// Cleanup removes expired windows. Call periodically to prevent memory leaks.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	for key, w := range l.windows {
		if now.After(w.resetAt) {
			delete(l.windows, key)
		}
	}
}
