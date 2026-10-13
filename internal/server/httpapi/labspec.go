package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"

	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labspec"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	codeInvalidLabSpec = "invalid_lab_spec"

	// maxLabSpecBodyBytes는 LabSpecWrite body 상한이다. 계약 수치가 아니라 과도한 입력을 막는 구현 값이다.
	maxLabSpecBodyBytes = 1 << 20
)

// LabSpecs는 handler가 사용하는 LabSpec use case다. *labspec.Service가 구현한다.
type LabSpecs interface {
	List(ctx context.Context, user repository.User) ([]labspec.Summary, error)
	Get(ctx context.Context, user repository.User, labSpecID string) (labspec.Definition, error)
	Create(ctx context.Context, user repository.User, in labspec.Write) (labspec.Definition, error)
	Update(ctx context.Context, user repository.User, labSpecID, ifMatch string, in labspec.Write) (labspec.Definition, error)
}

// labSpecSummaryResponse는 OpenAPI LabSpecSummary다. description은 저장하지 않았으면 생략한다.
type labSpecSummaryResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	OwnerUserID string  `json:"ownerUserId"`
}

type labSpecListResponse struct {
	Items []labSpecSummaryResponse `json:"items"`
}

// labSpecResponse는 OpenAPI LabSpec이다. revision은 body가 아니라 ETag header로 전달한다.
type labSpecResponse struct {
	labSpecSummaryResponse
	VMs              []vmRoleResponse    `json:"vms"`
	WorkspaceVM      workspaceVMResponse `json:"workspaceVm"`
	InternetOutbound bool                `json:"internetOutbound"`
	StartupScript    *string             `json:"startupScript,omitempty"`
}

type vmRoleResponse struct {
	Role     string  `json:"role"`
	Name     *string `json:"name,omitempty"`
	ImageRef string  `json:"imageRef"`
	SizeRef  string  `json:"sizeRef"`
	Count    int64   `json:"count"`
}

type workspaceVMResponse struct {
	Role          string `json:"role"`
	InstanceIndex int64  `json:"instanceIndex"`
}

func newLabSpecSummaryResponse(s labspec.Summary) labSpecSummaryResponse {
	return labSpecSummaryResponse{ID: s.ID.String(), Name: s.Name, Description: s.Description, OwnerUserID: s.OwnerUserID.String()}
}

func newLabSpecResponse(d labspec.Definition) labSpecResponse {
	vms := make([]vmRoleResponse, 0, len(d.VMs))
	for _, vm := range d.VMs {
		vms = append(vms, vmRoleResponse{Role: vm.Role, Name: vm.Name, ImageRef: vm.ImageRef, SizeRef: vm.SizeRef, Count: vm.Count})
	}
	return labSpecResponse{
		labSpecSummaryResponse: newLabSpecSummaryResponse(d.Summary),
		VMs:                    vms,
		WorkspaceVM:            workspaceVMResponse{Role: d.WorkspaceVM.Role, InstanceIndex: d.WorkspaceVM.InstanceIndex},
		InternetOutbound:       d.InternetOutbound,
		StartupScript:          d.StartupScript,
	}
}

// labSpecETag는 revision의 strong entity-tag다. Browser에는 opaque 값이다.
func labSpecETag(revision int64) string {
	return `"` + strconv.FormatInt(revision, 10) + `"`
}

func (a *api) listLabSpecs(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "list_lab_specs", errors.New("인증 context가 없습니다"))
		return
	}
	items, err := a.labSpecs.List(r.Context(), principal.User)
	if err != nil {
		a.internalError(w, r, "list_lab_specs", err)
		return
	}
	out := make([]labSpecSummaryResponse, 0, len(items))
	for _, item := range items {
		out = append(out, newLabSpecSummaryResponse(item))
	}
	writeJSON(w, http.StatusOK, labSpecListResponse{Items: out})
}

func (a *api) getLabSpec(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "get_lab_spec", errors.New("인증 context가 없습니다"))
		return
	}
	def, err := a.labSpecs.Get(r.Context(), principal.User, r.PathValue("labSpecId"))
	if err != nil {
		a.labSpecError(w, r, "get_lab_spec", err)
		return
	}
	w.Header().Set("ETag", labSpecETag(def.Revision))
	writeJSON(w, http.StatusOK, newLabSpecResponse(def))
}

// createLabSpec은 정의만 저장한다. Provider 리소스나 Operation을 만들지 않는다.
func (a *api) createLabSpec(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "create_lab_spec", errors.New("인증 context가 없습니다"))
		return
	}
	in, ok := decodeLabSpecWrite(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	def, err := a.labSpecs.Create(r.Context(), principal.User, in)
	if err != nil {
		a.labSpecError(w, r, "create_lab_spec", err)
		return
	}
	w.Header().Set("Location", "/api/v1/lab-specs/"+def.ID.String())
	writeJSON(w, http.StatusCreated, newLabSpecResponse(def))
}

// updateLabSpec은 If-Match가 현재 revision일 때만 수정한다. If-Match가 없거나 strong entity-tag 하나가 아니면 400이다.
func (a *api) updateLabSpec(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "update_lab_spec", errors.New("인증 context가 없습니다"))
		return
	}
	revision, status, code := parseIfMatch(r.Header.Values("If-Match"))
	if code != "" {
		writeProblem(w, r, status, code, "If-Match가 필요하거나 올바르지 않습니다.")
		return
	}
	in, ok := decodeLabSpecWrite(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	def, err := a.labSpecs.Update(r.Context(), principal.User, r.PathValue("labSpecId"), revision, in)
	if err != nil {
		a.labSpecError(w, r, "update_lab_spec", err)
		return
	}
	w.Header().Set("ETag", labSpecETag(def.Revision))
	writeJSON(w, http.StatusOK, newLabSpecResponse(def))
}

// labSpecError는 LabSpec use case의 typed error를 OpenAPI의 status와 Problem code로 바꾼다.
// detail은 고정 문구이며 입력 값이나 내부 오류 문자열을 복사하지 않는다.
func (a *api) labSpecError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var invalid *labspec.ValidationError
	switch {
	case errors.Is(err, labspec.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 LabSpec을 찾을 수 없습니다.")
	case errors.Is(err, labspec.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 LabSpec에 대한 권한이 없습니다.")
	case errors.Is(err, labspec.ErrStaleRevision):
		writeProblem(w, r, http.StatusPreconditionFailed, codeStaleRevision, "LabSpec이 그 사이에 바뀌어 수정하지 않았습니다. 다시 조회한 뒤 수정해 주세요.")
	case errors.As(err, &invalid):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeInvalidLabSpec, labSpecInvalidDetail(invalid.Reason))
	default:
		a.internalError(w, r, op, err)
	}
}

func labSpecInvalidDetail(reason labspec.Reason) string {
	switch reason {
	case labspec.ReasonNameBlank:
		return "name은 공백만으로 이루어질 수 없습니다."
	case labspec.ReasonInvalidText:
		return "문자열에 NUL 문자를 사용할 수 없습니다."
	case labspec.ReasonVMsEmpty:
		return "vms는 하나 이상이어야 합니다."
	case labspec.ReasonRoleBlank:
		return "vms의 role은 공백만으로 이루어질 수 없습니다."
	case labspec.ReasonDuplicateRole:
		return "vms의 role은 서로 달라야 합니다."
	case labspec.ReasonCountOutOfRange:
		return "vms의 count가 허용 범위를 벗어났습니다."
	case labspec.ReasonWorkspaceRoleUnknown:
		return "workspaceVm.role은 vms에 선언한 role이어야 합니다."
	case labspec.ReasonWorkspaceIndexRange:
		return "workspaceVm.instanceIndex는 선택한 role의 count보다 작아야 합니다."
	case labspec.ReasonImageRefUnknown:
		return "imageRef가 사용할 수 있는 Image가 아닙니다."
	case labspec.ReasonSizeRefUnknown:
		return "sizeRef가 사용할 수 있는 Size가 아닙니다."
	case labspec.ReasonMixedConnections:
		return "모든 imageRef와 sizeRef는 같은 Provider 연결에 속해야 합니다."
	default:
		return "LabSpec 정의가 올바르지 않습니다."
	}
}

type labSpecWriteRequest struct {
	Name             *string              `json:"name"`
	Description      *string              `json:"description"`
	VMs              []vmRoleWriteRequest `json:"vms"`
	WorkspaceVM      *workspaceVMRequest  `json:"workspaceVm"`
	InternetOutbound *bool                `json:"internetOutbound"`
	StartupScript    *string              `json:"startupScript"`
}

type vmRoleWriteRequest struct {
	Role     *string         `json:"role"`
	Name     *string         `json:"name"`
	ImageRef *string         `json:"imageRef"`
	SizeRef  *string         `json:"sizeRef"`
	Count    json.RawMessage `json:"count"`
}

type workspaceVMRequest struct {
	Role          *string         `json:"role"`
	InstanceIndex json.RawMessage `json:"instanceIndex"`
}

// decodeLabSpecWrite는 OpenAPI LabSpecWrite의 형식(필수 field, 타입, additionalProperties: false, minLength, minItems,
// integer minimum)을 확인한다. 어느 field도 nullable이 아니므로 JSON null은 어디에 있든 거절한다.
// 의미 규칙(role 중복, workspaceVm 참조, mapping 존재 등)은 Application이 판단한다.
//
// integer는 JSON Schema 규칙(1.0도 integer, maximum 없음)으로 판정한다. Schema 상한이 없으므로 int64를 넘는 값은 형식 오류가 아니라
// 범위를 벗어난 값으로 Application에 넘겨 422가 되게 한다.
func decodeLabSpecWrite(w http.ResponseWriter, r *http.Request) (labspec.Write, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return labspec.Write{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLabSpecBodyBytes))
	if err != nil {
		return labspec.Write{}, false
	}

	var generic any
	if err := decodeSingle(body, &generic, false); err != nil || containsNull(generic) {
		return labspec.Write{}, false
	}
	var req labSpecWriteRequest
	if err := decodeSingle(body, &req, true); err != nil {
		return labspec.Write{}, false
	}
	if req.Name == nil || *req.Name == "" || len(req.VMs) == 0 || req.WorkspaceVM == nil || req.InternetOutbound == nil {
		return labspec.Write{}, false
	}

	in := labspec.Write{
		Name:             *req.Name,
		Description:      req.Description,
		InternetOutbound: *req.InternetOutbound,
		StartupScript:    req.StartupScript,
		VMs:              make([]labspec.VM, 0, len(req.VMs)),
	}
	for _, vm := range req.VMs {
		if !nonEmpty(vm.Role) || !nonEmpty(vm.ImageRef) || !nonEmpty(vm.SizeRef) || !jsonnum.PositiveInteger(vm.Count) {
			return labspec.Write{}, false
		}
		count, _, inRange := jsonnum.Int64(vm.Count)
		if !inRange {
			count = math.MaxInt64
		}
		in.VMs = append(in.VMs, labspec.VM{Role: *vm.Role, Name: vm.Name, ImageRef: *vm.ImageRef, SizeRef: *vm.SizeRef, Count: count})
	}

	if !nonEmpty(req.WorkspaceVM.Role) {
		return labspec.Write{}, false
	}
	index, valid, inRange := jsonnum.Int64(req.WorkspaceVM.InstanceIndex)
	if !valid || (inRange && index < 0) || (!inRange && bytes.HasPrefix(bytes.TrimSpace(req.WorkspaceVM.InstanceIndex), []byte("-"))) {
		return labspec.Write{}, false
	}
	if !inRange {
		index = math.MaxInt64
	}
	in.WorkspaceVM = labspec.WorkspaceVM{Role: *req.WorkspaceVM.Role, InstanceIndex: index}
	return in, true
}

// decodeSingle은 body가 JSON 값 하나일 때만 into로 decode한다.
func decodeSingle(body []byte, into any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON 값이 둘 이상입니다")
	}
	return nil
}

func containsNull(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range v {
			if containsNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if containsNull(item) {
				return true
			}
		}
	}
	return false
}

func nonEmpty(s *string) bool { return s != nil && *s != "" }
