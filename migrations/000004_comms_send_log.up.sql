-- Comms module — outbound send log with idempotency (Phase 0).
--
-- OBSERVABILITY TABLE — retention policy: 30 days.
-- Rows older than 30 days are pruned by the `task db:gc` Taskfile target,
-- which runs as a scheduled job (cron / Kubernetes CronJob) in production.
-- (CONVENTIONS §15 / backend-security-design §1.3 — TTL implemented in THIS PR.)
--
-- PRIVACY: this table NEVER stores plaintext recipients, rendered bodies,
-- template variables, OTPs, or tokens (backend-security-design §3.1).
--   * to_hash    = sha256(recipient) — recipient is NOT recoverable.
--   * last_error = run through a credential-redaction scrub before persist.
-- We keep only routing metadata (channel/provider/template_id) + idempotency_key
-- + provider message id needed to correlate delivery receipts.
--
-- NO FOREIGN KEY constraints platform-wide (CONVENTIONS §11 / CLAUDE.md #9).
-- user_id is a soft reference (nullable, no FK).

CREATE TABLE comms_send_log (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text        NOT NULL,
    channel         text        NOT NULL,
    provider        text        NOT NULL,
    template_id     text        NOT NULL,
    user_id         uuid,                          -- soft ref (no FK), nullable
    to_hash         bytea       NOT NULL,          -- sha256(recipient), NEVER plaintext
    status          text        NOT NULL CHECK (status IN ('PENDING', 'SENT', 'FAILED', 'DEAD')),
    attempts        int         NOT NULL DEFAULT 0,
    provider_msg_id text,                          -- correlates delivery receipts
    last_error      text,                          -- REDACTED before persist
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Idempotency: a given idempotency_key produces at most one send row.
-- The service uses ON CONFLICT (idempotency_key) DO NOTHING to dedup retries.
CREATE UNIQUE INDEX comms_send_log_idempotency_key_idx
    ON comms_send_log (idempotency_key);

-- Hot operational query: find recent rows by status (e.g. retry FAILED, purge old).
CREATE INDEX comms_send_log_status_created_at_idx
    ON comms_send_log (status, created_at);

-- Receipt correlation: lookup by provider_msg_id, only for rows that have one.
CREATE INDEX comms_send_log_provider_msg_id_idx
    ON comms_send_log (provider_msg_id)
    WHERE provider_msg_id IS NOT NULL;
