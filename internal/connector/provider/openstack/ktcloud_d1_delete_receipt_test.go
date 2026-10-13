package openstackprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudDelete404ReceiptUsesCompleteInventory(t *testing.T) {
	for _, kind := range []string{coreprovider.ResourceTypeTier, coreprovider.ResourceTypeFirewall} {
		for _, receipt := range []string{"HTTP404", "body404-number", "body404-string"} {
			for _, reality := range []string{"absent", "present", "lookup failure", "incomplete", "present second page", "second page failure", "pagination missing", "pagination null", "total missing", "offset missing"} {
				t.Run(kind+"/"+receipt+"/"+reality, func(t *testing.T) {
					deletes, lists := 0, 0
					resource, key := "network", "networkId"
					if kind == coreprovider.ResourceTypeFirewall {
						resource, key = "firewall/policy", "policyId"
					}
					a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodDelete {
							deletes++
							if r.URL.Path != "/compute/v2/nsm/"+resource+"/owned-id" {
								t.Error("delete changed exact tracked ID")
							}
							switch receipt {
							case "HTTP404":
								w.WriteHeader(404)
							case "body404-number":
								fmt.Fprint(w, `{"httpStatus":404,"error":"sensitive-provider-payload"}`)
							case "body404-string":
								fmt.Fprint(w, `{"httpStatus":"404","error":"sensitive-provider-payload"}`)
							}
							return
						}
						if r.Method != http.MethodGet || r.URL.Path != "/compute/v2/nsm/"+resource || (resource == "network" && r.URL.Query().Get("networkType") != "ALL") {
							t.Errorf("incorrect inventory request %s %s", r.Method, r.URL.Path)
							w.WriteHeader(500)
							return
						}
						lists++
						if reality == "lookup failure" || (reality == "second page failure" && r.URL.Query().Get("page") == "2") {
							w.WriteHeader(500)
							return
						}
						items := []any{}
						total, offset := 0, 0
						switch reality {
						case "present":
							items = append(items, map[string]any{key: "owned-id", "status": "ACTIVE"})
							total = 1
						case "incomplete":
							total = 1
						case "present second page", "second page failure":
							total = 2
							id := "unrelated-id"
							if r.URL.Query().Get("page") == "2" {
								offset, id = 1, "owned-id"
							}
							items = append(items, map[string]any{key: id, "status": "ACTIVE"})
						}
						pagination := map[string]int{"total": total, "offset": offset}
						body := map[string]any{"httpStatus": 200, "pagination": pagination, "data": items}
						switch reality {
						case "pagination missing":
							delete(body, "pagination")
						case "pagination null":
							body["pagination"] = nil
						case "total missing":
							delete(pagination, "total")
						case "offset missing":
							delete(pagination, "offset")
						}
						writeJSON(t, w, 200, body)
					})
					ref := coreprovider.ResourceRef{ResourceType: kind, ProviderID: "owned-id", Generation: 1, LogicalName: "owned-resource"}
					result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 1}, ProviderResources: []coreprovider.ResourceRef{ref}})
					outcome, state := coreprovider.OutcomeUnknown, stateDeleteUnknown
					if reality == "absent" {
						outcome, state = coreprovider.OutcomeSucceeded, stateDeleted
					}
					if err != nil || result.Outcome != outcome || len(result.ProviderResources) != 1 || result.ProviderResources[0].ResourceRef != ref || result.ProviderResources[0].ObservedState != state || deletes != 1 || lists == 0 {
						t.Fatalf("404 receipt reality not reconciled: err=%v outcome=%s refs=%+v deletes=%d lists=%d", err, result.Outcome, result.ProviderResources, deletes, lists)
					}
					if result.Error != nil && strings.Contains(result.Error.Message, "sensitive-provider-payload") {
						t.Fatal("raw API error exposed")
					}
				})
			}
		}
	}
}
