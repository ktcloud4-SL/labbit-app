package terminal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

type fakeConnectorAuth struct {
	principal connector.Principal
	err       error
}

func (f fakeConnectorAuth) Authenticate(context.Context, connector.Credential) (connector.Principal, error) {
	return f.principal, f.err
}

// 인증된 Connector identity에는 Connector ID와 함께 그 connection을 인증한 Credential ID(원문이 아니다)가 담긴다.
// revoke 통지가 이 ID로 Terminal Data WSS를 찾는다.
func TestConnectorAuthenticatorIdentityCarriesCredentialID(t *testing.T) {
	principal := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	got, err := NewConnectorAuthenticator(fakeConnectorAuth{principal: principal}).AuthenticateConnector(t.Context(), "raw-credential-value")
	if err != nil {
		t.Fatalf("AuthenticateConnector() error = %v", err)
	}
	if got.ConnectorID != principal.ConnectorID.String() || got.CredentialID != principal.CredentialID.String() {
		t.Fatalf("identity = %+v, want connector %v credential %v", got, principal.ConnectorID, principal.CredentialID)
	}
	if got.CredentialID == "raw-credential-value" || got.ConnectorID == "raw-credential-value" {
		t.Fatal("identity에 Credential 원문이 들어 있음")
	}
}

func TestConnectorAuthenticatorMapsErrors(t *testing.T) {
	if _, err := NewConnectorAuthenticator(fakeConnectorAuth{err: connector.ErrUnauthenticated}).AuthenticateConnector(t.Context(), "x"); !errors.Is(err, realtime.ErrUnauthenticated) {
		t.Fatalf("revoke된 Credential error = %v, want realtime.ErrUnauthenticated", err)
	}
	if _, err := NewConnectorAuthenticator(fakeConnectorAuth{err: errors.New("db down")}).AuthenticateConnector(t.Context(), "x"); !errors.Is(err, realtime.ErrDependencyUnavailable) {
		t.Fatalf("저장소 장애 error = %v, want realtime.ErrDependencyUnavailable", err)
	}
}

type fakeTrustTerminator struct {
	credentials, connectors []string
}

func (f *fakeTrustTerminator) RevokeConnectorCredential(id string) int {
	f.credentials = append(f.credentials, id)
	return 1
}

func (f *fakeTrustTerminator) RevokeConnector(id string) int {
	f.connectors = append(f.connectors, id)
	return 1
}

// Registry의 revoke 통지(uuid)는 Relay가 쓰는 canonical ID 문자열로 바뀌어 전달된다. realtime은 connector package를 모른다.
func TestRevokeBridgeForwardsCanonicalIDs(t *testing.T) {
	relay := &fakeTrustTerminator{}
	var observer connector.RevokeObserver = NewRevokeBridge(relay)

	credentialID, connectorID := uuid.New(), uuid.New()
	observer.CredentialRevoked(credentialID)
	observer.ConnectorRevoked(connectorID)

	if len(relay.credentials) != 1 || relay.credentials[0] != credentialID.String() {
		t.Fatalf("Credential 통지 = %v, want [%s]", relay.credentials, credentialID)
	}
	if len(relay.connectors) != 1 || relay.connectors[0] != connectorID.String() {
		t.Fatalf("Connector 통지 = %v, want [%s]", relay.connectors, connectorID)
	}
}

// 인증 adapter가 만든 CredentialID와 bridge가 전달하는 ID는 같은 canonical 표기여야 Relay가 connection을 찾는다.
func TestCredentialIDFromAuthenticationMatchesBridgeID(t *testing.T) {
	credentialID := uuid.New()
	identity, err := NewConnectorAuthenticator(fakeConnectorAuth{principal: connector.Principal{CredentialID: credentialID}}).AuthenticateConnector(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}
	relay := &fakeTrustTerminator{}
	NewRevokeBridge(relay).CredentialRevoked(credentialID)
	if len(relay.credentials) != 1 || relay.credentials[0] != identity.CredentialID {
		t.Fatalf("bridge ID = %v, identity.CredentialID = %q, want 같은 값", relay.credentials, identity.CredentialID)
	}
}
