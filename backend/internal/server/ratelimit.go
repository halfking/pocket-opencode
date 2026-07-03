package server

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter 是简单的滑动窗口速率限制器（per-IP）。
// Phase 1 实现：保护 LLM/Embed 等昂贵端点防滥用。
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64
	capacity float64
	ttl      time.Duration
}

type bucket struct {
	tokens   float64
	lastFill time.Time
}

func NewRateLimiter(rate float64, capacity float64) *RateLimiter {
	if rate <= 0 {
		rate = 1.0
	}
	if capacity <= 0 {
		capacity = 5.0
	}
	return &RateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     rate,
		capacity: capacity,
		ttl:      10 * time.Minute,
	}
}

func (r *RateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	b, ok := r.buckets[key]
	if !ok {
		r.buckets[key] = &bucket{tokens: r.capacity - 1, lastFill: now}
		return true
	}
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * r.rate
	if b.tokens > r.capacity {
		b.tokens = r.capacity
	}
	b.lastFill = now
	if elapsed > r.ttl.Seconds() {
		r.buckets[key] = &bucket{tokens: r.capacity - 1, lastFill: now}
		return true
	}
	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func (r *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := req.RemoteAddr
		if !r.allow(key) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, req)
	})
}

var llmRateLimiter = NewRateLimiter(1.0, 5.0)
var vaultRateLimiter = NewRateLimiter(2.0, 10.0)

func (s *Server) RateLimitLLM(next http.Handler) http.Handler {
	return llmRateLimiter.Middleware(next)
}

func (s *Server) RateLimitVault(next http.Handler) http.Handler {
	return vaultRateLimiter.Middleware(next)
}
