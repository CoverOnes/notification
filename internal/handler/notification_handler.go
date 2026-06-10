// Package handler provides HTTP handlers for the notification service.
package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/platform/httpx"
	"github.com/CoverOnes/notification/internal/platform/middleware"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NotificationHandler handles notification HTTP endpoints.
type NotificationHandler struct {
	store store.NotificationStore
}

// NewNotificationHandler creates a NotificationHandler.
func NewNotificationHandler(s store.NotificationStore) *NotificationHandler {
	return &NotificationHandler{store: s}
}

// notificationResponse is the JSON shape returned for each notification.
type notificationResponse struct {
	ID            uuid.UUID               `json:"id"`
	Type          domain.NotificationType `json:"type"`
	Title         string                  `json:"title"`
	Body          string                  `json:"body"`
	Data          json.RawMessage         `json:"data,omitempty"`
	SourceEventID *uuid.UUID              `json:"sourceEventId,omitempty"`
	ReadAt        *string                 `json:"readAt,omitempty"` // RFC3339 or null
	CreatedAt     string                  `json:"createdAt"`        // RFC3339
}

func toResponse(n *domain.Notification) notificationResponse {
	r := notificationResponse{
		ID:            n.ID,
		Type:          n.Type,
		Title:         n.Title,
		Body:          n.Body,
		SourceEventID: n.SourceEventID,
		CreatedAt:     n.CreatedAt.Format("2006-01-02T15:04:05.999999999Z"),
	}

	if n.ReadAt != nil {
		s := n.ReadAt.Format("2006-01-02T15:04:05.999999999Z")
		r.ReadAt = &s
	}

	if len(n.Data) > 0 {
		r.Data = json.RawMessage(n.Data)
	}

	return r
}

// listResponse is the paginated list envelope.
type listResponse struct {
	Items      []notificationResponse `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

// List handles GET /v1/me/notifications
// Supports cursor pagination via ?cursor=<createdAt|id>&limit=<n>.
func (h *NotificationHandler) List(c *gin.Context) {
	identity, ok := middleware.IdentityFromCtx(c)
	if !ok {
		httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	// Limit with default 20, max 100.
	limitStr := c.DefaultQuery("limit", "20")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20
	}

	params := store.ListParams{
		UserID: identity.UserID,
		Limit:  limit,
	}

	// Cursor format: "<createdAt RFC3339nano>|<uuid>"
	cursor := c.Query("cursor")
	if cursor != "" {
		createdAt, cursorID, parseErr := parseCursor(cursor)
		if parseErr != nil {
			httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid cursor")
			return
		}

		params.CursorCreatedAt = &createdAt
		params.CursorID = &cursorID
	}

	notifications, listErr := h.store.List(c.Request.Context(), params)
	if listErr != nil {
		slog.Error("list notifications", "err", listErr, "user_id", identity.UserID)
		httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")

		return
	}

	resp := listResponse{
		Items: make([]notificationResponse, 0, len(notifications)),
	}

	for _, n := range notifications {
		resp.Items = append(resp.Items, toResponse(n))
	}

	// If we got exactly `limit` results, there may be more — return next cursor.
	if len(notifications) == limit {
		last := notifications[len(notifications)-1]
		resp.NextCursor = buildCursor(last.CreatedAt.Format("2006-01-02T15:04:05.999999999Z"), last.ID)
	}

	httpx.OK(c, resp)
}

// UnreadCount handles GET /v1/me/notifications/unread-count
func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	identity, ok := middleware.IdentityFromCtx(c)
	if !ok {
		httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	count, err := h.store.UnreadCount(c.Request.Context(), identity.UserID)
	if err != nil {
		slog.Error("unread count", "err", err, "user_id", identity.UserID)
		httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")

		return
	}

	httpx.OK(c, gin.H{"count": count})
}

// MarkRead handles POST /v1/me/notifications/:id/read
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	// Enforce body size limit (no body expected, but guard against abuse).
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	_, _ = io.ReadAll(c.Request.Body)

	identity, ok := middleware.IdentityFromCtx(c)
	if !ok {
		httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	rawID := c.Param("id")

	notifID, err := uuid.Parse(rawID)
	if err != nil {
		httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid notification id")
		return
	}

	if markErr := h.store.MarkRead(c.Request.Context(), notifID, identity.UserID); markErr != nil {
		httpx.Err(c, markErr)
		return
	}

	httpx.NoContent(c)
}

// MarkAllRead handles POST /v1/me/notifications/read-all
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	// Enforce body size limit.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	_, _ = io.ReadAll(c.Request.Body)

	identity, ok := middleware.IdentityFromCtx(c)
	if !ok {
		httpx.ErrCode(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	if err := h.store.MarkAllRead(c.Request.Context(), identity.UserID); err != nil {
		slog.Error("mark all read", "err", err, "user_id", identity.UserID)
		httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")

		return
	}

	httpx.NoContent(c)
}

// parseCursor splits "createdAt|uuid" into its parts.
// Returns an error if the createdAt portion is not a valid RFC3339Nano timestamp
// or the uuid portion is not a valid UUID.
func parseCursor(cursor string) (string, uuid.UUID, error) {
	// Find the last "|" separator — createdAt may contain colons.
	idx := -1

	for i := len(cursor) - 1; i >= 0; i-- {
		if cursor[i] == '|' {
			idx = i
			break
		}
	}

	if idx < 0 {
		return "", uuid.Nil, io.ErrUnexpectedEOF
	}

	createdAt := cursor[:idx]
	idStr := cursor[idx+1:]

	// Validate the timestamp so a malformed cursor yields 400 instead of a DB 500.
	if _, err := time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return "", uuid.Nil, err
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return "", uuid.Nil, err
	}

	return createdAt, id, nil
}

// buildCursor encodes (createdAt, id) as an opaque cursor string.
func buildCursor(createdAt string, id uuid.UUID) string {
	return createdAt + "|" + id.String()
}
