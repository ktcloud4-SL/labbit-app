package vmaccess

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticDiscardsRawNestedErrors(t *testing.T) {
	secret := "raw-token-provider-body-terminal-content"
	inner := Diagnose(StageChannelOpen, errors.Join(ErrInvalidTarget, context.DeadlineExceeded, &ssh.OpenChannelError{Reason: ssh.ResourceShortage, Message: secret}), time.Now())
	err := Diagnose(StageResolveServer, fmt.Errorf("%s: %w", secret, inner), time.Now())
	info, ok := Diagnostic(err)
	if !ok || info.Stage != StageChannelOpen || !info.Timeout || info.ChannelReason != uint32(ssh.ResourceShortage) {
		t.Fatalf("classification = %+v, %v", info, ok)
	}
	if !errors.Is(err, ErrInvalidTarget) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("safe sentinel lost")
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), secret) {
			t.Fatal("raw data retained")
		}
	}
	unknown := Diagnose(FailureStage(255), errors.New(secret), time.Time{})
	i, _ := Diagnostic(unknown)
	if i.Stage.String() != "unknown" || strings.Contains(unknown.Error(), secret) {
		t.Fatal("unbounded category or raw error")
	}
}

func TestManagerDiagnosticPreservesResolverStageAndHostPin(t *testing.T) {
	peer := newSSHPeer(t)
	target := Target{LabInstanceID: "lab", Generation: 1, VMKey: "vm", ServerID: "server"}
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) {
		return Endpoint{}, Diagnose(StageResolveServer, errors.New("private response"), time.Now())
	}), Config{})
	defer m.Close()
	_, err := m.OpenSession(context.Background(), target)
	info, _ := Diagnostic(err)
	if info.Stage != StageResolveServer || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("resolver stage = %+v", info)
	}
	endpoint := peer.endpoint
	endpoint.HostKeyCallback = func(string, net.Addr, ssh.PublicKey) error { return errors.New("private pin detail") }
	m2 := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{})
	defer m2.Close()
	_, err = m2.OpenSession(context.Background(), target)
	info, _ = Diagnostic(err)
	if info.Stage != StageHostPin || strings.Contains(err.Error(), "private") {
		t.Fatalf("pin stage = %+v", info)
	}
}

func TestDiagnosticPreservesOuterSafeSentinelsWithoutRawWrapper(t *testing.T) {
	prior := Diagnose(StageShellStart, errors.New("private peer rejection"), time.Now())
	outer := fmt.Errorf("raw-token: %w", errors.Join(prior, ErrInvalidTarget, ErrClosed, context.Canceled, context.DeadlineExceeded))
	err := Diagnose(StagePTYRequest, outer, time.Now())
	info, ok := Diagnostic(err)
	if !ok || info.Stage != StageShellStart || !info.Timeout {
		t.Fatalf("stage = %+v", info)
	}
	for _, kind := range []error{ErrInvalidTarget, ErrClosed, context.Canceled, context.DeadlineExceeded} {
		if !errors.Is(err, kind) {
			t.Fatalf("lost sentinel %v", kind)
		}
	}
	if strings.Contains(err.Error(), "raw-token") || strings.Contains(err.Error(), "private") {
		t.Fatal("raw cause retained")
	}
}
