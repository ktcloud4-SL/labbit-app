package previewsession

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
)

// ConnectorAuthenticator는 Preview Data WSS의 Connector Credential을 기존 Connector 인증 use case로 검증하는 adapter다.
// preview package가 PostgreSQL 기반 concrete type을 알지 않고도 Control WSS와 같은 인증 의미(revoke된 Credential/Connector 거절)를 쓴다.
type ConnectorAuthenticator struct {
	auth interface {
		Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
	}
}

var _ preview.ConnectorAuthenticator = ConnectorAuthenticator{}

// NewConnectorAuthenticator는 *connector.Service 같은 Connector 인증 use case로 adapter를 만든다.
func NewConnectorAuthenticator(auth interface {
	Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
}) ConnectorAuthenticator {
	return ConnectorAuthenticator{auth: auth}
}

// AuthenticateConnector는 preview.ConnectorAuthenticator의 구현이다.
func (a ConnectorAuthenticator) AuthenticateConnector(ctx context.Context, credential string) (preview.ConnectorIdentity, error) {
	principal, err := a.auth.Authenticate(ctx, connector.Credential(credential))
	switch {
	case err == nil:
		// CredentialID는 Credential 원문이 아니라 식별자다. PREVIEW_OPEN을 전달한 Control Session의 Credential과 대조하는 데 쓴다.
		return preview.ConnectorIdentity{ConnectorID: principal.ConnectorID, CredentialID: principal.CredentialID}, nil
	case errors.Is(err, connector.ErrUnauthenticated):
		return preview.ConnectorIdentity{}, preview.ErrUnauthenticated
	default:
		return preview.ConnectorIdentity{}, fmt.Errorf("%w: %w", preview.ErrDependencyUnavailable, err)
	}
}

// SessionBridge는 Registry가 관측한 Control Session의 교체·revoke(connector.SessionObserver)를 Preview Gateway의 PreviewSession 종료로 옮기는
// adapter다. preview package가 connector package를 알지 않고도 Control Session에 묶인 PreviewSession의 trust가 끝난다.
type SessionBridge struct {
	gateway interface {
		ControlSessionEnded(controlSessionID uuid.UUID, replaced bool)
	}
}

var _ connector.SessionObserver = SessionBridge{}

// NewSessionBridge는 *preview.Gateway 같은 PreviewSession 종료 경계로 bridge를 만든다.
func NewSessionBridge(gateway interface {
	ControlSessionEnded(controlSessionID uuid.UUID, replaced bool)
}) SessionBridge {
	return SessionBridge{gateway: gateway}
}

// SessionRetired는 connector.SessionObserver의 구현이다.
func (b SessionBridge) SessionRetired(session connector.Session, reason connector.CloseReason) {
	b.gateway.ControlSessionEnded(session.ID, reason == connector.CloseReplaced)
}
