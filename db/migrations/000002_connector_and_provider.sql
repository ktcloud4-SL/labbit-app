BEGIN;

CREATE TABLE connectors (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    last_runtime_id text NULL,
    last_connector_version text NULL,
    last_seen_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    CONSTRAINT fk_connectors_organization
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    CONSTRAINT uq_connectors_organization_id UNIQUE (organization_id, id)
);

CREATE INDEX idx_connectors_organization_id
    ON connectors (organization_id);

CREATE INDEX idx_connectors_last_seen_at
    ON connectors (last_seen_at);

CREATE TABLE connector_credentials (
    id uuid PRIMARY KEY,
    connector_id uuid NOT NULL,
    credential_hash bytea NOT NULL UNIQUE CHECK (octet_length(credential_hash) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NULL,
    revoked_at timestamptz NULL,
    CONSTRAINT fk_connector_credentials_connector
        FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE RESTRICT
);

CREATE INDEX idx_connector_credentials_active_connector
    ON connector_credentials (connector_id, created_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE provider_connections (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    status text NOT NULL DEFAULT 'UNVERIFIED',
    last_validated_at timestamptz NULL,
    last_validation_error_code text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_provider_connections_connector
        FOREIGN KEY (organization_id, connector_id)
        REFERENCES connectors (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_provider_connections_organization_id UNIQUE (organization_id, id)
);

CREATE INDEX idx_provider_connections_organization_id
    ON provider_connections (organization_id);

CREATE INDEX idx_provider_connections_connector_id
    ON provider_connections (connector_id);

CREATE TABLE provider_image_mappings (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    provider_connection_id uuid NOT NULL,
    provider_image_id text NOT NULL CHECK (btrim(provider_image_id) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    checksum text NULL,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_provider_image_mappings_connection
        FOREIGN KEY (organization_id, provider_connection_id)
        REFERENCES provider_connections (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_provider_image_mappings_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_provider_image_mappings_provider_image
        UNIQUE (provider_connection_id, provider_image_id)
);

CREATE INDEX idx_provider_image_mappings_active
    ON provider_image_mappings (provider_connection_id, is_active);

CREATE TABLE provider_flavor_mappings (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    provider_connection_id uuid NOT NULL,
    provider_flavor_id text NOT NULL CHECK (btrim(provider_flavor_id) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    vcpus integer NOT NULL CHECK (vcpus > 0),
    ram_mib integer NOT NULL CHECK (ram_mib > 0),
    disk_gib integer NOT NULL CHECK (disk_gib >= 0),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_provider_flavor_mappings_connection
        FOREIGN KEY (organization_id, provider_connection_id)
        REFERENCES provider_connections (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_provider_flavor_mappings_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_provider_flavor_mappings_provider_flavor
        UNIQUE (provider_connection_id, provider_flavor_id)
);

CREATE INDEX idx_provider_flavor_mappings_active
    ON provider_flavor_mappings (provider_connection_id, is_active);

COMMIT;
