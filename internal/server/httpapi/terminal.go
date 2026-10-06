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
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
)

// maxTerminalBodyBytes는 TerminalSession 생성 요청 body 상한이다. 본문은 VM key와 정수 둘뿐이다.
const maxTerminalBodyBytes = 16 << 10

// TerminalSession 관련 Problem code다. 클라이언트가 안정적으로 분기할 수 있다.
const (
	codeLabInstanceNotReady    = "lab_instance_not_ready"
	codeTerminalTargetInvalid  = "invalid_terminal_target"
	codeTerminalTargetNotReady = "terminal_target_unavailable"
	codeConnectorUnavailable   = "connector_unavailable"
	codeTerminalOpenFailed     = "terminal_open_failed"
	codeTerminalUnavailable    = "terminal_unavailable"
)

// Terminals는 handler가 사용하는 TerminalSession use case다. *terminal.Service가 구현한다.
type Terminals interface {
	Targets(ctx context.Context, user repository.User, labInstanceID string) (terminal.TargetCatalog, error)
	Create(ctx context.Context, user repository.User, in terminal.CreateInput) (terminal.Created, error)
	Close(ctx context.Context, user repository.User, terminalSessionID string) error
}

// unavailableTerminals는 Terminal Relay가 없는 배포 구성(예: realtime role 없이 api만 enabled)의 Terminals다.
// 인증과 Origin 검증을 거친 뒤 503(terminal_unavailable)으로 명확히 알린다.
type unavailableTerminals struct{}

func (unavailableTerminals) Targets(context.Context, repository.User, string) (terminal.TargetCatalog, error) {
	return terminal.TargetCatalog{}, terminal.ErrUnavailable
}

func (unavailableTerminals) Create(context.Context, repository.User, terminal.CreateInput) (terminal.Created, error) {
	return terminal.Created{}, terminal.ErrUnavailable
}

func (unavailableTerminals) Close(context.Context, repository.User, string) error {
	return terminal.ErrUnavailable
}

// createTerminalSessionRequest는 OpenAPI CreateTerminalSessionRequest다. cols/rows는 검증 전 원문으로 받는다.
type createTerminalSessionRequest struct {
	TargetVMKey string          `json:"targetVmKey"`
	Cols        json.RawMessage `json:"cols"`
	Rows        json.RawMessage `json:"rows"`
}

// decodeCreateTerminalSessionRequest는 OpenAPI의 required(targetVmKey, cols, rows), targetVmKey minLength 1,
// cols/rows integer minimum 1, 추가 field 없음을 그대로 확인한다. Schema에 없는 상한을 만들지 않는다.
func decodeCreateTerminalSessionRequest(w http.ResponseWriter, r *http.Request) (createTerminalSessionRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return createTerminalSessionRequest{}, false
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTerminalBodyBytes))
	decoder.DisallowUnknownFields()
	var req createTerminalSessionRequest
	if err := decoder.Decode(&req); err != nil {
		return createTerminalSessionRequest{}, false
	}
	// JSON 값 하나만 허용한다.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return createTerminalSessionRequest{}, false
	}
	if req.TargetVMKey == "" || !jsonnum.PositiveInteger(req.Cols) || !jsonnum.PositiveInteger(req.Rows) {
		return createTerminalSessionRequest{}, false
	}
	return req, true
}

// terminalSessionResponse는 OpenAPI TerminalSession이다. sessionToken은 이 응답에서만 원문으로 나간다.
type terminalSessionResponse struct {
	ID             string    `json:"id"`
	Generation     int64     `json:"generation"`
	SessionToken   string    `json:"sessionToken"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt"`
}

// terminalTargetResponse는 OpenAPI TerminalTarget이다. 이 밖의 field(Provider ID, Connector ID, 주소, 이미지·flavor)는 만들 수 없다.
type terminalTargetResponse struct {
	VMKey         string `json:"vmKey"`
	Role          string `json:"role"`
	InstanceIndex int64  `json:"instanceIndex"`
}

// terminalTargetListResponse는 OpenAPI TerminalTargetList다.
type terminalTargetListResponse struct {
	Generation     int64                    `json:"generation"`
	WorkspaceVMKey string                   `json:"workspaceVmKey"`
	Items          []terminalTargetResponse `json:"items"`
}

func (a *api) listTerminalTargets(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "list_terminal_targets", errors.New("인증 context가 없습니다"))
		return
	}
	catalog, err := a.terminals.Targets(r.Context(), principal.User, r.PathValue("labInstanceId"))
	if err != nil {
		a.terminalError(w, r, "list_terminal_targets", err)
		return
	}
	items := make([]terminalTargetResponse, 0, len(catalog.Items))
	for _, item := range catalog.Items {
		items = append(items, terminalTargetResponse{VMKey: item.VMKey, Role: item.Role, InstanceIndex: item.InstanceIndex})
	}
	writeJSON(w, http.StatusOK, terminalTargetListResponse{
		Generation:     catalog.Generation,
		WorkspaceVMKey: catalog.WorkspaceVMKey,
		Items:          items,
	})
}

func (a *api) createTerminalSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "create_terminal_session", errors.New("인증 context가 없습니다"))
		return
	}
	req, ok := decodeCreateTerminalSessionRequest(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	created, err := a.terminals.Create(r.Context(), principal.User, terminal.CreateInput{
		LabInstanceID: r.PathValue("labInstanceId"),
		TargetVMKey:   req.TargetVMKey,
		Cols:          req.Cols,
		Rows:          req.Rows,
		RequestID:     requestIDFrom(r.Context()),
	})
	if err != nil {
		a.terminalError(w, r, "create_terminal_session", err)
		return
	}
	// Cache-Control: no-store는 noStore middleware가 모든 응답에 적용한다. token 원문은 이 응답 본문에서만 나간다.
	writeJSON(w, http.StatusCreated, terminalSessionResponse{
		ID:             created.ID.String(),
		Generation:     created.Generation,
		SessionToken:   string(created.Token),
		TokenExpiresAt: created.TokenExpiresAt,
	})
}

func (a *api) closeTerminalSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "close_terminal_session", errors.New("인증 context가 없습니다"))
		return
	}
	if err := a.terminals.Close(r.Context(), principal.User, r.PathValue("terminalSessionId")); err != nil {
		a.terminalError(w, r, "close_terminal_session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// terminalError는 TerminalSession use case의 typed error를 OpenAPI의 status와 Problem code로 바꾼다.
// detail은 고정된 문구만 쓰며 내부 오류 문자열을 복사하지 않는다.
func (a *api) terminalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, terminal.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
	case errors.Is(err, terminal.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 리소스에 접근할 권한이 없습니다.")
	case errors.Is(err, terminal.ErrLabInstanceNotReady):
		writeProblem(w, r, http.StatusConflict, codeLabInstanceNotReady, "실습 환경이 아직 사용할 수 있는 상태가 아닙니다.")
	case errors.Is(err, terminal.ErrTargetUnavailable):
		writeProblem(w, r, http.StatusConflict, codeTerminalTargetNotReady, "대상 VM을 지금 사용할 수 없습니다.")
	case errors.Is(err, terminal.ErrTargetNotFound):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeTerminalTargetInvalid, "요청한 대상 VM을 찾을 수 없습니다.")
	case errors.Is(err, terminal.ErrConnectorUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codeConnectorUnavailable, "실습 환경 Connector를 지금 사용할 수 없습니다.")
	case errors.Is(err, terminal.ErrOpenFailed):
		writeProblem(w, r, http.StatusServiceUnavailable, codeTerminalOpenFailed, "Terminal을 열지 못했습니다. 잠시 후 다시 시도해 주세요.")
	case errors.Is(err, terminal.ErrUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codeTerminalUnavailable, "이 환경에서는 Terminal을 사용할 수 없습니다.")
	default:
		a.internalError(w, r, op, err)
	}
}
