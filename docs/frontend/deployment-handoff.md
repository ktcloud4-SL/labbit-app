# Frontend G1 Deployment Handoff

> 검토 기준: FE-01(PR #33) merge 이후 최신 `main`의 Frontend 코드와 Web CI
>
> 이 문서는 특정 `main` SHA에 고정하지 않고, 실제 `web/**` 변경이 생기면 build/runtime 입력을 다시 확인해 갱신한다.
>
> 목적: Sprint 01 / AWS1 G1 준비에서 Frontend가 Platform의 LBT-32(Container Image / Helm Packaging)에 전달해야 할 **현재 Web artifact·runtime 경계**를 짧게 고정한다. Dockerfile/Helm/Istio 구현 자체는 Platform 담당 범위이며, 이 문서는 그 구현에 필요한 Frontend 입력만 다룬다.

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

현재 Web은 정적 SPA이며, 저장소 안에 Web 전용 production Dockerfile이나 Nginx/Caddy 설정은 아직 없다. Node/Vite는 build 단계 입력이고, 정적 산출물을 어떤 서버/hosting 방식으로 제공할지는 Platform 배포 경계에서 결정한다. `npm run preview`는 로컬 build 확인용이며 production serving 계약으로 사용하지 않는다.

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

Backend endpoint 상세와 Session/Cookie/Origin 정책은 기존 Git 계약을 소비하며 Frontend packaging 단계에서 새로 정의하지 않는다.

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

따라서 사용자가 `/classes/...` 같은 route를 직접 열거나 새로고침해도 Web entry로 돌아와 React Router가 처리할 수 있어야 한다.

단, `/api/v1/*`는 이 SPA fallback 대상에서 제외해야 한다.

## 5. 개발 전용 Proxy와 Production의 차이

Local Vite 개발에서는 `web/vite.config.ts`가 `/api/v1`을 Backend로 proxy한다.

개발용 Backend target:

```text
LABBIT_DEV_BACKEND_URL
기본값: http://127.0.0.1:8080
```

이 값은 **Vite dev server용 proxy 설정**이다.

Production packaging에서 동일한 dev proxy를 runtime 기능으로 기대하지 않는다. 실제 Local K8s/AWS 경로에서는 Platform의 Gateway/Ingress/Service routing이 `/api/v1/*`을 Backend로 연결해야 한다.

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
| Build context | `web/` |
| Install | `npm ci` |
| Production build | `npm run build` |
| Build output | `web/dist/` (Vite 기본값, 현재 override 없음) |
| Production serving | 정적 SPA. 서버/hosting 방식은 Platform 결정 |
| Production API mode | HTTP 고정 |
| Browser API prefix | `/api/v1` |
| Cookie | `credentials: include` |
| SPA routing | Browser history route fallback 필요 |
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

실제 Auth/Class E2E 완료는 LBT-10 준비 후 LBT-12에서 검증한다.

## 9. 이 문서에서 확정하지 않는 것

다음은 이 Frontend handoff 문서의 범위가 아니다.

- Web용 Dockerfile 구현 방식
- Nginx/Caddy 등 정적 서버 선택
- Helm template 구조
- Istio Gateway / VirtualService 상세
- EKS Web serving vs Object Storage/CDN 최종 hosting 결정
- Backend Auth/Class 구현 상태
- File/Preview/Terminal/Live의 아직 닫히지 않은 HTTP Control 계약

이 항목들은 각 담당 Jira/설계 기준에서 결정한다.

## 관련

- 현재 문서 PR은 팀 범위 확인 전의 소규모 documentation maintenance로 Jira `없음` 예외를 사용한다.
- 팀에서 별도 Jira Task로 관리하기로 결정하면 그 기준에 맞춰 추적 방식을 조정한다.
- Jira LBT-11 — FE-01 Frontend Core / Auth Consumer 기반 (완료)
- Jira LBT-12 — FE-02 Auth / Class 실제 통합
- Jira LBT-32 — CD-02 Container Image / Helm Packaging
- Jira LBT-62 — G1-Prep AWS1 Minimal Infra / Secret Readiness
- `web/package.json`
- `web/vite.config.ts`
- `web/src/shared/api/LabbitApiProvider.tsx`
- `web/src/shared/api/httpClient.ts`
- `web/src/app/router.tsx`
- `.github/workflows/web.yml`
