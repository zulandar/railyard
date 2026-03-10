package dashboard

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimitConfig holds rate limiting settings for the dashboard.
type RateLimitConfig struct {
	Enabled           bool
	RequestsPerMinute int
}

// bucket tracks token bucket state for a single client IP.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastTime time.Time
}

// rateLimiter returns Gin middleware that enforces per-IP rate limiting.
// Static asset paths (/static/) are excluded from rate limiting.
func rateLimiter(cfg RateLimitConfig) gin.HandlerFunc {
	if !cfg.Enabled {
		return func(c *gin.Context) { c.Next() }
	}

	var buckets sync.Map
	rpm := float64(cfg.RequestsPerMinute)
	refillRate := rpm / 60.0 // tokens per second

	// Cleanup goroutine: evict stale buckets every 5 minutes.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			stale := time.Now().Add(-10 * time.Minute)
			buckets.Range(func(key, value any) bool {
				b := value.(*bucket)
				b.mu.Lock()
				if b.lastTime.Before(stale) {
					buckets.Delete(key)
				}
				b.mu.Unlock()
				return true
			})
		}
	}()

	return func(c *gin.Context) {
		// Skip rate limiting for static assets.
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Next()
			return
		}

		ip := c.ClientIP()
		now := time.Now()

		val, _ := buckets.LoadOrStore(ip, &bucket{
			tokens:   rpm,
			lastTime: now,
		})
		b := val.(*bucket)

		b.mu.Lock()
		// Refill tokens based on elapsed time.
		elapsed := now.Sub(b.lastTime).Seconds()
		b.tokens += elapsed * refillRate
		if b.tokens > rpm {
			b.tokens = rpm
		}
		b.lastTime = now

		if b.tokens < 1 {
			b.mu.Unlock()
			retryAfter := int((1 - b.tokens) / refillRate)
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}

		b.tokens--
		b.mu.Unlock()
		c.Next()
	}
}
