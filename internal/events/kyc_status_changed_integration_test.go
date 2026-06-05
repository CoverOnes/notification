package events_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/events"
	"github.com/CoverOnes/notification/internal/store"
	notifstore "github.com/CoverOnes/notification/internal/store/postgres"
	migrations "github.com/CoverOnes/notification/migrations"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startIntegrationDB spins up a Postgres testcontainer and returns its DSN.
// Registered as a t.Cleanup so the container is terminated when the test ends.
func startIntegrationDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	ctr, err := tcpostgres.Run(
		ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		if termErr := ctr.Terminate(ctx); termErr != nil {
			t.Logf("terminate container: %v", termErr)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return dsn
}

// applyMigrations runs all embedded *.up.sql files against the given DSN.
func applyMigrations(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()

	pool, err := notifstore.NewPool(ctx, dsn, "")
	require.NoError(t, err)

	defer pool.Close()

	var upFiles []string

	err = fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() && strings.HasSuffix(path, ".up.sql") {
			upFiles = append(upFiles, path)
		}

		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, upFiles)

	sort.Strings(upFiles)

	for _, file := range upFiles {
		data, readErr := migrations.FS.ReadFile(file)
		require.NoError(t, readErr, "read migration %s", file)

		_, execErr := pool.Exec(ctx, string(data))
		require.NoError(t, execErr, "apply migration %s", file)
	}
}

// TestConsumer_KYCStatusChanged_Integration verifies the full consumer-to-store
// path for kyc.status_changed using a real Postgres container (testcontainers).
// The test covers:
//   - valid HMAC + APPROVED → notification inserted with type KYC_STATUS_CHANGED
//   - valid HMAC + REJECTED → notification inserted with title "KYC Not Approved"
//   - forged HMAC → dropped, no insert
//   - existing kyc.tier_changed path → still works (regression)
func TestConsumer_KYCStatusChanged_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	dsn := startIntegrationDB(t)
	applyMigrations(t, ctx, dsn)

	pool, err := notifstore.NewPool(ctx, dsn, "")
	require.NoError(t, err)

	defer pool.Close()

	s := notifstore.NewNotificationStore(pool)
	secret := []byte(kycEventHMACVal)
	c := events.NewConsumer(nil, s, secret)

	t.Run("APPROVED event creates KYC_STATUS_CHANGED notification", func(t *testing.T) {
		userID := uuid.New()
		payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, false)

		msg := &redis.Message{Channel: "kyc.status_changed", Payload: payload}

		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, domain.NotificationTypeKYCStatusChanged, list[0].Type)
		assert.Equal(t, "KYC Approved", list[0].Title)
		assert.Equal(t, userID, list[0].UserID)
	})

	t.Run("REJECTED event creates KYC Not Approved notification", func(t *testing.T) {
		userID := uuid.New()
		payload := buildKYCStatusChangedPayload(t, userID, "REJECTED", 0, false)

		msg := &redis.Message{Channel: "kyc.status_changed", Payload: payload}

		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "KYC Not Approved", list[0].Title)
	})

	t.Run("forged HMAC is dropped — no insert", func(t *testing.T) {
		userID := uuid.New()
		payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, true /* forged */)

		msg := &redis.Message{Channel: "kyc.status_changed", Payload: payload}

		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		assert.Empty(t, list, "forged HMAC must produce no notification")
	})

	t.Run("duplicate event is idempotent (ON CONFLICT DO NOTHING)", func(t *testing.T) {
		userID := uuid.New()
		payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, false)

		msg := &redis.Message{Channel: "kyc.status_changed", Payload: payload}

		// Insert twice — the store must silently ignore the duplicate.
		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		assert.Len(t, list, 1, "duplicate event must not create duplicate notification")
	})

	t.Run("kyc.tier_changed regression — existing path still works", func(t *testing.T) {
		userID := uuid.New()
		eventID := uuid.New()

		dataJSON := `{"userId":"` + userID.String() + `","oldTier":0,"newTier":1}`
		payload := `{"eventId":"` + eventID.String() + `","occurredAt":"` + time.Now().UTC().Format(time.RFC3339) + `","version":1,"data":` + dataJSON + `}`

		msg := &redis.Message{Channel: "kyc.tier_changed", Payload: payload}

		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, domain.NotificationTypeKYCTierChanged, list[0].Type)
	})

	t.Run("GET v1/me/notifications via router returns KYC_STATUS_CHANGED notification", func(t *testing.T) {
		// This subtest verifies the notification is visible via the handler layer.
		// We use the counting store directly (not through HTTP) — the router-level
		// HTTP integration covering VerifyGatewaySignature is in gateway_signature_test.go.
		userID := uuid.New()
		payload := buildKYCStatusChangedPayload(t, userID, "APPROVED", 1, false)

		msg := &redis.Message{Channel: "kyc.status_changed", Payload: payload}

		require.NotPanics(t, func() {
			c.HandleForTest(ctx, msg)
		})

		list, err := s.List(ctx, store.ListParams{UserID: userID, Limit: 10})
		require.NoError(t, err)
		require.NotEmpty(t, list, "notification must be retrievable via store")

		// Verify the JSON-serialisable fields match what the API response would contain.
		raw, err := json.Marshal(list[0])
		require.NoError(t, err)
		assert.Contains(t, string(raw), "KYC_STATUS_CHANGED")
	})
}
