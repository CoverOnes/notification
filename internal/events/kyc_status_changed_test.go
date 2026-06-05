package events_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const kycEventHMACVal = "kyc-test-hmac-32bytes-01234567890"

// buildKYCStatusChangedPayload constructs a signed kyc.status_changed JSON payload.
// If forgeSignature is true, the signature field is set to a junk value.
func buildKYCStatusChangedPayload(t *testing.T, userID uuid.UUID, newStatus string, newTier int, forgeSignature bool) string {
	t.Helper()

	eventID := uuid.New()
	occurredAt := time.Now().UTC()

	data := map[string]any{
		"userId":       userID.String(),
		"oldStatus":    "PENDING",
		"newStatus":    newStatus,
		"oldTier":      0,
		"newTier":      newTier,
		"submissionId": uuid.New().String(),
		"requestId":    "req-test-unit",
	}

	dataBytes, err := json.Marshal(data)
	require.NoError(t, err)

	canonical := strings.Join([]string{
		eventID.String(),
		occurredAt.Format(time.RFC3339Nano),
		strconv.Itoa(1),
		userID.String(),
		newStatus,
		strconv.Itoa(newTier),
	}, "|")

	mac := hmac.New(sha256.New, []byte(kycEventHMACVal))
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))

	if forgeSignature {
		sig = "badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0bade"
	}

	payload := map[string]any{
		"eventId":    eventID.String(),
		"occurredAt": occurredAt.Format(time.RFC3339Nano),
		"version":    1,
		"data":       json.RawMessage(dataBytes),
		"signature":  sig,
	}

	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	return string(payloadBytes)
}

func TestConsumer_KYCStatusChanged_ValidHMAC_Approved(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	userID := uuid.New()
	payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, false)

	msg := &redis.Message{
		Channel: "kyc.status_changed",
		Payload: payload,
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 1, cs.insertCount, "valid APPROVED event must produce exactly one insert")
	require.NotNil(t, cs.lastInsert)
	assert.Equal(t, domain.NotificationTypeKYCStatusChanged, cs.lastInsert.Type)
	assert.Equal(t, "KYC Approved", cs.lastInsert.Title)
	assert.Equal(t, userID, cs.lastInsert.UserID)
}

func TestConsumer_KYCStatusChanged_ValidHMAC_Rejected(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	userID := uuid.New()
	payload := buildKYCStatusChangedPayload(t, userID, "REJECTED", 0, false)

	msg := &redis.Message{
		Channel: "kyc.status_changed",
		Payload: payload,
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 1, cs.insertCount, "valid REJECTED event must produce exactly one insert")
	require.NotNil(t, cs.lastInsert)
	assert.Equal(t, "KYC Not Approved", cs.lastInsert.Title)
}

func TestConsumer_KYCStatusChanged_WrongHMAC_Dropped(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	userID := uuid.New()
	payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, true /* forged */)

	msg := &redis.Message{
		Channel: "kyc.status_changed",
		Payload: payload,
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 0, cs.insertCount, "forged HMAC must drop event — no insert")
}

func TestConsumer_KYCStatusChanged_MissingSignature_Dropped(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	userID := uuid.New()

	// Manually craft payload without a signature field.
	data := map[string]any{
		"userId":       userID.String(),
		"oldStatus":    "PENDING",
		"newStatus":    "APPROVED",
		"oldTier":      0,
		"newTier":      1,
		"submissionId": uuid.New().String(),
		"requestId":    "req-test",
	}

	dataBytes, err := json.Marshal(data)
	require.NoError(t, err)

	payload := map[string]any{
		"eventId":    uuid.New().String(),
		"occurredAt": time.Now().UTC().Format(time.RFC3339Nano),
		"version":    1,
		"data":       json.RawMessage(dataBytes),
		// no "signature" field
	}

	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	msg := &redis.Message{
		Channel: "kyc.status_changed",
		Payload: string(payloadBytes),
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 0, cs.insertCount, "missing signature must drop event — no insert")
}

func TestConsumer_KYCStatusChanged_MalformedJSON_Dropped(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	msg := &redis.Message{
		Channel: "kyc.status_changed",
		Payload: "not-valid-json{{{",
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 0, cs.insertCount, "malformed JSON must drop event — no insert")
}

// TestConsumer_KYCTierChanged_Regression verifies that the existing
// kyc.tier_changed inbox path still works after the trust-C changes.
func TestConsumer_KYCTierChanged_Regression(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, []byte(kycEventHMACVal))

	userID := uuid.New()
	eventID := uuid.New()

	dataJSON := `{"userId":"` + userID.String() + `","oldTier":0,"newTier":1}`
	payload := `{"eventId":"` + eventID.String() + `","occurredAt":"2024-01-01T00:00:00Z","version":1,"data":` + dataJSON + `}`

	msg := &redis.Message{
		Channel: "kyc.tier_changed",
		Payload: payload,
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 1, cs.insertCount, "kyc.tier_changed regression: must still produce one insert")
	require.NotNil(t, cs.lastInsert)
	assert.Equal(t, domain.NotificationTypeKYCTierChanged, cs.lastInsert.Type)
}
