package admin

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	adminProbeRateBucketSize = 6
	adminProbeRateRefill     = 10 * time.Second
	adminProbeRateIPMaxIdle  = 1 * time.Hour
)

type adminProbeBucket struct {
	tokens  float64
	updated time.Time
}

type adminProbeRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*adminProbeBucket
}

func newAdminProbeRateLimiter() *adminProbeRateLimiter {
	return &adminProbeRateLimiter{buckets: make(map[string]*adminProbeBucket)}
}

func (l *adminProbeRateLimiter) allow(r *http.Request) bool {
	if l == nil {
		return true
	}
	ip := probeClientIP(r)
	if ip == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &adminProbeBucket{tokens: float64(adminProbeRateBucketSize) - 1, updated: now}
		l.maybeGC(now)
		return true
	}

	elapsed := now.Sub(b.updated).Seconds()
	b.tokens += elapsed / adminProbeRateRefill.Seconds()
	if b.tokens > float64(adminProbeRateBucketSize) {
		b.tokens = float64(adminProbeRateBucketSize)
	}
	b.updated = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *adminProbeRateLimiter) maybeGC(now time.Time) {
	if len(l.buckets) < 1024 {
		return
	}
	for ip, b := range l.buckets {
		if now.Sub(b.updated) > adminProbeRateIPMaxIdle {
			delete(l.buckets, ip)
		}
	}
}

func probeClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
