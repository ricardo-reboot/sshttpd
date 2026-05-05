// Package ratelimit implements per-tier token-bucket rate limiting for sshttpd.
//
// Each site has its own Limiter, configured from SiteConfig.Limits. The
// limiter tracks one bucket per tier (anonymous, identified, trusted) and
// refills tokens at the configured rate.
package ratelimit

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bugscave/sshttpd/internal/auth"
	"github.com/bugscave/sshttpd/internal/config"
)

// Limiter applies per-tier rate limits. A nil Limiter allows everything.
type Limiter struct {
	buckets map[string]*bucket
	mu      sync.Mutex
}

type bucket struct {
	rate     float64 // tokens per second; 0 means unlimited
	capacity float64
	tokens   float64
	last     time.Time
}

// New creates a Limiter from the configured per-tier limits. Strings are
// rate specifications: "60/min", "300/min", "1/sec", "unlimited", or empty
// for "no limit". Anything unparseable is treated as unlimited with a
// warning logged the first time the tier is used.
func New(cfg config.LimitsConfig) *Limiter {
	l := &Limiter{buckets: map[string]*bucket{}}
	l.buckets[auth.TierAnonymous] = parseBucket(cfg.Anonymous)
	l.buckets[auth.TierIdentified] = parseBucket(cfg.Identified)
	l.buckets[auth.TierTrusted] = parseBucket(cfg.Trusted)
	return l
}

// Allow consumes one token from the bucket for the given tier. Returns false
// when the bucket is empty (the caller should reject the request).
func (l *Limiter) Allow(tier string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[tier]
	if !ok || b == nil {
		return true
	}
	if b.rate == 0 {
		return true // unlimited
	}

	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// parseBucket parses strings like "60/min", "1/sec", "unlimited", "" into
// a token bucket. Empty or "unlimited" produce a bucket with rate=0
// (allowed to bypass the rate check).
func parseBucket(spec string) *bucket {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "unlimited" {
		return &bucket{rate: 0}
	}

	slash := strings.IndexByte(spec, '/')
	if slash < 0 {
		return &bucket{rate: 0}
	}

	count, err := strconv.Atoi(strings.TrimSpace(spec[:slash]))
	if err != nil || count <= 0 {
		return &bucket{rate: 0}
	}

	unit := strings.ToLower(strings.TrimSpace(spec[slash+1:]))
	var seconds float64
	switch unit {
	case "sec", "second", "s":
		seconds = 1
	case "min", "minute", "m":
		seconds = 60
	case "hour", "h":
		seconds = 3600
	case "day", "d":
		seconds = 86400
	default:
		return &bucket{rate: 0}
	}

	rate := float64(count) / seconds
	return &bucket{
		rate:     rate,
		capacity: float64(count),
		tokens:   float64(count),
		last:     time.Now(),
	}
}
