# Labbit Application Contract SSOT

이 저장소는 Labbit 애플리케이션의 P0 계약 산출물을 Git에서 관리하기 위한 SSOT 구조입니다.

## SSOT

- HTTP API 계약: `contracts/http/openapi.yaml`
- SaaS ↔ Connector Control WSS 계약: `contracts/connector/README.md` + `contracts/connector/connector.schema.json`
- Connector TerminalSession lifecycle Control: `contracts/connector/terminal-control.schema.json`
- Connector Terminal Data WSS: `contracts/connector/terminal-data.schema.json`
- Browser Terminal/Live WSS: `contracts/realtime/README.md` + `contracts/realtime/terminal-live.schema.json`
- PostgreSQL Physical Schema 초안: `db/migrations/*.sql` + `db/migrations/README.md`
- Application Runtime Contract: `runtime/contract.yaml` + `runtime/README.md`

실제 Kubernetes/AWS 배포 설정은 별도 Platform/GitOps 경계에서 Helm/IaC/GitOps로 관리하고, 애플리케이션 계약에 Istio·SQS/Kafka·Valkey·KEDA 같은 내부 인프라 구현을 노출하지 않습니다.

## 현재 계약 상태

- **HTTP Core v0.1 정의됨** — OpenAPI 3.1.2, `/api/v1`, Auth/Class/LabSpec/LabExecution/LabInstance/Operation.
- **Connector Control WSS v0.1 정의됨** — outbound WSS 443, Connector Credential 인증, `labbit.connector.v1`, HELLO/Heartbeat/Provider query/Operation/Reconciliation, 안전한 결과 불명 처리와 correlation 계약.
- **Terminal/Live WSS v0.1 정의됨** — Browser Terminal/Live subprotocol, JSON control + Binary PTY byte stream, 60초 PTY grace, 기록 없는 reconnect, Live read-only fan-out, bounded Queue/slow consumer, Session 종료 의미.
- **Connector Terminal Data v0.1 정의됨** — TerminalSession lifecycle은 persistent Control WSS로 전달하고, 실제 PTY bytes는 active TerminalSession별 별도 Connector outbound Data WSS로 중계.
- **Domain/Data Model 확정** — Organization/User/Class/LabSpec/LabExecution/LabInstance/Operation/OperationItem/ProviderResource/TerminalSession/LiveSession의 핵심 관계와 ownership, 주요 불변조건을 확정.
- **PostgreSQL Physical Schema Draft v0.1 작성됨** — 초기 5개 SQL Migration과 D-25의 additive `000006` 초안. 아직 실제 개발 PostgreSQL 적용·pgx Query·Migration runner·통합 검증 전이며, 최초 공용 개발 DB 적용 전까지 구현 피드백에 따라 정리할 수 있음.
- **HTTP 후속 범위** — Organization/Provider 관리, File, Preview, Terminal/Live Session 생성·종료 control API, Session TTL/회전·구체 CSRF 방어.
- **Runtime Contract v0.1.1 정의됨** — 기존 실행 경계에 SaaS OpenTelemetry/OTLP, Operation의 durable Trace Context, Connector propagation-only, 관측 장애의 업무 격리 계약 추가. 실제 계측·전파·Tempo E2E 구현 완료는 아님.

HTTP의 장시간 Provision/Reset/Cleanup은 durable `Operation`으로 노출하고 `Idempotency-Key`로 중복 요청을 제어합니다. DB 초안에서는 Browser가 보는 `operations`와 LabInstance별 실행 단위 `operation_items`를 분리하고, LabInstance당 active Mutation 최대 1개를 partial unique index로 표현합니다. 결과가 불명확한 Provider 작업은 동일 Create/Delete를 자동 반복하지 않고 Reconciliation을 먼저 수행합니다.

Reset 재현 기준은 immutable `CreationSnapshot`이며, Physical Schema 초안에서는 `creation_snapshots.snapshot` JSONB와 generation별 `provider_resources` 이력으로 이를 표현합니다. PostgreSQL은 제품 소유관계·작업 의도·이력을 보존하고 실제 Provider 존재/상태는 Connector가 조회한 OpenStack 현실과 대조합니다.

Terminal/Live는 Control과 Data를 분리합니다. Browser-facing Terminal/Live WSS와 Connector Terminal Data WSS는 실제 PTY byte stream을 Binary frame으로 중계하며, session lifecycle·권한·종료 의미는 JSON control message로 관리합니다. DB 초안에는 Terminal/Live 최소 lifecycle metadata만 두고 INPUT/OUTPUT, Transcript, subscriber Queue 내용은 저장하지 않습니다.

내부 작업 전달이 PostgreSQL polling에서 SQS/Kafka 등으로 바뀌거나 Connector Control connection owner가 API/전용 workload/registry 구조로 바뀌어도 Browser HTTP, 외부 WSS 계약, durable Operation/ProviderResource 모델을 유지하는 것을 원칙으로 합니다.

현재 Git 계약과 SQL 초안의 존재는 기능 구현·배포·검증 완료를 뜻하지 않습니다. 실제 백엔드 개발 시작 시 PostgreSQL에 초안을 적용하고 pgx repository/query, Migration runner, Worker claim, Integration/복구 테스트를 통해 검증합니다. 최초 공용 개발 DB에 적용된 baseline 이후에는 기존 Migration을 수정하지 않고 새 번호 Migration을 추가합니다.


## 개발 스켈레톤

개발 시작 전 최소 실행 골격만 제공합니다. 기능 package는 Jira Story가 시작될 때 필요한 만큼 추가합니다.

```text
cmd/
├─ labbit-server/       # 중앙 SaaS 실행 진입점
└─ labbit-connector/    # 고객 환경 Connector 실행 진입점

internal/
├─ server/app/          # Runtime role·listener·graceful shutdown bootstrap
├─ connector/app/       # Connector process lifecycle bootstrap
└─ observability/       # 공통 JSON logging bootstrap

web/                    # React + TypeScript + Vite 정적 SPA
```

로컬 기본 확인:

```bash
make setup
make test

make server
make connector
make web
```

`labbit-server`는 현재 Runtime Contract의 application/admin listener와 `/livez`, `/readyz`, `/metrics` 골격만 제공합니다. Auth/Class/LabSpec/Operation, DB repository, Connector Control/Provider/SSH, Terminal/Live 같은 실제 기능 구현 완료를 의미하지 않습니다.

Git/PR 규칙은 [CONTRIBUTING.md](./CONTRIBUTING.md)를 따릅니다. Commit/PR 제목은 **영어 prefix + 한글 description** 형식을 사용하고, merge는 **3-way merge 기반 Create a merge commit**을 기본으로 합니다.
