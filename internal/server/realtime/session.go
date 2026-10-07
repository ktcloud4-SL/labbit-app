package realtime

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type sessionState int

const (
	// stateOpening은 Control이 TerminalSession을 만드는 중이다. data channel은 bind될 수 있지만 Browser attach는 받지 않는다.
	stateOpening sessionState = iota
	// stateLive는 PTY가 준비되어 Browser attach를 받을 수 있다. Browser가 attach하지 않은 동안에는 grace timer가 돈다.
	stateLive
	// stateEnded는 종료되어 Relay에서 제거된 TerminalSession이다.
	stateEnded
)

// session은 TerminalSession 하나의 ephemeral 상태다. DB 상태의 원본이 아니라 연결 상태다.
//
// 잠금은 두 개다.
//   - tmu는 전이(attach, detach, data bind/loss, grace 만료, 종료)를 직렬화한다. 전이 중에 Control(저장소) I/O를 하므로
//     오래 잡을 수 있지만 다른 TerminalSession과 Binary relay 경로를 막지 않는다. tmu를 먼저, mu를 나중에 잡는다.
//   - mu는 아래 field를 보호하며 짧게만 잡는다. Binary relay 경로는 mu만 잡는다.
type session struct {
	corr        correlation
	connectorID string
	log         *slog.Logger

	tmu sync.Mutex

	mu              sync.Mutex
	state           sessionState
	data            *dataConn
	dataBoundBefore bool
	browser         *browserConn
	liveSession     *liveSession
	attachedBefore  bool
	grace           Timer
	// graceEpoch는 timer를 새로 걸거나 취소할 때마다 증가한다. 이미 만료되어 실행 대기 중인 callback이 그 사이에
	// 재attach된 TerminalSession을 종료하지 않도록 callback은 자신이 걸렸을 때의 epoch와 비교한다.
	graceEpoch uint64
}

func (s *session) ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == stateEnded
}

// stopGraceLocked는 grace timer를 취소한다. s.mu를 잡고 호출한다.
func (s *session) stopGraceLocked() {
	s.graceEpoch++
	if s.grace != nil {
		s.grace.Stop()
		s.grace = nil
	}
}

// armGraceLocked는 grace timer를 at까지로 새로 건다. s.mu를 잡고 호출한다.
func (r *Relay) armGraceLocked(s *session, at time.Time) {
	s.stopGraceLocked()
	epoch := s.graceEpoch
	delay := max(at.Sub(r.clock.Now()), 0)
	s.grace = r.clock.AfterFunc(delay, func() { r.graceExpired(s, epoch) })
}

// browserConn은 TerminalSession에 attach한(하려는) Browser WSS connection 하나다.
type browserConn struct {
	p   *peer
	log *slog.Logger
	// ctx는 이 connection의 read loop가 data channel queue에서 기다릴 때 쓴다. connection이 끝나면 취소한다.
	ctx    context.Context
	cancel context.CancelFunc
	// unavailableSent는 data channel이 없어 INPUT을 버릴 때 ERROR를 한 번만 보내기 위한 표시다.
	unavailableSent atomic.Bool
}

func newBrowserConn(p *peer, log *slog.Logger) *browserConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &browserConn{p: p, log: log, ctx: ctx, cancel: cancel}
}

// close는 close frame을 보내고 이 connection의 기다림을 끝낸다.
func (b *browserConn) close(code int, reason string, flush bool) {
	b.p.close(code, reason, flush)
	b.cancel()
}

func (b *browserConn) closeWithFinalMessage(kind int, data []byte, code int, reason string) {
	b.p.closeWithFinalMessage(kind, data, code, reason)
	b.cancel()
}

// dataConn은 Connector의 Terminal Data WSS connection 하나다.
//
// Upgrade 직후(attach 전)에 만들어 dataTrust에 등록한다. 그래서 TERMINAL_DATA_ATTACH를 기다리는 connection도 revoke 대상이다.
// runtimeID는 attach 뒤 bind되기 전에만 쓰며 s.data로 공개되기 전에 정해진다.
type dataConn struct {
	p         *peer
	runtimeID string

	// credentialID와 connectorID는 이 connection을 인증한 trust다. 생성 뒤 바뀌지 않는다.
	credentialID string
	connectorID  string
	// revoked는 이 connection의 trust를 잃었음(Credential/Connector revoke)을 나타낸다. 한 번 true가 되면 되돌아가지 않으며
	// bindData는 revoked인 connection을 TerminalSession의 data channel로 공개하지 않는다.
	revoked atomic.Bool
	// session은 bind를 시도한 TerminalSession이다. revoke가 그 세션의 data channel에서 이 connection을 내릴 때 쓴다.
	session atomic.Pointer[session]
}

// liveSession은 Class 내 active LiveSession 하나의 ephemeral 상태다.
type liveSession struct {
	id                      string
	sourceTerminalSessionID string
	classID                 string
	log                     *slog.Logger

	mu          sync.Mutex
	subscribers map[*liveSubscriber]struct{}
	ended       bool
}

// liveSubscriber는 LiveSession을 구독 중인 학생 Browser WSS connection 하나다.
type liveSubscriber struct {
	p      *peer
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

func newLiveSubscriber(p *peer, log *slog.Logger) *liveSubscriber {
	ctx, cancel := context.WithCancel(context.Background())
	return &liveSubscriber{p: p, log: log, ctx: ctx, cancel: cancel}
}

func (s *liveSubscriber) close(code int, reason string, flush bool) {
	s.p.close(code, reason, flush)
	s.cancel()
}

func (s *liveSubscriber) closeWithFinalMessage(kind int, data []byte, code int, reason string) {
	s.p.closeWithFinalMessage(kind, data, code, reason)
	s.cancel()
}
