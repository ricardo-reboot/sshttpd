package ratelimit

import (
	"testing"

	"github.com/bugscave/sshttpd/internal/auth"
	"github.com/bugscave/sshttpd/internal/config"
)

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter
	for i := 0; i < 1000; i++ {
		if !l.Allow(auth.TierAnonymous) {
			t.Fatalf("nil limiter should always allow")
		}
	}
}

func TestUnlimitedTier(t *testing.T) {
	l := New(config.LimitsConfig{
		Anonymous: "unlimited",
		Trusted:   "",
	})
	for i := 0; i < 1000; i++ {
		if !l.Allow(auth.TierAnonymous) {
			t.Fatalf("unlimited tier should allow at iteration %d", i)
		}
		if !l.Allow(auth.TierTrusted) {
			t.Fatalf("empty (unlimited) tier should allow at iteration %d", i)
		}
	}
}

func TestRateLimitedTier(t *testing.T) {
	l := New(config.LimitsConfig{
		Anonymous: "5/min",
	})
	allowed := 0
	for i := 0; i < 100; i++ {
		if l.Allow(auth.TierAnonymous) {
			allowed++
		}
	}
	// Token bucket fills the capacity initially (5), then refill is per-second so
	// the test should see roughly 5 allowed in this tight loop.
	if allowed > 6 || allowed < 4 {
		t.Errorf("expected ~5 allowed for 5/min in tight loop, got %d", allowed)
	}
}

func TestParseBucket(t *testing.T) {
	cases := []struct {
		spec     string
		wantRate float64
	}{
		{"", 0},
		{"unlimited", 0},
		{"60/min", 1.0},
		{"1/sec", 1.0},
		{"3600/hour", 1.0},
		{"86400/day", 1.0},
		{"junk", 0},
		{"60/garbage", 0},
		{"-1/min", 0},
		{"0/min", 0},
	}
	for _, c := range cases {
		b := parseBucket(c.spec)
		if b == nil {
			t.Errorf("parseBucket(%q) returned nil", c.spec)
			continue
		}
		if b.rate != c.wantRate {
			t.Errorf("parseBucket(%q) rate = %v, want %v", c.spec, b.rate, c.wantRate)
		}
	}
}

func TestUnknownTierIsAllowed(t *testing.T) {
	l := New(config.LimitsConfig{Anonymous: "1/min"})
	if !l.Allow("ghost") {
		t.Errorf("unknown tier should be allowed (no bucket)")
	}
}
