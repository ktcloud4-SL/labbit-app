package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

type committedFixtureFiles struct {
	mu           sync.Mutex
	body         []byte
	saves, reads int
}

func (*committedFixtureFiles) Tree(context.Context, vmaccess.Target, string) ([]workspacefile.Entry, error) {
	return nil, nil
}
func (f *committedFixtureFiles) Read(context.Context, vmaccess.Target, string, int64) (workspacefile.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return workspacefile.File{Content: append([]byte(nil), f.body...), Revision: "fixture-revision"}, nil
}
func (f *committedFixtureFiles) Save(_ context.Context, _ vmaccess.Target, _, _ string, body []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.body = append([]byte(nil), body...)
	return "fixture-revision", nil
}

// Exercise the acceptance driver against the production Control handler and
// File manager. A compliant completed-ID duplicate deliberately has no ACK.
// Only the VM file boundary is fake; no Cloud credentials or calls are used.
func TestOP02UnknownSaveUsesFreshReadWithoutDuplicateACK(t *testing.T) {
	oldService, oldMessages, oldUnmatched, oldJournal, oldPath := service, messages, unmatched, journal, journalPath
	defer func() {
		service, messages, unmatched, journal, journalPath = oldService, oldMessages, oldUnmatched, oldJournal, oldPath
	}()
	service = mock.NewMockSaaS("synthetic-fixture-token")
	control := service.Server.Config.Handler
	service.Server.Close()
	peer := newOP02Peer("synthetic-fixture-token")
	service.Server = httptest.NewTLSServer(peer.handler(control))
	defer service.Close()
	defer peer.close()
	messages, unmatched = make(chan []byte, 32), nil
	inbox := messages
	service.OnMsgReceived = func(raw []byte) { inbox <- append([]byte(nil), raw...) }
	connected := make(chan struct{})
	service.OnConnected = func(*websocket.Conn) { close(connected) }
	journal, journalPath = ledger{LabID: "fixture-lab"}, filepath.Join(t.TempDir(), "ledger.json")
	roots := x509.NewCertPool()
	roots.AddCert(service.Server.Certificate())
	dialer := &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Subprotocols: []string{protocol.SubprotocolControl}}
	base := "wss" + strings.TrimPrefix(service.Server.URL, "https")
	conn, _, err := dialer.Dial(base+"/connector/v1/control", http.Header{"Authorization": []string{"Bearer synthetic-fixture-token"}})
	if err != nil {
		t.Fatal("trusted Control dial failed")
	}
	defer conn.Close()
	<-connected
	files := &committedFixtureFiles{body: []byte("initial")}
	fileDialer := *dialer
	fileDialer.Subprotocols = []string{protocol.SubprotocolFileData}
	manager, err := filetransport.New(files, filetransport.Config{EndpointURL: base + "/connector/v1/file-data", RuntimeID: "fixture-runtime", Dialer: &fileDialer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	defer func() {
		stop, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if manager.Shutdown(stop) != nil {
			t.Error("File workers did not drain")
		}
	}()
	var writeMu sync.Mutex
	handler := wss.NewHandler(nil, wss.SendMessageFunc(func(_ context.Context, value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(value)
	}))
	handler.SetFileManager(manager, "synthetic-fixture-token")
	listening := make(chan struct{})
	go func() { defer close(listening); _ = handler.Listen(ctx, conn) }()
	defer func() { conn.Close(); <-listening }()
	r := &op02Run{peer: peer}
	if err = r.unknownSave(ctx, "owned-server", 1); err != nil {
		t.Fatalf("compliant completed Save rejected: %v", err)
	}
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.saves != 1 || files.reads != 2 {
		t.Fatal("Save replayed or fresh Read missing")
	}
	count := 0
	peer.files.Range(func(_, value any) bool {
		count++
		if value.(*dataPlan).dials.Load() != 1 {
			t.Error("File ID redialed")
		}
		return true
	})
	if count != 3 {
		t.Fatal("fresh Read did not allocate a new request ID")
	}
}
