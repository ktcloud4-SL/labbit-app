package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 테스트용 Credential 원문이다. 실제 Credential이 아니다.
const testCredential = "test-connector-credential-unique-7f3a"

type fakeRepo struct {
	found repository.ConnectorCredentialWithConnector
	err   error

	gotHash []byte
}

func (f *fakeRepo) ConnectorCredentialByHash(_ context.Context, hash []byte) (repository.ConnectorCredentialWithConnector, error) {
	f.gotHash = append([]byte(nil), hash...)
	return f.found, f.err
}

func activeRow() repository.ConnectorCredentialWithConnector {
	connectorID := uuid.New()
	return repository.ConnectorCredentialWithConnector{
		Credential: repository.ConnectorCredential{ID: uuid.New(), ConnectorID: connectorID},
		Connector:  repository.Connector{ID: connectorID, OrganizationID: uuid.New()},
	}
}

func TestAuthenticateReturnsConnectorIdentityFromStoredCredential(t *testing.T) {
	row := activeRow()
	repo := &fakeRepo{found: row}

	got, err := NewService(repo).Authenticate(t.Context(), Credential(testCredential))
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	want := Principal{ConnectorID: row.Connector.ID, OrganizationID: row.Connector.OrganizationID, CredentialID: row.Credential.ID}
	if got != want {
		t.Fatalf("Principal = %+v, want %+v", got, want)
	}

	// 저장소에는 원문이 아니라 CredentialDigest만 전달한다.
	digest := sha256.Sum256([]byte(testCredential))
	if !bytes.Equal(repo.gotHash, digest[:]) {
		t.Fatalf("repository lookup hash = %x, want sha256(credential) %x", repo.gotHash, digest)
	}
	if bytes.Contains(repo.gotHash, []byte(testCredential)) {
		t.Fatal("repository lookup must not receive the raw credential")
	}
}

func TestAuthenticateRejectsUnusableCredentials(t *testing.T) {
	revokedAt := time.Now()
	credentialRevoked := activeRow()
	credentialRevoked.Credential.RevokedAt = &revokedAt
	connectorRevoked := activeRow()
	connectorRevoked.Connector.RevokedAt = &revokedAt

	tests := []struct {
		name       string
		credential Credential
		repo       *fakeRepo
	}{
		{"존재하지 않는 Credential", Credential(testCredential), &fakeRepo{err: repository.ErrNotFound}},
		{"revoke된 Credential", Credential(testCredential), &fakeRepo{found: credentialRevoked}},
		{"revoke된 Connector", Credential(testCredential), &fakeRepo{found: connectorRevoked}},
		{"빈 Credential", "", &fakeRepo{found: activeRow()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo).Authenticate(t.Context(), tt.credential)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v, want ErrUnauthenticated", err)
			}
			if got != (Principal{}) {
				t.Fatalf("Principal = %+v, want zero value", got)
			}
		})
	}
}

// 저장소 장애는 인증 실패(401)가 아니라 의존성 오류다. 그렇다고 인증 성공으로 취급하지도 않는다.
func TestAuthenticateRepositoryFailureIsNotUnauthenticatedNorSuccess(t *testing.T) {
	repo := &fakeRepo{err: fmt.Errorf("%w: connection refused", repository.ErrInternal), found: activeRow()}

	got, err := NewService(repo).Authenticate(t.Context(), Credential(testCredential))
	if err == nil || errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() error = %v, want non-ErrUnauthenticated failure", err)
	}
	if got != (Principal{}) {
		t.Fatalf("Principal = %+v, want zero value on failure", got)
	}
	if strings.Contains(err.Error(), testCredential) {
		t.Fatalf("error leaks credential: %v", err)
	}
}

func TestCredentialIsRedactedEverywhere(t *testing.T) {
	credential := Credential(testCredential)

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("test", "credential", credential)
	marshaled, err := json.Marshal(struct{ Credential Credential }{credential})
	if err != nil {
		t.Fatal(err)
	}

	outputs := map[string]string{
		"%v":      fmt.Sprintf("%v", credential),
		"%+v":     fmt.Sprintf("%+v", credential),
		"%#v":     fmt.Sprintf("%#v", credential),
		"%s":      fmt.Sprintf("%s", credential),
		"slog":    logs.String(),
		"json":    string(marshaled),
		"Error()": fmt.Sprint(fmt.Errorf("wrapped: %v", credential)),
	}
	for name, out := range outputs {
		if strings.Contains(out, testCredential) {
			t.Errorf("%s output leaks credential: %s", name, out)
		}
	}
}
