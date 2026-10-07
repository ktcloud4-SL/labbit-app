package tracing

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentationName은 이 Application이 만드는 Span의 instrumentation scope 이름이다.
const InstrumentationName = "github.com/ktcloud4-SL/labbit-app"

// ComponentKey는 Trace resource에서 실행 중인 role(component)을 식별하는 attribute key다. Runtime Contract는 service.name에
// 더해 component와 environment가 식별 가능하기를 요구하며 그 이름을 정하지 않았으므로 Labbit namespace로 둔다.
const ComponentKey = attribute.Key("labbit.component")

// Options는 Config 밖에서 정해지는 Runtime 입력이다.
type Options struct {
	// Environment는 LABBIT_ENVIRONMENT이며 resource의 deployment.environment.name이다.
	Environment string
	// Component는 실행 중인 role 목록(예: "api,realtime")이다.
	Component string
	// Logger는 설정 오류와 export 진단을 받는다. nil이면 기록하지 않는다. Secret 값과 오류 원문은 어떤 경우에도 전달하지 않는다.
	Logger *slog.Logger
}

// Runtime은 TracerProvider와 선택적 OTLP exporter의 수명이다.
//
// exporter가 없어도(none이거나 설정 오류) SDK는 실제 Span과 유효한 SpanContext를 만든다. 그래서 W3C Context 전파와 log의 trace_id
// correlation은 export와 무관하게 유지된다. SDK의 기본 sampler(ParentBased(AlwaysSample), OTEL_TRACES_SAMPLER로 플랫폼이 조정)를
// 그대로 쓰므로 incoming parent의 sampled flag를 바꾸지 않는다.
type Runtime struct {
	provider  *sdktrace.TracerProvider
	tracer    trace.Tracer
	exporting bool
	logger    *slog.Logger
	release   func()

	shutdownOnce sync.Once
	shutdownErr  error
}

// Start는 Trace 설정으로 TracerProvider를 만든다. 실패하지 않는다.
//
// 설정 오류(cfg.Issues), exporter 생성 실패, 환경변수로 받은 header/인증서 해석 실패는 모두 안전한 진단을 남기고 export 없이 계속한다.
// exporter는 연결을 시작하지 않으므로 Collector가 없어도 Start는 즉시 반환한다. export는 BatchSpanProcessor의 제한된 비동기
// queue(SDK 기본값, OTEL_BSP_* 로 조정)로 하며 요청 경로에서 network I/O를 하지 않는다.
func Start(ctx context.Context, cfg Config, opts Options) *Runtime {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	// exporter와 SDK를 만들기 전에 안전한 handler를 설치한다. 기본 handler는 header 한 쌍 같은 원문을 stderr로 출력한다.
	release := attachDiagnostics(logger)

	for _, issue := range cfg.Issues {
		logger.Warn("Trace 설정이 올바르지 않아 외부 export를 비활성화합니다", "env", issue.Env, "reason", issue.Reason)
	}

	var exporter sdktrace.SpanExporter
	if cfg.Exporter == ExporterOTLP {
		exporter = newExporter(ctx, cfg, logger)
	}

	sharedDiagnostics.startCapture()
	providerOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(newResource(cfg, opts))}
	if exporter != nil {
		providerOptions = append(providerOptions, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(providerOptions...)
	captured := sharedDiagnostics.stopCapture()

	if len(captured) > 0 {
		if exporter != nil {
			logger.Warn("Trace 설정 오류로 외부 export를 비활성화합니다", "reason", captured[0])
			discardProvider(provider)
			exporter = nil
			sharedDiagnostics.startCapture()
			provider = sdktrace.NewTracerProvider(sdktrace.WithResource(newResource(cfg, opts)))
			_ = sharedDiagnostics.stopCapture()
		} else if len(cfg.Issues) == 0 {
			logger.Warn("Trace 설정 오류로 외부 export를 비활성화합니다", "reason", captured[0])
		}
	}

	if exporter != nil {
		logger.Info("Trace OTLP export 활성화", "protocol", string(cfg.Protocol))
	} else {
		logger.Info("Trace 외부 export 비활성화", "exporter", string(ExporterNone))
	}
	return &Runtime{
		provider:  provider,
		tracer:    provider.Tracer(InstrumentationName),
		exporting: exporter != nil,
		logger:    logger,
		release:   release,
	}
}

// newExporter는 검증된 protocol의 official OTLP exporter를 만든다. 실패하면 nil이다.
//
// endpoint는 이미 검증한 값을 명시적으로 넘기고, header/인증서/timeout 같은 나머지는 exporter가 표준 환경변수
// (OTEL_EXPORTER_OTLP_TRACES_*)에서 직접 해석한다. 해석 오류는 exporter가 중단 없이 값을 무시하고 internal logger로만 알리므로,
// 생성 구간의 진단을 모아 하나라도 있으면 일부 설정으로 계속 export하지 않고 비활성화한다(인증 없이 보내거나 시스템 CA로 대체하지 않는다).
func newExporter(ctx context.Context, cfg Config, logger *slog.Logger) sdktrace.SpanExporter {
	sharedDiagnostics.startCapture()
	var (
		exporter sdktrace.SpanExporter
		err      error
	)
	switch cfg.Protocol {
	case ProtocolGRPC:
		exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
	case ProtocolHTTPProtobuf:
		exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.Endpoint))
	default:
		err = errors.New("unsupported protocol")
	}
	captured := sharedDiagnostics.stopCapture()

	if err != nil || len(captured) > 0 {
		reason := "exporter_init_failed"
		if len(captured) > 0 {
			reason = captured[0]
		}
		logger.Warn("Trace exporter 설정 오류로 외부 export를 비활성화합니다", "reason", reason)
		if exporter != nil {
			// 연결을 시작하지 않은 exporter이므로 정리는 즉시 끝난다. 결과는 쓰지 않는다.
			_ = exporter.Shutdown(ctx)
		}
		return nil
	}
	return exporter
}

// newResource는 Trace resource를 만든다. service.name은 OTEL_SERVICE_NAME, environment와 component는 Application 값이다.
// SDK의 WithResource가 환경(OTEL_RESOURCE_ATTRIBUTES)과 병합하며 여기서 정한 값이 우선한다.
func newResource(cfg Config, opts Options) *resource.Resource {
	attrs := []attribute.KeyValue{semconv.ServiceName(cfg.ServiceName)}
	if env := strings.TrimSpace(opts.Environment); env != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(env))
	}
	if component := strings.TrimSpace(opts.Component); component != "" {
		attrs = append(attrs, ComponentKey.String(component))
	}
	// schema URL이 다른 Default resource와 병합할 때 충돌하지 않도록 schemaless로 만든다.
	return resource.NewSchemaless(attrs...)
}

// Tracer는 Application이 Span을 만드는 Tracer다. 제품 package에는 이 trace.Tracer만 넘기고 Runtime이나 SDK 타입은 넘기지 않는다.
func (r *Runtime) Tracer() trace.Tracer { return r.tracer }

// Exporting은 OTLP exporter가 실제로 연결되어 있는지 반환한다.
func (r *Runtime) Exporting() bool { return r.exporting }

// Shutdown은 ctx가 끝나기 전까지만 대기 중인 Span을 flush하고 exporter를 닫는다. 여러 번 호출해도 처음 결과를 반환한다.
//
// ctx의 deadline이 전체 상한이다. Collector가 응답하지 않으면 deadline에 context 오류로 반환하며 더 기다리지 않는다.
// 진단은 error_code만 기록한다. 호출자는 이 오류로 이미 끝난 업무 lifecycle을 실패로 바꾸지 않아야 한다.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		r.shutdownErr = r.provider.Shutdown(ctx)
		if r.shutdownErr != nil {
			r.logger.Warn("Trace flush를 끝내지 못하고 종료합니다", "error_code", classifyError(r.shutdownErr))
		}
		r.release()
	})
	return r.shutdownErr
}

const discardShutdownTimeout = 500 * time.Millisecond

// discardProvider는 설정 오류로 버려지는 TracerProvider의 SpanProcessor 및 Exporter 수명을 안전하게 정리한다.
// Exporter만 직접 Shutdown하면 BatchSpanProcessor의 background goroutine/timer가 누수되므로
// 반드시 provider.Shutdown을 통해 소유된 Processor 전체를 종료해야 한다.
// startup 지연을 방지하기 위해 bounded timeout을 사용하며, caller context와 독립적으로 실행된다.
func discardProvider(provider *sdktrace.TracerProvider) {
	if provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), discardShutdownTimeout)
	defer cancel()
	_ = provider.Shutdown(ctx)
}
