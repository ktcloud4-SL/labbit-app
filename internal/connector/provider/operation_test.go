package provider

import (
	"context"
	"errors"
	"testing"
)

func TestDispatchOperationPassesResolvedInputs(t *testing.T) {
	correlation := Correlation{OperationID: "operation-1", LabInstanceID: "lab-instance-1", Generation: 2}
	snapshot := CreationSnapshot{
		ProviderConnectionID: "connection-1",
		WorkspaceVMKey:       "workspace",
		VMs: []VMSpec{{
			VMKey: "workspace", ImageID: "image-1", FlavorID: "flavor-1",
		}},
	}
	oldResource := ResourceRef{ResourceType: "SERVER", ProviderID: "old-server", Generation: 1}
	newResource := ResourceResult{ResourceRef: ResourceRef{ResourceType: "SERVER", ProviderID: "new-server", Generation: 2}}

	mock := &MockProvider{
		ProvisionFunc: func(_ context.Context, request ProvisionRequest) (OperationResult, error) {
			if request.Correlation != correlation || request.CreationSnapshot.VMs[0].ImageID != "image-1" {
				t.Fatalf("Provision lost correlation or resolved snapshot: %+v", request)
			}
			return OperationResult{Outcome: OutcomeSucceeded, ProviderResources: []ResourceResult{newResource}}, nil
		},
		ResetFunc: func(_ context.Context, request ResetRequest) (OperationResult, error) {
			if request.Correlation != correlation || request.CreationSnapshot.ProviderConnectionID != "connection-1" {
				t.Fatalf("Reset lost correlation or original snapshot: %+v", request)
			}
			if len(request.ProviderResources) != 1 || request.ProviderResources[0] != oldResource {
				t.Fatalf("Reset lost old-generation resources: %+v", request.ProviderResources)
			}
			return OperationResult{Outcome: OutcomeSucceeded, ProviderResources: []ResourceResult{newResource}}, nil
		},
	}

	for _, command := range []OperationCommand{
		{Correlation: correlation, MutationType: MutationProvision, CreationSnapshot: &snapshot},
		{Correlation: correlation, MutationType: MutationReset, CreationSnapshot: &snapshot, ProviderResources: []ResourceRef{oldResource}},
	} {
		result, err := DispatchOperation(context.Background(), mock, command)
		if err != nil || result.Outcome != OutcomeSucceeded || len(result.ProviderResources) != 1 || result.ProviderResources[0] != newResource {
			t.Fatalf("%s result = %+v, %v", command.MutationType, result, err)
		}
	}
}

func TestDispatchOperationPreservesUnknownWithoutRetry(t *testing.T) {
	calls := 0
	mock := &MockProvider{CleanupFunc: func(_ context.Context, request CleanupRequest) (OperationResult, error) {
		calls++
		if request.OperationID != "operation-2" || request.Generation != 3 || len(request.ProviderResources) != 0 {
			t.Fatalf("Cleanup received unexpected input: %+v", request)
		}
		return OperationResult{
			Outcome:           OutcomeUnknown,
			ProviderResources: []ResourceResult{},
			Error:             &SafeError{Code: "ERR_PROVIDER_UNCERTAIN"},
		}, nil
	}}

	result, err := DispatchOperation(context.Background(), mock, OperationCommand{
		Correlation:       Correlation{OperationID: "operation-2", LabInstanceID: "lab-instance-2", Generation: 3},
		MutationType:      MutationCleanup,
		ProviderResources: []ResourceRef{},
	})
	if err != nil || result.Outcome != OutcomeUnknown || calls != 1 {
		t.Fatalf("Cleanup result = %+v, err = %v, calls = %d", result, err, calls)
	}
}

func TestDispatchOperationKeepsOriginalSnapshotAndResources(t *testing.T) {
	snapshot := &CreationSnapshot{
		VMs:           []VMSpec{{VMKey: "workspace", ImageID: "image-1"}},
		StartupScript: &StartupScript{Content: "echo ready"},
	}
	resources := []ResourceRef{{ResourceType: "SERVER", ProviderID: "old-server", Generation: 1}}
	mock := &MockProvider{ResetFunc: func(_ context.Context, request ResetRequest) (OperationResult, error) {
		request.CreationSnapshot.VMs[0].ImageID = "changed"
		request.CreationSnapshot.StartupScript.Content = "changed"
		request.ProviderResources[0].ProviderID = "changed"
		return OperationResult{Outcome: OutcomeSucceeded, ProviderResources: []ResourceResult{}}, nil
	}}

	_, err := DispatchOperation(context.Background(), mock, OperationCommand{
		Correlation:       Correlation{OperationID: "operation-5", LabInstanceID: "lab-instance-5", Generation: 2},
		MutationType:      MutationReset,
		CreationSnapshot:  snapshot,
		ProviderResources: resources,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.VMs[0].ImageID != "image-1" || snapshot.StartupScript.Content != "echo ready" || resources[0].ProviderID != "old-server" {
		t.Fatal("Provider mutation changed the immutable command input")
	}
}

func TestDispatchOperationRejectsMissingRequiredInputs(t *testing.T) {
	mock := &MockProvider{}
	base := Correlation{OperationID: "operation-3", LabInstanceID: "lab-instance-3", Generation: 1}
	for _, command := range []OperationCommand{
		{Correlation: base, MutationType: MutationProvision},
		{Correlation: base, MutationType: MutationReset},
		{Correlation: base, MutationType: MutationCleanup},
		{Correlation: Correlation{OperationID: "operation-3", LabInstanceID: "lab-instance-3"}, MutationType: MutationCleanup, ProviderResources: []ResourceRef{}},
		{Correlation: base, MutationType: MutationType("OTHER")},
	} {
		_, err := DispatchOperation(context.Background(), mock, command)
		if !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("%s error = %v, want ErrInvalidCommand", command.MutationType, err)
		}
	}
}

func TestMockProviderReconcileReturnsObservations(t *testing.T) {
	known := ResourceRef{ResourceType: "SERVER", ProviderID: "server-1", Generation: 1}
	mock := &MockProvider{ReconcileFunc: func(_ context.Context, request ReconcileRequest) (ReconcileResult, error) {
		if request.OperationID != "operation-4" || !request.DiscoverCandidates || len(request.KnownResources) != 1 || request.KnownResources[0] != known {
			t.Fatalf("Reconcile received unexpected input: %+v", request)
		}
		return ReconcileResult{Observations: []ResourceObservation{
			{ResourceType: "SERVER", ProviderID: "server-1", Generation: 1, Exists: true, Source: SourceKnownResource},
			{ResourceType: "SERVER", ProviderID: "candidate-1", Generation: 1, Exists: true, Source: SourceDiscoveredCandidate},
		}}, nil
	}}

	result, err := mock.Reconcile(context.Background(), ReconcileRequest{
		Correlation:        Correlation{OperationID: "operation-4", LabInstanceID: "lab-instance-4", Generation: 1},
		KnownResources:     []ResourceRef{known},
		DiscoverCandidates: true,
	})
	if err != nil || len(result.Observations) != 2 || result.Observations[1].Source != SourceDiscoveredCandidate {
		t.Fatalf("Reconcile result = %+v, %v", result, err)
	}
}

func TestMockProviderRequiresConfiguredResponse(t *testing.T) {
	_, err := (&MockProvider{}).Provision(context.Background(), ProvisionRequest{})
	if !errors.Is(err, ErrMockNotConfigured) {
		t.Fatalf("unconfigured mock error = %v", err)
	}
}

func TestDispatchOperationRejectsMissingProvider(t *testing.T) {
	_, err := DispatchOperation(context.Background(), nil, OperationCommand{
		Correlation:  Correlation{OperationID: "operation-6", LabInstanceID: "lab-instance-6", Generation: 1},
		MutationType: MutationCleanup,
	})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("missing provider error = %v", err)
	}
}
