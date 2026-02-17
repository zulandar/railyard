package dashboard

import (
	"fmt"
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

	// SSE endpoint for real-time escalation alerts.
	router.GET("/api/events", handleSSE(db))
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
		track := c.Query("track")
		status := c.Query("status")
		carType := c.Query("type")
		parentID := c.Query("parent")

		result := CarList(db, track, status, carType, parentID)

		c.HTML(http.StatusOK, "cars.html", gin.H{
			"Cars":         result.Cars,
			"Tracks":       result.Tracks,
			"Statuses":     result.Statuses,
			"Types":        result.Types,
			"ActiveTrack":  track,
			"ActiveStatus": status,
			"ActiveType":   carType,
		})
	}
}

func handleCarDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		detail, err := GetCarDetail(db, id)
		if err != nil {
			c.HTML(http.StatusNotFound, "layout.html", gin.H{
				"Error": fmt.Sprintf("Car not found: %s", id),
			})
			return
		}

		c.HTML(http.StatusOK, "car_detail.html", gin.H{
			"Car": detail,
		})
	}
}

func handleEngineDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		engine, err := GetEngineDetail(db, id)
		if err != nil {
			c.HTML(http.StatusNotFound, "layout.html", gin.H{
				"Error": fmt.Sprintf("Engine not found: %s", id),
			})
			return
		}

		activity := GetEngineActivity(db, id)

		c.HTML(http.StatusOK, "engine_detail.html", gin.H{
			"Engine":   engine,
			"Activity": activity,
		})
	}
}

func handleMessages(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		agent := c.Query("agent")
		priority := c.Query("priority")
		unacked := c.Query("unacked") == "true"

		filters := MessageFilters{
			Agent:    agent,
			Priority: priority,
			Unacked:  unacked,
		}
		result := ListMessages(db, filters)

		// Get pending escalations separately for the top section.
		escalations, _ := RecentEscalations(db)

		c.HTML(http.StatusOK, "messages.html", gin.H{
			"Messages":       result.Messages,
			"Agents":         result.Agents,
			"Priorities":     result.Priorities,
			"Escalations":    escalations,
			"ActiveAgent":    agent,
			"ActivePriority": priority,
			"ActiveUnacked":  unacked,
		})
	}
}
