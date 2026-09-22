package heartbeat

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
)

// MessageSender 는 하트비트 메시지를 전송하는 인터페이스입니다.
type MessageSender interface {
	SendMessage(ctx context.Context, msg interface{}) error
}

// Runner 는 SaaS 로 주기적인 HEARTBEAT 메시지 발송을 담당하는 러너입니다.
type Runner struct {
	sender         MessageSender
	interval       time.Duration
	offlineTimeout time.Duration
}

// NewRunner 는 새 Heartbeat Runner 인스턴스를 생성합니다.
func NewRunner(sender MessageSender, interval, offlineTimeout time.Duration) *Runner {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if offlineTimeout <= 0 {
		offlineTimeout = 45 * time.Second
	}
	return &Runner{
		sender:         sender,
		interval:       interval,
		offlineTimeout: offlineTimeout,
	}
}

// Interval 은 하트비트 발송 주기를 반환합니다.
func (r *Runner) Interval() time.Duration {
	return r.interval
}

// OfflineTimeout 은 단절 판단 기준 시간을 반환합니다.
func (r *Runner) OfflineTimeout() time.Duration {
	return r.offlineTimeout
}

// Start 는 백그라운드 고루틴에서 주기적으로 HEARTBEAT 를 발송하며,
// 전송 실패 시 에러를 전달하는 채널을 반환합니다.
func (r *Runner) Start(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				msg := protocol.HeartbeatMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:      protocol.MessageTypeHeartbeat,
						MessageID: generateUUID(),
						SentAt:    t.UTC(),
					},
					Payload: protocol.HeartbeatPayload{
						ObservedAt: t.UTC(),
					},
				}

				if err := r.sender.SendMessage(ctx, msg); err != nil {
					select {
					case errCh <- fmt.Errorf("failed to send heartbeat: %w", err):
					default:
					}
					return
				}
			}
		}
	}()

	return errCh
}

func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
