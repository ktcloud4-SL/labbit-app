package wss_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestHandler_TerminalOpen_OverlappingAttachFailurePreservesReplacement(t *testing.T) {
	for _, phase := range []string{"waiting_for_attached", "late_credential_failure"} {
		t.Run(phase, func(t *testing.T) {
			firstRelay := mock.NewTerminalRelay()
			defer firstRelay.Close()
			secondRelay := mock.NewTerminalRelay()
			defer secondRelay.Close()
			ready, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()

			results := make(chan protocol.TerminalOpenResultMessage, 4)
			ended := make(chan protocol.TerminalEndedMessage, 4)
			sender := wss.SendMessageFunc(func(_ context.Context, message interface{}) error {
				switch message := message.(type) {
				case protocol.TerminalOpenResultMessage:
					results <- message
				case protocol.TerminalEndedMessage:
					ended <- message
				}
				return nil
			})
			manager := terminal.NewSessionManager(5*time.Second, nil)
			defer manager.CloseAll(protocol.TerminalReasonServiceRestart)
			var allocations atomic.Int32
			factory := func(_, _ string, cols, rows int) (terminal.PTYChannel, error) {
				allocations.Add(1)
				return terminal.NewMockEchoPTY(cols, rows), nil
			}
			firstConfig := terminal.DataWSSClientConfig{
				EndpointURL: firstRelay.URL(), AllowInsecure: true, DialTimeout: 5 * time.Second,
			}
			if phase == "waiting_for_attached" {
				firstRelay.SetBeforeAttached(func(protocol.TerminalDataAttachMessage) {
					close(ready)
					<-release
				})
			} else {
				// A credential lookup can finish after its client lost ownership.
				// Gate that failure until the replacement has successfully attached.
				firstConfig.CredentialProvider = func() (string, error) {
					close(ready)
					<-release
					return "", errors.New("fixture credential lookup failed")
				}
			}
			first := wss.NewHandler(&provider.MockProvider{}, sender)
			first.SetTerminalManager(manager, factory, firstConfig)
			// Control reconnect can create another Handler sharing the manager.
			second := wss.NewHandler(&provider.MockProvider{}, sender)
			second.SetTerminalManager(manager, factory, terminal.DataWSSClientConfig{
				EndpointURL: secondRelay.URL(), AllowInsecure: true, DialTimeout: 5 * time.Second,
			})
			open := protocol.TerminalOpenMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type: protocol.MessageTypeTerminalOpen, MessageID: "first-open",
					SentAt: time.Now().UTC(), TerminalSessionID: "overlapping-terminal",
					LabInstanceID: "lab-1", Generation: 1,
				},
				Payload: protocol.TerminalOpenPayload{
					TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24,
				},
			}
			firstRaw, err := json.Marshal(open)
			if err != nil {
				t.Fatal(err)
			}
			firstDone := make(chan error, 1)
			go func() { firstDone <- first.HandleMessage(context.Background(), firstRaw) }()
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("first OPEN did not reach the gated attach")
			}
			original, exists := manager.GetSession(open.TerminalSessionID)
			if !exists {
				t.Fatal("first OPEN did not register its session")
			}
			open.MessageID = "second-open"
			secondRaw, err := json.Marshal(open)
			if err != nil {
				t.Fatal(err)
			}
			if err := second.HandleMessage(context.Background(), secondRaw); err != nil {
				t.Fatalf("replacement OPEN failed: %v", err)
			}
			unblock()
			select {
			case err := <-firstDone:
				if err == nil {
					t.Fatal("superseded OPEN unexpectedly succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("superseded attach did not stop")
			}
			outcomes := make(map[string]string)
			for range 2 {
				select {
				case result := <-results:
					outcomes[result.ReplyToMessageID] = result.Payload.Outcome
				case <-time.After(time.Second):
					t.Fatal("missing OPEN result")
				}
			}
			if outcomes["first-open"] != protocol.OutcomeFailed || outcomes["second-open"] != protocol.OutcomeSucceeded {
				t.Fatalf("unexpected OPEN outcomes: %v", outcomes)
			}
			current, exists := manager.GetSession(open.TerminalSessionID)
			if !exists || current != original || current.GetStatus() != terminal.StatusActive {
				t.Fatal("superseded attach failure closed or replaced the current session")
			}
			if allocations.Load() != 1 {
				t.Fatalf("overlapping OPEN allocated %d PTYs, want 1", allocations.Load())
			}
			select {
			case <-ended:
				t.Fatal("superseded attach failure emitted TERMINAL_ENDED")
			default:
			}
			if err := secondRelay.SendBinary([]byte("replacement still alive")); err != nil {
				t.Fatal(err)
			}
			output, err := secondRelay.ReadBinary(time.Second)
			if err != nil || string(output) != "replacement still alive" {
				t.Fatalf("replacement lost PTY byte bridge: output=%q err=%v", output, err)
			}
			if phase == "waiting_for_attached" {
				if _, err := firstRelay.WaitForClose(time.Second); err != nil {
					t.Fatal("superseded attach socket remained open")
				}
			}
		})
	}
}
