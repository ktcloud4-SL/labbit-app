# Labbit 개발 협업 규칙

이 문서는 `labbit-app` 개발에 필요한 최소 Git/PR 및 repository-level 검증 규칙을 정의합니다. 기능·도메인 정책은 Confluence, 기계 판독 계약은 `contracts/`, `db/`, `runtime/`의 각 SSOT를 따릅니다.

## 브랜치

브랜치 이름은 ASCII로 작성합니다.

관련 Jira가 있는 작업은 Jira key를 사용합니다.

```text
<type>/<jira-key>-<english-slug>
```

예:

```text
feat/SL-123-provision-targets
fix/SL-214-reset-conflict
docs/SL-301-runtime-contract
```

별도 Jira로 추적할 필요가 없는 **소규모 repository maintenance 또는 기존 review follow-up**은 다음 예외 형식을 사용할 수 있습니다.

```text
<type>/<english-slug>
```

예:

```text
fix/repo-module-path
refactor/frontend-confirmation-dialog
chore/testing-convention-go-gate
```

기존에 관련 Jira가 있다면 예외 형식보다 해당 Jira key를 재사용합니다. 사용자 기능, HTTP/WSS 계약, DB/보안/Runtime 의미 변경 또는 여러 PR에 걸쳐 추적해야 하는 작업은 Jira 연결을 우선합니다.

## Commit / PR 제목

**영어 prefix + 한글 description**을 사용합니다.

```text
<type>(<scope>): <한글 설명>
```

예:

```text
feat(api): 대상 학생 선택 Provision 기능 추가
fix(operation): Reset 중복 요청 충돌 처리 수정
refactor(connector): OpenStack Provider Adapter 책임 분리
docs(runtime): Graceful Shutdown 설명 보강
test(operation): Reconciliation 통합 테스트 추가
chore(repo): CODEOWNERS 추가
ci(repo): 기본 검증 워크플로 추가
build(web): Vite 빌드 설정 추가
```

Breaking change는 `!`를 사용합니다.

```text
feat(api)!: LabExecution 생성 요청 구조 변경
```

### Type

- `feat`: 기능 또는 계약 기능 추가
- `fix`: 버그 또는 잘못된 계약 수정
- `refactor`: 동작 변화 없는 구조 개선
- `docs`: 문서·description 변경
- `test`: 테스트 추가·수정
- `chore`: 저장소 관리·기타 작업
- `ci`: CI/CD 설정
- `build`: 빌드·의존성·artifact 관련 변경
- `perf`: 성능 개선
- `revert`: 변경 되돌리기

### Scope

초기 권장 scope는 다음과 같습니다.

```text
repo
api
auth
class
labspec
execution
operation
connector
realtime
preview
db
runtime
web
ci
```

새 scope는 실제 책임 경계가 생겼을 때만 추가합니다.

## Pull Request

- `main`에 직접 push하지 않고 PR을 사용합니다.
- PR 제목도 Commit Convention과 같은 형식을 사용합니다.
- 한 PR은 가능한 한 하나의 Jira Story 또는 하나의 명확한 목적에 집중합니다.
- 관련 Jira가 있으면 PR에 연결하고, Jira가 없는 예외 작업은 PR에 그 이유를 명시합니다.
- 계약 파일을 바꾸면 해당 producer/consumer 영향도 함께 확인합니다.
- 생성된 코드가 있다면 생성 원본과 생성 방법을 PR에 명시합니다.

## 테스트와 검증

테스트 코드 작성 기준은 [TESTING.md](./TESTING.md)를 따릅니다.

- 기능 또는 동작 변경 PR은 변경 동작에 대한 자동 테스트 필요 여부를 함께 검토합니다.
- Bug fix는 가능한 경우 동일 문제가 다시 발생하지 않도록 regression test를 추가합니다.
- 자동 테스트가 적절하지 않은 변경은 PR에 그 이유를 남깁니다.
- Integration/E2E가 Acceptance 기준인 작업은 unit test 통과만으로 완료로 판단하지 않습니다.

## Merge 전략

이 저장소는 **3-way merge 기반의 GitHub `Create a merge commit`** 방식을 사용합니다.

- `Squash and merge`를 기본 전략으로 사용하지 않습니다.
- `Rebase and merge`를 기본 전략으로 사용하지 않습니다.
- PR의 개별 commit을 main history에 유지합니다.
- merge commit 제목도 가능하면 `<type>(<scope>): <한글 설명>` 형식으로 작성합니다.

따라서 feature branch의 commit도 리뷰 가능한 단위로 정리하고 `fix typo`, `wip` 같은 의미 없는 commit을 남발하지 않습니다.

## CODEOWNERS

Labbit의 모든 코드 변경은 개발 리더가 최종 리뷰합니다.

- 기본 CODEOWNER는 개발 리더입니다.
- Backend, Frontend, Connector, Contract, DB, Runtime을 포함한 모든 변경에 동일한 리뷰 정책을 적용합니다.
- 영역 담당자의 peer review는 선택적으로 추가할 수 있지만, 최종 merge 승인 책임은 개발 리더가 가집니다.
- 개발 리더 본인의 PR은 Repository Ruleset에서 사람 approval만 예외 처리하고, PR 생성·CI·미해결 conversation 확인은 동일하게 적용합니다.

## 로컬 확인

```bash
make setup
make test
```

Go 검증은 개별 target으로도 실행할 수 있습니다.

```bash
make go-fmt-check
make go-vet
make go-test
make go-build
```

개별 실행:

```bash
make server
make connector
make web
```

현재 스켈레톤은 실제 제품 기능 구현 완료를 뜻하지 않습니다. Backend/Connector/Frontend의 각 기능 package는 Jira Story가 시작될 때 필요한 만큼 생성합니다.
