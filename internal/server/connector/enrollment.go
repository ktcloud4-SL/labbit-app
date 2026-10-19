package connector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// EnrollmentTokenLifetime is a proposed implementation default, pending team review
// in the HTTP contract; D-17 does not fix this TTL.
const EnrollmentTokenLifetime = 15 * time.Minute
const OfflineAfter = 45 * time.Second

var (
	ErrForbidden               = errors.New("connector: forbidden")
	ErrInvalidEnrollmentToken  = errors.New("connector: invalid enrollment token")
	ErrEnrollmentTokenConsumed = errors.New("connector: enrollment token consumed")
	ErrInvalidName             = errors.New("connector: invalid display name")
)

type EnrollmentToken string

func (EnrollmentToken) String() string               { return redacted }
func (EnrollmentToken) GoString() string             { return redacted }
func (EnrollmentToken) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (EnrollmentToken) MarshalText() ([]byte, error) { return []byte(redacted), nil }

func newEnrollmentSecret() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func enrollmentDigest(token EnrollmentToken) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != string(token) {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

type enrollmentStore interface {
	repository.EnrollmentRepository
	repository.Transactor
}

type connectorRevoker interface{ RevokeConnector(uuid.UUID) bool }

type EnrollmentService struct {
	store    enrollmentStore
	registry connectorRevoker
	now      func() time.Time
}

// The Registry is the existing lifecycle hook, called only after a successful commit.
func NewEnrollmentService(store enrollmentStore, registry connectorRevoker, now func() time.Time) *EnrollmentService {
	if now == nil {
		now = time.Now
	}
	return &EnrollmentService{store: store, registry: registry, now: now}
}

type IssuedEnrollmentToken struct {
	ID        uuid.UUID
	Token     EnrollmentToken
	ExpiresAt time.Time
}

type EnrollmentCreated struct {
	ConnectorID uuid.UUID
	Credential  Credential
}

type ConnectorView struct {
	repository.ManagedConnector
	RegistrationStatus, ConnectionStatus string
}

type ConnectorList struct {
	Items      []ConnectorView
	ObservedAt time.Time
}

func admin(user repository.User) error {
	if user.OrganizationRole != repository.OrganizationRoleAdmin || user.ID == uuid.Nil || user.OrganizationID == uuid.Nil {
		return ErrForbidden
	}
	return nil
}

func (s *EnrollmentService) Issue(ctx context.Context, user repository.User, name string) (IssuedEnrollmentToken, error) {
	if err := admin(user); err != nil {
		return IssuedEnrollmentToken{}, err
	}
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 200 {
		return IssuedEnrollmentToken{}, ErrInvalidName
	}
	token := EnrollmentToken(newEnrollmentSecret())
	hash, _ := enrollmentDigest(token)
	at := s.now().UTC().Truncate(time.Microsecond)
	t := repository.EnrollmentToken{ID: uuid.New(), TokenHash: hash[:], OrganizationID: user.OrganizationID, IssuedBy: user.ID, Name: name, CreatedAt: at, ExpiresAt: at.Add(EnrollmentTokenLifetime)}
	if err := s.store.CreateConnectorEnrollmentToken(ctx, t); err != nil {
		return IssuedEnrollmentToken{}, err
	}
	return IssuedEnrollmentToken{ID: t.ID, Token: token, ExpiresAt: t.ExpiresAt}, nil
}

func (s *EnrollmentService) Cancel(ctx context.Context, user repository.User, id string) error {
	if err := admin(user); err != nil {
		return err
	}
	tokenID, err := uuid.Parse(id)
	if err != nil {
		return repository.ErrNotFound
	}
	return s.store.WithinTransaction(ctx, func(ctx context.Context, q repository.Repositories) error {
		t, err := q.ConnectorEnrollmentTokenForUpdate(ctx, user.OrganizationID, tokenID)
		if err != nil {
			return err
		}
		if t.ConsumedAt != nil {
			return ErrEnrollmentTokenConsumed
		}
		return q.RevokeConnectorEnrollmentToken(ctx, t.ID, s.now().UTC())
	})
}

func (s *EnrollmentService) Enroll(ctx context.Context, token EnrollmentToken) (EnrollmentCreated, error) {
	hash, ok := enrollmentDigest(token)
	if !ok {
		return EnrollmentCreated{}, ErrInvalidEnrollmentToken
	}
	var result EnrollmentCreated
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, q repository.Repositories) error {
		t, err := q.ConnectorEnrollmentTokenByHashForUpdate(ctx, hash[:])
		if errors.Is(err, repository.ErrNotFound) {
			return ErrInvalidEnrollmentToken
		}
		if err != nil {
			return err
		}
		// Check the server time after acquiring the lock, including time spent waiting.
		at := s.now().UTC()
		if t.ConsumedAt != nil || t.RevokedAt != nil || !at.Before(t.ExpiresAt) {
			return ErrInvalidEnrollmentToken
		}
		credential := Credential(newEnrollmentSecret())
		digest := CredentialDigest(string(credential))
		connectorID := uuid.New()
		if err := q.CreateEnrolledConnector(ctx, connectorID, t.OrganizationID, t.Name, at, uuid.New(), digest[:]); err != nil {
			return err
		}
		if err := q.ConsumeConnectorEnrollmentToken(ctx, t.ID, connectorID, at); err != nil {
			return err
		}
		result = EnrollmentCreated{ConnectorID: connectorID, Credential: credential}
		return nil
	})
	if err != nil {
		return EnrollmentCreated{}, err
	}
	return result, nil
}

func (s *EnrollmentService) List(ctx context.Context, user repository.User) (ConnectorList, error) {
	if err := admin(user); err != nil {
		return ConnectorList{}, err
	}
	records, err := s.store.ConnectorsByOrganization(ctx, user.OrganizationID)
	if err != nil {
		return ConnectorList{}, err
	}
	at := s.now().UTC()
	out := ConnectorList{Items: make([]ConnectorView, 0, len(records)), ObservedAt: at}
	for _, c := range records {
		v := ConnectorView{ManagedConnector: c, RegistrationStatus: "REGISTERED", ConnectionStatus: "NEVER_SEEN"}
		switch {
		case c.RevokedAt != nil:
			v.RegistrationStatus = "REVOKED"
			v.ConnectionStatus = "REVOKED"
		case c.LastSeenAt == nil:
		case at.Sub(*c.LastSeenAt) < OfflineAfter:
			v.ConnectionStatus = "ONLINE"
		default:
			v.ConnectionStatus = "OFFLINE"
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

func (s *EnrollmentService) Revoke(ctx context.Context, user repository.User, id string) error {
	if err := admin(user); err != nil {
		return err
	}
	connectorID, err := uuid.Parse(id)
	if err != nil {
		return repository.ErrNotFound
	}
	if err := s.store.WithinTransaction(ctx, func(ctx context.Context, q repository.Repositories) error {
		return q.RevokeManagedConnector(ctx, user.OrganizationID, connectorID, s.now().UTC())
	}); err != nil {
		return err
	}
	// Do not hold a DB lock while retiring Registry entries: heartbeat/HELLO own
	// the entry fence while waiting for these same parent/child DB locks.
	s.registry.RevokeConnector(connectorID)
	return nil
}

// RecordHello is called only inside Registration.IfCurrent. It never records a heartbeat.
func (s *Service) RecordHello(ctx context.Context, p Principal, version, runtimeID string) error {
	repo, ok := s.repo.(repository.ConnectorHelloRepository)
	if !ok {
		return errors.New("connector: HELLO repository unavailable")
	}
	recorded, err := repo.RecordConnectorHello(ctx, p.ConnectorID, p.CredentialID, version, runtimeID)
	if err != nil {
		return err
	}
	if !recorded {
		return ErrUnauthenticated
	}
	return nil
}
