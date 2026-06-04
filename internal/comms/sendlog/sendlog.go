// Package sendlog persists the comms send-log (idempotency + delivery status)
// and the delivery receipts. It is the privacy boundary of the comms module:
//
//   - The recipient is stored ONLY as sha256(recipient) in to_hash — the
//     plaintext address/phone is NEVER written.
//   - The rendered body / template variables / OTP / token are NEVER written.
//   - last_error is run through comms.SanitizeError (credential redaction +
//     control-char strip) before persist.
//   - delivery_receipts.raw is sanitized before insert.
//
// (backend-security-design §3.1 / §5.4.)
package sendlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is a pgxpool-backed store over comms_send_log + comms_delivery_receipts.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a sendlog Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Ensure Store satisfies the orchestrator's persistence contract.
var _ comms.SendLogStore = (*Store)(nil)

// Reserve atomically claims an idempotency key by inserting a PENDING row.
// It returns (row, claimed=true) when this caller won the insert, or
// (existing, claimed=false) when the key already existed (a concurrent/duplicate
// send). The caller only proceeds to the provider when claimed is true; the
// loser short-circuits to a deduped result. Idempotency is enforced by the
// UNIQUE index on idempotency_key.
func (s *Store) Reserve(ctx context.Context, r *comms.SendLogRow) (existing *comms.SendLogRow, claimed bool, err error) {
	const insertQuery = `
INSERT INTO comms_send_log
    (id, idempotency_key, channel, provider, template_id, user_id, to_hash, status, attempts, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, now(), now())
ON CONFLICT (idempotency_key) DO NOTHING
`

	id := uuid.New()

	tag, execErr := s.pool.Exec(
		ctx, insertQuery,
		id, r.IdempotencyKey, string(r.Channel), r.Provider, r.TemplateID,
		r.UserID, r.ToHash, comms.StatusPending,
	)
	if execErr != nil {
		return nil, false, fmt.Errorf("reserve send_log: %w", execErr)
	}

	if tag.RowsAffected() == 1 {
		r.ID = id
		r.Status = comms.StatusPending

		return r, true, nil
	}

	// Key already existed — load and return the existing row as a dedup hit.
	ex, getErr := s.getByIdempotencyKey(ctx, r.IdempotencyKey)
	if getErr != nil {
		return nil, false, getErr
	}

	return ex, false, nil
}

// MarkResult updates a reserved row to its terminal status after a send attempt.
// providerMsgID may be empty; lastErr is sanitized here so callers cannot
// accidentally persist a raw provider error.
func (s *Store) MarkResult(ctx context.Context, id uuid.UUID, status, providerMsgID string, sendErr error) error {
	if status != comms.StatusSent && status != comms.StatusFailed && status != comms.StatusDead {
		return fmt.Errorf("%w: invalid send_log status %q", comms.ErrValidation, status)
	}

	var (
		msgID *string
		errTx *string
	)

	if providerMsgID != "" {
		msgID = &providerMsgID
	}

	if sendErr != nil {
		san := comms.SanitizeError(sendErr)
		errTx = &san
	}

	const query = `
UPDATE comms_send_log
SET status = $2,
    attempts = attempts + 1,
    provider_msg_id = COALESCE($3, provider_msg_id),
    last_error = $4,
    updated_at = now()
WHERE id = $1
`

	tag, err := s.pool.Exec(ctx, query, id, status, msgID, errTx)
	if err != nil {
		return fmt.Errorf("mark send_log result: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark send_log result: row %s not found", id)
	}

	return nil
}

// GetByIdempotencyKey returns the row for key, or errRowNotFound when absent.
// Exported for tests and the dedup path.
func (s *Store) GetByIdempotencyKey(ctx context.Context, key string) (*comms.SendLogRow, error) {
	return s.getByIdempotencyKey(ctx, key)
}

// errRowNotFound is returned internally when no send_log row matches.
var errRowNotFound = errors.New("sendlog: row not found")

func (s *Store) getByIdempotencyKey(ctx context.Context, key string) (*comms.SendLogRow, error) {
	const query = `
SELECT id, idempotency_key, channel, provider, template_id, user_id, to_hash,
       status, attempts, COALESCE(provider_msg_id, ''), COALESCE(last_error, ''),
       created_at, updated_at
FROM comms_send_log
WHERE idempotency_key = $1
`

	var (
		row     comms.SendLogRow
		chanStr string
	)

	err := s.pool.QueryRow(ctx, query, key).Scan(
		&row.ID, &row.IdempotencyKey, &chanStr, &row.Provider, &row.TemplateID,
		&row.UserID, &row.ToHash, &row.Status, &row.Attempts,
		&row.ProviderMsgID, &row.LastError, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errRowNotFound
		}

		return nil, fmt.Errorf("get send_log by idempotency key: %w", err)
	}

	row.Channel = comms.Channel(chanStr)

	return &row, nil
}
