package realtime_test

import (
	"sync"
	"testing"
)

func TestControlLogTraceWaitsForFirstEvent(t *testing.T) {
	var logs syncBuffer
	firstRead := make(chan struct{})
	written := make(chan struct{})
	var observed sync.Once
	go func() {
		defer close(written)
		<-firstRead
		_, _ = logs.Write([]byte(`{"message":"delayed event","terminal_session_id":"session-1","trace_id":"expected-trace"}` + "\n"))
	}()
	// Capture the empty first snapshot before allowing the producer to write.
	// An ACK is similarly visible before the server's following log statement.
	requireControlLogTraceFrom(t, func() string {
		snapshot := logs.String()
		observed.Do(func() { close(firstRead) })
		return snapshot
	}, "delayed event", "session-1", "expected-trace")
	<-written
}

func TestControlLogEventRejectsMalformedNonemptyJSON(t *testing.T) {
	for _, raw := range []string{"not JSON\n", "{\n"} {
		if _, err := findControlLogEvent(raw, "event", "session-1"); err == nil {
			t.Fatal("malformed nonempty log was ignored")
		}
	}
	const matching = `{"message":"event","terminal_session_id":"session-1","trace_id":"expected-trace"}`
	for _, prefix := range []string{"", " \n\t\n", `{"message":"unrelated","terminal_session_id":"session-1"}` + "\n"} {
		found, err := findControlLogEvent(prefix+matching+"\n", "event", "session-1")
		if err != nil || found["trace_id"] != "expected-trace" {
			t.Fatal("valid matching event was not found after blank or unrelated logs")
		}
	}
}
