package handler_test

// Regression tests for the ClientIP trust-chain fix (downstream of the gateway
// ClientIP keystone).
//
// Root cause being fixed: NewRouter previously called SetTrustedProxies(nil)
// unconditionally. Behind the API gateway, every request's RemoteAddr is the
// gateway's egress IP, so c.ClientIP() returned the gateway IP for ALL clients.
// The per-IP rate limiters (global 120/min AND the waitlist 5/min limiter) then
// collapsed to a single global bucket keyed on the gateway IP — a self-DoS where
// one busy client throttles everyone, and per-IP table-fill protection vanishes.
//
// Fix: when GatewayCIDR is set, NewRouter calls SetTrustedProxies([GatewayCIDR])
// so Gin honors X-Forwarded-For from the trusted gateway CIDR and c.ClientIP()
// resolves to the real end-user IP. When empty, it falls back to nil (XFF ignored,
// RemoteAddr used) — the safe dev default.
//
// These tests prove the BEHAVIOR (not just no-panic) by exercising the waitlist
// 5/min per-IP limiter through the real router, with a nil Redis client so the
// in-process fallback limiter keys directly on c.ClientIP(). The fallback bucket
// burst is 10 (middleware.fallbackBurst), so the 11th request from a single
// distinct ClientIP key is rejected with 429; requests spread across distinct
// ClientIP keys each get their own bucket and are not throttled.
//
// Pure HTTP-layer unit tests: no DB, no testcontainer (fakeWaitlistStore +
// newFakeWaitlistStore are defined in waitlist_handler_test.go, same package).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CoverOnes/notification/internal/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fallbackBurst mirrors middleware.fallbackBurst (the in-process limiter burst).
// The 11th request against a single ClientIP key trips 429; 1..10 pass.
const proxyTestFallbackBurst = 10

// trustProxyRouterCfg builds a RouterConfig wired with a fake waitlist store and
// the given GatewayCIDR. Redis is nil so the in-process fallback limiter (keyed on
// c.ClientIP()) is exercised, making per-IP behavior deterministic.
func trustProxyRouterCfg(gatewayCIDR string) *handler.RouterConfig {
	return &handler.RouterConfig{
		WaitlistStore:     newFakeWaitlistStore(),
		Redis:             nil, // nil -> in-process fallback limiter keyed on c.ClientIP()
		GatewayHMACSecret: "",  // dev posture — no gateway HMAC verification
		GatewayCIDR:       gatewayCIDR,
	}
}

// postWaitlist sends one POST /v1/waitlist with a unique email, simulating a
// request arriving from peerAddr (RemoteAddr) carrying the given X-Forwarded-For.
// Returns the HTTP status code.
func postWaitlist(t *testing.T, r http.Handler, peerAddr, xff string, seq int) int {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"email": fmt.Sprintf("user%d@example.com", seq),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/v1/waitlist", bytes.NewReader(payload),
	)
	req.RemoteAddr = peerAddr
	req.Header.Set("Content-Type", "application/json")

	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w.Code
}

// TestNewRouter_TrustedProxy_ClientIPResolution is table-driven over the three
// trust-chain scenarios plus a sanity baseline, asserting the rate-limit outcome
// that PROVES how c.ClientIP() resolved.
func TestNewRouter_TrustedProxy_ClientIPResolution(t *testing.T) {
	const gatewayPeer = "10.1.2.3:54321" // simulated gateway egress peer (inside 10.0.0.0/8)

	tests := []struct {
		name string
		// distinctXFF: when true, each of the N requests carries a different
		// X-Forwarded-For client IP; when false, all share one XFF value.
		gatewayCIDR string
		distinctXFF bool
		// requests is how many POSTs to send from gatewayPeer.
		requests int
		// wantFinal429 is the expected outcome of the LAST request.
		wantFinal429 bool
		reason       string
	}{
		{
			// CIDR trusted + distinct client IPs -> each XFF is its own ClientIP
			// key -> N independent buckets -> none throttled. Proves XFF is HONORED
			// (the bug fix): the real client IP drives the limiter, not the gateway.
			name:         "trusted_cidr_distinct_clients_not_throttled",
			gatewayCIDR:  "10.0.0.0/8",
			distinctXFF:  true,
			requests:     proxyTestFallbackBurst + 1, // 11 distinct IPs
			wantFinal429: false,
			reason:       "distinct trusted XFF client IPs each get their own per-IP bucket",
		},
		{
			// CIDR trusted + SAME client IP repeated -> one ClientIP key -> 11th
			// request exhausts the burst-10 bucket -> 429. Proves c.ClientIP()
			// resolved to the FORWARDED client IP (the limiter keyed on the XFF value).
			name:         "trusted_cidr_same_client_throttled",
			gatewayCIDR:  "10.0.0.0/8",
			distinctXFF:  false,
			requests:     proxyTestFallbackBurst + 1, // 11 from one client IP
			wantFinal429: true,
			reason:       "a single trusted forwarded client IP shares one per-IP bucket",
		},
		{
			// CIDR empty (dev/unset) + distinct client IPs from one peer -> XFF
			// IGNORED -> all collapse to the gateway peer's ClientIP -> one bucket
			// -> 11th request is 429. This is exactly the pre-fix self-DoS: distinct
			// real clients throttle each other because trust is disabled.
			name:         "untrusted_xff_ignored_clients_collapse_throttled",
			gatewayCIDR:  "",
			distinctXFF:  true,
			requests:     proxyTestFallbackBurst + 1, // 11 distinct XFF, one peer
			wantFinal429: true,
			reason:       "without proxy trust, XFF is ignored and all share the peer IP bucket",
		},
		{
			// Sanity baseline: trusted CIDR, distinct clients, but only burst-many
			// requests from any single one -> never throttled. Guards against a
			// future change that accidentally over-throttles legitimate traffic.
			name:         "trusted_cidr_under_burst_never_throttled",
			gatewayCIDR:  "10.0.0.0/8",
			distinctXFF:  true,
			requests:     proxyTestFallbackBurst, // exactly burst, distinct IPs
			wantFinal429: false,
			reason:       "distinct clients under the per-IP burst are always served",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := handler.NewRouter(trustProxyRouterCfg(tc.gatewayCIDR))

			var lastCode int
			for i := range tc.requests {
				xff := "203.0.113.42" // RFC 5737 TEST-NET-3 fixed client IP
				if tc.distinctXFF {
					xff = fmt.Sprintf("203.0.113.%d", i+1)
				}

				lastCode = postWaitlist(t, r, gatewayPeer, xff, i)
			}

			if tc.wantFinal429 {
				assert.Equal(t, http.StatusTooManyRequests, lastCode,
					"%s: final request should be rate-limited (429)", tc.reason)
			} else {
				assert.NotEqual(t, http.StatusTooManyRequests, lastCode,
					"%s: final request must NOT be rate-limited", tc.reason)
				assert.Equal(t, http.StatusAccepted, lastCode,
					"%s: final request should succeed with 202", tc.reason)
			}
		})
	}
}

// TestNewRouter_TrustedProxy_InvalidCIDR_Panics proves an invalid GatewayCIDR
// panics at startup, surfacing a config bug immediately rather than booting with
// wrong proxy trust. (config.validateGatewayCIDR also rejects it at boot; this is
// the router's defense-in-depth guard.)
func TestNewRouter_TrustedProxy_InvalidCIDR_Panics(t *testing.T) {
	cfg := trustProxyRouterCfg("not-a-cidr")

	assert.Panics(t, func() {
		handler.NewRouter(cfg)
	}, "NewRouter with an invalid GatewayCIDR must panic to surface the config bug at boot")
}

// TestNewRouter_NilWaitlistStore_NoWaitlistRoute proves that when WaitlistStore is
// nil the POST /v1/waitlist route is not registered (404), matching the S-3 path
// where NewRouter emits a slog.Warn so the disabled route is not silent.
func TestNewRouter_NilWaitlistStore_NoWaitlistRoute(t *testing.T) {
	cfg := &handler.RouterConfig{
		WaitlistStore:     nil, // disabled -> route not registered + slog.Warn at startup
		Redis:             nil,
		GatewayHMACSecret: "",
		GatewayCIDR:       "10.0.0.0/8",
	}

	r := handler.NewRouter(cfg)

	payload, err := json.Marshal(map[string]string{"email": "nobody@example.com"})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/v1/waitlist", bytes.NewReader(payload),
	)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"with a nil WaitlistStore the waitlist route must not be registered")
}
