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

저장소 root에서:

```bash
make dev-db-up
make dev-db-migrate
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

## 8. LBT-74 Evidence 기록 틀

실제 Browser 검증을 시작하면 아래 표를 채워 Jira LBT-74와 Backend LBT-68이 같은 Evidence를 참조할 수 있게 한다.

| 시나리오 | 기대 결과 | 확인 위치 | Evidence 상태 |
| --- | --- | --- | --- |
| 정상 Login | `POST /auth/login` 성공, Session 발급 | Browser Network / UI | 대기 |
| 현재 사용자 | `GET /me` 200, 사용자/Organization 표시 | Network / AppShell | 대기 |
| Class 목록 | `GET /classes` 200, 실제 목록 표시 | Network / Class 화면 | 대기 |
| Class 상세 | `GET /classes/{classId}` 200 | Network / 상세 화면 | 대기 |
| Logout | `POST /auth/logout` 성공 | Network / Login 화면 | 대기 |
| Logout 이후 보호 API | `/me` 또는 보호 API 401 | Network / Login redirect | 대기 |
| 잘못된 credential | 401, 계정 존재 여부를 구분하지 않는 UI | Network / Login 오류 | 대기 |
| Session 만료/폐기 | 401, 재로그인 안내 | Network / Login redirect | 대기 |
| Class 접근 불가 | 403, 권한 없음 UI | Network / 상세 화면 | 대기 |
| 없는 Class | 404, 찾을 수 없음 UI | Network / 상세 화면 | 대기 |
| Class 없음 | 200 + 빈 items, Empty UI | Network / 목록 화면 | 대기 |
| Backend/Proxy 실패 | 대표 5xx, 일반 오류 UI | Network / 오류 화면 | 대기 |
| Origin/Referer 불일치 | unsafe method 403 | Network | 공유 HTTPS에서 확인 |
| Session Cookie 속성 | Secure/HttpOnly/SameSite=Lax/Path=/, Domain 없음 | Browser Cookie / Network | 공유 HTTPS에서 확인 |

Evidence를 남길 때는 요청 URL, method, status, 화면 상태가 보이면 충분하다.
Password 입력값, `Set-Cookie` 값, Cookie 원문, Session token, DB/AWS credential은 가리거나 제외한다.

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
