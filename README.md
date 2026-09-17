# Labbit Application Contract SSOT

이 저장소는 Labbit 애플리케이션의 P0 계약 산출물을 Git에서 관리하기 위한 SSOT 구조입니다.

## SSOT

- HTTP API 계약: `contracts/http/openapi.yaml`
- SaaS ↔ Connector WSS 계약: `contracts/connector/connector.schema.json`
- Browser Terminal/Live WSS 계약: `contracts/realtime/terminal-live.schema.json`
- PostgreSQL Physical Schema: `db/migrations/*.sql`
- Application Runtime Contract 시작점: `runtime/README.md`

실제 Kubernetes/AWS 배포 설정은 별도 Platform/GitOps 경계에서 Helm/IaC/GitOps로 관리하고, 애플리케이션 계약에 Istio·SQS/Kafka·Valkey·KEDA 같은 내부 인프라 구현을 노출하지 않습니다.

## 현재 계약 상태

- **HTTP Core v0.1 정의됨** — OpenAPI 3.1.2, `/api/v1`, Auth/Class/LabSpec/LabExecution/LabInstance/Operation.
- **HTTP 후속 범위** — Organization/Provider 관리, File, Preview, Terminal/Live control, Session TTL/회전·구체 CSRF 방어.
- **Connector WSS / Terminal·Live WSS / DB Physical Schema / Runtime Contract** — 후속 계약 설계에서 구체화.

HTTP의 장시간 Provision/Reset/Cleanup은 durable `Operation`으로 노출하고 `Idempotency-Key`로 중복 요청을 제어합니다. 내부 작업 전달이 PostgreSQL polling에서 SQS/Kafka 등으로 바뀌거나 서비스가 분리되어도 Browser HTTP 계약을 유지하는 것을 원칙으로 합니다.

현재 Git 계약의 존재는 기능 구현·배포·검증 완료를 뜻하지 않습니다. 아직 확정되지 않은 WebSocket Close code, DB Column, Runtime 수치 등은 각 계약 설계 단계에서 확정합니다.
