package app

import (
	"os/exec"
	"strings"
	"testing"
)

// D-25: Connector는 propagation-only다. 중앙 Collector/Tempo로 보내는 TracerProvider나 OTLP exporter, 그리고 그것을 조립하는 SaaS의
// tracing runtime을 Connector binary에 넣지 않는다. dependency closure로 고정한다. 이 test가 실패하면 Connector에 SDK/exporter를
// 추가한 변경이므로 D-25와 Runtime Contract(connector.observability.tracing.central_export_enabled=false)를 먼저 확인한다.
func TestConnectorBinaryHasNoOpenTelemetrySDKOrExporter(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/ktcloud4-SL/labbit-app/cmd/labbit-connector").Output()
	if err != nil {
		t.Fatalf("go list -deps error = %v", err)
	}
	forbidden := []string{
		"go.opentelemetry.io/otel/sdk",
		"go.opentelemetry.io/otel/exporters",
		"go.opentelemetry.io/proto/otlp",
		"google.golang.org/grpc",
		"github.com/ktcloud4-SL/labbit-app/internal/observability/tracing",
	}
	for _, pkg := range strings.Fields(string(out)) {
		for _, prefix := range forbidden {
			if pkg == prefix || strings.HasPrefix(pkg, prefix+"/") {
				t.Errorf("Connector binary가 %s를 포함함", pkg)
			}
		}
	}
}
