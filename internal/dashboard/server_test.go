package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestStart_ShutdownWithTimeout(t *testing.T) {
	// Verify that server shutdown uses a bounded context, not context.Background().
	// We test this by cancelling the context and checking that Start returns
	// promptly (within a few seconds), not hanging indefinitely.
	ctx, cancel := context.WithCancel(context.Background())

	port := findFreePort()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	router.SetHTMLTemplate(tmpl)
	registerRoutes(router, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Start(ctx, StartOpts{
			DB:   &gorm.DB{}, // non-nil to pass validation
			Port: port,
		})
	}()

	// Wait for server to be listening.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/static/style.css", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cancel context — should trigger shutdown.
	cancel()

	// Start should return within a reasonable time (the shutdown timeout).
	select {
	case <-errCh:
		// Good — shut down promptly.
	case <-time.After(20 * time.Second):
		t.Fatal("Start did not return within 20s after context cancellation — shutdown may be hanging")
	}
}

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

func TestStartOpts_RateLimitField(t *testing.T) {
	opts := StartOpts{
		RateLimit: RateLimitConfig{Enabled: true, RequestsPerMinute: 60},
	}
	if !opts.RateLimit.Enabled {
		t.Error("RateLimit.Enabled should be true")
	}
	if opts.RateLimit.RequestsPerMinute != 60 {
		t.Errorf("RPM = %d, want 60", opts.RateLimit.RequestsPerMinute)
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
	router.Use(securityHeaders())

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
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

func TestCarsRoute_ContainsCarTable(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/cars")
	if err != nil {
		t.Fatalf("GET /cars: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"Cars",
		"No cars found",
		"Track:",
		"Status:",
		"Type:",
		"Showing 0 car(s)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("cars page missing %q", want)
		}
	}
}

func TestCarsRoute_WithFilters(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	// Filters should not break the page even with nil DB.
	resp, err := http.Get(baseURL + "/cars?track=backend&status=open&type=task")
	if err != nil {
		t.Fatalf("GET /cars?filters: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCarDetail_NotFound(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/cars/nonexistent-id")
	if err != nil {
		t.Fatalf("GET /cars/nonexistent-id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCarList_NilDB(t *testing.T) {
	result := CarList(nil, "", "", "", "")
	if result.Cars == nil {
		t.Error("Cars should not be nil")
	}
	if len(result.Cars) != 0 {
		t.Errorf("Cars = %d, want 0", len(result.Cars))
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hi", 2, "hi"},
		{"abcdef", 3, "abc"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := Truncate(tt.s, tt.maxLen)
		if got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
		}
	}
}

func TestDerefTime(t *testing.T) {
	now := time.Now()
	if got := DerefTime(&now); !got.Equal(now) {
		t.Errorf("DerefTime(&now) = %v, want %v", got, now)
	}
	if got := DerefTime(nil); !got.IsZero() {
		t.Errorf("DerefTime(nil) = %v, want zero", got)
	}
}

func TestEmbeddedTemplates_CarsAndDetail(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/cars.html")
	if err != nil {
		t.Fatalf("cars.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Showing") {
		t.Error("cars.html does not contain 'Showing'")
	}

	data, err = templatesFS.ReadFile("templates/car_detail.html")
	if err != nil {
		t.Fatalf("car_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Metadata") {
		t.Error("car_detail.html does not contain 'Metadata'")
	}

	data, err = templatesFS.ReadFile("templates/partials.html")
	if err != nil {
		t.Fatalf("partials.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "page_nav") {
		t.Error("partials.html does not contain 'page_nav'")
	}
}

func TestEngineDetail_NotFound(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/engines/nonexistent-id")
	if err != nil {
		t.Fatalf("GET /engines/nonexistent-id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetEngineActivity_NilDB(t *testing.T) {
	activity := GetEngineActivity(nil, "test-engine")
	if activity == nil {
		t.Error("activity should not be nil")
	}
	if len(activity) != 0 {
		t.Errorf("activity = %d, want 0", len(activity))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{25 * time.Hour, "1d 1h"},
		{48*time.Hour + 30*time.Minute, "2d 0h"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestEmbeddedTemplates_EngineDetail(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/engine_detail.html")
	if err != nil {
		t.Fatalf("engine_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Current Car") {
		t.Error("engine_detail.html does not contain 'Current Car'")
	}
}

func TestMessagesRoute_ContainsMessageTable(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/messages")
	if err != nil {
		t.Fatalf("GET /messages: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"Messages",
		"No messages found",
		"Agent:",
		"Priority:",
		"Showing 0 message(s)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("messages page missing %q", want)
		}
	}
}

func TestMessagesRoute_WithFilters(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/messages?agent=yardmaster&priority=urgent&unacked=true")
	if err != nil {
		t.Fatalf("GET /messages?filters: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestListMessages_NilDB(t *testing.T) {
	result := ListMessages(nil, MessageFilters{})
	if result.Messages == nil {
		t.Error("Messages should not be nil")
	}
	if len(result.Messages) != 0 {
		t.Errorf("Messages = %d, want 0", len(result.Messages))
	}
}

func TestPendingEscalationCount_NilDB(t *testing.T) {
	count := PendingEscalationCount(nil)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestEmbeddedTemplates_Messages(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/messages.html")
	if err != nil {
		t.Fatalf("messages.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Escalations") {
		t.Error("messages.html does not contain 'Escalations'")
	}
}

func TestSSEEndpoint_SendsConnectedEvent(t *testing.T) {
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

	// Read the connected event.
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "event: connected") {
		t.Errorf("response missing 'event: connected', got: %q", content)
	}
	if !strings.Contains(content, "\"type\":\"connected\"") {
		t.Errorf("response missing connected data, got: %q", content)
	}
}

func TestSSEWriteEvent(t *testing.T) {
	var buf strings.Builder
	writeSSE(&buf, "test-event", map[string]string{"key": "value"})
	got := buf.String()

	if !strings.Contains(got, "event: test-event\n") {
		t.Errorf("missing event line in: %q", got)
	}
	if !strings.Contains(got, "data: ") {
		t.Errorf("missing data line in: %q", got)
	}
	if !strings.Contains(got, "\"key\":\"value\"") {
		t.Errorf("missing JSON data in: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("should end with double newline: %q", got)
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

func TestCompletedToday_NilDB(t *testing.T) {
	count := CompletedToday(nil)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestTotalTokenUsage_NilDB(t *testing.T) {
	count := TotalTokenUsage(nil)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestComputeStats_NilDB(t *testing.T) {
	stats := ComputeStats(nil, nil, nil)
	if stats.ActiveEngines != 0 {
		t.Errorf("ActiveEngines = %d, want 0", stats.ActiveEngines)
	}
	if stats.OpenCars != 0 {
		t.Errorf("OpenCars = %d, want 0", stats.OpenCars)
	}
	if stats.InProgressCars != 0 {
		t.Errorf("InProgressCars = %d, want 0", stats.InProgressCars)
	}
	if stats.BlockedCars != 0 {
		t.Errorf("BlockedCars = %d, want 0", stats.BlockedCars)
	}
	if stats.CompletedToday != 0 {
		t.Errorf("CompletedToday = %d, want 0", stats.CompletedToday)
	}
	if stats.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", stats.TotalTokens)
	}
}

func TestComputeStats_WithData(t *testing.T) {
	engines := []EngineRow{
		{ID: "e1", Status: "idle"},
		{ID: "e2", Status: "working"},
		{ID: "e3", Status: "dead"},
		{ID: "e4", Status: "stopped"},
	}
	tracks := []TrackStatusCount{
		{Track: "backend", Open: 3, InProgress: 2, Blocked: 1},
		{Track: "frontend", Open: 1, InProgress: 4, Blocked: 0},
	}
	stats := ComputeStats(engines, tracks, nil)
	if stats.ActiveEngines != 2 {
		t.Errorf("ActiveEngines = %d, want 2", stats.ActiveEngines)
	}
	if stats.OpenCars != 4 {
		t.Errorf("OpenCars = %d, want 4", stats.OpenCars)
	}
	if stats.InProgressCars != 6 {
		t.Errorf("InProgressCars = %d, want 6", stats.InProgressCars)
	}
	if stats.BlockedCars != 1 {
		t.Errorf("BlockedCars = %d, want 1", stats.BlockedCars)
	}
}

func TestDashboardData_ContainsStats(t *testing.T) {
	data := dashboardData(nil)
	if data["Stats"] == nil {
		t.Error("Stats should not be nil")
	}
	stats, ok := data["Stats"].(DashboardStats)
	if !ok {
		t.Fatalf("Stats is not DashboardStats: %T", data["Stats"])
	}
	if stats.ActiveEngines != 0 {
		t.Errorf("ActiveEngines = %d, want 0", stats.ActiveEngines)
	}
}

func TestPartialsStats_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/partials/stats")
	if err != nil {
		t.Fatalf("GET /partials/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestIndex_ContainsStatsBar(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"stats-bar",
		"/partials/stats",
		"Active Engines",
		"Open Cars",
		"In Progress",
		"Blocked",
		"Completed Today",
		"Total Tokens",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index page missing %q", want)
		}
	}
}

func TestAgentLogList_NilDB(t *testing.T) {
	result := AgentLogList(nil, AgentLogFilters{})
	if result.Logs == nil {
		t.Error("Logs should not be nil")
	}
	if len(result.Logs) != 0 {
		t.Errorf("Logs = %d, want 0", len(result.Logs))
	}
}

func TestTokenUsageSummary_NilDB(t *testing.T) {
	result := TokenUsageSummary(nil)
	if result.ByEngine == nil {
		t.Error("ByEngine should not be nil")
	}
	if len(result.ByEngine) != 0 {
		t.Errorf("ByEngine = %d, want 0", len(result.ByEngine))
	}
	if result.TotalInput != 0 {
		t.Errorf("TotalInput = %d, want 0", result.TotalInput)
	}
	if result.TotalOutput != 0 {
		t.Errorf("TotalOutput = %d, want 0", result.TotalOutput)
	}
	if result.TotalAll != 0 {
		t.Errorf("TotalAll = %d, want 0", result.TotalAll)
	}
}

func TestLogsRoute_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestLogsRoute_ContainsContent(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"Logs",
		"Token Usage",
		"Input Tokens",
		"Output Tokens",
		"Total Tokens",
		"Log Entries",
		"Engine:",
		"Car:",
		"Direction:",
		"Showing 0 log(s)",
		"No logs found",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("logs page missing %q", want)
		}
	}
}

func TestLogsRoute_WithFilters(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/logs?engine=test&car=car-1&direction=out")
	if err != nil {
		t.Fatalf("GET /logs?filters: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestNavContainsLogsLink(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	if !strings.Contains(html, `href="/logs"`) {
		t.Error("nav missing /logs link")
	}
}

func TestEmbeddedTemplates_Logs(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/logs.html")
	if err != nil {
		t.Fatalf("logs.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Token Usage") {
		t.Error("logs.html does not contain 'Token Usage'")
	}
}

func TestSessionsRoute_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSessionsRoute_ContainsSessionTable(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"Dispatch Sessions",
		"No sessions found",
		"Source:",
		"Status:",
		"User:",
		"Showing 0 session(s)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("sessions page missing %q", want)
		}
	}
}

func TestSessionsRoute_WithFilters(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/sessions?source=telegraph&status=active&user=testuser")
	if err != nil {
		t.Fatalf("GET /sessions?filters: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSessionDetail_NotFound(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/sessions/999999")
	if err != nil {
		t.Fatalf("GET /sessions/999999: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSessionList_NilDB(t *testing.T) {
	result := SessionList(nil, SessionFilters{})
	if result.Sessions == nil {
		t.Error("Sessions should not be nil")
	}
	if len(result.Sessions) != 0 {
		t.Errorf("Sessions = %d, want 0", len(result.Sessions))
	}
}

func TestActiveSessionCount_NilDB(t *testing.T) {
	count := ActiveSessionCount(nil)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestCountJSONArray(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"null", 0},
		{"[]", 0},
		{`["car-1"]`, 1},
		{`["car-1","car-2","car-3"]`, 3},
		{"invalid json", 0},
	}
	for _, tt := range tests {
		got := countJSONArray(tt.input)
		if got != tt.want {
			t.Errorf("countJSONArray(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestNavContainsSessionsLink(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	if !strings.Contains(html, `href="/sessions"`) {
		t.Error("nav missing /sessions link")
	}
}

func TestEmbeddedTemplates_Sessions(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/sessions.html")
	if err != nil {
		t.Fatalf("sessions.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Dispatch Sessions") {
		t.Error("sessions.html does not contain 'Dispatch Sessions'")
	}

	data, err = templatesFS.ReadFile("templates/session_detail.html")
	if err != nil {
		t.Fatalf("session_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Conversation") {
		t.Error("session_detail.html does not contain 'Conversation'")
	}
}

func TestCarDetail_Breadcrumbs(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/car_detail.html")
	if err != nil {
		t.Fatalf("car_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "breadcrumbs") {
		t.Error("car_detail.html does not contain breadcrumbs")
	}
	if !strings.Contains(string(data), "/cars?track=") {
		t.Error("car_detail.html breadcrumbs missing track link")
	}
}

func TestEngineDetail_Breadcrumbs(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/engine_detail.html")
	if err != nil {
		t.Fatalf("engine_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "breadcrumbs") {
		t.Error("engine_detail.html does not contain breadcrumbs")
	}
	if !strings.Contains(string(data), `href="/"`) {
		t.Error("engine_detail.html breadcrumbs missing Dashboard link")
	}
}

func TestSessionDetail_Breadcrumbs(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/session_detail.html")
	if err != nil {
		t.Fatalf("session_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "breadcrumbs") {
		t.Error("session_detail.html does not contain breadcrumbs")
	}
	if !strings.Contains(string(data), `href="/sessions"`) {
		t.Error("session_detail.html breadcrumbs missing sessions link")
	}
}

func TestBreadcrumbCSS(t *testing.T) {
	data, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("style.css not embedded: %v", err)
	}
	if !strings.Contains(string(data), ".breadcrumbs") {
		t.Error("style.css does not contain .breadcrumbs")
	}
}

func TestDependencyGraph_NilDB(t *testing.T) {
	result := DependencyGraph(nil, "test-car")
	if result.Nodes != nil && len(result.Nodes) != 0 {
		t.Errorf("Nodes = %d, want 0", len(result.Nodes))
	}
	if result.Edges != nil && len(result.Edges) != 0 {
		t.Errorf("Edges = %d, want 0", len(result.Edges))
	}
	if result.Tree != nil && len(result.Tree) != 0 {
		t.Errorf("Tree = %d, want 0", len(result.Tree))
	}
}

func TestCarDetail_ContainsDepTreeTemplate(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/car_detail.html")
	if err != nil {
		t.Fatalf("car_detail.html not embedded: %v", err)
	}
	if !strings.Contains(string(data), "Dependency Tree") {
		t.Error("car_detail.html does not contain 'Dependency Tree'")
	}
	if !strings.Contains(string(data), "dep-tree") {
		t.Error("car_detail.html does not contain 'dep-tree' class")
	}
}

func TestDepTreeCSS(t *testing.T) {
	data, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatalf("style.css not embedded: %v", err)
	}
	if !strings.Contains(string(data), ".dep-tree") {
		t.Error("style.css does not contain .dep-tree")
	}
}

func TestYardmasterStatus_NilDB(t *testing.T) {
	result := YardmasterStatus(nil)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestDashboardData_ContainsYardmaster(t *testing.T) {
	data := dashboardData(nil)
	if _, ok := data["Yardmaster"]; !ok {
		t.Error("Yardmaster key should exist in dashboardData")
	}
}

func TestPartialsYardmaster_Returns200(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/partials/yardmaster")
	if err != nil {
		t.Fatalf("GET /partials/yardmaster: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestIndex_ContainsYardmasterCard(t *testing.T) {
	baseURL, cleanup := setupTestRouter(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	html := string(body[:n])

	for _, want := range []string{
		"yardmaster",
		"/partials/yardmaster",
		"Yardmaster",
		"not running",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index page missing %q", want)
		}
	}
}

func TestCarDetail_NoInlineScripts(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/car_detail.html")
	if err != nil {
		t.Fatalf("car_detail.html not embedded: %v", err)
	}
	content := string(data)

	// All <script> tags must have a src attribute (CSP-compliant).
	// Inline <script>...</script> blocks are forbidden by script-src 'self'.
	scriptIdx := 0
	for {
		idx := strings.Index(content[scriptIdx:], "<script")
		if idx == -1 {
			break
		}
		scriptIdx += idx
		closeIdx := strings.Index(content[scriptIdx:], ">")
		if closeIdx == -1 {
			break
		}
		tag := content[scriptIdx : scriptIdx+closeIdx+1]
		if !strings.Contains(tag, "src=") {
			t.Errorf("found inline <script> tag (CSP violation): %s", tag)
		}
		scriptIdx += closeIdx + 1
	}
}

func TestCycleChartJS_Embedded(t *testing.T) {
	data, err := assetsFS.ReadFile("assets/cycle-chart.js")
	if err != nil {
		t.Fatalf("cycle-chart.js not embedded: %v", err)
	}
	if len(data) == 0 {
		t.Error("cycle-chart.js is empty")
	}
	if !strings.Contains(string(data), "cycleDurationChart") {
		t.Error("cycle-chart.js does not reference cycleDurationChart canvas")
	}
}
