package labspec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type mappingOptionsStore struct {
	*fakeStore
	images              []repository.ImageMappingOption
	flavors             []repository.FlavorMappingOption
	orgs                []uuid.UUID
	imageErr, flavorErr error
}

func (f *mappingOptionsStore) ActiveImageMappingOptions(_ context.Context, org uuid.UUID) ([]repository.ImageMappingOption, error) {
	f.orgs = append(f.orgs, org)
	return f.images, f.imageErr
}

func (f *mappingOptionsStore) ActiveFlavorMappingOptions(_ context.Context, org uuid.UUID) ([]repository.FlavorMappingOption, error) {
	f.orgs = append(f.orgs, org)
	return f.flavors, f.flavorErr
}

func TestMappingOptionsGroupsCandidatesAndScopesQueries(t *testing.T) {
	e := newEnv()
	first := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	second := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	f := &mappingOptionsStore{fakeStore: e.store,
		images:  []repository.ImageMappingOption{{ID: e.image, ProviderConnectionID: second, DisplayName: "Ubuntu"}},
		flavors: []repository.FlavorMappingOption{{ID: e.flavor, ProviderConnectionID: first, DisplayName: "Small", VCPUs: 1, RAMMiB: 1024, DiskGiB: 0}},
	}
	got, err := NewService(f).MappingOptions(t.Context(), e.instructor)
	want := []MappingOptions{
		{ProviderConnectionID: first, Images: []ImageOption{}, Flavors: []FlavorOption{{ID: e.flavor, DisplayName: "Small", VCPUs: 1, RAMMiB: 1024, DiskGiB: 0}}},
		{ProviderConnectionID: second, Images: []ImageOption{{ID: e.image, DisplayName: "Ubuntu"}}, Flavors: []FlavorOption{}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MappingOptions = %+v, %v, want %+v", got, err, want)
	}
	if !reflect.DeepEqual(f.orgs, []uuid.UUID{e.org, e.org}) {
		t.Fatalf("query Organization = %v", f.orgs)
	}
}

func TestMappingOptionsRequiresInstructorAndPropagatesFailures(t *testing.T) {
	for _, name := range []string{"student", "admin", "instructor admin", "membership error", "image error", "flavor error", "empty"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv()
			f := &mappingOptionsStore{fakeStore: e.store}
			user := e.instructor
			var want error
			failure := errors.New("repository failure")
			switch name {
			case "student":
				user, want = e.student, ErrForbidden
			case "admin":
				user, want = e.admin, ErrForbidden
			case "instructor admin":
				user.OrganizationRole = repository.OrganizationRoleAdmin
			case "membership error":
				f.roleErr, want = failure, failure
			case "image error":
				f.imageErr, want = failure, failure
			case "flavor error":
				f.flavorErr, want = failure, failure
			}
			got, err := NewService(f).MappingOptions(t.Context(), user)
			if !errors.Is(err, want) || (want == nil && (got == nil || len(got) != 0)) {
				t.Fatalf("MappingOptions = %+v, %v, want %v", got, err, want)
			}
			if (name == "student" || name == "admin" || name == "membership error") && len(f.orgs) != 0 {
				t.Fatal("unauthorized query reached Mapping repository")
			}
		})
	}
}
