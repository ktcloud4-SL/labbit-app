BEGIN;

-- Trusted control-plane journal only. No application startup auto-activation.
-- An INTENT is never expired/deleted automatically, including after a crash.
CREATE TABLE worker_control_resumes (
    command_id uuid PRIMARY KEY,
    control_id varchar(128) NOT NULL REFERENCES worker_control_states(control_id),
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    approval_id uuid NOT NULL UNIQUE,
    request_payload bytea NOT NULL,
    proof_payload bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('INTENT','APPLIED')),
    created_at timestamptz NOT NULL,
    applied_at timestamptz,
    CHECK ((status='INTENT' AND applied_at IS NULL) OR
           (status='APPLIED' AND applied_at >= created_at))
);

CREATE UNIQUE INDEX worker_control_one_pending_resume
    ON worker_control_resumes(control_id) WHERE status='INTENT';

COMMIT;
