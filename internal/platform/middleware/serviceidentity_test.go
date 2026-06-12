package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func newS2SEngine(tokenMap map[string]string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	g := r.Group("/v1/comms")
	g.Use(middleware.RequireServiceIdentity(tokenMap))
	g.POST("/send", func(c *gin.Context) {
		c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"caller": middleware.ServiceIDFromCtx(c)}})
	})

	return r
}

func TestRequireServiceIdentity(t *testing.T) {
	const tokenUserSvc = "user-service-test-token-1234567890"   //nolint:gosec // G101: fake test token, not a real credential
	const tokenOtherSvc = "other-service-test-token-0987654321" //nolint:gosec // G101: fake test token, not a real credential

	tokenMap := map[string]string{
		"user-service":  tokenUserSvc,
		"other-service": tokenOtherSvc,
	}

	tests := []struct {
		name       string
		serverMap  map[string]string
		hdrID      string
		hdrToken   string
		wantStatus int
	}{
		{
			name:       "valid user-service id + correct token → 202",
			serverMap:  tokenMap,
			hdrID:      "user-service",
			hdrToken:   tokenUserSvc,
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "valid other-service id + correct token → 202",
			serverMap:  tokenMap,
			hdrID:      "other-service",
			hdrToken:   tokenOtherSvc,
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "correct id but wrong token → 401",
			serverMap:  tokenMap,
			hdrID:      "user-service",
			hdrToken:   "wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "token from a different service id (cross-caller) → 401",
			serverMap:  tokenMap,
			hdrID:      "user-service",
			hdrToken:   tokenOtherSvc, // valid token for other-service, not user-service
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown service id → 401",
			serverMap:  tokenMap,
			hdrID:      "unknown-service",
			hdrToken:   tokenUserSvc,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing service id → 401",
			serverMap:  tokenMap,
			hdrID:      "",
			hdrToken:   tokenUserSvc,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing token → 401",
			serverMap:  tokenMap,
			hdrID:      "user-service",
			hdrToken:   "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty token map fails closed → 401",
			serverMap:  map[string]string{},
			hdrID:      "user-service",
			hdrToken:   tokenUserSvc,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "nil token map fails closed → 401",
			serverMap:  nil,
			hdrID:      "user-service",
			hdrToken:   tokenUserSvc,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newS2SEngine(tc.serverMap)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/comms/send", http.NoBody)
			if tc.hdrID != "" {
				req.Header.Set("X-Service-Id", tc.hdrID)
			}

			if tc.hdrToken != "" {
				req.Header.Set("X-Service-Token", tc.hdrToken)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}
