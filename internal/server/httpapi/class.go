package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
)

// classResponse는 OpenAPI ClassSummary와 ClassDetail이 공통으로 필수로 요구하는 필드다.
// activeLabExecution과 myLabInstance는 optional이며 LabExecution/LabInstance 조회가 구현되기 전에는 생략한다.
type classResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	MyRole string `json:"myRole"`
}

type classListResponse struct {
	Items []classResponse `json:"items"`
}

func newClassResponse(view class.View) classResponse {
	return classResponse{ID: view.ID.String(), Name: view.Name, MyRole: string(view.MyRole)}
}

// listClasses는 현재 사용자가 ClassMembership으로 참여 중인 Class만 반환한다. 참여 Class가 없으면
// 오류가 아니라 빈 items다. 접근 판정은 class.Service가 하며 이 handler는 결과를 옮기기만 한다.
func (a *api) listClasses(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "list_classes", errors.New("인증 context가 없습니다"))
		return
	}
	views, err := a.classes.List(r.Context(), principal.User)
	if err != nil {
		a.internalError(w, r, "list_classes", err)
		return
	}

	// nil slice는 JSON null이 되므로 항상 배열로 만든다.
	items := make([]classResponse, 0, len(views))
	for _, view := range views {
		items = append(items, newClassResponse(view))
	}
	writeJSON(w, http.StatusOK, classListResponse{Items: items})
}

// getClass의 403은 Class가 존재하지만 접근할 수 없을 때, 404는 존재하지 않을 때다.
func (a *api) getClass(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "get_class", errors.New("인증 context가 없습니다"))
		return
	}
	view, err := a.classes.Get(r.Context(), principal.User, r.PathValue("classId"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, newClassResponse(view))
	case errors.Is(err, class.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 Class에 접근할 권한이 없습니다.")
	case errors.Is(err, class.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 Class를 찾을 수 없습니다.")
	default:
		a.internalError(w, r, "get_class", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
