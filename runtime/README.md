# Labbit Runtime Contract

이 디렉터리는 **Labbit 애플리케이션 ↔ 실행 플랫폼 사이의 Runtime Contract SSOT**입니다.

- 기계 판독 기준: [`runtime/contract.yaml`](./contract.yaml)
- 이 문서: 계약의 의미·운영 경계를 설명하는 사람용 안내
- 실제 Kubernetes/AWS 배포 값: 별도 Platform/GitOps 저장소의 Helm/IaC/Runbook

Runtime Contract는 HTTP/OpenAPI나 WSS 메시지 계약을 다시 정의하지 않습니다. 애플리케이션이 **어떻게 실행되고, 어떤 포트·설정·Probe·로그·종료 동작을 제공해야 하는지**와 플랫폼이 무엇을 주입·구성해야 하는지를 고정합니다.

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

`/livez`는 PostgreSQL·Connector·OpenStack의 일시 장애 때문에 실패시키지 않습니다. 반대로 `/readyz`는 API/Worker 역할에서 PostgreSQL이 사용할 수 없거나 지원 Schema 범위를 벗어나면 실패해야 합니다. 특정 고객 Connector가 OFFLINE이라고 전체 SaaS Pod를 unready로 만들지 않습니다.

## 필수 Runtime 설정

정확한 키·형식은 `contract.yaml`이 원본입니다. 핵심은 다음과 같습니다.

| 설정 | 의미 | Secret |
|---|---|---|
| `LABBIT_RUNTIME_ROLES` | `api,worker,realtime,preview` 중 enabled role | No |
| `LABBIT_ENVIRONMENT` | environment 식별 | No |
| `LABBIT_HTTP_ADDR` | application listen address | No |
| `LABBIT_ADMIN_ADDR` | health/metrics listen address | No |
| `LABBIT_DATABASE_DSN_FILE` | production DB DSN secret file 경로 | 경로 자체 No / 파일 내용 Yes |
| `LABBIT_DATABASE_DSN` | local development용 직접 DSN | **Yes** |
| `LABBIT_LOG_LEVEL` | application log level | No |
| `LABBIT_SHUTDOWN_GRACE` | graceful shutdown budget | No |

Production에서는 DB DSN 원문을 일반 ConfigMap이나 로그에 남기지 않고 Secret injection으로 파일을 제공하는 방식을 기준으로 합니다. `LABBIT_DATABASE_DSN`은 local development escape hatch이며 두 값이 모두 있으면 file form을 우선합니다.

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

중앙 로그 수집 경로와 보존은 D-23/Platform 설계가 담당합니다. Connector raw log/raw metric 상시 중앙 전송은 MVP Runtime Contract에서 요구하지 않습니다.

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

Frontend에 주입되는 설정은 Browser에 공개되어도 되는 값만 허용합니다. Secret은 build artifact나 public runtime configuration에 포함하지 않습니다.

## Platform이 책임지는 것

Platform/GitOps는 Runtime Contract를 소비해 다음을 실제 배포 값으로 구현합니다.

- immutable image/artifact build와 version 식별
- Secret/Config 주입
- public HTTPS/WSS 443 routing
- admin listener 비공개 유지
- `/livez`, `/readyz` Kubernetes Probe
- `/metrics` Prometheus scrape
- Replica/HPA/workload 분리
- `LABBIT_SHUTDOWN_GRACE` · `terminationGracePeriodSeconds` · connection draining 정합화
- 별도 SQL Migration 단계
- stdout/stderr JSON log 수집
- RDS·Backup/Restore·DR·DNS/TLS·rollback

따라서 **애플리케이션 Runtime Contract와 실제 Helm/IaC는 같은 문서가 아닙니다.** Application은 실행 능력과 의미를 제공하고 Platform은 환경별 실제 값을 선택합니다.

## 완료의 의미

Runtime Contract v0.1이 Git에 존재한다는 것은 **실행 인터페이스가 정의됐다는 뜻**입니다. 다음이 구현·검증됐다는 의미는 아닙니다.

- `labbit-server` / `labbit-connector` binary 구현
- Probe handler 구현
- multi-role/multi-replica routing
- graceful shutdown 통합 테스트
- 실제 Kubernetes Deployment/Service
- Prometheus/Loki 연결
- RDS Migration/Restore 검증

실제 구현값이 이 계약을 변경해야 한다면 먼저 `runtime/contract.yaml`을 수정하고, 의미가 바뀌는 경우에만 Confluence의 상위 설계를 함께 갱신합니다.
