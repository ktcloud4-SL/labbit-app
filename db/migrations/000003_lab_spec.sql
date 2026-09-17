BEGIN;

CREATE TABLE lab_specs (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    owner_user_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1),
    internet_outbound boolean NOT NULL,
    startup_script text NULL,
    workspace_role text NOT NULL CHECK (btrim(workspace_role) <> ''),
    workspace_instance_index integer NOT NULL CHECK (workspace_instance_index >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_lab_specs_owner
        FOREIGN KEY (organization_id, owner_user_id)
        REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_lab_specs_organization_id UNIQUE (organization_id, id)
);

CREATE INDEX idx_lab_specs_organization_owner
    ON lab_specs (organization_id, owner_user_id);

CREATE INDEX idx_lab_specs_updated_at
    ON lab_specs (updated_at DESC);

CREATE TABLE lab_spec_vm_roles (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    lab_spec_id uuid NOT NULL,
    role text NOT NULL CHECK (btrim(role) <> ''),
    display_name text NULL,
    image_mapping_id uuid NOT NULL,
    flavor_mapping_id uuid NOT NULL,
    vm_count integer NOT NULL CHECK (vm_count > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_lab_spec_vm_roles_lab_spec
        FOREIGN KEY (organization_id, lab_spec_id)
        REFERENCES lab_specs (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_lab_spec_vm_roles_image_mapping
        FOREIGN KEY (organization_id, image_mapping_id)
        REFERENCES provider_image_mappings (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_lab_spec_vm_roles_flavor_mapping
        FOREIGN KEY (organization_id, flavor_mapping_id)
        REFERENCES provider_flavor_mappings (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_lab_spec_vm_roles_role UNIQUE (lab_spec_id, role)
);

CREATE INDEX idx_lab_spec_vm_roles_lab_spec_id
    ON lab_spec_vm_roles (lab_spec_id);

COMMIT;
