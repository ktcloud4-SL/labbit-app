package terminal

import (
	"context"
	"errors"
	"fmt"

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
		return realtime.ConnectorIdentity{ConnectorID: principal.ConnectorID.String()}, nil
	case errors.Is(err, connector.ErrUnauthenticated):
		return realtime.ConnectorIdentity{}, realtime.ErrUnauthenticated
	default:
		return realtime.ConnectorIdentity{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
}
