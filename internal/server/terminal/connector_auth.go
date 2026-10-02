package terminal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// ConnectorAuthenticator는 Terminal Data WSS의 Connector credential을 기존 Connector 인증 use case로 검증하는 adapter다.
// realtime package가 PostgreSQL 기반 concrete type을 알지 않고도 Control WSS와 같은 인증 의미(revoke된 Credential/Connector 거절)를 쓴다.
type ConnectorAuthenticator struct {
	auth interface {
		Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
	}
}

var _ realtime.ConnectorAuthenticator = ConnectorAuthenticator{}

// NewConnectorAuthenticator는 *connector.Service 같은 Connector 인증 use case로 adapter를 만든다.
func NewConnectorAuthenticator(auth interface {
	Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
}) ConnectorAuthenticator {
	return ConnectorAuthenticator{auth: auth}
}

// AuthenticateConnector는 realtime.ConnectorAuthenticator의 구현이다.
func (a ConnectorAuthenticator) AuthenticateConnector(ctx context.Context, credential realtime.ConnectorCredential) (realtime.ConnectorIdentity, error) {
	principal, err := a.auth.Authenticate(ctx, connector.Credential(credential))
	switch {
	case err == nil:
		// CredentialID는 Credential 원문이 아니라 식별자다. 이후 revoke 통지로 이 connection을 찾는 데 쓴다.
		return realtime.ConnectorIdentity{
			ConnectorID:  principal.ConnectorID.String(),
			CredentialID: principal.CredentialID.String(),
		}, nil
	case errors.Is(err, connector.ErrUnauthenticated):
		return realtime.ConnectorIdentity{}, realtime.ErrUnauthenticated
	default:
		return realtime.ConnectorIdentity{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
}

// RevokeBridge는 Control이 관측한 Connector revoke(connector.RevokeObserver)를 Terminal Relay의 Data WSS 종료로 옮기는 adapter다.
// realtime package가 connector package를 알지 않고도 Credential revoke가 Control과 Data 연결을 함께 끝내게 한다.
type RevokeBridge struct {
	relay interface {
		RevokeConnectorCredential(credentialID string) int
		RevokeConnector(connectorID string) int
	}
}

var _ connector.RevokeObserver = RevokeBridge{}

// NewRevokeBridge는 *realtime.Relay 같은 Data WSS trust 종료 경계로 bridge를 만든다.
func NewRevokeBridge(relay interface {
	RevokeConnectorCredential(credentialID string) int
	RevokeConnector(connectorID string) int
}) RevokeBridge {
	return RevokeBridge{relay: relay}
}

// CredentialRevoked는 connector.RevokeObserver의 구현이다.
func (b RevokeBridge) CredentialRevoked(credentialID uuid.UUID) {
	b.relay.RevokeConnectorCredential(credentialID.String())
}

// ConnectorRevoked는 connector.RevokeObserver의 구현이다.
func (b RevokeBridge) ConnectorRevoked(connectorID uuid.UUID) {
	b.relay.RevokeConnector(connectorID.String())
}
