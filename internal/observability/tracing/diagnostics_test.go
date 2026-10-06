package tracing

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassifyErrorReturnsOnlyBoundedCodes(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", fmt.Errorf("wrap: %w", context.DeadlineExceeded), codeExportTimeout},
		{"canceled", context.Canceled, codeExportCanceled},
		{"connection refused", refused, codeCollectorUnreachable},
		{"dns", &net.DNSError{Err: "no such host", Name: "collector.internal.example"}, codeCollectorUnreachable},
		{"tls unknown authority", x509.UnknownAuthorityError{}, codeTLSFailure},
		{"grpc unavailable", status.Error(codes.Unavailable, "SECRET-DETAIL"), codeCollectorUnreachable},
		{"grpc unauthenticated", status.Error(codes.Unauthenticated, "SECRET-DETAIL"), codeCollectorUnauth},
		{"grpc deadline", status.Error(codes.DeadlineExceeded, "x"), codeExportTimeout},
		{"grpc other", status.Error(codes.InvalidArgument, "x"), codeExportRejected},
		{"http rejection", errors.New("failed to send to http://collector/v1/traces: 400 Bad Request (body: SECRET-BODY)"), codeExportRejected},
		{"unknown", errors.New("anything SECRET-TEXT"), codeExportFailed},
		{"nil", nil, codeTraceInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.want {
				t.Fatalf("classifyError() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "SECRET") {
				t.Fatal("code에 원문이 섞임")
			}
		})
	}
}

// otel.Handle로 들어오는 비동기 오류는 분류 code만 기록하고, 같은 code는 interval마다 한 번만 기록한다.
func TestAsyncExportErrorsAreLoggedAsCodesAndRateLimited(t *testing.T) {
	isolateOTelEnv(t)
	var logs lockedBuffer
	release := attachDiagnostics(newTestLogger(&logs).Logger)
	defer release()

	now := time.Unix(1_000, 0)
	d := sharedDiagnostics
	d.mu.Lock()
	oldNow, oldInterval := d.now, d.interval
	d.now, d.interval = func() time.Time { return now }, time.Minute
	d.lastLogged, d.suppressed = map[string]time.Time{}, map[string]int{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.now, d.interval = oldNow, oldInterval
		d.lastLogged, d.suppressed = map[string]time.Time{}, map[string]int{}
		d.mu.Unlock()
	}()

	rejected := errors.New("failed to send to http://collector/v1/traces: 400 Bad Request (body: BODY-SENTINEL-1)")
	otel.Handle(rejected) // 이 process-global 경로는 BatchSpanProcessor가 쓰는 것과 같다.
	otel.Handle(rejected)
	otel.Handle(rejected)
	if count := strings.Count(logs.String(), `"error_code":"export_rejected"`); count != 1 {
		t.Fatalf("1분 안의 같은 오류는 한 번만 기록해야 함: %d\n%s", count, logs.String())
	}

	now = now.Add(61 * time.Second)
	otel.Handle(rejected)
	out := logs.String()
	if count := strings.Count(out, `"error_code":"export_rejected"`); count != 2 || !strings.Contains(out, `"suppressed":2`) {
		t.Fatalf("interval 뒤에는 억제한 횟수와 함께 다시 기록해야 함:\n%s", out)
	}
	mustNotContain(t, "log", out, "BODY-SENTINEL-1", "collector/v1/traces", "400 Bad Request")
}

// exporter가 입력 원문을 key/value에 실어 internal logger로 보고해도 출력은 code뿐이다. 이 sink가 process-global internal logger로
// 설치되어 있다는 것은 TestMalformedHeaderEnvironmentDoesNotLeakAndDisablesExport가 실제 exporter 경로로 확인한다.
func TestInternalLoggerSinkDropsInputsAndKeepsOnlyTheCode(t *testing.T) {
	isolateOTelEnv(t)
	var logs lockedBuffer
	release := attachDiagnostics(newTestLogger(&logs).Logger)
	defer release()

	logr.New(diagnosticSink{d: sharedDiagnostics}).Error(errors.New("missing '='"), "parse headers", "input", "Bearer INPUT-SENTINEL-3")
	out := logs.String()
	if !strings.Contains(out, `"error_code":"invalid_headers"`) {
		t.Fatalf("internal logger 오류가 분류되지 않음:\n%s", out)
	}
	mustNotContain(t, "log", out, "INPUT-SENTINEL-3", "parse headers")
}

// release 뒤에는 이전 logger로 기록하지 않는다. 종료한 Runtime의 logger를 늦은 비동기 오류가 사용하지 않는다.
func TestReleasedDiagnosticsStopLogging(t *testing.T) {
	isolateOTelEnv(t)
	var logs lockedBuffer
	release := attachDiagnostics(newTestLogger(&logs).Logger)
	release()
	otel.Handle(errors.New("late failure"))
	if out := logs.String(); out != "" {
		t.Fatalf("release 뒤에 기록함: %s", out)
	}
}
