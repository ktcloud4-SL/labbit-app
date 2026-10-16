package openstackprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

// Reservations are deliberately never recycled automatically, including after
// Cleanup. An uncertain create must not make its CIDR available to another lab.
// The protected durable directory is the single-writer boundary for this pool.
// A crash leaves LOCK behind: operator reconciliation is required before reuse.
type ktCIDRPair struct {
	Lab        string `json:"lab"`
	Management string `json:"management"`
}
type ktCIDRPool struct {
	Version    int          `json:"version"`
	Project    string       `json:"project"`
	Connection string       `json:"connection"`
	Pairs      []ktCIDRPair `json:"pairs"`
}
type ktCIDRAssignment struct {
	Instance string     `json:"instance"`
	Pair     ktCIDRPair `json:"pair"`
}
type ktCIDRScope struct {
	dir     string
	slot    int
	started bool
}

func (a *Adapter) cidrPoolEnabled() bool {
	return strings.TrimSpace(a.provision.KTCloudCIDRPairs) != "" || strings.TrimSpace(a.provision.KTCloudCIDRStateDir) != ""
}

func (a *Adapter) cidrPool() (ktCIDRPool, string, error) {
	cfg := a.provision
	pool := ktCIDRPool{Version: 1, Project: cfg.ProjectID, Connection: cfg.ProviderConnectionID}
	dir := cfg.KTCloudCIDRStateDir
	if a.ktNetwork == nil || pool.Project == "" || pool.Connection == "" || !filepath.IsAbs(dir) {
		return pool, "", ErrProvisionConfig
	}
	if err := strictCIDRJSON([]byte(cfg.KTCloudCIDRPairs), &pool.Pairs); err != nil || len(pool.Pairs) < 1 || len(pool.Pairs) > 64 {
		return pool, "", ErrProvisionConfig
	}
	prefixes := []netip.Prefix{}
	for _, pair := range pool.Pairs {
		for _, value := range []string{pair.Lab, pair.Management} {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix.Bits() != 24 || prefix.Masked().String() != value {
				return pool, "", ErrProvisionConfig
			}
			for _, previous := range prefixes {
				if prefix.Overlaps(previous) {
					return pool, "", ErrProvisionConfig
				}
			}
			prefixes = append(prefixes, prefix)
		}
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return pool, "", ErrProvisionConfig
	}
	return pool, dir, nil
}

func strictCIDRJSON(data []byte, target any) error {
	if len(data) > 32768 {
		return ErrProvisionConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrProvisionConfig
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrProvisionConfig
	}
	return nil
}
func readCIDRFile(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 32768 {
		return ErrProvisionConfig
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return strictCIDRJSON(data, target)
}

// O_EXCL and fsync precede all Provider mutations. Partial files remain a HOLD;
// they are never removed on error or interpreted as an available reservation.
func writeCIDRFile(dir, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrProvisionConfig
	}
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr = directory.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func slotName(slot int) string { return fmt.Sprintf("slot-%03d.json", slot) }
func intentName(slot int, generation int64) string {
	return fmt.Sprintf("intent-%03d-%d.json", slot, generation)
}

func checkCIDRPoolFile(pool ktCIDRPool, dir string, create bool) error {
	var stored ktCIDRPool
	err := readCIDRFile(filepath.Join(dir, "pool.json"), &stored)
	if os.IsNotExist(err) && create {
		return writeCIDRFile(dir, "pool.json", pool)
	}
	if err != nil {
		return ErrProvisionConfig
	}
	expected, _ := json.Marshal(pool)
	actual, _ := json.Marshal(stored)
	if !bytes.Equal(expected, actual) {
		return ErrProvisionConfig
	}
	return nil
}
func readCIDRAssignments(pool ktCIDRPool, dir string) (map[int]ktCIDRAssignment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ErrProvisionConfig
	}
	result := map[int]ktCIDRAssignment{}
	instances := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "slot-") {
			continue
		}
		var slot int
		if _, err := fmt.Sscanf(entry.Name(), "slot-%03d.json", &slot); err != nil || slot < 0 || slot >= len(pool.Pairs) || slotName(slot) != entry.Name() {
			return nil, ErrProvisionConfig
		}
		var reservation ktCIDRAssignment
		if readCIDRFile(filepath.Join(dir, entry.Name()), &reservation) != nil || reservation.Instance == "" || reservation.Pair != pool.Pairs[slot] || instances[reservation.Instance] {
			return nil, ErrProvisionConfig
		}
		instances[reservation.Instance] = true
		result[slot] = reservation
	}
	return result, nil
}
func (a *Adapter) loadCIDRAssignment(instance string) (ktCIDRPair, error) {
	pool, dir, err := a.cidrPool()
	if err != nil {
		return ktCIDRPair{}, err
	}
	if checkCIDRPoolFile(pool, dir, false) != nil {
		return ktCIDRPair{}, ErrProvisionConfig
	}
	assignments, err := readCIDRAssignments(pool, dir)
	if err != nil {
		return ktCIDRPair{}, err
	}
	for _, assignment := range assignments {
		if assignment.Instance == instance {
			return assignment.Pair, nil
		}
	}
	return ktCIDRPair{}, ErrProvisionCheck
}

func (a *Adapter) withCIDRScope(ctx context.Context, correlation coreprovider.Correlation, reset bool, run func(*Adapter) (coreprovider.OperationResult, error)) (coreprovider.OperationResult, error) {
	hold := func() (coreprovider.OperationResult, error) {
		return unknownResult(nil, "CIDR assignment is unavailable; reconciliation required"), nil
	}
	if !validCorrelation(correlation) || (reset && correlation.Generation < 2) || (!reset && correlation.Generation != 1) {
		return hold()
	}
	pool, dir, err := a.cidrPool()
	if err != nil {
		return hold()
	}
	// Fail immediately on simultaneous operations, another process, or a stale
	// lock. No TTL, blind takeover, or provider mutation retries.
	if writeCIDRFile(dir, "LOCK", correlation) != nil {
		return hold()
	}
	defer os.Remove(filepath.Join(dir, "LOCK"))
	if ctx.Err() != nil || checkCIDRPoolFile(pool, dir, true) != nil {
		return hold()
	}
	assignments, err := readCIDRAssignments(pool, dir)
	if err != nil {
		return hold()
	}
	slot := -1
	for index, assignment := range assignments {
		if assignment.Instance == correlation.LabInstanceID {
			slot = index
		}
	}
	if slot < 0 {
		if reset {
			return hold()
		}
		tiers, err := a.ktNetwork.tiers(ctx)
		if err != nil {
			return hold()
		}
		for index, pair := range pool.Pairs {
			if _, used := assignments[index]; used {
				continue
			}
			free, err := cidrPairFree(pair, tiers)
			if err != nil {
				return hold()
			}
			if free {
				slot = index
				break
			}
		}
		if slot < 0 {
			return hold()
		}
		if writeCIDRFile(dir, slotName(slot), ktCIDRAssignment{Instance: correlation.LabInstanceID, Pair: pool.Pairs[slot]}) != nil {
			return hold()
		}
	}
	// The immutable intent survives a response loss/restart; the same generation
	// cannot issue Create/Delete again. Reset uses a new intent on the same pair.
	var previous coreprovider.Correlation
	if reset && (readCIDRFile(filepath.Join(dir, intentName(slot, correlation.Generation-1)), &previous) != nil || previous.LabInstanceID != correlation.LabInstanceID || previous.Generation != correlation.Generation-1 || previous.OperationID == "") {
		return hold()
	}
	if _, err := os.Lstat(filepath.Join(dir, intentName(slot, correlation.Generation))); !os.IsNotExist(err) {
		return hold()
	}
	scoped := *a
	scoped.provision = a.provision
	scoped.provision.LabSubnetCIDR = pool.Pairs[slot].Lab
	scoped.provision.KTCloudManagementCIDR = pool.Pairs[slot].Management
	scoped.cidrScope = &ktCIDRScope{dir: dir, slot: slot}
	return run(&scoped)
}
func cidrPairFree(pair ktCIDRPair, tiers []ktCloudTier) (bool, error) {
	lab, _ := netip.ParsePrefix(pair.Lab)
	management, _ := netip.ParsePrefix(pair.Management)
	free := true
	for _, tier := range tiers {
		if tier.CIDR == "" && tier.RefID == "" && strings.EqualFold(tier.Name, "external") {
			continue
		} // NSM external tier has no subnet.
		prefix, err := netip.ParsePrefix(tier.CIDR)
		if err != nil || !prefix.Addr().Is4() {
			return false, ErrProvisionCheck
		}
		if prefix.Overlaps(lab) || prefix.Overlaps(management) {
			free = false
		}
	}
	return free, nil
}
func (a *Adapter) startCIDRMutation(correlation coreprovider.Correlation) error {
	if a.cidrScope == nil || a.cidrScope.started {
		return nil
	}
	if err := writeCIDRFile(a.cidrScope.dir, intentName(a.cidrScope.slot, correlation.Generation), correlation); err != nil {
		return ErrProvisionCheck
	}
	a.cidrScope.started = true
	return nil
}

// Verify supplied exact IDs before they can grant quota/CIDR credit. Names are
// additional checks, never a source of ownership or deletion candidates.
func (a *Adapter) validateCIDRResetOwnership(ctx context.Context, request coreprovider.ResetRequest, resources []coreprovider.ResourceRef) error {
	for _, resource := range resources {
		switch resource.ResourceType {
		case coreprovider.ResourceTypeTier:
			tier, exists, err := a.ktNetwork.tier(ctx, resource.ProviderID)
			if err != nil {
				return ErrResourceOwnership
			}
			if !exists {
				continue
			}
			name, cidr := ktCloudLabTierName(request.LabInstanceID, resource.Generation), a.provision.LabSubnetCIDR
			if resource.LogicalName != "lab-tier" {
				name = ktCloudManagementTierName(request.LabInstanceID, resource.Generation, request.CreationSnapshot.VMs[0].VMKey)
				cidr = a.provision.KTCloudManagementCIDR
			}
			if tier.ID != resource.ProviderID || tier.Name != name || tier.CIDR != cidr || !tier.sharedKnown || tier.Shared || tier.RefID == "" {
				return ErrResourceOwnership
			}
		case coreprovider.ResourceTypeServer:
			server, err := a.lookupServer(ctx, resource.ProviderID)
			if err != nil {
				return ErrResourceOwnership
			}
			if server == nil || server.ID != resource.ProviderID || server.TenantID != a.provision.ProjectID || server.Metadata["labbit_lab_instance_id"] != request.LabInstanceID || server.Metadata["labbit_generation"] != strconv.FormatInt(resource.Generation, 10) || server.Metadata["labbit_vm_key"] != resource.LogicalName {
				return ErrResourceOwnership
			}
		}
	}
	return nil
}
