package middleware_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testGatewaySecret = "test-gateway-hmac-secret-32bytes!!"

// buildGatewayEngine creates a Gin engine with VerifyGatewaySignature applied to /v1/me.
func buildGatewayEngine(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	g := r.Group("/v1/me/notifications")
	g.Use(middleware.VerifyGatewaySignature(secret))
	g.GET("", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	return r
}

// computeTestSig produces the gateway HMAC-SHA256 for a canonical string
// matching the §24.1 spec: userID|kycTier|accountType|emailVerified|requestID|ts
func computeTestSig(secret, userID, kycTier, accountType, emailVerified, requestID, ts string) string {
	canonical := strings.Join([]string{userID, kycTier, accountType, emailVerified, requestID, ts}, "|")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGatewaySignature(t *testing.T) {
	validTS := fmt.Sprintf("%d", time.Now().Unix())
	validUserID := "8a2b5c4d-0000-0000-0000-000000000001"
	validRequestID := "req-abc123"

	// Pre-compute a valid signature for the happy-path request.
	validSig := computeTestSig(testGatewaySecret, validUserID, "0", "individual", "true", validRequestID, validTS)

	tests := []struct {
		name          string
		secret        string
		userID        string
		kycTier       string
		accountType   string
		emailVerified string
		requestID     string
		ts            string
		sig           string
		wantStatus    int
	}{
		{
			name:          "valid signature → 200",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            validTS,
			sig:           validSig,
			wantStatus:    http.StatusOK,
		},
		{
			name:          "missing X-Gateway-Signature → 401",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            validTS,
			sig:           "", // missing
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "missing X-Gateway-Ts → 401",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            "", // missing
			sig:           validSig,
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "forged/wrong signature → 401",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            validTS,
			sig:           "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "expired timestamp (> 30s) → 401",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            fmt.Sprintf("%d", time.Now().Unix()-120), // 2 min ago
			sig:           computeTestSig(testGatewaySecret, validUserID, "0", "individual", "true", validRequestID, fmt.Sprintf("%d", time.Now().Unix()-120)),
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "non-numeric ts → 401",
			secret:        testGatewaySecret,
			userID:        validUserID,
			kycTier:       "0",
			accountType:   "individual",
			emailVerified: "true",
			requestID:     validRequestID,
			ts:            "not-a-number",
			sig:           validSig,
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "empty secret (dev mode) → 200 with no headers",
			secret:        "", // dev: skip verification
			userID:        "",
			kycTier:       "",
			accountType:   "",
			emailVerified: "",
			requestID:     "",
			ts:            "",
			sig:           "",
			wantStatus:    http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := buildGatewayEngine(tc.secret)

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/me/notifications", http.NoBody)
			require.NoError(t, err)

			if tc.userID != "" {
				req.Header.Set("X-User-Id", tc.userID)
			}

			if tc.kycTier != "" {
				req.Header.Set("X-Kyc-Tier", tc.kycTier)
			}

			if tc.accountType != "" {
				req.Header.Set("X-Account-Type", tc.accountType)
			}

			if tc.emailVerified != "" {
				req.Header.Set("X-Email-Verified", tc.emailVerified)
			}

			if tc.requestID != "" {
				req.Header.Set("X-Request-ID", tc.requestID)
			}

			if tc.ts != "" {
				req.Header.Set("X-Gateway-Ts", tc.ts)
			}

			if tc.sig != "" {
				req.Header.Set("X-Gateway-Signature", tc.sig)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}
