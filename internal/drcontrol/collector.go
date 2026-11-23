package drcontrol

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"time"

	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// CollectionPlan is reviewed and pinned independently of live discovery. Empty
// discovery cannot establish coverage. Every namespace Pod must be accounted
// for explicitly; excluded Pods are a reviewed scope limitation, not invisible.
type CollectionPlan struct {
	CAFile, CASHA256                                                            string
	KubectlExecutable, Kubeconfig, Context, APIServer, KubeSystemUID, Namespace string
	Pods                                                                        []PodTarget
	ExcludedPods                                                                map[string]string
}
type PodTarget struct{ Name, UID, Container, ImageID string }
type PodIdentity struct {
	Name, UID, ResourceVersion string
	Ready                      bool
	Containers                 []ContainerIdentity
}
type ContainerIdentity struct {
	Name, ImageID string
	RestartCount  int64
	Ready         bool
}
type PodSnapshot struct {
	Pods   []PodIdentity
	SHA256 string
}
type PodReader interface {
	Snapshot(context.Context) (PodSnapshot, error)
	Process(context.Context, string) (wc.ProcessReadback, string, error)
}
type WorkSnapshot struct {
	Database, User                                                                             string
	Writer                                                                                     bool
	State                                                                                      wc.State
	OpenBoundaries, PendingResumes, PendingRestrictions, Operations, Items, ProviderUnresolved int64
	ObservedAt                                                                                 time.Time
}
type WorkReader interface {
	ReadWork(context.Context, wc.Scope) (WorkSnapshot, error)
}
type SafetyAttestation struct {
	Version, Source, DBResourceID             string
	Binding                                   EvidenceBinding
	ObservedAt, ValidUntil                    time.Time
	SingleWriter, Fencing, ProviderReconciled string
	Sources                                   []Reference
}
type CollectionReport struct {
	Observation                       Observation
	PlanSHA256                        string
	SourceSHA256                      []string
	Reasons                           []string
	ExecutionGate                     string
	RecoveryAuthorized, CloudVerified bool
}

var kubeName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

func (p CollectionPlan) Validate() error {
	u, e := url.Parse(p.APIServer)
	if !filepath.IsAbs(p.KubectlExecutable) || !filepath.IsAbs(p.Kubeconfig) || !filepath.IsAbs(p.CAFile) || !sha256Pattern.MatchString(p.CASHA256) || p.Context == "" || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || p.KubeSystemUID == "" || !kubeName.MatchString(p.Namespace) || len(p.Pods) == 0 || len(p.Pods) > 64 {
		return ErrInput
	}
	seen := map[string]bool{}
	uids := map[string]bool{}
	for _, x := range p.Pods {
		if !kubeName.MatchString(x.Name) || x.UID == "" || !kubeName.MatchString(x.Container) || !regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`).MatchString(x.ImageID) || seen[x.Name] || uids[x.UID] {
			return ErrInput
		}
		seen[x.Name] = true
		uids[x.UID] = true
	}
	for name, uid := range p.ExcludedPods {
		if !kubeName.MatchString(name) || uid == "" || seen[name] || uids[uid] {
			return ErrInput
		}
		seen[name] = true
		uids[uid] = true
	}
	return nil
}
func (p CollectionPlan) SHA256() string { b, _ := json.Marshal(p); return Hash(b) }
func snapshotMatches(p CollectionPlan, s PodSnapshot) bool {
	if !sha256Pattern.MatchString(s.SHA256) || len(s.Pods) != len(p.Pods)+len(p.ExcludedPods) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range s.Pods {
		if seen[x.Name] || x.UID == "" || x.ResourceVersion == "" {
			return false
		}
		seen[x.Name] = true
		if uid, ok := p.ExcludedPods[x.Name]; ok {
			if uid != x.UID {
				return false
			}
			continue
		}
		found := false
		for _, want := range p.Pods {
			if x.Name != want.Name {
				continue
			}
			found = true
			if x.UID != want.UID || !x.Ready {
				return false
			}
			count := 0
			for _, c := range x.Containers {
				if c.Name == want.Container {
					count++
					if !c.Ready || c.ImageID != want.ImageID || c.RestartCount < 0 {
						return false
					}
				}
			}
			if count != 1 {
				return false
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Collect never turns a missing source into PASS. This reads only; mutation
// authorization remains in Execute/Resume, including a fresh DB CAS/proof check.
func Collect(ctx context.Context, p Policy, i Invocation, plan CollectionPlan, pods PodReader, work WorkReader, artifacts ArtifactReader, safety *Reference, now func() time.Time) (CollectionReport, error) {
	result := CollectionReport{ExecutionGate: "HOLD", PlanSHA256: plan.SHA256()}
	if p.CheckInvocation(i) != nil || i.Action != "resume" || plan.Validate() != nil || p.CollectionPlanSHA256 != plan.SHA256() || pods == nil || work == nil || now == nil {
		return result, ErrInput
	}
	start := now()
	until := start.Add(time.Duration(p.EvidenceMaxSeconds) * time.Second)
	if i.Request.ValidUntil.Before(until) {
		until = i.Request.ValidUntil
	}
	o := Observation{Version: Version, Source: "LIVE_READBACK", Binding: Bind(i.Request), ObservedAt: start, ValidUntil: until, AdmissionBlocked: "NOT_VERIFIED", SingleWriter: "NOT_VERIFIED", Fencing: "NOT_VERIFIED", WorkInventory: "NOT_VERIFIED", PodCoverage: "NOT_VERIFIED", WorkReconciled: "NOT_VERIFIED"}
	result.Observation = o
	if start.Before(i.Request.RequestedAt) || !start.Before(until) || ctx.Err() != nil {
		return result, ErrEvidence
	}
	first, e := pods.Snapshot(ctx)
	coverage := e == nil && snapshotMatches(plan, first)
	closed := coverage
	knownUnsafe := false
	if e == nil {
		result.SourceSHA256 = append(result.SourceSHA256, first.SHA256)
	}
	for _, target := range plan.Pods {
		v, hash, e := pods.Process(ctx, target.Name)
		if e != nil || !sha256Pattern.MatchString(hash) || v.Binding.Validate() != nil || v.Binding.Scope != p.Target || v.Binding.PodID != target.Name || v.Binding.Epoch != i.Request.ExpectedEpoch || v.Binding.Revision > i.Request.ExpectedRevision || v.Control.Unverified || !validTime(v.ObservedAt) || v.ObservedAt.Before(start) || v.ObservedAt.After(now()) {
			coverage = false
			closed = false
			continue
		}
		result.SourceSHA256 = append(result.SourceSHA256, hash)
		if !v.AdmissionClosed || v.MaintenanceState != "closed" || v.Control.InFlight != 0 {
			closed = false
			knownUnsafe = knownUnsafe || v.MaintenanceState == "open" || v.Control.InFlight > 0
		}
		for _, k := range []string{"http", "worker", "reconciliation"} {
			if _, ok := v.Active[k]; !ok {
				closed = false
			}
		}
		for _, n := range v.Active {
			if n != 0 {
				closed = false
				if n > 0 {
					knownUnsafe = true
				}
			}
		}
	}
	second, e := pods.Snapshot(ctx)
	if e != nil || !snapshotMatches(plan, second) || !reflect.DeepEqual(first.Pods, second.Pods) {
		coverage = false
	}
	if e == nil {
		result.SourceSHA256 = append(result.SourceSHA256, second.SHA256)
	}
	if coverage {
		o.PodCoverage = "PASS"
		if closed {
			o.AdmissionBlocked = "PASS"
		}
	} else {
		result.Reasons = append(result.Reasons, "POD_COVERAGE_NOT_VERIFIED")
	}
	if knownUnsafe {
		o.AdmissionBlocked = "FAIL"
	}
	w, e := work.ReadWork(ctx, p.Target)
	validWork := e == nil && w.Database == p.Database.Name && w.User == p.Database.User && w.Writer && w.State.Scope == p.Target && w.State.Revision == i.Request.ExpectedRevision && w.State.Epoch == i.Request.ExpectedEpoch && w.State.Mode == wc.PauseNew && validTime(w.ObservedAt) && !w.ObservedAt.Before(start) && !w.ObservedAt.After(now())
	for _, n := range []int64{w.OpenBoundaries, w.PendingResumes, w.PendingRestrictions, w.Operations, w.Items, w.ProviderUnresolved} {
		if n < 0 {
			validWork = false
		}
	}
	dbUnreconciled := w.OpenBoundaries > 0 || w.PendingResumes > 0 || w.PendingRestrictions > 0 || w.Operations > 0 || w.Items > 0 || w.ProviderUnresolved > 0
	if validWork {
		o.WorkInventory = "PASS"
		// A trusted DB observation can establish unfinished work independently
		// of external safety evidence. Zero counts still cannot establish PASS.
		if dbUnreconciled {
			o.WorkReconciled = "FAIL"
		}
		raw, _ := json.Marshal(w)
		result.SourceSHA256 = append(result.SourceSHA256, Hash(raw))
	} else {
		result.Reasons = append(result.Reasons, "WORK_INVENTORY_NOT_VERIFIED")
	}
	// An external, trusted fencing/Provider collector is mandatory. Its signed
	// attestation must reference retrievable immutable raw evidence, not booleans.
	if safety != nil {
		var a SafetyAttestation
		_, err := readSigned(ctx, p, artifacts, *safety, "safety-attestation", p.ObservationKeys, i.Request.Phase, &a)
		ok := err == nil && a.Version == Version && a.Source == "LIVE_READBACK" && a.Binding == Bind(i.Request) && a.DBResourceID == p.Database.ResourceID && validTime(a.ObservedAt) && validTime(a.ValidUntil) && !a.ObservedAt.Before(i.Request.RequestedAt) && !a.ObservedAt.After(now()) && now().Before(a.ValidUntil) && a.ValidUntil.After(a.ObservedAt) && a.ValidUntil.Sub(a.ObservedAt) <= time.Duration(p.EvidenceMaxSeconds)*time.Second && len(a.Sources) > 0 && len(a.Sources) <= 32
		seen := map[Reference]bool{}
		for _, ref := range a.Sources {
			if seen[ref] || p.CheckReference(ref) != nil || artifacts == nil {
				ok = false
				break
			}
			seen[ref] = true
			raw, err := artifacts.Read(ctx, ref)
			if err != nil || int64(len(raw)) != ref.Size || Hash(raw) != ref.SHA256 {
				ok = false
				break
			}
			result.SourceSHA256 = append(result.SourceSHA256, ref.SHA256)
		}
		if ok {
			if a.SingleWriter == "FAIL" {
				o.SingleWriter = "FAIL"
			}
			if a.Fencing == "FAIL" {
				o.Fencing = "FAIL"
			}
			if a.ProviderReconciled == "FAIL" {
				o.WorkReconciled = "FAIL"
			}
		}
		if ok && validWork {
			if a.SingleWriter == "PASS" || a.SingleWriter == "FAIL" {
				o.SingleWriter = a.SingleWriter
			}
			if a.Fencing == "PASS" || a.Fencing == "FAIL" {
				o.Fencing = a.Fencing
			}
			if a.ValidUntil.Before(o.ValidUntil) {
				o.ValidUntil = a.ValidUntil
			}
			if !dbUnreconciled && a.ProviderReconciled == "PASS" {
				o.WorkReconciled = "PASS"
			} else if dbUnreconciled || a.ProviderReconciled == "FAIL" {
				o.WorkReconciled = "FAIL"
			}
		} else {
			result.Reasons = append(result.Reasons, "EXTERNAL_SAFETY_NOT_VERIFIED")
		}
	} else {
		result.Reasons = append(result.Reasons, "EXTERNAL_SAFETY_NOT_VERIFIED")
	}
	if ctx.Err() != nil || !now().Before(o.ValidUntil) {
		if o.AdmissionBlocked != "FAIL" {
			o.AdmissionBlocked = "NOT_VERIFIED"
		}
		if o.PodCoverage != "FAIL" {
			o.PodCoverage = "NOT_VERIFIED"
		}
		if o.WorkInventory != "FAIL" {
			o.WorkInventory = "NOT_VERIFIED"
		}
		if o.SingleWriter != "FAIL" {
			o.SingleWriter = "NOT_VERIFIED"
		}
		if o.Fencing != "FAIL" {
			o.Fencing = "NOT_VERIFIED"
		}
		if o.WorkReconciled != "FAIL" {
			o.WorkReconciled = "NOT_VERIFIED"
		}
		result.Reasons = append(result.Reasons, "COLLECTION_EXPIRED")
	}
	result.Observation = o
	sort.Strings(result.Reasons)
	return result, nil
}
