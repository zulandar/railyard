package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// StartOpts holds configuration for the dashboard server.
type StartOpts struct {
	DB   *gorm.DB
	Port int
	Out  io.Writer
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
		Addr:    addr,
		Handler: router,
	}

	// Graceful shutdown on context cancellation.
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	if opts.Out != nil {
		fmt.Fprintf(opts.Out, "Dashboard running at http://localhost:%d\n", opts.Port)
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// templateFuncs returns the FuncMap used by dashboard templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"timeAgo": TimeAgo,
	}
}

// parseTemplates loads the embedded HTML templates with custom functions.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return tmpl, nil
}

// TimeAgo formats a time as a human-readable relative duration.
func TimeAgo(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(math.Round(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
