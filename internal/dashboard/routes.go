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
	router.GET("/logs", handleLogs(db))
	router.GET("/sessions", handleSessionList(db))
	router.GET("/sessions/:id", handleSessionDetail(db))

	// HTMX partial endpoints for live refresh.
	router.GET("/partials/engines", handlePartialsEngines(db))
	router.GET("/partials/tracks", handlePartialsTracks(db))
	router.GET("/partials/alerts", handlePartialsAlerts(db))
	router.GET("/partials/stats", handlePartialsStats(db))
	router.GET("/partials/yardmaster", handlePartialsYardmaster(db))

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
			"Stats":       DashboardStats{},
			"Yardmaster":  (*YardmasterInfo)(nil),
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

	stats := ComputeStats(engines, tracks, db)

	return gin.H{
		"Engines":     engines,
		"Tracks":      tracks,
		"Escalations": escalations,
		"QueueDepth":  queueDepth,
		"Stats":       stats,
		"Yardmaster":  YardmasterStatus(db),
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

func handlePartialsStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := dashboardData(db)
		c.HTML(http.StatusOK, "stats_fragment", data)
	}
}

func handlePartialsYardmaster(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := dashboardData(db)
		c.HTML(http.StatusOK, "yardmaster_fragment", data)
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

		graph := DependencyGraph(db, id)
		c.HTML(http.StatusOK, "car_detail.html", gin.H{
			"Car":   detail,
			"Graph": graph,
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

func handleSessionList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		source := c.Query("source")
		status := c.Query("status")
		user := c.Query("user")

		filters := SessionFilters{
			Source:   source,
			Status:   status,
			UserName: user,
		}
		result := SessionList(db, filters)

		c.HTML(http.StatusOK, "sessions.html", gin.H{
			"Sessions":     result.Sessions,
			"Sources":      result.Sources,
			"Statuses":     result.Statuses,
			"Users":        result.Users,
			"ActiveSource": source,
			"ActiveStatus": status,
			"ActiveUser":   user,
		})
	}
}

func handleSessionDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		detail, err := GetSessionDetail(db, id)
		if err != nil {
			c.HTML(http.StatusNotFound, "layout.html", gin.H{
				"Error": fmt.Sprintf("Session not found: %s", id),
			})
			return
		}

		c.HTML(http.StatusOK, "session_detail.html", gin.H{
			"Session": detail,
		})
	}
}

func handleLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		engineID := c.Query("engine")
		carID := c.Query("car")
		direction := c.Query("direction")

		filters := AgentLogFilters{
			EngineID:  engineID,
			CarID:     carID,
			Direction: direction,
		}
		logResult := AgentLogList(db, filters)
		tokenResult := TokenUsageSummary(db)

		c.HTML(http.StatusOK, "logs.html", gin.H{
			"Logs":            logResult.Logs,
			"Engines":         logResult.Engines,
			"Cars":            logResult.Cars,
			"Directions":      logResult.Directions,
			"TokenByEngine":   tokenResult.ByEngine,
			"TotalInput":      tokenResult.TotalInput,
			"TotalOutput":     tokenResult.TotalOutput,
			"TotalAll":        tokenResult.TotalAll,
			"ActiveEngine":    engineID,
			"ActiveCar":       carID,
			"ActiveDirection": direction,
		})
	}
}
