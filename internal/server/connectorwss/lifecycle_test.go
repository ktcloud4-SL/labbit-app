package connectorwss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// OFFLINE timer test용 짧은 값이다. 여유(12배)를 두어 -race와 느린 CI에서도 흔들리지 않게 한다.
const (
	testHeartbeatInterval = 25 * time.Millisecond
	testOfflineTimeout    = 300 * time.Millisecond
)

func shortHeartbeat(o *Options) {
	o.HeartbeatInterval = testHeartbeatInterval
	o.OfflineTimeout = testOfflineTimeout
}

// registryReleased는 connectorID의 Session이 registry에서 사라질 때까지 기다린다.
func registryReleased(t *testing.T, h *harness) {
	t.Helper()
	waitFor(t, "registry release", func() bool {
		_, ok := h.registry.Current(h.principal.ConnectorID)
		return !ok
	})
}

// HELLO 뒤에 HEARTBEAT가 없으면 OFFLINE으로 판단해 registry에서 해제하고 표준 1001로 끝낸다.
func TestOfflineWithoutHeartbeatClosesAndReleases(t *testing.T) {
	h := newHarness(t, shortHeartbeat)
	p := h.establish()

	started := time.Now()
	closeErr := p.waitClosed(5 * time.Second)
	elapsed := time.Since(started)

	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want %d %q", closeErr.Code, closeErr.Text, websocket.CloseGoingAway, "heartbeat timeout")
	}
	if elapsed < testOfflineTimeout/2 {
		t.Fatalf("%v 만에 닫힘: offline timeout(%v)보다 훨씬 일찍 OFFLINE 처리됨", elapsed, testOfflineTimeout)
	}
	registryReleased(t, h)
	if got := h.beats.recorded(); len(got) != 0 {
		t.Fatalf("HEARTBEAT 없이 기록됨: %+v", got)
	}
}

// 유효한 HEARTBEAT를 반복하면 offline timeout보다 오래 연결이 유지되고, 멈추면 timeout 뒤에 OFFLINE 처리된다.
func TestValidHeartbeatsKeepConnectionPastOfflineTimeout(t *testing.T) {
	h := newHarness(t, shortHeartbeat)
	p := h.establish()
	session := h.waitRegistered()

	deadline := time.Now().Add(4 * testOfflineTimeout)
	for time.Now().Before(deadline) {
		p.heartbeat()
		time.Sleep(testHeartbeatInterval)
		if p.closedNow() {
			t.Fatal("유효한 HEARTBEAT를 보내는 중에 connection이 닫힘")
		}
	}
	if got, ok := h.registry.Current(h.principal.ConnectorID); !ok || got != session {
		t.Fatalf("registry = %+v, %v, want %+v", got, ok, session)
	}
	if n := len(h.beats.recorded()); n < 10 {
		t.Fatalf("기록된 heartbeat = %d, want >= 10", n)
	}

	// heartbeat를 멈추면 마지막 heartbeat 기준 offline timeout 뒤에 OFFLINE이다.
	stopped := time.Now()
	closeErr := p.waitClosed(5 * time.Second)
	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want 1001 heartbeat timeout", closeErr.Code, closeErr.Text)
	}
	if elapsed := time.Since(stopped); elapsed < testOfflineTimeout/2 {
		t.Fatalf("heartbeat를 멈춘 지 %v 만에 닫힘: 마지막 heartbeat 기준 timeout이 지켜지지 않음", elapsed)
	}
	registryReleased(t, h)
}

// WebSocket Ping/Pong은 transport health일 뿐 application heartbeat가 아니다. pong이 계속 돌아와도 OFFLINE을 막지 못한다.
func TestPingPongDoesNotPreventOffline(t *testing.T) {
	h := newHarness(t, shortHeartbeat)
	p := h.establish()

	stop := make(chan struct{})
	pinging := make(chan struct{})
	go func() {
		defer close(pinging)
		ticker := time.NewTicker(testHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				p.ping()
			}
		}
	}()
	closeErr := p.waitClosed(5 * time.Second)
	close(stop)
	<-pinging

	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want 1001 heartbeat timeout", closeErr.Code, closeErr.Text)
	}
	if pongs := p.pongs.Load(); pongs < 3 {
		t.Fatalf("pong = %d, want >= 3: ping이 처리되는 살아 있는 연결이어야 함", pongs)
	}
	registryReleased(t, h)
}

// HEARTBEAT가 아닌 message(알 수 없는 type, 대소문자만 다른 type, non-JSON, binary)와 잘못된 저장소 상태는 deadline을 연장하지 않는다.
func TestOtherTrafficDoesNotPreventOffline(t *testing.T) {
	h := newHarness(t, shortHeartbeat)
	p := h.establish()

	stop := make(chan struct{})
	sending := make(chan struct{})
	go func() {
		defer close(sending)
		ticker := time.NewTicker(testHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// 이 goroutine만 write한다. 연결이 닫힌 뒤의 write 오류는 무시한다.
				now := time.Now().UTC().Format(time.RFC3339Nano)
				_ = p.conn.WriteJSON(map[string]any{"type": "OPERATION_RESULT", "messageId": uuid.NewString(), "sentAt": now, "payload": map[string]any{}})
				_ = p.conn.WriteJSON(map[string]any{"TYPE": "HEARTBEAT", "messageId": uuid.NewString(), "sentAt": now, "payload": map[string]any{"observedAt": now}})
				_ = p.conn.WriteMessage(websocket.TextMessage, []byte("not json"))
				_ = p.conn.WriteMessage(websocket.BinaryMessage, []byte{1})
			}
		}
	}()
	closeErr := p.waitClosed(5 * time.Second)
	close(stop)
	<-sending

	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want 1001 heartbeat timeout", closeErr.Code, closeErr.Text)
	}
	if got := h.beats.recorded(); len(got) != 0 {
		t.Fatalf("HEARTBEAT가 아닌 traffic이 기록됨: %+v", got)
	}
}

// 저장소 장애로 기록하지 못한 HEARTBEAT는 OFFLINE deadline을 연장하지 않는다. 연결은 timeout까지 유지되고 오류 원문은 log에 남지 않는다.
func TestHeartbeatStorageFailureDoesNotExtendOfflineDeadline(t *testing.T) {
	h := newHarness(t, shortHeartbeat)
	h.beats.failWith(errors.New("dial tcp 10.0.0.9:5432: connection refused " + echoCheckMarker))
	p := h.establish()

	stop := make(chan struct{})
	sending := make(chan struct{})
	go func() {
		defer close(sending)
		ticker := time.NewTicker(testHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = p.conn.WriteJSON(validHeartbeatMessage())
			}
		}
	}()
	started := time.Now()
	closeErr := p.waitClosed(5 * time.Second)
	close(stop)
	<-sending

	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want 1001 heartbeat timeout", closeErr.Code, closeErr.Text)
	}
	if elapsed := time.Since(started); elapsed < testOfflineTimeout/2 {
		t.Fatalf("%v 만에 닫힘: 저장소 장애가 즉시 연결을 끊음", elapsed)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "DEPENDENCY_UNAVAILABLE") {
		t.Fatal("저장소 장애가 log에 남지 않음")
	}
	if strings.Contains(logs, echoCheckMarker) || strings.Contains(logs, "connection refused") {
		t.Fatalf("저장소 오류 원문이 log에 노출됨: %s", logs)
	}
}

// Credential revoke lifecycle hook: 그 Credential의 current 연결이 4001로 끝나고 registry에서 제거된다.
func TestRevokeCredentialHookClosesActiveConnectionWith4001(t *testing.T) {
	h := newHarness(t)
	p := h.establish()

	if n := h.registry.RevokeCredential(h.principal.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}

	closeErr := p.waitClosed(5 * time.Second)
	if closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}
	if _, ok := h.registry.Current(h.principal.ConnectorID); ok {
		t.Fatal("revoke 뒤에도 registry에 남음")
	}
	if got := h.beats.recorded(); len(got) != 0 {
		t.Fatalf("revoke된 connection에서 heartbeat가 기록됨: %+v", got)
	}
}

func TestRevokeConnectorHookClosesActiveConnectionWith4001(t *testing.T) {
	h := newHarness(t)
	p := h.establish()

	if !h.registry.RevokeConnector(h.principal.ConnectorID) {
		t.Fatal("RevokeConnector() = false, want true")
	}
	if closeErr := p.waitClosed(5 * time.Second); closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}
	registryReleased(t, h)
}

// DB에서 out-of-band로 revoke된 경우: 다음 유효한 HEARTBEAT에서 active 확인이 실패해 last_seen을 갱신하지 않고 4001로 끝난다.
func TestRevokedCredentialIsDetectedOnNextHeartbeat(t *testing.T) {
	h := newHarness(t)
	p := h.establish()

	// revoke 전 heartbeat는 기록된다.
	p.heartbeat()
	waitFor(t, "revoke 전 heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })

	h.beats.revoke(h.principal.CredentialID)
	p.heartbeat()

	closeErr := p.waitClosed(5 * time.Second)
	if closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}
	registryReleased(t, h)
	if got := h.beats.recorded(); len(got) != 1 {
		t.Fatalf("revoke 뒤 heartbeat가 기록됨: %+v", got)
	}
	if strings.Contains(h.logs.String(), testCredential) {
		t.Fatal("Credential 원문이 log에 노출됨")
	}
}

// 같은 Connector의 새 connection이 HELLO를 마치면 새 connection이 current가 되고 이전 connection은 4002로 끝난다.
func TestNewConnectionReplacesOldWith4002(t *testing.T) {
	h := newHarness(t)
	first := h.establish()
	firstSession := h.waitRegistered()

	second := h.establishWith(testCredential)
	closeErr := first.waitClosed(5 * time.Second)
	if closeErr.Code != closeReplaced {
		t.Fatalf("이전 connection close code = %d, want %d", closeErr.Code, closeReplaced)
	}

	secondSession, ok := h.registry.Current(h.principal.ConnectorID)
	if !ok || secondSession == firstSession {
		t.Fatalf("Current() = %+v, %v, want 새 Session (이전 %+v)", secondSession, ok, firstSession)
	}
	// 이전 connection의 serve가 끝나 늦은 release가 일어나도 새 Session은 유지된다.
	time.Sleep(100 * time.Millisecond)
	if got, ok := h.registry.Current(h.principal.ConnectorID); !ok || got != secondSession {
		t.Fatalf("이전 connection 종료 뒤 Current() = %+v, %v, want %+v", got, ok, secondSession)
	}
	if second.closedNow() {
		t.Fatal("새 connection이 함께 닫힘")
	}

	// 새 connection은 정상적으로 heartbeat를 보낼 수 있다.
	second.heartbeat()
	waitFor(t, "새 connection heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })
}

// 교체 직후 이전 connection이 보낸 HEARTBEAT는 기록되지 않고 current 상태를 바꾸지 못한다.
func TestStaleConnectionHeartbeatAfterReplacementIsIgnored(t *testing.T) {
	h := newHarness(t)
	first := h.establish()
	h.establishWith(testCredential)
	current, _ := h.registry.Current(h.principal.ConnectorID)

	// 이전 connection은 4002 종료 요청을 받은 상태에서도 write는 할 수 있다. 여러 번 보내 처리 순서와 무관하게 확인한다.
	for range 5 {
		_ = first.conn.WriteJSON(validHeartbeatMessage())
	}
	first.waitClosed(5 * time.Second)

	if got := h.beats.recorded(); len(got) != 0 {
		t.Fatalf("교체된 connection의 heartbeat가 기록됨: %+v", got)
	}
	if got, ok := h.registry.Current(h.principal.ConnectorID); !ok || got != current {
		t.Fatalf("stale heartbeat 뒤 Current() = %+v, %v, want %+v", got, ok, current)
	}
}

// 교체된 이전 Session의 Credential이 revoke된 것으로 기록되어도, 이전 connection의 HEARTBEAT는 새 current connection을 닫지 못한다.
func TestStaleRevokeDetectionDoesNotCloseNewConnection(t *testing.T) {
	h := newHarness(t)
	first := h.establish()
	second := h.establishWith(testCredential)
	first.waitClosed(5 * time.Second)

	h.beats.revoke(h.principal.CredentialID)
	_ = first.conn.WriteJSON(validHeartbeatMessage())
	time.Sleep(100 * time.Millisecond)

	if second.closedNow() {
		t.Fatal("이전 connection의 heartbeat가 새 connection을 닫음")
	}
	if _, ok := h.registry.Current(h.principal.ConnectorID); !ok {
		t.Fatal("이전 connection의 heartbeat가 새 Session을 registry에서 제거함")
	}
}

// 같은 Credential로 동시에 여러 connection을 열어도 최종 current는 하나이고 나머지는 모두 4002로 끝난다.
func TestConcurrentConnectionsLeaveExactlyOneCurrent(t *testing.T) {
	h := newHarness(t)
	const connections = 6

	peers := make([]*peer, connections)
	var wg sync.WaitGroup
	for i := range connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			peers[i] = h.establishWith(testCredential)
		}()
	}
	wg.Wait()

	var replaced, alive int
	for _, p := range peers {
		select {
		case <-p.done:
			if closeErr := p.waitClosed(time.Second); closeErr.Code != closeReplaced {
				t.Fatalf("교체된 connection close code = %d, want %d", closeErr.Code, closeReplaced)
			}
			replaced++
		case <-time.After(1500 * time.Millisecond):
			alive++
		}
	}
	if alive != 1 || replaced != connections-1 {
		t.Fatalf("살아 있는 connection = %d, 교체 종료 = %d, want 1 and %d", alive, replaced, connections-1)
	}
	if _, ok := h.registry.Current(h.principal.ConnectorID); !ok {
		t.Fatal("최종 current가 없음")
	}
}

// 서로 다른 Connector의 connection은 서로 교체하거나 종료하지 않는다.
func TestDifferentConnectorsDoNotReplaceEachOther(t *testing.T) {
	h := newHarness(t)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	const otherCredential = "test-ws-other-credential-unique-91ac"
	h.auth.add(otherCredential, other)

	a := h.establish()
	b := h.establishWith(otherCredential)
	waitFor(t, "두 Connector 등록", func() bool {
		_, okA := h.registry.Current(h.principal.ConnectorID)
		_, okB := h.registry.Current(other.ConnectorID)
		return okA && okB
	})

	time.Sleep(100 * time.Millisecond)
	if a.closedNow() || b.closedNow() {
		t.Fatal("다른 Connector의 등록이 기존 connection을 닫음")
	}
}

// Shutdown은 새 Upgrade를 거절하고 모든 connection을 1001로 닫으며 registry와 goroutine이 남지 않을 때까지 기다린다.
func TestShutdownDrainsAllConnections(t *testing.T) {
	h := newHarness(t)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	const otherCredential = "test-ws-other-credential-unique-91ac"
	h.auth.add(otherCredential, other)
	a := h.establish()
	b := h.establishWith(otherCredential)
	waitFor(t, "두 Connector 등록", func() bool {
		_, okB := h.registry.Current(other.ConnectorID)
		return okB
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := h.handler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := h.handler.Shutdown(ctx); err != nil {
		t.Fatalf("두 번째 Shutdown() error = %v", err)
	}
	h.handler.Close() // Shutdown 뒤 Close도 안전하다.

	for name, p := range map[string]*peer{"A": a, "B": b} {
		if closeErr := p.waitClosed(time.Second); closeErr.Code != websocket.CloseGoingAway {
			t.Fatalf("Connector %s close code = %d, want %d", name, closeErr.Code, websocket.CloseGoingAway)
		}
	}
	for _, id := range []uuid.UUID{h.principal.ConnectorID, other.ConnectorID} {
		if _, ok := h.registry.Current(id); ok {
			t.Fatalf("Shutdown 뒤에도 Connector %s가 registry에 남음", id)
		}
	}
	_, resp, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Shutdown 뒤 Dial() error = %v, resp = %v, want 503", err, resp)
	}
}

// 상대가 close에 응답하지 않아도 closeGrace 뒤 connection을 닫아 Shutdown이 끝난다.
func TestShutdownFinishesWhenPeerDoesNotAnswerClose(t *testing.T) {
	h := newHarness(t)
	conn := h.connect()
	sendJSON(t, conn, validHello())
	readJSON(t, conn)
	h.waitRegistered()
	// 이후 read를 하지 않으므로 close frame에 응답하지 않는다.

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	if err := h.handler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > closeGrace+3*time.Second {
		t.Fatalf("Shutdown이 %v 걸림: closeGrace(%v) 뒤에 끝나야 함", elapsed, closeGrace)
	}
	registryReleased(t, h)
}

// ctx가 먼저 끝나면 Shutdown은 ctx 오류를 반환하고, 이후에도 connection은 정리된다.
func TestShutdownReturnsContextErrorWhenNotDrainedInTime(t *testing.T) {
	h := newHarness(t)
	conn := h.connect()
	sendJSON(t, conn, validHello())
	readJSON(t, conn)
	h.waitRegistered()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := h.handler.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want context.DeadlineExceeded", err)
	}

	// grace 뒤에는 결국 정리된다.
	registryReleased(t, h)
	drained, cancelDrained := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelDrained()
	if err := h.handler.Shutdown(drained); err != nil {
		t.Fatalf("이후 Shutdown() error = %v", err)
	}
}

// shutdown, 교체, revoke hook, offline이 동시에 일어나도 panic이나 교착 없이 모든 connection이 정리된다. -race로 실행한다.
func TestShutdownRacesWithReplacementRevokeAndOffline(t *testing.T) {
	for range 5 {
		h := newHarness(t, func(o *Options) {
			o.HeartbeatInterval = 10 * time.Millisecond
			o.OfflineTimeout = 60 * time.Millisecond
		})

		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, _, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
				if err != nil {
					return // shutdown이 먼저 이겨 503을 받을 수 있다.
				}
				defer conn.Close()
				_ = conn.WriteJSON(validHello())
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}()
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20 {
				h.registry.RevokeConnector(h.principal.ConnectorID)
				time.Sleep(time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			time.Sleep(20 * time.Millisecond)
			h.handler.Close()
		}()
		wg.Wait()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		if err := h.handler.Shutdown(ctx); err != nil {
			cancel()
			t.Fatalf("Shutdown() error = %v", err)
		}
		cancel()
		if _, ok := h.registry.Current(h.principal.ConnectorID); ok {
			t.Fatal("정리 뒤에도 registry에 남음")
		}
	}
}

// connection별 writer 직렬화: 여러 goroutine이 동시에 write해도 gorilla의 single-writer 요구를 깨지 않고 모든 message가 온전히 전달된다.
// close는 처음 요청한 이유만 전달한다.
func TestControlConnSerializesConcurrentWritesAndClosesOnce(t *testing.T) {
	const writers, perWriter = 16, 25

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

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				if err := cc.writeJSON(map[string]any{"writer": w, "seq": i}); err != nil {
					t.Errorf("writeJSON() error = %v", err)
					return
				}
			}
		}()
	}

	received := 0
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	for received < writers*perWriter {
		var msg map[string]any
		if err := client.ReadJSON(&msg); err != nil {
			t.Fatalf("%d번째 message read 오류 = %v", received, err)
		}
		received++
	}
	wg.Wait()

	// 처음 요청한 이유만 적용된다. 이후 요청과 write는 실패하거나 무시되며 panic하지 않는다.
	cc.close(closeReplaced, "first")
	cc.close(closeCredentialRevoked, "second")
	cc.closeFor(connector.CloseRevoked)
	if err := cc.writeJSON(map[string]any{"after": "close"}); err == nil {
		t.Fatal("close 뒤 writeJSON()이 성공함")
	}
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != closeReplaced || closeErr.Text != "first" {
		t.Fatalf("client가 받은 종료 = %v, want %d first", err, closeReplaced)
	}
}
