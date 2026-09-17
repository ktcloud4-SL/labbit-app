BEGIN;

CREATE TABLE terminal_sessions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    lab_instance_id uuid NOT NULL,
    user_id uuid NOT NULL,
    provider_resource_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation >= 1),
    status text NOT NULL CHECK (status IN ('OPENING', 'ACTIVE', 'DETACHED', 'ENDED')),
    attach_token_hash bytea NOT NULL UNIQUE CHECK (octet_length(attach_token_hash) > 0),
    token_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    attached_at timestamptz NULL,
    detached_at timestamptz NULL,
    grace_expires_at timestamptz NULL,
    ended_at timestamptz NULL,
    end_reason text NULL,
    CONSTRAINT fk_terminal_sessions_lab_instance
        FOREIGN KEY (organization_id, lab_instance_id)
        REFERENCES lab_instances (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_terminal_sessions_user
        FOREIGN KEY (organization_id, user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_terminal_sessions_provider_resource
        FOREIGN KEY (organization_id, provider_resource_id, lab_instance_id, generation)
        REFERENCES provider_resources (organization_id, id, lab_instance_id, generation)
        ON DELETE RESTRICT,
    CONSTRAINT uq_terminal_sessions_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_terminal_sessions_token_expiry
        CHECK (token_expires_at > created_at),
    CONSTRAINT ck_terminal_sessions_ended_state
        CHECK ((status = 'ENDED' AND ended_at IS NOT NULL) OR (status <> 'ENDED' AND ended_at IS NULL))
);

CREATE INDEX idx_terminal_sessions_instance_status
    ON terminal_sessions (lab_instance_id, status);

CREATE INDEX idx_terminal_sessions_user_status
    ON terminal_sessions (user_id, status);

CREATE INDEX idx_terminal_sessions_grace_expiry
    ON terminal_sessions (grace_expires_at)
    WHERE status = 'DETACHED' AND grace_expires_at IS NOT NULL;

CREATE TABLE live_sessions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    class_id uuid NOT NULL,
    source_terminal_session_id uuid NOT NULL,
    instructor_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz NULL,
    end_reason text NULL,
    CONSTRAINT fk_live_sessions_class
        FOREIGN KEY (organization_id, class_id)
        REFERENCES classes (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_live_sessions_source_terminal
        FOREIGN KEY (organization_id, source_terminal_session_id)
        REFERENCES terminal_sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_live_sessions_instructor
        FOREIGN KEY (organization_id, instructor_user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT ck_live_sessions_ended_at
        CHECK (ended_at IS NULL OR ended_at >= created_at)
);

CREATE UNIQUE INDEX uq_live_sessions_active_per_class
    ON live_sessions (class_id)
    WHERE ended_at IS NULL;

CREATE INDEX idx_live_sessions_source_terminal
    ON live_sessions (source_terminal_session_id);

COMMIT;
