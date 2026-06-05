package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CoverOnes/notification/internal/events"
)

// countingStore records Insert calls so tests can assert whether an insert happened.
type countingStore struct {
	insertCount int
	lastInsert  *domain.Notification
}

func (s *countingStore) Insert(_ context.Context, n *domain.Notification) error {
	s.insertCount++
	s.lastInsert = n

	return nil
}

func (s *countingStore) List(_ context.Context, _ store.ListParams) ([]*domain.Notification, error) {
	return nil, nil
}

func (s *countingStore) UnreadCount(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

func (s *countingStore) MarkRead(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (s *countingStore) MarkAllRead(_ context.Context, _ uuid.UUID) error {
	return nil
}

// callHandle invokes the exported HandleForTest shim (see consumer_export_test.go).
// We use a package-level exported wrapper to avoid exposing handle() in production.

// TestConsumer_OversizedPayload verifies that a payload exceeding maxPayloadBytes
// on a subscribed channel is silently skipped (no insert, no panic).
func TestConsumer_OversizedPayload(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, nil)

	// Build a payload larger than 64 KiB.
	oversized := strings.Repeat("x", 64*1024+1)

	msg := &redis.Message{
		Channel: "kyc.tier_changed",
		Payload: oversized,
	}

	// Must not panic; consumer loop must skip the message.
	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 0, cs.insertCount, "oversized payload must not produce an insert")
}

// TestConsumer_ValidSmallPayload verifies that a well-formed, within-limit payload
// on a subscribed channel results in exactly one insert.
func TestConsumer_ValidSmallPayload(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, nil)

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

	assert.Equal(t, 1, cs.insertCount, "valid payload must produce exactly one insert")
}

// TestConsumer_MalformedJSON verifies that a malformed (non-JSON) payload is
// silently skipped — no insert, no panic.
func TestConsumer_MalformedJSON(t *testing.T) {
	cs := &countingStore{}
	c := events.NewConsumer(nil, cs, nil)

	msg := &redis.Message{
		Channel: "kyc.tier_changed",
		Payload: "not-valid-json{{{",
	}

	require.NotPanics(t, func() {
		c.HandleForTest(context.Background(), msg)
	})

	assert.Equal(t, 0, cs.insertCount, "malformed JSON must not produce an insert")
}
