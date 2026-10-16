package preview

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewLifecycleCancelClosesPendingHTTPUpgrade(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(w, "fixture finished", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	a, b := net.Pipe()
	defer b.Close()
	session := NewPreviewSession("S", "lab", 1, "workspace", "server", 3000, nil, nil)
	_, _ = session.BindTunnel("M1", a)
	defer session.Close("cleanup")
	client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: strings.Replace(server.URL, "http", "ws", 1), AllowInsecure: true, RuntimeID: "runtime", DialTimeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.DialAndAttach(ctx, session, "M1") }()
	<-entered
	cancel()
	select {
	case err := <-done:
		t.Logf("canceled Upgrade returned promptly: %v", err)
		close(release)
	case <-time.After(250 * time.Millisecond):
		t.Error("cancel does not close socket while HTTP Upgrade response is pending")
		close(release)
		<-done
	}
}
