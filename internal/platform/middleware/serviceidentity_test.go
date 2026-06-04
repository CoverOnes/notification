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

func newS2SEngine(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	g := r.Group("/v1/comms")
	g.Use(middleware.RequireServiceIdentity(token))
	g.POST("/send", func(c *gin.Context) {
		c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"caller": middleware.ServiceIDFromCtx(c)}})
	})

	return r
}

func TestRequireServiceIdentity(t *testing.T) {
	const token = "the-shared-s2s-token-value-1234567890" //nolint:gosec // G101: fake test token, not a real credential

	tests := []struct {
		name       string
		serverTok  string
		hdrID      string
		hdrToken   string
		wantStatus int
	}{
		{
			name:       "valid service id + token → 202",
			serverTok:  token,
			hdrID:      "user-service",
			hdrToken:   token,
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "missing service id → 401",
			serverTok:  token,
			hdrID:      "",
			hdrToken:   token,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong token → 401",
			serverTok:  token,
			hdrID:      "user-service",
			hdrToken:   "wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing token → 401",
			serverTok:  token,
			hdrID:      "user-service",
			hdrToken:   "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty server token fails closed → 401 even with matching empty",
			serverTok:  "",
			hdrID:      "user-service",
			hdrToken:   "",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newS2SEngine(tc.serverTok)

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
