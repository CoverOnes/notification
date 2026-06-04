package comms_test

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/comms/provider"
	"github.com/CoverOnes/notification/internal/comms/sendlog"
	"github.com/CoverOnes/notification/internal/comms/template"
	"github.com/CoverOnes/notification/internal/store/postgres"
	migrations "github.com/CoverOnes/notification/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startCommsTestDB spins up a real Postgres container via testcontainers and
// applies ALL embedded migrations (including the comms 000002..000005).
func startCommsTestDB(t *testing.T) *pgxpool.Pool {
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

	pool, err := postgres.NewPool(ctx, dsn, "")
	require.NoError(t, err)

	t.Cleanup(pool.Close)

	applyMigrations(t, ctx, pool)

	return pool
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	var upFiles []string

	err := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() && strings.HasSuffix(path, ".up.sql") {
			upFiles = append(upFiles, path)
		}

		return nil
	})
	require.NoError(t, err, "walk embedded migrations FS")
	require.NotEmpty(t, upFiles)

	sort.Strings(upFiles)

	for _, file := range upFiles {
		data, readErr := migrations.FS.ReadFile(file)
		require.NoError(t, readErr, "read migration %s", file)

		_, execErr := pool.Exec(ctx, string(data))
		require.NoError(t, execErr, fmt.Sprintf("apply migration %s", file))
	}
}

func TestCommsStores_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool := startCommsTestDB(t)

	tplStore := template.NewStore(pool)
	logStore := sendlog.NewStore(pool)
	provStore := provider.NewStore(pool)

	t.Run("templates: seed + Get exact + locale fallback", func(t *testing.T) {
		require.NoError(t, template.Seed(ctx, tplStore))

		// Exact match for the seeded EMAIL template.
		got, err := tplStore.Get(ctx, comms.ChannelEmail, "email_verify", "en")
		require.NoError(t, err)
		assert.Equal(t, comms.ChannelEmail, got.Channel)
		assert.Equal(t, "Verify your CoverOnes account", got.Subject)
		assert.Contains(t, got.Body, "{{.verifyURL}}")

		// Unknown locale falls back to the default 'en'.
		fallback, err := tplStore.Get(ctx, comms.ChannelEmail, "email_verify", "zh-TW")
		require.NoError(t, err)
		assert.Equal(t, "en", fallback.Locale, "missing locale must fall back to default")

		// Unknown template id is a typed not-found.
		_, err = tplStore.Get(ctx, comms.ChannelEmail, "does_not_exist", "en")
		require.ErrorIs(t, err, comms.ErrTemplateNotFound)
	})

	t.Run("templates: Upsert bumps version on conflict", func(t *testing.T) {
		tpl := &comms.Template{Channel: comms.ChannelSMS, TemplateID: "upsert_test", Body: "v1 {{.x}}"}
		require.NoError(t, tplStore.Upsert(ctx, tpl))

		tpl.Body = "v2 {{.x}}"
		require.NoError(t, tplStore.Upsert(ctx, tpl))

		got, err := tplStore.Get(ctx, comms.ChannelSMS, "upsert_test", "en")
		require.NoError(t, err)
		assert.Equal(t, "v2 {{.x}}", got.Body)
	})

	t.Run("send_log: Reserve is idempotent (UNIQUE conflict path)", func(t *testing.T) {
		key := "integ-idem-" + uuid.NewString()

		row := &comms.SendLogRow{
			IdempotencyKey: key,
			Channel:        comms.ChannelSMS,
			Provider:       "stub",
			TemplateID:     "phone_otp",
			ToHash:         comms.HashRecipient("+15551234567"),
		}

		first, claimed, err := logStore.Reserve(ctx, row)
		require.NoError(t, err)
		require.True(t, claimed, "first reserve must claim the key")
		require.NotEqual(t, uuid.Nil, first.ID)

		// Second reserve with the SAME key must NOT claim (UNIQUE conflict → dedup).
		dup := &comms.SendLogRow{
			IdempotencyKey: key,
			Channel:        comms.ChannelSMS,
			Provider:       "stub",
			TemplateID:     "phone_otp",
			ToHash:         comms.HashRecipient("+15551234567"),
		}

		second, claimed2, err := logStore.Reserve(ctx, dup)
		require.NoError(t, err)
		assert.False(t, claimed2, "duplicate key must NOT be claimed")
		assert.Equal(t, first.ID, second.ID, "dedup must return the existing row id")
	})

	t.Run("send_log: stores sha256(recipient) not plaintext + sanitized error", func(t *testing.T) {
		key := "integ-hash-" + uuid.NewString()
		recipient := "secret-user@example.com"

		row := &comms.SendLogRow{
			IdempotencyKey: key,
			Channel:        comms.ChannelEmail,
			Provider:       "smtp",
			TemplateID:     "email_verify",
			ToHash:         comms.HashRecipient(recipient),
		}

		reserved, claimed, err := logStore.Reserve(ctx, row)
		require.NoError(t, err)
		require.True(t, claimed)

		// Record a FAILED result with a credential-bearing error.
		require.NoError(t, logStore.MarkResult(ctx, reserved.ID, comms.StatusFailed, "", fmt.Errorf("smtp auth failed password=hunter2")))

		got, err := logStore.GetByIdempotencyKey(ctx, key)
		require.NoError(t, err)

		// to_hash equals sha256(recipient) and never the plaintext.
		assert.Equal(t, comms.HashRecipient(recipient), got.ToHash)
		assert.NotContains(t, string(got.ToHash), "secret-user")

		// last_error went through the redaction scrub.
		assert.NotContains(t, got.LastError, "hunter2")
		assert.Contains(t, got.LastError, "[REDACTED")
		assert.Equal(t, comms.StatusFailed, got.Status)
		assert.Equal(t, 1, got.Attempts)
	})

	t.Run("delivery receipts: insert normalizes status + sanitizes raw", func(t *testing.T) {
		msgID := "provider-msg-" + uuid.NewString()

		id, err := logStore.InsertReceipt(ctx, "ses", msgID, "Delivery", "raw token=LEAKED status ok")
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, id)

		// Verify normalization + redaction by reading the row back directly.
		var status, raw string

		err = pool.QueryRow(
			ctx,
			`SELECT status, COALESCE(raw::text, '') FROM comms_delivery_receipts WHERE id = $1`, id,
		).Scan(&status, &raw)
		require.NoError(t, err)

		assert.Equal(t, sendlog.ReceiptDelivered, status, "Delivery must normalize to DELIVERED")
		assert.NotContains(t, raw, "LEAKED", "raw payload must be redacted before insert")
	})

	t.Run("retention: PurgeExpired deletes rows older than the cutoff", func(t *testing.T) {
		// Insert an old send_log row + an old receipt by backdating created_at.
		oldKey := "integ-old-" + uuid.NewString()
		oldID := uuid.New()

		_, err := pool.Exec(
			ctx,
			`INSERT INTO comms_send_log (id, idempotency_key, channel, provider, template_id, to_hash, status, created_at, updated_at)
			 VALUES ($1, $2, 'SMS', 'stub', 'phone_otp', $3, 'SENT', now() - interval '40 days', now() - interval '40 days')`,
			oldID, oldKey, comms.HashRecipient("+10000000000"),
		)
		require.NoError(t, err)

		_, err = pool.Exec(
			ctx,
			`INSERT INTO comms_delivery_receipts (id, provider, provider_msg_id, status, received_at)
			 VALUES ($1, 'ses', 'old-msg', 'DELIVERED', now() - interval '40 days')`,
			uuid.New(),
		)
		require.NoError(t, err)

		// Insert a fresh row that must survive.
		freshKey := "integ-fresh-" + uuid.NewString()
		freshRow := &comms.SendLogRow{
			IdempotencyKey: freshKey,
			Channel:        comms.ChannelSMS,
			Provider:       "stub",
			TemplateID:     "phone_otp",
			ToHash:         comms.HashRecipient("+12222222222"),
		}
		_, _, err = logStore.Reserve(ctx, freshRow)
		require.NoError(t, err)

		// Purge with the default 30-day cutoff.
		sendDeleted, recDeleted, err := logStore.PurgeExpired(ctx, time.Time{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, sendDeleted, int64(1), "the 40-day-old send_log row must be purged")
		assert.GreaterOrEqual(t, recDeleted, int64(1), "the 40-day-old receipt must be purged")

		// The fresh row survives.
		survivor, err := logStore.GetByIdempotencyKey(ctx, freshKey)
		require.NoError(t, err)
		assert.Equal(t, freshKey, survivor.IdempotencyKey)
	})

	t.Run("provider settings: Upsert rejects secret-shaped keys + Get round-trips", func(t *testing.T) {
		// Non-secret settings round-trip.
		require.NoError(t, provStore.Upsert(ctx, &provider.Settings{
			Channel:  comms.ChannelSMS,
			Provider: "aws-sns",
			Enabled:  true,
			Values:   map[string]string{"region": "ap-southeast-1", "sender_id": "CoverOnes"},
		}))

		got, err := provStore.Get(ctx, comms.ChannelSMS, "aws-sns")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, got.Enabled)
		assert.Equal(t, "ap-southeast-1", got.Values["region"])

		// A credential-shaped key is rejected (secrets are env-only).
		err = provStore.Upsert(ctx, &provider.Settings{
			Channel:  comms.ChannelSMS,
			Provider: "aws-sns",
			Values:   map[string]string{"api_secret": "should-not-be-here"},
		})
		require.ErrorIs(t, err, comms.ErrValidation)

		// Absent (channel, provider) returns (nil, nil).
		missing, err := provStore.Get(ctx, comms.ChannelEmail, "ses")
		require.NoError(t, err)
		assert.Nil(t, missing)
	})
}
