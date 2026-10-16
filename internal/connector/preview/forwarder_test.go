package preview

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

type forwardingFunc func(context.Context, vmaccess.Target, int) (net.Conn, error)

func (f forwardingFunc) DialTCP(ctx context.Context, target vmaccess.Target, port int) (net.Conn, error) {
	return f(ctx, target, port)
}

type workspaceValidatorFunc func(context.Context, vmaccess.Target) error

func (f workspaceValidatorFunc) ValidateWorkspace(ctx context.Context, target vmaccess.Target) error {
	return f(ctx, target)
}

type accessResolverFunc func(context.Context, vmaccess.Target) (vmaccess.Endpoint, error)

func (f accessResolverFunc) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
	return f(ctx, target)
}

func TestSessionManagerForwardsFullControlTarget(t *testing.T) {
	expected := vmaccess.Target{LabInstanceID: "lab", Generation: 7, VMKey: "workspace", ServerID: "server"}
	manager := NewSessionManager(forwardingFunc(func(_ context.Context, target vmaccess.Target, port int) (net.Conn, error) {
		if target != expected || port != 5000 {
			t.Fatalf("control target lost: %+v port=%d", target, port)
		}
		return nil, ErrAppNotRunning
	}), nil)
	_, _, err := manager.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{TargetVmKey: expected.VMKey, ProviderServerID: expected.ServerID, TargetPort: 5000}, protocol.BaseEnvelope{PreviewSessionID: "preview", LabInstanceID: expected.LabInstanceID, Generation: expected.Generation})
	if !errors.Is(err, ErrAppNotRunning) || manager.Count() != 0 {
		t.Fatal("failed dial registered a session")
	}
}

func TestSSHForwarderGuardsWorkspacePortAndSanitizesErrors(t *testing.T) {
	expected := vmaccess.Target{LabInstanceID: "lab", Generation: 7, VMKey: "workspace", ServerID: "server"}
	var validations, resolutions int
	access := vmaccess.NewManager(accessResolverFunc(func(_ context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
		resolutions++
		if target != expected {
			t.Fatal("resolver target changed")
		}
		return vmaccess.Endpoint{}, errors.New("private provider endpoint/key detail")
	}), vmaccess.Config{})
	defer access.Close()
	var reject bool
	f := &SSHForwarder{Access: access, Validator: workspaceValidatorFunc(func(_ context.Context, target vmaccess.Target) error {
		validations++
		if target != expected {
			t.Fatal("validator target changed")
		}
		if reject {
			return errors.New("private Workspace metadata")
		}
		return nil
	})}
	for _, port := range []int{-1, 0, 22, 65536} {
		if _, err := f.DialTCP(context.Background(), expected, port); !errors.Is(err, ErrPortRejected) {
			t.Fatal("unsafe port accepted")
		}
	}
	if validations != 0 || resolutions != 0 {
		t.Fatal("port rejection reached Provider")
	}
	reject = true
	if _, err := f.DialTCP(context.Background(), expected, 80); !errors.Is(err, ErrVMUnreachable) || strings.Contains(err.Error(), "private") {
		t.Fatal("unsafe Workspace error")
	}
	if resolutions != 0 {
		t.Fatal("non-Workspace target reached SSH resolver")
	}
	reject = false
	for _, port := range []int{80, 3000, 5000, 65535} {
		if _, err := f.DialTCP(context.Background(), expected, port); !errors.Is(err, ErrVMUnreachable) || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe Provider error")
		}
	}
	if resolutions != 4 {
		t.Fatal("connector introduced an application port allowlist")
	}
}

func TestSSHForwarderCancellationAndUnavailableConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	access := vmaccess.NewManager(nil, vmaccess.Config{})
	defer access.Close()
	f := &SSHForwarder{Access: access, Validator: workspaceValidatorFunc(func(ctx context.Context, _ vmaccess.Target) error { <-ctx.Done(); return ctx.Err() })}
	if _, err := f.DialTCP(ctx, vmaccess.Target{}, 8080); !errors.Is(err, ErrVMUnreachable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation classification: %v", err)
	}
	for _, f := range []*SSHForwarder{nil, {}, {Access: access}} {
		if _, err := f.DialTCP(context.Background(), vmaccess.Target{}, 8080); !errors.Is(err, ErrVMUnreachable) {
			t.Fatal("unconfigured SSH forwarder accepted")
		}
	}
}
