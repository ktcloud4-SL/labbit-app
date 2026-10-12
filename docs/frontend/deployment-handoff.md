# Frontend Deployment Handoff · G1 → Sprint 02

> 최종 갱신: 2026-10-12 · `labbit-app` main의 Web build/Nginx, Browser Router, HTTP/WSS 계약 및 Jira LBT-36/74/26/53를 대조. 배포 계약은 10/08 기준선을 재검토했고 공개 HTTPS Browser 실측은 10/12 결과를 반영했다.
>
> 이 문서는 특정 `main` SHA에 고정하지 않고, 실제 `web/**` 변경이 생기면 build/runtime 입력을 다시 확인해 갱신한다.
>
> 목적: Sprint 01 / AWS1 G1에서 시작한 Frontend 배포 입력을 Sprint 02의 Local/AWS Browser Acceptance까지 유지한다. **Web image/Helm/Istio의 배포 소유권은 Platform에 있고, Frontend는 Router·HTTP/WSS Consumer와 사용자 화면 검증을 담당한다.**
>
> 현재 판정: LBT-32 Web packaging과 공개 HTTPS Web 서빙은 준비됐고, LBT-74 Auth/Class는 Local Stage A **PASS**, 공개 HTTPS Stage B **부분 PASS**다(2026-10-12 정상 로그인·수업·로그아웃, 대표 401/403/404, Session Cookie 속성 실측). 미검증 보안·장애 시나리오가 남아 있어 LBT-74 최종 Acceptance는 Jira #10418에 따라 **완료**(상위 LBT-12 #10419도 완료)됐으며, 아래 미검증 항목은 별도 환경/실측 범위다. Backend 배포/DB/rollback은 LBT-36에서 별도 판정한다. 자세한 결과는 8절과 [Jira LBT-74](https://samsunglions.atlassian.net/browse/LBT-74)를 따른다.

## 1. 현재 Web build 계약

Frontend는 `web/`의 React + TypeScript + Vite SPA다.

현재 저장소 기준 기본 검증 진입점:

```bash
cd web
npm ci
npm run typecheck
npm run lint
npm run test
npm run build
```

동일 단계는 `.github/workflows/web.yml`의 Web CI에서도 실행한다.

현재 Node engine:

```text
^22.22.2 || ^24.15.0 || >=26.0.0
```

G1 packaging에서는 기존 `npm run build` 결과를 배포 입력으로 사용한다. 현재 `vite.config.ts`에서 `build.outDir`을 별도로 바꾸지 않으므로 기본 산출물은 `web/dist/`다.

현재 `web/Dockerfile`은 Node 22 builder에서 `npm ci`/`npm run build` 후 `web/dist/`를 Nginx image로 옮기는 multi-stage 방식이며, `web/nginx.conf`가 포트 80의 정적 SPA를 제공합니다. 배포 Image/Helm/Istio 구현은 Platform 담당 범위입니다. `npm run preview`는 로컬 확인용이며 production serving 계약으로 사용하지 않습니다.

## 2. Production API mode

Production에서는 API mode를 별도 환경변수로 고르지 않는다.

`web/src/shared/api/LabbitApiProvider.tsx` 기준:

- `import.meta.env.DEV === false`이면 항상 `httpLabbitApi` 사용
- `VITE_LABBIT_API_MODE=mock|http` 선택은 개발 모드에서만 사용
- 따라서 production artifact가 Mock mode로 전환되는 runtime switch는 현재 없다

즉 G1 배포 시 Frontend에 `VITE_LABBIT_API_MODE=http`를 runtime secret/config처럼 주입할 필요가 없다.

## 3. Browser → Backend 경계

`web/src/shared/api/httpClient.ts` 기준 Browser HTTP 요청은 항상 same-origin 상대경로를 사용한다.

```text
/api/v1/...
```

또한 요청은 `credentials: 'include'`를 사용하므로 Cookie Session을 전제로 한다.

따라서 배포 경로는 최소 다음을 만족해야 한다.

1. `/` 및 Frontend route는 Web SPA로 전달
2. `/api/v1/*`는 Backend로 전달
3. `/api/v1/*`를 SPA fallback이 가로채지 않음
4. Browser 관점에서 Frontend와 Backend API가 same-origin 경계로 동작

`web/nginx.conf`의 `/api/v1` 위치는 SPA HTML fallback을 차단하는 보호용 404입니다. 정상 환경에서 실제 `/api/v1/*` 요청을 Backend로 보내는 분기는 Nginx 내부 proxy가 아니라 Platform Gateway/Istio Routing 책임입니다. HTTP endpoint 상세와 Session/Cookie/Origin 정책은 Git OpenAPI와 Backend 계약을 소비하며 Frontend packaging에서 새로 정의하지 않습니다.

Terminal Browser WSS는 `contracts/realtime/README.md`의 same-origin `/realtime/v1/terminal`, subprotocol `labbit.terminal.v1`을 사용합니다. 배포 환경에서 HTTPS는 WSS로 업그레이드되어야 하며 이 경로도 Web SPA로 fallback시키지 않습니다.

**WSS 배포 검증 주의:** 현재 `web/nginx.conf`에는 `/realtime/v1` 별도 차단/Proxy 규칙이 없습니다. 따라서 Gateway/Istio가 해당 WSS 요청을 Web SPA보다 먼저 올바른 Relay로 라우팅하는지, 실제 WebSocket Upgrade·Origin·subprotocol 협상이 정상인지 LBT-26/27 실환경 통합 때 확인해야 합니다. 이는 라우팅 위험에 대한 인계사항이며 실제 WSS E2E PASS 또는 배포 결함 확정은 아닙니다.

## 4. SPA route fallback 요구사항

현재 Router는 `createBrowserRouter`를 사용하고 다음과 같은 client route를 가진다.

- `/login`
- `/classes`
- `/classes/:classId`
- `/classes/:classId/provision`
- `/classes/:classId/lab`
- `/lab-specs`
- `/lab-specs/new`
- `/lab-specs/:labSpecId`
- `/lab-executions/:labExecutionId`
- `/operations/:operationId`

따라서 사용자가 `/classes/...` 같은 route를 직접 열거나 새로고침해도 Web entry로 돌아와 React Router가 처리할 수 있어야 합니다. 루트 `/`는 의도적으로 `/classes`로 이동하고, 해당 보호 Route의 `/api/v1/me`가 `401`일 때만 `/login`으로 이동합니다. `503` 등 non-401은 로그인 실패가 아니라 일반 API 오류 UI로 남습니다.

단, `/api/v1/*`는 이 SPA fallback 대상에서 제외해야 한다.

## 5. 개발 전용 Proxy와 Production의 차이

Local Vite 개발에서는 `web/vite.config.ts`가 `/api/v1` HTTP와 `/realtime/v1` WebSocket을 Backend로 proxy합니다.

개발용 Backend target:

```text
LABBIT_DEV_BACKEND_URL
기본값: http://127.0.0.1:8080
```

이 값은 **Vite dev server용 proxy 설정**이다.

Production packaging에서 동일한 dev proxy를 runtime 기능으로 기대하지 않습니다. 실제 Local K8s/AWS 경로에서는 Platform의 Gateway/Istio Routing이 `/api/v1/*`을 Backend로, `/realtime/v1/*`을 해당 실시간 Relay로 연결해야 합니다. 브라우저가 API/WSS 요청에서 SPA HTML을 받는다면 라우팅 오류로 기록합니다.

## 6. Frontend Config / Secret 경계

현재 Production Web이 요구하는 별도 secret 값은 없다.

Frontend 쪽에서 유지할 원칙:

- Session token/credential을 Web build-time config에 넣지 않음
- Backend Secret/DB credential을 Frontend artifact에 넣지 않음
- API base를 외부 절대 URL로 새로 고정하지 않고 현재 `/api/v1` same-origin 경계를 유지
- AWS/Local 환경 차이는 Web code가 아니라 배포 routing/config 경계에서 처리

향후 계약 변경이 생기면 별도 review 후 갱신한다.

## 7. LBT-32 담당자에게 넘길 최소 입력

Platform packaging에서 현재 필요한 Frontend 입력은 다음과 같다.

| 항목 | 현재 기준 |
| --- | --- |
| Frontend source/build root | `web/` |
| Install | `npm ci` |
| Production build | `npm run build` |
| Build output | `web/dist/` (Vite 기본값, 현재 override 없음) |
| Production serving | `web/Dockerfile`: Node 22 build → Nginx port 80 (`web/nginx.conf`), 배포/Helm 소유는 Platform |
| Production API mode | HTTP 고정 |
| Browser API prefix | `/api/v1` · Web Nginx는 API 경로를 SPA fallback에서 제외 |
| Cookie | `credentials: include` |
| Browser Terminal WSS | `/realtime/v1/terminal` · `labbit.terminal.v1` · HTTPS에서는 WSS |
| SPA routing | Browser history route fallback, `/` → `/classes`, 미인증 `/me 401`만 Login 이동 |
| Dev-only backend target | `LABBIT_DEV_BACKEND_URL` |
| Frontend runtime secret | 현재 없음 |
| Mock | Production에서는 사용하지 않음 |

## 8. G1 Smoke에서 Frontend가 확인할 것

배포 artifact가 준비되면 Frontend 관점에서 최소 다음을 확인한다.

- Browser에서 Web 첫 화면과 정적 asset이 정상 로드
- `/login`, `/classes` 등 직접 URL 진입/새로고침이 Web 404로 끝나지 않음
- `/api/v1/*`가 SPA HTML이 아니라 Backend 응답으로 전달
- Production artifact가 Mock 데이터를 사용하지 않음
- 실제 Backend에 구현·배포된 제품 HTTP endpoint별로 Browser → Frontend → Backend Flow 확인(계약이나 Consumer만 있는 경로를 E2E PASS로 보지 않음)
- Secret/Credential 원문이 Web artifact나 공개 Config에 들어가지 않음

**실제 통합 검증 현황(2026-10-12 14:50 Jira 최종 마감 기준)**

- **Local Stage A PASS:** LBT-72/73의 실제 Backend HTTP Auth/Class 연동 확인.
- **고정 HTTPS Stage B Browser 부분 PASS(LBT-74 최종 Acceptance의 직접 검증 근거):** 개발용 관리자·강사·학생 계정의 Login `204` → `/me 200` → Class 목록/상세 `200` → Logout `204` → `/me 401`, 관리자/학생의 비인가 Class 접근 `403`, 없는 Class `404`, 잘못된 비밀번호 `401`, 새로고침 후 세션 유지·로그아웃 후 보호 Route 차단 및 `__Host-labbit-session` Cookie의 Secure/HttpOnly/SameSite=Lax/Path=/를 Browser에서 관측. [Jira LBT-74](https://samsunglions.atlassian.net/browse/LBT-74) 댓글 #10398·#10399가 상세 Evidence이며, 과거 `/me 503`은 최신 결과가 아닙니다.
- **공유 HTTPS에서 FE 직접 미검증인 별도 시나리오:** FE가 강제한 Origin/Referer 거부, 시간 경과에 따른 서버 측 세션 만료, 실제 서버 5xx·장애/복구 UI는 이번 FE Browser 직접 실측이 아님. Backend Owner의 LBT-68 #10405 Origin/Referer·세션/격리 오류 계약 증거는 별도 Owner Evidence로 재사용하며 LBT-74 Jira 완료 판정과 구분한다. LBT-36의 DB/Migration/read-write/readiness/rollback도 별도 Gate입니다.
- **Provision의 별도 404:** Browser에서 `GET /classes/{classId}/memberships`와 `GET /lab-specs`가 404였습니다. 두 경로 모두 현재 Backend HTTP router에 등록되지 않았습니다. LabSpec은 LBT-17 후속 구현 범위이나 Membership 목록 조회는 LBT-67 완료 범위에 명시되지 않았으므로 **Backend 담당자와 Owner/일정을 별도로 확인**합니다. 이를 Auth/Class Stage B의 실패나 Provision E2E 완료로 판정하지 않습니다.
- **후속 E2E:** Terminal/File 실제 VM은 LBT-21/22 준비 후 LBT-26/27에서, Local/AWS 전체 사용자 Flow는 LBT-53에서 각각 검증합니다.

## 9. 이 문서에서 확정하지 않는 것

다음은 이 Frontend handoff 문서의 범위가 아니다.

- Platform이 소유하는 Web Dockerfile/Nginx 배포 설정의 추가 변경 방식 (현재 구현 `web/Dockerfile`, `web/nginx.conf`는 코드 기준)
- Helm template 구조
- Istio Gateway / VirtualService 상세
- EKS Web serving vs Object Storage/CDN 최종 hosting 결정
- Backend Auth/Class 배포·DB/Secret 통합의 실제 완료 판정
- File/Terminal의 실제 VM E2E 및 Preview/Live 기능 완료 판정 (계약과 코드 존재는 별개)

이 항목들은 각 담당 Jira/설계 기준에서 결정한다.

## 관련

- 이 문서는 기존 **Jira LBT-32 — CD-02 Container Image / Helm Packaging**에 연결된 Frontend handoff 입력이다.
- LBT-32의 Dockerfile/Helm 구현 책임을 Frontend로 옮기는 것이 아니라, 해당 작업이 소비할 Web build/runtime 입력만 정리한다.
- Jira LBT-11 — FE-01 Frontend Core / Auth Consumer 기반 (완료)
- Jira LBT-12 — FE-02 Auth / Class 실제 통합
- Jira LBT-32 — CD-02 Container Image / Helm Packaging
- Jira LBT-62 — G1-Prep AWS1 Minimal Infra / Secret Readiness
- Jira LBT-36 — Local App Deploy / Rollback
- Jira LBT-74 — HTTPS Browser Acceptance
- Jira LBT-26/27 — Workspace 실제 통합 / Reconnect UX
- Jira LBT-53 — Local/AWS 사용자 Flow
- `web/package.json`
- `web/vite.config.ts`
- `web/Dockerfile`, `web/nginx.conf`
- `contracts/http/openapi.yaml`, `contracts/realtime/README.md`
- `web/src/shared/api/LabbitApiProvider.tsx`
- `web/src/shared/api/httpClient.ts`
- `web/src/app/router.tsx`
- `.github/workflows/web.yml`
