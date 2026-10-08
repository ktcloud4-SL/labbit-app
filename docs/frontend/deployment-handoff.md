# Frontend Deployment Handoff · G1 → Sprint 02

> 갱신 기준: 2026-10-08 · `labbit-app` main의 `web/Dockerfile`, `web/nginx.conf`, Browser Router, HTTP/WSS 계약과 Jira LBT-36/74/26/53를 재확인
>
> 이 문서는 특정 `main` SHA에 고정하지 않고, 실제 `web/**` 변경이 생기면 build/runtime 입력을 다시 확인해 갱신한다.
>
> 목적: Sprint 01 / AWS1 G1에서 시작한 Frontend 배포 입력을 Sprint 02의 Local/AWS Browser Acceptance까지 유지한다. **Web image/Helm/Istio의 배포 소유권은 Platform에 있고, Frontend는 Router·HTTP/WSS Consumer와 사용자 화면 검증을 담당한다.**
>
> 상태 경계: LBT-32 Web packaging과 공개 HTTPS Web 서빙은 준비됨. LBT-74의 Local 실제 HTTP Stage A는 검증했지만 고정 HTTPS Stage B는 **아직 미완료**(마지막 기록 `/api/v1/me = 503`). 실제 Backend LBT-36/DB/Secret readiness와 `401 + JSON` 확인 전에는 완성을 주장하지 않는다.

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
- 실제 Backend 제품 HTTP endpoint가 준비되면 Browser → Frontend → Backend 최소 Product HTTP Flow 확인
- Secret/Credential 원문이 Web artifact나 공개 Config에 들어가지 않음

Auth/Class 로컬 실제 Backend HTTP Stage A는 완료(LBT-72/73)됐지만, 공유 고정 HTTPS Stage B(LBT-74)는 Backend 배포 LBT-36·DB/Secret·Public Origin과 미인증 `/api/v1/me = 401 + JSON`이 준비되어야 실행합니다. Web 정적 서빙 200이나 기존 `/me = 503`만으로 Stage B PASS가 아닙니다. FE-03 Terminal/File actual VM E2E는 LBT-21/22 이후 LBT-26/27에서, Local/AWS 사용자 Flow는 LBT-53에서 따로 확인합니다.

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
