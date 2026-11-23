package drcontrol

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

type KubeCLI struct {
	plan   CollectionPlan
	runner Runner
}

func NewKubeCLI(plan CollectionPlan, runner Runner) (*KubeCLI, error) {
	if plan.Validate() != nil || runner == nil {
		return nil, ErrInput
	}
	return &KubeCLI{plan, runner}, nil
}

// No shell/exec/mutation verb. Fixed API server, kube-system UID and protected
// kubeconfig establish the reviewed cluster boundary; no arbitrary raw paths.
func (k *KubeCLI) get(ctx context.Context, path string) ([]byte, error) {
	if k == nil || k.plan.Validate() != nil || ctx.Err() != nil {
		return nil, ErrInput
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	args := []string{"--kubeconfig", k.plan.Kubeconfig, "--context", k.plan.Context, "--server", k.plan.APIServer, "--insecure-skip-tls-verify=false", "--tls-server-name", mustHostname(k.plan.APIServer), "--certificate-authority", k.plan.CAFile, "--request-timeout=2s", "get", "--raw", path}
	b, e := k.runner.Run(c, k.plan.KubectlExecutable, args)
	if e != nil || c.Err() != nil || len(b) == 0 || len(b) > MaxDocumentBytes {
		return nil, ErrUnavailable
	}
	return b, nil
}
func (k *KubeCLI) Snapshot(ctx context.Context) (PodSnapshot, error) {
	b, e := k.get(ctx, "/api/v1/namespaces/kube-system")
	if e != nil {
		return PodSnapshot{}, e
	}
	var cluster struct{ Metadata struct{ UID string } }
	if json.Unmarshal(b, &cluster) != nil || cluster.Metadata.UID != k.plan.KubeSystemUID {
		return PodSnapshot{}, ErrEvidence
	}
	var result PodSnapshot
	var hashes []string
	token := ""
	seen := map[string]bool{}
	version := ""
	for page := 0; page < 64; page++ {
		path := "/api/v1/namespaces/" + k.plan.Namespace + "/pods?limit=100"
		if token != "" {
			path += "&continue=" + url.QueryEscape(token)
		}
		b, e := k.get(ctx, path)
		if e != nil {
			return PodSnapshot{}, e
		}
		var v struct {
			Metadata struct{ Continue, ResourceVersion string }
			Items    []struct {
				Metadata struct {
					Name, UID, ResourceVersion string
					DeletionTimestamp          *time.Time
				}
				Status struct {
					Phase             string
					Conditions        []struct{ Type, Status string }
					ContainerStatuses []struct {
						Name, ImageID string
						RestartCount  int64
						Ready         bool
					}
				}
			}
		}
		if json.Unmarshal(b, &v) != nil || v.Metadata.ResourceVersion == "" || (version != "" && version != v.Metadata.ResourceVersion) {
			return PodSnapshot{}, ErrEvidence
		}
		version = v.Metadata.ResourceVersion
		hashes = append(hashes, Hash(b))
		for _, pod := range v.Items {
			p := PodIdentity{Name: pod.Metadata.Name, UID: pod.Metadata.UID, ResourceVersion: pod.Metadata.ResourceVersion}
			for _, c := range pod.Status.Conditions {
				if c.Type == "Ready" && c.Status == "True" && pod.Status.Phase == "Running" && pod.Metadata.DeletionTimestamp == nil {
					p.Ready = true
				}
			}
			for _, c := range pod.Status.ContainerStatuses {
				p.Containers = append(p.Containers, ContainerIdentity{c.Name, c.ImageID, c.RestartCount, c.Ready})
			}
			sort.Slice(p.Containers, func(a, b int) bool { return p.Containers[a].Name < p.Containers[b].Name })
			result.Pods = append(result.Pods, p)
		}
		if len(result.Pods) > 256 {
			return PodSnapshot{}, ErrEvidence
		}
		token = v.Metadata.Continue
		if token == "" {
			sort.Slice(result.Pods, func(a, b int) bool { return result.Pods[a].Name < result.Pods[b].Name })
			raw, _ := json.Marshal(hashes)
			result.SHA256 = Hash(raw)
			return result, nil
		}
		if seen[token] {
			return PodSnapshot{}, ErrEvidence
		}
		seen[token] = true
	}
	return PodSnapshot{}, ErrEvidence
}
func (k *KubeCLI) Process(ctx context.Context, name string) (wc.ProcessReadback, string, error) {
	allowed := false
	for _, p := range k.plan.Pods {
		if p.Name == name {
			allowed = true
		}
	}
	if !allowed {
		return wc.ProcessReadback{}, "", ErrInput
	}
	b, e := k.get(ctx, "/api/v1/namespaces/"+k.plan.Namespace+"/pods/"+name+":9090/proxy/dr-control/status")
	var v wc.ProcessReadback
	if e != nil || Strict(b, &v) != nil {
		return v, "", ErrUnavailable
	}
	return v, Hash(b), nil
}

type SQLWorkReader struct{ Pool *pgxpool.Pool }

// Repeatable read, read-only transaction: inventory spans the entire selected
// DB, not an operation allowlist. Counts do not prove Provider reconciliation.
func (s SQLWorkReader) ReadWork(ctx context.Context, scope wc.Scope) (WorkSnapshot, error) {
	var w WorkSnapshot
	if s.Pool == nil || scope.Validate() != nil {
		return w, ErrInput
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, e := s.Pool.BeginTx(c, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return w, ErrUnavailable
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(c), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	var mode string
	e = tx.QueryRow(c, `SELECT current_database(),session_user,NOT pg_is_in_recovery(),s.control_id,s.region,s.cluster,s.revision,s.epoch,s.mode,
 (SELECT count(*) FROM worker_control_boundaries WHERE closed_at IS NULL),
 (SELECT count(*) FROM worker_control_resumes WHERE status='INTENT'),
 (SELECT count(*) FROM worker_control_restrictions WHERE status='INTENT'),
 (SELECT count(*) FROM operations WHERE status IN ('PENDING','RUNNING','RECONCILING')),
 (SELECT count(*) FROM operation_items WHERE status IN ('PENDING','RUNNING','RECONCILING') OR lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL),
 (SELECT count(*) FROM provider_resources WHERE lifecycle_status IN ('DELETING','MISSING') OR cleanup_error_code IS NOT NULL),clock_timestamp()
 FROM worker_control_states s WHERE s.control_id=$1`, scope.ID).Scan(&w.Database, &w.User, &w.Writer, &w.State.Scope.ID, &w.State.Scope.Region, &w.State.Scope.Cluster, &w.State.Revision, &w.State.Epoch, &mode, &w.OpenBoundaries, &w.PendingResumes, &w.PendingRestrictions, &w.Operations, &w.Items, &w.ProviderUnresolved, &w.ObservedAt)
	if e != nil {
		return WorkSnapshot{}, ErrUnavailable
	}
	w.State.Mode, e = wc.ParseMode(mode)
	if e != nil || w.State.Scope != scope || w.State.Revision < 1 || w.State.Epoch < 1 {
		return WorkSnapshot{}, ErrEvidence
	}
	w.ObservedAt = w.ObservedAt.UTC()
	if tx.Commit(c) != nil {
		return WorkSnapshot{}, ErrUnavailable
	}
	return w, nil
}

func mustHostname(server string) string {
	u, e := url.Parse(server)
	if e != nil {
		return ""
	}
	return u.Hostname()
}
