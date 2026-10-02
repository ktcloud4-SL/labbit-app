package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// Workspace file 관련 Problem code다. 클라이언트가 안정적으로 분기할 수 있다(contracts/http/openapi.yaml).
const (
	codeInvalidPath            = "invalid_path"
	codeIfMatchRequired        = "if_match_required"
	codeInvalidIfMatch         = "invalid_if_match"
	codePathNotFound           = "path_not_found"
	codePathNotFile            = "path_not_file"
	codePathNotDirectory       = "path_not_directory"
	codeFilePermissionDenied   = "file_permission_denied"
	codeStaleRevision          = "stale_revision"
	codeFileTooLarge           = "file_too_large"
	codeDirectoryTooLarge      = "directory_too_large"
	codeUnsupportedEncoding    = "unsupported_encoding"
	codeBinaryContent          = "binary_content"
	codeWorkspaceUnavailable   = "workspace_target_unavailable"
	codeWorkspaceChanged       = "workspace_target_changed"
	codeFileTransportDown      = "file_transport_unavailable"
	codeFileSaveOutcomeUnknown = "file_save_outcome_unknown"
)

// maxJSONEscapeExpansion은 JSON 문자열로 인코딩한 본문이 원문 byte 수의 최대 몇 배까지 늘어날 수 있는지다(제어 문자 하나가 \u00XX 6 byte).
// 요청 body 상한을 정하는 데만 쓰며 파일 크기 한도(413)는 디코드한 본문의 UTF-8 byte 수로 판정한다.
const (
	maxJSONEscapeExpansion = 6
	jsonEnvelopeSlack      = 1 << 10
)

// Files는 handler가 사용하는 Workspace file use case다. *workspacefile.Service가 구현한다.
type Files interface {
	Tree(ctx context.Context, user repository.User, in workspacefile.TreeInput) (workspacefile.Listing, error)
	Read(ctx context.Context, user repository.User, in workspacefile.ReadInput) (workspacefile.File, error)
	Save(ctx context.Context, user repository.User, in workspacefile.SaveInput) (workspacefile.Saved, error)
	// MaxFileBytes는 Save가 받아들이는 파일 본문의 최대 byte 수다. 요청 body 상한을 정하는 데 쓴다.
	MaxFileBytes() int64
}

// unavailableFiles는 File use case가 조립되지 않은 구성의 Files다. 인증과 Origin 검증을 거친 뒤 503으로 명확히 알린다.
type unavailableFiles struct{}

func (unavailableFiles) Tree(context.Context, repository.User, workspacefile.TreeInput) (workspacefile.Listing, error) {
	return workspacefile.Listing{}, workspacefile.ErrTransportUnavailable
}

func (unavailableFiles) Read(context.Context, repository.User, workspacefile.ReadInput) (workspacefile.File, error) {
	return workspacefile.File{}, workspacefile.ErrTransportUnavailable
}

func (unavailableFiles) Save(context.Context, repository.User, workspacefile.SaveInput) (workspacefile.Saved, error) {
	return workspacefile.Saved{}, workspacefile.ErrTransportUnavailable
}

func (unavailableFiles) MaxFileBytes() int64 { return workspacefile.DefaultMaxFileBytes }

// workspaceFileEntryResponse는 OpenAPI WorkspaceFileEntry다. 이 밖의 field(uid/gid, mode, 크기, SSH metadata)는 만들 수 없다.
type workspaceFileEntryResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// workspaceFileTreeResponse는 OpenAPI WorkspaceFileTree다.
type workspaceFileTreeResponse struct {
	Path  string                       `json:"path"`
	Items []workspaceFileEntryResponse `json:"items"`
}

// workspaceFileContentResponse는 OpenAPI WorkspaceFileContent다. revision은 ETag header로 전달한다.
type workspaceFileContentResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// workspaceFileSavedResponse는 OpenAPI WorkspaceFileSaved다.
type workspaceFileSavedResponse struct {
	Path string `json:"path"`
}

// filePathParam은 query의 path parameter를 읽는다. 원문 query를 직접 parse하므로 잘못된 percent-encoding과 중복 parameter를 놓치지 않는다.
// query의 percent-decoding은 여기서 한 번만 일어나며 이후 어떤 계층도 다시 decode하지 않는다. present는 parameter가 있었는지다.
func filePathParam(r *http.Request) (value string, present, ok bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false, false
	}
	switch paths := values["path"]; len(paths) {
	case 0:
		return "", false, true
	case 1:
		return paths[0], true, true
	default:
		// 어느 것이 대상인지 알 수 없다.
		return "", false, false
	}
}

func (a *api) listWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "list_workspace_files", errors.New("인증 context가 없습니다"))
		return
	}
	raw, _, ok := filePathParam(r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	listing, err := a.files.Tree(r.Context(), principal.User, workspacefile.TreeInput{
		LabInstanceID: r.PathValue("labInstanceId"),
		Path:          raw,
		RequestID:     requestIDFrom(r.Context()),
	})
	if err != nil {
		a.fileError(w, r, "list_workspace_files", err)
		return
	}
	items := make([]workspaceFileEntryResponse, 0, len(listing.Items))
	for _, item := range listing.Items {
		items = append(items, workspaceFileEntryResponse{Name: item.Name, Path: item.Path, Kind: string(item.Kind)})
	}
	writeJSON(w, http.StatusOK, workspaceFileTreeResponse{Path: listing.Path, Items: items})
}

func (a *api) readWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "read_workspace_file", errors.New("인증 context가 없습니다"))
		return
	}
	raw, present, ok := filePathParam(r)
	if !ok || !present {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	file, err := a.files.Read(r.Context(), principal.User, workspacefile.ReadInput{
		LabInstanceID: r.PathValue("labInstanceId"),
		Path:          raw,
		RequestID:     requestIDFrom(r.Context()),
	})
	if err != nil {
		a.fileError(w, r, "read_workspace_file", err)
		return
	}
	w.Header().Set("ETag", `"`+string(file.Revision)+`"`)
	writeJSON(w, http.StatusOK, workspaceFileContentResponse{Path: file.Path, Content: file.Content})
}

func (a *api) saveWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "save_workspace_file", errors.New("인증 context가 없습니다"))
		return
	}
	raw, present, ok := filePathParam(r)
	if !ok || !present {
		writeProblem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	revision, status, code := parseIfMatch(r.Header.Values("If-Match"))
	if code != "" {
		writeProblem(w, r, status, code, "If-Match가 필요하거나 올바르지 않습니다.")
		return
	}
	content, status, code := a.decodeSaveBody(w, r)
	if code != "" {
		writeProblem(w, r, status, code, saveBodyProblemDetail(code))
		return
	}

	saved, err := a.files.Save(r.Context(), principal.User, workspacefile.SaveInput{
		LabInstanceID:   r.PathValue("labInstanceId"),
		Path:            raw,
		IfMatchRevision: revision,
		Content:         content,
		RequestID:       requestIDFrom(r.Context()),
	})
	if err != nil {
		a.fileError(w, r, "save_workspace_file", err)
		return
	}
	w.Header().Set("ETag", `"`+string(saved.Revision)+`"`)
	writeJSON(w, http.StatusOK, workspaceFileSavedResponse{Path: saved.Path})
}

// parseIfMatch는 If-Match header에서 strong entity-tag 하나의 값(따옴표를 벗긴 revision)을 꺼낸다. 없으면 400 if_match_required,
// 하나의 strong entity-tag가 아니면(`*`, 목록, weak validator, 여러 header 포함) 400 invalid_if_match다.
// revision이 Connector 계약의 형식인지는 판단하지 않는다. 그 값은 어떤 파일의 현재 revision과도 같을 수 없으므로 Application이 412로 처리한다.
func parseIfMatch(values []string) (revision string, status int, code string) {
	if len(values) == 0 {
		return "", http.StatusBadRequest, codeIfMatchRequired
	}
	if len(values) > 1 {
		return "", http.StatusBadRequest, codeInvalidIfMatch
	}
	tag := strings.TrimSpace(values[0])
	if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return "", http.StatusBadRequest, codeInvalidIfMatch
	}
	inner := tag[1 : len(tag)-1]
	for i := 0; i < len(inner); i++ {
		// etagc = %x21 / %x23-7E / obs-text. 따옴표는 두 번째 entity-tag(목록)의 신호이므로 허용하지 않는다.
		if c := inner[i]; c == 0x21 || (c >= 0x23 && c <= 0x7e) || c >= 0x80 {
			continue
		}
		return "", http.StatusBadRequest, codeInvalidIfMatch
	}
	return inner, 0, ""
}

// decodeSaveBody는 Save 요청 body(`{"content": "..."}`)를 읽는다. 문제가 있으면 status와 code를 돌려준다.
//
// body 상한은 파일 크기 한도에 JSON escape 확장을 더한 값이다. 한도를 넘는 body는 413이다. 디코드한 본문의 크기(UTF-8 byte)와
// UTF-8 text 여부, NUL은 Application이 판단한다. 여기서는 JSON이 본문을 조용히 바꾸지 않는지만 확인한다.
// Go의 JSON decoder는 유효하지 않은 UTF-8과 짝이 맞지 않는 surrogate escape를 U+FFFD로 바꾸므로 그대로 저장하면 파일 내용이 바뀐다.
// 그래서 body가 유효한 UTF-8이고 surrogate escape가 모두 짝을 이룰 때만 받아들이고, 아니면 422 unsupported_encoding이다.
func (a *api) decodeSaveBody(w http.ResponseWriter, r *http.Request) (content string, status int, code string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", http.StatusBadRequest, codeInvalidRequest
	}

	limit := a.files.MaxFileBytes()*maxJSONEscapeExpansion + jsonEnvelopeSlack
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return "", http.StatusRequestEntityTooLarge, codeFileTooLarge
		}
		return "", http.StatusBadRequest, codeInvalidRequest
	}
	if !utf8.Valid(body) || hasLoneSurrogateEscape(body) {
		return "", http.StatusUnprocessableEntity, codeUnsupportedEncoding
	}

	var req struct {
		Content *string `json:"content"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.Content == nil {
		return "", http.StatusBadRequest, codeInvalidRequest
	}
	// JSON 값 하나만 허용한다.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", http.StatusBadRequest, codeInvalidRequest
	}
	return *req.Content, 0, ""
}

func saveBodyProblemDetail(code string) string {
	switch code {
	case codeFileTooLarge:
		return "파일이 허용된 크기를 넘습니다."
	case codeUnsupportedEncoding:
		return "파일 본문은 유효한 UTF-8이어야 합니다."
	default:
		return "요청 형식이 올바르지 않습니다."
	}
}

// hasLoneSurrogateEscape는 JSON text에 짝이 맞지 않는 \uD800-\uDFFF escape가 있는지 확인한다. 높은 surrogate는 바로 뒤에 낮은 surrogate
// escape가 따라야 하고, 낮은 surrogate는 그 짝으로만 나타날 수 있다. 문자열 밖의 백슬래시는 유효한 JSON이 아니므로 decode가 거절한다.
func hasLoneSurrogateEscape(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		if i+1 >= len(body) {
			return false
		}
		if body[i+1] != 'u' {
			i++ // \\ 처럼 이스케이프된 문자를 건너뛴다.
			continue
		}
		code, ok := hex4(body, i+2)
		if !ok {
			continue // 유효한 JSON이 아니다. decode가 거절한다.
		}
		switch {
		case code >= 0xD800 && code <= 0xDBFF:
			if i+11 < len(body) && body[i+6] == '\\' && body[i+7] == 'u' {
				if low, ok := hex4(body, i+8); ok && low >= 0xDC00 && low <= 0xDFFF {
					i += 11
					continue
				}
			}
			return true
		case code >= 0xDC00 && code <= 0xDFFF:
			return true
		}
		i += 5
	}
	return false
}

// hex4는 body[at:at+4]의 16진수 4자리를 읽는다.
func hex4(body []byte, at int) (rune, bool) {
	if at < 0 || at+4 > len(body) {
		return 0, false
	}
	var v rune
	for _, c := range body[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// fileError는 Workspace file use case의 typed error를 OpenAPI의 status와 Problem code로 바꾼다.
// detail은 고정된 문구만 쓰며 경로, 파일 본문, Connector나 SSH/SFTP가 보낸 문구를 복사하지 않는다.
func (a *api) fileError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, workspacefile.ErrInvalidPath):
		writeProblem(w, r, http.StatusBadRequest, codeInvalidPath, "경로가 올바르지 않습니다.")
	case errors.Is(err, workspacefile.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
	case errors.Is(err, workspacefile.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 리소스에 접근할 권한이 없습니다.")
	case errors.Is(err, workspacefile.ErrLabInstanceNotReady):
		writeProblem(w, r, http.StatusConflict, codeLabInstanceNotReady, "실습 환경이 아직 사용할 수 있는 상태가 아닙니다.")
	case errors.Is(err, workspacefile.ErrTargetUnavailable):
		writeProblem(w, r, http.StatusConflict, codeWorkspaceUnavailable, "Workspace VM을 지금 사용할 수 없습니다.")
	case errors.Is(err, workspacefile.ErrTargetChanged):
		writeProblem(w, r, http.StatusConflict, codeWorkspaceChanged, "처리하는 동안 실습 환경이 바뀌어 결과를 사용할 수 없습니다. 다시 시도해 주세요.")
	case errors.Is(err, workspacefile.ErrPathNotFound):
		writeProblem(w, r, http.StatusNotFound, codePathNotFound, "요청한 파일이나 디렉터리를 찾을 수 없습니다.")
	case errors.Is(err, workspacefile.ErrNotAFile):
		writeProblem(w, r, http.StatusUnprocessableEntity, codePathNotFile, "일반 파일이 아닙니다.")
	case errors.Is(err, workspacefile.ErrNotADirectory):
		writeProblem(w, r, http.StatusUnprocessableEntity, codePathNotDirectory, "디렉터리가 아닙니다.")
	case errors.Is(err, workspacefile.ErrFilePermissionDenied):
		writeProblem(w, r, http.StatusForbidden, codeFilePermissionDenied, "Workspace의 파일 권한이 접근을 허용하지 않습니다.")
	case errors.Is(err, workspacefile.ErrStaleRevision):
		writeProblem(w, r, http.StatusPreconditionFailed, codeStaleRevision, "파일이 그 사이에 바뀌어 저장하지 않았습니다. 다시 읽은 뒤 저장해 주세요.")
	case errors.Is(err, workspacefile.ErrTooLarge):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, codeFileTooLarge, "파일이 허용된 크기를 넘습니다.")
	case errors.Is(err, workspacefile.ErrDirectoryTooLarge):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, codeDirectoryTooLarge, "디렉터리 목록이 너무 큽니다.")
	case errors.Is(err, workspacefile.ErrUnsupportedEncoding):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeUnsupportedEncoding, "파일 본문은 유효한 UTF-8이어야 합니다.")
	case errors.Is(err, workspacefile.ErrBinaryContent):
		writeProblem(w, r, http.StatusUnprocessableEntity, codeBinaryContent, "binary 파일은 지원하지 않습니다.")
	case errors.Is(err, workspacefile.ErrConnectorUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codeConnectorUnavailable, "실습 환경 Connector를 지금 사용할 수 없습니다.")
	case errors.Is(err, workspacefile.ErrSaveOutcomeUnknown):
		writeProblem(w, r, http.StatusServiceUnavailable, codeFileSaveOutcomeUnknown, "저장 여부를 확인하지 못했습니다. 파일을 다시 읽어 확인해 주세요.")
	case errors.Is(err, workspacefile.ErrTransportUnavailable),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, r, http.StatusServiceUnavailable, codeFileTransportDown, "Workspace 파일 시스템을 지금 사용할 수 없습니다.")
	default:
		a.internalError(w, r, op, err)
	}
}
