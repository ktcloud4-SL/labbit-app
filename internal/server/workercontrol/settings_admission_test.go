package workercontrol

import "testing"

func TestApprovedControlAdmissionRequiresCompletePersistentSettings(t *testing.T) {
	valid := Settings{Backend: "postgres", AdmissionPolicy: "APPROVED_CONTROL", Scope: Scope{ID: "fixture", Region: "region-fixture", Cluster: "cluster-fixture"}, Revision: 2, Epoch: 2}
	if valid.Validate("pod") != nil {
		t.Fatal("complete binding denied")
	}
	for _, change := range []func(*Settings){func(s *Settings) { s.Backend = "local" }, func(s *Settings) { s.Backend = "" }, func(s *Settings) { s.AdmissionPolicy = "unknown" }, func(s *Settings) { s.Epoch = 0 }} {
		s := valid
		change(&s)
		if s.Validate("pod") == nil {
			t.Fatal("incomplete admission fallback")
		}
	}
	s, e := LoadSettings(func(k string) string {
		if k == "LABBIT_WORKER_ADMISSION_POLICY" {
			return "APPROVED_CONTROL"
		}
		return ""
	})
	if e != nil || s.Validate("pod") == nil {
		t.Fatal("local control bypass")
	}
	if (Settings{}).Validate("") != nil {
		t.Fatal("legacy default changed")
	}
}
