# Labbit Application Contract SSOT

이 저장소는 Labbit 애플리케이션의 P0 계약 산출물을 Git에서 관리하기 위한 SSOT 구조입니다.

## SSOT

- HTTP API 계약: `contracts/http/openapi.yaml`
- SaaS ↔ Connector Control WSS 계약: `contracts/connector/README.md` + `contracts/connector/connector.schema.json`
- Connector TerminalSession lifecycle Control: `contracts/connector/terminal-control.schema.json`
- Connector Terminal Data WSS: `contracts/connector/terminal-data.schema.json`
- Connector Workspace File lifecycle Control / File Data WSS: `contracts/connector/file-control.schema.json`, `contracts/connector/file-data.schema.json`
- Connector PreviewSession lifecycle Control / Preview Data WSS: `contracts/connector/preview-control.schema.json`, `contracts/connector/preview-data.schema.json`
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
- **PostgreSQL Physical Schema Draft v0.1 작성됨** — 초기 5개 SQL Migration과 D-25의 additive `000006` 초안. 별도 Migration runner와 폐기 가능한 PostgreSQL 16의 전체/`000005→000006` 적용 Integration Test에 더해, Auth/Class 다음 Vertical Slice용 최소 pgx Repository/Transaction 경계와 실제 PostgreSQL 제약(FK/Unique/Check/partial unique) Integration Test가 있으나, 공용 개발 DB 적용과 나머지 도메인 Repository는 아직이며 최초 공용 개발 DB 적용 전까지 구현 피드백에 따라 정리할 수 있음.
- **Auth/Session 구현 계약 v0.1 준비됨** — docs/backend/auth-session.md에서 8시간 absolute Session, fresh login token, Argon2id Password hash, raw Session token 비저장, Origin/Referer 검증 기준을 정의합니다. 실제 Handler와 Auth use case 구현은 LBT-10에서 추적합니다.
- **Workspace File HTTP/Connector transport 정의됨** — Tree/Read/Save HTTP(`ETag`/`If-Match`), workspace-relative canonical path 규칙, Control(`file-v1` capability, lifecycle/correlation만)과 요청별 File Data WSS(JSON control + 파일 본문 Binary frame) 분리. 실제 SSH/SFTP·Workspace root·symlink containment 구현과 실제 VM 검증은 Connector OP-02(LBT-21) 범위입니다.
- **Preview HTTP/Connector transport 정의됨** — PreviewSession 생성·종료 HTTP(`POST /lab-instances/{id}/preview-sessions`, `DELETE /preview-sessions/{id}`), SaaS 본 서비스와 분리된 Preview Origin의 bootstrap/교환(`/__labbit/bootstrap`, `/__labbit/exchange`)과 Preview 전용 Cookie, Control(`preview-v1` capability, `PREVIEW_OPEN`/`PREVIEW_OPEN_RESULT`/`PREVIEW_CLOSE`, lifecycle/correlation과 승인된 target만)과 PreviewSession별 Preview Data WSS(`PREVIEW_ATTACH`/`PREVIEW_ATTACHED` 뒤 Binary byte stream, WSS 하나 = Workspace VM TCP 연결 하나). 허용 port는 Backend가 명시적으로 설정한 목록(`LABBIT_PREVIEW_ALLOWED_PORTS`)이 승인하며 숫자 범위나 기본 port는 없습니다. 실제 SSH TCP forwarding(LBT-24)과 Connector의 Preview WSS client(LBT-23), 실제 VM 왕복(LBT-25 C3)은 이 계약의 구현 범위가 아닙니다.
- **HTTP 후속 범위** — Organization/Provider 관리, LiveSession 생성·종료 control API. Terminal 대상 VM 조회와 TerminalSession 생성·종료, Preview 생성·종료 control API는 정의되어 있습니다.
- **Runtime Contract v0.1.6 정의됨** — v0.1.6에서 `preview` role의 PreviewSession/Gateway 경계(`saas.preview`)와 `LABBIT_PREVIEW_ALLOWED_PORTS`·`LABBIT_PREVIEW_SESSION_TTL`·`LABBIT_PREVIEW_ORIGIN_TEMPLATE`을 추가했습니다(preview role은 DB DSN을 요구하지 않으며 v0.1에서는 DB-backed authority를 제공하는 `api` role과의 co-location이 필요, PreviewSession 상태는 process 안의 ephemeral 상태). v0.1.5에서 Workspace File Tree/Read/Save를 `api` role이 소유하고 Connector File Data WSS도 같은 process에서 받으며 요청 상태는 process 안의 ephemeral 상태라는 경계(`saas.file`)를 추가했습니다. v0.1.4에서 Browser Terminal/Live WSS의 strict Origin 검증을 위해 `api` 또는 `realtime` role에 `LABBIT_PUBLIC_ORIGIN` 요구를 반영했고, v0.1.3의 one-shot `labbit-migrate`, api/worker DB DSN, rolling schema compatibility와 기존 OTel/OTLP·durable Trace Context·Connector propagation-only 계약을 유지합니다. HTTP/Realtime Application Metric은 LBT-142에서 실제 Prometheus exposition과 lifecycle 계측이 main에 반영됐습니다. Worker Metric은 실제 Worker 실행 경로(LBT-18) 전에는 만들지 않으며, OTel/OTLP exporter와 Tempo E2E는 아직 후속 범위입니다.

HTTP의 장시간 Provision/Reset/Cleanup은 durable `Operation`으로 노출하고 `Idempotency-Key`로 중복 요청을 제어합니다. DB 초안에서는 Browser가 보는 `operations`와 LabInstance별 실행 단위 `operation_items`를 분리하고, LabInstance당 active Mutation 최대 1개를 partial unique index로 표현합니다. 결과가 불명확한 Provider 작업은 동일 Create/Delete를 자동 반복하지 않고 Reconciliation을 먼저 수행합니다.

Reset 재현 기준은 immutable `CreationSnapshot`이며, Physical Schema 초안에서는 `creation_snapshots.snapshot` JSONB와 generation별 `provider_resources` 이력으로 이를 표현합니다. PostgreSQL은 제품 소유관계·작업 의도·이력을 보존하고 실제 Provider 존재/상태는 Connector가 조회한 OpenStack 현실과 대조합니다.

Terminal/Live는 Control과 Data를 분리합니다. Browser-facing Terminal/Live WSS와 Connector Terminal Data WSS는 실제 PTY byte stream을 Binary frame으로 중계하며, session lifecycle·권한·종료 의미는 JSON control message로 관리합니다. DB 초안에는 Terminal/Live 최소 lifecycle metadata만 두고 INPUT/OUTPUT, Transcript, subscriber Queue 내용은 저장하지 않습니다.

내부 작업 전달이 PostgreSQL polling에서 SQS/Kafka 등으로 바뀌거나 Connector Control connection owner가 API/전용 workload/registry 구조로 바뀌어도 Browser HTTP, 외부 WSS 계약, durable Operation/ProviderResource 모델을 유지하는 것을 원칙으로 합니다.

현재 Git 계약과 SQL 초안의 존재는 기능 구현·배포·검증 완료를 뜻하지 않습니다. 실제 백엔드 개발 시작 시 PostgreSQL에 초안을 적용하고 pgx repository/query, Migration runner, Worker claim, Integration/복구 테스트를 통해 검증합니다. 최초 공용 개발 DB에 적용된 baseline 이후에는 기존 Migration을 수정하지 않고 새 번호 Migration을 추가합니다.


## 개발 스켈레톤

개발 시작 전 최소 실행 골격만 제공합니다. 기능 package는 Jira 업무가 시작될 때 필요한 만큼 추가합니다.

```text
cmd/
├─ labbit-server/       # 중앙 SaaS 실행 진입점
├─ labbit-migrate/      # rollout 전 별도 단계로 실행하는 SQL Migration runner
└─ labbit-connector/    # 고객 환경 Connector 실행 진입점

internal/
├─ server/app/          # Runtime role·listener·readiness·graceful shutdown bootstrap
├─ server/repository/   # Application이 의존하는 Repository/Transaction port와 typed error (pgx 비의존)
├─ server/bootstrap/    # trusted operator Bootstrap use case (하나의 transaction)
├─ postgres/            # DSN 입력·pgx 연결·Migration 적용·schema 호환성 확인·Repository/Transaction 구현
├─ connector/app/       # Connector process lifecycle bootstrap
└─ observability/       # 공통 JSON logging + Application Prometheus metrics

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

`labbit-server`는 현재 Runtime Contract의 application/admin listener와 `/livez`, `/readyz`, 실제 Prometheus exposition `/metrics`, api/worker role의 PostgreSQL 연결·schema 호환성 readiness를 제공합니다. `api` role의 HTTP request count/duration/inflight와 실제 `api,realtime` Terminal 경로의 connection/reconnect/rejection/draining Metric을 low-cardinality label로 노출하며, 실행 경로가 없는 Worker/Live Metric은 만들지 않습니다. `api` role에는 Auth/Session HTTP(`POST /api/v1/auth/login`, `GET /api/v1/me`, `POST /api/v1/auth/logout`), Class 목록·상세 권한 경계, Connector Control WSS의 인증·HELLO/Heartbeat·연결 수명 및 command/result correlation routing이 구현되어 있습니다. `api` role은 Origin 검증을 위한 `LABBIT_PUBLIC_ORIGIN`이 필요하며 `make server`는 Vite dev origin(`LABBIT_DEV_PUBLIC_ORIGIN`, 기본 `http://localhost:5173`)을 주입합니다. `api`와 `realtime` role을 같은 process에서 함께 켜면(`LABBIT_RUNTIME_ROLES=api,realtime`) Terminal 대상 VM 조회(`GET /api/v1/lab-instances/{id}/terminal-targets`)와 TerminalSession 생성·종료 HTTP(`POST /api/v1/lab-instances/{id}/terminal-sessions`, `DELETE /api/v1/terminal-sessions/{id}`), Browser Terminal WSS(`/realtime/v1/terminal`), Connector Terminal Data WSS(`/connector/v1/terminal-data`)와 Terminal lifecycle Control(`TERMINAL_OPEN`/`CLOSE`/`ENDED`)이 계약 peer 기준으로 구현되어 있습니다. `realtime` role은 PostgreSQL DSN을 요구하지 않지만 v0.1에서는 DB-backed authority를 제공하는 `api` role과의 co-location이 필요합니다. 같은 `api` role에는 Workspace File Tree/Read/Save HTTP(`GET /api/v1/lab-instances/{id}/files/tree`, `GET`/`PUT /api/v1/lab-instances/{id}/files/content`, `ETag`/`If-Match`)와 Connector File Data WSS(`/connector/v1/file-data`), File lifecycle Control(`FILE_OPEN`/`FILE_OPEN_RESULT`/`FILE_CLOSE`, Connector HELLO의 `file-v1` capability)도 계약 peer 기준으로 구현되어 있으며 `realtime` role은 필요하지 않습니다. 실제 Workspace VM의 SSH/SFTP 기반 file 검증(SFTP 구현, Workspace root, symlink containment)은 LBT-21과 후속 Workspace 통합 범위입니다. `api`와 `preview` role을 같은 process에서 함께 켜면(`LABBIT_RUNTIME_ROLES=api,preview`, `LABBIT_PREVIEW_ALLOWED_PORTS`·`LABBIT_PREVIEW_SESSION_TTL`·`LABBIT_PREVIEW_ORIGIN_TEMPLATE` 필수) PreviewSession 생성·종료 HTTP(`POST /api/v1/lab-instances/{id}/preview-sessions`, `DELETE /api/v1/preview-sessions/{id}`), request Host로 구분되는 별도 Preview Origin의 Gateway(bootstrap/교환, Preview 전용 Cookie 인증, HTTP/1.1 reverse proxy), Connector Preview Data WSS(`/connector/v1/preview-data`)와 Preview lifecycle Control(`PREVIEW_OPEN`/`PREVIEW_OPEN_RESULT`/`PREVIEW_CLOSE`, Connector HELLO의 `preview-v1` capability)이 계약 peer 기준으로 구현되어 있습니다. `preview` role도 PostgreSQL DSN을 요구하지 않지만 v0.1에서는 `api` role과의 co-location이 필요하며 `preview`만 enabled된 process는 `/readyz`가 실패합니다. PreviewSession은 PostgreSQL에 저장하지 않는 ephemeral 상태이고 Preview 본문·경로·Cookie는 Control, DB, log에 남기지 않습니다. 실제 Workspace VM SSH TCP forwarding(LBT-24), Connector의 Preview WSS client(LBT-23), 실제 VM 왕복(LBT-25 C3)은 후속 범위입니다. LabSpec/Execution/Operation HTTP, durable Operation Worker/Reconciliation, Live Backend 경로는 후속 범위입니다. 실제 VM Terminal 검증은 단계가 나뉩니다. LBT-20에서 Connector→실제 Workspace VM SSH/PTY INPUT/OUTPUT·resize·close를 먼저 검증하고, 이후 LBT-22(C2)에서 Browser/SaaS TerminalSession → Connector Transport → 실제 VM SSH/PTY 전체 왕복과 reconnect/close를 공동 통합 검증합니다. 상세 Backend 구현 위치와 책임 경계는 [`docs/backend/README.md`](./docs/backend/README.md)를 기준으로 확인합니다.

### Local PostgreSQL

Docker Compose로 PostgreSQL 16만 실행합니다. `compose.yaml`의 credential은 로컬 폐기용 dummy 값입니다.

```bash
make dev-db-up          # PostgreSQL 16 시작, healthcheck 통과까지 대기
make dev-db-migrate     # 별도 runner로 db/migrations 적용 (server startup은 적용하지 않음)
make server             # api role, /readyz는 Migration 적용 후 200
make go-integration-test
make dev-db-down        # container만 중지, DB volume은 유지
```

- 5432 포트가 이미 사용 중이면 `LABBIT_DEV_DB_PORT=15432 make dev-db-up`처럼 모든 target에 같은 값을 지정합니다.
- 다른 container runtime을 쓰면 `COMPOSE="podman compose"`처럼 지정할 수 있습니다.
- `make go-integration-test`는 개발 DB(`labbit`)가 아니라 같은 server에 test별 임시 database를 만들고 삭제합니다.
- 개발 DB를 완전히 초기화해야 할 때만 `docker compose down -v`를 직접 실행합니다. 이 명령은 volume의 데이터를 삭제합니다.

#### Local Auth/Class fixture

Local Browser 검증에서 실제 Backend HTTP로 로그인·Class 목록을 확인할 수 있도록 dev-only fixture를 저장합니다. 기존 Bootstrap use case(`bootstrap.Run`)를 재사용하는 실행기이며 **production Bootstrap 방법이 아닙니다.**

```bash
make dev-db-up
make dev-db-migrate
make dev-auth-class-fixture
make server             # 별도 terminal에서 make web
```

- Password는 실행 시 무작위로 생성해 `.local/dev-auth-class-fixture.json`(gitignore, mode 0600)에만 저장하며 DB에는 Argon2id hash만 들어갑니다. Git에 commit하지 않습니다.
- Migration은 실행하지 않으며 기존 row를 삭제·갱신하지 않습니다. 이미 fixture가 있으면 아무것도 저장하지 않고 실패합니다. 다시 만들려면 개발 DB를 직접 초기화합니다.
- Organization `Labbit Local Dev`, Class `Dev Alpha`/`Dev Bravo`:

| 계정 | Organization role | Class Membership |
| --- | --- | --- |
| `dev-admin` | ADMIN | 없음 |
| `dev-instructor` | MEMBER | Alpha INSTRUCTOR, Bravo STUDENT |
| `dev-student` | MEMBER | Alpha STUDENT |

Backend 구현 경계는 [docs/backend/README.md](./docs/backend/README.md), Auth/Session 구현 기준은 [docs/backend/auth-session.md](./docs/backend/auth-session.md), 테스트 규칙은 [TESTING.md](./TESTING.md)를 따릅니다.

Git/PR 규칙은 [CONTRIBUTING.md](./CONTRIBUTING.md)를 따릅니다. Commit은 Conventional 형식을 사용하고, Jira Task가 있는 PR은 제목에 `LBT-*`를 남기며, `main` 반영은 **Squash Merge**를 사용합니다.
