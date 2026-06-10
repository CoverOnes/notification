package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/handler"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fake store for handler unit tests ---

type fakeStore struct {
	notifications []*domain.Notification
}

func (f *fakeStore) Insert(_ context.Context, n *domain.Notification) error {
	f.notifications = append(f.notifications, n)
	return nil
}

func (f *fakeStore) List(_ context.Context, p store.ListParams) ([]*domain.Notification, error) {
	var result []*domain.Notification

	for _, n := range f.notifications {
		if n.UserID == p.UserID {
			result = append(result, n)
		}
	}

	return result, nil
}

func (f *fakeStore) UnreadCount(_ context.Context, userID uuid.UUID) (int64, error) {
	var count int64

	for _, n := range f.notifications {
		if n.UserID == userID && n.ReadAt == nil {
			count++
		}
	}

	return count, nil
}

func (f *fakeStore) MarkRead(_ context.Context, id, userID uuid.UUID) error {
	for _, n := range f.notifications {
		if n.ID == id && n.UserID == userID && n.ReadAt == nil {
			now := time.Now().UTC()
			n.ReadAt = &now

			return nil
		}
	}

	return domain.ErrNotificationNotFound
}

func (f *fakeStore) MarkAllRead(_ context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()

	for _, n := range f.notifications {
		if n.UserID == userID && n.ReadAt == nil {
			n.ReadAt = &now
		}
	}

	return nil
}

// buildRouter creates a router backed by the fake store (no pool/redis needed).
func buildRouter(s store.NotificationStore) http.Handler {
	notifH := handler.NewNotificationHandler(s)
	return notifH.BuildTestEngine()
}

func TestNotificationHandler_MarkRead_IDOR(t *testing.T) {
	ownerID := uuid.New()
	attackerID := uuid.New()

	eid := uuid.New()
	n := &domain.Notification{
		ID:            uuid.New(),
		UserID:        ownerID,
		Type:          domain.NotificationTypeKYCTierChanged,
		Title:         "Test",
		Body:          "Body",
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}

	fs := &fakeStore{notifications: []*domain.Notification{n}}
	engine := buildRouter(fs)

	// Attacker (different user) tries to mark owner's notification as read.
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/v1/me/notifications/"+n.ID.String()+"/read",
		http.NoBody,
	)
	req.Header.Set("X-User-Id", attackerID.String())
	req.Header.Set("X-Kyc-Tier", "0")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "IDOR: attacker must get 404, not 403")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "NOTIFICATION_NOT_FOUND", errObj["code"])
}

func TestNotificationHandler_MarkRead_NotFound(t *testing.T) {
	fs := &fakeStore{}
	engine := buildRouter(fs)

	userID := uuid.New()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/v1/me/notifications/"+uuid.New().String()+"/read",
		http.NoBody,
	)
	req.Header.Set("X-User-Id", userID.String())

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNotificationHandler_RequireIdentity(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		method     string
		wantStatus int
	}{
		{
			name:       "GET /v1/me/notifications without identity header returns 401",
			path:       "/v1/me/notifications",
			method:     http.MethodGet,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "GET /v1/me/notifications/unread-count without identity returns 401",
			path:       "/v1/me/notifications/unread-count",
			method:     http.MethodGet,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/me/notifications/read-all without identity returns 401",
			path:       "/v1/me/notifications/read-all",
			method:     http.MethodPost,
			wantStatus: http.StatusUnauthorized,
		},
	}

	fs := &fakeStore{}
	engine := buildRouter(fs)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, http.NoBody)
			// No X-User-Id header.
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

func TestNotificationHandler_MarkRead_InvalidUUID(t *testing.T) {
	fs := &fakeStore{}
	engine := buildRouter(fs)

	userID := uuid.New()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/v1/me/notifications/not-a-uuid/read",
		http.NoBody,
	)
	req.Header.Set("X-User-Id", userID.String())

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestNotificationHandler_List_DataFieldIsJSONObject asserts that the `data` field
// in the list response serializes as a JSON object (not a base64-encoded string).
// Regression test for audit finding: internal/handler/notification_handler.go:35,57 —
// assigning []byte to `any` causes encoding/json to emit base64; the fix is
// json.RawMessage so the JSON is passed through verbatim.
func TestNotificationHandler_List_DataFieldIsJSONObject(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantNil  bool
		wantJSON string // expected value of "data" key in the first item
	}{
		{
			name:     "non-nil data renders as JSON object not base64",
			data:     []byte(`{"newTier":3,"oldTier":1}`),
			wantNil:  false,
			wantJSON: `{"newTier":3,"oldTier":1}`,
		},
		{
			name:    "nil data omitted from response",
			data:    nil,
			wantNil: true,
		},
		{
			name:    "empty data omitted from response",
			data:    []byte{},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.New()
			eid := uuid.New()
			n := &domain.Notification{
				ID:            uuid.New(),
				UserID:        userID,
				Type:          domain.NotificationTypeKYCTierChanged,
				Title:         "KYC Level Updated",
				Body:          "Your KYC level has been updated.",
				Data:          tc.data,
				SourceEventID: &eid,
				CreatedAt:     time.Now().UTC(),
			}

			fs := &fakeStore{notifications: []*domain.Notification{n}}
			engine := buildRouter(fs)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				"/v1/me/notifications",
				http.NoBody,
			)
			req.Header.Set("X-User-Id", userID.String())
			req.Header.Set("X-Kyc-Tier", "0")

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var envelope struct {
				Data struct {
					Items []struct {
						Data json.RawMessage `json:"data"`
					} `json:"items"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Len(t, envelope.Data.Items, 1)

			item := envelope.Data.Items[0]

			if tc.wantNil {
				// data field must be absent (omitempty); json.RawMessage zero value is nil
				assert.Nil(t, item.Data, "data field must be omitted when notification has no data")
				return
			}

			// data must be a JSON object, never a base64 string.
			// A base64 string would start with '"'; an object starts with '{'.
			require.NotNil(t, item.Data)
			assert.Equal(t, byte('{'), item.Data[0],
				"data field must be a JSON object literal, not a base64-encoded string; got: %s", string(item.Data))

			// Verify the full JSON round-trips correctly.
			assert.JSONEq(t, tc.wantJSON, string(item.Data))
		})
	}
}

func TestNotificationHandler_List_CursorValidation(t *testing.T) {
	tests := []struct {
		name       string
		cursor     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "malformed timestamp in cursor returns 400",
			cursor:     "not-a-timestamp|" + uuid.New().String(),
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "cursor missing separator returns 400",
			cursor:     "noseparatorhere",
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "valid RFC3339Nano cursor is accepted",
			cursor:     "2024-01-15T12:00:00.000000000Z|" + uuid.New().String(),
			wantStatus: http.StatusOK,
			wantCode:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{}
			engine := buildRouter(fs)

			userID := uuid.New()
			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				"/v1/me/notifications?cursor="+tc.cursor,
				http.NoBody,
			)
			req.Header.Set("X-User-Id", userID.String())
			req.Header.Set("X-Kyc-Tier", "0")

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)

			if tc.wantCode != "" {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				errObj, ok := body["error"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, tc.wantCode, errObj["code"])
			}
		})
	}
}
