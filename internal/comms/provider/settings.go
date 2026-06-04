// Package provider persists per-provider NON-SECRET settings (the
// comms_provider_settings table). It is the operator-facing knob store: an
// enabled flag plus a small bag of non-secret values (region, sender id,
// endpoint host) per (channel, provider).
//
// SECRETS ARE NEVER STORED HERE. API keys, SMTP passwords and the like come from
// the environment only (CONVENTIONS §4 / backend-security-design §4). To make
// that boundary enforceable rather than aspirational, Upsert REJECTS any setting
// whose key looks credential-shaped (password / secret / api_key / token / ...).
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings is a per-provider non-secret configuration record.
type Settings struct {
	Channel  comms.Channel
	Provider string
	Enabled  bool
	// Values holds non-secret knobs only (region, sender_id, endpoint, ...).
	Values map[string]string
}

// Store is a pgxpool-backed store over comms_provider_settings.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a provider-settings Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// secretKeyFragments are substrings that, if present in a settings key, mark it
// as credential-shaped and therefore forbidden in this NON-secret table.
var secretKeyFragments = []string{"password", "secret", "api_key", "apikey", "token", "credential", "private"}

// looksSecret reports whether a settings key is credential-shaped.
func looksSecret(key string) bool {
	lk := strings.ToLower(key)
	for _, frag := range secretKeyFragments {
		if strings.Contains(lk, frag) {
			return true
		}
	}

	return false
}

// Get returns the settings for (channel, provider), or comms.ErrTemplateNotFound-
// style not found. Returns (nil, nil) when absent so callers can treat "no row"
// as "use defaults" without an error branch.
func (s *Store) Get(ctx context.Context, channel comms.Channel, providerName string) (*Settings, error) {
	const query = `
SELECT channel, provider, enabled, settings
FROM comms_provider_settings
WHERE channel = $1 AND provider = $2
`

	var (
		out     Settings
		chanStr string
		rawJSON []byte
	)

	err := s.pool.QueryRow(ctx, query, string(channel), providerName).Scan(&chanStr, &out.Provider, &out.Enabled, &rawJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // absent settings is a valid "use defaults" signal, not an error
		}

		return nil, fmt.Errorf("get provider settings: %w", err)
	}

	out.Channel = comms.Channel(chanStr)

	if len(rawJSON) > 0 {
		if unmarshalErr := json.Unmarshal(rawJSON, &out.Values); unmarshalErr != nil {
			return nil, fmt.Errorf("decode provider settings json: %w", unmarshalErr)
		}
	}

	return &out, nil
}

// Upsert inserts or updates the non-secret settings for (channel, provider).
// It REJECTS any credential-shaped key so secrets cannot leak into the DB.
func (s *Store) Upsert(ctx context.Context, st *Settings) error {
	if !st.Channel.Valid() {
		return fmt.Errorf("%w: invalid channel %q", comms.ErrValidation, st.Channel)
	}

	if st.Provider == "" {
		return fmt.Errorf("%w: provider is required", comms.ErrValidation)
	}

	for k := range st.Values {
		if looksSecret(k) {
			return fmt.Errorf("%w: settings key %q looks like a secret; secrets are env-only", comms.ErrValidation, k)
		}
	}

	values := st.Values
	if values == nil {
		values = map[string]string{}
	}

	rawJSON, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode provider settings json: %w", err)
	}

	const query = `
INSERT INTO comms_provider_settings (channel, provider, enabled, settings, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (channel, provider)
DO UPDATE SET enabled = EXCLUDED.enabled,
              settings = EXCLUDED.settings,
              updated_at = now()
`

	if _, err := s.pool.Exec(ctx, query, string(st.Channel), st.Provider, st.Enabled, rawJSON); err != nil {
		return fmt.Errorf("upsert provider settings: %w", err)
	}

	return nil
}
