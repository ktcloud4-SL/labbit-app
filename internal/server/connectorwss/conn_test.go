package connectorwss

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsEvent는 fakeWS가 받은 write 하나다.
type wsEvent struct {
	kind string // "message", "control"
	code int    // control일 때 close code
}

// fakeWS는 wsConn의 fake다. 호출 순서를 기록하고, write를 gate로 붙잡아 "진행 중인 write"를 재현한다.
// gorilla와 달리 close frame 뒤의 write를 스스로 거절하지 않으므로, close 이후의 거절은 controlConn의 책임으로만 관찰된다.
type fakeWS struct {
	mu     sync.Mutex
	events []wsEvent

	messageGate    chan struct{} // nil이 아니면 WriteMessage가 이 channel이 닫힐 때까지 기다린다.
	messageEntered chan struct{} // WriteMessage가 처음 진입하면 닫힌다.
	controlGate    chan struct{}
	controlEntered chan struct{}
	controlErr     error

	messageOnce, controlOnce sync.Once
}

func (f *fakeWS) SetWriteDeadline(time.Time) error { return nil }

func (f *fakeWS) WriteMessage(int, []byte) error {
	f.mu.Lock()
	f.events = append(f.events, wsEvent{kind: "message"})
	f.mu.Unlock()
	if f.messageEntered != nil {
		f.messageOnce.Do(func() { close(f.messageEntered) })
	}
	if f.messageGate != nil {
		<-f.messageGate
	}
	return nil
}

func (f *fakeWS) WriteControl(_ int, data []byte, _ time.Time) error {
	code := 0
	if len(data) >= 2 {
		code = int(binary.BigEndian.Uint16(data))
	}
	f.mu.Lock()
	f.events = append(f.events, wsEvent{kind: "control", code: code})
	f.mu.Unlock()
	if f.controlEntered != nil {
		f.controlOnce.Do(func() { close(f.controlEntered) })
	}
	if f.controlGate != nil {
		<-f.controlGate
	}
	return f.controlErr
}

func (f *fakeWS) Close() error { return nil }

func (f *fakeWS) recorded() []wsEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wsEvent(nil), f.events...)
}

func requireEvents(t *testing.T, f *fakeWS, want ...wsEvent) {
	t.Helper()
	got := f.recorded()
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %+v, want %+v", got, want)
		}
	}
}

func requireStillRunning(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s이(가) 기다려야 할 작업이 끝나기 전에 끝남", what)
	case <-time.After(150 * time.Millisecond):
	}
}

// close가 시작된 뒤의 application write는 close frame 전송의 성패, 상대의 응답, TCP 상태와 무관하게 이 connection의 closing 상태로
// 거절되며 아무것도 쓰지 않는다.
func TestWriteAfterCloseIsRejectedLocallyRegardlessOfCloseFrameOutcome(t *testing.T) {
	tests := map[string]error{
		"close frame 전송 성공":         nil,
		"close frame 전송 실패(TCP 오류)": errors.New("write tcp: broken pipe"),
		"close frame 전송 시간 초과":      errors.New("websocket: write timeout"),
	}
	for name, controlErr := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeWS{controlErr: controlErr}
			cc := newControlConn(fake)
			defer cc.release()

			cc.close(closeReplaced, "replaced")
			for range 3 {
				if err := cc.writeJSON(map[string]any{"type": "COMMAND"}); !errors.Is(err, errConnClosing) {
					t.Fatalf("close 뒤 writeJSON() error = %v, want errConnClosing", err)
				}
			}
			requireEvents(t, fake, wsEvent{"control", closeReplaced})
			if !cc.isClosing() {
				t.Fatal("close 뒤 isClosing() = false")
			}
		})
	}
}

// close가 먼저 소유권을 얻으면(close frame 전송 중) 그동안 들어온 writeJSON은 기다렸다가, close가 끝난 뒤 거절된다.
// close frame이 나가기 전이든 후든 그 writeJSON은 application message를 쓰지 않는다.
func TestWritersWaitingDuringCloseAreRejectedWithoutWriting(t *testing.T) {
	fake := &fakeWS{controlGate: make(chan struct{}), controlEntered: make(chan struct{})}
	cc := newControlConn(fake)
	defer cc.release()

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		cc.close(closeCredentialRevoked, "revoked")
	}()
	<-fake.controlEntered // close가 소유권을 얻고 close frame을 쓰는 중이다.

	const writers = 4
	results := make(chan error, writers)
	for range writers {
		go func() { results <- cc.writeJSON(map[string]any{"type": "COMMAND"}) }()
	}
	// close가 끝나기 전에는 writer들이 끝나지 않고, 그동안 application message는 하나도 나가지 않는다.
	select {
	case err := <-results:
		t.Fatalf("close가 진행 중인데 writeJSON이 끝남: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	requireEvents(t, fake, wsEvent{"control", closeCredentialRevoked})

	close(fake.controlGate)
	<-closed
	for range writers {
		if err := <-results; !errors.Is(err, errConnClosing) {
			t.Fatalf("writeJSON() error = %v, want errConnClosing", err)
		}
	}
	requireEvents(t, fake, wsEvent{"control", closeCredentialRevoked})
}

// write가 먼저 소유권을 얻었다면 그 write가 끝난 뒤에 close가 진행된다(data complete → close).
func TestInProgressWriteCompletesBeforeClose(t *testing.T) {
	fake := &fakeWS{messageGate: make(chan struct{}), messageEntered: make(chan struct{})}
	cc := newControlConn(fake)
	defer cc.release()

	writeDone := make(chan error, 1)
	go func() { writeDone <- cc.writeJSON(map[string]any{"type": "COMMAND"}) }()
	<-fake.messageEntered // write가 소유권을 얻고 쓰는 중이다.

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		cc.close(closeReplaced, "replaced")
	}()
	// write가 끝나기 전에는 close frame이 나가지 않고 closing으로 전환되지도 않는다.
	requireStillRunning(t, closeDone, "close")
	requireEvents(t, fake, wsEvent{"message", 0})
	if cc.isClosing() {
		t.Fatal("진행 중인 write가 끝나기 전에 closing으로 전환됨")
	}

	close(fake.messageGate)
	if err := <-writeDone; err != nil {
		t.Fatalf("진행 중이던 writeJSON() error = %v, want nil", err)
	}
	<-closeDone
	requireEvents(t, fake, wsEvent{"message", 0}, wsEvent{"control", closeReplaced})

	if err := cc.writeJSON(map[string]any{"type": "LATE"}); !errors.Is(err, errConnClosing) {
		t.Fatalf("close 뒤 writeJSON() error = %v, want errConnClosing", err)
	}
}

// 처음 요청한 close 이유만 적용된다.
func TestFirstCloseReasonWins(t *testing.T) {
	t.Run("순차 요청", func(t *testing.T) {
		fake := &fakeWS{}
		cc := newControlConn(fake)
		defer cc.release()

		cc.close(closeReplaced, "first")
		cc.close(closeCredentialRevoked, "second")
		cc.close(websocket.CloseGoingAway, "third")
		requireEvents(t, fake, wsEvent{"control", closeReplaced})
	})
	t.Run("동시 요청", func(t *testing.T) {
		fake := &fakeWS{}
		cc := newControlConn(fake)
		defer cc.release()

		codes := []int{closeReplaced, closeCredentialRevoked, websocket.CloseGoingAway, closeProtocolError}
		var wg sync.WaitGroup
		for _, code := range codes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cc.close(code, "reason")
			}()
		}
		wg.Wait()

		events := fake.recorded()
		if len(events) != 1 || events[0].kind != "control" {
			t.Fatalf("events = %+v, want close frame 정확히 1개", events)
		}
	})
}

// write와 close가 동시에 경쟁해도 close frame 이후에는 어떤 application message도 쓰지 않는다.
// 관측 가능한 결과는 "message들 → close" 뿐이며, 성공한 writeJSON 수와 쓰인 message 수가 같다. -race로 반복 실행한다.
func TestConcurrentWritesAndCloseNeverWriteAfterClose(t *testing.T) {
	const iterations, writers, perWriter = 300, 6, 3
	for range iterations {
		fake := &fakeWS{}
		cc := newControlConn(fake)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var okMu sync.Mutex
		succeeded := 0
		for range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range perWriter {
					err := cc.writeJSON(map[string]any{"type": "COMMAND"})
					switch {
					case err == nil:
						okMu.Lock()
						succeeded++
						okMu.Unlock()
					case !errors.Is(err, errConnClosing):
						t.Errorf("writeJSON() error = %v, want nil 또는 errConnClosing", err)
						return
					}
					runtime.Gosched()
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			runtime.Gosched()
			cc.close(closeReplaced, "replaced")
		}()
		close(start)
		wg.Wait()
		cc.release()

		events := fake.recorded()
		controlAt, messages := -1, 0
		for i, event := range events {
			switch event.kind {
			case "control":
				if controlAt >= 0 {
					t.Fatalf("close frame이 두 번 나감: %+v", events)
				}
				controlAt = i
			case "message":
				if controlAt >= 0 {
					t.Fatalf("close frame 뒤에 application message가 나감: %+v", events)
				}
				messages++
			}
		}
		if controlAt < 0 {
			t.Fatalf("close frame이 나가지 않음: %+v", events)
		}
		if messages != succeeded {
			t.Fatalf("쓰인 message = %d, 성공한 writeJSON = %d", messages, succeeded)
		}
	}
}

// 한 connection의 close/write가 막혀 있어도 다른 connection은 영향을 받지 않는다(전역 lock 없음).
func TestConnectionsDoNotBlockEachOther(t *testing.T) {
	blocked := &fakeWS{controlGate: make(chan struct{}), controlEntered: make(chan struct{})}
	ccBlocked := newControlConn(blocked)
	defer ccBlocked.release()
	go ccBlocked.close(closeReplaced, "replaced")
	<-blocked.controlEntered

	other := &fakeWS{}
	ccOther := newControlConn(other)
	defer ccOther.release()

	done := make(chan error, 1)
	go func() { done <- ccOther.writeJSON(map[string]any{"type": "COMMAND"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("다른 connection writeJSON() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("한 connection의 close가 다른 connection의 write를 막음")
	}
	close(blocked.controlGate)
	requireEvents(t, other, wsEvent{"message", 0})
}

// 실제 WebSocket에서 peer가 close에 응답하지 않아 socket이 closeGrace 동안 살아 있어도, close 뒤의 writeJSON은 gorilla가
// 아니라 controlConn의 closing 상태로 거절되고 상대에게는 close frame이 가장 먼저(data frame 없이) 도착한다.
func TestWriteAfterCloseIsRejectedByClosingStateWhileSocketIsStillOpen(t *testing.T) {
	ready := make(chan *controlConn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ready <- newControlConn(conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cc := <-ready
	defer cc.release()

	// client는 read하지 않으므로 close에 응답하지 않는다. socket은 closeGrace 동안 열려 있다.
	closedAt := time.Now()
	cc.close(closeReplaced, "replaced")
	err = cc.writeJSON(map[string]any{"type": "COMMAND"})
	if !errors.Is(err, errConnClosing) || errors.Is(err, websocket.ErrCloseSent) {
		t.Fatalf("close 뒤 writeJSON() error = %v, want errConnClosing(gorilla의 ErrCloseSent가 아님)", err)
	}
	if elapsed := time.Since(closedAt); elapsed >= closeGrace {
		t.Fatalf("검사가 %v 걸려 closeGrace(%v)가 지나 socket 상태 전제가 깨짐", elapsed, closeGrace)
	}

	// 이제 client가 읽으면 첫 frame이 close이고 그 앞에 data frame이 없다.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != closeReplaced {
		t.Fatalf("client가 처음 받은 것 = %v, want close %d (data frame 없이)", err, closeReplaced)
	}
}
