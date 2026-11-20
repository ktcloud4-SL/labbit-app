BEGIN;

-- Candidate Worker control journal. Application startup never applies this file.
-- One authoritative Writer is a prerequisite, not something these tables prove.
CREATE TABLE worker_control_states (
    control_id varchar(128) PRIMARY KEY,
    region varchar(128) NOT NULL,
    cluster varchar(128) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    epoch bigint NOT NULL CHECK (epoch > 0),
    mode text NOT NULL CHECK (mode IN ('ACTIVE', 'PAUSE_NEW', 'RECONCILE_ONLY')),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (control_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    CHECK (region ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    CHECK (cluster ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$')
);

CREATE TABLE worker_control_changes (
    control_id varchar(128) NOT NULL REFERENCES worker_control_states(control_id),
    request_id uuid NOT NULL,
    region varchar(128) NOT NULL,
    cluster varchar(128) NOT NULL,
    from_mode text CHECK (from_mode IN ('ACTIVE','PAUSE_NEW','RECONCILE_ONLY')),
    pod_id varchar(128),
    boot_id uuid,
    expected_revision bigint NOT NULL CHECK (expected_revision >= 0),
    revision bigint NOT NULL CHECK (revision > 0),
    epoch bigint NOT NULL CHECK (epoch > 0),
    mode text NOT NULL CHECK (mode = 'PAUSE_NEW'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (control_id, request_id),
    UNIQUE (control_id, revision),
    CHECK (revision = expected_revision + 1),
    CHECK ((expected_revision = 0 AND from_mode IS NULL AND pod_id IS NULL AND boot_id IS NULL)
        OR (expected_revision > 0 AND from_mode IS NOT NULL AND pod_id IS NOT NULL AND boot_id IS NOT NULL))
);

CREATE TABLE worker_control_boundaries (
    id uuid PRIMARY KEY,
    control_id varchar(128) NOT NULL REFERENCES worker_control_states(control_id),
    region varchar(128) NOT NULL,
    cluster varchar(128) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    epoch bigint NOT NULL CHECK (epoch > 0),
    mode text NOT NULL CHECK (mode IN ('ACTIVE', 'RECONCILE_ONLY')),
    pod_id varchar(128) NOT NULL CHECK (pod_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    boot_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('MUTATION', 'RECONCILIATION')),
    phase text NOT NULL,
    CHECK ((kind = 'MUTATION' AND phase IN ('MUTATION_CLAIM','MUTATION_PREPARE','MUTATION_DISPATCH','MUTATION_REQUEUE')) OR (kind = 'RECONCILIATION' AND phase IN ('RECONCILIATION_CLAIM','RECONCILIATION_PREPARE','RECONCILIATION_DISPATCH'))),
    opened_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    closed_at timestamptz,
    CHECK (mode = 'ACTIVE' OR kind = 'RECONCILIATION'),
    CHECK (closed_at IS NULL OR closed_at >= opened_at)
);

-- No expiry, lease cleanup, cascade deletion, or automatic UNKNOWN resolution.
CREATE INDEX worker_control_open_boundaries
    ON worker_control_boundaries(control_id) WHERE closed_at IS NULL;

COMMIT;
