package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// setupDBRouter creates a test router backed by a real SQLite database.
// It returns the DB (for seeding), the base URL, and a cleanup function.
func setupDBRouter(t *testing.T) (*gorm.DB, string, func()) {
	t.Helper()

	db := testDB(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	router.SetHTMLTemplate(tmpl)
	registerRoutes(router, db, "testproject")

	port := findFreePort()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: router}
	go srv.ListenAndServe()

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

	return db, baseURL, func() { srv.Close() }
}

// readBody reads the full response body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestRouteIndex_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	now := time.Now()
	db.Create(&models.Engine{
		ID: "eng-idx-1", Track: "backend", Status: "active", LastActivity: now,
	})
	db.Create(&models.Car{
		ID: "car-idx-1", Title: "Index Test Car", Track: "backend", Status: "open",
	})

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteCarDetail_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	db.Create(&models.Car{
		ID: "car-detail-1", Title: "My Special Car", Track: "backend", Status: "open",
	})

	resp, err := http.Get(baseURL + "/cars/car-detail-1")
	if err != nil {
		t.Fatalf("GET /cars/car-detail-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "My Special Car") {
		t.Errorf("body missing car title 'My Special Car'")
	}
}

func TestRouteEngineDetail_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	now := time.Now()
	db.Create(&models.Engine{
		ID: "eng-detail-1", Track: "backend", Status: "active", LastActivity: now,
	})

	resp, err := http.Get(baseURL + "/engines/eng-detail-1")
	if err != nil {
		t.Fatalf("GET /engines/eng-detail-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteSessionDetail_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	session := models.DispatchSession{
		Source:        "local",
		UserName:      "testuser",
		Status:        "active",
		LastHeartbeat: time.Now(),
		CarsCreated:   `["car-1"]`,
	}
	db.Create(&session)

	resp, err := http.Get(fmt.Sprintf("%s/sessions/%d", baseURL, session.ID))
	if err != nil {
		t.Fatalf("GET /sessions/%d: %v", session.ID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteCars_WithFilters(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	db.Create(&models.Car{
		ID: "car-be-1", Title: "Backend Car", Track: "backend", Status: "open",
	})
	db.Create(&models.Car{
		ID: "car-fe-1", Title: "Frontend Car", Track: "frontend", Status: "open",
	})

	resp, err := http.Get(baseURL + "/cars?track=backend")
	if err != nil {
		t.Fatalf("GET /cars?track=backend: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteMessages_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	db.Create(&models.Message{
		FromAgent: "eng-1", ToAgent: "eng-2", Subject: "test msg",
		Body: "hello", Priority: "normal",
	})

	resp, err := http.Get(baseURL + "/messages")
	if err != nil {
		t.Fatalf("GET /messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteLogs_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	db.Create(&models.AgentLog{
		EngineID: "eng-log-1", CarID: "car-log-1", Direction: "out",
		Content: "test log entry", TokenCount: 100,
		InputTokens: 50, OutputTokens: 50,
	})

	resp, err := http.Get(baseURL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRouteSessions_WithData(t *testing.T) {
	db, baseURL, cleanup := setupDBRouter(t)
	defer cleanup()

	db.Create(&models.DispatchSession{
		Source:        "telegraph",
		UserName:      "alice",
		Status:        "active",
		LastHeartbeat: time.Now(),
		CarsCreated:   `["car-a","car-b"]`,
	})
	db.Create(&models.DispatchSession{
		Source:        "local",
		UserName:      "bob",
		Status:        "completed",
		LastHeartbeat: time.Now(),
		CarsCreated:   `[]`,
	})

	resp, err := http.Get(baseURL + "/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
