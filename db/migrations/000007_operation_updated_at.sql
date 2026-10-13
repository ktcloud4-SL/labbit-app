BEGIN;

ALTER TABLE operations ADD COLUMN updated_at timestamptz;
-- 기존 row에는 마지막으로 기록된 lifecycle 시각을 사용한다.
UPDATE operations SET updated_at = COALESCE(finished_at, started_at, created_at);
ALTER TABLE operations ALTER COLUMN updated_at SET DEFAULT now();
ALTER TABLE operations ALTER COLUMN updated_at SET NOT NULL;
COMMENT ON COLUMN operations.updated_at IS
    'Initial registration time; every subsequent status/stage/error writer must update this timestamp in the same transaction.';

COMMIT;
