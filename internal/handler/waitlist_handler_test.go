package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWaitlistStore is an in-memory WaitlistStore for handler unit tests.
type fakeWaitlistStore struct {
	emails   map[string]struct{}
	forceErr error
}

func newFakeWaitlistStore() *fakeWaitlistStore {
	return &fakeWaitlistStore{emails: make(map[string]struct{})}
}

func (f *fakeWaitlistStore) AddToWaitlist(_ context.Context, entry *domain.Waitlist) (bool, error) {
	if f.forceErr != nil {
		return false, f.forceErr
	}

	key := strings.ToLower(entry.Email)
	if _, exists := f.emails[key]; exists {
		return false, nil
	}

	f.emails[key] = struct{}{}

	return true, nil
}

// buildWaitlistEngine creates a minimal Gin engine for waitlist handler tests.
func buildWaitlistEngine(s *fakeWaitlistStore) http.Handler {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.SetTrustedProxies(nil) //nolint:errcheck // nil proxy list disables proxy trust; gin docs confirm error is always nil for nil argument

	waitlistH := handler.NewWaitlistHandler(s)
	r.POST("/v1/waitlist", waitlistH.Capture)

	return r
}

func TestWaitlistHandler_Capture(t *testing.T) {
	tests := []struct {
		name       string
		body       any
		wantStatus int
		wantCode   string // non-empty = expect error envelope with this code
		forceErr   error
		wantOK     bool // expect {data:{ok:true}} on success
	}{
		{
			name:       "valid email only — 202 ok",
			body:       map[string]string{"email": "alice@example.com"},
			wantStatus: http.StatusAccepted,
			wantOK:     true,
		},
		{
			name: "valid email with optional fields — 202 ok",
			body: map[string]string{
				"email":        "bob@example.com",
				"company":      "Acme Corp",
				"interestedIn": "risk-tools",
			},
			wantStatus: http.StatusAccepted,
			wantOK:     true,
		},
		{
			name:       "duplicate email — same 202 (privacy, no enumeration)",
			body:       map[string]string{"email": "dup@example.com"},
			wantStatus: http.StatusAccepted,
			wantOK:     true,
			// We'll call twice in the subtest body; both must be 202
		},
		{
			name:       "missing email field — 400 VALIDATION_ERROR",
			body:       map[string]string{"company": "Acme"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "empty email — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": ""},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email without @ — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "notanemail"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email without domain dot — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "user@nodot"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email exceeds 320 chars — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": strings.Repeat("a", 316) + "@x.co"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email with null byte — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "user\x00@example.com"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email with newline — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "user\n@example.com"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "email with carriage return — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "user\r@example.com"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "company exceeds 200 runes — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "x@example.com", "company": strings.Repeat("a", 201)},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "company with control char — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "x@example.com", "company": "Acme\x01Corp"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "interestedIn exceeds 200 runes — 400 VALIDATION_ERROR",
			body:       map[string]string{"email": "y@example.com", "interestedIn": strings.Repeat("b", 201)},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "store returns error — 500 INTERNAL_ERROR",
			body:       map[string]string{"email": "err@example.com"},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			forceErr:   errors.New("db connection lost"),
		},
		{
			name:       "non-JSON body — 400 VALIDATION_ERROR",
			body:       "plain text body",
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeWaitlistStore()
			s.forceErr = tc.forceErr
			engine := buildWaitlistEngine(s)

			var bodyBytes []byte
			var err error

			if str, ok := tc.body.(string); ok {
				bodyBytes = []byte(str)
			} else {
				bodyBytes, err = json.Marshal(tc.body)
				require.NoError(t, err)
			}

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/v1/waitlist",
				bytes.NewReader(bodyBytes),
			)
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)

			if tc.wantCode != "" {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				errObj, ok := body["error"].(map[string]any)
				require.True(t, ok, "expected error envelope but got: %s", w.Body.String())
				assert.Equal(t, tc.wantCode, errObj["code"])
			}

			if tc.wantOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				dataObj, ok := body["data"].(map[string]any)
				require.True(t, ok, "expected data envelope but got: %s", w.Body.String())
				assert.Equal(t, true, dataObj["ok"])
			}
		})
	}
}

func TestWaitlistHandler_DuplicatePrivacy(t *testing.T) {
	// Both first and second submission of the same email must return 202 {ok:true}.
	// The handler MUST NOT leak whether the email was already registered.
	s := newFakeWaitlistStore()
	engine := buildWaitlistEngine(s)

	sendCapture := func() *httptest.ResponseRecorder {
		bodyBytes, err := json.Marshal(map[string]string{"email": "dup@example.com"})
		require.NoError(t, err)

		req := httptest.NewRequestWithContext(
			context.Background(),
			http.MethodPost,
			"/v1/waitlist",
			bytes.NewReader(bodyBytes),
		)
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		return w
	}

	first := sendCapture()
	assert.Equal(t, http.StatusAccepted, first.Code, "first submission must be 202")

	second := sendCapture()
	assert.Equal(t, http.StatusAccepted, second.Code, "duplicate submission must also be 202 (privacy)")

	// Both responses must be identical — no "already registered" hint.
	assert.Equal(t, first.Body.String(), second.Body.String(), "response bodies must be identical for privacy")
}

func TestWaitlistHandler_NoAuthRequired(t *testing.T) {
	// This test verifies the endpoint is accessible WITHOUT any auth headers.
	// Absence of X-User-Id / X-Kyc-Tier must not cause a 401.
	s := newFakeWaitlistStore()
	engine := buildWaitlistEngine(s)

	bodyBytes, err := json.Marshal(map[string]string{"email": "public@example.com"})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/v1/waitlist",
		bytes.NewReader(bodyBytes),
	)
	req.Header.Set("Content-Type", "application/json")
	// Intentionally no X-User-Id, no X-Kyc-Tier, no auth headers.

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code, "public endpoint must not require auth headers")
}
