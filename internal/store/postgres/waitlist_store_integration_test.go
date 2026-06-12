package postgres_test

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/store/postgres"
	migrations "github.com/CoverOnes/notification/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestWaitlistStore_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start real Postgres via testcontainers (never mock DB — backend-security-design §6.5).
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

	// Apply all embedded migrations (including 000007_waitlist).
	var upFiles []string

	walkErr := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}

		if !d.IsDir() && strings.HasSuffix(path, ".up.sql") {
			upFiles = append(upFiles, path)
		}

		return nil
	})
	require.NoError(t, walkErr, "walk embedded migrations FS")
	require.NotEmpty(t, upFiles)

	sort.Strings(upFiles)

	for _, file := range upFiles {
		data, readErr := migrations.FS.ReadFile(file)
		require.NoError(t, readErr, "read migration %s", file)

		_, execErr := pool.Exec(ctx, string(data))
		require.NoError(t, execErr, "apply migration %s", file)
	}

	s := postgres.NewWaitlistStore(pool)

	t.Run("valid insert persists to DB", func(t *testing.T) {
		entry := &domain.Waitlist{
			ID:    uuid.New(),
			Email: "alice@example.com",
		}

		created, err := s.AddToWaitlist(ctx, entry)
		require.NoError(t, err)
		assert.True(t, created, "first insert must report created=true")

		var count int

		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM waitlist WHERE lower(email) = lower($1)`, entry.Email).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "one row must exist after insert")
	})

	t.Run("duplicate email is idempotent — no error, no second row", func(t *testing.T) {
		email := "bob@example.com"

		first := &domain.Waitlist{ID: uuid.New(), Email: email}
		second := &domain.Waitlist{ID: uuid.New(), Email: email}
		thirdCaseVariant := &domain.Waitlist{ID: uuid.New(), Email: "BOB@EXAMPLE.COM"}

		created1, err := s.AddToWaitlist(ctx, first)
		require.NoError(t, err)
		assert.True(t, created1)

		created2, err := s.AddToWaitlist(ctx, second)
		require.NoError(t, err)
		assert.False(t, created2, "duplicate same-casing must report created=false")

		created3, err := s.AddToWaitlist(ctx, thirdCaseVariant)
		require.NoError(t, err)
		assert.False(t, created3, "duplicate different-casing must report created=false (case-insensitive index)")

		// Exactly one row must exist despite three inserts.
		var count int

		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM waitlist WHERE lower(email) = lower($1)`, email).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "exactly one row must exist despite duplicate inserts")
	})

	t.Run("optional fields are persisted correctly", func(t *testing.T) {
		company := "Acme Corp"
		interested := "risk-management"
		src := "web-form"

		entry := &domain.Waitlist{
			ID:           uuid.New(),
			Email:        "carol@example.com",
			Company:      &company,
			InterestedIn: &interested,
			Source:       &src,
		}

		created, err := s.AddToWaitlist(ctx, entry)
		require.NoError(t, err)
		require.True(t, created)

		var gotCompany, gotInterested, gotSource *string

		err = pool.QueryRow(
			ctx,
			`SELECT company, interested_in, source FROM waitlist WHERE id = $1`,
			entry.ID,
		).Scan(&gotCompany, &gotInterested, &gotSource)
		require.NoError(t, err)

		require.NotNil(t, gotCompany)
		assert.Equal(t, company, *gotCompany)
		require.NotNil(t, gotInterested)
		assert.Equal(t, interested, *gotInterested)
		require.NotNil(t, gotSource)
		assert.Equal(t, src, *gotSource)
	})

	t.Run("null optional fields are persisted as NULL", func(t *testing.T) {
		entry := &domain.Waitlist{
			ID:    uuid.New(),
			Email: "dave@example.com",
		}

		created, err := s.AddToWaitlist(ctx, entry)
		require.NoError(t, err)
		require.True(t, created)

		var gotCompany, gotInterested, gotSource *string

		err = pool.QueryRow(
			ctx,
			`SELECT company, interested_in, source FROM waitlist WHERE id = $1`,
			entry.ID,
		).Scan(&gotCompany, &gotInterested, &gotSource)
		require.NoError(t, err)

		assert.Nil(t, gotCompany)
		assert.Nil(t, gotInterested)
		assert.Nil(t, gotSource)
	})
}
