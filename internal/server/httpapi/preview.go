package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// maxPreviewBodyBytes는 PreviewSession 생성 요청 body 상한이다. 본문은 정수 하나뿐이다.
const maxPreviewBodyBytes = 16 << 10

// PreviewSession 관련 Problem code다(contracts/http/openapi.yaml createPreviewSession). 클라이언트가 안정적으로 분기할 수 있다.
// lab_instance_not_ready, workspace_target_unavailable, workspace_target_changed, connector_unavailable은 Terminal·File과 같은 의미의 기존 code를 쓴다.
const (
	codePreviewPortNotAllowed       = "preview_port_not_allowed"
	codePreviewPortRejected         = "preview_port_rejected"
	codePreviewAppNotRunning        = "preview_app_not_running"
	codePreviewTargetUnreachable    = "preview_target_unreachable"
	codePreviewOpenTimeout          = "preview_open_timeout"
	codePreviewOpenFailed           = "preview_open_failed"
	codePreviewTransportUnavailable = "preview_transport_unavailable"
	codePreviewUnavailable          = "preview_unavailable"
)

// Previews는 handler가 사용하는 PreviewSession use case다. *previewsession.Service가 구현한다.
type Previews interface {
	Create(ctx context.Context, user repository.User, in previewsession.CreateInput) (previewsession.Created, error)
	Close(ctx context.Context, user repository.User, previewSessionID string) error
}

// unavailablePreviews는 Preview Gateway가 없는 배포 구성(예: preview role 없이 api만 enabled)의 Previews다.
// 인증과 Origin 검증을 거친 뒤 503(preview_unavailable)으로 명확히 알린다.
type unavailablePreviews struct{}

func (unavailablePreviews) Create(context.Context, repository.User, previewsession.CreateInput) (previewsession.Created, error) {
	return previewsession.Created{}, previewsession.ErrUnavailable
}

func (unavailablePreviews) Close(context.Context, repository.User, string) error {
	return previewsession.ErrUnavailable
}

// createPreviewSessionRequest는 OpenAPI CreatePreviewSessionRequest다. targetPort는 검증 전 원문으로 받는다.
type createPreviewSessionRequest struct {
	TargetPort json.RawMessage `json:"targetPort"`
}

// decodeCreatePreviewSessionRequest는 OpenAPI의 required(targetPort), integer minimum 1 maximum 65535, 추가 field 없음을 그대로 확인한다.
// JSON Schema의 integer이므로 3000.0 같은 표기도 정수다. 이 검증은 port 승인이 아니다(승인은 Backend 허용 목록이 한다).
func decodeCreatePreviewSessionRequest(w http.ResponseWriter, r *http.Request) (int, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return 0, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPreviewBodyBytes))
	decoder.DisallowUnknownFields()
	var req createPreviewSessionRequest
	if err := decoder.Decode(&req); err != nil {
		return 0, false
	}
	// JSON 값 하나만 허용한다.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return 0, false
	}
	if !jsonnum.PositiveInteger(req.TargetPort) {
		return 0, false
	}
	port, _, inRange := jsonnum.Int64(req.TargetPort)
	if !inRange || port < 1 || port > 65535 {
		return 0, false
	}
	return int(port), true
}

// previewSessionResponse는 OpenAPI PreviewSession이다. previewUrl은 이 응답에서만 나가며 VM IP/port, Provider Server ID, Connector ID를 담지 않는다.
type previewSessionResponse struct {
	ID         string    `json:"id"`
	TargetPort int       `json:"targetPort"`
	PreviewURL string    `json:"previewUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (a *api) createPreviewSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "create_preview_session", errors.New("인증 context가 없습니다"))
		return
	}
	port, ok := decodeCreatePreviewSessionRequest(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	created, err := a.previews.Create(r.Context(), principal.User, previewsession.CreateInput{
		LabInstanceID: r.PathValue("labInstanceId"),
		TargetPort:    port,
		RequestID:     requestIDFrom(r.Context()),
	})
	if err != nil {
		a.previewError(w, r, "create_preview_session", err)
		return
	}
	// Cache-Control: no-store는 noStore middleware가 모든 응답에 적용한다. bootstrap credential이 든 previewUrl은 이 응답 본문에서만 나간다.
	writeJSON(w, http.StatusCreated, previewSessionResponse{
		ID:         created.ID,
		TargetPort: created.TargetPort,
		PreviewURL: created.PreviewURL,
		ExpiresAt:  created.ExpiresAt,
	})
}

func (a *api) closePreviewSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "close_preview_session", errors.New("인증 context가 없습니다"))
		return
	}
	if err := a.previews.Close(r.Context(), principal.User, r.PathValue("previewSessionId")); err != nil {
		a.previewError(w, r, "close_preview_session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewError는 PreviewSession use case의 typed error를 OpenAPI의 status와 Problem code로 바꾼다.
// detail은 고정된 문구만 쓰며 내부 오류 문자열(Connector, SSH, 저장소)을 복사하지 않는다.
func (a *api) previewError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, previewsession.ErrInvalidPort):
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
	case errors.Is(err, previewsession.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
	case errors.Is(err, previewsession.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 리소스에 접근할 권한이 없습니다.")
	case errors.Is(err, previewsession.ErrPortNotAllowed):
		writeProblem(w, r, http.StatusForbidden, codePreviewPortNotAllowed, "이 port는 Preview로 사용할 수 없습니다.")
	case errors.Is(err, previewsession.ErrPortRejected):
		writeProblem(w, r, http.StatusForbidden, codePreviewPortRejected, "실습 환경이 이 port의 Preview를 거절했습니다.")
	case errors.Is(err, previewsession.ErrLabInstanceNotReady):
		writeProblem(w, r, http.StatusConflict, codeLabInstanceNotReady, "실습 환경이 아직 사용할 수 있는 상태가 아닙니다.")
	case errors.Is(err, previewsession.ErrTargetUnavailable):
		writeProblem(w, r, http.StatusConflict, codeWorkspaceUnavailable, "Workspace VM을 지금 사용할 수 없습니다.")
	case errors.Is(err, previewsession.ErrTargetChanged):
		writeProblem(w, r, http.StatusConflict, codeWorkspaceChanged, "처리하는 동안 실습 환경이 바뀌어 Preview를 만들지 않았습니다. 다시 시도해 주세요.")
	case errors.Is(err, previewsession.ErrAppNotRunning):
		writeProblem(w, r, http.StatusBadGateway, codePreviewAppNotRunning, "Workspace에서 이 port로 실행 중인 application이 없습니다.")
	case errors.Is(err, previewsession.ErrTargetUnreachable):
		writeProblem(w, r, http.StatusGatewayTimeout, codePreviewTargetUnreachable, "Workspace VM에 도달할 수 없습니다.")
	case errors.Is(err, previewsession.ErrOpenTimeout):
		writeProblem(w, r, http.StatusGatewayTimeout, codePreviewOpenTimeout, "Preview를 시간 안에 열지 못했습니다. 잠시 후 다시 시도해 주세요.")
	case errors.Is(err, previewsession.ErrConnectorUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codeConnectorUnavailable, "실습 환경 Connector를 지금 사용할 수 없습니다.")
	case errors.Is(err, previewsession.ErrTransportUnsupported):
		writeProblem(w, r, http.StatusServiceUnavailable, codePreviewTransportUnavailable, "실습 환경 Connector가 Preview를 지원하지 않습니다.")
	case errors.Is(err, previewsession.ErrOpenFailed),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, r, http.StatusServiceUnavailable, codePreviewOpenFailed, "Preview를 열지 못했습니다. 잠시 후 다시 시도해 주세요.")
	case errors.Is(err, previewsession.ErrUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codePreviewUnavailable, "이 환경에서는 Preview를 사용할 수 없습니다.")
	default:
		a.internalError(w, r, op, err)
	}
}
