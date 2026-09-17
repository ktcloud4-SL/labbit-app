BEGIN;

CREATE TABLE lab_executions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    class_id uuid NOT NULL,
    lab_spec_id uuid NOT NULL,
    instructor_user_id uuid NOT NULL,
    status text NOT NULL CHECK (btrim(status) <> ''),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_lab_executions_class
        FOREIGN KEY (organization_id, class_id)
        REFERENCES classes (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_lab_executions_lab_spec
        FOREIGN KEY (organization_id, lab_spec_id)
        REFERENCES lab_specs (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_lab_executions_instructor
        FOREIGN KEY (organization_id, instructor_user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_lab_executions_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_lab_executions_finished_at
        CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE UNIQUE INDEX uq_lab_executions_active_per_class
    ON lab_executions (class_id)
    WHERE finished_at IS NULL;

CREATE INDEX idx_lab_executions_class_started_at
    ON lab_executions (class_id, started_at DESC);

CREATE TABLE creation_snapshots (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    lab_execution_id uuid NOT NULL UNIQUE,
    source_lab_spec_id uuid NOT NULL,
    source_lab_spec_revision bigint NOT NULL CHECK (source_lab_spec_revision >= 1),
    provider_connection_id uuid NOT NULL,
    schema_version integer NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_creation_snapshots_execution
        FOREIGN KEY (organization_id, lab_execution_id)
        REFERENCES lab_executions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_creation_snapshots_lab_spec
        FOREIGN KEY (organization_id, source_lab_spec_id)
        REFERENCES lab_specs (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_creation_snapshots_provider_connection
        FOREIGN KEY (organization_id, provider_connection_id)
        REFERENCES provider_connections (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_creation_snapshots_organization_id UNIQUE (organization_id, id)
);

CREATE OR REPLACE FUNCTION labbit_reject_creation_snapshot_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'creation_snapshots are immutable; insert a new LabExecution instead of updating a snapshot';
END;
$$;

CREATE TRIGGER trg_creation_snapshots_immutable
BEFORE UPDATE ON creation_snapshots
FOR EACH ROW
EXECUTE FUNCTION labbit_reject_creation_snapshot_update();

CREATE TABLE lab_instances (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    lab_execution_id uuid NOT NULL,
    user_id uuid NOT NULL,
    participant_role text NOT NULL CHECK (participant_role IN ('INSTRUCTOR', 'STUDENT')),
    status text NOT NULL CHECK (btrim(status) <> ''),
    generation integer NOT NULL DEFAULT 1 CHECK (generation >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_lab_instances_execution
        FOREIGN KEY (organization_id, lab_execution_id)
        REFERENCES lab_executions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_lab_instances_user
        FOREIGN KEY (organization_id, user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_lab_instances_execution_user UNIQUE (lab_execution_id, user_id),
    CONSTRAINT uq_lab_instances_organization_id UNIQUE (organization_id, id)
);

CREATE UNIQUE INDEX uq_lab_instances_execution_instructor
    ON lab_instances (lab_execution_id)
    WHERE participant_role = 'INSTRUCTOR';

CREATE INDEX idx_lab_instances_execution_id
    ON lab_instances (lab_execution_id);

CREATE INDEX idx_lab_instances_user_status
    ON lab_instances (user_id, status);

CREATE TABLE operations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    operation_type text NOT NULL CHECK (operation_type IN ('PROVISION', 'RESET', 'CLEANUP')),
    requested_by_user_id uuid NOT NULL,
    request_id text NULL,
    lab_execution_id uuid NULL,
    lab_instance_id uuid NULL,
    idempotency_key_hash bytea NOT NULL CHECK (octet_length(idempotency_key_hash) > 0),
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) > 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'RECONCILING', 'SUCCEEDED', 'FAILED')),
    stage text NULL,
    error_code text NULL,
    error_message text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz NULL,
    finished_at timestamptz NULL,
    CONSTRAINT fk_operations_requested_by
        FOREIGN KEY (organization_id, requested_by_user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_operations_execution
        FOREIGN KEY (organization_id, lab_execution_id)
        REFERENCES lab_executions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_operations_lab_instance
        FOREIGN KEY (organization_id, lab_instance_id)
        REFERENCES lab_instances (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_operations_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_operations_target
        CHECK (
            (operation_type IN ('PROVISION', 'CLEANUP') AND lab_execution_id IS NOT NULL)
            OR
            (operation_type = 'RESET' AND lab_instance_id IS NOT NULL)
        ),
    CONSTRAINT ck_operations_finished_at
        CHECK (finished_at IS NULL OR finished_at >= created_at)
);

CREATE UNIQUE INDEX uq_operations_idempotency
    ON operations (requested_by_user_id, operation_type, idempotency_key_hash);

CREATE INDEX idx_operations_status_created_at
    ON operations (status, created_at);

CREATE INDEX idx_operations_execution_id
    ON operations (lab_execution_id)
    WHERE lab_execution_id IS NOT NULL;

CREATE INDEX idx_operations_lab_instance_id
    ON operations (lab_instance_id)
    WHERE lab_instance_id IS NOT NULL;

CREATE TABLE operation_items (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    lab_instance_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation >= 1),
    status text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'RECONCILING', 'SUCCEEDED', 'FAILED')),
    stage text NULL,
    lease_owner text NULL,
    lease_expires_at timestamptz NULL,
    error_code text NULL,
    error_message text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz NULL,
    finished_at timestamptz NULL,
    CONSTRAINT fk_operation_items_operation
        FOREIGN KEY (organization_id, operation_id)
        REFERENCES operations (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_operation_items_lab_instance
        FOREIGN KEY (organization_id, lab_instance_id)
        REFERENCES lab_instances (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_operation_items_operation_instance UNIQUE (operation_id, lab_instance_id),
    CONSTRAINT uq_operation_items_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_operation_items_finished_at
        CHECK (finished_at IS NULL OR finished_at >= created_at)
);

CREATE UNIQUE INDEX uq_operation_items_active_mutation_per_instance
    ON operation_items (lab_instance_id)
    WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING');

CREATE INDEX idx_operation_items_pending_claim
    ON operation_items (created_at, id)
    WHERE status = 'PENDING';

CREATE INDEX idx_operation_items_lease_expiry
    ON operation_items (lease_expires_at)
    WHERE status IN ('RUNNING', 'RECONCILING') AND lease_expires_at IS NOT NULL;

CREATE TABLE provider_resources (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    lab_instance_id uuid NOT NULL,
    provider_connection_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation >= 1),
    resource_type text NOT NULL CHECK (btrim(resource_type) <> ''),
    logical_name text NULL,
    provider_id text NOT NULL CHECK (btrim(provider_id) <> ''),
    lifecycle_status text NOT NULL CHECK (lifecycle_status IN ('PRESENT', 'DELETING', 'DELETED', 'MISSING')),
    observed_state text NULL,
    created_by_operation_item_id uuid NULL,
    last_observed_at timestamptz NULL,
    deleted_at timestamptz NULL,
    cleanup_error_code text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_provider_resources_lab_instance
        FOREIGN KEY (organization_id, lab_instance_id)
        REFERENCES lab_instances (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_provider_resources_connection
        FOREIGN KEY (organization_id, provider_connection_id)
        REFERENCES provider_connections (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_provider_resources_operation_item
        FOREIGN KEY (organization_id, created_by_operation_item_id)
        REFERENCES operation_items (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_provider_resources_provider_id
        UNIQUE (provider_connection_id, resource_type, provider_id),
    CONSTRAINT uq_provider_resources_terminal_target
        UNIQUE (organization_id, id, lab_instance_id, generation)
);

CREATE INDEX idx_provider_resources_instance_generation
    ON provider_resources (lab_instance_id, generation);

CREATE INDEX idx_provider_resources_lifecycle_status
    ON provider_resources (lifecycle_status)
    WHERE lifecycle_status <> 'DELETED';

COMMIT;
