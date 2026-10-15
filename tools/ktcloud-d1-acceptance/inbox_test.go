package main

import (
	"context"
	"encoding/json"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"testing"
	"time"
)

func TestAwaitRetainsEndedBeforeOperationAck(t *testing.T) {
	oldMessages, oldUnmatched := messages, unmatched
	defer func() { messages, unmatched = oldMessages, oldUnmatched }()
	messages, unmatched = make(chan []byte, 2), nil
	ended, _ := json.Marshal(protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalEnded, TerminalSessionID: "active"})
	ack, _ := json.Marshal(protocol.BaseEnvelope{Type: protocol.MessageTypeOperationAck, ReplyToMessageID: "reset"})
	messages <- ended
	messages <- ack
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := await(ctx, protocol.MessageTypeOperationAck, "reset"); err != nil {
		t.Fatal(err)
	}
	b, err := awaitMatch(ctx, func(e protocol.BaseEnvelope) bool {
		return e.Type == protocol.MessageTypeTerminalEnded && e.TerminalSessionID == "active"
	})
	if err != nil || string(b) != string(ended) || len(unmatched) != 0 {
		t.Fatal("lifecycle frame lost while waiting for ACK")
	}
}
