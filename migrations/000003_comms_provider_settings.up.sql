-- Comms module — per-provider NON-SECRET settings (Phase 0).
--
-- This table holds only operator-tunable, non-secret configuration per
-- (channel, provider): enabled flag + a small jsonb bag of non-secret knobs
-- (e.g. region, sender id, endpoint host). SECRETS (API keys, SMTP passwords)
-- are NEVER stored here — they come from the environment only
-- (CONVENTIONS §4 / backend-security-design §4.2). The service rejects any
-- attempt to read credentials from this jsonb.
--
-- NO FOREIGN KEY constraints platform-wide (CONVENTIONS §11 / CLAUDE.md #9).
-- Configuration data (not observability) so it has NO TTL.

CREATE TABLE comms_provider_settings (
    channel    text        NOT NULL CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'INAPP', 'LINE')),
    provider   text        NOT NULL,
    enabled    boolean     NOT NULL DEFAULT false,
    settings   jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- NON-secret knobs only
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel, provider)
);
