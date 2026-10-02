package preview

import (
	"context"
	"fmt"
	"net"
)

// TCPForwarder 는 대상 VM의 특정 포트로 TCP 연결을 수립하는 인터페이스입니다.
// OP-03(SSH Direct TCP-IP forwarding channel) 구현체 또는 로컬 다이얼러를 주입받을 수 있습니다.
type TCPForwarder interface {
	DialTCP(ctx context.Context, targetVmKey, serverID string, port int) (net.Conn, error)
}

// DirectTCPForwarder 는 대상 주소로 직접 net.Dial 을 수행하는 TCPForwarder 기본 구현체입니다.
type DirectTCPForwarder struct {
	AddressResolver func(ctx context.Context, targetVmKey, serverID string) (string, error)
}

// NewDirectTCPForwarder 는 지정된 주소 변환기를 사용하는 DirectTCPForwarder 를 반환합니다.
func NewDirectTCPForwarder(resolver func(ctx context.Context, targetVmKey, serverID string) (string, error)) *DirectTCPForwarder {
	return &DirectTCPForwarder{
		AddressResolver: resolver,
	}
}

// DialTCP 는 대상 주소를 결정한 후 TCP 연결을 수립합니다.
func (f *DirectTCPForwarder) DialTCP(ctx context.Context, targetVmKey, serverID string, port int) (net.Conn, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be 1-65535", port)
	}

	host := "127.0.0.1"
	if f.AddressResolver != nil {
		resolved, err := f.AddressResolver(ctx, targetVmKey, serverID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve address for target %s (server %s): %w", targetVmKey, serverID, err)
		}
		if resolved != "" {
			host = resolved
		}
	}

	targetAddr := fmt.Sprintf("%s:%d", host, port)
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", targetAddr)
}
