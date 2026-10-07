package realtime

import (
	"errors"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// TestOutQueueCloseWithFinalNormal은 정상 큐 상태에서 기존 프레임을 flush하고
// 마지막 application 프레임과 close 프레임이 원자적으로 순서대로 등록됨을 검증한다.
func TestOutQueueCloseWithFinalNormal(t *testing.T) {
	q := newOutQueue(1024, 10)

	// 1. 기존 Binary 프레임 push
	if err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("frame 1")}); err != nil {
		t.Fatalf("push frame 1 error: %v", err)
	}
	if err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("frame 2")}); err != nil {
		t.Fatalf("push frame 2 error: %v", err)
	}

	// 2. LIVE_ENDED + closeKind 등록
	finalData := []byte(`{"type":"LIVE_ENDED"}`)
	q.closeWithFinal(websocket.TextMessage, finalData, 4006, "live ended")

	// 3. closeWithFinal 이후의 push는 즉시 errPeerClosed로 거절되어야 함
	err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("late frame")})
	if !errors.Is(err, errPeerClosed) {
		t.Fatalf("expected errPeerClosed after closeWithFinal, got: %v", err)
	}

	// 4. 큐 순서 검증: frame 1 -> frame 2 -> LIVE_ENDED -> closeKind
	f1, ok := q.next()
	if !ok || f1.kind != websocket.BinaryMessage || string(f1.data) != "frame 1" {
		t.Fatalf("expected frame 1, got %+v", f1)
	}
	f2, ok := q.next()
	if !ok || f2.kind != websocket.BinaryMessage || string(f2.data) != "frame 2" {
		t.Fatalf("expected frame 2, got %+v", f2)
	}
	f3, ok := q.next()
	if !ok || f3.kind != websocket.TextMessage || string(f3.data) != string(finalData) {
		t.Fatalf("expected LIVE_ENDED text frame, got %+v", f3)
	}
	f4, ok := q.next()
	if !ok || f4.kind != closeKind || f4.code != 4006 || f4.text != "live ended" {
		t.Fatalf("expected closeKind frame with code 4006, got %+v", f4)
	}

	// 5. 더 이상 프레임이 없어야 함
	if _, ok := q.next(); ok {
		t.Fatalf("expected queue to be exhausted after closeKind")
	}
}

// TestOutQueueCloseWithFinalSlowQueueFull은 이미 큐가 가득 찬 느린 수신자에서
// 밀린 backlog를 버리고 마지막 application 프레임과 close 프레임이 전달됨을 검증한다.
func TestOutQueueCloseWithFinalSlowQueueFull(t *testing.T) {
	q := newOutQueue(1024, 2) // 용량 2개

	// 1. 큐를 가득 채움
	if err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("old frame 1")}); err != nil {
		t.Fatalf("push 1 error: %v", err)
	}
	if err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("old frame 2")}); err != nil {
		t.Fatalf("push 2 error: %v", err)
	}

	// 2. 가득 찬 상태에서 closeWithFinal 호출
	finalData := []byte(`{"type":"LIVE_ENDED"}`)
	q.closeWithFinal(websocket.TextMessage, finalData, 4006, "live ended")

	// 3. backlog(old frame 1, 2)는 버려지고, LIVE_ENDED와 closeKind만 순서대로 나와야 함
	f1, ok := q.next()
	if !ok || f1.kind != websocket.TextMessage || string(f1.data) != string(finalData) {
		t.Fatalf("expected LIVE_ENDED text frame after backlog drop, got %+v", f1)
	}
	f2, ok := q.next()
	if !ok || f2.kind != closeKind || f2.code != 4006 {
		t.Fatalf("expected closeKind frame with code 4006, got %+v", f2)
	}
	if _, ok := q.next(); ok {
		t.Fatalf("expected queue to be exhausted")
	}
}

// TestOutQueueConcurrentPushAndCloseWithFinalOrdering은 다수의 고루틴이 프레임을 넣는 도중
// closeWithFinal이 호출되어도 LIVE_ENDED 뒤에 Binary 프레임이 끼어들지 않음을 결정적으로 검증한다.
func TestOutQueueConcurrentPushAndCloseWithFinalOrdering(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		q := newOutQueue(1024*1024, 1000)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				err := q.push(frame{kind: websocket.BinaryMessage, data: []byte("output data")})
				if errors.Is(err, errPeerClosed) {
					break
				}
			}
		}()

		go func() {
			defer wg.Done()
			<-start
			q.closeWithFinal(websocket.TextMessage, []byte("LIVE_ENDED"), 4006, "live ended")
		}()

		close(start)
		wg.Wait()

		// 큐를 drain하면서 LIVE_ENDED 이후에 Binary 프레임이 오는지 검증
		seenLiveEnded := false
		for {
			f, ok := q.next()
			if !ok {
				break
			}
			if seenLiveEnded {
				// LIVE_ENDED를 본 이후에는 오직 closeKind만 허용됨
				if f.kind != closeKind {
					t.Fatalf("iter %d: frame of kind %d received after LIVE_ENDED! Binary frames must never follow LIVE_ENDED", iter, f.kind)
				}
			}
			if f.kind == websocket.TextMessage && string(f.data) == "LIVE_ENDED" {
				seenLiveEnded = true
			}
		}

		if !seenLiveEnded {
			t.Fatalf("iter %d: LIVE_ENDED was never queued", iter)
		}
	}
}

// TestPeerCloseWithFinalMessageDeterministicInterleaving은 dataOutput이 subscriber 목록을 복사한 뒤
// finishLive가 closeWithFinalMessage를 호출하고 dataOutput이 push를 시도하는 interleaving을 결정적으로 검증한다.
func TestPeerCloseWithFinalMessageDeterministicInterleaving(t *testing.T) {
	p := &peer{
		q:    newOutQueue(1024, 10),
		done: make(chan struct{}),
	}
	sub := newLiveSubscriber(p, nil)

	// G2: finishLive가 closeWithFinalMessage 호출
	finalData := []byte(`{"type":"LIVE_ENDED"}`)
	sub.closeWithFinalMessage(websocket.TextMessage, finalData, 4006, "live ended")

	// G1: dataOutput이 뒤늦게 Binary 출력 전송 시도
	err := sub.p.send(websocket.BinaryMessage, []byte("late pty data"))
	if !errors.Is(err, errPeerClosed) {
		t.Fatalf("expected errPeerClosed for late binary send, got: %v", err)
	}

	// Queue drain: LIVE_ENDED -> closeKind (late binary는 절대 들어가지 않음)
	f1, ok := sub.p.q.next()
	if !ok || f1.kind != websocket.TextMessage || string(f1.data) != string(finalData) {
		t.Fatalf("expected LIVE_ENDED, got %+v", f1)
	}
	f2, ok := sub.p.q.next()
	if !ok || f2.kind != closeKind || f2.code != 4006 {
		t.Fatalf("expected closeKind, got %+v", f2)
	}
	if _, ok := sub.p.q.next(); ok {
		t.Fatalf("late pty data was incorrectly queued after LIVE_ENDED")
	}
}
