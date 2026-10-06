// Package tracing은 SaaS Application의 OpenTelemetry SDK와 OTLP Trace exporter 조립이다(Runtime Contract saas.observability.tracing, D-25).
//
// 이 package만 SDK와 exporter module을 import한다. 제품 package(httpapi, terminal 등)는 trace API(go.opentelemetry.io/otel/trace)의
// Tracer만 받으므로 SDK나 Collector/Tempo를 알지 못한다. Trace backend는 Application dependency가 아니다.
//
// Trace는 업무 성공 조건이 아니다. 설정 오류, exporter 초기화 실패, Collector 장애는 모두 안전한 진단 뒤 export 비활성화로 처리하며
// Application startup, /livez, /readyz, 업무 요청을 실패시키지 않는다. Connector는 propagation-only이므로 이 package를 쓰지 않는다.
package tracing

import (
	"net/url"
	"strings"
)

// Runtime Contract(saas.config)의 Trace 설정 이름이다. Header, 인증서 경로는 official exporter가 같은 이름의 환경변수를 직접 읽는다
// (OTEL_EXPORTER_OTLP_TRACES_HEADERS, _CERTIFICATE, _CLIENT_CERTIFICATE, _CLIENT_KEY). 이 package는 그 값을 읽거나 기록하지 않는다.
const (
	EnvServiceName = "OTEL_SERVICE_NAME"
	EnvExporter    = "OTEL_TRACES_EXPORTER"
	EnvEndpoint    = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	EnvProtocol    = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"

	// DefaultServiceName은 OTEL_SERVICE_NAME의 Runtime Contract 기본값이다.
	DefaultServiceName = "labbit-server"
)

// Exporter는 OTEL_TRACES_EXPORTER의 허용 값이다. none은 export만 끄며 Span 생성과 Context 전파는 유지한다.
type Exporter string

const (
	ExporterNone Exporter = "none"
	ExporterOTLP Exporter = "otlp"
)

// Protocol은 OTEL_EXPORTER_OTLP_TRACES_PROTOCOL의 허용 값이다. http/json은 Runtime Contract에 없으므로 받지 않는다.
type Protocol string

const (
	ProtocolGRPC         Protocol = "grpc"
	ProtocolHTTPProtobuf Protocol = "http/protobuf"
)

// Issue 사유 code다. 값 원문이 아니라 고정된 분류만 담는다.
const (
	IssueInvalidValue = "invalid_value"
	IssueMissing      = "missing"
)

// Issue는 Trace 설정 하나의 오류다. Env는 환경변수 이름이며 값은 담지 않는다. Header와 인증서 경로 같은 Secret 값이 진단에 들어갈 길을 막는다.
type Issue struct {
	Env    string
	Reason string
}

// Config는 환경에서 해석한 Trace 설정이다. Exporter가 otlp이면 Protocol과 Endpoint가 모두 검증된 값이다.
// 설정 오류가 있으면 Exporter는 none이고 Issues에 사유가 남는다. 오류는 startup 실패가 아니다.
type Config struct {
	ServiceName string
	Exporter    Exporter
	Protocol    Protocol
	// Endpoint는 userinfo 없는 http/https URL이다. HTTP/protobuf는 /v1/traces를 포함한 최종 URL이다.
	Endpoint string
	Issues   []Issue
}

// ConfigFromEnv는 getenv에서 Trace 설정을 읽는다. 절대 실패하지 않는다.
//
//   - OTEL_TRACES_EXPORTER가 비었으면 Labbit 기본값 none이다. none/otlp 외의 값은 오류다.
//   - otlp이면 endpoint(http/https URL, 자격증명 없음)와 protocol(grpc, http/protobuf)이 모두 필요하다.
//   - 어느 하나라도 오류이면 export를 비활성화(none)하고 모든 오류를 Issues에 남긴다. 값 원문은 남기지 않는다.
func ConfigFromEnv(getenv func(string) string) Config {
	cfg := Config{ServiceName: DefaultServiceName, Exporter: ExporterNone}
	if name := strings.TrimSpace(getenv(EnvServiceName)); name != "" {
		cfg.ServiceName = name
	}

	requested := Exporter(strings.ToLower(strings.TrimSpace(getenv(EnvExporter))))
	switch requested {
	case "", ExporterNone:
		return cfg
	case ExporterOTLP:
	default:
		cfg.Issues = append(cfg.Issues, Issue{Env: EnvExporter, Reason: IssueInvalidValue})
		return cfg
	}

	endpoint, endpointIssue := parseEndpoint(getenv(EnvEndpoint))
	protocol, protocolIssue := parseProtocol(getenv(EnvProtocol))
	if endpointIssue != "" {
		cfg.Issues = append(cfg.Issues, Issue{Env: EnvEndpoint, Reason: endpointIssue})
	}
	if protocolIssue != "" {
		cfg.Issues = append(cfg.Issues, Issue{Env: EnvProtocol, Reason: protocolIssue})
	}
	if len(cfg.Issues) > 0 {
		return cfg
	}
	cfg.Exporter, cfg.Protocol, cfg.Endpoint = ExporterOTLP, protocol, endpoint
	return cfg
}

func parseProtocol(raw string) (Protocol, string) {
	switch Protocol(strings.ToLower(strings.TrimSpace(raw))) {
	case ProtocolGRPC:
		return ProtocolGRPC, ""
	case ProtocolHTTPProtobuf:
		return ProtocolHTTPProtobuf, ""
	case "":
		return "", IssueMissing
	default:
		return "", IssueInvalidValue
	}
}

// parseEndpoint는 Collector 주소를 검증한다. 자격증명은 URL에 넣지 않으므로 userinfo가 있으면 오류다.
// 오류 값에는 URL 원문이 들어가지 않는다.
func parseEndpoint(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", IssueMissing
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", IssueInvalidValue
	}
	return raw, ""
}
