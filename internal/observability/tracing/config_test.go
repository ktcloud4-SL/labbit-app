package tracing

import (
	"reflect"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	const httpEndpoint = "http://alloy.example.test:4318/v1/traces"
	tests := []struct {
		name         string
		env          envMap
		wantExporter Exporter
		wantProtocol Protocol
		wantEndpoint string
		wantService  string
		wantIssues   []Issue
	}{
		{name: "아무 설정이 없으면 Labbit 기본값 none", env: envMap{}, wantExporter: ExporterNone, wantService: DefaultServiceName},
		{name: "none", env: envMap{EnvExporter: "none"}, wantExporter: ExporterNone, wantService: DefaultServiceName},
		{name: "none은 대소문자와 공백을 정상화", env: envMap{EnvExporter: "  NONE "}, wantExporter: ExporterNone, wantService: DefaultServiceName},
		{
			name:         "otlp http/protobuf",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: httpEndpoint, EnvProtocol: "http/protobuf"},
			wantExporter: ExporterOTLP, wantProtocol: ProtocolHTTPProtobuf, wantEndpoint: httpEndpoint, wantService: DefaultServiceName,
		},
		{
			name:         "otlp grpc",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: "https://collector.example.test:4317", EnvProtocol: "grpc"},
			wantExporter: ExporterOTLP, wantProtocol: ProtocolGRPC, wantEndpoint: "https://collector.example.test:4317", wantService: DefaultServiceName,
		},
		{
			name:         "OTEL_SERVICE_NAME",
			env:          envMap{EnvServiceName: " custom-saas "},
			wantExporter: ExporterNone, wantService: "custom-saas",
		},
		{
			name:         "잘못된 exporter 값은 export 비활성화",
			env:          envMap{EnvExporter: "jaeger", EnvEndpoint: httpEndpoint, EnvProtocol: "grpc"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvExporter, IssueInvalidValue}},
		},
		{
			name:         "복수 exporter는 계약 밖이라 오류",
			env:          envMap{EnvExporter: "otlp,console"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvExporter, IssueInvalidValue}},
		},
		{
			name:         "otlp인데 endpoint 누락",
			env:          envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvEndpoint, IssueMissing}},
		},
		{
			name:         "otlp인데 protocol 누락",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: httpEndpoint},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvProtocol, IssueMissing}},
		},
		{
			name:         "otlp인데 endpoint와 protocol 모두 누락",
			env:          envMap{EnvExporter: "otlp"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvEndpoint, IssueMissing}, {EnvProtocol, IssueMissing}},
		},
		{
			name:         "잘못된 protocol",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: httpEndpoint, EnvProtocol: "http/json"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvProtocol, IssueInvalidValue}},
		},
		{
			name:         "자격증명이 들어간 endpoint는 오류",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: "http://user:pw-sentinel@alloy.example.test:4318/v1/traces", EnvProtocol: "http/protobuf"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvEndpoint, IssueInvalidValue}},
		},
		{
			name:         "scheme 없는 endpoint는 오류",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: "alloy.example.test:4317", EnvProtocol: "grpc"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvEndpoint, IssueInvalidValue}},
		},
		{
			name:         "http/https가 아닌 scheme은 오류",
			env:          envMap{EnvExporter: "otlp", EnvEndpoint: "ftp://alloy.example.test/v1/traces", EnvProtocol: "http/protobuf"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
			wantIssues: []Issue{{EnvEndpoint, IssueInvalidValue}},
		},
		{
			name:         "none이면 endpoint나 protocol 오류를 평가하지 않음",
			env:          envMap{EnvExporter: "none", EnvEndpoint: "::bad::", EnvProtocol: "bad"},
			wantExporter: ExporterNone, wantService: DefaultServiceName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigFromEnv(tt.env.get)
			if got.Exporter != tt.wantExporter || got.Protocol != tt.wantProtocol || got.Endpoint != tt.wantEndpoint || got.ServiceName != tt.wantService {
				t.Fatalf("ConfigFromEnv() = %+v", got)
			}
			if !reflect.DeepEqual(got.Issues, tt.wantIssues) {
				t.Fatalf("Issues = %+v, want %+v", got.Issues, tt.wantIssues)
			}
		})
	}
}

// 오류가 있으면 일부 설정으로 export를 시도하지 않는다. protocol/endpoint도 비운다.
func TestConfigFromEnvDoesNotKeepPartialOTLPSettingsOnError(t *testing.T) {
	got := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvEndpoint: "http://alloy.example.test:4318/v1/traces"}.get)
	if got.Exporter != ExporterNone || got.Protocol != "" || got.Endpoint != "" {
		t.Fatalf("ConfigFromEnv() = %+v, want export disabled with no partial settings", got)
	}
}
