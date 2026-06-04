-- Comms module — provider delivery receipts (Phase 0).
--
-- OBSERVABILITY TABLE — retention policy: 30 days.
-- Rows older than 30 days are pruned by the `task db:gc` Taskfile target,
-- which runs as a scheduled job (cron / Kubernetes CronJob) in production.
-- (CONVENTIONS §15 / backend-security-design §1.3 — TTL implemented in THIS PR.)
--
-- PRIVACY: `raw` holds the provider's callback payload AFTER a credential
-- redaction scrub + control-char/length sanitisation (backend-security-design
-- §3.1 / §5.4). status is normalised to a small closed set.
--
-- NO FOREIGN KEY constraints platform-wide (CONVENTIONS §11 / CLAUDE.md #9).
-- provider_msg_id is a soft join key to comms_send_log.provider_msg_id (no FK).

CREATE TABLE comms_delivery_receipts (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        text        NOT NULL,
    provider_msg_id text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('DELIVERED', 'BOUNCED', 'FAILED', 'UNKNOWN')),
    raw             jsonb,                         -- REDACTED + sanitised before insert
    received_at     timestamptz NOT NULL DEFAULT now()
);

-- Receipt correlation: join receipts back to a send row by provider_msg_id.
CREATE INDEX comms_delivery_receipts_provider_msg_id_idx
    ON comms_delivery_receipts (provider_msg_id);
