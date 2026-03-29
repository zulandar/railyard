package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// StartOpts holds configuration for the dashboard server.
type StartOpts struct {
	DB        *gorm.DB
	Port      int
	Out       io.Writer
	TLSCert   string          // path to TLS certificate file (optional)
	TLSKey    string          // path to TLS private key file (optional)
	RateLimit RateLimitConfig // per-IP rate limiting (optional)
}

// Start launches the dashboard HTTP server. It blocks until ctx is cancelled,
// then shuts down gracefully.
func Start(ctx context.Context, opts StartOpts) error {
	if opts.DB == nil {
		return fmt.Errorf("dashboard: db is required")
	}
	if opts.Port <= 0 {
		opts.Port = 8080
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(securityHeaders())
	router.Use(rateLimiter(opts.RateLimit))

	// Parse embedded templates.
	tmpl, err := parseTemplates()
	if err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	router.SetHTMLTemplate(tmpl)

	// Register routes.
	registerRoutes(router, opts.DB)

	addr := fmt.Sprintf(":%d", opts.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // generous for SSE long-poll
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// Graceful shutdown on context cancellation with a bounded timeout
	// so the server doesn't hang indefinitely on stuck connections.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	useTLS := opts.TLSCert != "" && opts.TLSKey != ""
	if opts.Out != nil {
		scheme := "http"
		if useTLS {
			scheme = "https"
		}
		fmt.Fprintf(opts.Out, "Dashboard running at %s://localhost:%d\n", scheme, opts.Port)
	}

	var listenErr error
	if useTLS {
		listenErr = srv.ListenAndServeTLS(opts.TLSCert, opts.TLSKey)
	} else {
		listenErr = srv.ListenAndServe()
	}
	if listenErr != nil && listenErr != http.ErrServerClosed {
		return fmt.Errorf("dashboard: %w", listenErr)
	}
	return nil
}

// templateFuncs returns the FuncMap used by dashboard templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"timeAgo":   TimeAgo,
		"truncate":  Truncate,
		"deref":     DerefTime,
		"commaFmt":  CommaFmt,
		"dollars":   Dollars,
		"hasPrefix": func(s, prefix string) bool { return strings.HasPrefix(s, prefix) },
		"percent": func(done, total int) int {
			if total == 0 {
				return 0
			}
			return int(float64(done) / float64(total) * 100)
		},
	}
}

// Truncate shortens a string to maxLen characters, appending "..." if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// DerefTime dereferences a *time.Time for use in templates.
func DerefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// parseTemplates loads the embedded HTML templates with custom functions.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return tmpl, nil
}

// securityHeaders returns middleware that sets standard security response headers.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		if c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		c.Next()
	}
}

// CommaFmt formats an int64 with comma separators (e.g. 45230 -> "45,230").
func CommaFmt(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + CommaFmt(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	remainder := len(s) % 3
	if remainder > 0 {
		result = append(result, s[:remainder]...)
	}
	for i := remainder; i < len(s); i += 3 {
		if len(result) > 0 {
			result = append(result, ',')
		}
		result = append(result, s[i:i+3]...)
	}
	return string(result)
}

// Dollars formats a float64 as a dollar amount (e.g. 1.5 -> "$1.50").
func Dollars(f float64) string {
	return fmt.Sprintf("$%.2f", f)
}

// TimeAgo formats a time as a human-readable relative duration wrapped in a
// <time> element with the absolute datetime as a title tooltip.
func TimeAgo(t time.Time) template.HTML {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	var relative string
	switch {
	case d < time.Minute:
		relative = fmt.Sprintf("%ds ago", int(math.Round(d.Seconds())))
	case d < time.Hour:
		relative = fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		relative = fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		relative = fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	absoluteStr := t.Format("2006-01-02 15:04:05 MST")
	return template.HTML(fmt.Sprintf("<time title=\"%s\">%s</time>", absoluteStr, relative))
}
