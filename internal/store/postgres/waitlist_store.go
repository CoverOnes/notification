package postgres

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WaitlistStore is a pool-backed waitlist store.
type WaitlistStore struct {
	pool *pgxpool.Pool
}

// NewWaitlistStore returns a WaitlistStore backed by pool.
func NewWaitlistStore(pool *pgxpool.Pool) *WaitlistStore {
	return &WaitlistStore{pool: pool}
}

// AddToWaitlist inserts a waitlist entry using a parameterized INSERT … ON CONFLICT
// DO NOTHING so that duplicate submissions (same email, case-insensitive) are
// silently discarded. created reports whether a new row was actually written.
//
// Privacy note: the caller MUST NOT vary the HTTP response based on the created
// value — returning the same 202 for new and duplicate submissions prevents email
// enumeration.
func (s *WaitlistStore) AddToWaitlist(ctx context.Context, entry *domain.Waitlist) (bool, error) {
	const query = `
INSERT INTO waitlist (id, email, company, interested_in, source, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (lower(email)) DO NOTHING
`

	tag, err := s.pool.Exec(
		ctx, query,
		entry.ID, entry.Email, entry.Company, entry.InterestedIn, entry.Source, entry.CreatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("add to waitlist: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}
