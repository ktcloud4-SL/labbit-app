BEGIN;
-- No FK: an init INTENT must exist durably before its state row is created.
CREATE TABLE worker_control_restrictions (
    command_id uuid PRIMARY KEY,
    control_id varchar(128) NOT NULL,
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    approval_id uuid NOT NULL UNIQUE,
    request_payload bytea NOT NULL,
    proof_payload bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('INTENT','APPLIED')),
    created_at timestamptz NOT NULL,
    applied_at timestamptz,
    CHECK ((status='INTENT' AND applied_at IS NULL) OR (status='APPLIED' AND applied_at IS NOT NULL AND applied_at >= created_at))
);
CREATE UNIQUE INDEX worker_control_one_pending_restriction ON worker_control_restrictions(control_id) WHERE status='INTENT';
-- No TTL, automatic retry, cleanup, or implicit resolution of UNKNOWN.
COMMIT;
