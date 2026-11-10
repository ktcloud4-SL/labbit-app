package valkeyowner

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	ValkeyAddr        string
	ValkeyAddrs       []string
	Username          string
	Password          string
	TLSEnabled        bool
	TLSServerName     string
	TLSConfig         *tls.Config
	ForceSingleClient bool
	PodID             string
	PodAddr           string
	PeerSecret        string
	DialTimeout       time.Duration
}

type connectorLease struct {
	epoch  int64
	cancel context.CancelFunc
	onLost func()
	once   sync.Once
}

func (cl *connectorLease) triggerLost() {
	if cl == nil {
		return
	}
	cl.cancel()
	if cl.onLost != nil {
		cl.once.Do(cl.onLost)
	}
}

type terminalLease struct {
	epoch  int64
	cancel context.CancelFunc
}

// Locator는 Valkey 기반의 ephemeral owner mapping을 관리한다.
type Locator struct {
	client    valkey.Client
	clientOpt valkey.ClientOption
	podID     string
	podAddr   string

	mu                sync.Mutex
	closed            bool
	heartbeat         map[uuid.UUID]*connectorLease
	terminalHeartbeat map[string]*terminalLease
}

// LoadSecretValue는 *_FILE 환경변수를 우선하여 비밀값을 읽는다.
// production 환경에서 평문 환경변수 사용을 차단하며, 오류 메시지에 비밀값 원문을 노출하지 않는다.
func LoadSecretValue(environment, fileEnvKey, rawEnvKey string, requiredInProd bool) (string, error) {
	if path := strings.TrimSpace(os.Getenv(fileEnvKey)); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s 파일을 읽을 수 없습니다", fileEnvKey)
		}
		secret := strings.TrimSpace(string(content))
		if secret == "" {
			return "", fmt.Errorf("%s 파일이 비어 있습니다", fileEnvKey)
		}
		return secret, nil
	}
	raw := strings.TrimSpace(os.Getenv(rawEnvKey))
	if environment == "production" {
		if raw != "" {
			return "", fmt.Errorf("production에서는 %s 대신 %s이 필요합니다", rawEnvKey, fileEnvKey)
		}
		if requiredInProd {
			return "", fmt.Errorf("production에서는 %s이 필요합니다", fileEnvKey)
		}
		return "", nil
	}
	return raw, nil
}

// BuildClientOption은 Config로부터 클러스터/TLS/인증 설정이 반영된 valkey.ClientOption을 조립한다.
// 오류 반환 시 주소나 자격증명 원문을 포함하지 않는다.
func BuildClientOption(cfg Config) (valkey.ClientOption, error) {
	var addrs []string
	for _, a := range cfg.ValkeyAddrs {
		if trimmed := strings.TrimSpace(a); trimmed != "" {
			addrs = append(addrs, trimmed)
		}
	}
	if len(addrs) == 0 && strings.TrimSpace(cfg.ValkeyAddr) != "" {
		for _, part := range strings.Split(cfg.ValkeyAddr, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				addrs = append(addrs, trimmed)
			}
		}
	}
	if len(addrs) == 0 {
		return valkey.ClientOption{}, errors.New("valkeyowner: ValkeyAddr 필요")
	}

	username := cfg.Username
	password := cfg.Password
	tlsEnabled := cfg.TLSEnabled
	normalizedAddrs := make([]string, 0, len(addrs))

	for _, rawAddr := range addrs {
		if strings.Contains(rawAddr, "://") {
			u, err := url.Parse(rawAddr)
			if err != nil || u.Host == "" {
				return valkey.ClientOption{}, errors.New("valkeyowner: Valkey 주소 형식이 올바르지 않습니다")
			}
			switch strings.ToLower(u.Scheme) {
			case "valkeys", "rediss":
				tlsEnabled = true
			case "valkey", "redis":
			default:
				return valkey.ClientOption{}, errors.New("valkeyowner: 지원하지 않는 Valkey URL scheme입니다")
			}
			if u.User != nil {
				if username == "" && u.User.Username() != "" {
					username = u.User.Username()
				}
				if pw, ok := u.User.Password(); ok && password == "" {
					password = pw
				}
			}
			rawAddr = u.Host
		}
		host, port, err := net.SplitHostPort(rawAddr)
		if err != nil || host == "" || port == "" {
			return valkey.ClientOption{}, errors.New("valkeyowner: Valkey 주소는 host:port 형식이어야 합니다")
		}
		normalizedAddrs = append(normalizedAddrs, net.JoinHostPort(host, port))
	}

	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}

	opt := valkey.ClientOption{
		InitAddress:       normalizedAddrs,
		Username:          username,
		Password:          password,
		DisableCache:      true, // owner lookup은 항상 fresh 데이터를 읽는다 (캐시 미사용)
		ShuffleInit:       len(normalizedAddrs) > 1,
		ForceSingleClient: cfg.ForceSingleClient,
		Dialer: net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: time.Second,
		},
	}

	if cfg.TLSConfig != nil {
		cloned := cfg.TLSConfig.Clone()
		if cloned.MinVersion < tls.VersionTLS12 {
			cloned.MinVersion = tls.VersionTLS12
		}
		opt.TLSConfig = cloned
	} else if tlsEnabled {
		serverName := strings.TrimSpace(cfg.TLSServerName)
		if serverName == "" {
			if h, _, err := net.SplitHostPort(normalizedAddrs[0]); err == nil && net.ParseIP(h) == nil {
				serverName = h
			}
		}
		opt.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}

	return opt, nil
}

// NewLocator는 Valkey 클라이언트를 생성하고 Locator를 반환한다.
func NewLocator(cfg Config) (*Locator, error) {
	if cfg.PodID == "" {
		cfg.PodID = "pod-default"
	}
	if cfg.PodAddr == "" {
		cfg.PodAddr = "127.0.0.1:8080"
	}
	if err := ValidateTargetAddr(cfg.PodAddr, ""); err != nil {
		return nil, errors.New("valkeyowner: 유효하지 않은 PodAddr")
	}

	clientOpt, err := BuildClientOption(cfg)
	if err != nil {
		return nil, err
	}

	client, err := valkey.NewClient(clientOpt)
	if err != nil {
		// 자격증명이나 내부 주소를 에러 문자열에 노출하지 않는다.
		return nil, ErrLocatorUnavailable
	}

	return &Locator{
		client:            client,
		clientOpt:         clientOpt,
		podID:             cfg.PodID,
		podAddr:           cfg.PodAddr,
		heartbeat:         make(map[uuid.UUID]*connectorLease),
		terminalHeartbeat: make(map[string]*terminalLease),
	}, nil
}

// PodID는 현재 인스턴스의 식별자다.
func (l *Locator) PodID() string {
	if l == nil {
		return ""
	}
	return l.podID
}

// PodAddr는 현재 인스턴스의 peer 통신 주소(host:port)다.
func (l *Locator) PodAddr() string {
	if l == nil {
		return ""
	}
	return l.podAddr
}

// ConnectorKey는 Connector 소유자 레코드의 Valkey key를 반환한다.
// ElastiCache Serverless / Valkey Cluster 모드에서 multi-key Lua 스크립트가 CROSSSLOT 없이
// 동일한 해시 슬롯에서 원자적으로 실행되도록 `{connectorID}` 해시 태그를 공유한다.
func ConnectorKey(id uuid.UUID) string {
	return "labbit:valkey:connector:{" + id.String() + "}:owner"
}

// ConnectorEpochKey는 Connector 단조 증가 epoch 카운터의 Valkey key를 반환한다.
// ConnectorKey와 동일한 `{connectorID}` 해시 태그를 사용한다.
func ConnectorEpochKey(id uuid.UUID) string {
	return "labbit:valkey:connector:{" + id.String() + "}:epoch"
}

// TerminalKey는 TerminalSession 소유자 레코드의 Valkey key를 반환한다.
func TerminalKey(id string) string {
	return "labbit:valkey:terminal:{" + id + "}:owner"
}

func connectorKey(id uuid.UUID) string {
	return ConnectorKey(id)
}

func connectorEpochKey(id uuid.UUID) string {
	return ConnectorEpochKey(id)
}

func terminalKey(id string) string {
	return TerminalKey(id)
}

// HashSlot은 Valkey Cluster 규격(CRC16-XMODEM mod 16384, `{...}` 해시 태그 지원)에 따라 key의 슬롯을 계산한다.
func HashSlot(key string) uint16 {
	s := strings.IndexByte(key, '{')
	if s >= 0 {
		e := strings.IndexByte(key[s+1:], '}')
		if e > 0 {
			key = key[s+1 : s+1+e]
		}
	}
	var crc uint16
	for i := 0; i < len(key); i++ {
		crc = crc ^ (uint16(key[i]) << 8)
		for j := 0; j < 8; j++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc & 16383
}

// RegisterConnectorLua는 atomic conditional set을 수행한다.
// requested_epoch(ARGV[4])가 0 이하이면 Valkey INCR로 단조 증가 epoch를 원자적으로 생성한다.
// 전달된 epoch가 기존 epoch보다 낮거나 같으면 STALE_EPOCH를 반환한다.
var registerConnectorLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
local current_epoch = 0
if existing then
    local data = cjson.decode(existing)
    if data and data.epoch then
        current_epoch = tonumber(data.epoch)
    end
end

local target_epoch = tonumber(ARGV[4])
if target_epoch <= 0 then
    target_epoch = redis.call('INCR', KEYS[2])
    if target_epoch <= current_epoch then
        target_epoch = current_epoch + 1
        redis.call('SET', KEYS[2], target_epoch)
    end
else
    if target_epoch <= current_epoch then
        return redis.error_reply("STALE_EPOCH")
    end
    local gen = redis.call('GET', KEYS[2])
    if not gen or tonumber(gen) < target_epoch then
        redis.call('SET', KEYS[2], target_epoch)
    end
end

local record = {
    connector_id = ARGV[5],
    pod_id = ARGV[1],
    pod_addr = ARGV[2],
    epoch = target_epoch,
    expires_at = ARGV[6]
}
local encoded = cjson.encode(record)
redis.call('SET', KEYS[1], encoded, 'EX', tonumber(ARGV[3]))
return target_epoch
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

// refreshTerminalLua는 소유자와 epoch가 일치할 때만 TerminalSession lease를 연장한다.
var refreshTerminalLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
if not existing then
    return redis.error_reply("NO_OWNER")
end
local data = cjson.decode(existing)
if not data or data.pod_id ~= ARGV[1] or tonumber(data.epoch) ~= tonumber(ARGV[4]) then
    return redis.error_reply("STALE_EPOCH")
end
redis.call('SET', KEYS[1], ARGV[2], 'EX', tonumber(ARGV[3]))
return "OK"
`)

// releaseTerminalLua는 소유자와 epoch가 일치할 때만 TerminalSession 키를 삭제한다.
var releaseTerminalLua = valkey.NewLuaScript(`
local existing = redis.call('GET', KEYS[1])
if not existing then
    return "OK"
end
local data = cjson.decode(existing)
if data and data.pod_id == ARGV[1] and (tonumber(ARGV[2]) <= 0 or tonumber(data.epoch) == tonumber(ARGV[2])) then
    redis.call('DEL', KEYS[1])
end
return "OK"
`)

// RegisterConnector는 Connector의 소유자를 등록하고 백그라운드 heartbeat를 시작한다.
// epoch가 0 이하이면 Valkey INCR 원자적 연산을 사용하여 분산 단조 증가 generation을 할당한다.
// 반환값은 할당/검증된 atomic epoch와 에러이다.
// 선택적 onLost 콜백이 제공되면 lease 만료, 소유자 교체, 또는 Valkey 장애 시 호출되어 fail-closed를 유도한다.
func (l *Locator) RegisterConnector(ctx context.Context, connectorID uuid.UUID, epoch int64, ttl time.Duration, onLost ...func()) (int64, error) {
	if l == nil || l.client == nil {
		return 0, ErrLocatorUnavailable
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = 1
	}

	res := registerConnectorLua.Exec(ctx, l.client, []string{
		connectorKey(connectorID),
		connectorEpochKey(connectorID),
	}, []string{
		l.podID,
		l.podAddr,
		strconv.Itoa(ttlSec),
		strconv.FormatInt(epoch, 10),
		connectorID.String(),
		time.Now().Add(ttl).Format(time.RFC3339Nano),
	})
	if res.Error() != nil {
		if res.Error().Error() == "STALE_EPOCH" {
			return 0, ErrStaleEpoch
		}
		return 0, ErrLocatorUnavailable
	}

	assignedEpoch, err := res.AsInt64()
	if err != nil {
		return 0, ErrLocatorUnavailable
	}

	var lostCb func()
	if len(onLost) > 0 && onLost[0] != nil {
		lostCb = onLost[0]
	}

	// 기존 heartbeat가 있다면 취소 후 새로 등록
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return 0, ErrLocatorUnavailable
	}
	if prev, exists := l.heartbeat[connectorID]; exists {
		prev.cancel()
	}
	hbCtx, hbCancel := context.WithCancel(context.Background())
	lease := &connectorLease{
		epoch:  assignedEpoch,
		cancel: hbCancel,
		onLost: lostCb,
	}
	l.heartbeat[connectorID] = lease
	l.mu.Unlock()

	// 백그라운드 주기적 lease 갱신
	go l.runHeartbeat(hbCtx, connectorID, lease, ttl)

	return assignedEpoch, nil
}

func (l *Locator) runHeartbeat(ctx context.Context, connectorID uuid.UUID, lease *connectorLease, ttl time.Duration) {
	interval := ttl / 3
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	leaseExpiresAt := time.Now().Add(ttl)

	notifyLost := func() {
		l.mu.Lock()
		if current, ok := l.heartbeat[connectorID]; ok && current == lease {
			delete(l.heartbeat, connectorID)
		}
		l.mu.Unlock()
		lease.triggerLost()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := l.RefreshConnector(refCtx, connectorID, lease.epoch, ttl)
			cancel()
			if err != nil {
				// 소유권 상실 또는 stale epoch 시 즉시 중단 및 onLost 호출
				if errors.Is(err, ErrStaleEpoch) || errors.Is(err, ErrNoOwner) {
					notifyLost()
					return
				}
				// Valkey 장애/지연 시: lease TTL 만료 검증
				if time.Now().After(leaseExpiresAt) {
					notifyLost()
					return
				}
				continue
			}
			leaseExpiresAt = time.Now().Add(ttl)
		}
	}
}

// RefreshConnector는 소유자 lease를 갱신한다.
func (l *Locator) RefreshConnector(ctx context.Context, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	if l == nil || l.client == nil {
		return ErrLocatorUnavailable
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

	res := refreshConnectorLua.Exec(ctx, l.client, []string{connectorKey(connectorID)}, []string{
		l.podID,
		strconv.FormatInt(epoch, 10),
		string(bytes),
		strconv.Itoa(ttlSec),
	})
	if res.Error() != nil {
		if res.Error().Error() == "STALE_EPOCH" {
			l.retireIfEpochMismatch(connectorID, epoch)
			return ErrStaleEpoch
		}
		if res.Error().Error() == "NO_OWNER" {
			l.retireIfEpochMismatch(connectorID, epoch)
			return ErrNoOwner
		}
		return ErrLocatorUnavailable
	}
	return nil
}

func (l *Locator) retireIfEpochMismatch(connectorID uuid.UUID, staleEpoch int64) {
	l.mu.Lock()
	var stale *connectorLease
	if current, exists := l.heartbeat[connectorID]; exists && current.epoch <= staleEpoch {
		stale = current
		delete(l.heartbeat, connectorID)
	}
	l.mu.Unlock()
	if stale != nil {
		stale.triggerLost()
	}
}

// ReleaseConnector는 소유권 등록을 해제하고 heartbeat를 중지한다.
func (l *Locator) ReleaseConnector(ctx context.Context, connectorID uuid.UUID, epoch int64) error {
	if l == nil || l.client == nil {
		return ErrLocatorUnavailable
	}
	l.mu.Lock()
	if current, exists := l.heartbeat[connectorID]; exists && (epoch <= 0 || current.epoch == epoch) {
		current.cancel()
		delete(l.heartbeat, connectorID)
	}
	l.mu.Unlock()

	res := releaseConnectorLua.Exec(ctx, l.client, []string{connectorKey(connectorID)}, []string{
		l.podID,
		strconv.FormatInt(epoch, 10),
	})
	if res.Error() != nil {
		return ErrLocatorUnavailable
	}
	return nil
}

// LookupConnector는 Connector의 현재 소유 Pod 메타데이터를 조회한다.
// 만약 이 Pod에 이전 epoch의 로컬 lease가 남아 있다면, heartbeat 주기를 기다리지 않고 즉시 퇴역(onLost)시킨다.
func (l *Locator) LookupConnector(ctx context.Context, connectorID uuid.UUID) (*ConnectorOwnerRecord, error) {
	if l == nil || l.client == nil {
		return nil, ErrLocatorUnavailable
	}
	cmd := l.client.B().Get().Key(connectorKey(connectorID)).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		if valkey.IsValkeyNil(res.Error()) {
			l.mu.Lock()
			stale := l.heartbeat[connectorID]
			if stale != nil {
				delete(l.heartbeat, connectorID)
			}
			l.mu.Unlock()
			if stale != nil {
				stale.triggerLost()
			}
			return nil, ErrNoOwner
		}
		return nil, ErrLocatorUnavailable
	}

	val, err := res.ToString()
	if err != nil {
		return nil, ErrLocatorUnavailable
	}
	var record ConnectorOwnerRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, ErrLocatorUnavailable
	}
	if record.PodID == "" || ValidateTargetAddr(record.PodAddr, "") != nil {
		return nil, ErrLocatorUnavailable
	}

	l.mu.Lock()
	var stale *connectorLease
	if current, exists := l.heartbeat[connectorID]; exists {
		if record.PodID != l.podID || record.Epoch > current.epoch {
			stale = current
			delete(l.heartbeat, connectorID)
		}
	}
	l.mu.Unlock()
	if stale != nil {
		stale.triggerLost()
	}

	return &record, nil
}

// RegisterTerminal은 TerminalSession 소유자를 등록하고 백그라운드 heartbeat를 시작한다.
func (l *Locator) RegisterTerminal(ctx context.Context, sessionID string, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	if l == nil || l.client == nil {
		return ErrLocatorUnavailable
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	if epoch <= 0 {
		epoch = 1
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
		return ErrLocatorUnavailable
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrLocatorUnavailable
	}
	if prev, exists := l.terminalHeartbeat[sessionID]; exists {
		prev.cancel()
	}
	hbCtx, hbCancel := context.WithCancel(context.Background())
	tLease := &terminalLease{
		epoch:  epoch,
		cancel: hbCancel,
	}
	l.terminalHeartbeat[sessionID] = tLease
	l.mu.Unlock()

	go l.runTerminalHeartbeat(hbCtx, sessionID, connectorID, tLease, ttl)

	return nil
}

func (l *Locator) runTerminalHeartbeat(ctx context.Context, sessionID string, connectorID uuid.UUID, tLease *terminalLease, ttl time.Duration) {
	interval := ttl / 3
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	leaseExpiresAt := time.Now().Add(ttl)

	clearLease := func() {
		l.mu.Lock()
		if current, ok := l.terminalHeartbeat[sessionID]; ok && current == tLease {
			delete(l.terminalHeartbeat, sessionID)
		}
		l.mu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := l.RefreshTerminal(refCtx, sessionID, connectorID, tLease.epoch, ttl)
			cancel()
			if err != nil {
				if errors.Is(err, ErrStaleEpoch) || errors.Is(err, ErrNoOwner) {
					clearLease()
					return
				}
				if time.Now().After(leaseExpiresAt) {
					clearLease()
					return
				}
				continue
			}
			leaseExpiresAt = time.Now().Add(ttl)
		}
	}
}

// RefreshTerminal은 TerminalSession 소유자 lease를 갱신한다.
func (l *Locator) RefreshTerminal(ctx context.Context, sessionID string, connectorID uuid.UUID, epoch int64, ttl time.Duration) error {
	if l == nil || l.client == nil {
		return ErrLocatorUnavailable
	}
	if epoch <= 0 {
		epoch = 1
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

	res := refreshTerminalLua.Exec(ctx, l.client, []string{terminalKey(sessionID)}, []string{
		l.podID,
		string(bytes),
		strconv.Itoa(ttlSec),
		strconv.FormatInt(epoch, 10),
	})
	if res.Error() != nil {
		if res.Error().Error() == "STALE_EPOCH" {
			return ErrStaleEpoch
		}
		if res.Error().Error() == "NO_OWNER" {
			return ErrNoOwner
		}
		return ErrLocatorUnavailable
	}
	return nil
}

// LookupTerminal은 TerminalSession의 소유 Pod를 조회한다.
func (l *Locator) LookupTerminal(ctx context.Context, sessionID string) (*TerminalOwnerRecord, error) {
	if l == nil || l.client == nil {
		return nil, ErrLocatorUnavailable
	}
	cmd := l.client.B().Get().Key(terminalKey(sessionID)).Build()
	res := l.client.Do(ctx, cmd)
	if res.Error() != nil {
		if valkey.IsValkeyNil(res.Error()) {
			return nil, ErrNoOwner
		}
		return nil, ErrLocatorUnavailable
	}
	val, err := res.ToString()
	if err != nil {
		return nil, ErrLocatorUnavailable
	}
	var record TerminalOwnerRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, ErrLocatorUnavailable
	}
	if record.PodID == "" || ValidateTargetAddr(record.PodAddr, "") != nil {
		return nil, ErrLocatorUnavailable
	}
	return &record, nil
}

// ReleaseTerminal은 TerminalSession 소유자 키를 삭제하고 heartbeat를 중지한다.
func (l *Locator) ReleaseTerminal(ctx context.Context, sessionID string) error {
	if l == nil || l.client == nil {
		return ErrLocatorUnavailable
	}
	var epoch int64
	l.mu.Lock()
	if current, exists := l.terminalHeartbeat[sessionID]; exists {
		epoch = current.epoch
		current.cancel()
		delete(l.terminalHeartbeat, sessionID)
	}
	l.mu.Unlock()

	res := releaseTerminalLua.Exec(ctx, l.client, []string{terminalKey(sessionID)}, []string{
		l.podID,
		strconv.FormatInt(epoch, 10),
	})
	if res.Error() != nil {
		return ErrLocatorUnavailable
	}
	return nil
}

// Close는 활성 heartbeat를 모두 정리하고 클라이언트를 종료한다.
func (l *Locator) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	for _, lease := range l.heartbeat {
		lease.cancel()
	}
	l.heartbeat = make(map[uuid.UUID]*connectorLease)
	for _, tLease := range l.terminalHeartbeat {
		tLease.cancel()
	}
	l.terminalHeartbeat = make(map[string]*terminalLease)
	l.mu.Unlock()
	if l.client != nil {
		l.client.Close()
	}
}
