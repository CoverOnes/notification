package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/CoverOnes/notification/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

const (
	headerServiceID    = "X-Service-Id"
	headerServiceToken = "X-Service-Token" // header NAME (not a credential value)
	ctxKeyServiceID    = "service_id"
)

// RequireServiceIdentity is a deny-by-default service-to-service (S2S) guard for
// internal-only endpoints (e.g. POST /v1/comms/send). A caller MUST present:
//
//	X-Service-Id    — the caller's service identifier (used to look up its token)
//	X-Service-Token — the per-caller token from the tokenMap, compared in constant time
//
// tokenMap is a serviceID → token map. Each caller has its own independently
// rotatable token, so a single compromised credential does not affect other callers.
//
// Fail-closed rules:
//   - nil or empty tokenMap → every request is rejected (unavailable posture)
//   - unknown X-Service-Id → 401 (not in map)
//   - empty token in map for a service-id → 401 (misconfiguration treated as deny)
//   - wrong token → 401
//
// This endpoint is an arbitrary-send primitive (a spam relay if exposed to
// browsers), so the API gateway MUST NOT proxy it from the public edge — it is
// reachable only over the internal network by trusted services that hold the
// per-caller token (backend-security-design §5.5).
func RequireServiceIdentity(tokenMap map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(tokenMap) == 0 {
			c.Abort()
			httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "service authentication unavailable")

			return
		}

		serviceID := strings.TrimSpace(c.GetHeader(headerServiceID))
		token := c.GetHeader(headerServiceToken)

		if serviceID == "" {
			c.Abort()
			httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "service authentication required")

			return
		}

		expectedToken, known := tokenMap[serviceID]
		if !known || expectedToken == "" {
			c.Abort()
			httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "service authentication required")

			return
		}

		// Constant-time compare prevents timing side-channel.
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.Abort()
			httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "service authentication required")

			return
		}

		c.Set(ctxKeyServiceID, serviceID)
		c.Next()
	}
}

// ServiceIDFromCtx returns the authenticated caller service id set by
// RequireServiceIdentity, or "" if absent.
func ServiceIDFromCtx(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyServiceID); ok {
		if id, ok2 := v.(string); ok2 {
			return id
		}
	}

	return ""
}
