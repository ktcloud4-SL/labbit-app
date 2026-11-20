package provision

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

type executorResumeAuth struct{ proof wc.ResumeProof }

func (a executorResumeAuth) AuthorizeResume(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) {
	return a.proof.Approval, nil
}
func (a executorResumeAuth) RecheckResume(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) {
	return a.proof.Checks, nil
}

func TestApprovedResumeMultipleExecutorsClaimAndDispatchOnlyWithNewBinding(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			scope := wc.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}
			journal := workercontrolfake.New(wc.State{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.PauseNew})
			now := time.Now().UTC().Truncate(time.Microsecond)
			journal.SetClock(func() time.Time { return now })
			b := wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.PauseNew, PodID: "old-pod", BootID: uuid.New()}
			oldGate, err := wc.NewDurable(b, journal)
			if err != nil {
				t.Fatal(err)
			}
			writes := make(chan repository.ProvisionUpdate, 20)
			base := provisionTestStore(t, writes)
			var sends atomic.Int32
			old := controlledExecutor(t, kind, oldGate, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("old worker sent")
				return connector.SentMessage{}, nil
			}})
			if claimed, err := old.RunOne(t.Context()); claimed || !errors.Is(err, wc.ErrPaused) {
				t.Fatal("standby entered", err)
			}
			r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run-active", Phase: "failover", Principal: "operator-fixture", ApprovalRef: "approval-fixture", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
			proof := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
			a := executorResumeAuth{proof}
			resumer, err := wc.NewResumer(journal, a, a, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = resumer.Resume(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			if claimed, err := old.RunOne(t.Context()); claimed || !errors.Is(err, wc.ErrPaused) {
				t.Fatal("old gate automatically reopened", err)
			}
			var wg sync.WaitGroup
			for range 2 {
				next := b
				next.Mode = wc.Active
				next.Revision = 2
				next.Epoch = 2
				next.BootID = uuid.New()
				next.PodID = "resumed-pod"
				gate, err := wc.NewDurable(next, journal)
				if err != nil {
					t.Fatal(err)
				}
				var executor *Executor
				executor = controlledExecutor(t, kind, gate, base, provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
					sends.Add(1)
					executor.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
					return connector.SentMessage{MessageID: "outbound-message"}, nil
				}})
				wg.Go(func() {
					if _, err := executor.RunOne(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if sends.Load() != 1 {
				t.Fatalf("same queued item dispatched %d times", sends.Load())
			}
			report, err := journal.Report(t.Context(), scope)
			if err != nil || report.OpenBoundaries != 0 || report.PendingResumes != 0 {
				t.Fatal("journal not closed", err)
			}
		})
	}
}
