package ratelimit

import (
	"testing"
	"time"
)

func TestParseLimit(t *testing.T) {
	tests := []struct {
		input   string
		count   int
		window  time.Duration
		wantErr bool
	}{
		{"60/min", 60, time.Minute, false},
		{"1000/hour", 1000, time.Hour, false},
		{"10/s", 10, time.Second, false},
		{"100/30s", 100, 30 * time.Second, false},
		{"5/d", 5, 24 * time.Hour, false},
		{"500/500ms", 500, 500 * time.Millisecond, false},
		{"", 0, 0, true},
		{"abc/min", 0, 0, true},
		{"-1/min", 0, 0, true},
		{"0/min", 0, 0, true},
		{"10/fortnight", 0, 0, true},
		{"no-slash", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			l, err := ParseLimit(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseLimit(%q) = %v, want error", tt.input, l)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLimit(%q) error: %v", tt.input, err)
			}
			if l.Count != tt.count {
				t.Errorf("Count = %d, want %d", l.Count, tt.count)
			}
			if l.Window != tt.window {
				t.Errorf("Window = %v, want %v", l.Window, tt.window)
			}
		})
	}
}

func TestLimiter_AllowBasic(t *testing.T) {
	limit := Limit{Count: 3, Window: time.Minute}
	l := NewLimiter(limit)

	// First 3 should be allowed
	for i := 0; i < 3; i++ {
		r := l.Allow("user1")
		if !r.Allowed {
			t.Errorf("request %d: want allowed, got denied", i+1)
		}
		if r.Remaining != 2-i {
			t.Errorf("request %d: remaining = %d, want %d", i+1, r.Remaining, 2-i)
		}
	}

	// 4th should be denied
	r := l.Allow("user1")
	if r.Allowed {
		t.Error("4th request: want denied, got allowed")
	}
	if r.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive")
	}
	if r.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0", r.Remaining)
	}
}

func TestLimiter_SeparateKeys(t *testing.T) {
	limit := Limit{Count: 1, Window: time.Minute}
	l := NewLimiter(limit)

	r1 := l.Allow("user1")
	r2 := l.Allow("user2")

	if !r1.Allowed || !r2.Allowed {
		t.Error("different keys should have separate limits")
	}
}

func TestLimiter_WindowReset(t *testing.T) {
	limit := Limit{Count: 1, Window: time.Second}
	l := NewLimiter(limit)

	now := time.Now()
	l.now = func() time.Time { return now }

	r := l.Allow("key")
	if !r.Allowed {
		t.Fatal("first request should be allowed")
	}

	r = l.Allow("key")
	if r.Allowed {
		t.Fatal("second request should be denied")
	}

	// Advance past the window
	l.now = func() time.Time { return now.Add(2 * time.Second) }

	r = l.Allow("key")
	if !r.Allowed {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestLimiter_Cleanup(t *testing.T) {
	limit := Limit{Count: 1, Window: time.Second}
	l := NewLimiter(limit)

	now := time.Now()
	l.now = func() time.Time { return now }

	l.Allow("key1")
	l.Allow("key2")

	// Advance past the window
	l.now = func() time.Time { return now.Add(2 * time.Second) }
	l.Cleanup()

	l.mu.Lock()
	count := len(l.windows)
	l.mu.Unlock()

	if count != 0 {
		t.Errorf("after cleanup, windows = %d, want 0", count)
	}
}

func TestLimiter_RetryAfterAccuracy(t *testing.T) {
	limit := Limit{Count: 1, Window: 10 * time.Second}
	l := NewLimiter(limit)

	now := time.Now()
	l.now = func() time.Time { return now }

	l.Allow("key")

	// Advance 3 seconds
	l.now = func() time.Time { return now.Add(3 * time.Second) }

	r := l.Allow("key")
	if r.Allowed {
		t.Fatal("should be denied")
	}

	// RetryAfter should be ~7 seconds
	if r.RetryAfter < 6*time.Second || r.RetryAfter > 8*time.Second {
		t.Errorf("RetryAfter = %v, want ~7s", r.RetryAfter)
	}
}
