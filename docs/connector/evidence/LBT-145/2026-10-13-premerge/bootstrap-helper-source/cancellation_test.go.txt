package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestCancellationClosesStalledConnectedTransport(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			connection, peer := net.Pipe()
			defer connection.Close()
			defer peer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			}
			defer cancel()
			closeOnCancellation(ctx, connection)
			done := make(chan error, 1)
			go func() { b := make([]byte, 1); _, err := connection.Read(b); done <- err }()
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled transport succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("parent cancellation did not unblock connected session")
			}
		})
	}
}
