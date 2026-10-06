package tracing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OpenTelemetry SDK와 official exporter는 두 가지 process-global 경로로 진단을 내보낸다.
//
//   - otel.Handle: BatchSpanProcessor의 비동기 export 실패. 기본 handler는 err.Error()를 그대로 출력한다.
//   - otel internal logger: exporter가 환경변수(OTEL_EXPORTER_OTLP_TRACES_HEADERS, 인증서 경로 등)를 해석하다 실패하면 입력 원문
//     (header 한 쌍, 값, 파일 경로)을 key/value로 실어 stderr로 출력한다.
//
// 어느 쪽도 Secret이 들어갈 수 있고 HTTP exporter의 오류 문자열에는 Collector 응답 본문이 들어가므로, 둘 다 원문을 버리고
// 고정된 분류 code만 기록하는 handler로 바꾼다. 같은 code는 interval마다 한 번만 기록해 Collector 장애가 로그를 채우지 않게 한다.

// 진단 code는 error_code 필드의 닫힌 집합이다. 원문 오류를 구분하는 데 쓰지 않는다.
const (
	codeCollectorUnreachable = "collector_unreachable"
	codeCollectorUnauth      = "collector_unauthorized"
	codeExportTimeout        = "export_timeout"
	codeExportCanceled       = "export_canceled"
	codeExportRejected       = "export_rejected"
	codeTLSFailure           = "tls_failure"
	codeExportFailed         = "export_failed"
	codeInvalidHeaders       = "invalid_headers"
	codeInvalidCertificate   = "invalid_certificate"
	codeInvalidEndpoint      = "invalid_endpoint"
	codeInvalidTimeout       = "invalid_timeout"
	codeTraceInternal        = "trace_internal"
)

// diagnosticLogInterval은 같은 code를 다시 기록하기까지의 최소 간격이다. 억제한 횟수는 다음 기록의 suppressed로 남는다.
const diagnosticLogInterval = time.Minute

type diagnostics struct {
	mu         sync.Mutex
	logger     *slog.Logger
	now        func() time.Time
	interval   time.Duration
	lastLogged map[string]time.Time
	suppressed map[string]int
	// capturing이면 기록하는 대신 code를 모은다. exporter 생성 중의 설정 오류를 호출자가 판정하는 데 쓴다.
	capturing bool
	captured  []string
}

var (
	sharedDiagnostics = &diagnostics{
		now:        time.Now,
		interval:   diagnosticLogInterval,
		lastLogged: map[string]time.Time{},
		suppressed: map[string]int{},
	}
	installDiagnostics sync.Once
)

// attachDiagnostics는 process-global OpenTelemetry handler를 한 번 설치하고 이 logger로 진단을 보낸다.
// 반환한 release를 호출하면(같은 logger가 아직 연결되어 있을 때) 연결을 끊고 이후 진단은 기록하지 않고 버린다.
// handler 자체는 process 수명 동안 남지만 원문을 출력하는 기본 handler로 돌아가지 않는다.
func attachDiagnostics(logger *slog.Logger) (release func()) {
	installDiagnostics.Do(func() {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(sharedDiagnostics.handleExportError))
		otel.SetLogger(logr.New(diagnosticSink{d: sharedDiagnostics}))
	})
	sharedDiagnostics.mu.Lock()
	sharedDiagnostics.logger = logger
	sharedDiagnostics.mu.Unlock()
	return func() {
		sharedDiagnostics.mu.Lock()
		defer sharedDiagnostics.mu.Unlock()
		if sharedDiagnostics.logger == logger {
			sharedDiagnostics.logger = nil
		}
	}
}

// startCapture는 stopCapture까지의 진단을 기록하지 않고 code로 모은다.
func (d *diagnostics) startCapture() {
	d.mu.Lock()
	d.capturing, d.captured = true, nil
	d.mu.Unlock()
}

func (d *diagnostics) stopCapture() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	codes := d.captured
	d.capturing, d.captured = false, nil
	return codes
}

// handleExportError는 otel.Handle의 목적지다. 비동기 export 오류를 분류해 기록한다. err 원문은 버린다.
func (d *diagnostics) handleExportError(err error) {
	d.report("OTLP export 실패", classifyError(err))
}

func (d *diagnostics) report(message, code string) {
	d.mu.Lock()
	if d.capturing {
		d.captured = append(d.captured, code)
		d.mu.Unlock()
		return
	}
	logger := d.logger
	if logger == nil {
		d.mu.Unlock()
		return
	}
	now := d.now()
	if last, seen := d.lastLogged[code]; seen && now.Sub(last) < d.interval {
		d.suppressed[code]++
		d.mu.Unlock()
		return
	}
	d.lastLogged[code] = now
	suppressed := d.suppressed[code]
	delete(d.suppressed, code)
	d.mu.Unlock()

	attrs := []any{"error_code", code}
	if suppressed > 0 {
		attrs = append(attrs, "suppressed", suppressed)
	}
	logger.Warn(message, attrs...)
}

// diagnosticSink는 OpenTelemetry internal logger의 logr.LogSink다. Error만 받고 key/value는 모두 버린다.
// msg는 library가 정한 고정 문자열이므로 분류에만 쓰고 출력하지 않는다.
type diagnosticSink struct{ d *diagnostics }

func (diagnosticSink) Init(logr.RuntimeInfo)            {}
func (diagnosticSink) Enabled(int) bool                 { return false }
func (diagnosticSink) Info(int, string, ...any)         {}
func (s diagnosticSink) WithValues(...any) logr.LogSink { return s }
func (s diagnosticSink) WithName(string) logr.LogSink   { return s }
func (s diagnosticSink) Error(err error, msg string, _ ...any) {
	s.d.report("OpenTelemetry 진단", classifyLibraryError(msg, err))
}

// classifyLibraryError는 exporter/SDK가 internal logger로 보고한 오류를 분류한다. 환경변수 해석 실패는 설정 오류 code다.
func classifyLibraryError(msg string, err error) string {
	switch {
	case strings.Contains(msg, "header"):
		return codeInvalidHeaders
	case strings.Contains(msg, "tls"), strings.Contains(msg, "cert"):
		return codeInvalidCertificate
	case strings.Contains(msg, "duration"):
		return codeInvalidTimeout
	case strings.Contains(msg, "url"), strings.Contains(msg, "endpoint"):
		return codeInvalidEndpoint
	case err != nil:
		return classifyError(err)
	default:
		return codeTraceInternal
	}
}

// classifyError는 export 오류를 로그에 남겨도 안전한 고정 code로 바꾼다. 오류 문자열은 쓰지 않는다.
// HTTP exporter의 오류 문자열은 요청 URL과 Collector 응답 본문을 포함하므로 일부 판별(failed to send)에만 비교하고 기록하지 않는다.
func classifyError(err error) string {
	var (
		netErr   net.Error
		dnsErr   *net.DNSError
		opErr    *net.OpError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		verify   *tls.CertificateVerificationError
		recHdr   tls.RecordHeaderError
	)
	switch {
	case err == nil:
		return codeTraceInternal
	case errors.Is(err, context.DeadlineExceeded):
		return codeExportTimeout
	case errors.Is(err, context.Canceled):
		return codeExportCanceled
	case errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &verify), errors.As(err, &recHdr):
		return codeTLSFailure
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH), errors.As(err, &dnsErr):
		return codeCollectorUnreachable
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable:
			return codeCollectorUnreachable
		case codes.DeadlineExceeded:
			return codeExportTimeout
		case codes.Canceled:
			return codeExportCanceled
		case codes.Unauthenticated, codes.PermissionDenied:
			return codeCollectorUnauth
		default:
			return codeExportRejected
		}
	}
	switch {
	case errors.As(err, &netErr) && netErr.Timeout():
		return codeExportTimeout
	case errors.As(err, &opErr):
		return codeCollectorUnreachable
	case strings.Contains(err.Error(), "failed to send to"):
		return codeExportRejected
	default:
		return codeExportFailed
	}
}
