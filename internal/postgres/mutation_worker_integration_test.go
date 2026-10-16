//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func TestMutationWriterFencesOwnershipCorrelationAndResources(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		for _, scenario := range []string{"owner", "expiry", "generation", "connector", "connection", "operation", "type", "resource generation", "resource name", "foreign resource", "duplicate resource", "stale terminal result"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				db, f := mutationFixture(t)
				generation := int64(1)
				if kind == "RESET" {
					generation = 2
				}
				in := generationInput(f, kind, generation)
				if _, err := registerOperation(t.Context(), db, in, at(1)); err != nil {
					t.Fatal(err)
				}
				claim, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				d, err := db.store.PrepareMutation(t.Context(), claim.Lease)
				if err != nil {
					t.Fatal(err)
				}
				u := repository.ProvisionUpdate{Dispatch: d, Status: "SUCCEEDED", Stage: "VERIFYING_READINESS"}
				for _, r := range d.Resources {
					r.ObservedState = "DELETED"
					u.Resources = append(u.Resources, r)
				}
				if kind == "RESET" {
					u.Resources = append(u.Resources, repository.ProvisionResource{Type: "SERVER", LogicalName: "workspace", ProviderID: "new-generation-exact-ID", Generation: 2, ObservedState: "ACTIVE"})
				}
				switch scenario {
				case "owner":
					u.Dispatch.Lease.Owner = uuid.New()
				case "expiry":
					mustExec(t, db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.Lease.ItemID)
				case "generation":
					u.Dispatch.Generation++
				case "connector":
					u.Dispatch.ConnectorID = uuid.New()
				case "connection":
					u.Dispatch.ProviderConnectionID = uuid.New()
				case "operation":
					u.Dispatch.OperationID = uuid.New()
				case "type":
					u.Dispatch.MutationType = "PROVISION"
				case "resource generation":
					u.Resources[0].Generation++
				case "resource name":
					u.Resources[0].LogicalName = "foreign-name"
				case "foreign resource":
					// Try claiming an existing sibling's exact ID as a new reset resource,
					// or reporting that sibling's resource absent in a cleanup result.
					var id string
					if err := db.pool.QueryRow(t.Context(), `SELECT provider_id FROM provider_resources WHERE lab_instance_id=$1 LIMIT 1`, f.PeerLabInstanceID).Scan(&id); err != nil {
						t.Fatal(err)
					}
					u.Resources = append(u.Resources, repository.ProvisionResource{Type: "SERVER", ProviderID: id, LogicalName: "workspace", Generation: generation, ObservedState: "DELETED"})
				case "duplicate resource":
					u.Resources = append(u.Resources, u.Resources[0])
				case "stale terminal result":
					if err := db.store.WriteMutation(t.Context(), repository.ProvisionUpdate{Dispatch: d, Status: "FAILED", Stage: "REJECTED"}); err != nil {
						t.Fatal(err)
					}
				}
				err = db.store.WriteMutation(t.Context(), u)
				if !errors.Is(err, repository.ErrConstraintViolation) && !errors.Is(err, repository.ErrOperationLeaseLost) {
					t.Fatalf("unfenced write: %v", err)
				}
				if n := countRows(t, db, `SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1 AND (lifecycle_status<>'PRESENT' OR generation>1)`, d.LabInstanceID); n != 0 {
					t.Fatal("partial resource write escaped rollback")
				}
				li, err := db.store.LabInstanceByID(t.Context(), d.LabInstanceID)
				if err != nil || li.Generation != 1 {
					t.Fatalf("generation changed: %+v %v", li, err)
				}
			})
		}
	}
}

func TestMutationGuaranteedUnsentDefersAndRestoresGenerationAndStatus(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			db, f := mutationFixture(t)
			generation := int64(1)
			if kind == "RESET" {
				generation = 2
			}
			if _, err := registerOperation(t.Context(), db, generationInput(f, kind, generation), at(1)); err != nil {
				t.Fatal(err)
			}
			c, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			before, err := db.store.LabInstanceByID(t.Context(), c.Item.LabInstanceID)
			if err != nil {
				t.Fatal(err)
			}
			d, err := db.store.PrepareMutation(t.Context(), c.Lease)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.store.DeferUnsentMutation(t.Context(), d, time.Minute); err != nil {
				t.Fatal(err)
			}
			after, err := db.store.LabInstanceByID(t.Context(), d.LabInstanceID)
			if err != nil || after.Generation != before.Generation || after.Status != before.Status {
				t.Fatalf("unsent changed instance: %+v %v", after, err)
			}
			if _, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("ignored durable delay: %v", err)
			}
			mustExec(t, db, `UPDATE operation_items SET dispatch_not_before=clock_timestamp()-interval '1 second' WHERE id=$1`, c.Lease.ItemID)
			next, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.store.PrepareMutation(t.Context(), next.Lease); err != nil {
				t.Fatal(err)
			}
			if err := db.store.WriteMutation(t.Context(), repository.ProvisionUpdate{Dispatch: d, Status: "FAILED"}); !errors.Is(err, repository.ErrOperationLeaseLost) {
				t.Fatalf("old lease wrote after deferral: %v", err)
			}
		})
	}
}

func mutationFixture(t *testing.T) (*testDB, *terminaltest.Fixture) {
	t.Helper()
	p, _ := pendingProvision(t)
	var ids []uuid.UUID
	var execution uuid.UUID
	for range 3 {
		d := prepareProvision(t, p)
		if err := p.store.WriteProvision(t.Context(), provisionUpdate(d, "SUCCEEDED", "exact-"+d.LabInstanceID.String())); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.LabInstanceID)
		execution = d.LabExecutionID
	}
	f := &terminaltest.Fixture{OrganizationID: p.f.OrganizationID, InstructorID: p.f.InstructorID, LabExecutionID: execution, LabInstanceID: ids[0], PeerLabInstanceID: ids[1]}
	return p.testDB, f
}
