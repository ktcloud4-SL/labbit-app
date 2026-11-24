package workercontrol

import "strconv"

// Settings is opt-in. Partial persistent settings must never downgrade to local.
type Settings struct {
	Backend         string
	AdmissionPolicy string
	Scope           Scope
	Revision, Epoch int64
}

func (s Settings) Enabled() bool { return s.Backend == "postgres" }
func (s Settings) Validate(podID string) error {
	if s.AdmissionPolicy != "" && s.AdmissionPolicy != "MAINTENANCE" && s.AdmissionPolicy != "APPROVED_CONTROL" {
		return ErrInvalidBinding
	}
	if s.AdmissionPolicy == "APPROVED_CONTROL" && !s.Enabled() {
		return ErrInvalidBinding
	}
	if s.Backend == "" || s.Backend == "local" {
		if s.Scope != (Scope{}) || s.Revision != 0 || s.Epoch != 0 {
			return ErrInvalidBinding
		}
		return nil
	}
	if !s.Enabled() || s.Scope.Validate() != nil || s.Revision < 1 || s.Epoch < 1 || !labelPattern.MatchString(podID) {
		return ErrInvalidBinding
	}
	return nil
}
func LoadSettings(getenv func(string) string) (Settings, error) {
	s := Settings{Backend: getenv("LABBIT_WORKER_CONTROL_BACKEND"), AdmissionPolicy: getenv("LABBIT_WORKER_ADMISSION_POLICY"), Scope: Scope{ID: getenv("LABBIT_WORKER_CONTROL_ID"), Region: getenv("LABBIT_WORKER_CONTROL_REGION"), Cluster: getenv("LABBIT_WORKER_CONTROL_CLUSTER")}}
	for key, dest := range map[string]*int64{"LABBIT_WORKER_CONTROL_REVISION": &s.Revision, "LABBIT_WORKER_CONTROL_EPOCH": &s.Epoch} {
		raw := getenv(key)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 1 {
			return Settings{}, ErrInvalidBinding
		}
		*dest = v
	}
	return s, nil
}
