BEGIN;

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    organization_role text NOT NULL CHECK (organization_role IN ('ADMIN', 'MEMBER')),
    created_at timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz NULL,
    CONSTRAINT fk_users_organization
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    CONSTRAINT uq_users_organization_id UNIQUE (organization_id, id)
);

CREATE INDEX idx_users_organization_id
    ON users (organization_id);

CREATE TABLE local_accounts (
    user_id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE CHECK (btrim(username) <> ''),
    password_hash text NOT NULL CHECK (btrim(password_hash) <> ''),
    password_changed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_local_accounts_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT
);

CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NULL,
    revoked_at timestamptz NULL,
    CONSTRAINT fk_auth_sessions_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_auth_sessions_expiry
        CHECK (expires_at > created_at)
);

CREATE INDEX idx_auth_sessions_active_user
    ON auth_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE classes (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_classes_organization
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    CONSTRAINT uq_classes_organization_id UNIQUE (organization_id, id)
);

CREATE INDEX idx_classes_organization_id
    ON classes (organization_id);

CREATE TABLE class_memberships (
    organization_id uuid NOT NULL,
    class_id uuid NOT NULL,
    user_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('INSTRUCTOR', 'STUDENT')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (class_id, user_id),
    CONSTRAINT fk_class_memberships_class
        FOREIGN KEY (organization_id, class_id)
        REFERENCES classes (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_class_memberships_user
        FOREIGN KEY (organization_id, user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_class_memberships_user_id
    ON class_memberships (user_id);

CREATE INDEX idx_class_memberships_class_role
    ON class_memberships (class_id, role);

COMMIT;
