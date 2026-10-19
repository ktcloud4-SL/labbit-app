BEGIN;

CREATE TABLE connector_enrollment_tokens (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    organization_id uuid NOT NULL,
    connector_name text NOT NULL CHECK (btrim(connector_name) <> '' AND char_length(connector_name) <= 200),
    issued_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at timestamptz NULL,
    revoked_at timestamptz NULL,
    connector_id uuid NULL,
    CONSTRAINT fk_connector_enrollment_tokens_issuer
        FOREIGN KEY (organization_id, issued_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_connector_enrollment_tokens_connector
        FOREIGN KEY (organization_id, connector_id) REFERENCES connectors (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT ck_connector_enrollment_tokens_consumption
        CHECK ((consumed_at IS NULL) = (connector_id IS NULL)),
    CONSTRAINT ck_connector_enrollment_tokens_lifecycle
        CHECK (consumed_at IS NULL OR (revoked_at IS NULL AND consumed_at >= created_at AND consumed_at < expires_at)),
    CONSTRAINT ck_connector_enrollment_tokens_revocation
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX idx_connector_enrollment_tokens_organization ON connector_enrollment_tokens (organization_id);

COMMIT;
