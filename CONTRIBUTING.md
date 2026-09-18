# Labbit 개발 협업 규칙

이 문서는 `rabbit-app` 개발 시작 전에 필요한 최소 Git/PR 규칙만 정의합니다. 기능·도메인 정책은 Confluence, 기계 판독 계약은 `contracts/`, `db/`, `runtime/`의 각 SSOT를 따릅니다.

## 브랜치

브랜치 이름은 ASCII로 작성합니다.

```text
<type>/<jira-key>-<english-slug>
```

예:

```text
feat/SL-123-provision-targets
fix/SL-214-reset-conflict
docs/SL-301-runtime-contract
```

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
- 한 PR은 가능한 한 하나의 Jira Story/명확한 목적에 집중합니다.
- 계약 파일을 바꾸면 해당 producer/consumer 영향도 함께 확인합니다.
- 생성된 코드가 있다면 생성 원본과 생성 방법을 PR에 명시합니다.

## Merge 전략

이 저장소는 **3-way merge 기반의 GitHub `Create a merge commit`** 방식을 사용합니다.

- `Squash and merge`를 기본 전략으로 사용하지 않습니다.
- `Rebase and merge`를 기본 전략으로 사용하지 않습니다.
- PR의 개별 commit을 main history에 유지합니다.
- merge commit 제목도 가능하면 `<type>(<scope>): <한글 설명>` 형식으로 작성합니다.

따라서 feature branch의 commit도 리뷰 가능한 단위로 정리하고 `fix typo`, `wip` 같은 의미 없는 commit을 남발하지 않습니다.

## CODEOWNERS

현재 저장소에서 확인 가능한 관리자 계정만 기본 CODEOWNER로 지정합니다. Backend/Frontend/Connector/Platform GitHub Team slug가 실제로 생성·확인되면 역할별 경로 ownership을 세분화합니다.

팀 slug가 확정되기 전에는 존재하지 않는 Team 이름을 CODEOWNERS에 임의로 적지 않습니다.

## 로컬 확인

```bash
make setup
make test
```

개별 실행:

```bash
make server
make connector
make web
```

현재 스켈레톤은 실제 제품 기능 구현 완료를 뜻하지 않습니다. Backend/Connector/Frontend의 각 기능 package는 Jira Story가 시작될 때 필요한 만큼 생성합니다.
