# Labbit AI 에이전트 지침

이 파일은 Codex를 포함한 AI 코딩 에이전트가 저장소 전체에서 따라야 할 공통 지침을 정의합니다.

## 작업 원칙

- 브랜치, Commit, PR, Merge 규칙은 `CONTRIBUTING.md`를 따릅니다.
- 테스트 코드 작성 기준은 `TESTING.md`를 따릅니다.
- 변경 범위는 Jira 작업 또는 명시된 작업 목적에 집중합니다.
- 기존 SSOT가 관리하는 제품·아키텍처 결정을 코드 주석이나 임의 문서에 중복 정의하지 않습니다.
- producer 또는 consumer를 변경하기 전에 적용되는 계약을 먼저 확인합니다.

## 저장소 SSOT

- HTTP API: `contracts/http/openapi.yaml`
- SaaS ↔ Connector 프로토콜: `contracts/connector/`
- Browser Terminal/Live 프로토콜: `contracts/realtime/`
- PostgreSQL Physical Schema: `db/migrations/`
- Application Runtime Contract: `runtime/`

## Backend 구현 가이드

- Backend 책임/의존 방향: docs/backend/README.md
- Auth/Session 구현 계약: docs/backend/auth-session.md

Backend 작업은 상위 제품/HTTP/DB/Runtime SSOT를 바꾸지 않는 범위에서 이 구현 가이드를 따릅니다. 구현 중 계약 변경이 필요하면 가이드만 수정하지 않고 owning SSOT를 함께 수정합니다.

구현은 적용되는 SSOT와 모순되는 field, state, protocol behavior, runtime semantics를 임의로 만들지 않습니다. 계약 자체가 변경되어야 한다면 계약을 명시적으로 수정하고 영향을 받는 producer와 consumer를 함께 확인합니다.

## 검증

작업 중에는 변경 범위에 맞는 최소 검증을 사용하고, 의미 있는 작업을 완료하기 전에는 저장소 전체 기본 검증을 실행합니다. 테스트 레이어와 Mock/Fake 선택은 `TESTING.md`를 따릅니다.

```bash
make setup
make test
```

필요한 검증은 개별 target으로도 실행할 수 있습니다.

```bash
make go-fmt-check
make go-vet
make go-test
make go-build
make web-typecheck
make web-lint
make web-test
make web-build
```

계약 변경은 GitHub Actions의 `Contracts / validate` 검증에서도 파싱 가능해야 합니다.

## Code Review Rules

리뷰는 correctness, contract drift, security, data integrity, operational safety에 집중합니다. CI가 안정적으로 강제하는 formatting, lint, type-check 문제는 behavioral defect를 드러내는 경우가 아니라면 중복 지적하지 않습니다.

동작 변경이나 Bug fix에서는 기존 테스트가 통과하는지만 보지 않고, `TESTING.md` 기준에 따라 변경된 행동 또는 재발 조건을 검증하는 테스트가 필요한지 확인합니다. 단순 문구/CSS/문서 변경에 새 테스트를 기계적으로 요구하지 않습니다.

### 계약 경계

- 적용되는 Git SSOT와 다른 구현 또는 consumer 동작을 지적합니다.
- owning contract를 수정하지 않은 채 문서화되지 않은 API field, state, protocol message, semantics를 추가하는 변경을 지적합니다.
- 계약이 변경되면 계약 파일만 보지 않고 영향을 받는 producer와 consumer를 함께 확인합니다.

### Durable Operation과 Provider Reconciliation

- Provision, Reset, Cleanup은 durable asynchronous Operation입니다. request lifetime 안에서 동기 작업으로 바꾸거나 기존 Operation/idempotency model을 우회하는 변경을 지적합니다.
- Provider 작업 결과가 불명확하면 같은 Create/Delete를 무작정 반복하기 전에 Reconciliation을 수행해야 합니다.

### Reset 재현성

- Reset은 해당 LabInstance generation의 immutable CreationSnapshot을 기준으로 재현해야 합니다. 기존 인스턴스를 현재의 mutable LabSpec으로 다시 만드는 코드를 지적합니다.

### Terminal과 Live

- Terminal input/output, transcript, Live subscriber queue 내용은 저장하지 않습니다. 필요한 lifecycle metadata만 영속합니다.
- Terminal/Live traffic의 control/data 분리를 유지합니다.
- Live는 read-only입니다. Live viewer가 TerminalSession에 write할 수 있게 만드는 변경을 지적합니다.

### 민감정보와 Logging

- Credential, Secret, Password, Session Secret, Connector Credential, 민감한 Provider raw payload를 commit·log·API로 노출하는 변경을 지적합니다.
- Log는 민감한 값을 노출하지 않으면서 필요한 correlation 정보를 유지해야 합니다.

### Runtime 경계

- 기존 Runtime Contract가 요구하는 범위에서는 application-facing contract를 내부 infrastructure 구현 선택과 분리합니다.
- 명시적인 contract decision 없이 구현 세부 infrastructure 정보를 외부 HTTP/WSS 계약에 노출하는 변경을 지적합니다.
