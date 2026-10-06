package preview

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// errTunnelClosed는 PreviewSession의 Workspace tunnel을 더 쓸 수 없음이다(닫혔거나 이미 하나를 썼다). 응답에는 안전한 code만 쓴다.
var errTunnelClosed = errors.New("preview: tunnel이 닫힘")

type sessionState int

const (
	// statePending은 Expect로 등록되었고 Connector의 Data attach를 기다린다.
	statePending sessionState = iota
	// stateAttached는 Connector의 Data WSS가 bind되어 tunnel이 있다. 아직 Activate 전이라 Browser가 쓸 수 없다.
	stateAttached
	// stateActive는 Activate되어 Preview Origin이 요청을 받는다.
	stateActive
	// stateEnded는 끝났다. 보존 기간 동안 tombstone으로 남을 수 있다.
	stateEnded
)

// session은 PreviewSession 하나의 ephemeral 상태다. 모든 가변 field는 mu로 보호한다.
// bootstrap credential과 Preview Cookie token은 원문을 저장하지 않고 SHA-256 digest만 둔다.
type session struct {
	id             string
	ownerID, orgID string
	connectorID    uuid.UUID
	labInstanceID  string
	generation     int64
	vmKey          string
	serverID       string
	port           int
	ttl            time.Duration
	requestID      string
	log            *slog.Logger

	// attached는 tunnel이 준비되면, ended는 PreviewSession이 끝나면 닫힌다.
	attached chan struct{}
	ended    chan struct{}

	mu             sync.Mutex
	state          sessionState
	bound          bool
	controlSession uuid.UUID
	credentialID   uuid.UUID
	data           *dataConn
	tunnel         *tunnel
	dialed         bool
	activated      bool
	expiresAt      time.Time

	bootstrapDigest  [sha256.Size]byte
	bootstrapExpires time.Time
	bootstrapUsed    bool
	cookieDigest     [sha256.Size]byte
	cookieIssued     bool

	expiry    Timer
	transport *http.Transport
	proxy     http.Handler
	end       End
}

func newSession(e Expected, logger *slog.Logger) *session {
	log := logger.With(
		"preview_session_id", e.SessionID,
		"lab_instance_id", e.LabInstanceID,
		"connector_id", e.ConnectorID.String(),
		"generation", e.Generation,
	)
	if e.RequestID != "" {
		log = log.With("request_id", e.RequestID)
	}
	return &session{
		id: e.SessionID, ownerID: e.OwnerID, orgID: e.OrganizationID,
		connectorID: e.ConnectorID, labInstanceID: e.LabInstanceID, generation: e.Generation,
		vmKey: e.TargetVMKey, serverID: e.ProviderServerID, port: e.TargetPort,
		ttl: e.TTL, requestID: e.RequestID, log: log,
		attached: make(chan struct{}), ended: make(chan struct{}),
	}
}

// bind는 PREVIEW_OPEN을 전달한 Control Session을 기록한다.
func (s *session) bind(b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.state == stateEnded:
		return ErrSessionEnded
	case s.state != statePending:
		return ErrAlreadyActive
	case b.ConnectorID != s.connectorID:
		return ErrControlMismatch
	}
	s.bound = true
	s.controlSession = b.ControlSessionID
	s.credentialID = b.CredentialID
	return nil
}

// claimAttach는 대기 중인 PreviewSession을 d에 bind한다. 성공하면 빈 문자열, 아니면 log에 남길 수 있는 거절 사유다.
//
// 인증된 Connector와, PREVIEW_OPEN을 전달한 Control Session을 인증한 Credential이 모두 같을 때만 bind한다.
// Connector ID가 같다는 이유만으로 신뢰하지 않으므로 이전 Credential이나 다른 Credential로 인증한 Data WSS는 이 PreviewSession을 완료시키지 못한다.
func (s *session) claimAttach(d *dataConn, identity ConnectorIdentity) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.state == stateEnded:
		return "session_ended"
	case s.state != statePending:
		return "request_not_waiting"
	case !s.bound:
		return "control_not_bound"
	case identity.ConnectorID != s.connectorID:
		return "wrong_connector"
	case identity.CredentialID != s.credentialID:
		return "wrong_credential"
	}
	s.state = stateAttached
	s.data = d
	return ""
}

// setTunnel은 tunnel을 기록하고 attach를 알린다. 그 사이 PreviewSession이 끝났다면 false다.
func (s *session) setTunnel(t *tunnel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateAttached {
		return false
	}
	s.tunnel = t
	close(s.attached)
	return true
}

// activate는 attach된 PreviewSession을 활성화하고 일회용 bootstrap credential 원문을 돌려준다.
func (s *session) activate(g *Gateway) (credential string, expiresAt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.state == stateEnded:
		return "", time.Time{}, ErrSessionEnded
	case s.state == stateActive:
		return "", time.Time{}, ErrAlreadyActive
	case s.state != stateAttached || s.tunnel == nil:
		return "", time.Time{}, ErrNotAttached
	}

	now := g.clock.Now()
	s.expiresAt = now.Add(s.ttl)
	credential, s.bootstrapDigest = newToken()
	s.bootstrapExpires = now.Add(g.bootstrapTTL)
	if s.bootstrapExpires.After(s.expiresAt) {
		s.bootstrapExpires = s.expiresAt
	}

	// Workspace application으로의 HTTP/1.1 transport다. tunnel이 TCP 연결 하나이므로 한 번만 dial하고 연결을 하나만 쓴다.
	s.transport = g.newTransport(s)
	s.proxy = g.newProxy(s)
	s.state = stateActive
	s.activated = true
	s.expiry = g.clock.AfterFunc(s.ttl, func() {
		g.endSession(s, End{Reason: EndSessionExpired, NotifyConnector: true}, closeNormal, "session expired", false)
	})
	return credential, s.expiresAt, nil
}

// exchange는 Preview Origin에 제시된 bootstrap credential을 검증하고 성공하면 Preview Cookie token 원문을 돌려준다.
// credential은 한 번만 성공하며 만료·종료된 PreviewSession, 알 수 없거나 이미 사용한 credential은 구분하지 않고 같은 실패다.
func (s *session) exchange(now time.Time, presented string) (token string, expiresAt time.Time, ok bool) {
	digest, wellFormed := tokenDigest(presented)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateActive || !now.Before(s.expiresAt) || s.bootstrapUsed || !now.Before(s.bootstrapExpires) {
		return "", time.Time{}, false
	}
	if !wellFormed || subtle.ConstantTimeCompare(digest[:], s.bootstrapDigest[:]) != 1 {
		return "", time.Time{}, false
	}
	s.bootstrapUsed = true
	token, s.cookieDigest = newToken()
	s.cookieIssued = true
	return token, s.expiresAt, true
}

// authenticate는 요청이 제시한 Preview Cookie token을 검증한다. 활성이고 만료 전이며 digest가 같을 때만 proxy를 돌려준다.
// 만료와 종료는 timer가 상태를 바꾸기 전에도 시각으로 먼저 판정한다.
func (s *session) authenticate(now time.Time, presented string) (http.Handler, bool) {
	digest, wellFormed := tokenDigest(presented)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateActive || !now.Before(s.expiresAt) || !s.cookieIssued || !wellFormed {
		return nil, false
	}
	if subtle.ConstantTimeCompare(digest[:], s.cookieDigest[:]) != 1 {
		return nil, false
	}
	return s.proxy, true
}

// active는 PreviewSession이 활성이고 만료 전인지다(bootstrap 페이지 제공 판단).
func (s *session) active(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == stateActive && now.Before(s.expiresAt)
}

// dial은 Transport의 DialContext다. 활성 PreviewSession의 tunnel을 한 번만 내준다. tunnel이 TCP 연결 하나이므로 닫힌 뒤에는 새 연결이 없다.
func (s *session) dial(context.Context, string, string) (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateActive || s.tunnel == nil || s.dialed {
		return nil, errTunnelClosed
	}
	s.dialed = true
	return s.tunnel.local, nil
}

func (s *session) info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Info{
		SessionID: s.id, OwnerID: s.ownerID, OrganizationID: s.orgID,
		ConnectorID: s.connectorID, LabInstanceID: s.labInstanceID, Generation: s.generation, TargetPort: s.port,
		Ended: s.state == stateEnded, EndReason: s.end.Reason, ExpiresAt: s.expiresAt, ControlSessionID: s.controlSession,
	}
}

// endSession은 PreviewSession을 끝낸다. 처음 호출만 적용하며 적용했으면 true다. 모든 종료 경로(만료, 명시적 종료, revoke, 교체, tunnel 종료, 종료)가
// 이 함수 하나를 지나므로 credential, tunnel, transport가 한꺼번에 무효가 된다.
//
// silent이면 Lifecycle에 알리지 않고 tombstone도 남기지 않는다(생성 중 실패 정리). 활성화되지 않은 PreviewSession도 같다.
// 그 밖에는 Lifecycle에 알리고, 종료 뒤 같은 ID로 오는 요청(DELETE의 멱등 응답, Preview Origin의 일관된 401)을 위해 TTL만큼 tombstone을 남긴다.
func (g *Gateway) endSession(s *session, end End, code int, text string, silent bool) bool {
	s.mu.Lock()
	if s.state == stateEnded {
		s.mu.Unlock()
		return false
	}
	activated := s.activated
	s.state = stateEnded
	s.end = end
	close(s.ended)
	tun, data, tr := s.tunnel, s.data, s.transport
	if s.expiry != nil {
		s.expiry.Stop()
		s.expiry = nil
	}
	// credential을 즉시 무효로 하고 큰 객체를 놓는다. tombstone에는 식별 정보만 남는다.
	s.bootstrapUsed = true
	s.cookieIssued = false
	s.tunnel, s.data, s.transport, s.proxy = nil, nil, nil, nil
	notice := Ended{
		SessionID: s.id, ConnectorID: s.connectorID, LabInstanceID: s.labInstanceID, Generation: s.generation,
		RequestID: s.requestID, Reason: end.Reason, NotifyConnector: end.NotifyConnector, ControlSessionID: s.controlSession,
	}
	s.mu.Unlock()

	if tun != nil {
		tun.close(code, text)
	} else if data != nil {
		data.closeNow(code, text)
	}
	if tr != nil {
		tr.CloseIdleConnections()
	}

	if activated && !silent {
		g.clock.AfterFunc(s.ttl, func() { g.removeSession(s) })
		s.log.Info("PreviewSession 종료", "reason", end.Reason)
		g.notifyEnded(notice)
	} else {
		g.removeSession(s)
		s.log.Debug("PreviewSession 정리", "reason", end.Reason)
	}
	return true
}

func (g *Gateway) removeSession(s *session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessions[s.id] == s {
		delete(g.sessions, s.id)
	}
}

func (g *Gateway) notifyEnded(e Ended) {
	g.mu.Lock()
	l := g.lifecycle
	g.mu.Unlock()
	if l != nil {
		l.SessionEnded(e)
	}
}

// boundCredential은 PREVIEW_OPEN을 전달한 Control Session을 인증한 Credential이다. 아직 묶이지 않았다면 zero다.
func (s *session) boundCredential() uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bound {
		return uuid.Nil
	}
	return s.credentialID
}

// boundControlSession은 PREVIEW_OPEN을 전달한 Control Session의 ID다. 아직 묶이지 않았다면 zero다.
func (s *session) boundControlSession() uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bound {
		return uuid.Nil
	}
	return s.controlSession
}
