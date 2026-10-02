// Package httpapi는 contracts/http/openapi.yaml의 Browser HTTP handler와 middleware다.
//
// Handler는 request 해석, Cookie, 상태 코드, Problem Details만 담당한다. SQL/pgx를 알지 못하며
// 인증 판단은 Authenticator(auth.Service)에, Class 조회와 권한 판정은 Classes(class.Service)에 위임한다.
// 인증 성공은 resource authorization이 아니므로 Class/Organization 권한은 이 package가 판단하지 않는다.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// maxLoginBodyBytes는 Login 요청 body 상한이다. Argon2id에 과도하게 긴 입력이 전달되지 않게 한다.
const maxLoginBodyBytes = 16 << 10

// Authenticator는 handler가 사용하는 인증 use case다. *auth.Service가 구현한다.
type Authenticator interface {
	Login(ctx context.Context, in auth.LoginInput) (auth.SessionToken, error)
	Authenticate(ctx context.Context, token auth.SessionToken) (auth.Principal, error)
	Logout(ctx context.Context, sessionID uuid.UUID) error
}

// Classes는 handler가 사용하는 Class 조회 use case다. *class.Service가 구현한다.
type Classes interface {
	List(ctx context.Context, user repository.User) ([]class.View, error)
	Get(ctx context.Context, user repository.User, classID string) (class.View, error)
}

// Options는 Handler 구성이다.
type Options struct {
	Auth    Authenticator
	Classes Classes
	// Terminals가 nil이면 Terminal Relay가 없는 구성으로 보고 Terminal target 조회와 TerminalSession 생성/종료를 503(terminal_unavailable)으로 응답한다.
	Terminals Terminals
	// Files가 nil이면 Workspace file use case가 없는 구성으로 보고 file Tree/Read/Save를 503(file_transport_unavailable)으로 응답한다.
	Files Files
	// PublicOrigin은 unsafe method의 trusted origin(LABBIT_PUBLIC_ORIGIN)이다. ParseOrigin 형식을 따른다.
	PublicOrigin string
	// Logger가 nil이면 로그를 남기지 않는다.
	Logger  *slog.Logger
	Metrics *observability.HTTPMetrics
}

type api struct {
	auth      Authenticator
	classes   Classes
	terminals Terminals
	files     Files
	origin    string
	logger    *slog.Logger
}

// New는 /api/v1 아래 Auth와 Class endpoint를 제공하는 http.Handler를 만든다.
func New(opts Options) (http.Handler, error) {
	if opts.Auth == nil {
		return nil, errors.New("httpapi: Authenticator가 필요합니다")
	}
	if opts.Classes == nil {
		return nil, errors.New("httpapi: Classes가 필요합니다")
	}
	origin, err := ParseOrigin(opts.PublicOrigin)
	if err != nil {
		return nil, errors.New("httpapi: PublicOrigin이 올바르지 않습니다")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	terminals := opts.Terminals
	if terminals == nil {
		terminals = unavailableTerminals{}
	}

	files := opts.Files
	if files == nil {
		files = unavailableFiles{}
	}

	a := &api{auth: opts.Auth, classes: opts.Classes, terminals: terminals, files: files, origin: origin, logger: logger}
	mux := http.NewServeMux()
	routes := make(map[string]string)
	handle := func(pattern string, handler http.Handler) {
		mux.Handle(pattern, handler)
		_, route, _ := strings.Cut(pattern, " ")
		routes[pattern] = route
	}
	handle("POST /api/v1/auth/login", http.HandlerFunc(a.login))
	handle("POST /api/v1/auth/logout", a.authenticated(http.HandlerFunc(a.logout)))
	handle("GET /api/v1/me", a.authenticated(http.HandlerFunc(a.me)))
	handle("GET /api/v1/classes", a.authenticated(http.HandlerFunc(a.listClasses)))
	handle("GET /api/v1/classes/{classId}", a.authenticated(http.HandlerFunc(a.getClass)))
	handle("GET /api/v1/lab-instances/{labInstanceId}/terminal-targets", a.authenticated(http.HandlerFunc(a.listTerminalTargets)))
	handle("POST /api/v1/lab-instances/{labInstanceId}/terminal-sessions", a.authenticated(http.HandlerFunc(a.createTerminalSession)))
	handle("DELETE /api/v1/terminal-sessions/{terminalSessionId}", a.authenticated(http.HandlerFunc(a.closeTerminalSession)))
	handle("GET /api/v1/lab-instances/{labInstanceId}/files/tree", a.authenticated(http.HandlerFunc(a.listWorkspaceFiles)))
	handle("GET /api/v1/lab-instances/{labInstanceId}/files/content", a.authenticated(http.HandlerFunc(a.readWorkspaceFile)))
	handle("PUT /api/v1/lab-instances/{labInstanceId}/files/content", a.authenticated(http.HandlerFunc(a.saveWorkspaceFile)))

	return withMetrics(opts.Metrics, mux, routes, withRequestID(noStore(a.originGuard(mux)))), nil
}

// noStore는 인증 응답이 Browser나 중간 cache에 저장되지 않게 한다.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// originGuard는 POST/PUT/PATCH/DELETE에 source origin 검증을 적용한다. Login도 포함한다.
// 인증보다 먼저 실행하므로 거절된 요청은 Session 조회나 Cookie 변경을 일으키지 않는다.
func (a *api) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !sourceAllowed(a.origin, r.Header) {
				writeProblem(w, r, http.StatusForbidden, codeCSRFRejected, "요청의 출처를 확인할 수 없어 거절했습니다.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type principalKey struct{}

// PrincipalFrom은 auth middleware가 request context에 연결한 현재 사용자를 반환한다.
// 인증된 사용자일 뿐이며 특정 Class를 다룰 권한이 있다는 뜻이 아니다.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(auth.Principal)
	return principal, ok
}

// authenticated는 유효한 Session이 없으면 401로 거절하고, 있으면 Principal을 context에 연결한다.
// 제시된 Cookie가 유효하지 않으면 stale Cookie를 함께 제거한다.
func (a *api) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, presented := presentedToken(r)
		if !presented {
			a.unauthenticated(w, r, false)
			return
		}
		principal, err := a.auth.Authenticate(r.Context(), token)
		switch {
		case err == nil:
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
		case errors.Is(err, auth.ErrUnauthenticated):
			a.unauthenticated(w, r, true)
		default:
			a.internalError(w, r, "authenticate", err)
		}
	})
}

func (a *api) unauthenticated(w http.ResponseWriter, r *http.Request, clearCookie bool) {
	if clearCookie {
		http.SetCookie(w, clearedSessionCookie())
	}
	writeProblem(w, r, http.StatusUnauthorized, codeUnauthenticated, "로그인이 필요하거나 세션이 만료되었습니다.")
}

// internalError는 응답에는 고정 문구만, log에는 correlation과 분류 정보만 남긴다.
// err나 repository.Error를 그대로 기록하지 않는다. repository.Error.Cause와 오류 문자열에는
// driver/PostgreSQL 원문이나 credential이 들어 있을 수 있다.
func (a *api) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	attrs := append([]any{"request_id", requestIDFrom(r.Context()), "operation", op}, errorClassification(err)...)
	a.logger.Error("HTTP 요청 처리 실패", attrs...)
	writeProblem(w, r, http.StatusInternalServerError, codeInternal, "요청을 처리하지 못했습니다.")
}

// errorClassification은 err를 log에 남겨도 안전한 분류 정보로 바꾼다. 오류 문자열과 Cause는 포함하지 않는다.
// repository.Error의 Op, SQLState, Constraint는 repository 계약이 운영 진단용 log metadata로 정한 값이다.
func errorClassification(err error) []any {
	var repoErr *repository.Error
	switch {
	case errors.As(err, &repoErr):
		attrs := []any{"error_kind", repoErr.Kind.String(), "repository_operation", repoErr.Op}
		if repoErr.SQLState != "" {
			attrs = append(attrs, "sqlstate", repoErr.SQLState)
		}
		if repoErr.Constraint != "" {
			attrs = append(attrs, "constraint", repoErr.Constraint)
		}
		return attrs
	case errors.Is(err, auth.ErrMalformedPasswordHash):
		return []any{"error_kind", "unusable_password_hash"}
	case errors.Is(err, class.ErrInconsistentData), errors.Is(err, terminal.ErrInconsistentData), errors.Is(err, workspacefile.ErrInconsistentData):
		return []any{"error_kind", "inconsistent_data"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return []any{"error_kind", "context"}
	default:
		return []any{"error_kind", "unclassified"}
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLoginRequest(w, r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	presented, _ := presentedToken(r)
	token, err := a.auth.Login(r.Context(), auth.LoginInput{
		Username:       req.Username,
		Password:       req.Password,
		PresentedToken: presented,
	})
	switch {
	case err == nil:
		http.SetCookie(w, newSessionCookie(token))
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, http.StatusUnauthorized, codeInvalidCredentials, "username 또는 password가 올바르지 않습니다.")
	default:
		a.internalError(w, r, "login", err)
	}
}

// decodeLoginRequest는 OpenAPI LoginRequest(username, password 필수·비어 있지 않음, 추가 필드 없음)를 읽는다.
func decodeLoginRequest(w http.ResponseWriter, r *http.Request) (loginRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return loginRequest{}, false
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBodyBytes))
	decoder.DisallowUnknownFields()
	var req loginRequest
	if err := decoder.Decode(&req); err != nil {
		return loginRequest{}, false
	}
	// JSON 값 하나만 허용한다.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return loginRequest{}, false
	}
	if req.Username == "" || req.Password == "" {
		return loginRequest{}, false
	}
	return req, true
}

type meResponse struct {
	ID               string              `json:"id"`
	Username         string              `json:"username"`
	Organization     organizationSummary `json:"organization"`
	OrganizationRole string              `json:"organizationRole"`
}

type organizationSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "me", errors.New("인증 context가 없습니다"))
		return
	}
	user := principal.User
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meResponse{
		ID:       user.ID.String(),
		Username: user.Username,
		Organization: organizationSummary{
			ID:   user.OrganizationID.String(),
			Name: user.OrganizationName,
		},
		OrganizationRole: string(user.OrganizationRole),
	})
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "logout", errors.New("인증 context가 없습니다"))
		return
	}
	switch err := a.auth.Logout(r.Context(), principal.SessionID); {
	case err == nil:
		http.SetCookie(w, clearedSessionCookie())
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, auth.ErrUnauthenticated):
		a.unauthenticated(w, r, true)
	default:
		a.internalError(w, r, "logout", err)
	}
}
