BEGIN;

ALTER TABLE operation_items ADD COLUMN dispatch_not_before timestamptz NULL;

COMMENT ON COLUMN operation_items.dispatch_not_before IS
    'Earliest claim time after a definitively unsent PROVISION dispatch. NULL for initial work. Never authorizes replay of RUNNING or RECONCILING mutations.';

COMMIT;
