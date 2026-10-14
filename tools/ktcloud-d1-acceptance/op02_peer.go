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

// These are synthetic SaaS peers, not a Browser/Backend acceptance claim.
// Payloads stay in bounded memory; only assertion names enter evidence.
type dataFrame struct {
	protocol.BaseEnvelope
	Payload map[string]json.RawMessage `json:"payload"`
}
type dataEvent struct {
	kind int
	raw  []byte
	err  error
}
type dataPlan struct {
	envelope          protocol.BaseEnvelope
	server, operation string
	payload           map[string]any
	body              []byte
	mode              string
	mu                sync.Mutex
	conn              *websocket.Conn
	attaches          int
	attached          chan struct{}
	events            chan dataEvent
	ended             chan struct{}
	dials             atomic.Int32
	invalid           atomic.Bool
}

func (p *dataPlan) write(kind int, raw []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return errors.New("Data connection absent")
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.conn.WriteMessage(kind, raw)
}
func (p *dataPlan) json(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.write(websocket.TextMessage, b)
}
func (p *dataPlan) disconnect(code int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "fixture disconnect"), time.Now().Add(time.Second))
		_ = p.conn.Close()
	}
}
func (p *dataPlan) next(ctx context.Context) (dataEvent, error) {
	select {
	case e := <-p.events:
		return e, e.err
	case <-ctx.Done():
		return dataEvent{}, errors.New("Data response timeout")
	}
}

type op02Peer struct {
	token            string
	terminals, files sync.Map
	blocked          atomic.Bool
	active           atomic.Int32
	mu               sync.Mutex
	connections      map[*websocket.Conn]struct{}
	wg               sync.WaitGroup
	closed           bool
}

func newOP02Peer(token string) *op02Peer {
	return &op02Peer{token: token, connections: map[*websocket.Conn]struct{}{}}
}
func newDataPlan(env protocol.BaseEnvelope, server string) *dataPlan {
	return &dataPlan{envelope: env, server: server, attached: make(chan struct{}, 8), events: make(chan dataEvent, 256), ended: make(chan struct{})}
}
func (p *op02Peer) handler(control http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/connector/v1/control", control)
	mux.HandleFunc("/connector/v1/terminal-data", func(w http.ResponseWriter, r *http.Request) { p.serve(w, r, true) })
	mux.HandleFunc("/connector/v1/file-data", func(w http.ResponseWriter, r *http.Request) { p.serve(w, r, false) })
	return mux
}
func (p *op02Peer) close() {
	p.mu.Lock()
	p.closed = true
	for c := range p.connections {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}
func (p *op02Peer) serve(w http.ResponseWriter, r *http.Request, terminal bool) {
	if r.Header.Get("Authorization") != "Bearer "+p.token || r.URL.RawQuery != "" {
		http.Error(w, "unauthorized", 401)
		return
	}
	if terminal && p.blocked.Load() {
		http.Error(w, "fixture unavailable", 503)
		return
	}
	subprotocol := protocol.SubprotocolFileData
	if terminal {
		subprotocol = protocol.SubprotocolTerminalData
	}
	if len(websocket.Subprotocols(r)) != 1 || websocket.Subprotocols(r)[0] != subprotocol {
		http.Error(w, "invalid subprotocol", 400)
		return
	}
	up := websocket.Upgrader{Subprotocols: []string{subprotocol}}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.connections[c] = struct{}{}
	if p.closed {
		delete(p.connections, c)
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	p.wg.Add(1)
	p.mu.Unlock()
	p.active.Add(1)
	defer func() {
		_ = c.Close()
		p.mu.Lock()
		delete(p.connections, c)
		p.mu.Unlock()
		p.active.Add(-1)
		p.wg.Done()
	}()
	c.SetReadLimit(protocol.MaxJSONMessageSize)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, raw, err := c.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		return
	}
	var attach dataFrame
	if json.Unmarshal(raw, &attach) != nil {
		return
	}
	id, expectedType := attach.FileRequestID, protocol.MessageTypeFileDataAttach
	store := &p.files
	if terminal {
		id, expectedType, store = attach.TerminalSessionID, protocol.MessageTypeTerminalDataAttach, &p.terminals
	}
	value, exists := store.Load(id)
	if !exists {
		return
	}
	plan := value.(*dataPlan)
	if attach.Type != expectedType || attach.MessageID == "" || attach.SentAt.IsZero() || attach.LabInstanceID != plan.envelope.LabInstanceID || attach.Generation != plan.envelope.Generation {
		return
	}
	var runtimeID string
	if json.Unmarshal(attach.Payload["runtimeId"], &runtimeID) != nil || runtimeID == "" {
		return
	}
	if !terminal {
		var vm, server string
		if json.Unmarshal(attach.Payload["targetVmKey"], &vm) != nil || json.Unmarshal(attach.Payload["providerServerId"], &server) != nil || vm != "workspace" || server != plan.server {
			return
		}
	}
	plan.mu.Lock()
	if !terminal && plan.attaches != 0 {
		plan.invalid.Store(true)
		plan.dials.Add(1)
		plan.mu.Unlock()
		return
	}
	plan.conn = c
	plan.attaches++
	resumed := plan.attaches > 1
	plan.mu.Unlock()
	plan.dials.Add(1)
	env := plan.envelope
	env.Type = expectedType // replaced below with the contracted attached name
	env.MessageID = id + "-attached"
	env.SentAt = time.Now().UTC()
	env.ReplyToMessageID = attach.MessageID
	payload := map[string]any{}
	if terminal {
		env.Type = protocol.MessageTypeTerminalDataAttached
		payload = map[string]any{"resumed": resumed, "historyAvailable": false}
	} else {
		env.Type = protocol.MessageTypeFileDataAttached
	}
	if plan.json(struct {
		protocol.BaseEnvelope
		Payload map[string]any `json:"payload"`
	}{env, payload}) != nil {
		return
	}
	select {
	case plan.attached <- struct{}{}:
	default:
	}
	_ = c.SetReadDeadline(time.Time{})
	if !terminal {
		env.Type = "FILE_" + plan.operation
		env.MessageID = id + "-request"
		env.ReplyToMessageID = ""
		if plan.json(struct {
			protocol.BaseEnvelope
			Payload map[string]any `json:"payload"`
		}{env, plan.payload}) != nil {
			return
		}
		if plan.operation == "SAVE" && plan.write(websocket.BinaryMessage, plan.body) != nil {
			return
		}
		defer close(plan.ended)
		c.SetReadLimit((1 << 20) + protocol.MaxJSONMessageSize)
	}
	for {
		kind, raw, err := c.ReadMessage()
		if !terminal && plan.mode == "lost-save-result" && err == nil && kind == websocket.TextMessage {
			var result dataFrame
			var outcome string
			if json.Unmarshal(raw, &result) != nil || result.Type != protocol.MessageTypeFileSaveResult || result.FileRequestID != id || result.ReplyToMessageID != id+"-request" || !controlBinding(result.BaseEnvelope, plan.envelope) || json.Unmarshal(result.Payload["outcome"], &outcome) != nil || outcome != protocol.OutcomeSucceeded {
				plan.invalid.Store(true)
			}
			// Discard the result at the synthetic SaaS application boundary.
			// Keep only actual file-state and no-redial assertions as evidence.
			plan.disconnect(websocket.CloseGoingAway)
			return
		}
		// A full queue fails this fixture rather than silently losing output.
		select {
		case plan.events <- dataEvent{kind, raw, err}:
		default:
			return
		}
		if err != nil {
			return
		}
		if !terminal && kind == websocket.TextMessage {
			var result dataFrame
			if json.Unmarshal(raw, &result) != nil || result.Type != "FILE_"+plan.operation+"_RESULT" || result.ReplyToMessageID != id+"-request" || result.FileRequestID != id || result.LabInstanceID != env.LabInstanceID || result.Generation != env.Generation {
				plan.invalid.Store(true)
				return
			}
			var outcome string
			_ = json.Unmarshal(result.Payload["outcome"], &outcome)
			if plan.operation != "READ" || outcome != protocol.OutcomeSucceeded {
				finishFile(plan, c)
				return
			}
		} else if !terminal && kind == websocket.BinaryMessage {
			finishFile(plan, c)
			return
		}
	}
}

// SaaS owns normal close. Wait for the peer's close reply and reject extra
// frames instead of declaring completion immediately after sending 1000.
func finishFile(plan *dataPlan, c *websocket.Conn) {
	plan.mu.Lock()
	err := c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "complete"), time.Now().Add(time.Second))
	plan.mu.Unlock()
	if err != nil {
		plan.invalid.Store(true)
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = c.ReadMessage()
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.CloseNormalClosure {
		plan.invalid.Store(true)
	}
}
