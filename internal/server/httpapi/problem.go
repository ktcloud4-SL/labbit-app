package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// Problem code는 클라이언트가 안정적으로 분기할 수 있는 Labbit 오류 코드다.
const (
	codeInvalidRequest     = "invalid_request"
	codeInvalidCredentials = "invalid_credentials"
	codeUnauthenticated    = "unauthenticated"
	codeCSRFRejected       = "csrf_rejected"
	codeInternal           = "internal_error"
)

// problemDetails는 OpenAPI ProblemDetails(RFC 9457 application/problem+json)다.
// detail은 고정된 안전한 문구만 사용하며 내부 오류 문자열을 복사하지 않는다.
type problemDetails struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	body, err := json.Marshal(problemDetails{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

type requestIDKey struct{}

// withRequestID는 요청마다 새 correlation ID를 만든다. 클라이언트가 보낸 값은 신뢰하지 않는다.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestIDKey{}, uuid.NewString())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
