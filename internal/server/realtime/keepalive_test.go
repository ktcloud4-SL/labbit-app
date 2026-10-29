package realtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

func TestTerminalKeepaliveOnBothAttachedLegs(t *testing.T) {
	e := newEnv(t)
	realtime.SetTestPingInterval(e.relay, 25*time.Millisecond)
	s := e.newSession()
	d, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatal(err)
	}
	dp := make(chan struct{}, 16)
	bp := make(chan struct{}, 16)
	observe := func(p *peer, ch chan struct{}) {
		p.conn.SetPingHandler(func(data string) error {
			select {
			case ch <- struct{}{}:
			default:
			}
			return p.conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})
		p.start.Do(func() { go p.readLoop() })
	}
	observe(d, dp)
	observe(b, bp)
	select {
	case <-dp:
		t.Fatal("data ping before attach")
	case <-bp:
		t.Fatal("browser ping before attach")
	case <-time.After(100 * time.Millisecond):
	}
	d.writeText(dataAttachMessage(t, s, nil))
	if got := d.readJSON()["type"]; got != "TERMINAL_DATA_ATTACHED" {
		t.Fatalf("data attach: %v", got)
	}
	if err := e.relay.Activate(s.ID, e.clock.Now().Add(realtime.DefaultGrace)); err != nil {
		t.Fatal(err)
	}
	b.writeText(browserAttachMessage(t, s, nil, nil))
	if got := b.readJSON()["type"]; got != "TERMINAL_ATTACHED" {
		t.Fatalf("browser attach: %v", got)
	}
	for name, ch := range map[string]<-chan struct{}{"browser": bp, "data": dp} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("no %s ping while idle", name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.relay.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*peer{"browser": b, "data": d} {
		select {
		case <-p.msgs: // May contain a final application message; wait for the reader to exit below.
		case <-time.After(time.Second):
			t.Fatalf("%s did not drain", name)
		}
		for range p.msgs {
		}
	}
	// After both readers exit, no more Ping handlers can run.
	for _, ch := range []chan struct{}{bp, dp} {
		for len(ch) > 0 {
			<-ch
		}
	}
	select {
	case <-bp:
		t.Fatal("browser ping after drain")
	case <-dp:
		t.Fatal("data ping after drain")
	case <-time.After(100 * time.Millisecond):
	}
}
