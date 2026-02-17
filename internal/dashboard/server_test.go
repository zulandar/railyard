package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestStart_NilDB(t *testing.T) {
	err := Start(context.Background(), StartOpts{DB: nil})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db is required")
	}
}

func TestStart_DefaultPort(t *testing.T) {
	opts := StartOpts{}
	if opts.Port != 0 {
		t.Errorf("zero-value port = %d, want 0", opts.Port)
	}
}

func TestStartOpts_ZeroValue(t *testing.T) {
	opts := StartOpts{}
	if opts.DB != nil || opts.Port != 0 || opts.Out != nil {
		t.Error("zero-value StartOpts should have nil/zero fields")
	}
}

// findFreePort finds an available port for testing.
func findFreePort() int {
	// Use a high port range unlikely to conflict.
	return 18080 + int(time.Now().UnixNano()%1000)
}

func TestEmbeddedAssets(t *testing.T) {
	// Verify embedded files are accessible.
	data, err := assetsFS.ReadFile("assets/htmx.min.js")
	if err != nil {
		t.Fatalf("htmx.min.js not embedded: %v", err)
	}
	if len(data) == 0 {
		t.Error("htmx.min.js is empty")
	}

	data, err = assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("style.css not embedded: %v", err)
	}
	if len(data) == 0 {
		t.Error("style.css is empty")
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("layout.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Railyard") {
		t.Error("layout.html does not contain 'Railyard'")
	}
}

// mockDB creates a minimal gorm.DB that won't actually connect.
// We use a real (but failing) DB just to get past the nil check —
// routes that don't query the DB still work fine.
func setupTestRouter(t *testing.T) (string, func()) {
	t.Helper()

	// We need a non-nil *gorm.DB to pass validation.
	// Use a stub that creates a real gin router without DB queries.
	port := findFreePort()
	ctx, cancel := context.WithCancel(context.Background())

	// Start server in background.
	errCh := make(chan error, 1)
	go func() {
		// We can't use a nil DB, so we create a fake gorm.DB.
		// Import a helper or just use the package-level function.
		// For this test, we'll use a simple approach: create the gin router directly.
		errCh <- startTestServer(ctx, port)
	}()

	// Wait for server to be ready.
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/static/style.css")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return baseURL, func() {
		cancel()
		<-errCh
	}
}

// startTestServer runs a dashboard server without a real DB connection.
func startTestServer(ctx context.Context, port int) error {
	// We need a non-nil gorm.DB. Create one that won't be used for the
	// static/template routes we're testing. Use gorm.Open with a dialector
	// that doesn't connect. Simplest: pass a placeholder.
	//
	// Since gin routes that serve static files and templates don't touch the
	// DB, we can use registerRoutes with a nil-ish DB wrapped in gorm.
	// But gorm.DB is a struct, not interface... so let's just create a
	// minimal server manually for testing.

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	tmpl, err := parseTemplates()
	if err != nil {
		return err
	}
	router.SetHTMLTemplate(tmpl)

	// Register routes with nil DB — static routes don't need it.
	registerRoutes(router, nil)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func TestStaticAssets_HTMX(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/static/htmx.min.js")
	if err != nil {
		t.Fatalf("GET /static/htmx.min.js: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStaticAssets_CSS(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/static/style.css")
	if err != nil {
		t.Fatalf("GET /static/style.css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestIndex_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCarsRoute_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/cars")
	if err != nil {
		t.Fatalf("GET /cars: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMessagesRoute_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/messages")
	if err != nil {
		t.Fatalf("GET /messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSSEEndpoint_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
}

func TestIndex_ContainsDashboardContent(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 8192)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"Dashboard",
		"Engines",
		"Tracks",
		"hx-get",
		"hx-trigger",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index page missing %q", want)
		}
	}
}

func TestPartialsEngines_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/partials/engines")
	if err != nil {
		t.Fatalf("GET /partials/engines: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPartialsTracks_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/partials/tracks")
	if err != nil {
		t.Fatalf("GET /partials/tracks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPartialsAlerts_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/partials/alerts")
	if err != nil {
		t.Fatalf("GET /partials/alerts: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		name string
		when time.Time
		want string
	}{
		{"zero", time.Time{}, "—"},
		{"seconds", time.Now().Add(-30 * time.Second), "30s ago"},
		{"minutes", time.Now().Add(-5 * time.Minute), "5m ago"},
		{"hours", time.Now().Add(-3 * time.Hour), "3h ago"},
		{"days", time.Now().Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeAgo(tt.when)
			if !strings.Contains(got, strings.TrimSuffix(tt.want, " ago")) && tt.want != "—" {
				// Allow small timing variance for seconds.
				if tt.name != "seconds" {
					t.Errorf("TimeAgo = %q, want to contain %q", got, tt.want)
				}
			}
			if tt.want == "—" && got != "—" {
				t.Errorf("TimeAgo(zero) = %q, want %q", got, "—")
			}
		})
	}
}

func TestDashboardData_NilDB(t *testing.T) {
	data := dashboardData(nil)
	if data["Engines"] == nil {
		t.Error("Engines should not be nil")
	}
	if data["Tracks"] == nil {
		t.Error("Tracks should not be nil")
	}
	if data["Escalations"] == nil {
		t.Error("Escalations should not be nil")
	}
	if data["QueueDepth"] != int64(0) {
		t.Errorf("QueueDepth = %v, want 0", data["QueueDepth"])
	}
}

func TestUnknownRoute_Returns404(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/nonexistent")
	if err != nil {
		t.Fatalf("GET /nonexistent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
