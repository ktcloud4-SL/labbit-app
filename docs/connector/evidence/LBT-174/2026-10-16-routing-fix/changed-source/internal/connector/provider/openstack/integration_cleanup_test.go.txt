package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestIntegrationCleanupConfirmsGETAbsence(t *testing.T) {
	notFound := gophercloud.ErrUnexpectedResponseCode{Actual: http.StatusNotFound}
	t.Run("delete acknowledgement can precede disappearance", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		calls := 0
		err := waitIntegrationResourceAbsent(ctx, func(context.Context) error {
			calls++
			if calls < 3 {
				return nil // GET still sees the resource after a DELETE ACK.
			}
			return notFound
		})
		if err != nil || calls != 3 {
			t.Fatalf("absence confirmation = %v after %d GETs", err, calls)
		}
	})
	t.Run("unavailable is not absent", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
			failure := gophercloud.ErrUnexpectedResponseCode{Actual: status}
			err := waitIntegrationResourceAbsent(context.Background(), func(context.Context) error { return failure })
			if err == nil {
				t.Fatalf("GET status %d was treated as absence", status)
			}
		}
	})
	t.Run("still present at deadline fails", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitIntegrationResourceAbsent(ctx, func(context.Context) error { return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("remaining resource at cancellation = %v", err)
		}
	})
}
