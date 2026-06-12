package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildUserRLEngine creates a minimal Gin engine with:
//
//	RequireValidIdentity → UserRateLimiter
//
// so that tests exercise the real identity→limiter chain.
func buildUserRLEngine(perMin, burst int) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()

	r.GET("/test", middleware.RequireValidIdentity(), middleware.NewUserRateLimiter(perMin, burst).Handler(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	return r
}

// doReq fires a GET /test with the given X-User-Id header value (empty = omit).
func doReq(t *testing.T, r http.Handler, userID string) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	require.NoError(t, err)

	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}

// TestUserRateLimiter_AllowWithinBudget verifies that requests within the burst
// allowance are passed through with 200 OK.
func TestUserRateLimiter_AllowWithinBudget(t *testing.T) {
	uid := uuid.New().String()
	r := buildUserRLEngine(60, 5) // burst=5 — up to 5 simultaneous requests pass

	for i := range 5 {
		w := doReq(t, r, uid)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass within burst budget", i+1)
	}
}

// TestUserRateLimiter_DenyOverBudget verifies that requests exceeding the burst
// allowance are rejected with 429 and a Retry-After header that is >= "1".
func TestUserRateLimiter_DenyOverBudget(t *testing.T) {
	tests := []struct {
		name   string
		perMin int
		burst  int
	}{
		{"perMin=60 burst=2", 60, 2},
		{"perMin=120 burst=2", 120, 2}, // default perMin; must never produce Retry-After "0"
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uid := uuid.New().String()
			r := buildUserRLEngine(tc.perMin, tc.burst)

			// Exhaust burst.
			for i := range tc.burst {
				w := doReq(t, r, uid)
				require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
			}

			// Next request must be rate-limited.
			w := doReq(t, r, uid)
			assert.Equal(t, http.StatusTooManyRequests, w.Code)

			ra := w.Header().Get("Retry-After")
			assert.NotEmpty(t, ra, "Retry-After header must be set on 429")

			// Retry-After must be a positive integer string (never "0").
			raVal, err := strconv.Atoi(ra)
			require.NoError(t, err, "Retry-After must be a numeric string, got %q", ra)
			assert.GreaterOrEqual(t, raVal, 1, "Retry-After must be >= 1 to prevent immediate-retry loops (perMin=%d)", tc.perMin)
		})
	}
}

// TestUserRateLimiter_IndependentBuckets verifies that two different user IDs
// have independent token buckets — exhausting one does not affect the other.
func TestUserRateLimiter_IndependentBuckets(t *testing.T) {
	uid1 := uuid.New().String()
	uid2 := uuid.New().String()
	r := buildUserRLEngine(60, 1) // burst=1 per user

	// uid1 exhausts its bucket.
	w1first := doReq(t, r, uid1)
	require.Equal(t, http.StatusOK, w1first.Code)

	w1second := doReq(t, r, uid1)
	assert.Equal(t, http.StatusTooManyRequests, w1second.Code, "uid1 should be rate-limited after burst")

	// uid2 still has its own full bucket.
	w2 := doReq(t, r, uid2)
	assert.Equal(t, http.StatusOK, w2.Code, "uid2 bucket is independent and must not be affected by uid1")
}

// TestUserRateLimiter_MissingIdentityPassthrough verifies that a request without
// a valid X-User-Id header passes through the user rate limiter (not 429).
// Note: RequireValidIdentity returns 401 for missing/invalid headers; this test
// wires the limiter WITHOUT RequireValidIdentity to exercise the uuid.Nil branch.
func TestUserRateLimiter_MissingIdentityPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()

	// Wire limiter WITHOUT RequireValidIdentity so identity is absent in context.
	r.GET("/test", middleware.NewUserRateLimiter(60, 5).Handler(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// No X-User-Id header → identity not in context → limiter must pass through.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// The handler returns 200 because the limiter issued c.Next() on missing identity.
	assert.Equal(t, http.StatusOK, w.Code, "missing identity must not cause 429; limiter passes through")
}

// TestUserRateLimiter_LRUBoundDoesNotPanic verifies that creating many distinct
// user keys (more than typical load) does not panic or error — the LRU evicts
// oldest entries silently when full.
func TestUserRateLimiter_LRUBoundDoesNotPanic(t *testing.T) {
	// Use a large burst so all requests pass (we are testing stability, not denial).
	r := buildUserRLEngine(600, 1000)

	assert.NotPanics(t, func() {
		// Fire 1000 requests with distinct UUIDs — all should succeed without panic.
		// The key invariant is that the code path executes safely under many distinct keys.
		for range 1000 {
			uid := uuid.New().String()
			w := doReq(t, r, uid)
			assert.Equal(t, http.StatusOK, w.Code)
		}
	})
}

// hasKeyWithPrefix reports whether any key in keys starts with prefix.
func hasKeyWithPrefix(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
}

// TestIPRateLimiter_WaitlistPrefixIsolatedFromGlobal is a regression test for the
// shared-Redis-key bug: the global 120/min limiter and the dedicated 5/min
// waitlist limiter both used the "notification:rl:ip:<IP>" key, so global traffic
// pre-consumed the waitlist budget and the effective waitlist limit collapsed to
// ~2-3 instead of 5. With a dedicated key prefix the waitlist limiter keeps its
// own independent counter.
func TestIPRateLimiter_WaitlistPrefixIsolatedFromGlobal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	globalRL := middleware.NewIPRateLimiter(rdb, 120, time.Minute)
	waitlistRL := middleware.NewIPRateLimiterWithPrefix(rdb, 5, time.Minute, "notification:rl:waitlist:ip")

	r := gin.New()
	r.GET("/global", globalRL.Handler(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/waitlist", waitlistRL.Handler(), func(c *gin.Context) { c.Status(http.StatusOK) })

	fire := func(method, path string) int {
		req, err := http.NewRequestWithContext(context.Background(), method, path, http.NoBody)
		require.NoError(t, err)
		req.RemoteAddr = "203.0.113.7:5555" // fixed client IP shared by both limiters

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		return w.Code
	}

	// Drive the global limiter past the waitlist limit. If both limiters shared a
	// Redis key, these 4 hits would pre-consume the waitlist's 5/min budget.
	for i := range 4 {
		require.Equal(t, http.StatusOK, fire(http.MethodGet, "/global"), "global hit %d is within the 120/min budget", i+1)
	}

	// The waitlist limiter must still grant its full independent 5/min budget.
	for i := range 5 {
		require.Equal(t, http.StatusOK, fire(http.MethodPost, "/waitlist"), "waitlist hit %d is within its own 5/min budget", i+1)
	}

	// 6th waitlist request exceeds 5/min → 429.
	assert.Equal(t, http.StatusTooManyRequests, fire(http.MethodPost, "/waitlist"), "waitlist 6th request must be rate-limited")

	// The two limiters must have written to distinct Redis keys.
	keys := mr.Keys()
	assert.True(t, hasKeyWithPrefix(keys, "notification:rl:waitlist:ip:"), "waitlist limiter must use its own prefixed key, got %v", keys)
	assert.True(t, hasKeyWithPrefix(keys, "notification:rl:ip:"), "global limiter must keep the default key, got %v", keys)
}
