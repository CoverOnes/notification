package handler

import (
	"log/slog"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/platform/health"
	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// RouterConfig holds all handler-level dependencies.
type RouterConfig struct {
	Store         store.NotificationStore
	WaitlistStore store.WaitlistStore
	Pool          *pgxpool.Pool
	Redis         *redis.Client // may be nil in dev

	// GatewayHMACSecret is the §24.1 shared secret used to verify the
	// gateway-origin identity signature on the /v1/me/notifications group.
	// Empty == dev posture (verification disabled); config validation guarantees
	// it is non-empty in non-dev.
	GatewayHMACSecret string

	// UserRateLimitPerMin is the per-authenticated-user rate limit (requests/min).
	// 0 = disabled. When > 0, UserRateLimitBurst must also be > 0.
	UserRateLimitPerMin int

	// UserRateLimitBurst is the per-user token-bucket burst allowance.
	UserRateLimitBurst int

	// GatewayCIDR is the CIDR of the API gateway/LB that forwards requests.
	// When non-empty, Gin trusts X-Forwarded-For from this source so c.ClientIP()
	// returns the real end-user IP. This is required for the per-IP rate limiters
	// (the global 120/min limiter AND the waitlist 5/min limiter) to key per
	// client rather than collapsing to a single per-gateway bucket.
	// Empty (dev/unset): SetTrustedProxies(nil) — safe fallback (RemoteAddr).
	GatewayCIDR string

	// Comms wiring — only set when NOTIFICATION_COMMS_ENABLED is true. When
	// CommsService is nil, NO comms routes and NO S2S middleware are registered
	// (the module is dormant and the service behaves exactly as before).
	CommsService comms.CommsService
	// S2STokenMap is the per-caller token map (serviceID → token) parsed from
	// NOTIFICATION_S2S_TOKENS. Each caller has an independently rotatable token.
	S2STokenMap map[string]string
}

// NewRouter builds and returns the configured Gin engine.
//
// CORS policy: CORS is intentionally NOT applied at this internal service layer.
// notification is reached only via the API gateway, which owns all browser-facing
// CORS handling. Adding permissive CORS here would widen the attack surface without
// benefit (CONVENTIONS §9 positions CORS after the access-log in the chain but
// the gateway/edge handles it before requests reach this service).
func NewRouter(cfg *RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	// Trust the gateway proxy CIDR so c.ClientIP() returns the real end-user IP
	// from X-Forwarded-For rather than the gateway's egress IP.
	//
	// When GatewayCIDR is set (non-dev): Gin honors X-Forwarded-For only from the
	// gateway, so the per-IP rate limiters (global 120/min AND waitlist 5/min) key
	// per client. When GatewayCIDR is empty (dev/unset): SetTrustedProxies(nil)
	// disables proxy trust entirely — c.ClientIP() returns RemoteAddr (safe fallback).
	//
	// We must NOT trust XFF blindly (SetTrustedProxies([]string{"0.0.0.0/0"})) as
	// that lets any client spoof its IP via the header; config.validateGatewayCIDR
	// rejects wildcard CIDRs at boot.
	if cfg.GatewayCIDR != "" {
		if err := r.SetTrustedProxies([]string{cfg.GatewayCIDR}); err != nil {
			// SetTrustedProxies only fails on an invalid CIDR, which config.validate()
			// already rejects at boot. Panic here to surface a config bug fast rather
			// than silently running without proxy trust.
			panic("router: invalid GatewayCIDR: " + err.Error())
		}
	} else {
		r.SetTrustedProxies(nil) //nolint:errcheck // nil proxy list disables proxy trust; gin docs confirm error is always nil for nil argument
	}

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

	// Public waitlist capture — NO auth required, registered before the identity-
	// protected groups. The global IP limiter above still applies; additionally a
	// dedicated tighter limiter (5 req/min per IP) prevents table-fill DoS from
	// bots spread across many IPs. Without this, N IPs × 120 shared-limit =
	// unbounded inserts on a table with no TTL (backend-security-design §5.3).
	// Gateway must add a passthrough rule for POST /v1/waitlist (separate task).
	if cfg.WaitlistStore != nil {
		waitlistH := NewWaitlistHandler(cfg.WaitlistStore)
		// Own Redis key prefix so this 5/min budget is an independent counter and
		// is not co-consumed by the global 120/min limiter sharing the same IP.
		waitlistRL := middleware.NewIPRateLimiterWithPrefix(cfg.Redis, 5, time.Minute, "notification:rl:waitlist:ip")
		r.POST("/v1/waitlist", waitlistRL.Handler(), waitlistH.Capture)
	} else {
		// S-3: a nil WaitlistStore silently skips registering POST /v1/waitlist —
		// requests would 404 with no operator-visible reason. Warn at startup so a
		// misconfigured/forgotten store dependency is not invisible.
		slog.Warn("waitlist store is nil; POST /v1/waitlist not registered (waitlist capture disabled)")
	}

	// Notification endpoints — all require valid identity (tier 0).
	notifH := NewNotificationHandler(cfg.Store)

	api := r.Group("/v1/me/notifications")
	// Defense-in-depth (§24.1): verify the gateway-origin HMAC signature BEFORE
	// any identity-header middleware trusts the request. When the secret is empty
	// (dev) this is a no-op passthrough, matching the gateway's dev signing-skip.
	// NOTE: do NOT add VerifyGatewaySignature to /v1/comms/* — that group is
	// S2S-only (X-Service-Token) and is intentionally NOT proxied from the gateway.
	api.Use(middleware.VerifyGatewaySignature(cfg.GatewayHMACSecret, cfg.Redis))
	api.Use(middleware.RequireValidIdentity())

	// Per-authenticated-user rate limiter — mounted AFTER VerifyGatewaySignature +
	// RequireValidIdentity so that identity.UserID is gateway-verified and in
	// context before the limiter reads it. Only registered when perMin > 0.
	if cfg.UserRateLimitPerMin > 0 {
		userRL := middleware.NewUserRateLimiter(cfg.UserRateLimitPerMin, cfg.UserRateLimitBurst)
		api.Use(userRL.Handler())
	}

	api.Use(middleware.RequireTier(0)) // CONVENTIONS §10: explicit min-tier declaration per protected route

	api.GET("", notifH.List)
	api.GET("/unread-count", notifH.UnreadCount)
	api.POST("/read-all", notifH.MarkAllRead)
	api.POST("/:id/read", notifH.MarkRead)

	// Comms module (S2S) routes — registered ONLY when the module is enabled.
	// DENY-BY-DEFAULT: every route is behind RequireServiceIdentity. These
	// endpoints are an arbitrary-send primitive and MUST NOT be proxied from the
	// public edge by the gateway (backend-security-design §5.5).
	if cfg.CommsService != nil {
		commsH := NewCommsHandler(cfg.CommsService)

		// Per-caller best-effort rate limit (keyed by service id / IP) on top of
		// the global IP limiter, so a single compromised caller cannot flood sends.
		commsRL := middleware.NewServiceRateLimiter(cfg.Redis, 300, time.Minute)

		commsGroup := r.Group("/v1/comms")
		commsGroup.Use(middleware.RequireServiceIdentity(cfg.S2STokenMap))
		commsGroup.Use(commsRL.Handler())

		commsGroup.POST("/send", commsH.Send)
		commsGroup.POST("/receipts/:provider", commsH.Receipts)
	}

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
