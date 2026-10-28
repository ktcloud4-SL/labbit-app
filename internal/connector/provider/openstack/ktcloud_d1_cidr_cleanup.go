package openstackprovider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

// A completion is written only after a definite result, before it leaves the
// pool lock. Missing evidence (including legacy intents) never grants release.
type ktCIDRCompletion struct {
	Correlation coreprovider.Correlation   `json:"correlation"`
	Outcome     coreprovider.Outcome       `json:"outcome"`
	Resources   []coreprovider.ResourceRef `json:"resources"`
}

// This immutable tombstone fences delayed commands from the previous owner.
// It is synced BEFORE any active ledger files are removed.
type ktCIDRRetired struct {
	Assignment  ktCIDRAssignment         `json:"assignment"`
	Slot        int                      `json:"slot"`
	Cleanup     coreprovider.Correlation `json:"cleanup"`
	Completions []ktCIDRCompletion       `json:"completions"`
}

func completionName(slot int, generation int64) string {
	return fmt.Sprintf("completion-%03d-%d.json", slot, generation)
}
func retiredName(instance string) string {
	return fmt.Sprintf("retired-%x.json", sha256.Sum256([]byte(instance)))
}
func cidrResultRefs(results []coreprovider.ResourceResult) []coreprovider.ResourceRef {
	refs := make([]coreprovider.ResourceRef, 0, len(results))
	for _, result := range results {
		refs = append(refs, result.ResourceRef)
	}
	return refs
}
func cidrCompleteResult(result coreprovider.OperationResult, generation int64) bool {
	if result.Outcome != coreprovider.OutcomeSucceeded && result.Outcome != coreprovider.OutcomeFailed {
		return false
	}
	refs := cidrResultRefs(result.ProviderResources)
	if _, err := validateResourceRefs(refs, generation, false); err != nil {
		return false
	}
	// A server may be known before its asynchronously allocated boot volume.
	// FAILED boot-timeout without that exact volume ID is not complete evidence.
	for _, ref := range refs {
		if ref.ResourceType != coreprovider.ResourceTypeServer {
			continue
		}
		found := false
		for _, volume := range refs {
			if volume.ResourceType == coreprovider.ResourceTypeVolume && volume.Generation == ref.Generation && volume.LogicalName == ref.LogicalName+":root-volume" {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func validateCIDRCompletionFile(dir, name string, assignments map[int]ktCIDRAssignment) error {
	var slot int
	var generation int64
	if _, err := fmt.Sscanf(name, "completion-%03d-%d.json", &slot, &generation); err != nil || name != completionName(slot, generation) {
		return ErrProvisionCheck
	}
	assignment, ok := assignments[slot]
	var completion ktCIDRCompletion
	var intent coreprovider.Correlation
	if !ok || readCIDRFile(filepath.Join(dir, name), &completion) != nil || readCIDRFile(filepath.Join(dir, intentName(slot, generation)), &intent) != nil || completion.Correlation != intent || !validCorrelation(intent) || intent.Generation != generation || intent.LabInstanceID != assignment.Instance {
		return ErrProvisionCheck
	}
	if !validCIDRCompletion(completion) {
		return ErrProvisionCheck
	}
	return nil
}
func validCIDRCompletion(completion ktCIDRCompletion) bool {
	return cidrCompleteResult(coreprovider.OperationResult{Outcome: completion.Outcome, ProviderResources: resourceResults(completion.Resources, "")}, completion.Correlation.Generation)
}
func readCIDRRetired(pool ktCIDRPool, dir, name string) (ktCIDRRetired, error) {
	var retired ktCIDRRetired
	if readCIDRFile(filepath.Join(dir, name), &retired) != nil || !validCorrelation(retired.Cleanup) || retired.Assignment.Instance != retired.Cleanup.LabInstanceID || name != retiredName(retired.Assignment.Instance) || retired.Slot < 0 || retired.Slot >= len(pool.Pairs) || retired.Assignment.Pair != pool.Pairs[retired.Slot] {
		return retired, ErrProvisionCheck
	}
	for _, completion := range retired.Completions {
		if !validCorrelation(completion.Correlation) || completion.Correlation.LabInstanceID != retired.Assignment.Instance || completion.Correlation.Generation > retired.Cleanup.Generation {
			return retired, ErrProvisionCheck
		}
		if !validCIDRCompletion(completion) {
			return retired, ErrProvisionCheck
		}
	}
	return retired, nil
}

func (a *Adapter) cleanupCIDR(ctx context.Context, request coreprovider.CleanupRequest, resources []coreprovider.ResourceRef) coreprovider.OperationResult {
	hold := func() coreprovider.OperationResult {
		return unknownResult(resourceResults(resources, stateDeleteNotAttempted), "CIDR cleanup requires reconciliation")
	}
	pool, dir, err := a.cidrPool()
	if err != nil {
		return hold()
	}
	if writeCIDRFile(dir, "LOCK", request.Correlation) != nil {
		return hold()
	}
	defer os.Remove(filepath.Join(dir, "LOCK"))
	if checkCIDRPoolFile(pool, dir, false) != nil {
		return hold()
	}
	assignments, err := readCIDRAssignments(pool, dir)
	if err != nil {
		return hold()
	}
	if _, err := os.Lstat(filepath.Join(dir, retiredName(request.LabInstanceID))); !os.IsNotExist(err) {
		retired, err := readCIDRRetired(pool, dir, retiredName(request.LabInstanceID))
		if err != nil || request.Generation > retired.Cleanup.Generation {
			return hold()
		}
		for _, assignment := range assignments {
			if assignment.Instance == request.LabInstanceID {
				return hold()
			}
		}
		for _, ref := range resources {
			found := false
			for _, completion := range retired.Completions {
				for _, old := range completion.Resources {
					if old == ref {
						found = true
					}
				}
			}
			if !found {
				return hold()
			}
		}
		return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded, ProviderResources: resourceResults(resources, stateDeleted)}
	}
	// Validate all ledger files without interpreting this owner's existing intent
	// as a reason to reject its Cleanup. Allocation still rejects that intent.
	probe := request.Correlation
	probe.LabInstanceID = "" // valid intents never have an empty owner
	if a.cidrPriorUncertain(probe, true) {
		return hold()
	}
	slot := -1
	for index, assignment := range assignments {
		if assignment.Instance == request.LabInstanceID {
			slot = index
		}
	}
	if slot < 0 {
		if len(resources) != 0 {
			return hold()
		}
		return a.cleanupResources(ctx, resources) // e.g. a pool-exhausted preflight
	}
	// A delayed Cleanup of an earlier generation must not delete a reset owner.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return hold()
	}
	completions := []ktCIDRCompletion{}
	files := []string{slotName(slot)}
	eligible := true
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), fmt.Sprintf("intent-%03d-", slot)) {
			continue
		}
		var intent coreprovider.Correlation
		if readCIDRFile(filepath.Join(dir, entry.Name()), &intent) != nil || intent.Generation > request.Generation {
			return hold()
		}
		var completion ktCIDRCompletion
		name := completionName(slot, intent.Generation)
		if readCIDRFile(filepath.Join(dir, name), &completion) != nil {
			eligible = false
			continue
		}
		completions = append(completions, completion)
		files = append(files, entry.Name(), name)
	}
	result := a.cleanupResources(ctx, resources)
	if result.Outcome != coreprovider.OutcomeSucceeded || !eligible {
		return result
	}
	// The caller cannot accidentally free an unlisted live resource. These are
	// read-only exact-ID observations, never additional deletion candidates.
	for _, completion := range completions {
		for _, ref := range completion.Resources {
			_, exists, err := a.observeResource(ctx, ref)
			if err != nil || exists {
				return result
			}
		}
	}
	tiers, err := a.ktNetwork.tiers(ctx)
	if err != nil {
		return result
	}
	free, err := cidrPairFree(pool.Pairs[slot], tiers)
	if err != nil || !free {
		return result
	}
	retired := ktCIDRRetired{Assignment: assignments[slot], Slot: slot, Cleanup: request.Correlation, Completions: completions}
	if writeCIDRFile(dir, retiredName(request.LabInstanceID), retired) != nil {
		return unknownResult(result.ProviderResources, "CIDR release requires reconciliation")
	}
	for _, name := range files {
		if os.Remove(filepath.Join(dir, name)) != nil {
			return unknownResult(result.ProviderResources, "CIDR release requires reconciliation")
		}
	}
	directory, err := os.Open(dir)
	if err != nil {
		return unknownResult(result.ProviderResources, "CIDR release requires reconciliation")
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		return unknownResult(result.ProviderResources, "CIDR release requires reconciliation")
	}
	return result
}
