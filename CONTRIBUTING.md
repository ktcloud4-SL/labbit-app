# Labbit 개발 협업 규칙

이 문서는 `labbit-app`의 최소 Git/PR 운영 규칙을 정의합니다. 기능·도메인 결정은 Jira/Confluence, 코드와 검증 기록은 GitHub, 기계 판독 계약은 `contracts/`, `db/`, `runtime/`을 기준으로 합니다.

## Branch

- 기본 브랜치는 `main`이며 직접 push하지 않고 PR을 사용합니다.
- Jira Task가 있는 작업은 `<type>/LBT-<number>-<english-slug>` 형식을 사용합니다.
- 예: `feat/LBT-10-auth-session`, `fix/LBT-19-terminal-reconnect`
- Jira로 별도 추적할 필요가 없는 소규모 repository maintenance 또는 기존 review follow-up은 `<type>/<english-slug>` 형식을 허용합니다.
- 이미 관련 Jira Task가 있으면 새 예외 작업으로 분리하지 말고 해당 `LBT-*`를 재사용합니다.
- 과거 `SL-*` branch/commit/PR은 이력으로 유지하며 신규 Jira 작업부터 `LBT-*`를 사용합니다.
- 작업 브랜치는 short-lived로 운영하고 merge 후 삭제합니다.

## Commit / PR

Commit은 기존 Conventional 형식을 사용하며 Jira key를 강제하지 않습니다.

```text
<type>(<scope>): <한글 설명>
```

예: `feat(auth): Session 생성 로직 구현`

PR 제목은 다음 기준을 사용합니다.

```text
# Jira Task가 있는 작업
<type>(<scope>): [LBT-<number>] <한글 설명>

# Jira가 없는 소규모 maintenance
<type>(<scope>): <한글 설명>
```

- PR 본문의 `Jira` 항목에는 관련 `LBT-*`를 적고, Jira가 없는 maintenance는 `없음`으로 표시합니다.
- 한 PR은 가능한 한 하나의 Jira Task 또는 하나의 명확한 목적에 집중합니다.
- 허용 type: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `ci`, `build`, `perf`, `revert`
- scope는 필요할 때만 사용합니다.

## Review / 검증

- 현재 repository ruleset과 CODEOWNERS 기준의 PR·CI·리뷰 절차를 따릅니다.
- 기능 또는 동작 변경은 필요한 자동 테스트와 검증 결과를 PR에 남깁니다.
- HTTP/WSS/Runtime 계약 변경은 producer/consumer 영향을 함께 확인합니다.
- DB Migration은 이미 적용된 Migration을 수정하지 않고 새 Migration을 추가합니다.
- 테스트 상세 기준은 [TESTING.md](./TESTING.md)를 따릅니다.

## Merge

- `main` 반영은 **Squash Merge만 사용**합니다.
- Merge Commit과 Rebase Merge는 사용하지 않습니다.
- Jira 작업의 PR 제목에 `LBT-*`가 남도록 하여 PR과 최종 변경 단위를 추적합니다.
- merge 후 작업 브랜치는 삭제합니다.

## 로컬 확인

```bash
make setup
make test
```

필요하면 개별 target을 실행합니다.

```bash
make go-fmt-check
make go-vet
make go-test
make go-build
make server
make connector
make web
```

## 컨테이너 이미지 태그 덮어쓰기 방지 및 릴리즈 규칙 정의
스프린트 종료 및 정식 출시(마일스톤 배포) 시, 불변의 컨테이너 이미지 버전을 생성하기 위해 Git Tag를 사용합니다.
- 버전 형식: `v<Major>.<Minor>.<Patch>` (예: `v0.1.0`, `v0.1.1`)
- 태그 푸시 시 GitHub Actions가 이를 감지하여 동일한 버전 태그의 컨테이너 이미지를 자동 빌드 및 GHCR로 배포합니다.
- **일상 개발 시에는 태그를 붙이지 않고 평소처럼 PR & Squash Merge를 진행**하며, 이때는 커밋 해시(`sha-<short>`)와 `latest`로 자동 관리됩니다.
```bash
# 릴리즈 태그 생성 및 원격 푸시 예시 (스프린트 마일스톤 완료 시 사용)
git tag v0.1.0
git push origin v0.1.0
```