-- Restore the type CHECK constraint to the pre-trust-C set (removes KYC_STATUS_CHANGED).
-- This will FAIL if any row has type = 'KYC_STATUS_CHANGED' at rollback time.
-- Operator must DELETE those rows before running this down migration.

ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_type_check;

ALTER TABLE notifications
    ADD CONSTRAINT notifications_type_check CHECK (type IN (
        'KYC_TIER_CHANGED',
        'BID_RECEIVED',
        'BID_ACCEPTED',
        'MILESTONE_REACHED',
        'CONTRACT_SIGNED',
        'ACCOUNT_SUSPENDED'
    ));
