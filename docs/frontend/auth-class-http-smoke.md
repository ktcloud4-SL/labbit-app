# Auth / Class Local HTTP Smoke

> 대상: Jira LBT-72, LBT-73의 Frontend 실제 HTTP 연동 준비
>
> 이 문서는 Mock 없이 Local Browser → Vite proxy → Backend `/api/v1` 흐름을 반복 확인하기 위한 실행 체크리스트다.
> 실제 테스트 계정, Password, Session token, AWS credential 같은 Secret은 이 문서나 Git에 기록하지 않는다.

## 1. 전제

필요한 로컬 구성:

- Docker 또는 호환 Compose runtime
- Go toolchain
- Node.js / npm
- Backend가 사용할 PostgreSQL
- 로그인 가능한 Local Account 1개 이상
- 해당 계정이 Membership으로 참여한 Class 1개 이상

Local Account / Class 초기 데이터는 팀에서 합의한 Backend bootstrap/seed/공유 개발 DB 경로를 사용한다.
Frontend가 임의의 Auth/Class 정책이나 별도 데이터 생성 규칙을 추가하지 않는다.

## 2. Backend 준비

저장소 root에서 먼저 DB와 Migration을 준비한다.

```bash
make dev-db-up
make dev-db-migrate
```

Backend Local dev fixture 실행 경로는 PR #51(LBT-68)에서 `make dev-auth-class-fixture`로 준비되었다.
PR #51이 merge된 main 또는 해당 branch를 반영한 작업 트리에서 다음 순서로 fixture를 준비한다.

```bash
make dev-auth-class-fixture
```

성공하면 credential은 repository root의 `.local/dev-auth-class-fixture.json`에만 생성된다.
이 파일은 gitignore 대상이며 Password 원문이 있으므로 Git/Jira/캡처에 올리지 않는다.

fixture 준비 후 Backend를 실행한다.

```bash
make server
```

기본값:

```text
PostgreSQL: 127.0.0.1:5432
Backend:    127.0.0.1:8080
Public Origin: http://localhost:5173
```

`make server`는 Browser가 실제로 보는 Vite origin을 `LABBIT_PUBLIC_ORIGIN`으로 전달한다.

## 3. Frontend를 실제 HTTP mode로 실행

`web/.env.local`:

```dotenv
VITE_LABBIT_API_MODE=http
LABBIT_DEV_BACKEND_URL=http://127.0.0.1:8080
```

`.env.local`은 Git에 커밋하지 않는다.

저장소 root에서:

```bash
make web
```

Browser는 Vite dev server를 열고, Frontend 요청은 상대경로 `/api/v1/*`를 사용한다.
Vite가 해당 경로를 `LABBIT_DEV_BACKEND_URL`로 proxy한다.

## 3-1. 팀 Local dev fixture 기준

Backend에서 아래 Local dev fixture를 준비하는 것으로 합의했다.
실제 Password나 Secret은 Git에 기록하지 않고, 실행 경로가 전달되면 해당 Bootstrap use case를 사용한다.

| 계정 | Organization Role | Class Alpha | Class Bravo | 주요 검증 목적 |
| --- | --- | --- | --- | --- |
| `dev-admin` | ADMIN | Membership 없음 | Membership 없음 | ADMIN ≠ INSTRUCTOR, Class empty list, Class 접근 403 |
| `dev-instructor` | MEMBER | INSTRUCTOR | STUDENT | 한 사용자의 Class별 Role 분리, Instructor/Student UI |
| `dev-student` | MEMBER | STUDENT | Membership 없음 | Student UI, Membership 없는 다른 Class 403 |

이 fixture는 직접 SQL을 넣는 방식이 아니라 기존 `bootstrap.Run()` use case를 재사용한 PR #51의 dev-only 실행 경로를 사용한다.

403 검증은 목록에 보이지 않는 기존 Class를 직접 요청해야 한다.
PR #51의 credential file에는 `classes.alpha` / `classes.bravo` Resource ID가 포함되므로 그 값을 사용한다.
Resource ID는 Secret이 아니지만 credential file 전체나 Password, Session token, DB credential은 기록하지 않는다.

권장 재사용 시나리오:

- `dev-admin`: Login → `/me`에서 ADMIN 확인 → Class 목록 empty → Class Alpha 상세 직접 접근 시 403
- `dev-instructor`: Login → Alpha/Bravo 목록 확인 → Alpha는 INSTRUCTOR, Bravo는 STUDENT UI 확인
- `dev-student`: Login → Alpha 목록/상세 STUDENT 확인 → Bravo 상세 직접 접근 시 403

## 4. LBT-72 Auth smoke

준비된 개발용 계정으로 다음 흐름을 확인한다.

1. `/login`에서 실제 계정으로 로그인
2. Network에서 `POST /api/v1/auth/login` → `204` 확인
3. 이어서 `GET /api/v1/me` → `200`이 호출되고 보호 화면으로 이동하는지 확인
4. 화면 상단의 현재 사용자 / Organization 정보 확인
5. 로그아웃
6. `POST /api/v1/auth/logout` → `204` 확인
7. 로그아웃 후 `/me` 또는 보호 API가 `401`이 되고 인증 필요 상태로 돌아가는지 확인

추가 확인:

- HTTP mode에서 Mock 전용 계정 안내가 노출되지 않음
- Frontend가 Session Cookie를 직접 읽거나 Web Storage에 복사하지 않음
- 잘못된 username/password는 사용자 존재 여부를 구분하지 않는 로그인 실패 UI로 처리

## 5. LBT-73 Class smoke

로그인 Session을 유지한 상태에서 다음을 확인한다.

1. `GET /api/v1/classes` → `200`
2. 실제 Backend 응답의 Class 목록이 화면에 표시
3. Class 열기
4. `GET /api/v1/classes/{classId}` → `200`
5. `id`, `name`, `myRole`이 실제 응답과 화면에서 일치

현재 Backend가 아직 제공하지 않는 optional 필드:

- `activeLabExecution`
- `myLabInstance`

이 필드가 응답에서 생략되어도 Frontend는 오류로 취급하지 않는다.

데이터가 준비되면 추가로 확인:

- 참여 Class가 없는 계정 → 빈 목록
- 존재하지만 Membership이 없는 Class → 403
- 존재하지 않거나 서버가 발급하지 않은 형식의 Class ID → 404

## 6. Browser / Network Evidence

LBT-74 최종 Browser Acceptance와 연결할 때 최소 다음을 남긴다.

- Mock이 아닌 HTTP mode임을 확인할 수 있는 실행 조건
- Login 성공 요청/응답 status
- `/me` 성공
- Class 목록 성공
- Class 상세 성공
- Logout 성공
- Logout 이후 보호 API 401
- 대표 403 / 404 / empty / Backend 실패 UI

Password, Cookie 원문, Authorization/Session token, DB credential은 캡처나 Jira/PR Evidence에 노출하지 않는다.

## 7. Local HTTP와 최종 Cookie Acceptance 구분

개발자 PC의 plain HTTP smoke는 API routing과 Frontend consumer 동작을 빠르게 확인하기 위한 단계다.

Backend Auth 계약상 production 및 production-like 환경의 Session Cookie는
`Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`, Domain 없음의 `__Host-labbit-session`을 사용한다.

따라서 Local HTTP 환경에서 Browser별 Cookie 제약 때문에 Session 저장/전송이 기대와 다르더라도
개발 편의를 위해 Cookie 보안 속성을 약화하지 않는다.

Local Browser가 Secure Cookie를 유지하지 않아 Login 이후 `/me`가 401이 되는 경우에는 다음을 구분해 기록한다.

- Login 응답 자체가 204이고 `Set-Cookie`가 계약대로 내려왔는지
- Browser가 Cookie를 저장했는지
- 이후 `/me` 요청에 Cookie가 실제로 포함됐는지

Login 응답은 정상인데 Browser의 Local HTTP Cookie 제약 때문에 이후 요청에 Cookie가 없으면
이를 Frontend consumer 결함으로 단정하지 않는다. 최종 Cookie / Origin / Referer acceptance는
공유 Local HTTPS 또는 AWS HTTPS 경로에서 확인한다.

## 8. LBT-74 Evidence 기록 Template

아래 표는 재현 가능한 시나리오·기대 결과·확인 위치를 정리하는 Template이다.
실제 Browser Acceptance 실행 결과와 PASS/FAIL Evidence는 Jira LBT-74에 기록한다.

| 시나리오 | 권장 fixture | 기대 결과 | 확인 위치 | Evidence 기록 |
| --- | --- | --- | --- | --- |
| 정상 Login | `dev-instructor` | `POST /auth/login` 204, Session 발급 | Browser Network / UI | Jira LBT-74 |
| 현재 사용자 | `dev-instructor` | `GET /me` 200, MEMBER/Organization 확인 | Network / AppShell | Jira LBT-74 |
| Class 목록 role 분리 | `dev-instructor` | Alpha=INSTRUCTOR, Bravo=STUDENT | Network / Class 화면 | Jira LBT-74 |
| Class 상세 정상 | `dev-instructor` | Alpha/Bravo `GET /classes/{classId}` 200, 각 role UI 일치 | Network / 상세 화면 | Jira LBT-74 |
| Logout | `dev-instructor` | `POST /auth/logout` 204 | Network / Login 화면 | Jira LBT-74 |
| Logout 이후 보호 API | `dev-instructor` | `/me` 또는 보호 API 401 | Network / Login redirect | Jira LBT-74 |
| 잘못된 credential | 기존 dev username + 잘못된 password | 401, 계정 존재 여부를 구분하지 않는 UI | Network / Login 오류 | Jira LBT-74 |
| Class 없음 | `dev-admin` | `GET /classes` 200 + 빈 items, Empty UI | Network / 목록 화면 | Jira LBT-74 |
| ADMIN ≠ INSTRUCTOR | `dev-admin` | `/me` ADMIN이지만 Class Membership/Instructor 권한은 생기지 않음 | Network / UI | Jira LBT-74 |
| Class 접근 불가 | `dev-admin` → Alpha 또는 `dev-student` → Bravo | 403, 권한 없음 UI | Network / 상세 화면 | Jira LBT-74 |
| Student UI | `dev-student` → Alpha | STUDENT 표시, Instructor 전용 동작 비노출 | Network / 상세 화면 | Jira LBT-74 |
| 없는 Class | 로그인된 dev 계정 + 안전한 미존재 테스트 ID | 404, 찾을 수 없음 UI | Network / 상세 화면 | Jira LBT-74 |
| Session 만료/폐기 | 실행 가능한 Backend 검증 경로 사용 | 401, Login redirect | Network / Login redirect | Jira LBT-74 |
| Backend/Proxy 실패 | 환경에서 재현 가능한 실패 경로 | 대표 5xx, 일반 오류 UI | Network / 오류 화면 | Jira LBT-74 |
| Origin/Referer 불일치 | 공유 HTTPS 환경 | unsafe method 403 | Network | Jira LBT-74 |
| Session Cookie 속성 | 공유 HTTPS 환경 | Secure/HttpOnly/SameSite=Lax/Path=/, Domain 없음 | Browser Cookie / Network | Jira LBT-74 |

Evidence를 남길 때는 요청 URL, method, status, 화면 상태가 보이면 충분하다.
Password 입력값, Cookie/Session token 원문, DB/AWS credential은 가리거나 제외한다.
Session Cookie 속성 검증이 필요한 캡처에서는 token 값만 가리고
`Secure`, `HttpOnly`, `SameSite`, `Path`, Domain 유무 같은 속성은 확인 가능하게 남긴다.

## 8-1. 대표 Backend / Proxy 실패 재현

LBT-74의 대표 Backend/Proxy 실패 UI는 제품 데이터를 임의로 손상시키지 않고 다음처럼 확인할 수 있다.

1. 정상 로그인/조회가 가능한 상태에서 Backend process를 중지한다.
2. Browser에서 Class 목록 또는 상세을 다시 조회한다.
3. Vite proxy / network 실패가 Frontend의 일반 오류 상태로 표시되는지 확인한다.
4. Backend를 다시 실행한 뒤 정상 조회가 복구되는지 확인한다.

이 검증은 Session 만료나 Backend 내부 5xx 정책을 대신하지 않는다.
실제 5xx를 안전하게 재현할 팀 기준 경로가 별도로 제공되면 그 경로를 우선 사용한다.

## 9. 완료 판단

LBT-72/73은 코드가 존재하거나 unit test만 통과했다고 완료 처리하지 않는다.

최소한 준비된 실제 개발 데이터 기준으로 Browser에서 Mock 없이 다음 흐름을 확인해야 한다.

```text
Login
  → /me
  → Class 목록
  → Class 상세
  → Logout
  → 보호 API 401
```

전체 오류/권한/Cookie/Origin 회귀 Evidence는 LBT-74에서 최종 정리한다.
