package terminal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func ownerTestSession(t *testing.T) *Session {
	t.Helper()
	manager := NewSessionManager(time.Second, nil)
	t.Cleanup(func() { manager.CloseAll(protocol.TerminalReasonServiceRestart) })
	session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}, protocol.BaseEnvelope{TerminalSessionID: "terminal-1", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return NewMockEchoPTY(80, 24), nil })
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestReplacementDataClientStopsPreviousReconnectOwner(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()
	session := ownerTestSession(t)
	config := DataWSSClientConfig{EndpointURL: relay.URL(), AllowInsecure: true, DialTimeout: time.Second}
	first := NewDataWSSClient(config, session)
	defer first.Close()
	if err := first.ConnectAndStream(); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.WaitForAttach(time.Second); err != nil {
		t.Fatal(err)
	}
	second := NewDataWSSClient(config, session)
	defer second.Close()
	if err := second.ConnectAndStream(); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.WaitForAttach(time.Second); err != nil {
		t.Fatal(err)
	}
	first.mu.Lock()
	closed := first.closed
	first.mu.Unlock()
	if !closed {
		t.Fatal("replaced Data client remains a reconnect owner")
	}
	if err := relay.SendBinary([]byte("replacement-echo")); err != nil {
		t.Fatal(err)
	}
	output, err := relay.ReadBinary(time.Second)
	if err != nil || string(output) != "replacement-echo" {
		t.Fatal("replacement Data client cannot use preserved PTY")
	}
	if _, err := relay.WaitForAttach(700 * time.Millisecond); err == nil {
		t.Fatal("previous reconnect owner reattached")
	}
}

func TestDataClientCloseCancelsPendingAttach(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolTerminalData}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		close(started)
		_, _, _ = conn.ReadMessage()
		close(stopped)
	}))
	defer server.Close()
	session := ownerTestSession(t)
	client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: "ws" + server.URL[4:], AllowInsecure: true, DialTimeout: 5 * time.Second}, session)
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- client.DialAndAttach(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("attach not started")
	}
	client.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled attach succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pending attach survived client close")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("pending attach socket leaked")
	}
}
