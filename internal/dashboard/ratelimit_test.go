package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// rateLimitedRouter creates a test router with rate limiting enabled.
func rateLimitedRouter(rpm int) *gin.Engine {
	router := gin.New()
	router.Use(rateLimiter(RateLimitConfig{Enabled: true, RequestsPerMinute: rpm}))
	router.GET("/", func(c *gin.Context) { c.String(200, "ok") })
	router.GET("/static/style.css", func(c *gin.Context) { c.String(200, "css") })
	return router
}

func TestRateLimiter_Returns429WhenExceeded(t *testing.T) {
	router := rateLimitedRouter(5)

	for i := range 5 {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, w.Code)
		}
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("request 6: got %d, want 429", w.Code)
	}

	if w.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on 429 response")
	}
}

func TestRateLimiter_DifferentIPsTrackedSeparately(t *testing.T) {
	router := rateLimitedRouter(2)

	for range 2 {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:1111"
		router.ServeHTTP(w, req)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.2:2222"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("different IP got %d, want 200", w.Code)
	}
}

func TestRateLimiter_Disabled(t *testing.T) {
	router := gin.New()
	router.Use(rateLimiter(RateLimitConfig{Enabled: false}))
	router.GET("/", func(c *gin.Context) { c.String(200, "ok") })

	for i := range 100 {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 (rate limiting disabled)", i, w.Code)
		}
	}
}

func TestRateLimiter_StaticAssetsExcluded(t *testing.T) {
	router := rateLimitedRouter(1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	router.ServeHTTP(w, req)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/static/style.css", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("static asset got %d, want 200 (should bypass rate limit)", w.Code)
	}
}

func TestRateLimitConfig_Defaults(t *testing.T) {
	cfg := RateLimitConfig{}
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
	if cfg.RequestsPerMinute != 0 {
		t.Errorf("default RPM = %d, want 0", cfg.RequestsPerMinute)
	}
}
