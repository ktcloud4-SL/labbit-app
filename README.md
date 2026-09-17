# Labbit Application Contract SSOT

이 저장소는 Labbit 애플리케이션의 P0 계약 산출물을 Git에서 관리하기 위한 최소 SSOT 구조를 둡니다.

## SSOT

- HTTP API 계약: `contracts/http/openapi.yaml`
- SaaS ↔ Connector WSS 계약: `contracts/connector/connector.schema.json`
- Browser Terminal/Live WSS 계약: `contracts/realtime/terminal-live.schema.json`
- PostgreSQL Physical Schema: `db/migrations/*.sql`
- Application Runtime Contract 시작점: `runtime/README.md`

실제 Kubernetes/AWS 배포 설정은 향후 별도 `labbit-platform` 저장소의 Helm/IaC/GitOps에서 관리합니다.

현재 단계의 목적은 기능 구현이 아니라 계약 SSOT 위치를 확립하는 것입니다. 아직 확정되지 않은 Endpoint, 필드, WebSocket Close code, DB Column, Runtime 수치는 각 계약 설계 단계에서 확정합니다.
