# Labbit Runtime Contract

이 디렉터리는 **Labbit 애플리케이션 ↔ 실행 플랫폼 사이의 Runtime Contract SSOT**입니다.

- 기계 판독 기준: [`runtime/contract.yaml`](./contract.yaml)
- 이 문서: 계약의 의미·운영 경계를 설명하는 사람용 안내
- 실제 Kubernetes/AWS 배포 값: 별도 Platform/GitOps 저장소의 Helm/IaC/Runbook

Runtime Contract는 HTTP/OpenAPI나 WSS 메시지 계약을 다시 정의하지 않습니다. 애플리케이션이 **어떻게 실행되고, 어떤 포트·설정·Probe·로그·종료 동작을 제공해야 하는지**와 플랫폼이 무엇을 주입·구성해야 하는지를 고정합니다.

**v0.1.2 변경:** SL-64 Auth/Class 구현 준비에 따라 Browser unsafe method의 CSRF/Origin 검증에 사용할 trusted LABBIT_PUBLIC_ORIGIN 입력을 추가했습니다. v0.1.1의 SaaS OTel/OTLP, durable Context, Connector propagation-only 계약은 유지합니다. 아래 요구사항은 구현 기준이며, 기존 스켈레톤이 이미 이를 제공한다는 뜻은 아닙니다.

## v0.1에서 확정하는 경계

### SaaS 실행 역할

중앙 SaaS는 다음 네 **논리 역할**을 가집니다.

- `api`: HTTP 요청·인증/인가·Operation 등록
- `worker`: PostgreSQL의 durable OperationItem claim·Provider mutation 처리
- `realtime`: Browser/Connector Terminal·Live WebSocket 처리
- `preview`: Preview proxy/gateway 처리

이 네 역할이 곧 네 개의 Kubernetes Deployment라는 뜻은 아닙니다. v0.1은 하나의 `labbit-server` 실행 파일이 `LABBIT_RUNTIME_ROLES`로 하나 이상 역할을 켤 수 있게 계약하고, 실제 workload 분리·Replica/HPA는 부하·장애·배포 검증 후 Platform/GitOps에서 결정합니다.

고객 환경 Connector는 별도 `labbit-connector` 실행 단위입니다.

## Listener

### Application listener

- 설정: `LABBIT_HTTP_ADDR`
- 기본값: `:8080`
- 용도: HTTP API, Browser WSS, Connector WSS, co-located Preview
- 외부 사용자는 이 포트에 직접 의존하지 않습니다. 플랫폼이 D-16의 공개 **HTTPS/WSS 443** 경계를 통해 필요한 Route만 노출합니다.

### Admin listener

- 설정: `LABBIT_ADMIN_ADDR`
- 기본값: `:9090`
- **Public 노출 금지**
- `GET /livez`: 프로세스 자체 liveness
- `GET /readyz`: enabled role이 새 작업을 안전하게 받을 수 있는지
- `GET /metrics`: Prometheus scrape

`/livez`는 PostgreSQL·Connector·OpenStack의 일시 장애 때문에 실패시키지 않습니다. 반대로 `/readyz`는 API/Worker 역할에서 PostgreSQL이 사용할 수 없거나 지원 Schema 범위를 벗어나면 실패해야 합니다. 특정 고객 Connector가 OFFLINE이라고 전체 SaaS Pod를 unready로 만들지 않습니다. Collector/Tempo 가용성도 liveness/readiness의 업무 의존성으로 두지 않습니다.

## 필수 Runtime 설정

정확한 키·형식은 `contract.yaml`이 원본입니다. 핵심은 다음과 같습니다.

| 설정 | 의미 | Secret |
|---|---|---|
| `LABBIT_RUNTIME_ROLES` | `api,worker,realtime,preview` 중 enabled role | No |
| `LABBIT_ENVIRONMENT` | environment 식별 | No |
| `LABBIT_HTTP_ADDR` | application listen address | No |
| `LABBIT_PUBLIC_ORIGIN` | Browser Auth/CSRF 검증의 trusted public app origin | No |
| `LABBIT_ADMIN_ADDR` | health/metrics listen address | No |
| `LABBIT_DATABASE_DSN_FILE` | production DB DSN secret file 경로 | 경로 자체 No / 파일 내용 Yes |
| `LABBIT_DATABASE_DSN` | local development용 직접 DSN | **Yes** |
| `LABBIT_LOG_LEVEL` | application log level | No |
| `LABBIT_SHUTDOWN_GRACE` | graceful shutdown budget | No |

Production에서는 DB DSN 원문을 일반 ConfigMap이나 로그에 남기지 않고 Secret injection으로 파일을 제공하는 방식을 기준으로 합니다. LABBIT_DATABASE_DSN은 local development escape hatch이며 두 값이 모두 있으면 file form을 우선합니다.

LABBIT_PUBLIC_ORIGIN은 Proxy/LB 뒤의 request Host에서 추론하지 않는 trusted 설정입니다. 개발자 PC에서는 Vite가 보이는 Browser origin을, 공유 Local/AWS에서는 실제 public HTTPS application origin을 사용합니다. Session lifecycle과 Origin/Referer 검증 세부는 docs/backend/auth-session.md를 따릅니다.

## Schema Migration

Application startup AutoMigration은 사용하지 않습니다.

```text
Git SQL Migration
      ↓
별도 CI / Migration Job
      ↓
Schema 호환 확인
      ↓
Application rollout
```

Replica마다 startup 시 Migration을 실행하지 않습니다. 애플리케이션이 지원하지 않는 Schema 상태라면 정상 서비스인 것처럼 Ready가 되지 않아야 합니다.

## Graceful Shutdown

`SIGTERM`/ `SIGINT` 수신 시 공통 순서는 다음입니다.

```text
readiness = false
      ↓
신규 작업/연결 수락 중지
      ↓
현재 작업·연결 drain
      ↓
알고 있는 durable 결과 저장
      ↓
grace 만료 시 종료
```

역할별 의미는 다릅니다.

- **API**: 새로운 Provider mutation 등록을 중단하고 이미 수락한 bounded HTTP 요청을 안전한 범위에서 마무리합니다.
- **Worker**: 새 `PENDING operation_items`를 claim하지 않습니다. Provider 결과가 불명확하면 lease 만료만 보고 재실행하지 않고 재시작 후 Reconciliation합니다.
- **Realtime**: 종료 대상 인스턴스가 새 WSS upgrade를 받지 않고 기존 연결을 drain합니다.
- **Preview**: 새 proxy/session을 받지 않고 현재 연결을 가능한 범위에서 drain합니다.

중앙 Realtime workload의 Connection Draining과 D-21의 **Connector PTY 60초 grace**는 서로 다른 정책입니다.

`LABBIT_SHUTDOWN_GRACE`의 production 값은 v0.1에서 숫자로 고정하지 않습니다. D-22의 운영 후보와 실제 workload를 기반으로 `terminationGracePeriodSeconds`, LB/Gateway draining과 함께 통합 테스트 후 Platform/GitOps에서 확정합니다.

## Logging · Tracing · Metrics

Application과 Connector는 **JSON 한 줄 = 한 event** 형식으로 stdout/stderr에 기록합니다. 로컬 파일 로그를 Runtime Contract의 필수 경로로 만들지 않습니다.

기본 필드는 `timestamp`, `level`, `message`, `service`, `component`, `version`, `environment`입니다. 알 수 있는 시점부터 `trace_id`, `request_id`, `operation_id`, `lab_instance_id`, `connector_id`, `terminal_session_id`, `live_session_id`, `preview_session_id`를 상관관계 필드로 유지합니다. HTTP/WSS 경계의 trace propagation은 W3C Trace Context를 사용합니다.

Prometheus label에는 위 ID처럼 high-cardinality 값을 사용하지 않습니다. `service`, `component`, `operation_type`, `stage`, `result`, `environment` 같은 low-cardinality label만 사용합니다.

다음 본문·Secret은 로그/trace에 기록하지 않습니다.

- OpenStack Credential / Keystone Token
- Connector Credential 원문
- Authorization Header
- Password 원문
- Browser/Terminal 인증 Token 원문
- Secret을 포함할 수 있는 raw Provider payload
- Terminal INPUT/OUTPUT
- Live OUTPUT
- Workspace 파일 본문/Source Code

중앙 로그 수집 경로와 보존은 D-23/Platform 설계가 담당합니다. Connector raw log/raw metric/raw trace의 상시 중앙 전송은 MVP에서 요구하지 않습니다.

### SaaS 계측과 OTLP 전송

SaaS는 HTTP 요청·Operation 등록, Worker 처리, Connector Command 전송과 ACK/PROGRESS/RESULT 수신 경계의 Span을 생성합니다. 계측 코드와 OTLP exporter는 개발팀, Collector 수신 경로·저장소·조회·TLS/인증 주입은 운영팀 책임입니다. D-25의 플랫폼 선택은 Alloy → Tempo → Grafana이며, 애플리케이션은 Tempo 전용 API/주소에 결합하지 않습니다.

SaaS 전용 기본 설정은 `OTEL_SERVICE_NAME`, `OTEL_TRACES_EXPORTER`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`입니다. Collector가 별도 인증/TLS 입력을 요구하면 표준 `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, `OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE`, `OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE`, `OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY`도 지원합니다. 정확한 필수 조건은 `contract.yaml`을 따릅니다. 첫 통합 전에 SDK exporter와 Collector의 gRPC 또는 HTTP/protobuf 선택을 맞춥니다. Trace 전용 HTTP endpoint는 `/v1/traces`를 포함한 최종 URL이며 자격증명은 URL에 넣지 않습니다. Header 원문은 Secret으로 주입하고, custom CA/client certificate/private key는 플랫폼이 파일로 mount한 뒤 경로만 애플리케이션에 전달합니다.

로컬에서는 export `none`으로 실행할 수 있지만, 중앙 Trace 인수 검증 환경에서는 플랫폼이 `otlp`를 명시적으로 활성화합니다. Export 비활성화나 미샘플링은 유효한 Context 전달을 생략할 이유가 아닙니다. SDK 초기화 코드가 이 설정을 실제로 읽고 적용하는지 별도로 테스트해야 합니다.

기존 `dedicated_trace_backend_required: false`는 제거하고 `trace_backend_is_application_dependency: false`로 의미를 명확히 했습니다. 이는 **플랫폼에서 Tempo를 쓰지 않는다는 뜻이 아니라, Tempo 장애 때문에 업무 프로세스가 기동/처리를 못 하게 만들지 않는다는 뜻**입니다.

### API → Operation → Worker

새 Operation을 등록할 때 현재 SaaS Span에서 W3C Context를 직렬화하고 `operations.traceparent` / `operations.tracestate`에 같은 transaction으로 저장합니다. DB 필드·Migration은 [DB 계약](../db/migrations/README.md)이 원본입니다. 저장 대상은 전체 HTTP header, Go context 객체, Span 전체, Baggage가 아니라 최소 전파 metadata입니다.

Worker는 저장된 Context를 복원하고 독립 lifecycle의 새 Span을 만듭니다. HTTP 응답과 HTTP Span이 끝나도 비동기 작업은 같은 Trace에 연결될 수 있습니다. **HTTP 요청 취소/timeout을 Worker 실행 context에 재사용하지 않습니다.** 여러 OperationItem과 재처리 시도는 각각 새 Span ID를 사용하며, 결과 수신은 명령별 Context와 제품 ID로 연결합니다.

Idempotency replay는 새 요청의 Context로 원본 Operation Context를 덮어쓰지 않습니다. Trace metadata는 요청의 업무 fingerprint·권한·중복 실행 판단에 사용하지 않습니다. 결과 불명은 기존 Reconciliation 규칙을 따르며 관측 오류를 Provider 재시도 근거로 사용하지 않습니다.

### Connector 및 장애 격리

Connector는 **propagation-only**입니다. 유효한 명령 Context를 작업별로 보존해 관련 응답과 로컬 로그에 연결하되 중앙 OTLP exporter/수신 주소/추가 외부 인증을 요구하지 않습니다. 전파의 정확한 정상화·명령별 격리 규칙은 [Connector 계약 9절](../contracts/connector/README.md#9-공통-correlation)을 따릅니다. 이 원칙은 경량 propagator/API 라이브러리 사용까지 금지한다는 뜻은 아닙니다.

Context가 없거나 잘못되면 해당 관측 metadata만 폐기합니다. SaaS는 필요 시 새 Trace를 만들고 Connector는 제품 ID만으로 처리합니다. 실제 Operation 성공은 기존 업무 로직·Provider 결과로 판단하며, Trace 누락 때문에 실패시키지 않습니다. 인증 실패, 잘못된 필수 업무 필드, 전체 메시지 크기 제한 위반까지 허용하는 규칙은 아닙니다.

OTLP 전송 장애·Trace 전용 설정 오류는 안전한 진단을 남기고 제한된 비동기 처리/전송 비활성화로 격리합니다. 무제한 buffer/retry나 readiness 실패를 만들지 않습니다. 종료 flush는 durable 업무 상태 저장보다 우선하지 않고 남은 shutdown budget을 넘기지 않습니다.

### 후속 구현 인수 검증

아래는 **완료된 테스트가 아니라 실제 계측 PR의 인수 기준**입니다.

- 수집 대상인 정상 Provision/Reset/Cleanup에서 API → Worker → Command/Result가 같은 중앙 Trace로 연결됩니다. 병렬 item의 Context가 섞이지 않습니다.
- HTTP 응답 후 Worker가 처리하거나 재시작한 뒤에도 저장된 Context를 사용할 수 있으며, 재처리 Span ID는 새로 생성됩니다.
- Context 없음/손상, `tracestate`만 잘못된 경우, 미샘플링 Context, Idempotency replay를 검증합니다.
- Collector 중단·export 비활성화·잘못된 Trace 설정에도 업무 상태와 Probe의 업무 의미를 유지합니다.
- Grafana의 Trace와 JSON 로그가 연결되고, 금지 데이터·고유 ID metric label·PTY 본문 계측이 없습니다.

Sampling 비율·보존기간·정확한 Span 이름·Dashboard는 첫 통합과 운영 검증에서 조정합니다. 미수집 실행, Context 유실, 프로세스 crash에서 완전한 Trace를 보장하지 않으며 `operation_id`를 지속적인 조사 기준으로 사용합니다.

## Connector Runtime

Connector는 고객망 내부에서 실행하며 **SaaS가 Connector로 inbound 접속할 public port를 요구하지 않습니다.**

핵심 설정은 다음과 같습니다.

- `LABBIT_SAAS_BASE_URL`
- `LABBIT_CONNECTOR_ID`
- `LABBIT_CONNECTOR_CREDENTIAL_FILE`
- `LABBIT_PROVIDER_CONFIG_FILE`
- `LABBIT_LOG_LEVEL`

Connector Credential과 Provider/OpenStack Credential 원문은 고객 환경 Secret 경계에 남습니다. Connector는 SaaS 공개 443으로 outbound WSS를 생성합니다. Control path/subprotocol과 Terminal Data framing은 `contracts/connector`의 Git 계약이 원본입니다.

D-17의 기본 Heartbeat는 15초, SaaS OFFLINE 판단은 45초입니다. 값은 설정 가능하지만 reconnect를 Operation Retry로 해석하지 않습니다.

## Frontend Runtime

Frontend는 Vite production build의 **정적 산출물**입니다. MVP Runtime Contract는 SSR/RSC Application Server를 요구하지 않습니다.

Frontend에 주입되는 설정은 Browser에 공개되어도 되는 값만 허용합니다. Secret은 build artifact나 public runtime configuration에 포함하지 않습니다. 이번 Trace 계약은 Browser OTel SDK나 제품 HTTP JSON payload 변경을 요구하지 않습니다.

## Platform이 책임지는 것

Platform/GitOps는 Runtime Contract를 소비해 다음을 실제 배포 값으로 구현합니다.

- immutable image/artifact build와 version 식별
- Secret/Config 주입
- 환경별 LABBIT_PUBLIC_ORIGIN 주입
- public HTTPS/WSS 443 routing
- admin listener 비공개 유지
- `/livez`, `/readyz` Kubernetes Probe
- `/metrics` Prometheus scrape
- Replica/HPA/workload 분리
- `LABBIT_SHUTDOWN_GRACE` · `terminationGracePeriodSeconds` · connection draining 정합화
- 별도 SQL Migration 단계
- stdout/stderr JSON log 수집
- SaaS 전용 OTLP 수신 주소/protocol·TLS/인증 주입, Alloy/Tempo/Grafana 및 Log correlation 검증
- RDS·Backup/Restore·DR·DNS/TLS·rollback

따라서 **애플리케이션 Runtime Contract와 실제 Helm/IaC는 같은 문서가 아닙니다.** Application은 실행 능력과 의미를 제공하고 Platform은 환경별 실제 값을 선택합니다.

## 완료의 의미

Runtime Contract가 Git에 존재한다는 것은 **실행 인터페이스가 정의됐다는 뜻**입니다. 다음의 구현·검증 완료를 이 문서만으로 판단하지 않습니다.

- `labbit-server` / `labbit-connector`의 실제 기능 구현
- Probe handler와 업무 의존성 연결
- multi-role/multi-replica routing
- graceful shutdown 통합 테스트
- 실제 Kubernetes Deployment/Service
- Prometheus/Loki 연결
- OTel/OTLP 초기화, durable Context 저장/복원, Connector propagation 및 Tempo E2E
- RDS Migration/Restore 검증

실제 구현값이 이 계약을 변경해야 한다면 먼저 `runtime/contract.yaml`을 수정하고, 의미가 바뀌는 경우에만 Confluence의 상위 설계를 함께 갱신합니다.

표준 참고: [W3C Trace Context](https://www.w3.org/TR/trace-context/) · [OpenTelemetry OTLP Exporter](https://opentelemetry.io/docs/specs/otel/protocol/exporter/) · [OpenTelemetry Error Handling](https://opentelemetry.io/docs/specs/otel/error-handling/)
