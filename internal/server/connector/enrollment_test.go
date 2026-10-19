package connector

import (
	"bytes"
	"context"
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

type enrollmentFake struct {
	repository.Repositories
	repository.Transactor
	records []repository.ManagedConnector
}

func (f enrollmentFake) ConnectorsByOrganization(context.Context, uuid.UUID) ([]repository.ManagedConnector, error) {
	return f.records, nil
}

func TestConnectorStatusHeartbeatBoundaries(t *testing.T) {
	now := time.Now().UTC()
	admin := repository.User{ID: uuid.New(), OrganizationID: uuid.New(), OrganizationRole: repository.OrganizationRoleAdmin}
	for _, tc := range []struct {
		name          string
		age           time.Duration
		seen, revoked bool
		want          string
	}{
		{name: "never seen", want: "NEVER_SEEN"},
		{name: "recent", age: OfflineAfter - time.Nanosecond, seen: true, want: "ONLINE"},
		{name: "exact boundary", age: OfflineAfter, seen: true, want: "OFFLINE"},
		{name: "old", age: time.Hour, seen: true, want: "OFFLINE"},
		{name: "revoked never seen", revoked: true, want: "REVOKED"},
		{name: "revoked recent", seen: true, revoked: true, want: "REVOKED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := repository.ManagedConnector{}
			at := now.Add(-tc.age)
			if tc.seen {
				c.LastSeenAt = &at
			}
			if tc.revoked {
				c.RevokedAt = &now
			}
			s := NewEnrollmentService(enrollmentFake{records: []repository.ManagedConnector{c}}, NewRegistry(), func() time.Time { return now })
			list, err := s.List(t.Context(), admin)
			if err != nil || list.Items[0].ConnectionStatus != tc.want {
				t.Fatalf("status != %s", tc.want)
			}
		})
	}
}

func TestConnectorManagementRequiresOrganizationAdmin(t *testing.T) {
	s := NewEnrollmentService(enrollmentFake{}, NewRegistry(), nil)
	for _, role := range []repository.OrganizationRole{repository.OrganizationRoleMember, "INSTRUCTOR", ""} {
		user := repository.User{ID: uuid.New(), OrganizationID: uuid.New(), OrganizationRole: role}
		if _, err := s.Issue(t.Context(), user, "name"); !errors.Is(err, ErrForbidden) {
			t.Fatal("issue allowed")
		}
		if err := s.Cancel(t.Context(), user, uuid.NewString()); !errors.Is(err, ErrForbidden) {
			t.Fatal("cancel allowed")
		}
		if _, err := s.List(t.Context(), user); !errors.Is(err, ErrForbidden) {
			t.Fatal("list allowed")
		}
		if err := s.Revoke(t.Context(), user, uuid.NewString()); !errors.Is(err, ErrForbidden) {
			t.Fatal("revoke allowed")
		}
	}
}

func TestEnrollmentSecretsAreOpaqueAndRedacted(t *testing.T) {
	token := EnrollmentToken(newEnrollmentSecret())
	if len(token) != 43 {
		t.Fatal("wrong token entropy length")
	}
	for _, bad := range []EnrollmentToken{token + "\n", token + "=", "bad"} {
		if _, ok := enrollmentDigest(bad); ok {
			t.Fatal("noncanonical token accepted")
		}
	}
	if _, ok := enrollmentDigest(token); !ok {
		t.Fatal("generated token rejected")
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("secret", "token", token)
	j, _ := json.Marshal(token)
	for _, text := range []string{fmt.Sprint(token), fmt.Sprintf("%#v", token), string(j), log.String()} {
		if strings.Contains(text, string(token)) {
			t.Fatal("secret exposed")
		}
	}
}
