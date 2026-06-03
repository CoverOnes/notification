package handler

import (
	"net/http"

	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/gin-gonic/gin"
)

// BuildTestEngine creates a minimal Gin engine for handler unit tests.
// It wires up identity middleware and the notification routes without requiring
// a real Postgres pool (health endpoints are excluded).
func (h *NotificationHandler) BuildTestEngine() http.Handler {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.SetTrustedProxies(nil) //nolint:errcheck // nil proxy list disables proxy trust; gin docs confirm error is always nil for nil argument

	r.Use(middleware.Recover())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders())

	api := r.Group("/v1/me/notifications")
	api.Use(middleware.RequireValidIdentity())

	api.GET("", h.List)
	api.GET("/unread-count", h.UnreadCount)
	api.POST("/read-all", h.MarkAllRead)
	api.POST("/:id/read", h.MarkRead)

	return r
}
