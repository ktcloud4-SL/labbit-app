package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Synthetic Gateway only. Each Control attempt has an exact authenticated binding.
// HTTP and SSH content remains bounded in memory and is never exported.
type previewTransportPlan struct {
	open      protocol.PreviewOpenMessage
	mu        sync.Mutex
	conn      *websocket.Conn
	attached  chan struct{}
	done      chan struct{}
	events    chan dataEvent
	readErr   error // published by closing done/events
	attaches  atomic.Int32
	forbidden bool // fixed before publishing the plan
	overflow  atomic.Bool
}

func (p *previewTransportPlan) write(kind int, raw []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return errors.New("Preview Data unavailable")
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.conn.WriteMessage(kind, raw)
}

func (p *previewTransportPlan) disconnect(code int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "fixture"), time.Now().Add(time.Second))
		_ = p.conn.Close()
	}
}

func (p *previewTransportPlan) closed(ctx context.Context, normal bool) error {
	select {
	case <-p.done:
		if p.overflow.Load() {
			return errors.New("Preview Data observation overflow")
		}
		if normal && !websocket.IsCloseError(p.readErr, websocket.CloseNormalClosure) {
			return errors.New("Preview Data did not close normally")
		}
		return nil
	case <-ctx.Done():
		return errors.New("Preview Data close not observed")
	}
}

type previewTransportPeer struct {
	token       string
	mu          sync.Mutex
	plans       map[string]*previewTransportPlan
	connections map[*websocket.Conn]struct{}
	active      map[string]int
	maxActive   int
	invalid     atomic.Bool
	stopped     bool
	wg          sync.WaitGroup
}

func newPreviewTransportPeer(token string) *previewTransportPeer {
	return &previewTransportPeer{token: token, plans: map[string]*previewTransportPlan{}, connections: map[*websocket.Conn]struct{}{}, active: map[string]int{}}
}

func (g *previewTransportPeer) plan(open protocol.PreviewOpenMessage, forbidden bool) *previewTransportPlan {
	p := &previewTransportPlan{open: open, attached: make(chan struct{}), done: make(chan struct{}), events: make(chan dataEvent, 32)}
	p.forbidden = forbidden
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.plans[open.MessageID]; exists {
		g.invalid.Store(true)
	}
	g.plans[open.MessageID] = p
	return p
}

func (g *previewTransportPeer) handler(control http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/connector/v1/control", control)
	mux.HandleFunc("/connector/v1/preview-data", g.serve)
	return mux
}

func previewAttachMatches(a protocol.PreviewAttachMessage, o protocol.PreviewOpenMessage) bool {
	return a.Type == protocol.MessageTypePreviewAttach && a.MessageID != "" && !a.SentAt.IsZero() &&
		a.ReplyToMessageID == o.MessageID && a.PreviewSessionID == o.PreviewSessionID && a.LabInstanceID == o.LabInstanceID && a.Generation == o.Generation &&
		a.Payload.RuntimeID != "" && a.Payload.TargetVmKey == o.Payload.TargetVmKey && a.Payload.ProviderServerID == o.Payload.ProviderServerID && a.Payload.TargetPort == o.Payload.TargetPort
}

func (g *previewTransportPeer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+g.token || r.URL.RawQuery != "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if protocols := websocket.Subprotocols(r); len(protocols) != 1 || protocols[0] != protocol.SubprotocolPreviewData {
		http.Error(w, "invalid subprotocol", http.StatusBadRequest)
		return
	}
	up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		_ = c.Close()
		return
	}
	g.connections[c] = struct{}{}
	g.wg.Add(1)
	g.mu.Unlock()
	defer func() { _ = c.Close(); g.mu.Lock(); delete(g.connections, c); g.mu.Unlock(); g.wg.Done() }()
	c.SetReadLimit(64 * 1024)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, raw, err := c.ReadMessage()
	var attach protocol.PreviewAttachMessage
	if err != nil || kind != websocket.TextMessage || json.Unmarshal(raw, &attach) != nil {
		g.invalid.Store(true)
		return
	}
	g.mu.Lock()
	p := g.plans[attach.ReplyToMessageID]
	if p == nil || p.forbidden || !previewAttachMatches(attach, p.open) || p.attaches.Load() != 0 || g.active[attach.PreviewSessionID] != 0 {
		g.invalid.Store(true)
		g.mu.Unlock()
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1008, "binding rejected"), time.Now().Add(time.Second))
		return
	}
	p.attaches.Add(1)
	g.active[attach.PreviewSessionID]++
	if g.active[attach.PreviewSessionID] > g.maxActive {
		g.maxActive = g.active[attach.PreviewSessionID]
	}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.active[attach.PreviewSessionID]--
		g.mu.Unlock()
		close(p.events)
		close(p.done)
	}()
	p.mu.Lock()
	p.conn = c
	err = c.WriteJSON(protocol.PreviewAttachedMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewAttached, MessageID: attach.MessageID + "-ack", SentAt: time.Now().UTC(), ReplyToMessageID: attach.MessageID, PreviewSessionID: attach.PreviewSessionID, LabInstanceID: attach.LabInstanceID, Generation: attach.Generation}, Payload: protocol.PreviewAttachedPayload{}})
	p.mu.Unlock()
	if err != nil {
		p.readErr = err
		return
	}
	close(p.attached)
	_ = c.SetReadDeadline(time.Now().Add(12 * time.Minute))
	for {
		kind, raw, err = c.ReadMessage()
		if err != nil {
			p.readErr = err
			return
		}
		if kind != websocket.BinaryMessage {
			g.invalid.Store(true)
			return
		}
		select {
		case p.events <- dataEvent{kind: kind, raw: raw}:
		default:
			p.overflow.Store(true)
			return
		}
	}
}

func (g *previewTransportPeer) close() {
	g.mu.Lock()
	g.stopped = true
	for c := range g.connections {
		_ = c.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *previewTransportPeer) drained() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.connections) == 0 && !g.invalid.Load() && g.maxActive == 1
}
