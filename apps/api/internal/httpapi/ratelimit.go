package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type ratePolicy struct {
	Limit  int
	Window time.Duration
}

type rateEntry struct {
	Count    int
	ResetAt  time.Time
	LastSeen time.Time
}

type rateLimiter struct {
	mu            sync.Mutex
	entries       map[string]rateEntry
	maxEntries    int
	sweepInterval time.Duration
	nextSweep     time.Time
	now           func() time.Time
}

func newRateLimiter(maxEntries int, sweepInterval time.Duration, clock func() time.Time) *rateLimiter {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}
	if clock == nil {
		clock = time.Now
	}
	return &rateLimiter{
		entries: map[string]rateEntry{}, maxEntries: maxEntries,
		sweepInterval: sweepInterval, now: clock,
	}
}

func (limiter *rateLimiter) allow(key string, policy ratePolicy) (bool, time.Duration) {
	if policy.Limit <= 0 || policy.Window <= 0 {
		return true, 0
	}
	now := limiter.now().UTC()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.nextSweep.IsZero() || !now.Before(limiter.nextSweep) {
		for candidate, entry := range limiter.entries {
			if !now.Before(entry.ResetAt) {
				delete(limiter.entries, candidate)
			}
		}
		limiter.nextSweep = now.Add(limiter.sweepInterval)
	}
	entry, exists := limiter.entries[key]
	if exists && !now.Before(entry.ResetAt) {
		delete(limiter.entries, key)
		exists = false
	}
	if !exists {
		if len(limiter.entries) >= limiter.maxEntries {
			limiter.evictOldest()
		}
		limiter.entries[key] = rateEntry{Count: 1, ResetAt: now.Add(policy.Window), LastSeen: now}
		return true, 0
	}
	entry.LastSeen = now
	if entry.Count >= policy.Limit {
		limiter.entries[key] = entry
		retry := entry.ResetAt.Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	entry.Count++
	limiter.entries[key] = entry
	return true, 0
}

func (limiter *rateLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range limiter.entries {
		if oldestKey == "" || entry.LastSeen.Before(oldest) {
			oldestKey, oldest = key, entry.LastSeen
		}
	}
	if oldestKey != "" {
		delete(limiter.entries, oldestKey)
	}
}

func (s *Server) rateLimit(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		policy, exists := s.ratePolicies[name]
		if !exists {
			c.Next()
			return
		}
		key := rateLimitSubject(c) + "|" + name
		allowed, retry := s.limiter.allow(key, policy)
		if !allowed {
			retrySeconds := int(math.Ceil(retry.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(retrySeconds))
			writeError(c, http.StatusTooManyRequests, "rate_limit.exceeded", "Too many requests; retry later", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func rateLimitSubject(c *gin.Context) string {
	if user := currentUser(c); user.ID != "" {
		return "user:" + user.ID
	}
	return "ip:" + requestIP(c.Request)
}

func requestIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if request.RemoteAddr == "" {
		return "unknown"
	}
	return request.RemoteAddr
}
