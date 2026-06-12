-- Waitlist capture table for pre-launch email collection.
-- Stores prospective-user contact info before account creation.
--
-- Design notes:
--   NO FOREIGN KEY constraints (CONVENTIONS §11 / CLAUDE.md #9).
--   Dedup on lower(email) ensures case-insensitive uniqueness without losing
--   the original submission casing.
--
-- Retention: waitlist rows are retained until the launch notify job processes
-- them (deferred task 319f1882). No TTL on this table — rows are operational
-- data, not observability.

CREATE TABLE waitlist (
    id           uuid        NOT NULL DEFAULT gen_random_uuid(),
    email        text        NOT NULL,
    company      text,
    interested_in text,
    source       text,
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT waitlist_pkey PRIMARY KEY (id)
);

-- Case-insensitive unique index prevents duplicate signups regardless of email casing.
CREATE UNIQUE INDEX waitlist_email_lower_idx ON waitlist (lower(email));
