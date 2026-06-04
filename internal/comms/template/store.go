package template

import (
	"context"
	"errors"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultLocale is the fallback locale used when an exact (channel, id, locale)
// match is absent. Matches the comms_templates.locale column default.
const defaultLocale = "en"

// Store is a pgxpool-backed comms.TemplateStore over the comms_templates table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a template Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Ensure Store satisfies the interface.
var _ comms.TemplateStore = (*Store)(nil)

// Get loads the template for (channel, templateID, locale). It first tries the
// exact locale; if absent and locale != defaultLocale it falls back to the
// default locale. Returns comms.ErrTemplateNotFound when neither exists.
func (s *Store) Get(ctx context.Context, channel comms.Channel, templateID, locale string) (*comms.Template, error) {
	if locale == "" {
		locale = defaultLocale
	}

	tpl, err := s.getExact(ctx, channel, templateID, locale)
	if err == nil {
		return tpl, nil
	}

	if !errors.Is(err, comms.ErrTemplateNotFound) {
		return nil, err
	}

	// Fallback to the default locale (unless we already queried it).
	if locale != defaultLocale {
		return s.getExact(ctx, channel, templateID, defaultLocale)
	}

	return nil, comms.ErrTemplateNotFound
}

// getExact loads exactly one (channel, templateID, locale) row.
func (s *Store) getExact(ctx context.Context, channel comms.Channel, templateID, locale string) (*comms.Template, error) {
	const query = `
SELECT channel, template_id, locale, COALESCE(subject, ''), body, updated_at
FROM comms_templates
WHERE channel = $1 AND template_id = $2 AND locale = $3
`

	var (
		tpl     comms.Template
		chanStr string
	)

	err := s.pool.QueryRow(ctx, query, string(channel), templateID, locale).Scan(
		&chanStr, &tpl.TemplateID, &tpl.Locale, &tpl.Subject, &tpl.Body, &tpl.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, comms.ErrTemplateNotFound
		}

		return nil, fmt.Errorf("get template: %w", err)
	}

	tpl.Channel = comms.Channel(chanStr)

	return &tpl, nil
}

// Upsert inserts or updates a template (used by the seeder and admin tooling).
// version is bumped to existing+1 on conflict so callers can observe drift.
func (s *Store) Upsert(ctx context.Context, tpl *comms.Template) error {
	if !tpl.Channel.Valid() {
		return fmt.Errorf("%w: invalid channel %q", comms.ErrValidation, tpl.Channel)
	}

	if tpl.TemplateID == "" || tpl.Body == "" {
		return fmt.Errorf("%w: template_id and body are required", comms.ErrValidation)
	}

	locale := tpl.Locale
	if locale == "" {
		locale = defaultLocale
	}

	var subject *string
	if tpl.Subject != "" {
		subject = &tpl.Subject
	}

	const query = `
INSERT INTO comms_templates (channel, template_id, locale, subject, body, version, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, now())
ON CONFLICT (channel, template_id, locale)
DO UPDATE SET subject = EXCLUDED.subject,
              body = EXCLUDED.body,
              version = comms_templates.version + 1,
              updated_at = now()
`

	if _, err := s.pool.Exec(ctx, query, string(tpl.Channel), tpl.TemplateID, locale, subject, tpl.Body); err != nil {
		return fmt.Errorf("upsert template: %w", err)
	}

	return nil
}
