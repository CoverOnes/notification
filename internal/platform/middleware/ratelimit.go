package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/CoverOnes/notification/internal/platform/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// fallbackBurst is the token-bucket burst for the in-process fallback limiter.
const fallbackBurst = 10

// fallbackLRUCap is the maximum number of unique keys tracked by the in-process
// fallback limiter. Bounded LRU prevents memory-DoS from IP rotation attacks.
const fallbackLRUCap = 100_000

// userFallbackLRUCap is the maximum number of unique user keys tracked by the
// per-user in-process limiter. Bounded LRU prevents memory-exhaustion DoS under
// account-rotation attacks.
const userFallbackLRUCap = 100_000

// RateLimiter is a Redis-backed fixed-window rate limiter with an in-process
// token-bucket fallback that engages when Redis errors (fails safe, not open).
type RateLimiter struct {
	rdb      *redis.Client
	limit    int
	window   time.Duration
	keyFunc  func(c *gin.Context) string
	fallback *fallbackLimiter
}

// fallbackLimiter holds per-IP token buckets for the in-process safety net.
type fallbackLimiter struct {
	mu      sync.Mutex
	buckets *lru.Cache[string, *rate.Limiter]
	r       rate.Limit
	burst   int
}

func newFallbackLimiter(r rate.Limit, burst int) *fallbackLimiter {
	cache, err := lru.New[string, *rate.Limiter](fallbackLRUCap)
	if err != nil {
		// lru.New only errors when cap <= 0, which cannot happen here.
		panic(fmt.Sprintf("fallbackLimiter: unexpected lru.New error: %v", err))
	}

	return &fallbackLimiter{
		buckets: cache,
		r:       r,
		burst:   burst,
	}
}

func (f *fallbackLimiter) allow(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	lim, ok := f.buckets.Get(key)
	if !ok {
		lim = rate.NewLimiter(f.r, f.burst)
		f.buckets.Add(key, lim)
	}

	return lim.Allow()
}

// NewIPRateLimiter builds a limiter keyed by client IP using the default
// "notification:rl:ip" key prefix (the global per-IP limiter).
func NewIPRateLimiter(rdb *redis.Client, limit int, window time.Duration) *RateLimiter {
	return NewIPRateLimiterWithPrefix(rdb, limit, window, "notification:rl:ip")
}

// NewIPRateLimiterWithPrefix builds a limiter keyed by client IP under the given
// Redis key prefix. Endpoints that need their own rate budget MUST pass a
// distinct prefix; otherwise two limiters INCR the same Redis key and the
// smaller limit is silently eaten by the larger limiter's traffic.
func NewIPRateLimiterWithPrefix(rdb *redis.Client, limit int, window time.Duration, keyPrefix string) *RateLimiter {
	r := rate.Limit(float64(limit) / window.Seconds())

	return &RateLimiter{
		rdb:    rdb,
		limit:  limit,
		window: window,
		keyFunc: func(c *gin.Context) string {
			return fmt.Sprintf("%s:%s", keyPrefix, c.ClientIP())
		},
		fallback: newFallbackLimiter(r, fallbackBurst),
	}
}

// NewServiceRateLimiter builds a limiter keyed by the authenticated caller
// service id (set by RequireServiceIdentity), falling back to client IP when no
// service id is present. Used to best-effort cap a single S2S caller's send rate
// so one compromised caller cannot flood the send endpoint
// (backend-security-design §5.5).
func NewServiceRateLimiter(rdb *redis.Client, limit int, window time.Duration) *RateLimiter {
	r := rate.Limit(float64(limit) / window.Seconds())

	return &RateLimiter{
		rdb:    rdb,
		limit:  limit,
		window: window,
		keyFunc: func(c *gin.Context) string {
			if sid := ServiceIDFromCtx(c); sid != "" {
				return fmt.Sprintf("notification:rl:svc:%s", sid)
			}

			return fmt.Sprintf("notification:rl:svc-ip:%s", c.ClientIP())
		},
		fallback: newFallbackLimiter(r, fallbackBurst),
	}
}

// Handler returns the Gin middleware function.
func (rl *RateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rl.rdb == nil {
			key := rl.keyFunc(c)
			if !rl.fallback.allow(key) {
				c.Abort()
				httpx.ErrCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")

				return
			}

			c.Next()

			return
		}

		key := rl.keyFunc(c)
		ctx := c.Request.Context()

		count, err := rl.increment(ctx, key)
		if err != nil {
			slog.Warn("rate limiter redis error; applying in-process fallback limiter", "err", err)

			if !rl.fallback.allow(key) {
				c.Abort()
				httpx.ErrCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")

				return
			}

			c.Next()

			return
		}

		if count > rl.limit {
			c.Abort()
			httpx.ErrCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")

			return
		}

		c.Next()
	}
}

func (rl *RateLimiter) increment(ctx context.Context, key string) (int, error) {
	pipe := rl.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, rl.window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}

	return int(incr.Val()), nil
}

// UserRateLimiter is a pure in-process per-authenticated-user token-bucket
// limiter. It is keyed on the verified user_id extracted from context (set by
// RequireValidIdentity) — never on a raw header — so the key is always
// gateway-verified and cannot be spoofed by a client.
//
// The LRU is bounded at userFallbackLRUCap to prevent memory-exhaustion DoS
// under account-rotation attacks.
type UserRateLimiter struct {
	mu          sync.Mutex
	buckets     *lru.Cache[string, *rate.Limiter]
	r           rate.Limit
	burst       int
	limitPerMin int
}

// NewUserRateLimiter constructs a UserRateLimiter allowing limitPerMin requests
// per minute with the given burst allowance.
func NewUserRateLimiter(limitPerMin, burst int) *UserRateLimiter {
	r := rate.Limit(float64(limitPerMin) / 60.0)
	cache, err := lru.New[string, *rate.Limiter](userFallbackLRUCap)
	if err != nil {
		// lru.New only errors when cap <= 0, which cannot happen here.
		panic(fmt.Sprintf("UserRateLimiter: unexpected lru.New error: %v", err))
	}

	return &UserRateLimiter{buckets: cache, r: r, burst: burst, limitPerMin: limitPerMin}
}

func (l *UserRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.buckets.Get(key)
	if !ok {
		lim = rate.NewLimiter(l.r, l.burst)
		l.buckets.Add(key, lim)
	}

	return lim.Allow()
}

// Handler returns the Gin middleware function for per-user rate limiting.
// It MUST be mounted AFTER VerifyGatewaySignature + RequireValidIdentity so
// that identity.UserID is already verified and in context.
//
// Requests with no verified identity (uuid.Nil) are allowed through with a
// Warn log — this mirrors the downstream fallback posture: the identity
// middleware has already rejected truly unauthenticated requests with 401;
// reaching here with uuid.Nil is a misconfiguration, not a normal path.
func (l *UserRateLimiter) Handler() gin.HandlerFunc {
	retryAfter := strconv.Itoa(max(1, int(math.Ceil(60.0/float64(l.limitPerMin)))))

	return func(c *gin.Context) {
		identity, ok := IdentityFromCtx(c)
		if !ok || identity.UserID == uuid.Nil {
			slog.Warn("user rate limiter: no verified user_id; passing through", "path", c.Request.URL.Path)
			c.Next()

			return
		}

		if !l.allow("notification:rl:user:" + identity.UserID.String()) {
			c.Header("Retry-After", retryAfter)
			c.Abort()
			httpx.ErrCode(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")

			return
		}

		c.Next()
	}
}
