// Package domain defines event envelope types matching CONVENTIONS §14.
package domain

import (
	"encoding/json"
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
