package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

type rejectedAccessResolver struct{ calls int }

func (r *rejectedAccessResolver) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error) {
	r.calls++
	return vmaccess.Endpoint{}, vmaccess.ErrInvalidTarget
}

func TestOperationResultInvalidatesObsoleteAccess(t *testing.T) {
	for _, mutation := range []string{"reset", "cleanup"} {
		for _, outcome := range []string{"succeeded", "unknown", "preflight-failed", "internal-error"} {
			t.Run(mutation+"/"+outcome, func(t *testing.T) {
				resolver := &rejectedAccessResolver{}
				manager := vmaccess.NewManager(resolver, vmaccess.Config{})
				defer manager.Close()
				result := provider.OperationResult{Outcome: provider.OutcomeSucceeded}
				var mutationErr error
				switch outcome {
				case "unknown":
					result.Outcome = provider.OutcomeUnknown
				case "preflight-failed":
					result.Outcome = provider.OutcomeFailed
				case "internal-error":
					mutationErr = errors.New("safe internal error")
				}
				p := &accessLifecycleProvider{Provider: &provider.MockProvider{
					ResetFunc: func(context.Context, provider.ResetRequest) (provider.OperationResult, error) {
						return result, mutationErr
					},
					CleanupFunc: func(context.Context, provider.CleanupRequest) (provider.OperationResult, error) {
						return result, mutationErr
					},
				}, manager: manager}
				target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
				if mutation == "reset" {
					_, _ = p.Reset(context.Background(), provider.ResetRequest{Correlation: provider.Correlation{LabInstanceID: "lab-1", Generation: 2}})
				} else {
					_, _ = p.Cleanup(context.Background(), provider.CleanupRequest{Correlation: provider.Correlation{LabInstanceID: "lab-1", Generation: 1}})
				}
				_, _ = manager.OpenSession(context.Background(), target)
				want := 0
				if outcome == "preflight-failed" {
					want = 1
				}
				if resolver.calls != want {
					t.Fatalf("obsolete target reached resolver %d times, want %d", resolver.calls, want)
				}
			})
		}
	}
}

func TestSSHIdleRuntimeDuration(t *testing.T) {
	for _, value := range []string{"", "150ms", "3m", "0", "-1s", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LABBIT_OPENSTACK_SSH_IDLE_TIMEOUT", value)
			timeout, err := sshIdleTimeoutFromEnvironment()
			if value == "0" || value == "-1s" || value == "invalid" {
				if err == nil {
					t.Fatal("invalid idle policy accepted")
				}
				return
			}
			if err != nil || timeout <= 0 {
				t.Fatal("valid idle policy rejected")
			}
			if value == "" && timeout != time.Minute {
				t.Fatal("runtime default differs from contract")
			}
		})
	}
}
