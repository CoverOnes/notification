package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationStore is a pool-backed notification store.
type NotificationStore struct {
	pool *pgxpool.Pool
}

// NewNotificationStore returns a NotificationStore backed by pool.
func NewNotificationStore(pool *pgxpool.Pool) *NotificationStore {
	return &NotificationStore{pool: pool}
}

// Insert inserts a notification. ON CONFLICT DO NOTHING ensures idempotency for
// duplicate source_event_id values (Redis event replay / at-least-once delivery).
func (s *NotificationStore) Insert(ctx context.Context, n *domain.Notification) error {
	const query = `
INSERT INTO notifications
    (id, user_id, type, title, body, data, source_event_id, read_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (user_id, source_event_id) WHERE source_event_id IS NOT NULL DO NOTHING
`

	_, err := s.pool.Exec(
		ctx, query,
		n.ID, n.UserID, string(n.Type), n.Title, n.Body,
		n.Data, n.SourceEventID, n.ReadAt, n.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}

	return nil
}

// List returns paginated notifications for a user, newest-first.
// Cursor pagination uses (created_at, id) to guarantee stable ordering.
func (s *NotificationStore) List(ctx context.Context, p store.ListParams) ([]*domain.Notification, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var (
		rows pgx.Rows
		err  error
	)

	if p.CursorCreatedAt != nil && p.CursorID != nil {
		const query = `
SELECT id, user_id, type, title, body, data, source_event_id, read_at, created_at
FROM notifications
WHERE user_id = $1
  AND (created_at, id) < ($2::timestamptz, $3)
ORDER BY created_at DESC, id DESC
LIMIT $4
`
		rows, err = s.pool.Query(ctx, query, p.UserID, *p.CursorCreatedAt, *p.CursorID, limit)
	} else {
		const query = `
SELECT id, user_id, type, title, body, data, source_event_id, read_at, created_at
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2
`
		rows, err = s.pool.Query(ctx, query, p.UserID, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}

	defer rows.Close()

	var result []*domain.Notification

	for rows.Next() {
		n, scanErr := scanNotification(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		result = append(result, n)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}

	return result, nil
}

// UnreadCount returns the count of unread notifications for a user.
func (s *NotificationStore) UnreadCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	const query = `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`

	var count int64

	if err := s.pool.QueryRow(ctx, query, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("unread count: %w", err)
	}

	return count, nil
}

// MarkRead marks a single notification as read. Returns ErrNotificationNotFound
// when the row does not exist OR belongs to a different user (IDOR prevention —
// never leak existence via 403).
func (s *NotificationStore) MarkRead(ctx context.Context, id, userID uuid.UUID) error {
	const query = `
UPDATE notifications
SET read_at = $3
WHERE id = $1 AND user_id = $2 AND read_at IS NULL
`

	tag, err := s.pool.Exec(ctx, query, id, userID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}

	// RowsAffected == 0 either means: not found, wrong owner, or already read.
	// In all cases return ErrNotificationNotFound (no existence leak).
	if tag.RowsAffected() == 0 {
		return domain.ErrNotificationNotFound
	}

	return nil
}

// MarkAllRead marks all unread notifications for a user as read.
func (s *NotificationStore) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	const query = `UPDATE notifications SET read_at = $2 WHERE user_id = $1 AND read_at IS NULL`

	_, err := s.pool.Exec(ctx, query, userID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row rowScanner) (*domain.Notification, error) {
	var n domain.Notification

	var typeStr string

	err := row.Scan(
		&n.ID, &n.UserID, &typeStr, &n.Title, &n.Body,
		&n.Data, &n.SourceEventID, &n.ReadAt, &n.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotificationNotFound
		}

		return nil, fmt.Errorf("scan notification: %w", err)
	}

	n.Type = domain.NotificationType(typeStr)

	return &n, nil
}
