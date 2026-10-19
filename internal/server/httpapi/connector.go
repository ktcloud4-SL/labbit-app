package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type Connectors interface {
	Issue(context.Context, repository.User, string) (connector.IssuedEnrollmentToken, error)
	Cancel(context.Context, repository.User, string) error
	Enroll(context.Context, connector.EnrollmentToken) (connector.EnrollmentCreated, error)
	List(context.Context, repository.User) (connector.ConnectorList, error)
	Revoke(context.Context, repository.User, string) error
}

func (a *api) connectorError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, connector.ErrForbidden):
		writeProblem(w, r, 403, codeForbidden, "Organization ADMIN 권한이 필요합니다.")
	case errors.Is(err, connector.ErrInvalidName):
		writeProblem(w, r, 400, codeInvalidRequest, "표시 이름이 올바르지 않습니다.")
	case errors.Is(err, repository.ErrNotFound):
		writeProblem(w, r, 404, codeNotFound, "리소스를 찾을 수 없습니다.")
	case errors.Is(err, connector.ErrInvalidEnrollmentToken):
		writeProblem(w, r, 401, "invalid_enrollment_token", "유효한 등록 Token이 필요합니다.")
	case errors.Is(err, connector.ErrEnrollmentTokenConsumed):
		writeProblem(w, r, 409, "enrollment_token_consumed", "이미 소비한 Token입니다. 등록된 Connector를 명시적으로 폐기하세요.")
	default:
		a.internalError(w, r, "connector_management", err)
	}
}

func (a *api) connectorAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.connectors != nil {
		return true
	}
	a.internalError(w, r, "connector_management", errors.New("connector service unavailable"))
	return false
}

// Only an exact object with one case-sensitive string field is accepted. This
// rejects duplicate keys and null as well as unknown fields and trailing JSON.
func connectorStringBody(w http.ResponseWriter, r *http.Request, key string, limit int) (string, bool) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return "", false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') || !d.More() {
		return "", false
	}
	k, err := d.Token()
	if err != nil || k != key {
		return "", false
	}
	var value *string
	if err := d.Decode(&value); err != nil || value == nil || !utf8.ValidString(*value) || utf8.RuneCountInString(*value) < 1 || utf8.RuneCountInString(*value) > limit {
		return "", false
	}
	if d.More() {
		return "", false
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') {
		return "", false
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return "", false
	}
	return *value, true
}

func (a *api) issueConnectorToken(w http.ResponseWriter, r *http.Request) {
	if !a.connectorAvailable(w, r) {
		return
	}
	name, ok := connectorStringBody(w, r, "name", 200)
	if !ok {
		writeProblem(w, r, 400, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	p, _ := PrincipalFrom(r.Context())
	issued, err := a.connectors.Issue(r.Context(), p.User, name)
	if err != nil {
		a.connectorError(w, r, err)
		return
	}
	// Explicit conversion is restricted to these one-time success responses.
	writeJSON(w, 201, map[string]any{"tokenId": issued.ID.String(), "enrollmentToken": string(issued.Token), "expiresAt": issued.ExpiresAt})
}

func (a *api) cancelConnectorToken(w http.ResponseWriter, r *http.Request) {
	if !a.connectorAvailable(w, r) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := a.connectors.Cancel(r.Context(), p.User, r.PathValue("tokenId")); err != nil {
		a.connectorError(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *api) enrollConnector(w http.ResponseWriter, r *http.Request) {
	if !a.connectorAvailable(w, r) {
		return
	}
	token, ok := connectorStringBody(w, r, "enrollmentToken", 256)
	if !ok || r.URL.RawQuery != "" {
		writeProblem(w, r, 400, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	out, err := a.connectors.Enroll(r.Context(), connector.EnrollmentToken(token))
	if err != nil {
		a.connectorError(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"connectorId": out.ConnectorID.String(), "connectorCredential": string(out.Credential)})
}

func (a *api) listConnectors(w http.ResponseWriter, r *http.Request) {
	if !a.connectorAvailable(w, r) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	out, err := a.connectors.List(r.Context(), p.User)
	if err != nil {
		a.connectorError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(out.Items))
	for _, v := range out.Items {
		items = append(items, map[string]any{"id": v.ID.String(), "name": v.Name, "registrationStatus": v.RegistrationStatus, "connectionStatus": v.ConnectionStatus, "createdAt": v.CreatedAt, "revokedAt": v.RevokedAt, "lastSeenAt": v.LastSeenAt, "lastConnectorVersion": v.LastConnectorVersion, "lastRuntimeId": v.LastRuntimeID})
	}
	writeJSON(w, 200, map[string]any{"items": items, "observedAt": out.ObservedAt})
}

func (a *api) revokeConnector(w http.ResponseWriter, r *http.Request) {
	if !a.connectorAvailable(w, r) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := a.connectors.Revoke(r.Context(), p.User, r.PathValue("connectorId")); err != nil {
		a.connectorError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
