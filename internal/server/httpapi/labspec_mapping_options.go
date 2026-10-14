package httpapi

import (
	"errors"
	"net/http"
)

type imageMappingOptionResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type flavorMappingOptionResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	VCPUs       int64  `json:"vcpus"`
	RAMMiB      int64  `json:"ramMiB"`
	DiskGiB     int64  `json:"diskGiB"`
}

type labSpecMappingOptionsResponse struct {
	ProviderConnectionID string                        `json:"providerConnectionId"`
	Images               []imageMappingOptionResponse  `json:"images"`
	Flavors              []flavorMappingOptionResponse `json:"flavors"`
}

func (a *api) listLabSpecMappingOptions(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "list_lab_spec_mapping_options", errors.New("인증 context가 없습니다"))
		return
	}
	items, err := a.labSpecs.MappingOptions(r.Context(), principal.User)
	if err != nil {
		a.labSpecError(w, r, "list_lab_spec_mapping_options", err)
		return
	}
	out := make([]labSpecMappingOptionsResponse, 0, len(items))
	for _, item := range items {
		g := labSpecMappingOptionsResponse{ProviderConnectionID: item.ProviderConnectionID.String(),
			Images:  make([]imageMappingOptionResponse, 0, len(item.Images)),
			Flavors: make([]flavorMappingOptionResponse, 0, len(item.Flavors))}
		for _, image := range item.Images {
			g.Images = append(g.Images, imageMappingOptionResponse{ID: image.ID.String(), DisplayName: image.DisplayName})
		}
		for _, flavor := range item.Flavors {
			g.Flavors = append(g.Flavors, flavorMappingOptionResponse{ID: flavor.ID.String(), DisplayName: flavor.DisplayName,
				VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB})
		}
		out = append(out, g)
	}
	writeJSON(w, http.StatusOK, struct {
		Items []labSpecMappingOptionsResponse `json:"items"`
	}{Items: out})
}
