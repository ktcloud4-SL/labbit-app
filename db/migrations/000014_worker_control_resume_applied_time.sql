BEGIN;

-- Preserve migration 13 and its checksum. PostgreSQL CHECK accepts NULL,
-- so the existing comparison alone does not reject an APPLIED row without
-- a completion timestamp. Do not invent timestamps for invalid old rows:
-- validation must fail and leave the migration unapplied instead.
ALTER TABLE worker_control_resumes
    ADD CONSTRAINT worker_control_resume_applied_time
    CHECK (status <> 'APPLIED' OR applied_at IS NOT NULL);

COMMIT;
