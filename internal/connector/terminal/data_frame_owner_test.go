package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

type failingInputPTY struct {
	*MockPTY
	mode             string
	started, release chan struct{}
	calls            atomic.Int32
}

func (p *failingInputPTY) Write(data []byte) (int, error) {
	if p.mode == "input" || (p.mode == "late-input" && p.calls.Add(1) == 1) {
		if p.started != nil {
			close(p.started)
			<-p.release
		}
		return 0, errors.New("private SSH failure detail")
	}
	return p.MockPTY.Write(data)
}

func (p *failingInputPTY) Resize(cols, rows int) error {
	if p.mode == "resize" {
		return errors.New("private SSH failure detail")
	}
	return p.MockPTY.Resize(cols, rows)
}

func frameTestSession(t *testing.T, manager *SessionManager, id string, pty PTYChannel) *Session {
	t.Helper()
	session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}, protocol.BaseEnvelope{TerminalSessionID: id, LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return pty, nil })
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func frameTestClient(t *testing.T, session *Session) (*DataWSSClient, *mock.TerminalRelay) {
	t.Helper()
	relay := mock.NewTerminalRelay()
	t.Cleanup(relay.Close)
	client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: relay.URL(), AllowInsecure: true, DialTimeout: time.Second}, session)
	t.Cleanup(client.Close)
	if err := client.DialAndAttach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.WaitForAttach(time.Second); err != nil {
		t.Fatal(err)
	}
	return client, relay
}

func frameTestJSON(t *testing.T, session *Session, kind string, payload any) []byte {
	t.Helper()
	env := struct {
		protocol.BaseEnvelope
		Payload any `json:"payload"`
	}{BaseEnvelope: protocol.BaseEnvelope{Type: kind, MessageID: generateUUID(), SentAt: time.Now().UTC(), TerminalSessionID: session.SessionID, LabInstanceID: session.LabInstanceID, Generation: session.Generation}, Payload: payload}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Model a frame already read from the previous socket, whose dispatch resumes
// only after a replacement has attached. No timing-dependent read hook is used.
func TestReplacedDataReaderCannotDispatchBufferedFrames(t *testing.T) {
	session := ownerTestSession(t)
	old, _ := frameTestClient(t, session)
	oldConn := session.GetDataConn()
	current, relay := frameTestClient(t, session)
	frames := []struct {
		kind int
		data []byte
	}{
		{websocket.BinaryMessage, []byte("stale-input")},
		{websocket.TextMessage, frameTestJSON(t, session, protocol.MessageTypeTerminalDataResize, protocol.TerminalDataResizePayload{Cols: 120, Rows: 40})},
		{websocket.TextMessage, frameTestJSON(t, session, protocol.MessageTypeTerminalDataClose, protocol.TerminalDataClosePayload{Reason: protocol.TerminalReasonSessionClosed})},
		{websocket.TextMessage, frameTestJSON(t, session, protocol.MessageTypeError, protocol.TerminalDataErrorPayload{Code: "STALE_ERROR", Message: "stale", Fatal: true})},
	}
	for _, frame := range frames {
		if old.handleDataFrame(oldConn, frame.kind, frame.data) {
			t.Fatal("old reader admitted a buffered frame after replacement")
		}
	}
	session.mu.Lock()
	cols, rows := session.Cols, session.Rows
	session.mu.Unlock()
	if cols != 80 || rows != 24 || session.GetStatus() != StatusActive {
		t.Fatal("stale frame changed preserved PTY state")
	}
	if !current.handleDataFrame(session.GetDataConn(), websocket.BinaryMessage, []byte("current-input")) {
		t.Fatal("current reader rejected input")
	}
	output, err := relay.ReadBinary(time.Second)
	if err != nil || string(output) != "current-input" {
		t.Fatal("replacement cannot use preserved PTY or stale input escaped")
	}
}

func TestOldInputFailureCannotEndReplacement(t *testing.T) {
	manager := NewSessionManager(time.Second, nil)
	t.Cleanup(func() { manager.CloseAll(protocol.TerminalReasonServiceRestart) })
	pty := &failingInputPTY{MockPTY: NewMockEchoPTY(80, 24), mode: "late-input", started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce atomic.Bool
	release := func() {
		if releaseOnce.CompareAndSwap(false, true) {
			close(pty.release)
		}
	}
	t.Cleanup(release)
	session := frameTestSession(t, manager, "late-input", pty)
	old, _ := frameTestClient(t, session)
	oldConn := session.GetDataConn()
	done := make(chan bool, 1)
	go func() { done <- old.handleDataFrame(oldConn, websocket.BinaryMessage, []byte("old-input")) }()
	select {
	case <-pty.started:
	case <-time.After(time.Second):
		t.Fatal("input did not start")
	}
	current, relay := frameTestClient(t, session)
	release()
	select {
	case keep := <-done:
		if keep {
			t.Fatal("failed old reader continued")
		}
	case <-time.After(time.Second):
		t.Fatal("old input was not released")
	}
	if session.GetStatus() != StatusActive || manager.ActiveCount() != 1 {
		t.Fatal("old failure ended replacement")
	}
	if !current.handleDataFrame(session.GetDataConn(), websocket.BinaryMessage, []byte("replacement-input")) {
		t.Fatal("replacement input failed")
	}
	output, err := relay.ReadBinary(time.Second)
	if err != nil || string(output) != "replacement-input" {
		t.Fatal("replacement PTY unusable after old failure")
	}
}

func TestDataInputAndResizeFailuresCloseOnlyTheirSession(t *testing.T) {
	for _, mode := range []string{"input", "resize"} {
		t.Run(mode, func(t *testing.T) {
			manager := NewSessionManager(time.Second, nil)
			t.Cleanup(func() { manager.CloseAll(protocol.TerminalReasonServiceRestart) })
			failedPTY := &failingInputPTY{MockPTY: NewMockEchoPTY(80, 24), mode: mode}
			failed := frameTestSession(t, manager, "failed-"+mode, failedPTY)
			client, relay := frameTestClient(t, failed)
			sibling := frameTestSession(t, manager, "sibling-"+mode, NewMockEchoPTY(80, 24))
			siblingClient, siblingRelay := frameTestClient(t, sibling)
			kind, data := websocket.BinaryMessage, []byte("fail-input")
			if mode == "resize" {
				kind = websocket.TextMessage
				data = frameTestJSON(t, failed, protocol.MessageTypeTerminalDataResize, protocol.TerminalDataResizePayload{Cols: 100, Rows: 30})
			}
			if client.handleDataFrame(failed.GetDataConn(), kind, data) {
				t.Fatal("failed reader continued")
			}
			frame, err := relay.ReadText(time.Second)
			if err != nil {
				t.Fatal("Data ENDED not observed")
			}
			var ended protocol.TerminalDataEndedMessage
			if err := json.Unmarshal(frame, &ended); err != nil {
				t.Fatal(err)
			}
			if ended.Type != protocol.MessageTypeTerminalDataEnded || ended.Payload.Reason != protocol.TerminalReasonSSHDisconnected || ended.Payload.Error == nil || ended.Payload.Error.Message != "Terminal SSH channel ended" {
				t.Fatal("failure was not safely classified")
			}
			if manager.ActiveCount() != 1 || failed.GetStatus() != StatusClosed {
				t.Fatal("failed session resources remain")
			}
			failedPTY.mu.Lock()
			ptyClosed := failedPTY.closed
			failedPTY.mu.Unlock()
			if !ptyClosed {
				t.Fatal("failed session did not close its PTY")
			}
			if !siblingClient.handleDataFrame(sibling.GetDataConn(), websocket.BinaryMessage, []byte("sibling-alive")) {
				t.Fatal("sibling input failed")
			}
			output, err := siblingRelay.ReadBinary(time.Second)
			if err != nil || string(output) != "sibling-alive" {
				t.Fatal("failure affected sibling PTY")
			}
		})
	}
}
