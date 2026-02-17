package dashboard

import (
	"io/fs"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// registerRoutes sets up all dashboard routes on the Gin router.
func registerRoutes(router *gin.Engine, db *gorm.DB) {
	// Embedded static assets (served from assets/ subdir of the embed.FS).
	staticFS, _ := fs.Sub(assetsFS, "assets")
	router.StaticFS("/static", http.FS(staticFS))

	// Pages.
	router.GET("/", handleIndex(db))
	router.GET("/cars", handleCarList(db))
	router.GET("/cars/:id", handleCarDetail(db))
	router.GET("/engines/:id", handleEngineDetail(db))
	router.GET("/messages", handleMessages(db))

	// HTMX partial endpoints for live refresh.
	router.GET("/partials/engines", handlePartialsEngines(db))
	router.GET("/partials/tracks", handlePartialsTracks(db))
	router.GET("/partials/alerts", handlePartialsAlerts(db))

	// SSE endpoint (stub for now — implemented in later task).
	router.GET("/api/events", handleSSEStub())
}

// dashboardData gathers all data needed for the dashboard page.
func dashboardData(db *gorm.DB) gin.H {
	if db == nil {
		return gin.H{
			"Engines":     []EngineRow{},
			"Tracks":      []TrackStatusCount{},
			"Escalations": []Escalation{},
			"QueueDepth":  int64(0),
		}
	}

	engines, err := EngineSummary(db)
	if err != nil {
		log.Printf("dashboard: engines query: %v", err)
	}
	tracks, err := TrackSummary(db)
	if err != nil {
		log.Printf("dashboard: tracks query: %v", err)
	}
	queueDepth, err := MessageQueueDepth(db)
	if err != nil {
		log.Printf("dashboard: queue depth query: %v", err)
	}
	escalations, err := RecentEscalations(db)
	if err != nil {
		log.Printf("dashboard: escalations query: %v", err)
	}

	return gin.H{
		"Engines":     engines,
		"Tracks":      tracks,
		"Escalations": escalations,
		"QueueDepth":  queueDepth,
	}
}

func handleIndex(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", dashboardData(db))
	}
}

func handlePartialsEngines(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := dashboardData(db)
		c.HTML(http.StatusOK, "engines_fragment", data)
	}
}

func handlePartialsTracks(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := dashboardData(db)
		c.HTML(http.StatusOK, "tracks_fragment", data)
	}
}

func handlePartialsAlerts(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := dashboardData(db)
		c.HTML(http.StatusOK, "alerts_fragment", data)
	}
}

func handleCarList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"page": "cars",
		})
	}
}

func handleCarDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"page":  "car-detail",
			"carID": c.Param("id"),
		})
	}
}

func handleEngineDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"page":     "engine-detail",
			"engineID": c.Param("id"),
		})
	}
}

func handleMessages(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"page": "messages",
		})
	}
}

func handleSSEStub() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.String(http.StatusOK, "data: {\"type\":\"connected\"}\n\n")
	}
}
