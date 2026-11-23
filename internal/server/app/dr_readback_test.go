package app

import (
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
