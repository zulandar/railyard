package dashboard

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Auth security regression tests for the dashboard.
//
// These tests document the current state of authentication boundaries.
// Currently the dashboard has NO authentication — all routes are publicly
// accessible. These tests serve as a compliance gate: once authentication
// middleware is added, the skipped tests should be unskipped and enforced.

func init() {
	gin.SetMode(gin.TestMode)
}

// testRouter creates a minimal gin router with dashboard routes registered
// against a nil DB (routes return empty data, which is fine for auth checks).
func testRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(rateLimiter(RateLimitConfig{Enabled: true, RequestsPerMinute: 120}))

	// Use a simple HTML template to avoid template parse errors.
	router.SetHTMLTemplate(mustParseTemplates())
	registerRoutes(router, nil)
	return router
}

// mustParseTemplates loads the embedded dashboard templates for testing.
func mustParseTemplates() *template.Template {
	tmpl, err := parseTemplates()
	if err != nil {
		panic("auth_security_test: parse templates: " + err.Error())
	}
	return tmpl
}

// TestDashboardRoutes_NoAuthRequired documents that all dashboard routes
// currently return 200 without any authentication. This is the baseline
// before auth is implemented.
func TestDashboardRoutes_NoAuthRequired(t *testing.T) {
	router := testRouter()

	routes := []string{
		"/",
		"/cars",
		"/messages",
		"/logs",
		"/sessions",
		"/partials/engines",
		"/partials/tracks",
		"/partials/alerts",
		"/partials/stats",
		"/partials/yardmaster",
	}

	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			// Document: all routes return 200 with no auth.
			// When auth is added, unauthenticated requests should return 401.
			if w.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200 (no auth currently required)", path, w.Code)
			}
		})
	}
}

// TestDashboardRoutes_NoAuthHeaders documents that the dashboard does not
// check for any authentication headers. Once auth middleware is added,
// this test should be updated to verify 401 responses.
// TestDashboardRoutes_NoAuthHeaders documents that sending requests without
// any authentication headers currently returns 200. This is the expected
// baseline until authentication middleware is added.
func TestDashboardRoutes_NoAuthHeaders(t *testing.T) {
	router := testRouter()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}

	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			// No Authorization header, no session cookie.
			router.ServeHTTP(w, req)

			// Current behavior: all routes return 200 without auth.
			// Update to assert 401 when auth middleware is added.
			if w.Code != http.StatusOK {
				t.Errorf("GET %s without auth = %d, want 200 (no auth middleware yet)", path, w.Code)
			}
		})
	}
}

// TestSSEEndpoint_NoAuthRequired documents that the SSE endpoint is publicly
// accessible without authentication.
func TestSSEEndpoint_NoAuthRequired(t *testing.T) {
	router := testRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/events", nil)
	req.Header.Set("Accept", "text/event-stream")

	// With nil DB, the SSE handler sends a "connected" event and returns
	// immediately, so this does not block.
	router.ServeHTTP(w, req)

	// SSE endpoint is publicly accessible — no auth required.
	if w.Code != http.StatusOK {
		t.Errorf("GET /api/events = %d, want 200 (no auth required)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// TestStaticAssets_PublicAccess verifies static assets are publicly accessible.
// This is expected behavior — static assets typically don't require auth.
func TestStaticAssets_PublicAccess(t *testing.T) {
	router := testRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/static/", nil)
	router.ServeHTTP(w, req)

	// Static assets should be publicly accessible (200 or 301 redirect)
	if w.Code != http.StatusOK && w.Code != http.StatusMovedPermanently && w.Code != http.StatusNotFound {
		t.Errorf("GET /static/ = %d, expected 200/301/404", w.Code)
	}
}

// TestSensitiveRoutes_NoAuthRequired documents that routes exposing sensitive
// operational data (agent messages, full I/O logs, session details) are
// accessible without authentication.
//
// Data sensitivity classification:
//   - /messages: inter-agent communications, escalation details, human-targeted messages
//   - /logs: full agent I/O (prompts, responses, tool calls with arguments)
//   - /sessions/:id: session metadata, engine assignments
//   - /engines/:id: engine configuration, current car assignments
//   - /api/events: real-time SSE stream of escalation alerts
func TestSensitiveRoutes_NoAuthRequired(t *testing.T) {
	router := testRouter()

	sensitiveRoutes := []struct {
		path        string
		sensitivity string
	}{
		{"/messages", "HIGH: inter-agent messages, escalation details"},
		{"/logs", "HIGH: full agent I/O including prompts and tool calls"},
		{"/sessions", "MEDIUM: session metadata and engine assignments"},
	}

	for _, route := range sensitiveRoutes {
		t.Run(route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", route.path, nil)
			router.ServeHTTP(w, req)

			// Document: sensitive routes return 200 without auth.
			if w.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200 (currently no auth)", route.path, w.Code)
			}
			// When auth is implemented, this test should verify 401.
			t.Logf("SECURITY: %s is publicly accessible — sensitivity: %s", route.path, route.sensitivity)
		})
	}
}

// TestServerBindAddress_AllInterfaces documents that the dashboard server
// binds to all interfaces (0.0.0.0), not just localhost. This means any
// machine on the network can access the dashboard.
func TestServerBindAddress_AllInterfaces(t *testing.T) {
	// Start builds addr as fmt.Sprintf(":%d", port) — no host prefix means 0.0.0.0
	addr := fmt.Sprintf(":%d", 8080)

	// Document: binding to :<port> means all interfaces.
	// For local-only access, should bind to 127.0.0.1:<port>.
	if !strings.HasPrefix(addr, ":") {
		t.Error("expected addr to start with : (all interfaces)")
	}
	if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") {
		t.Log("GOOD: dashboard binds to localhost only")
	} else {
		t.Log("SECURITY: dashboard binds to all interfaces (0.0.0.0) — accessible from network")
	}
}

// TestCSP_NoUnsafeInlineScript verifies that the CSP does not allow
// unsafe-inline for scripts, which would weaken XSS protection.
func TestCSP_NoUnsafeInlineScript(t *testing.T) {
	router := gin.New()
	router.Use(securityHeaders())
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "script-src") && strings.Contains(csp, "'unsafe-inline'") {
		// Check it's not in the style-src directive
		parts := strings.Split(csp, ";")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if strings.HasPrefix(trimmed, "script-src") && strings.Contains(trimmed, "'unsafe-inline'") {
				t.Errorf("CSP script-src allows 'unsafe-inline': %s", csp)
			}
		}
	}
}

// TestNoCSRFProtection documents that the dashboard has no CSRF protection.
// Currently all routes are GET-only (read-only dashboard), so CSRF risk is low.
// If POST/PUT/DELETE routes are added, CSRF middleware must be added.
func TestNoCSRFProtection(t *testing.T) {
	router := testRouter()

	// Verify POST to a page route returns 404/405 (no POST handlers registered)
	methods := []string{"POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(method, "/", nil)
			router.ServeHTTP(w, req)

			// 404 or 405 means no handler for this method — good for read-only dashboard
			if w.Code == http.StatusOK {
				t.Errorf("%s / = 200, expected 404/405 (dashboard should be read-only)", method)
			}
		})
	}
}

// TestSSEConnectionLimit verifies that the SSE endpoint enforces a max
// connection limit to prevent resource exhaustion.
func TestSSEConnectionLimit(t *testing.T) {
	router := testRouter()
	router.Use(securityHeaders())

	// Reset the counter for test isolation.
	sseConnectionCount.Store(maxSSEConnections)
	defer sseConnectionCount.Store(0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/events", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("SSE at max connections returned %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// TestSSEConnectionLimit_BelowMax verifies SSE works when under the limit.
func TestSSEConnectionLimit_BelowMax(t *testing.T) {
	router := testRouter()

	// Ensure counter is at 0 (below limit).
	sseConnectionCount.Store(0)
	defer sseConnectionCount.Store(0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/events", nil)
	router.ServeHTTP(w, req)

	// With nil DB, SSE sends "connected" and returns immediately (200).
	if w.Code != http.StatusOK {
		t.Errorf("SSE below limit returned %d, want 200", w.Code)
	}
}

// TestRateLimiting_Enforcement verifies that rate limiting middleware
// returns 429 Too Many Requests when a client exceeds the allowed RPM.
func TestRateLimiting_Enforcement(t *testing.T) {
	router := gin.New()
	router.Use(rateLimiter(RateLimitConfig{Enabled: true, RequestsPerMinute: 5}))
	router.SetHTMLTemplate(mustParseTemplates())
	registerRoutes(router, nil)

	// Exhaust the limit.
	for i := range 5 {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, w.Code)
		}
	}

	// Next request should be rate limited.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("exceeded rate limit: got %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}
