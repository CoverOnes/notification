package handler

import (
	"log/slog"
	"time"

	"github.com/CoverOnes/notification/internal/platform/health"
	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// RouterConfig holds all handler-level dependencies.
type RouterConfig struct {
	Store store.NotificationStore
	Pool  *pgxpool.Pool
	Redis *redis.Client // may be nil in dev
}

// NewRouter builds and returns the configured Gin engine.
//
// CORS policy: CORS is intentionally NOT applied at this internal service layer.
// notification is reached only via the API gateway, which owns all browser-facing
// CORS handling. Adding permissive CORS here would widen the attack surface without
// benefit (CONVENTIONS §9 positions CORS after the access-log in the chain but
// the gateway/edge handles it before requests reach this service).
func NewRouter(cfg RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.SetTrustedProxies(nil) //nolint:errcheck // nil proxy list disables proxy trust; gin docs confirm error is always nil for nil argument

	// Global middleware chain (order per CONVENTIONS §9).
	r.Use(middleware.Recover())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders())
	r.Use(accessLogger())

	// Health endpoints — registered BEFORE the rate limiter so that liveness /
	// readiness probes are never rate-limited.
	h := health.NewHandler(cfg.Pool)
	r.GET("/healthz", h.Liveness)
	r.GET("/readyz", h.Readiness)

	// Rate limiter — 120 req/min per IP for all API routes below.
	ipRL := middleware.NewIPRateLimiter(cfg.Redis, 120, time.Minute)
	r.Use(ipRL.Handler())

	// Notification endpoints — all require valid identity (tier 0).
	notifH := NewNotificationHandler(cfg.Store)

	api := r.Group("/v1/me/notifications")
	api.Use(middleware.RequireValidIdentity())
	api.Use(middleware.RequireTier(0)) // CONVENTIONS §10: explicit min-tier declaration per protected route

	api.GET("", notifH.List)
	api.GET("/unread-count", notifH.UnreadCount)
	api.POST("/read-all", notifH.MarkAllRead)
	api.POST("/:id/read", notifH.MarkRead)

	return r
}

// accessLogger returns a minimal slog-based access-log middleware.
// Health probe paths (/healthz, /readyz) are excluded to keep logs noise-free.
func accessLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/healthz" || path == "/readyz" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		slog.Info(
			"http",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"request_id", c.GetString("request_id"),
		)
	}
}
