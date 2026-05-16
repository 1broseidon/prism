package adminauth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Tight, single-IP-aware token bucket on the login path. Real-world abuse
// here is bounded (a single OIDC redirect costs almost nothing) but burning
// the KV with login_attempt entries is a cheap DoS — this is the cap.
const (
	loginRateBucketSize = 20              // burst capacity per IP
	loginRateRefill     = 1 * time.Second // tokens regenerate at 1/sec
	loginRateIPMaxIdle  = 1 * time.Hour   // when to forget an IP bucket
)

type loginBucket struct {
	tokens  float64
	updated time.Time
}

// loginRateLimiter is a tiny per-IP token-bucket limiter wired into the
// admin /auth/login handler. Zero value is usable; goroutine-safe.
type loginRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{buckets: make(map[string]*loginBucket)}
}

// allow consumes one token for ip and reports whether the request may
// proceed. Returns true also for unknown / unparseable IPs (we'd rather
// over-allow than block legit traffic — the worst case is the KV bloat
// problem the sweeper already mitigates).
func (l *loginRateLimiter) allow(ip string) bool {
	if l == nil || ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &loginBucket{tokens: float64(loginRateBucketSize) - 1, updated: now}
		l.maybeGC(now)
		return true
	}
	// Refill based on elapsed time.
	elapsed := now.Sub(b.updated).Seconds()
	b.tokens += elapsed / loginRateRefill.Seconds()
	if b.tokens > float64(loginRateBucketSize) {
		b.tokens = float64(loginRateBucketSize)
	}
	b.updated = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// maybeGC drops idle buckets so the map doesn't grow without bound. Called
// under the lock, only when a new bucket is added — cheap amortized cost.
func (l *loginRateLimiter) maybeGC(now time.Time) {
	if len(l.buckets) < 1024 {
		return
	}
	for ip, b := range l.buckets {
		if now.Sub(b.updated) > loginRateIPMaxIdle {
			delete(l.buckets, ip)
		}
	}
}

// clientIP returns the best-effort source IP for rate-limit keying.
// We do NOT honor X-Forwarded-For here — the rate limit is per inbound
// socket so a malicious client can't pivot through forged headers to evade
// it. Operators terminating TLS at a proxy will see all login traffic from
// one IP; that's still a reasonable cap.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
