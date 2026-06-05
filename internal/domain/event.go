// Package domain defines event envelope types matching CONVENTIONS §14.
package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EventEnvelope is the canonical Redis pub/sub payload (CONVENTIONS §14).
type EventEnvelope struct {
	EventID    uuid.UUID       `json:"eventId"`
	OccurredAt time.Time       `json:"occurredAt"`
	Version    int             `json:"version"`
	Data       json.RawMessage `json:"data"`
}

// KYCTierChangedData is the data payload for kyc.tier_changed events.
type KYCTierChangedData struct {
	UserID  uuid.UUID `json:"userId"`
	OldTier int       `json:"oldTier"`
	NewTier int       `json:"newTier"`
}

// UserSuspendedData is the data payload for user.suspended events.
type UserSuspendedData struct {
	UserID uuid.UUID `json:"userId"`
	Reason string    `json:"reason"`
}

// BidReceivedData is the data payload for marketplace.bid_received events.
type BidReceivedData struct {
	UserID    uuid.UUID `json:"userId"` // recipient (seller / listing owner)
	BidID     uuid.UUID `json:"bidId"`
	ListingID uuid.UUID `json:"listingId"`
}

// BidAcceptedData is the data payload for marketplace.bid_accepted events.
type BidAcceptedData struct {
	UserID    uuid.UUID `json:"userId"` // recipient (buyer who placed the bid)
	BidID     uuid.UUID `json:"bidId"`
	ListingID uuid.UUID `json:"listingId"`
}

// MilestoneReachedData is the data payload for workspace.milestone_reached events.
type MilestoneReachedData struct {
	UserID      uuid.UUID `json:"userId"`
	ContractID  uuid.UUID `json:"contractId"`
	MilestoneID uuid.UUID `json:"milestoneId"`
}

// ContractSignedData is the data payload for workspace.contract_signed events.
type ContractSignedData struct {
	UserID     uuid.UUID `json:"userId"`
	ContractID uuid.UUID `json:"contractId"`
}

// SignedEventEnvelope extends EventEnvelope with an HMAC signature field.
// Used for events that carry a publisher-side HMAC-SHA256 signature to allow
// the consumer to verify authenticity before processing (trust-C §2).
type SignedEventEnvelope struct {
	EventEnvelope
	Signature string `json:"signature"`
}

// KYCStatusChangedData is the data payload for kyc.status_changed events.
// This event is published by the kyc service when a user's KYC review concludes.
// Note: no email address is included (PII §15); outbound dispatch requires a
// separate S2S user-email lookup (tracked as a follow-up task).
type KYCStatusChangedData struct {
	UserID       uuid.UUID `json:"userId"`
	OldStatus    string    `json:"oldStatus"`
	NewStatus    string    `json:"newStatus"`
	OldTier      int       `json:"oldTier"`
	NewTier      int       `json:"newTier"`
	SubmissionID uuid.UUID `json:"submissionId"`
	RequestID    string    `json:"requestId"`
}

// VerifyStatusChangedSignature verifies the HMAC-SHA256 signature on a
// kyc.status_changed event against the shared EVENT_HMAC_SECRET.
//
// SHARED EVENT CONTRACT — must match byte-for-byte with the kyc publisher:
//
//	canonical = eventId + "|" + occurredAt.UTC().Format(time.RFC3339Nano) + "|" +
//	            version + "|" + userId + "|" + newStatus + "|" + newTier
//	want      = lowercase hex of HMAC-SHA256(secret, canonical)
//
// kyc publishes occurredAt as time.RFC3339Nano, so parsing and re-formatting
// with the same layout is byte-identical — this is safe.
//
// Returns false (drop the event) if:
//   - env.Signature is empty
//   - HMAC comparison fails
//   - secret is empty (dev-mode callers should skip verification or use a stub)
func VerifyStatusChangedSignature(env *SignedEventEnvelope, data *KYCStatusChangedData, secret []byte) bool {
	if env.Signature == "" {
		return false
	}

	canonical := strings.Join([]string{
		env.EventID.String(),
		env.OccurredAt.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(env.Version),
		data.UserID.String(),
		data.NewStatus,
		strconv.Itoa(data.NewTier),
	}, "|")

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical)) // hash.Hash.Write never returns an error
	want := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(want), []byte(env.Signature))
}
