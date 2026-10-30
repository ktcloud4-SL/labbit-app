BEGIN;

-- Operator-only failed result restoration. No credential or raw evidence body.
CREATE TABLE failed_provision_recoveries (
    operation_item_id uuid PRIMARY KEY REFERENCES operation_items(id) ON DELETE RESTRICT,
    organization_id uuid NOT NULL,
    provider_connection_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    plan_sha256 text NOT NULL CHECK (plan_sha256 ~ '^[a-f0-9]{64}$'),
    receipt_sha256 text NOT NULL CHECK (receipt_sha256 ~ '^[a-f0-9]{64}$'),
    ledger_sha256 text NOT NULL CHECK (ledger_sha256 ~ '^[a-f0-9]{64}$'),
    ssh_host_key text NOT NULL,
    operator_label text NOT NULL CHECK (length(operator_label) BETWEEN 1 AND 200),
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 200),
    recovered_at timestamptz NOT NULL,
    FOREIGN KEY (organization_id, operation_item_id) REFERENCES operation_items(organization_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, provider_connection_id) REFERENCES provider_connections(organization_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, connector_id) REFERENCES connectors(organization_id,id) ON DELETE RESTRICT
);

COMMIT;
