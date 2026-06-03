-- Notification service schema
-- Retention policy: 90 days (CONVENTIONS §15 / backend-security-design §1.3).
-- Rows older than 90 days are pruned by the db:gc Taskfile target (scheduled job).
--
-- NO FOREIGN KEY constraints platform-wide (CONVENTIONS §11 / CLAUDE.md #9).
-- Referential integrity to users.id enforced in service layer (validate-on-write).

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE notifications (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid        NOT NULL,          -- recipient (soft ref, no FK)
    type            text        NOT NULL CHECK (type IN (
                                    'KYC_TIER_CHANGED',
                                    'BID_RECEIVED',
                                    'BID_ACCEPTED',
                                    'MILESTONE_REACHED',
                                    'CONTRACT_SIGNED',
                                    'ACCOUNT_SUSPENDED'
                                )),
    title           text        NOT NULL,
    body            text        NOT NULL,
    data            jsonb,                         -- optional structured payload
    source_event_id uuid,                          -- Redis event eventId for idempotency
    read_at         timestamptz,                   -- NULL = unread
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Fast inbox query: newest-first per user, cursor pagination.
CREATE INDEX notifications_user_id_created_at_idx
    ON notifications (user_id, created_at DESC);

-- Idempotent event -> notification mapping: each source_event_id inserts at most
-- one notification per user. ON CONFLICT DO NOTHING in the consumer uses this.
CREATE UNIQUE INDEX notifications_user_source_event_key
    ON notifications (user_id, source_event_id)
    WHERE source_event_id IS NOT NULL;
