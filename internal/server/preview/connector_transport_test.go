package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectorpreview "github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

type sequentialForwarder struct {
	forwarder *connectorpreview.DirectTCPForwarder
	active    atomic.Int32
	maximum   atomic.Int32
}

func (f *sequentialForwarder) DialTCP(ctx context.Context, target vmaccess.Target, port int) (net.Conn, error) {
	conn, err := f.forwarder.DialTCP(ctx, target, port)
	if err != nil {
		return nil, err
	}
	active := f.active.Add(1)
	for maximum := f.maximum.Load(); active > maximum; maximum = f.maximum.Load() {
		if f.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	return &sequentialConn{Conn: conn, release: func() { f.active.Add(-1) }}, nil
}

type sequentialConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *sequentialConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func TestConnectorSequentialHTTPRequestsPreserveSession(t *testing.T) {
	e := newEnv(t)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = fmt.Fprint(w, "sequential response")
	})
	_, port, err := net.SplitHostPort(e.app.addr())
	if err != nil {
		t.Fatal(err)
	}
	appPort, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	x := e.expect(func(x *Expected) { x.TargetPort = appPort })
	forwarder := &sequentialForwarder{forwarder: connectorpreview.NewDirectTCPForwarder(nil)}
	manager := connectorpreview.NewSessionManager(forwarder, nil)
	defer manager.Shutdown("cleanup")
	h := wss.NewHandler(nil, wss.SendMessageFunc(func(context.Context, any) error { return nil }))
	h.SetPreviewManager(manager, connectorpreview.DataWSSClientConfig{
		EndpointURL: e.dataURL(), Credential: testCredential, RuntimeID: "runtime", AllowInsecure: true,
	})
	open := func(ctx context.Context, id string) error {
		raw, err := json.Marshal(protocol.PreviewOpenMessage{
			BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: id,
				SentAt: time.Now(), PreviewSessionID: x.SessionID, LabInstanceID: x.LabInstanceID, Generation: x.Generation},
			Payload: protocol.PreviewOpenPayload{TargetVmKey: x.TargetVMKey, ProviderServerID: x.ProviderServerID, TargetPort: x.TargetPort},
		})
		if err != nil {
			return err
		}
		return h.HandleMessage(ctx, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	attached, _, _ := e.gw.Pending(x.SessionID)
	if err := open(ctx, x.OpenMessageID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	activation, err := e.gw.Activate(x.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, credential, _ := strings.Cut(activation.URL, "#")
	cookie := e.login(activeSession{Expected: x, Credential: credential})
	var attempts atomic.Int32
	e.gw.SetTunnelOpener(fakeOpener{openFn: func(ctx context.Context, _ string) error {
		id := fmt.Sprintf("sequential-%d", attempts.Add(1))
		ready, err := e.gw.PrepareTunnel(x.SessionID, id)
		if err != nil {
			return err
		}
		if err := open(ctx, id); err != nil {
			e.gw.CancelTunnel(x.SessionID, id)
			return err
		}
		// Like Service.OpenTunnel, actual Gateway readiness is authoritative.
		// The Connector may receive ATTACHED before Gateway setTunnel runs.
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			e.gw.CancelTunnel(x.SessionID, id)
			return ctx.Err()
		}
	}})
	const requests = 200
	for i := range requests {
		req := httptest.NewRequest(http.MethodGet, "http://"+e.host(x)+"/", nil).WithContext(ctx)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.gw.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "sequential response" {
			t.Fatalf("sequential request %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if attempts.Load() != requests-1 || forwarder.maximum.Load() != 1 {
		t.Fatalf("attempts=%d maximum active TCP=%d", attempts.Load(), forwarder.maximum.Load())
	}
	if info, ok := e.gw.Info(x.SessionID); !ok || info.Ended || len(e.lifecyle.all()) != 0 {
		t.Fatal("sequential TCP closes ended the logical session")
	}
	manager.Shutdown("cleanup")
	if forwarder.active.Load() != 0 || manager.Count() != 0 {
		t.Fatalf("cleanup retained TCP=%d sessions=%d", forwarder.active.Load(), manager.Count())
	}
}
