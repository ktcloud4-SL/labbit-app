# Labbit Application Contract SSOT

이 저장소는 Labbit 애플리케이션의 P0 계약 산출물을 Git에서 관리하기 위한 SSOT 구조입니다.

## SSOT

- HTTP API 계약: `contracts/http/openapi.yaml`
- SaaS ↔ Connector Control WSS 계약: `contracts/connector/README.md` + `contracts/connector/connector.schema.json`
- Browser Terminal/Live WSS 계약: `contracts/realtime/terminal-live.schema.json`
- PostgreSQL Physical Schema: `db/migrations/*.sql`
- Application Runtime Contract 시작점: `runtime/README.md`

실제 Kubernetes/AWS 배포 설정은 별도 Platform/GitOps 경계에서 Helm/IaC/GitOps로 관리하고, 애플리케이션 계약에 Istio·SQS/Kafka·Valkey·KEDA 같은 내부 인프라 구현을 노출하지 않습니다.

## 현재 계약 상태

- **HTTP Core v0.1 정의됨** — OpenAPI 3.1.2, `/api/v1`, Auth/Class/LabSpec/LabExecution/LabInstance/Operation.
- **Connector Control WSS v0.1 정의됨** — outbound WSS 443, Connector Credential 인증, `labbit.connector.v1`, HELLO/Heartbeat/Provider query/Operation/Reconciliation, 안전한 결과 불명 처리와 correlation 계약.
- **HTTP 후속 범위** — Organization/Provider 관리, File, Preview, Terminal/Live control, Session TTL/회전·구체 CSRF 방어.
- **Terminal·Live WSS / DB Physical Schema / Runtime Contract** — 후속 계약 설계에서 구체화.

HTTP의 장시간 Provision/Reset/Cleanup은 durable `Operation`으로 노출하고 `Idempotency-Key`로 중복 요청을 제어합니다. Connector WSS에서는 하나의 HTTP Operation이 여러 LabInstance command로 fan-out될 수 있으며, 결과가 불명확한 Provider 작업은 동일 Create/Delete를 자동 반복하지 않고 Reconciliation을 먼저 수행합니다.

내부 작업 전달이 PostgreSQL polling에서 SQS/Kafka 등으로 바뀌거나 Connector Control connection owner가 API/전용 workload/registry 구조로 바뀌어도 Browser HTTP 및 Connector wire contract를 유지하는 것을 원칙으로 합니다.

현재 Git 계약의 존재는 기능 구현·배포·검증 완료를 뜻하지 않습니다. DB Column, Runtime 수치, Terminal/Live Close code 등은 각 후속 계약 설계 단계에서 확정합니다.
