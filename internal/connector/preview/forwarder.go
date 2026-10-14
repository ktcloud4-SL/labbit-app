package preview

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// 미리 정의된 Preview Forwarding 표준 에러
var (
	// ErrPortRejected 는 SSH 포트 22번이거나 허용 범위를 벗어난 포트일 때 반환됩니다.
	ErrPortRejected = errors.New("preview: port rejected (port 22 or invalid port)")
	// ErrAppNotRunning 은 대상 포트에서 애플리케이션이 listen하고 있지 않을 때(Connection refused) 반환됩니다.
	ErrAppNotRunning = errors.New("preview: application not running on target port")
	// ErrVMUnreachable 은 VM 또는 호스트에 연결할 수 없거나 타임아웃일 때 반환됩니다.
	ErrVMUnreachable = errors.New("preview: target VM or host unreachable")
)

// TCPForwarder 는 대상 VM의 특정 포트로 TCP 연결을 수립하는 인터페이스입니다.
// LBT-24(SSH Direct TCP-IP forwarding channel) 구현체 또는 테스트용 포워더를 주입받을 수 있습니다.
type TCPForwarder interface {
	DialTCP(ctx context.Context, targetVmKey, serverID string, port int) (net.Conn, error)
}

// DirectTCPForwarder 는 테스트 및 로컬 환경에서 지정된 주소 변환기를 거쳐 TCP 연결을 수립하는 TCPForwarder 기본 구현체입니다.
// 프로덕션 환경에서는 보안 정책에 따라 Direct TCP Fallback이 금지되며, LBT-24(SSH Direct TCP-IP forwarding)가 필수입니다.
type DirectTCPForwarder struct {
	AddressResolver func(ctx context.Context, targetVmKey, serverID string) (string, error)
}

// NewDirectTCPForwarder 는 지정된 주소 변환기를 사용하는 DirectTCPForwarder 를 반환합니다.
func NewDirectTCPForwarder(resolver func(ctx context.Context, targetVmKey, serverID string) (string, error)) *DirectTCPForwarder {
	return &DirectTCPForwarder{
		AddressResolver: resolver,
	}
}

// DialTCP 는 대상 주소를 결정한 후 TCP 연결을 수립합니다. 포트 22번(SSH)은 보안 정책상 거절됩니다.
func (f *DirectTCPForwarder) DialTCP(ctx context.Context, targetVmKey, serverID string, port int) (net.Conn, error) {
	if port <= 0 || port > 65535 || port == 22 {
		return nil, fmt.Errorf("%w: port %d is not allowed", ErrPortRejected, port)
	}

	host := "127.0.0.1"
	if f.AddressResolver != nil {
		resolved, err := f.AddressResolver(ctx, targetVmKey, serverID)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve failed: %v", ErrVMUnreachable, err)
		}
		if resolved != "" {
			host = resolved
		}
	}

	targetAddr := fmt.Sprintf("%s:%d", host, port)
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused") {
			return nil, fmt.Errorf("%w: %v", ErrAppNotRunning, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrVMUnreachable, err)
	}
	return conn, nil
}
