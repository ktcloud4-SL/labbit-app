//go:build dr_transport_trial

package app

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestReconciliationTransportTrial runs only in the dedicated, short-lived
// Osaka transport Pod. It has no DB, worker, or Connector clients.
func TestReconciliationTransportTrial(t *testing.T) {
	ip := net.ParseIP(os.Getenv("DR_TRANSPORT_POD_IP")).To4()
	if ip == nil || ip[0] != 10 || ip[1] != 20 {
		t.Fatal("Osaka Pod IP required")
	}
	config, err := reconciliationTLS("/trial/server")
	if err != nil {
		t.Fatal("trial TLS material could not be loaded")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "9443"))
	if err != nil {
		t.Fatal("could not open dedicated listener")
	}
	server := &http.Server{
		Handler:           reconciliationHandler(reconciliationIdentity{}, func() bool { return false }, nil),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       4 * time.Second,
		WriteTimeout:      6 * time.Second,
		IdleTimeout:       3 * time.Second,
	}
	timer := time.AfterFunc(15*time.Minute, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	defer timer.Stop()
	t.Log("dedicated mTLS transport listener ready")
	if err = server.Serve(tls.NewListener(listener, config)); !errors.Is(err, http.ErrServerClosed) {
		t.Fatal("trial listener stopped unexpectedly")
	}
}
