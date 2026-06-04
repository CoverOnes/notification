package sendlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/google/uuid"
)

// Receipt status normalization. Providers report many strings; we map to a small
// closed set matching the comms_delivery_receipts CHECK constraint.
const (
	ReceiptDelivered = "DELIVERED"
	ReceiptBounced   = "BOUNCED"
	ReceiptFailed    = "FAILED"
	ReceiptUnknown   = "UNKNOWN"
)

// NormalizeReceiptStatus maps a provider-reported status to the closed set.
// Unknown / empty inputs map to UNKNOWN (never rejected — we still record the
// receipt for audit).
func NormalizeReceiptStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "delivered", "delivery", "success", "sent", "ok":
		return ReceiptDelivered
	case "bounce", "bounced", "blocked", "spam":
		return ReceiptBounced
	case "failed", "failure", "undelivered", "rejected", "error":
		return ReceiptFailed
	default:
		return ReceiptUnknown
	}
}

// InsertReceipt records a delivery receipt. The raw payload is sanitized
// (credential redaction + control-char strip + length cap) before insert; an
// empty rawPayload stores SQL NULL. provider/providerMsgID are required.
func (s *Store) InsertReceipt(ctx context.Context, provider, providerMsgID, status, rawPayload string) (uuid.UUID, error) {
	if provider == "" || providerMsgID == "" {
		return uuid.Nil, fmt.Errorf("%w: provider and provider_msg_id are required", comms.ErrValidation)
	}

	normalized := NormalizeReceiptStatus(status)

	var raw *string

	if rawPayload != "" {
		// Sanitize the provider blob, then store it as a JSON string value so the
		// jsonb column always holds valid JSON even for non-JSON provider bodies.
		clean := comms.SanitizeText(rawPayload)

		encoded, err := json.Marshal(clean)
		if err != nil {
			return uuid.Nil, fmt.Errorf("encode receipt raw: %w", err)
		}

		js := string(encoded)
		raw = &js
	}

	const query = `
INSERT INTO comms_delivery_receipts (id, provider, provider_msg_id, status, raw, received_at)
VALUES ($1, $2, $3, $4, $5, now())
`

	id := uuid.New()

	if _, err := s.pool.Exec(ctx, query, id, provider, providerMsgID, normalized, raw); err != nil {
		return uuid.Nil, fmt.Errorf("insert delivery receipt: %w", err)
	}

	return id, nil
}

// retentionInterval is the observability-table retention window (30 days) —
// mirrors the `task db:gc` Taskfile target and the migration comments.
const retentionInterval = 30 * 24 * time.Hour

// PurgeExpired deletes send-log + delivery-receipt rows older than the 30-day
// retention window. It is the programmatic equivalent of the `task db:gc`
// target and is used by the integration test to prove the retention DELETE works
// against a real Postgres. cutoff defaults to now()-30d when zero.
func (s *Store) PurgeExpired(ctx context.Context, cutoff time.Time) (sendLogDeleted, receiptsDeleted int64, err error) {
	if cutoff.IsZero() {
		cutoff = time.Now().UTC().Add(-retentionInterval)
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM comms_send_log WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("purge send_log: %w", err)
	}

	sendLogDeleted = tag.RowsAffected()

	tag, err = s.pool.Exec(ctx, `DELETE FROM comms_delivery_receipts WHERE received_at < $1`, cutoff)
	if err != nil {
		return sendLogDeleted, 0, fmt.Errorf("purge delivery_receipts: %w", err)
	}

	receiptsDeleted = tag.RowsAffected()

	return sendLogDeleted, receiptsDeleted, nil
}
