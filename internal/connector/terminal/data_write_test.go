package terminal

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// An unbuffered peer accepts the Upgrade but never reads PTY output. Its write
// cannot finish until the production deadline expires and closes the socket.
func TestDataWriteTimeoutReleasesBlockedConnection(t *testing.T) {
	clientSide, peer := net.Pipe()
	defer peer.Close()
	handshakeDone := make(chan struct{})
	go func() {
		defer close(handshakeDone)
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			return
		}
		digest := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
	}()
	endpoint, _ := url.Parse("ws://localhost/")
	conn, _, err := websocket.NewClient(clientSide, endpoint, nil, 1024, 1024)
	if err != nil {
		t.Fatal("test Upgrade failed")
	}
	defer conn.Close()
	<-handshakeDone
	session := &Session{Status: StatusActive, DataConn: conn}
	done := make(chan error, 1)
	go func() { done <- session.WriteMessage(websocket.BinaryMessage, []byte("test-only-output")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unread output unexpectedly completed")
		}
	case <-time.After(dataWriteTimeout + 2*time.Second):
		t.Fatal("blocked Data writer exceeded its deadline")
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("failed writer did not release its socket")
	}
}
