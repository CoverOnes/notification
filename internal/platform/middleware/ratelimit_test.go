package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
// allowance are rejected with 429 and a Retry-After header.
func TestUserRateLimiter_DenyOverBudget(t *testing.T) {
	uid := uuid.New().String()
	r := buildUserRLEngine(60, 2) // burst=2 — 3rd request must be rejected

	// First two succeed.
	for i := range 2 {
		w := doReq(t, r, uid)
		require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}

	// Third request must be rate-limited.
	w := doReq(t, r, uid)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "Retry-After header must be set on 429")
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
