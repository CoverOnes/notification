package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCommsService is a programmable comms.CommsService for handler tests.
type fakeCommsService struct {
	res comms.SendResult
	err error
}

//nolint:gocritic // hugeParam: value receiver is fixed by the comms.CommsService interface
func (f *fakeCommsService) Send(_ context.Context, _ comms.SendRequest) (comms.SendResult, error) {
	return f.res, f.err
}

// newCommsEngine wires the comms handler directly (no S2S middleware — that is
// covered by the middleware test; here we exercise the handler logic).
func newCommsEngine(svc comms.CommsService) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	h := handler.NewCommsHandler(svc)
	r.POST("/v1/comms/send", h.Send)
	r.POST("/v1/comms/receipts/:provider", h.Receipts)

	return r
}

func TestCommsHandler_Send(t *testing.T) {
	sendID := uuid.New()

	tests := []struct {
		name       string
		body       string
		svc        comms.CommsService
		wantStatus int
		wantCode   string // error code (empty for success)
	}{
		{
			name:       "happy: 202 with sendId/status/deduped",
			body:       `{"channel":"SMS","to":"+15551234567","templateId":"phone_otp","idempotencyKey":"k1","vars":{"code":"123"}}`,
			svc:        &fakeCommsService{res: comms.SendResult{SendID: sendID, Status: comms.StatusSent}},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "validation: malformed JSON → 400 VALIDATION_ERROR",
			body:       `{not json`,
			svc:        &fakeCommsService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "validation: missing required field → 400",
			body:       `{"channel":"SMS","to":"+1"}`,
			svc:        &fakeCommsService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "validation: bad userId UUID → 400",
			body:       `{"channel":"SMS","to":"+1","templateId":"t","idempotencyKey":"k","userId":"not-a-uuid"}`,
			svc:        &fakeCommsService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "service validation error → 400",
			body:       `{"channel":"TELEPATHY","to":"+1","templateId":"t","idempotencyKey":"k"}`,
			svc:        &fakeCommsService{err: comms.ErrValidation},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "provider not integrated → 500 PROVIDER_UNAVAILABLE (no internals leaked)",
			body:       `{"channel":"EMAIL","to":"a@b.com","templateId":"t","idempotencyKey":"k"}`,
			svc:        &fakeCommsService{err: comms.ErrProviderNotIntegrated},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "PROVIDER_UNAVAILABLE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newCommsEngine(tc.svc)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/comms/send", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)

			if tc.wantCode != "" {
				assert.Contains(t, w.Body.String(), tc.wantCode)
				// Provider internals must never leak in the error body.
				assert.NotContains(t, w.Body.String(), "not integrated")
			}

			if tc.wantStatus == http.StatusAccepted {
				assert.Contains(t, w.Body.String(), sendID.String())
				assert.Contains(t, w.Body.String(), comms.StatusSent)
			}
		})
	}
}

func TestCommsHandler_Receipts_stub(t *testing.T) {
	r := newCommsEngine(&fakeCommsService{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/comms/receipts/ses", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_IMPLEMENTED")
}
