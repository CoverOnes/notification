package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/CoverOnes/notification/internal/store/postgres"
	migrations "github.com/CoverOnes/notification/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startTestDB spins up a real Postgres container via testcontainers.
func startTestDB(t *testing.T) string {
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

// runMigrations applies embedded *.up.sql files against the test DB.
func runMigrations(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()

	pool, err := postgres.NewPool(ctx, dsn, "") // empty schema = public (test default)
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
	require.NoError(t, err, "walk embedded migrations FS")
	require.NotEmpty(t, upFiles, "no *.up.sql files found")

	sort.Strings(upFiles)

	for _, file := range upFiles {
		data, readErr := migrations.FS.ReadFile(file)
		require.NoError(t, readErr, "read migration file %s", file)

		_, execErr := pool.Exec(ctx, string(data))
		require.NoError(t, execErr, fmt.Sprintf("apply migration %s", file))
	}
}

// makeNotification creates a test notification with a given userID.
func makeNotification(userID uuid.UUID, nType domain.NotificationType) *domain.Notification {
	eid := uuid.New()

	return &domain.Notification{
		ID:            uuid.New(),
		UserID:        userID,
		Type:          nType,
		Title:         "Test Title",
		Body:          "Test body.",
		Data:          json.RawMessage(`{"key":"value"}`),
		SourceEventID: &eid,
		CreatedAt:     time.Now().UTC(),
	}
}

func TestNotificationStore_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	dsn := startTestDB(t)
	runMigrations(t, ctx, dsn)

	pool, err := postgres.NewPool(ctx, dsn, "") // empty schema = public (test default)
	require.NoError(t, err)

	defer pool.Close()

	s := postgres.NewNotificationStore(pool)

	t.Run("Insert: stores a notification", func(t *testing.T) {
		uid := uuid.New()
		n := makeNotification(uid, domain.NotificationTypeKYCTierChanged)

		require.NoError(t, s.Insert(ctx, n))
	})

	t.Run("Insert: ON CONFLICT DO NOTHING for duplicate source_event_id", func(t *testing.T) {
		uid := uuid.New()
		n := makeNotification(uid, domain.NotificationTypeBidReceived)

		require.NoError(t, s.Insert(ctx, n))

		// Second insert with same source_event_id — should be no-op (no error).
		n2 := &domain.Notification{
			ID:            uuid.New(), // different row ID
			UserID:        uid,
			Type:          domain.NotificationTypeBidReceived,
			Title:         "Duplicate",
			Body:          "Should be ignored.",
			SourceEventID: n.SourceEventID, // same event ID
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, s.Insert(ctx, n2), "duplicate source_event_id must be silently ignored")

		// List should return only 1 notification for this user.
		list, err := s.List(ctx, store.ListParams{UserID: uid, Limit: 10})
		require.NoError(t, err)
		assert.Len(t, list, 1, "idempotent insert must not create a duplicate")
	})

	t.Run("List: returns notifications newest-first", func(t *testing.T) {
		uid := uuid.New()

		// Insert two notifications at different times.
		n1 := makeNotification(uid, domain.NotificationTypeKYCTierChanged)
		n1.CreatedAt = time.Now().UTC().Add(-2 * time.Minute)

		n2 := makeNotification(uid, domain.NotificationTypeBidReceived)
		n2.CreatedAt = time.Now().UTC()

		require.NoError(t, s.Insert(ctx, n1))
		require.NoError(t, s.Insert(ctx, n2))

		list, err := s.List(ctx, store.ListParams{UserID: uid, Limit: 10})
		require.NoError(t, err)
		require.Len(t, list, 2)
		// Newest first.
		assert.Equal(t, n2.ID, list[0].ID)
		assert.Equal(t, n1.ID, list[1].ID)
	})

	t.Run("List: empty result for unknown user", func(t *testing.T) {
		list, err := s.List(ctx, store.ListParams{UserID: uuid.New(), Limit: 10})
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("UnreadCount: counts unread notifications", func(t *testing.T) {
		uid := uuid.New()

		n1 := makeNotification(uid, domain.NotificationTypeContractSigned)
		n2 := makeNotification(uid, domain.NotificationTypeMilestoneReached)

		require.NoError(t, s.Insert(ctx, n1))
		require.NoError(t, s.Insert(ctx, n2))

		count, err := s.UnreadCount(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})

	t.Run("MarkRead: marks a notification as read", func(t *testing.T) {
		uid := uuid.New()
		n := makeNotification(uid, domain.NotificationTypeBidAccepted)

		require.NoError(t, s.Insert(ctx, n))

		require.NoError(t, s.MarkRead(ctx, n.ID, uid))

		count, err := s.UnreadCount(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})

	t.Run("MarkRead: IDOR — different user gets ErrNotificationNotFound", func(t *testing.T) {
		ownerID := uuid.New()
		attackerID := uuid.New()

		n := makeNotification(ownerID, domain.NotificationTypeAccountSuspended)
		require.NoError(t, s.Insert(ctx, n))

		// Attacker tries to mark owner's notification as read.
		err := s.MarkRead(ctx, n.ID, attackerID)
		require.ErrorIs(t, err, domain.ErrNotificationNotFound,
			"IDOR: different user must receive ErrNotificationNotFound, never 403")
	})

	t.Run("MarkRead: non-existent notification returns ErrNotificationNotFound", func(t *testing.T) {
		err := s.MarkRead(ctx, uuid.New(), uuid.New())
		require.ErrorIs(t, err, domain.ErrNotificationNotFound)
	})

	t.Run("MarkAllRead: marks all unread for user", func(t *testing.T) {
		uid := uuid.New()

		for range 3 {
			n := makeNotification(uid, domain.NotificationTypeKYCTierChanged)
			require.NoError(t, s.Insert(ctx, n))
		}

		count, err := s.UnreadCount(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, int64(3), count)

		require.NoError(t, s.MarkAllRead(ctx, uid))

		count, err = s.UnreadCount(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})
}
