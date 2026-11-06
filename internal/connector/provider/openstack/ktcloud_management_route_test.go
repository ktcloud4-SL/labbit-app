package openstackprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"testing"
	"testing/synctest"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTManagementUserDataPreservesBytesAndDeterminism(t *testing.T) {
	a := ktTopologyAdapter(t, func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected provider request") })
	for _, original := range [][]byte{nil, []byte("#!/bin/sh\r\necho 한글"), []byte("#cloud-config\npackage_update: false\n"), []byte("#!/bin/bash\n#labbit-kt-management-route\ntrue\n")} {
		first, err := ktManagementUserData(a.provision, original)
		if err != nil {
			t.Fatal(err)
		}
		second, err := ktManagementUserData(a.provision, original)
		if err != nil || !bytes.Equal(first, second) || !bytes.Equal(ktUserStartupPart(t, first), original) {
			t.Fatal("composition changed immutable bytes or is nondeterministic")
		}
	}
}

func TestKTManagementUserDataPreservesNestedMIME(t *testing.T) {
	a := ktTopologyAdapter(t, nil)
	user := []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=inner\r\n\r\n--inner\r\nContent-Type: text/cloud-config\r\n\r\n#cloud-config\npackage_update: false\r\n--inner\r\nContent-Type: text/x-shellscript\r\n\r\n#!/bin/sh\ntrue\r\n--inner--\r\n")
	data, err := ktManagementUserData(a.provision, user)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, user) {
		t.Fatal("complete immutable MIME archive bytes changed")
	}
	msg, _ := mail.ReadMessage(bytes.NewReader(data))
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	r := multipart.NewReader(msg.Body, params["boundary"])
	_, _ = r.NextPart()
	nested, err := r.NextPart()
	if err != nil || nested.Header.Get("Content-Type") != "multipart/mixed; boundary=inner" {
		t.Fatal("nested MIME type changed")
	}
	got, _ := io.ReadAll(nested)
	original, _ := mail.ReadMessage(bytes.NewReader(user))
	want, _ := io.ReadAll(original.Body)
	if !bytes.Equal(got, want) {
		t.Fatal("nested MIME body changed")
	}
}

func TestKTManagementInvalidMIMEAndFinalOverflowRejectBeforeProvisionOrReset(t *testing.T) {
	random := make([]byte, 700)
	_, _ = rand.New(rand.NewSource(61)).Read(random)
	// Original fits the provider transport; adding mandatory management setup
	// must still be rejected before any Create or previous-generation Delete.
	if _, err := ktCloudCompressedUserData(random); err != nil {
		t.Fatal("invalid fixture")
	}
	cases := map[string][]byte{
		"combined overflow": random,
		"missing boundary":  []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed\r\n\r\nignored"),
		"missing close":     []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/x-shellscript\r\n\r\n#!/bin/sh\ntrue\n"),
		"bad nested":        []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: multipart/mixed\r\n\r\nignored\r\n--x--\r\n"),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := ktTopologyAdapter(t, func(http.ResponseWriter, *http.Request) { calls++ })
			snapshot := validSnapshot()
			snapshot.ProviderConnectionID = a.provision.ProviderConnectionID
			snapshot.InternetOutbound = false
			snapshot.StartupScript.Content = string(content)
			snapshot.StartupScript.SHA256 = fmt.Sprintf("%x", sha256.Sum256(content))
			refs := []coreprovider.ResourceRef{
				{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "old-lab", Generation: 1, LogicalName: "lab-tier"},
				{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "old-management", Generation: 1, LogicalName: "workspace:management-tier"},
				{ResourceType: coreprovider.ResourceTypeFirewall, ProviderID: "old-policy", Generation: 1, LogicalName: "workspace:management-ssh"},
				{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "old-server", Generation: 1, LogicalName: "workspace"},
				{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: "old-root", Generation: 1, LogicalName: "workspace:root-volume"},
			}
			if _, err := a.validateResetResourceSet(refs, snapshot, 2); err != nil {
				t.Fatal("invalid Reset fixture", err)
			}
			if _, err := ktManagementUserData(a.provision, content); err == nil {
				t.Fatal("invalid transport accepted")
			}
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: cidrCorrelation("reject", 1), CreationSnapshot: snapshot})
			if err != nil || result.Outcome != coreprovider.OutcomeFailed || calls != 0 {
				t.Fatalf("provision mutated: %+v %v calls=%d", result, err, calls)
			}
			reset, err := a.Reset(context.Background(), coreprovider.ResetRequest{Correlation: cidrCorrelation("reject", 2), CreationSnapshot: snapshot, ProviderResources: refs})
			if err != nil || reset.Outcome != coreprovider.OutcomeFailed || calls != 0 {
				t.Fatalf("reset changed previous generation: %+v %v calls=%d", reset, err, calls)
			}
		})
	}
}

func TestKTManagementRouteGateRequiredWithoutUserStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, snapshot := readinessFixture(t, func(next http.Handler) http.Handler { return next })
		a.provision.StartupReadyTimeout = 2 * time.Second
		probes, ptys := 0, 0
		a.startupProbe = func(context.Context, string, string) error { probes++; return errors.New("route not installed") }
		a.ptyProbe = func(context.Context, string, string) error { ptys++; return nil }
		result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: cidrCorrelation("gate", 1), CreationSnapshot: snapshot})
		if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Readiness != nil || len(result.ProviderResources) != 5 || probes == 0 || ptys != 0 {
			t.Fatalf("failed route gate was bypassed or lost created resources: %+v err=%v probes=%d ptys=%d", result, err, probes, ptys)
		}
	})
}
