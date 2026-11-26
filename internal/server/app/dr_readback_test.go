package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminDRReadbackIsExactLocalAndOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maintenance")
	if os.WriteFile(path, []byte("closed"), 0600) != nil {
		t.Fatal("fixture")
	}
	gate := maintenance.New(path)
	b := wc.Binding{Scope: wc.Scope{ID: "scope", Region: "ap-northeast-3", Cluster: "cluster"}, Revision: 1, Epoch: 1, Mode: wc.PauseNew, PodID: "backend-1", BootID: uuid.New()}
	store := workercontrolfake.New(wc.State{Scope: b.Scope, Revision: 1, Epoch: 1, Mode: wc.PauseNew})
	c, e := wc.NewDurable(b, store)
	if e != nil {
		t.Fatal(e)
	}
	next := http.NotFoundHandler()
	handler := withDRReadback(next, c, gate)
	get := func(method string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/dr-control/status", nil))
		return w
	}
	w := get("GET")
	var v wc.ProcessReadback
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Binding != b || !v.AdmissionClosed || v.MaintenanceState != "closed" || v.Active["http"] != 0 {
		t.Fatal(w.Body.String())
	}
	if get("POST").Code != 405 {
		t.Fatal("mutation method accepted")
	}
	if os.WriteFile(path, []byte("bad"), 0600) != nil {
		t.Fatal("fixture")
	}
	w = get("GET")
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.AdmissionClosed || v.MaintenanceState != "NOT_VERIFIED" {
		t.Fatal("invalid file reported closed")
	}
	if err := os.WriteFile(path, []byte("open"), 0600); err != nil {
		t.Fatal(err)
	}
	release, ok := gate.Enter("http")
	if !ok {
		t.Fatal("open fixture refused")
	}
	w = get("GET")
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.MaintenanceState != "open" || v.AdmissionClosed || v.Active["http"] != 1 {
		t.Fatal("open/busy readback lost", w.Body.String())
	}
	release()
	defaultControl, _ := wc.New(wc.Active)
	w = httptest.NewRecorder()
	withDRReadback(next, defaultControl, gate).ServeHTTP(w, httptest.NewRequest("GET", "/dr-control/status", nil))
	if w.Code != 404 {
		t.Fatal("default exposed binding")
	}
}

type drStatusStore struct {
	wc.DurableStore
	report wc.Report
	err    error
}

func (s drStatusStore) Report(context.Context, wc.Scope) (wc.Report, error) { return s.report, s.err }

func TestPrivateWorkerStatusReadsDurableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := maintenance.New(path)
	b := wc.Binding{Scope: wc.Scope{ID: "scope", Region: "ap-northeast-3", Cluster: "cluster"},
		Revision: 2, Epoch: 1, Mode: wc.PauseNew, PodID: "backend-2", BootID: uuid.New()}
	at := time.Now().UTC().Truncate(time.Microsecond)
	row := wc.Report{State: wc.State{Scope: b.Scope, Revision: b.Revision, Epoch: b.Epoch, Mode: b.Mode}, ObservedAt: at}
	for _, tc := range []struct {
		name string
		edit func(*wc.Report)
		err  error
		want bool
	}{
		{name: "exact", want: true},
		{name: "database unavailable", err: wc.ErrUnavailable},
		{name: "superseded revision", edit: func(r *wc.Report) { r.State.Revision++ }},
		{name: "different mode", edit: func(r *wc.Report) { r.State.Mode = wc.Active }},
		{name: "pending resume", edit: func(r *wc.Report) { r.PendingResumes = 1 }},
		{name: "pending restriction", edit: func(r *wc.Report) { r.PendingRestrictions = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := drStatusStore{report: row, err: tc.err}
			if tc.edit != nil {
				tc.edit(&store.report)
			}
			c, err := wc.NewDurable(b, store)
			if err != nil {
				t.Fatal(err)
			}
			status, ok := durableStatus(t.Context(), c, gate)
			if ok != tc.want || (ok && (status.Binding != b || !status.ObservedAt.Equal(at))) {
				t.Fatalf("readback=%+v success=%v", status, ok)
			}
		})
	}
}

func TestStandbyStatusReportsOnlyLocalProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := maintenance.New(path)
	c, err := wc.New(wc.PauseNew)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	writeStandbyStatus(w, c, gate)
	var status standbyWorkerReadback
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil ||
		status.Scope != "STANDBY_PROCESS_ONLY" || status.Durable || status.Mode != wc.PauseNew || status.Maintenance != "closed" {
		t.Fatalf("readback=%d %s", w.Code, w.Body.String())
	}
	if err := os.WriteFile(path, []byte("unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	writeStandbyStatus(w, c, gate)
	if w.Code != http.StatusConflict || w.Body.Len() != 0 {
		t.Fatal("unknown configuration returned status")
	}
}
