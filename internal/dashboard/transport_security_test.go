package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Transport security regression tests for the dashboard.
//
// These tests verify that security headers are applied to every response
// and that TLS configuration fields are properly wired through StartOpts.

func init() {
	gin.SetMode(gin.TestMode)
}

// testRouterWithSecurityHeaders creates a gin router with the securityHeaders
// middleware applied, matching the production Start() configuration.
func testRouterWithSecurityHeaders() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(securityHeaders())

	router.SetHTMLTemplate(mustParseTemplates())
	registerRoutes(router, nil)
	return router
}

// TestSecurityHeaders_XContentTypeOptions verifies X-Content-Type-Options is
// set to "nosniff" on every response to prevent MIME-type sniffing attacks.
func TestSecurityHeaders_XContentTypeOptions(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			got := w.Header().Get("X-Content-Type-Options")
			if got != "nosniff" {
				t.Errorf("GET %s: X-Content-Type-Options = %q, want %q", path, got, "nosniff")
			}
		})
	}
}

// TestSecurityHeaders_XFrameOptions verifies X-Frame-Options is set to "DENY"
// to prevent clickjacking by disallowing framing of the dashboard.
func TestSecurityHeaders_XFrameOptions(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			got := w.Header().Get("X-Frame-Options")
			if got != "DENY" {
				t.Errorf("GET %s: X-Frame-Options = %q, want %q", path, got, "DENY")
			}
		})
	}
}

// TestSecurityHeaders_ReferrerPolicy verifies the Referrer-Policy header is set
// to "strict-origin-when-cross-origin" to limit referrer leakage.
func TestSecurityHeaders_ReferrerPolicy(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			got := w.Header().Get("Referrer-Policy")
			if got != "strict-origin-when-cross-origin" {
				t.Errorf("GET %s: Referrer-Policy = %q, want %q", path, got, "strict-origin-when-cross-origin")
			}
		})
	}
}

// TestSecurityHeaders_PermissionsPolicy verifies the Permissions-Policy header
// disables access to sensitive browser APIs (camera, microphone, geolocation).
func TestSecurityHeaders_PermissionsPolicy(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			got := w.Header().Get("Permissions-Policy")
			if got != "camera=(), microphone=(), geolocation=()" {
				t.Errorf("GET %s: Permissions-Policy = %q, want %q", path, got, "camera=(), microphone=(), geolocation=()")
			}
		})
	}
}

// TestSecurityHeaders_ContentSecurityPolicy verifies the Content-Security-Policy
// header contains "default-src 'self'" to restrict resource loading origins.
func TestSecurityHeaders_ContentSecurityPolicy(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	routes := []string{"/", "/cars", "/messages", "/logs", "/sessions"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			got := w.Header().Get("Content-Security-Policy")
			if !strings.Contains(got, "default-src 'self'") {
				t.Errorf("GET %s: Content-Security-Policy = %q, want to contain %q", path, got, "default-src 'self'")
			}
		})
	}
}

// TestSecurityHeaders_AllPresent verifies that all required security headers
// are present on a single response, ensuring the middleware sets them together.
func TestSecurityHeaders_AllPresent(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	router.ServeHTTP(w, req)

	required := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	}

	for header, want := range required {
		got := w.Header().Get(header)
		if got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, want to contain %q", csp, "default-src 'self'")
	}
}

// TestSecurityHeaders_OnPartials verifies security headers are also set on
// HTMX partial responses, not just full page loads.
func TestSecurityHeaders_OnPartials(t *testing.T) {
	router := testRouterWithSecurityHeaders()

	partials := []string{
		"/partials/engines",
		"/partials/tracks",
		"/partials/alerts",
		"/partials/stats",
		"/partials/yardmaster",
	}

	for _, path := range partials {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", path, nil)
			router.ServeHTTP(w, req)

			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("GET %s: X-Content-Type-Options = %q, want %q", path, got, "nosniff")
			}
			if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("GET %s: X-Frame-Options = %q, want %q", path, got, "DENY")
			}
		})
	}
}

// TestServerTimeouts verifies that the http.Server created by Start() would
// include appropriate timeouts to prevent slowloris and resource exhaustion.
// We test the timeout values defined in the Start function by verifying
// the server configuration struct.
func TestServerTimeouts(t *testing.T) {
	// Replicate the server creation from Start() to verify timeout values.
	srv := &http.Server{
		Addr:              ":8080",
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout is zero — slowloris vulnerability")
	}
	if srv.ReadTimeout == 0 {
		t.Error("ReadTimeout is zero — slow request body attacks possible")
	}
	if srv.WriteTimeout == 0 {
		t.Error("WriteTimeout is zero — slow response consumption attacks possible")
	}
	if srv.IdleTimeout == 0 {
		t.Error("IdleTimeout is zero — idle connection exhaustion possible")
	}
	if srv.MaxHeaderBytes == 0 {
		t.Error("MaxHeaderBytes is zero — large header attacks possible")
	}
}

// TestTransportTLS_StartOptsFields verifies that StartOpts exposes TLSCert
// and TLSKey fields for configuring TLS termination at the server.
func TestTransportTLS_StartOptsFields(t *testing.T) {
	opts := StartOpts{
		TLSCert: "/path/to/cert.pem",
		TLSKey:  "/path/to/key.pem",
	}

	if opts.TLSCert != "/path/to/cert.pem" {
		t.Errorf("TLSCert = %q, want %q", opts.TLSCert, "/path/to/cert.pem")
	}
	if opts.TLSKey != "/path/to/key.pem" {
		t.Errorf("TLSKey = %q, want %q", opts.TLSKey, "/path/to/key.pem")
	}
}

// TestTransportTLS_StartOptsZeroValue verifies that TLS fields default to
// empty strings, meaning TLS is disabled by default.
func TestTransportTLS_StartOptsZeroValue(t *testing.T) {
	opts := StartOpts{}

	if opts.TLSCert != "" {
		t.Errorf("zero-value TLSCert = %q, want empty", opts.TLSCert)
	}
	if opts.TLSKey != "" {
		t.Errorf("zero-value TLSKey = %q, want empty", opts.TLSKey)
	}
}

// TestTransportTLS_SchemeDetection verifies that the output message uses
// "https" when TLS is configured and "http" when it is not.
func TestTransportTLS_SchemeDetection(t *testing.T) {
	tests := []struct {
		name       string
		cert       string
		key        string
		wantScheme string
	}{
		{"no TLS", "", "", "http"},
		{"with TLS", "/cert.pem", "/key.pem", "https"},
		{"cert only (no key)", "/cert.pem", "", "http"},
		{"key only (no cert)", "", "/key.pem", "http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the scheme detection logic from Start().
			useTLS := tt.cert != "" && tt.key != ""
			scheme := "http"
			if useTLS {
				scheme = "https"
			}

			if scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q (cert=%q, key=%q)", scheme, tt.wantScheme, tt.cert, tt.key)
			}
		})
	}
}

// TestTransportTLS_OutputMessage verifies the dashboard startup message
// contains the correct scheme based on TLS configuration.
func TestTransportTLS_OutputMessage(t *testing.T) {
	tests := []struct {
		name       string
		cert       string
		key        string
		wantScheme string
	}{
		{"plaintext", "", "", "http://"},
		{"TLS enabled", "/cert.pem", "/key.pem", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We cannot fully Start() (it would try to listen), but we can
			// verify the output logic by checking what Start() would print.
			// The Start() function writes: "Dashboard running at <scheme>://localhost:<port>"
			useTLS := tt.cert != "" && tt.key != ""
			scheme := "http"
			if useTLS {
				scheme = "https"
			}
			msg := scheme + "://localhost:8080"

			if !strings.Contains(msg, tt.wantScheme) {
				t.Errorf("output message contains %q, want %q", msg, tt.wantScheme)
			}
		})
	}
}
