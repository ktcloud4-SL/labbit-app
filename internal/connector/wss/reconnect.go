package wss

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/heartbeat"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// BackoffPolicy 는 WSS 재접속 대기 시간(지수 백오프 + Jitter)을 관리합니다.
type BackoffPolicy struct {
	InitialInterval     time.Duration // 초기 대기 시간 (기본: 1s)
	MaxInterval         time.Duration // 최대 대기 시간 (기본: 30s)
	Multiplier          float64       // 지수 배수 (기본: 2.0)
	RandomizationFactor float64       // Jitter 범위 비율 (기본: 0.2, 즉 ±20%)
}

// NewDefaultBackoffPolicy 는 기본 지수 백오프 정책을 생성합니다.
func NewDefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicy{
		InitialInterval:     1 * time.Second,
		MaxInterval:         30 * time.Second,
		Multiplier:          2.0,
		RandomizationFactor: 0.2,
	}
}

// NextBackoff 는 시도 횟수(attempt >= 0)에 따른 다음 대기 시간을 계산합니다.
func (b *BackoffPolicy) NextBackoff(attempt int) time.Duration {
	initial := b.InitialInterval
	if initial <= 0 {
		initial = 1 * time.Second
	}
	maxInt := b.MaxInterval
	if maxInt <= 0 {
		maxInt = 30 * time.Second
	}
	mult := b.Multiplier
	if mult <= 1.0 {
		mult = 2.0
	}
	factor := b.RandomizationFactor
	if factor < 0 || factor >= 1.0 {
		factor = 0.2
	}

	// base = initial * (multiplier ^ attempt)
	base := float64(initial) * math.Pow(mult, float64(attempt))
	if base > float64(maxInt) {
		base = float64(maxInt)
	}

	// jitter: [base * (1 - factor), base * (1 + factor)]
	delta := base * factor
	minVal := base - delta
	maxVal := base + delta

	r := secureRandomFloat()
	val := minVal + r*(maxVal-minVal)

	d := time.Duration(val)
	if d > maxInt {
		d = maxInt
	}
	if d < 0 {
		d = initial
	}
	return d
}

func secureRandomFloat() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := binary.BigEndian.Uint64(b[:])
	return float64(u) / float64(math.MaxUint64)
}

// Supervisor 는 SaaS 로의 WSS 제어 채널 수명(연결, 핸드셰이크, 하트비트, 메시지 수신, 자동 재접속)을 총괄 관리합니다.
type Supervisor struct {
	cfg            Config
	handler        *Handler
	backoff        BackoffPolicy
	mu             sync.RWMutex
	currentClient  *Client
	onConnected    func(ack *protocol.HelloAckPayload)
	onDisconnected func(err error)
}

// NewSupervisor 는 새 WSS Supervisor 인스턴스를 생성합니다.
func NewSupervisor(cfg Config, handler *Handler, backoff BackoffPolicy) *Supervisor {
	return &Supervisor{
		cfg:     cfg,
		handler: handler,
		backoff: backoff,
	}
}

// SetOnConnected 는 연결 및 HELLO_ACK 수신 성공 시 호출할 콜백을 설정합니다.
func (s *Supervisor) SetOnConnected(cb func(ack *protocol.HelloAckPayload)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnected = cb
}

// SetOnDisconnected 는 세션 단절 시 호출할 콜백을 설정합니다.
func (s *Supervisor) SetOnDisconnected(cb func(err error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDisconnected = cb
}

// CurrentClient 는 현재 활성화된 WSS Client 인스턴스를 반환합니다.
func (s *Supervisor) CurrentClient() *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentClient
}

// Run 은 context 가 취소될 때까지 지속적으로 WSS 연결을 유지하고 단절 시 자동 재접속을 수행합니다.
// 재접속 시 "재접속은 Operation Retry가 아니다" 원칙에 따라 기존 상태를 재시도하지 않고 순수 연결 복구만 수행합니다.
func (s *Supervisor) Run(ctx context.Context) error {
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		client := NewClient(s.cfg)
		s.mu.Lock()
		s.currentClient = client
		s.mu.Unlock()

		if s.handler != nil {
			s.handler.SetSender(client)
		}

		// 단일 세션 실행 (Dial -> Hello -> Heartbeat & Listen)
		err := s.runSession(ctx, client)
		_ = client.Close()

		s.mu.Lock()
		s.currentClient = nil
		s.mu.Unlock()

		if s.handler != nil {
			s.handler.SetSender(nil)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		s.mu.RLock()
		onDisc := s.onDisconnected
		s.mu.RUnlock()
		if onDisc != nil {
			onDisc(err)
		}

		// 지수 백오프 + Jitter 대기
		backoffDur := s.backoff.NextBackoff(attempt)
		attempt++

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoffDur):
		}
	}
}

func (s *Supervisor) runSession(ctx context.Context, client *Client) error {
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	// 1. Dial (WSS 연결 + Bearer 인증 + ReadLimit 설정)
	if err := client.Dial(sessionCtx); err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	// 2. HELLO 핸드셰이크
	helloAck, err := client.SendHello(sessionCtx)
	if err != nil {
		return fmt.Errorf("hello handshake failed: %w", err)
	}

	s.mu.RLock()
	onConn := s.onConnected
	s.mu.RUnlock()
	if onConn != nil {
		onConn(helloAck)
	}

	// 3. Heartbeat 루프 시작 (HELLO_ACK 에서 협상된 주기 사용)
	hbInterval := time.Duration(helloAck.HeartbeatIntervalSeconds) * time.Second
	offlineTimeout := time.Duration(helloAck.OfflineTimeoutSeconds) * time.Second
	if hbInterval <= 0 {
		hbInterval = 15 * time.Second
	}
	if offlineTimeout <= 0 {
		offlineTimeout = 45 * time.Second
	}

	hbRunner := heartbeat.NewRunner(client, hbInterval, offlineTimeout)
	hbErrCh := hbRunner.Start(sessionCtx)

	// 4. 메시지 수신 청취 루프
	readErrCh := make(chan error, 1)
	if s.handler != nil {
		go func() {
			readErrCh <- s.handler.Listen(sessionCtx, client.Conn())
		}()
	}

	// 5. 단절 또는 에러 대기
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-hbErrCh:
		if err != nil {
			return fmt.Errorf("heartbeat runner failed: %w", err)
		}
		return nil
	case err := <-readErrCh:
		if err != nil {
			return fmt.Errorf("read loop failed: %w", err)
		}
		return nil
	}
}
