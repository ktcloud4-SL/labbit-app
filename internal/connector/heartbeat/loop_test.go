package heartbeat_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/heartbeat"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

type mockSender struct {
	mu       sync.Mutex
	messages []interface{}
	sendErr  error
}

func (m *mockSender) SendMessage(ctx context.Context, msg interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.messages = append(m.messages, msg)
	return nil
}

func (m *mockSender) getMessages() []interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]interface{}, len(m.messages))
	copy(res, m.messages)
	return res
}

func TestHeartbeatRunner_PeriodicHeartbeat(t *testing.T) {
	sender := &mockSender{}
	interval := 20 * time.Millisecond
	offlineTimeout := 60 * time.Millisecond

	runner := heartbeat.NewRunner(sender, interval, offlineTimeout)
	if runner.Interval() != interval {
		t.Fatalf("expected interval %v, got %v", interval, runner.Interval())
	}
	if runner.OfflineTimeout() != offlineTimeout {
		t.Fatalf("expected offlineTimeout %v, got %v", offlineTimeout, runner.OfflineTimeout())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := runner.Start(ctx)

	// 하트비트가 최소 3번 발송될 때까지 대기
	time.Sleep(80 * time.Millisecond)
	cancel()

	// 취소 후 에러 채널 닫힘 확인
	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			t.Fatalf("unexpected error after cancel: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for runner to stop")
	}

	msgs := sender.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 heartbeats, got %d", len(msgs))
	}

	for _, m := range msgs {
		hb, ok := m.(protocol.HeartbeatMessage)
		if !ok {
			t.Fatalf("expected HeartbeatMessage, got %T", m)
		}
		if hb.Type != protocol.MessageTypeHeartbeat {
			t.Fatalf("expected type %s, got %s", protocol.MessageTypeHeartbeat, hb.Type)
		}
		if hb.MessageID == "" {
			t.Fatal("expected non-empty messageId")
		}
		if hb.Payload.ObservedAt.IsZero() {
			t.Fatal("expected non-zero observedAt")
		}
	}
}

func TestHeartbeatRunner_SendFailure(t *testing.T) {
	expectedErr := errors.New("network socket closed")
	sender := &mockSender{
		sendErr: expectedErr,
	}

	runner := heartbeat.NewRunner(sender, 10*time.Millisecond, 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	errCh := runner.Start(ctx)

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error on send failure, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for send failure error")
	}
}

func TestHeartbeatRunner_DefaultValues(t *testing.T) {
	sender := &mockSender{}
	runner := heartbeat.NewRunner(sender, 0, 0)

	if runner.Interval() != 15*time.Second {
		t.Fatalf("expected default interval 15s, got %v", runner.Interval())
	}
	if runner.OfflineTimeout() != 45*time.Second {
		t.Fatalf("expected default offlineTimeout 45s, got %v", runner.OfflineTimeout())
	}
}
