# Labbit Test Code Convention

이 문서는 Labbit 저장소의 **테스트 코드 작성 기준**을 정의합니다.

상위 테스트 전략과 요구 → 검증 추적은 Confluence의
[검증 설계 — 테스트 전략·추적성](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637)을 따릅니다.
실제 자동 테스트 코드와 Fixture는 Git을 SSOT로 합니다.

## 공통 원칙

- 기능 추가 또는 동작 변경에는 변경된 동작을 검증하는 자동 테스트를 가능한 범위에서 함께 작성합니다.
- Bug fix에는 같은 문제가 재발하지 않도록 regression test를 추가합니다.
- 내부 함수 호출 순서보다 외부에서 관찰 가능한 동작, 계약, 상태 전이와 오류 의미를 우선 검증합니다.
- 단순 문구, CSS, 문서처럼 자동 테스트의 가치가 낮은 변경에는 새 테스트를 기계적으로 강제하지 않습니다.
- 테스트는 실행 순서나 다른 테스트의 side effect에 의존하지 않아야 합니다.
- 테스트를 위해 production code에 테스트 전용 우회 경로를 추가하지 않습니다.
- 실제 Credential, Password, Token, Secret 원문을 Fixture나 test log에 사용하지 않습니다.

## Go Unit Test

주 대상:

- Domain/Application logic
- 권한 판정
- 상태 전이와 정책 함수
- 오류 분기
- Idempotency, Retry, Reconciliation 같은 안전성 규칙

원칙:

- DB, OpenStack, 외부 HTTP/WSS 같은 외부 시스템을 unit test에서 직접 호출하지 않습니다.
- 외부 경계는 필요한 최소 interface, fake 또는 mock으로 대체합니다.
- 반복 입력을 검증할 때 table-driven test를 사용할 수 있지만 형식 자체를 강제하지 않습니다.
- concurrency/reconnect처럼 경쟁 조건이 correctness에 영향을 주는 변경은 해당 조건을 재현하는 회귀 테스트를 우선합니다.

## Backend HTTP / Service Integration

주 대상:

- HTTP Handler → Application → Repository 경계
- OpenAPI request/response/status/error 의미
- Auth/Session/Organization/Class 권한
- Transaction, constraint, concurrency

원칙:

- HTTP 외부 계약은 `contracts/http/openapi.yaml`을 기준으로 합니다.
- Handler transport 처리와 Business/Application Logic을 한 테스트에서 불필요하게 결합하지 않습니다.
- PostgreSQL 고유 제약이나 transaction 의미를 검증해야 할 때는 실제 PostgreSQL Integration Test를 사용합니다.

## Frontend Test

기본 도구는 Vitest + Testing Library입니다.

주 대상:

- 사용자에게 보이는 상태와 행동
- 권한별 화면과 Action
- Loading/Error/Empty/Disabled 상태
- 주요 keyboard/accessibility 동작
- HTTP consumer의 URL, Method, Cookie, encoding과 metadata 계약

원칙:

- Component 내부 구현보다 role, text, action, state 같은 사용자 관점의 결과를 우선 검증합니다.
- HTTP client는 fetch boundary에서 검증할 수 있습니다.
- Page/Flow 테스트는 `LabbitApi` 같은 외부 consumer boundary를 fake/mock하여 Backend 구현을 테스트 안에 복제하지 않습니다.
- CSS pixel/detail 자체는 별도 visual regression 체계가 생기기 전까지 unit test 대상으로 강제하지 않습니다.
- `capture:ui`는 현재 로컬 UI 리뷰 도구이며 자동 visual regression Gate로 취급하지 않습니다.

## DB Integration

- FK, Unique, partial unique index, transaction, locking, concurrency처럼 PostgreSQL 의미에 의존하는 검증은 실제 PostgreSQL에서 수행합니다.
- SQLite나 단순 in-memory DB를 PostgreSQL 고유 의미의 대체 검증으로 사용하지 않습니다.
- 구체적인 테스트 환경 도구(Testcontainers, CI service 등)는 해당 Story의 구현 요구에 맞춰 선택하고 이 문서에서 선행 고정하지 않습니다.
- 테스트가 생성한 DB 데이터는 다른 테스트에 영향을 주지 않도록 격리하거나 정리합니다.

## Connector Test

주 대상:

- Control message validation
- Handler → Provider 경계
- Heartbeat/Reconnect
- correlation
- UNKNOWN/Reconciliation
- Terminal/Preview lifecycle

원칙:

- Control/WSS 로직은 Mock SaaS / Mock Provider로 실제 Provider 없이 독립 검증할 수 있어야 합니다.
- Mock 통과를 실제 OpenStack E2E 성공으로 간주하지 않습니다.
- 실제 Provider 동작은 별도 OpenStack Integration Test에서 인증, 조회, 생성, 삭제 및 잔여 Resource 정리를 검증합니다.
- 결과 불명 Provider mutation이 동일 Create/Delete의 blind retry로 이어지지 않는지 검증합니다.

## Contract Test

- OpenAPI, Connector/Realtime JSON Schema, Runtime Contract, DB Migration은 각 Git SSOT를 기준으로 검증합니다.
- 계약 변경은 producer와 consumer 양쪽 영향도를 함께 확인합니다.
- CI validator가 강제하는 범위는 실제 workflow를 기준으로 하며, 문서가 구현되지 않은 Gate를 통과한 것으로 간주하지 않습니다.

## Mock / Fake 사용 원칙

권장 경계:

```text
Frontend          → LabbitApi / HTTP
Backend           → Repository / Connector 같은 external port
Connector Control → Provider
Provider E2E      → 실제 OpenStack
```

- Mock은 외부 경계를 격리하기 위해 사용합니다.
- 자기 코드 내부 함수 호출 순서를 그대로 expectation으로 고정하는 테스트는 피합니다.
- 리팩터링만으로 깨지는 테스트보다 제품/계약 동작이 바뀔 때 깨지는 테스트를 우선합니다.
- 설정하지 않은 Mock 동작을 실제 Provider 결과 불명으로 가장하지 않고 명시적인 test configuration error로 처리합니다.

## 실패·회귀 테스트

모든 오류 조합을 전부 작성할 필요는 없지만 해당 Story의 Acceptance Criteria와 관련된 대표 실패 경로는 검토합니다.

대표 예:

- 401 / 403 권한
- 409 충돌
- semantic validation
- stale update
- session expiry
- timeout / 결과 불명
- reconnect
- partial failure
- recovery / reconciliation

Bug fix는 수정 전 문제가 재현되는 조건을 테스트로 남기는 것을 기본으로 합니다.

## Coverage

MVP 단계에서는 전체 coverage percentage를 merge 기준으로 강제하지 않습니다.

coverage 숫자보다 다음 영역의 실질적 회귀 방지를 우선합니다.

- 핵심 도메인 규칙
- 권한 경계
- 계약 경계
- 상태 전이
- destructive action
- failure/recovery
- bug regression

코드 규모가 커져 수치가 실제 의사결정에 도움이 될 때 package/module별 coverage 추세를 별도로 검토합니다.

## PR 검증 기록

PR 작성자는 변경 범위에 맞는 자동 검증과 필요한 Integration/E2E 결과를 PR에 기록합니다.

자동 테스트가 적절하지 않은 변경은 테스트를 억지로 추가하는 대신 그 이유를 PR에 적습니다.
자동 테스트 통과만으로 Integration/E2E 또는 사용자 Story의 Acceptance가 자동 완료됐다고 간주하지 않습니다.
