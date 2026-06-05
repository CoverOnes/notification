-- Add KYC_STATUS_CHANGED notification type (trust-C).
-- The notifications.type column uses a text CHECK constraint (not an enum),
-- so we drop the existing constraint and re-add it with the new value.
-- Retention: same 90-day policy as the parent table (migrations/000001).
--
-- NO FOREIGN KEY constraints (CONVENTIONS §11 / CLAUDE.md #9).

ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_type_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_type_check CHECK (type IN (
        'KYC_TIER_CHANGED',
        'BID_RECEIVED',
        'BID_ACCEPTED',
        'MILESTONE_REACHED',
        'CONTRACT_SIGNED',
        'ACCOUNT_SUSPENDED',
        'KYC_STATUS_CHANGED'
    ));
