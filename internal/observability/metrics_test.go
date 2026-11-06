package observability

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestWorkerMetricsRegistrationAndObservation(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewWorkerMetrics(reg)
	if m == nil {
		t.Fatal("NewWorkerMetrics returned nil")
	}

	// ActiveOperations starts at 0
	if got := testutil.ToFloat64(m.ActiveOperations); got != 0 {
		t.Fatalf("initial active_operations = %v, want 0", got)
	}

	// Active operations gauge inc/dec
	m.ActiveOperations.Inc()
	if got := testutil.ToFloat64(m.ActiveOperations); got != 1 {
		t.Fatalf("active_operations after Inc = %v, want 1", got)
	}
	m.ActiveOperations.Dec()
	if got := testutil.ToFloat64(m.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after Dec = %v, want 0", got)
	}

	// Counter and Histogram observation
	m.OperationsProcessed.WithLabelValues("PROVISION", "succeeded").Inc()
	m.OperationDuration.WithLabelValues("PROVISION", "succeeded").Observe(0.123)

	if got := testutil.ToFloat64(m.OperationsProcessed.WithLabelValues("PROVISION", "succeeded")); got != 1 {
		t.Fatalf("operations_processed counter = %v, want 1", got)
	}

	// Verify exposition format via testutil.GatherAndLint
	if problems, err := testutil.GatherAndLint(reg); err != nil || len(problems) > 0 {
		var msgs []string
		for _, p := range problems {
			msgs = append(msgs, p.Text)
		}
		t.Fatalf("Prometheus lint errors: %s", strings.Join(msgs, "; "))
	}
}
