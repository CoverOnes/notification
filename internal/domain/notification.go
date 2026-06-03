// Package domain defines the core domain types for the notification service.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// NotificationType is the set of allowed notification types.
type NotificationType string

const (
	// NotificationTypeKYCTierChanged is fired when a user's KYC tier changes.
	NotificationTypeKYCTierChanged NotificationType = "KYC_TIER_CHANGED"
	// NotificationTypeBidReceived fires when a user receives a bid.
	NotificationTypeBidReceived NotificationType = "BID_RECEIVED"
	// NotificationTypeBidAccepted fires when a bid is accepted.
	NotificationTypeBidAccepted NotificationType = "BID_ACCEPTED"
	// NotificationTypeMilestoneReached fires when a milestone is reached.
	NotificationTypeMilestoneReached NotificationType = "MILESTONE_REACHED"
	// NotificationTypeContractSigned fires when a contract is signed.
	NotificationTypeContractSigned NotificationType = "CONTRACT_SIGNED"
	// NotificationTypeAccountSuspended fires when an account is suspended.
	NotificationTypeAccountSuspended NotificationType = "ACCOUNT_SUSPENDED"
)

// Sentinel errors for domain operations.
var (
	// ErrNotificationNotFound is returned when a notification cannot be found.
	// IDOR prevention: this is the only sentinel returned for both "not found"
	// and "belongs to a different user" cases — callers never see ErrForbidden.
	ErrNotificationNotFound = errors.New("notification not found")
)

// Notification is the core domain object.
type Notification struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Type          NotificationType
	Title         string
	Body          string
	Data          []byte     // raw JSON (jsonb), may be nil
	SourceEventID *uuid.UUID // Redis event eventId; used for idempotency
	ReadAt        *time.Time
	CreatedAt     time.Time
}
