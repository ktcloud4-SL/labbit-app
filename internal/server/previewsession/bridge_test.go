package previewsession

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
)

type fakeAuth struct {
	principal connector.Principal
	err       error
	seen      connector.Credential
}

func (a *fakeAuth) Authenticate(_ context.Context, credential connector.Credential) (connector.Principal, error) {
	a.seen = credential
	return a.principal, a.err
}

func TestConnectorAuthenticatorMapsTheIdentityAndTheErrors(t *testing.T) {
	principal := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	auth := &fakeAuth{principal: principal}
	got, err := NewConnectorAuthenticator(auth).AuthenticateConnector(context.Background(), "the-credential")
	if err != nil {
		t.Fatalf("AuthenticateConnector() error = %v", err)
	}
	if got != (preview.ConnectorIdentity{ConnectorID: principal.ConnectorID, CredentialID: principal.CredentialID}) || auth.seen != "the-credential" {
		t.Fatalf("identity = %+v, seen = %q", got, auth.seen)
	}

	// revoke되었거나 알 수 없는 Credential은 인증 실패다.
	auth.err = connector.ErrUnauthenticated
	if _, err := NewConnectorAuthenticator(auth).AuthenticateConnector(context.Background(), "x"); !errors.Is(err, preview.ErrUnauthenticated) {
		t.Fatalf("error = %v, want preview.ErrUnauthenticated", err)
	}

	// 저장소 장애는 인증 실패가 아니라 사용 불가다. 오류 원문은 보존하되 인증 실패로 오인되지 않는다.
	auth.err = errors.New("storage unavailable")
	_, err = NewConnectorAuthenticator(auth).AuthenticateConnector(context.Background(), "x")
	if !errors.Is(err, preview.ErrDependencyUnavailable) || errors.Is(err, preview.ErrUnauthenticated) {
		t.Fatalf("error = %v, want preview.ErrDependencyUnavailable", err)
	}
}

type fakeSessionGateway struct {
	ids      []uuid.UUID
	replaced []bool
}

func (g *fakeSessionGateway) ControlSessionEnded(id uuid.UUID, replaced bool) {
	g.ids = append(g.ids, id)
	g.replaced = append(g.replaced, replaced)
}

func TestSessionBridgeTellsTheGatewayWhichControlSessionEndedAndWhy(t *testing.T) {
	gw := &fakeSessionGateway{}
	bridge := NewSessionBridge(gw)
	session := connector.Session{ID: uuid.New(), ConnectorID: uuid.New(), CredentialID: uuid.New()}

	bridge.SessionRetired(session, connector.CloseReplaced)
	bridge.SessionRetired(session, connector.CloseRevoked)
	if len(gw.ids) != 2 || gw.ids[0] != session.ID || gw.ids[1] != session.ID {
		t.Fatalf("ids = %v, want Control Session ID(Connector ID가 아님)", gw.ids)
	}
	if !gw.replaced[0] || gw.replaced[1] {
		t.Fatalf("replaced = %v, want [true false]", gw.replaced)
	}
}

// 실제 Registry의 교체와 revoke가 SessionBridge를 거쳐 Gateway까지 전달된다.
func TestRegistryReplacementAndRevokeReachTheGatewayThroughTheBridge(t *testing.T) {
	registry := connector.NewRegistry()
	gw := &fakeSessionGateway{}
	registry.SetSessionObserver(NewSessionBridge(gw))
	principal := connector.Principal{ConnectorID: uuid.New(), CredentialID: uuid.New()}

	first := registry.Register(principal, nil)
	second := registry.Register(principal, nil)
	if len(gw.ids) != 1 || gw.ids[0] != first.Session().ID || !gw.replaced[0] {
		t.Fatalf("교체 통지 = %v / %v", gw.ids, gw.replaced)
	}
	registry.RevokeCredential(principal.CredentialID)
	if len(gw.ids) != 2 || gw.ids[1] != second.Session().ID || gw.replaced[1] {
		t.Fatalf("revoke 통지 = %v / %v", gw.ids, gw.replaced)
	}
}
