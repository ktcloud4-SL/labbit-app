package previewtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestConnectionRefusedRecognizesClosedLoopbackPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || !connectionRefused(fmt.Errorf("workspace dial: %w", err)) {
		t.Fatalf("closed loopback port was not classified as refused: %v", err)
	}
}

func TestConnectionRefusedDoesNotMisclassifyOtherErrors(t *testing.T) {
	for name, err := range map[string]error{
		"nil":      nil,
		"deadline": context.DeadlineExceeded,
		"canceled": context.Canceled,
		"timeout":  &net.DNSError{IsTimeout: true},
		"denied":   &net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}},
	} {
		t.Run(name, func(t *testing.T) {
			if connectionRefused(err) {
				t.Fatalf("unrelated error classified as refused: %v", err)
			}
		})
	}
}
