package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/labspec"
)

func TestMappingOptionsHTTPResponse(t *testing.T) {
	id := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	fake := &fakeLabSpecs{options: []labspec.MappingOptions{{ProviderConnectionID: id,
		Images:  []labspec.ImageOption{{ID: id, DisplayName: "Ubuntu"}},
		Flavors: []labspec.FlavorOption{{ID: id, DisplayName: "Small", VCPUs: 1, RAMMiB: 1024, DiskGiB: 0}},
	}, {ProviderConnectionID: id}}}
	h := newLabSpecHarness(t, fake)
	rec := h.send(http.MethodGet, "/api/v1/lab-specs/mapping-options", "", h.signIn())
	want := `{"items":[{"providerConnectionId":"00000000-0000-4000-8000-000000000001","images":[{"id":"00000000-0000-4000-8000-000000000001","displayName":"Ubuntu"}],"flavors":[{"id":"00000000-0000-4000-8000-000000000001","displayName":"Small","vcpus":1,"ramMiB":1024,"diskGiB":0}]},{"providerConnectionId":"00000000-0000-4000-8000-000000000001","images":[],"flavors":[]}]}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMappingOptionsHTTPEmptyAndErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"empty", nil, http.StatusOK, ""},
		{"forbidden", labspec.ErrForbidden, http.StatusForbidden, codeForbidden},
		{"internal", errors.New("private provider payload"), http.StatusInternalServerError, codeInternal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newLabSpecHarness(t, &fakeLabSpecs{err: tt.err})
			rec := h.send(http.MethodGet, "/api/v1/lab-specs/mapping-options", "", h.signIn())
			if rec.Code != tt.status {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if tt.err == nil {
				if strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
					t.Fatalf("body = %s", rec.Body.String())
				}
			} else if decodeProblem(t, rec).Code != tt.code {
				t.Fatalf("body = %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String()+h.logs.String(), "private provider payload") {
				t.Fatal("internal payload exposed")
			}
		})
	}
}
