//go:build integration

package postgres_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// seedConnector는 Organization에 Connector 하나와 Credential 하나를 저장한다.
// credential_hash는 production 경로와 같은 connector.CredentialDigest로 만든다. credential은 실제 값이 아닌 fixture다.
func seedConnector(t *testing.T, db *testDB, organizationID, connectorID, credentialID uuid.UUID, credential string, connectorRevokedAt, credentialRevokedAt *time.Time) {
	t.Helper()
	digest := connector.CredentialDigest(credential)
	mustExec(t, db,
		`INSERT INTO connectors (id, organization_id, name, revoked_at) VALUES ($1, $2, $3, $4)`,
		connectorID, organizationID, "Test Connector", connectorRevokedAt,
	)
	mustExec(t, db,
		`INSERT INTO connector_credentials (id, connector_id, credential_hash, revoked_at) VALUES ($1, $2, $3, $4)`,
		credentialID, connectorID, digest[:], credentialRevokedAt,
	)
}

func TestConnectorCredentialByHash(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	revokedAt := at(9)

	const (
		activeCredential             = "test-pg-active-credential"
		revokedCredential            = "test-pg-revoked-credential"
		credentialOfRevokedConnector = "test-pg-credential-of-revoked-connector"
	)
	activeConnector, activeCredentialID := uuid.New(), uuid.New()
	revokedCredConnector, revokedCredentialID := uuid.New(), uuid.New()
	revokedConnector, revokedConnectorCredentialID := uuid.New(), uuid.New()
	seedConnector(t, db, org, activeConnector, activeCredentialID, activeCredential, nil, nil)
	seedConnector(t, db, org, revokedCredConnector, revokedCredentialID, revokedCredential, nil, &revokedAt)
	seedConnector(t, db, org, revokedConnector, revokedConnectorCredentialID, credentialOfRevokedConnector, &revokedAt, nil)

	lookup := func(credential string) (repository.ConnectorCredentialWithConnector, error) {
		digest := connector.CredentialDigest(credential)
		return db.store.ConnectorCredentialByHash(t.Context(), digest[:])
	}

	t.Run("active Credential은 소유 Connector를 반환", func(t *testing.T) {
		got, err := lookup(activeCredential)
		if err != nil {
			t.Fatalf("ConnectorCredentialByHash() error = %v", err)
		}
		if got.Credential.ID != activeCredentialID || got.Credential.ConnectorID != activeConnector || got.Credential.RevokedAt != nil {
			t.Fatalf("Credential = %+v", got.Credential)
		}
		if got.Connector.ID != activeConnector || got.Connector.OrganizationID != org || got.Connector.RevokedAt != nil {
			t.Fatalf("Connector = %+v", got.Connector)
		}
	})

	t.Run("revoke된 Credential은 revoked_at과 함께 반환", func(t *testing.T) {
		got, err := lookup(revokedCredential)
		if err != nil {
			t.Fatalf("ConnectorCredentialByHash() error = %v", err)
		}
		if got.Credential.RevokedAt == nil || !got.Credential.RevokedAt.Equal(revokedAt) || got.Connector.RevokedAt != nil {
			t.Fatalf("got = %+v, want credential revoked only", got)
		}
	})

	t.Run("revoke된 Connector는 revoked_at과 함께 반환", func(t *testing.T) {
		got, err := lookup(credentialOfRevokedConnector)
		if err != nil {
			t.Fatalf("ConnectorCredentialByHash() error = %v", err)
		}
		if got.Connector.RevokedAt == nil || !got.Connector.RevokedAt.Equal(revokedAt) || got.Credential.RevokedAt != nil {
			t.Fatalf("got = %+v, want connector revoked only", got)
		}
	})

	t.Run("없는 hash는 ErrNotFound", func(t *testing.T) {
		if _, err := lookup("test-pg-unknown-credential"); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("Credential 원문 자체는 저장되지 않고 원문을 key로 조회되지 않음", func(t *testing.T) {
		if _, err := db.store.ConnectorCredentialByHash(t.Context(), []byte(activeCredential)); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("원문으로 조회한 error = %v, want ErrNotFound", err)
		}
		if n := countRows(t, db, `SELECT count(*) FROM connector_credentials WHERE position($1::bytea in credential_hash) > 0`, []byte(activeCredential)); n != 0 {
			t.Fatalf("credential_hash에 원문이 들어 있는 row = %d", n)
		}
		want := sha256.Sum256([]byte(activeCredential))
		if n := countRows(t, db, `SELECT count(*) FROM connector_credentials WHERE credential_hash = $1`, want[:]); n != 1 {
			t.Fatalf("sha256 digest로 저장된 row = %d, want 1", n)
		}
	})
}

// Application(connector.Service) 판정까지 포함해 실제 PostgreSQL 데이터에서 인증 가능/불가 조건을 확인한다.
func TestConnectorServiceAuthenticatesAgainstPostgreSQL(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	revokedAt := at(9)

	const (
		activeCredential             = "test-svc-active-credential"
		revokedCredential            = "test-svc-revoked-credential"
		credentialOfRevokedConnector = "test-svc-credential-of-revoked-connector"
	)
	activeConnector, activeCredentialID := uuid.New(), uuid.New()
	seedConnector(t, db, org, activeConnector, activeCredentialID, activeCredential, nil, nil)
	seedConnector(t, db, org, uuid.New(), uuid.New(), revokedCredential, nil, &revokedAt)
	seedConnector(t, db, org, uuid.New(), uuid.New(), credentialOfRevokedConnector, &revokedAt, nil)

	service := connector.NewService(db.store)

	got, err := service.Authenticate(t.Context(), connector.Credential(activeCredential))
	if err != nil {
		t.Fatalf("active Credential Authenticate() error = %v", err)
	}
	want := connector.Principal{ConnectorID: activeConnector, OrganizationID: org, CredentialID: activeCredentialID}
	if got != want {
		t.Fatalf("Principal = %+v, want %+v", got, want)
	}

	for name, credential := range map[string]string{
		"revoke된 Credential": revokedCredential,
		"revoke된 Connector":  credentialOfRevokedConnector,
		"없는 Credential":      "test-svc-unknown-credential",
		"빈 Credential":       "",
	} {
		t.Run(name, func(t *testing.T) {
			principal, err := service.Authenticate(t.Context(), connector.Credential(credential))
			if !errors.Is(err, connector.ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v, want ErrUnauthenticated", err)
			}
			if principal != (connector.Principal{}) {
				t.Fatalf("Principal = %+v, want zero value", principal)
			}
		})
	}

	// Credential을 revoke하면 같은 Credential의 이후 인증이 거절된다.
	mustExec(t, db, `UPDATE connector_credentials SET revoked_at = now() WHERE id = $1`, activeCredentialID)
	if _, err := service.Authenticate(t.Context(), connector.Credential(activeCredential)); !errors.Is(err, connector.ErrUnauthenticated) {
		t.Fatalf("revoke 후 Authenticate() error = %v, want ErrUnauthenticated", err)
	}
}
