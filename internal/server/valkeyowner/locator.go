package valkeyowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/valkey-io/valkey-go"
)

var (
	// ErrNoOwner는 Valkey에 활성 소유자 레코드가 없음을 나타낸다 (fail-closed).
	ErrNoOwner = errors.New("valkeyowner: 활성 소유자 없음")
	// ErrStaleEpoch는 레코드의 epoch가 기대값보다 낮거나 이미 교체되었음을 나타낸다.
	ErrStaleEpoch = errors.New("valkeyowner: 오래된(stale) 소유자 epoch")
	// ErrLocatorUnavailable은 Valkey 장애 또는 통신 실패를 나타낸다 (fail-closed).
	ErrLocatorUnavailable = errors.New("valkeyowner: Valkey locator 접근 불가")
)

// ConnectorOwnerRecord는 Connector의 소유 Pod 식별자 및 lease 메타데이터다.
type ConnectorOwnerRecord struct {
	ConnectorID uuid.UUID `json:"connector_id"`
	PodID       string    `json:"pod_id"`
	PodAddr     string    `json:"pod_addr"` // host:port
	Epoch       int64     `json:"epoch"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// TerminalOwnerRecord는 활성 TerminalSession의 소유 Pod 메타데이터다.
type TerminalOwnerRecord struct {
	TerminalSessionID string    `json:"terminal_session_id"`
	ConnectorID       uuid.UUID `json:"connector_id"`
	PodID             string    `json:"pod_id"`
	PodAddr           string    `json:"pod_addr"`
	Epoch             int64     `json:"epoch"`
	ExpiresAt         time.Time `json:"expires_at"`
}

// Config는 Locator 초기화 설정이다.
type Config struct {
	ValkeyAddr  string
	PodID       string
	PodAddr     string
	PeerSecret  string
	DialTimeout time.Duration
}

// Locator는 Valkey 기반의 ephemeral owner mapping을 관리한다.
type Locator struct {
	client  valkey.Client
	podID   string
	podAddr string

	mu        sync.Mutex
	heartbeat map[uuid.UUID]context.CancelFunc
}

// NewLocator는 Valkey 클라이언트를 생성하고 Locator를 반환한다.
func NewLocator(cfg Config) (*Locator, error) {
	if cfg.ValkeyAddr == "" {
		return nil, errors.New("valkeyowner: ValkeyAddr 필요")
	}
	if cfg.PodID == "" {
		cfg.PodID = "pod-default"
	}
	if cfg.PodAddr == "" {
		cfg.PodAddr = "127.0.0.1:8080"
	}

	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{cfg.ValkeyAddr},
		DisableCache: true, // owner lookup은 항상 fresh 데이터를 읽는다
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocatorUnavailable, err)
	}

	return &Locator{
		client:    client,
		podID:     cfg.PodID,
		podAddr:   cfg.PodAddr,
		heartbeat: make(map[uuid.UUID]context.CancelFunc),
	}, nil
}

// PodID는 현재 인스턴스의 식별자다.
func (l *Locator) PodID() string { return l.podID }

// PodAddr는 현재 인스턴스의 peer 통신 주소(host:port)다.
func (l *Locator) PodAddr() string { return l.podAddr }

func connectorKey(id uuid.UUID) string {
	return "labbit:valkey:connector:owner:" + id.String()
}

func terminalKey(id string) string {
	return "labbit:valkey:terminal:owner:" + id
}

// RegisterConnectorLua는 atomic conditional set을 수행한다.
// 기존 epoch가 전달된 epoch보다 높거나 같으면 STALE_EPOCH를 반환한다.
var registerConnectorLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
if existing then
    local data = cjson.decode(existing)
    if data and data.epoch and tonumber(data.epoch) >= tonumber(ARGV[3]) then
        return redis.error_reply("STALE_EPOCH")
    end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[2]))
return "OK"
`)

// refreshConnectorLua는 소유자와 epoch가 일치할 때만 TTL을 연장한다.
var refreshConnectorLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
if not existing then
    return redis.error_reply("NO_OWNER")
end
local data = cjson.decode(existing)
if not data or data.pod_id ~= ARGV[1] or tonumber(data.epoch) ~= tonumber(ARGV[2]) then
    return redis.error_reply("STALE_EPOCH")
end
redis.call('SET', KEYS[1], ARGV[3], 'EX', tonumber(ARGV[4]))
return "OK"
`)

// releaseConnectorLua는 소유자와 epoch가 일치할 때만 key를 삭제한다.
var releaseConnectorLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
if not existing then
    return "OK"
end
local data = cjson.decode(existing)
if data and data.pod_id == ARGV[1] and tonumber(data.epoch) == tonumber(ARGV[2]) then
    redis.call('DEL', KEYS[1])
end
return "OK"
`)

// RegisterConnector는 Connector의 소유자를 등록하고 백그라운드 heartbeat를 시작한다.
func (l *Locator) RegisterConnector(ctx context.Context, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	record := ConnectorOwnerRecord{
		ConnectorID: connectorID,
		PodID:       l.podID,
		PodAddr:     l.podAddr,
		Epoch:       epoch,
		ExpiresAt:   time.Now().Add(ttl),
	}
	bytes, err := json.Marshal(record)
	if err != nil {
		return err
	}

	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = 1
	}

	res := registerConnectorLua.Exec(ctx, l.client, []string{connectorKey(connectorID)}, []string{
		string(bytes),
		strconv.Itoa(ttlSec),
		strconv.FormatInt(epoch, 10),
	})
	if res.Error() != nil {
		if res.Error().Error() == "STALE_EPOCH" {
			return ErrStaleEpoch
		}
		return fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}

	// 기존 heartbeat가 있다면 취소 후 새로 등록
	l.mu.Lock()
	if cancel, exists := l.heartbeat[connectorID]; exists {
		cancel()
	}
	hbCtx, hbCancel := context.WithCancel(context.Background())
	l.heartbeat[connectorID] = hbCancel
	l.mu.Unlock()

	// 백그라운드 주기적 lease 갱신
	go l.runHeartbeat(hbCtx, connectorID, epoch, ttl)

	return nil
}

func (l *Locator) runHeartbeat(ctx context.Context, connectorID uuid.UUID, epoch int64, ttl time.Duration) {
	interval := ttl / 3
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := l.RefreshConnector(refCtx, connectorID, epoch, ttl)
			cancel()
			if err != nil {
				// 소유권 상실 또는 stale epoch 시 중단
				if errors.Is(err, ErrStaleEpoch) || errors.Is(err, ErrNoOwner) {
					l.mu.Lock()
					delete(l.heartbeat, connectorID)
					l.mu.Unlock()
					return
				}
			}
		}
	}
}

// RefreshConnector는 소유자 lease를 갱신한다.
func (l *Locator) RefreshConnector(ctx context.Context, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	record := ConnectorOwnerRecord{
		ConnectorID: connectorID,
		PodID:       l.podID,
		PodAddr:     l.podAddr,
		Epoch:       epoch,
		ExpiresAt:   time.Now().Add(ttl),
	}
	bytes, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = 1
	}

	res := refreshConnectorLua.Exec(ctx, l.client, []string{connectorKey(connectorID)}, []string{
		l.podID,
		strconv.FormatInt(epoch, 10),
		string(bytes),
		strconv.Itoa(ttlSec),
	})
	if res.Error() != nil {
		if res.Error().Error() == "STALE_EPOCH" {
			return ErrStaleEpoch
		}
		if res.Error().Error() == "NO_OWNER" {
			return ErrNoOwner
		}
		return fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}
	return nil
}

// ReleaseConnector는 소유권 등록을 해제하고 heartbeat를 중지한다.
func (l *Locator) ReleaseConnector(ctx context.Context, connectorID uuid.UUID, epoch int64) error {
	l.mu.Lock()
	if cancel, exists := l.heartbeat[connectorID]; exists {
		cancel()
		delete(l.heartbeat, connectorID)
	}
	l.mu.Unlock()

	res := releaseConnectorLua.Exec(ctx, l.client, []string{connectorKey(connectorID)}, []string{
		l.podID,
		strconv.FormatInt(epoch, 10),
	})
	if res.Error() != nil {
		return fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}
	return nil
}

// LookupConnector는 Connector의 현재 소유 Pod 메타데이터를 조회한다.
func (l *Locator) LookupConnector(ctx context.Context, connectorID uuid.UUID) (*ConnectorOwnerRecord, error) {
	cmd := l.client.B().Get().Key(connectorKey(connectorID)).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		if valkey.IsValkeyNil(res.Error()) {
			return nil, ErrNoOwner
		}
		return nil, fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}

	val, err := res.ToString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocatorUnavailable, err)
	}
	var record ConnectorOwnerRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, fmt.Errorf("%w: corrupt payload", ErrLocatorUnavailable)
	}
	return &record, nil
}

// RegisterTerminal은 TerminalSession 소유자를 등록한다.
func (l *Locator) RegisterTerminal(ctx context.Context, sessionID string, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	record := TerminalOwnerRecord{
		TerminalSessionID: sessionID,
		ConnectorID:       connectorID,
		PodID:             l.podID,
		PodAddr:           l.podAddr,
		Epoch:             epoch,
		ExpiresAt:         time.Now().Add(ttl),
	}
	bytes, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = 1
	}

	cmd := l.client.B().Set().Key(terminalKey(sessionID)).Value(string(bytes)).Ex(time.Duration(ttlSec) * time.Second).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		return fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}
	return nil
}

// LookupTerminal은 TerminalSession의 소유 Pod를 조회한다.
func (l *Locator) LookupTerminal(ctx context.Context, sessionID string) (*TerminalOwnerRecord, error) {
	cmd := l.client.B().Get().Key(terminalKey(sessionID)).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		if valkey.IsValkeyNil(res.Error()) {
			return nil, ErrNoOwner
		}
		return nil, fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}
	val, err := res.ToString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocatorUnavailable, err)
	}
	var record TerminalOwnerRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, fmt.Errorf("%w: corrupt payload", ErrLocatorUnavailable)
	}
	return &record, nil
}

// ReleaseTerminal은 TerminalSession 소유자 키를 삭제한다.
func (l *Locator) ReleaseTerminal(ctx context.Context, sessionID string) error {
	cmd := l.client.B().Del().Key(terminalKey(sessionID)).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		return fmt.Errorf("%w: %v", ErrLocatorUnavailable, res.Error())
	}
	return nil
}

// Close는 활성 heartbeat를 모두 정리하고 클라이언트를 종료한다.
func (l *Locator) Close() {
	l.mu.Lock()
	for _, cancel := range l.heartbeat {
		cancel()
	}
	l.heartbeat = make(map[uuid.UUID]context.CancelFunc)
	l.mu.Unlock()
	l.client.Close()
}
