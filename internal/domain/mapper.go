// Package domain contains domain logic including event-to-notification mapping.
package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MapEventToNotification maps a parsed EventEnvelope from a Redis channel to a
// Notification struct ready for insertion. Returns an error if the payload is
// unrecognized or malformed.
//
// Note: kyc.status_changed is NOT routed through this function — the consumer
// handles it separately via handleKYCStatusChanged (HMAC-verified before mapping)
// and calls MapKYCStatusChanged directly with pre-parsed typed data.
func MapEventToNotification(channel string, env EventEnvelope) (*Notification, error) {
	switch channel {
	case "kyc.tier_changed":
		return mapKYCTierChanged(env)
	case "user.suspended":
		return mapUserSuspended(env)
	case "marketplace.bid_received":
		return mapBidReceived(env)
	case "marketplace.bid_accepted":
		return mapBidAccepted(env)
	case "workspace.milestone_reached":
		return mapMilestoneReached(env)
	case "workspace.contract_signed":
		return mapContractSigned(env)
	default:
		return nil, fmt.Errorf("unknown channel: %s", channel)
	}
}

// MapKYCStatusChanged maps a verified kyc.status_changed event to an inbox
// Notification. Called by the consumer AFTER HMAC verification succeeds.
//
// Title / body per product spec:
//   - NewStatus == "APPROVED" → "KYC Approved" + tier info
//   - NewStatus == "REJECTED" → "KYC Not Approved" + resubmit prompt
//   - else                   → generic status-changed message
func MapKYCStatusChanged(env EventEnvelope, data *KYCStatusChangedData) (*Notification, error) {
	if data.UserID == uuid.Nil {
		return nil, fmt.Errorf("kyc.status_changed: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("kyc.status_changed: missing eventId")
	}

	eid := env.EventID

	var title, body string

	switch data.NewStatus {
	case "APPROVED":
		title = "KYC Approved"
		body = fmt.Sprintf("Your identity has been verified. You are now KYC tier %d.", data.NewTier)
	case "REJECTED":
		title = "KYC Not Approved"
		body = "Your KYC submission was not approved. Please review the requirements and resubmit."
	default:
		title = "KYC Status Updated"
		body = fmt.Sprintf("Your KYC status has changed to %s.", data.NewStatus)
	}

	return &Notification{
		ID:            uuid.New(),
		UserID:        data.UserID,
		Type:          NotificationTypeKYCStatusChanged,
		Title:         title,
		Body:          body,
		Data:          nil, // PII §15: do NOT store raw event data (no email, no submission details)
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapKYCTierChanged(env EventEnvelope) (*Notification, error) {
	var d KYCTierChangedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal kyc.tier_changed data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("kyc.tier_changed: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("kyc.tier_changed: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeKYCTierChanged,
		Title:         "KYC Level Updated",
		Body:          fmt.Sprintf("Your KYC level has been updated from %d to %d.", d.OldTier, d.NewTier),
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapUserSuspended(env EventEnvelope) (*Notification, error) {
	var d UserSuspendedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal user.suspended data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("user.suspended: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("user.suspended: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeAccountSuspended,
		Title:         "Account Suspended",
		Body:          "Your account has been suspended. Please contact support.",
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapBidReceived(env EventEnvelope) (*Notification, error) {
	var d BidReceivedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal marketplace.bid_received data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("marketplace.bid_received: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("marketplace.bid_received: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeBidReceived,
		Title:         "New Bid Received",
		Body:          "You have received a new bid on your listing.",
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapBidAccepted(env EventEnvelope) (*Notification, error) {
	var d BidAcceptedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal marketplace.bid_accepted data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("marketplace.bid_accepted: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("marketplace.bid_accepted: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeBidAccepted,
		Title:         "Bid Accepted",
		Body:          "Your bid has been accepted.",
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapMilestoneReached(env EventEnvelope) (*Notification, error) {
	var d MilestoneReachedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal workspace.milestone_reached data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("workspace.milestone_reached: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("workspace.milestone_reached: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeMilestoneReached,
		Title:         "Milestone Reached",
		Body:          "A milestone in your contract has been reached.",
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func mapContractSigned(env EventEnvelope) (*Notification, error) {
	var d ContractSignedData

	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("unmarshal workspace.contract_signed data: %w", err)
	}

	if d.UserID == uuid.Nil {
		return nil, fmt.Errorf("workspace.contract_signed: missing userId")
	}

	if env.EventID == uuid.Nil {
		return nil, fmt.Errorf("workspace.contract_signed: missing eventId")
	}

	eid := env.EventID

	return &Notification{
		ID:            uuid.New(),
		UserID:        d.UserID,
		Type:          NotificationTypeContractSigned,
		Title:         "Contract Signed",
		Body:          "Your contract has been signed.",
		Data:          env.Data,
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}, nil
}
