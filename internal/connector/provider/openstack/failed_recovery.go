package openstackprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
)

// FailedProvisionEvidence is an operator-only observation. It never creates or
// deletes Provider resources and is not used by ordinary Reconcile.
func (a *Adapter) FailedProvisionEvidence(ctx context.Context, req operationrecovery.Request) (operationrecovery.Evidence, error) {
	deny := operationrecovery.ErrUnsafe
	if a == nil || a.provider == nil || a.ktNetwork == nil || a.volume == nil || req.Generation != 1 || req.ProjectID != a.provision.ProjectID || req.ConnectionID.String() != a.provision.ProviderConnectionID {
		return operationrecovery.Evidence{}, deny
	}
	auth, ok := a.provider.GetAuthResult().(interface {
		ExtractProject() (*tokens.Project, error)
	})
	if !ok {
		return operationrecovery.Evidence{}, deny
	}
	project, err := auth.ExtractProject()
	if err != nil || project == nil || project.ID != req.ProjectID {
		return operationrecovery.Evidence{}, deny
	}
	pool, dir, err := a.cidrPool()
	if err != nil || !recoveryOwnedPath(dir, 0700, true) || checkCIDRPoolFile(pool, dir, false) != nil {
		return operationrecovery.Evidence{}, deny
	}
	corr := coreprovider.Correlation{OperationID: req.OperationID.String(), LabInstanceID: req.InstanceID.String(), Generation: req.Generation}
	// Serialize the complete immutable ledger read and exact Provider observation
	// with existing CIDR writers. A crash retains LOCK and requires operator review.
	if !validCorrelation(corr) || writeCIDRFile(dir, "LOCK", corr) != nil {
		return operationrecovery.Evidence{}, deny
	}
	defer os.Remove(filepath.Join(dir, "LOCK"))
	assignments, err := readCIDRAssignments(pool, dir)
	if err != nil {
		return operationrecovery.Evidence{}, deny
	}
	slot := -1
	for index, entry := range assignments {
		if entry.Instance == corr.LabInstanceID {
			slot = index
		}
	}
	if slot < 0 {
		return operationrecovery.Evidence{}, deny
	}
	var completion ktCIDRCompletion
	if validateCIDRCompletionFile(dir, completionName(slot, 1), assignments) != nil || readCIDRFile(filepath.Join(dir, completionName(slot, 1)), &completion) != nil || completion.Correlation != corr || completion.Outcome != coreprovider.OutcomeFailed {
		return operationrecovery.Evidence{}, deny
	}
	// No other generation intent, retired history, symlink or foreign owner may
	// hide a mutation that this generation-one recovery would otherwise forget.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return operationrecovery.Evidence{}, deny
	}
	for _, entry := range entries {
		if entry.Name() == retiredName(corr.LabInstanceID) {
			return operationrecovery.Evidence{}, deny
		}
		var intent coreprovider.Correlation
		if len(entry.Name()) > 7 && entry.Name()[:7] == "intent-" {
			if readCIDRFile(filepath.Join(dir, entry.Name()), &intent) != nil || (intent.LabInstanceID == corr.LabInstanceID && intent != corr) {
				return operationrecovery.Evidence{}, deny
			}
		}
	}
	ledger := map[string]string{}
	for _, name := range []string{"pool.json", slotName(slot), intentName(slot, 1), completionName(slot, 1)} {
		path := filepath.Join(dir, name)
		if !recoveryOwnedPath(path, 0600, false) {
			return operationrecovery.Evidence{}, deny
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 32768 {
			return operationrecovery.Evidence{}, deny
		}
		ledger[name] = operationrecovery.SHA256(data)
	}
	encoded, _ := json.Marshal(ledger)
	proof := operationrecovery.Evidence{Request: req, Outcome: "FAILED", LedgerSHA256: operationrecovery.SHA256(encoded), ReceiptSHA256: ledger[completionName(slot, 1)]}
	for _, ref := range completion.Resources {
		proof.Resources = append(proof.Resources, operationrecovery.Resource{Type: string(ref.ResourceType), ProviderID: ref.ProviderID, LogicalName: ref.LogicalName, Generation: ref.Generation, ObservedState: "PRESENT"})
	}
	if operationrecovery.ValidateResources(req.VMKey, 1, proof.Resources) != nil {
		return operationrecovery.Evidence{}, deny
	}
	byName := map[string]coreprovider.ResourceRef{}
	for _, ref := range completion.Resources {
		byName[ref.LogicalName] = ref
	}
	serverRef, rootRef := byName[req.VMKey], byName[req.VMKey+":root-volume"]
	server, err := a.lookupServer(ctx, serverRef.ProviderID)
	if err != nil || server == nil || server.ID != serverRef.ProviderID || server.TenantID != req.ProjectID ||
		server.Metadata["labbit_operation_id"] != corr.OperationID || server.Metadata["labbit_lab_instance_id"] != corr.LabInstanceID ||
		server.Metadata["labbit_generation"] != strconv.FormatInt(req.Generation, 10) || server.Metadata["labbit_vm_key"] != req.VMKey ||
		len(server.AttachedVolumes) != 1 || server.AttachedVolumes[0].ID != rootRef.ProviderID {
		return operationrecovery.Evidence{}, deny
	}
	root, err := volumes.Get(ctx, a.volume, rootRef.ProviderID).Extract()
	if err != nil || root == nil || root.ID != rootRef.ProviderID || len(root.Attachments) != 1 || root.Attachments[0].ServerID != server.ID {
		return operationrecovery.Evidence{}, deny
	}
	config := a.provision
	config.KTCloudManagementCIDR = assignments[slot].Pair.Management
	for _, name := range []string{"lab-tier", req.VMKey + ":management-tier"} {
		tier, exists, err := a.ktNetwork.tier(ctx, byName[name].ProviderID)
		logical, matches := ktCloudTierCandidate(tier.Name, corr.LabInstanceID, 1, []string{req.VMKey})
		expected := assignments[slot].Pair.Lab
		if name != "lab-tier" {
			expected = assignments[slot].Pair.Management
		}
		if err != nil || !exists || tier.ID != byName[name].ProviderID || tier.ID == config.KTCloudConnectorTierID || !tier.sharedKnown || tier.Shared || !matches || logical != name || tier.CIDR != expected {
			return operationrecovery.Evidence{}, deny
		}
	}
	policies, err := a.ktNetwork.policies(ctx)
	if err != nil {
		return operationrecovery.Evidence{}, deny
	}
	matches := 0
	for _, policy := range policies {
		if policy.ID == byName[req.VMKey+":management-ssh"].ProviderID {
			if policy.Comment != provisionBaseName(corr.LabInstanceID, 1)+":"+req.VMKey+":management-ssh" || !ktSSHPolicyMatches(policy, config, byName[req.VMKey+":management-tier"].ProviderID) {
				return operationrecovery.Evidence{}, deny
			}
			matches++
		}
	}
	if matches != 1 {
		return operationrecovery.Evidence{}, deny
	}
	for i, ref := range completion.Resources {
		state, exists, err := a.observeResource(ctx, ref)
		if err != nil || !exists {
			return operationrecovery.Evidence{}, deny
		}
		proof.Resources[i].ObservedState = state
	}
	if operationrecovery.ValidateResources(req.VMKey, 1, proof.Resources) != nil {
		return operationrecovery.Evidence{}, deny
	}
	proof.ObservedAt = time.Now().UTC()
	return proof, nil
}
