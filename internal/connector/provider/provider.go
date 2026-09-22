package provider

import "context"

// Correlation identifies one LabInstance operation across the Control and Provider boundary.
type Correlation struct {
	OperationID   string
	LabInstanceID string
	Generation    int64
}

// CreationSnapshot is the resolved, immutable input used by Provision and Reset.
type CreationSnapshot struct {
	ProviderConnectionID string
	VMs                  []VMSpec
	WorkspaceVMKey       string
	InternetOutbound     bool
	StartupScript        *StartupScript
}

type VMSpec struct {
	VMKey         string
	Role          string
	InstanceIndex int64
	ImageID       string
	FlavorID      string
	FlavorSpec    FlavorSpec
}

type FlavorSpec struct {
	VCPUs   int64
	RAMMiB  int64
	DiskGiB int64
}

type StartupScript struct {
	Content string
	SHA256  string
}

type ResourceRef struct {
	ResourceType string
	ProviderID   string
	Generation   int64
	LogicalName  string
}

type ResourceResult struct {
	ResourceRef
	ObservedState string
}

type SafeError struct {
	Code    string
	Message string
}

type Outcome string

const (
	OutcomeSucceeded Outcome = "SUCCEEDED"
	OutcomeFailed    Outcome = "FAILED"
	OutcomeUnknown   Outcome = "UNKNOWN"
)

type OperationResult struct {
	Outcome           Outcome
	ProviderResources []ResourceResult
	Error             *SafeError
}

type ObservationSource string

const (
	SourceKnownResource       ObservationSource = "KNOWN_RESOURCE"
	SourceDiscoveredCandidate ObservationSource = "DISCOVERED_CANDIDATE"
)

type ResourceObservation struct {
	ResourceType  string
	ProviderID    string
	Generation    int64
	Exists        bool
	ObservedState string
	Source        ObservationSource
	LogicalName   string
}

type ReconcileResult struct {
	Observations []ResourceObservation
	Error        *SafeError
}

type ProvisionRequest struct {
	Correlation
	CreationSnapshot CreationSnapshot
}

type ResetRequest struct {
	Correlation
	CreationSnapshot  CreationSnapshot
	ProviderResources []ResourceRef
}

type CleanupRequest struct {
	Correlation
	ProviderResources []ResourceRef
}

type ReconcileRequest struct {
	Correlation
	KnownResources []ResourceRef
	// DiscoverCandidates is the effective value after applying the wire default of true.
	DiscoverCandidates bool
}

// Provider is the boundary called by Control; implementations own Provider API calls.
// Known failures belong in OperationResult with a classified outcome and SafeError.
// An unclassified Go error is internal; DispatchOperation and DispatchReconcile
// convert it to a safe result. Mock configuration errors remain internal errors.
type Provider interface {
	Provision(context.Context, ProvisionRequest) (OperationResult, error)
	Reset(context.Context, ResetRequest) (OperationResult, error)
	Cleanup(context.Context, CleanupRequest) (OperationResult, error)
	Reconcile(context.Context, ReconcileRequest) (ReconcileResult, error)
}
