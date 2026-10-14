package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	codeActiveLabExecution       = "active_lab_execution_exists"
	codeIdempotencyKeyReused     = "idempotency_key_reused"
	codeInvalidTargetStudents    = "invalid_target_students"
	codeLabSpecNotProvisionable  = "lab_spec_not_provisionable"
	codeOperationInProgress      = "operation_in_progress"
	codeLabExecutionFinished     = "lab_execution_finished"
	codeInvalidResetTarget       = "invalid_reset_target"
	operationTargetLabExecution  = "LAB_EXECUTION"
	operationTargetLabInstance   = "LAB_INSTANCE"
	maxCreateLabExecutionBytes   = 256 << 10 // 계약 수치가 아니라 과도한 입력을 막는 구현 값이다.
	maxIdempotencyKeyHeaderBytes = 4 * 255   // UTF-8 255 code point의 byte 상한이다. 길이 판정 자체는 Application이 한다.
)

// LabExecutions는 handler가 사용하는 Provision/Reset/Cleanup 접수와 LabExecution/LabInstance 조회 use case다. *labexecution.Service가 구현한다.
type LabExecutions interface {
	Provision(ctx context.Context, user repository.User, in labexecution.ProvisionInput) (labexecution.Accepted, error)
	Reset(ctx context.Context, user repository.User, in labexecution.ResetInput) (labexecution.MutationAccepted, error)
	Cleanup(ctx context.Context, user repository.User, in labexecution.CleanupInput) (labexecution.MutationAccepted, error)
	Execution(ctx context.Context, user repository.User, labExecutionID string) (labexecution.ExecutionView, error)
	Instance(ctx context.Context, user repository.User, labInstanceID string) (labexecution.InstanceView, error)
}

// operationAcceptedResponse는 OpenAPI OperationAccepted다.
type operationAcceptedResponse struct {
	OperationID string                  `json:"operationId"`
	Target      operationTargetResponse `json:"target"`
}

// labInstanceSummaryResponse는 OpenAPI LabInstanceSummary다.
type labInstanceSummaryResponse struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	Status     string `json:"status"`
	Generation int64  `json:"generation"`
}

// labInstanceResponse는 OpenAPI LabInstance다.
type labInstanceResponse struct {
	labInstanceSummaryResponse
	LabExecutionID string `json:"labExecutionId"`
}

// labExecutionResponse는 OpenAPI LabExecution이다. CreationSnapshot과 실제 Provider 값은 담지 않는다.
type labExecutionResponse struct {
	ID               string                       `json:"id"`
	ClassID          string                       `json:"classId"`
	LabSpecID        string                       `json:"labSpecId"`
	InstructorUserID string                       `json:"instructorUserId"`
	TargetUserIDs    []string                     `json:"targetUserIds"`
	Status           string                       `json:"status"`
	LabInstances     []labInstanceSummaryResponse `json:"labInstances"`
}

type createLabExecutionRequest struct {
	LabSpecID        *string  `json:"labSpecId"`
	TargetStudentIDs []string `json:"targetStudentIds"`
}

// createLabExecution은 Provision을 durable하게 접수한다. 새 접수는 202 + Location, 같은 의미의 replay는 200이다.
// Provider 작업을 기다리지 않는다.
func (a *api) createLabExecution(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok || a.labExecutions == nil {
		a.internalError(w, r, "create_lab_execution", errors.New("인증 context 또는 LabExecution use case가 없습니다"))
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	req, ok := decodeCreateLabExecution(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	accepted, err := a.labExecutions.Provision(r.Context(), principal.User, labexecution.ProvisionInput{
		ClassID: r.PathValue("classId"), LabSpecID: *req.LabSpecID, TargetStudentIDs: req.TargetStudentIDs,
		IdempotencyKey: key, RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		a.labExecutionError(w, r, "create_lab_execution", err)
		return
	}
	writeOperationAccepted(w, accepted.OperationID, operationTargetLabExecution, accepted.LabExecutionID, accepted.Replayed)
}

// resetLabInstance는 학생 LabInstance Reset을 durable하게 접수한다. 계약에 request body가 없으므로 body는 읽지 않는다.
// 새 접수는 202 + Location, 같은 의미의 replay는 200이며 Provider 작업을 기다리지 않는다.
func (a *api) resetLabInstance(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok || a.labExecutions == nil {
		a.internalError(w, r, "reset_lab_instance", errors.New("인증 context 또는 LabExecution use case가 없습니다"))
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	accepted, err := a.labExecutions.Reset(r.Context(), principal.User, labexecution.ResetInput{
		LabInstanceID: r.PathValue("labInstanceId"), IdempotencyKey: key, RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		a.labExecutionError(w, r, "reset_lab_instance", err)
		return
	}
	writeOperationAccepted(w, accepted.OperationID, operationTargetLabInstance, accepted.TargetID, accepted.Replayed)
}

// cleanupLabExecution은 LabExecution Cleanup을 durable하게 접수한다. 계약에 request body가 없으므로 body는 읽지 않는다.
// 새 접수는 202 + Location, 같은 의미의 replay는 200이며 Provider 작업을 기다리지 않는다.
func (a *api) cleanupLabExecution(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok || a.labExecutions == nil {
		a.internalError(w, r, "cleanup_lab_execution", errors.New("인증 context 또는 LabExecution use case가 없습니다"))
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	accepted, err := a.labExecutions.Cleanup(r.Context(), principal.User, labexecution.CleanupInput{
		LabExecutionID: r.PathValue("labExecutionId"), IdempotencyKey: key, RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		a.labExecutionError(w, r, "cleanup_lab_execution", err)
		return
	}
	writeOperationAccepted(w, accepted.OperationID, operationTargetLabExecution, accepted.TargetID, accepted.Replayed)
}

// idempotencyKey는 Idempotency-Key header가 정확히 하나이고 비어 있지 않은지 확인한다. 아니면 400을 쓰고 false다.
// 길이(UTF-8 code point 1~255) 판정은 Application이 한다.
func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || keys[0] == "" || len(keys[0]) > maxIdempotencyKeyHeaderBytes {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "Idempotency-Key가 필요하거나 올바르지 않습니다.")
		return "", false
	}
	return keys[0], true
}

// writeOperationAccepted는 OpenAPI OperationAccepted를 쓴다. 새 접수는 202 + Location, replay는 Location 없이 200이다.
func writeOperationAccepted(w http.ResponseWriter, operationID uuid.UUID, targetType string, targetID uuid.UUID, replayed bool) {
	body := operationAcceptedResponse{
		OperationID: operationID.String(),
		Target:      operationTargetResponse{Type: targetType, ID: targetID.String()},
	}
	if replayed {
		writeJSON(w, http.StatusOK, body)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+body.OperationID)
	writeJSON(w, http.StatusAccepted, body)
}

func (a *api) getLabExecution(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok || a.labExecutions == nil {
		a.internalError(w, r, "get_lab_execution", errors.New("인증 context 또는 LabExecution use case가 없습니다"))
		return
	}
	v, err := a.labExecutions.Execution(r.Context(), principal.User, r.PathValue("labExecutionId"))
	if err != nil {
		a.labExecutionError(w, r, "get_lab_execution", err)
		return
	}
	body := labExecutionResponse{
		ID: v.ID.String(), ClassID: v.ClassID.String(), LabSpecID: v.LabSpecID.String(), InstructorUserID: v.InstructorUserID.String(),
		Status: v.Status, TargetUserIDs: make([]string, 0, len(v.TargetUserIDs)), LabInstances: make([]labInstanceSummaryResponse, 0, len(v.LabInstances)),
	}
	for _, id := range v.TargetUserIDs {
		body.TargetUserIDs = append(body.TargetUserIDs, id.String())
	}
	for _, i := range v.LabInstances {
		body.LabInstances = append(body.LabInstances, newLabInstanceSummaryResponse(i))
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *api) getLabInstance(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok || a.labExecutions == nil {
		a.internalError(w, r, "get_lab_instance", errors.New("인증 context 또는 LabExecution use case가 없습니다"))
		return
	}
	v, err := a.labExecutions.Instance(r.Context(), principal.User, r.PathValue("labInstanceId"))
	if err != nil {
		a.labExecutionError(w, r, "get_lab_instance", err)
		return
	}
	writeJSON(w, http.StatusOK, labInstanceResponse{labInstanceSummaryResponse: newLabInstanceSummaryResponse(v), LabExecutionID: v.LabExecutionID.String()})
}

func newLabInstanceSummaryResponse(v labexecution.InstanceView) labInstanceSummaryResponse {
	return labInstanceSummaryResponse{ID: v.ID.String(), UserID: v.UserID.String(), Status: v.Status, Generation: v.Generation}
}

// labExecutionError는 typed error를 OpenAPI status와 Problem code로 바꾼다. detail은 고정 문구이며 입력 값, 대상 학생 ID,
// 실제 Provider 값, 내부 오류 문자열을 복사하지 않는다.
func (a *api) labExecutionError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, labexecution.ErrInvalidRequest):
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
	case errors.Is(err, labexecution.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
	case errors.Is(err, labexecution.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 동작을 수행할 권한이 없습니다.")
	case errors.Is(err, labexecution.ErrActiveExecution):
		writeProblem(w, r, http.StatusConflict, codeActiveLabExecution, "이 Class에 이미 진행 중인 실습 실행이 있습니다. Cleanup 후 다시 시작해 주세요.")
	case errors.Is(err, labexecution.ErrIdempotencyConflict):
		writeProblem(w, r, http.StatusConflict, codeIdempotencyKeyReused, "같은 Idempotency-Key가 다른 요청에 이미 사용되었습니다.")
	case errors.Is(err, labexecution.ErrInvalidTargets):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeInvalidTargetStudents, "대상 학생은 이 Class의 STUDENT 참여자여야 합니다.")
	case errors.Is(err, labexecution.ErrMutationInProgress):
		writeProblem(w, r, http.StatusConflict, codeOperationInProgress, "대상 실습 환경에 진행 중인 변경 작업이 있습니다. 작업이 끝난 뒤 다시 요청해 주세요.")
	case errors.Is(err, labexecution.ErrExecutionFinished):
		writeProblem(w, r, http.StatusConflict, codeLabExecutionFinished, "이미 종료된 실습 실행입니다.")
	case errors.Is(err, labexecution.ErrInvalidResetTarget):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeInvalidResetTarget, "Reset은 학생 실습 환경에만 요청할 수 있습니다.")
	case errors.Is(err, labexecution.ErrLabSpecNotProvisionable):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeLabSpecNotProvisionable, "LabSpec의 Image/Size를 현재 사용할 수 있는 Provider 값으로 확정할 수 없습니다.")
	default:
		a.internalError(w, r, op, err)
	}
}

// decodeCreateLabExecution은 OpenAPI CreateLabExecutionRequest의 형식(필수 field, 타입, additionalProperties: false, ResourceId
// minLength 1, targetStudentIds minItems 1·uniqueItems)을 확인한다. null은 어디에 있든 거절한다. 학생이 이 Class의 STUDENT인지,
// LabSpec이 있는지 같은 의미는 Application이 판단한다.
func decodeCreateLabExecution(w http.ResponseWriter, r *http.Request) (createLabExecutionRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return createLabExecutionRequest{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCreateLabExecutionBytes))
	if err != nil {
		return createLabExecutionRequest{}, false
	}
	var generic any
	if err := decodeSingle(body, &generic, false); err != nil || containsNull(generic) {
		return createLabExecutionRequest{}, false
	}
	var req createLabExecutionRequest
	if err := decodeSingle(body, &req, true); err != nil {
		return createLabExecutionRequest{}, false
	}
	if !nonEmpty(req.LabSpecID) || len(req.TargetStudentIDs) == 0 {
		return createLabExecutionRequest{}, false
	}
	seen := make(map[string]bool, len(req.TargetStudentIDs))
	for _, id := range req.TargetStudentIDs {
		if id == "" || seen[id] {
			return createLabExecutionRequest{}, false
		}
		seen[id] = true
	}
	return req, true
}
