// Package store defines the storage interfaces for the notification domain.
package store

import (
	"context"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/google/uuid"
)

// ListParams controls pagination for inbox queries.
type ListParams struct {
	UserID uuid.UUID
	// Cursor is a created_at value (RFC3339 nanosecond) + id for keyset pagination.
	// Empty means "from the beginning" (newest first).
	CursorCreatedAt *string
	CursorID        *uuid.UUID
	Limit           int
}

// NotificationStore defines persistence operations for notifications.
type NotificationStore interface {
	// Insert inserts a new notification.
	// ON CONFLICT (user_id, source_event_id) DO NOTHING guarantees idempotency.
	Insert(ctx context.Context, n *domain.Notification) error
	// List returns paginated notifications for a user, newest-first.
	List(ctx context.Context, p ListParams) ([]*domain.Notification, error)
	// UnreadCount returns the number of unread notifications for a user.
	UnreadCount(ctx context.Context, userID uuid.UUID) (int64, error)
	// MarkRead marks a single notification as read. Returns ErrNotificationNotFound if
	// the notification does not exist OR does not belong to userID (IDOR prevention).
	MarkRead(ctx context.Context, id, userID uuid.UUID) error
	// MarkAllRead marks all unread notifications for userID as read.
	MarkAllRead(ctx context.Context, userID uuid.UUID) error
}
