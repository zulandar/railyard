package dashboard

import (
	"io/fs"
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

	// SSE endpoint (stub for now — implemented in later task).
	router.GET("/api/events", handleSSEStub())
}

func handleIndex(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"page": "dashboard",
		})
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
