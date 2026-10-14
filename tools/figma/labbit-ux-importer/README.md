# Labbit UX Importer

Labbit의 현재 React 화면을 자동 캡처한 뒤 Figma에 리뷰 보드 형태로 넣기 위한 **개발용 Figma Plugin**입니다.

이 도구는 Figma AI를 호출하지 않으며 외부 네트워크 요청도 하지 않습니다. 로컬에서 선택한 PNG와 `manifest.json`만 Figma 파일에 배치합니다.

## 1. UI 캡처 번들 만들기

`web` 폴더에서:

```bash
npm run capture:figma
```

`capture:figma`는 기존 `capture:ui`의 Mock 전용 Vite·Chrome/Edge 캡처를 재사용하고, **Figma용 실행에 한해** Editable DOM 데이터도 수집합니다. 실제 Backend나 운영 데이터에 mutation을 보내지 않습니다.

결과:

```text
web/ui-captures/
├─ 01-login.png
├─ ...
├─ 17-reset-confirm.png
├─ editable-dom.json
└─ manifest.json
```

## 기존 Figma TypeScript template에 바로 설치하기 (Windows 권장)

Figma에서 **New plugin**으로 만든 기본 TypeScript template은 `code.ts`를 빌드해 `code.js`를 생성하기 전에는 실행 시 오류가 납니다. Labbit은 별도 TypeScript build를 쓰지 않고 이 저장소의 완성된 `code.js` / `ui.html`을 그대로 사용합니다.

Figma가 만든 폴더가 예를 들어 다음이라면:

```text
C:\Users\user\Downloads\Labbit UX Importer
```

저장소 루트에서 한 번만 실행합니다.

```powershell
node tools\figma\labbit-ux-importer\install-dev-plugin.mjs "$env:USERPROFILE\Downloads\Labbit UX Importer" <FIGMA_PLUGIN_ID>
```

이 스크립트는 기존 `manifest.json`을 `manifest.figma-template.backup.json`으로 한 번 백업한 뒤 Labbit용 `manifest.json`, `code.js`, `ui.html`을 설치합니다. 이후 Figma에서 기존 **Labbit UX Importer**를 닫았다가 다시 실행하면 됩니다.

## 2. Figma 개발 Plugin ID 한 번만 만들기

Figma는 Plugin ID를 Figma에서 발급합니다. 처음 한 번만 **Plugins > Development > New plugin**에서 Custom UI 개발 Plugin을 만든 뒤 생성된 manifest의 `id` 값을 확인합니다.

그 ID로 이 폴더에서:

```bash
node setup-manifest.mjs <FIGMA_PLUGIN_ID>
```

예:

```bash
node setup-manifest.mjs 1234567890123456789
```

그러면 로컬 `manifest.json`이 생성됩니다. 이 파일에는 개인 개발 Plugin ID가 들어가므로 commit하지 않습니다.

## 3. Plugin 연결

Figma에서:

1. **Plugins > Development > Import new plugin from manifest...**
2. 이 폴더의 `manifest.json` 선택
3. **Labbit UX Importer** 실행
4. `web/ui-captures` 폴더의 **manifest.json, editable-dom.json, PNG 전체** 선택
5. **새 UX Review 페이지 만들기** 클릭

Plugin은 새 Figma Page를 만들고 다음을 자동 처리합니다.

- 로그인/수업, Workspace, 환경 생성/운영, LabSpec, 공통 상태로 화면 분류
- 화면 제목, Route, Jira 번호, 현재 상태 표시
- PNG를 원본 비율로 배치
- Terminal 연결·재연결·종료 상태를 **실제 VM E2E 검증 전 참고 카드**로 생성 (Frontend 구현 완료와 E2E 검증 완료를 구분)
- 기존 Figma Page는 건드리지 않음

## 안전 경계

- `capture:figma`는 기존 Mock 캡처만 사용합니다.
- 실제 계정 Password, Cookie, Session token은 Plugin/manifest/PNG 파일에 저장하지 않습니다.
- 실제 HTTPS Browser Acceptance(LBT-74)는 이 도구와 분리합니다.
- Terminal/File의 Frontend 코드는 구현되어 있지만, 이 도구의 참고 카드는 **Mock UX 설명용**입니다. 실제 VM 연결·WSS·SFTP·재연결 E2E 성공 Evidence로 보지 않습니다.

## 로컬 파일

Figma가 발급한 ID가 들어간 `manifest.json`은 개인 개발 환경 파일이므로 **Git에 commit하지 않습니다.** Git에는 `manifest.template.json`만 유지하며, 실제 `manifest.json`은 `.gitignore`로 제외합니다.


## 2026-10-14 보존·부분 최신화 안내

- 이 코드는 10/02 기존 [PR #60](https://github.com/ktcloud4-SL/labbit-app/pull/60)에서 **최신 main을 기준으로 선택 이식**한 개인 개발 도구의 작업본입니다. 원본 PR #60과 그 브랜치는 그대로 유지합니다.
- `capture:figma`만 `LABBIT_CAPTURE_EDITABLE=1`로 Editable DOM을 수집합니다. 기존 `capture:ui` 단독 실행은 PNG 동작을 그대로 유지합니다.
- 10/14 [PR #93](https://github.com/ktcloud4-SL/labbit-app/pull/93) 이후 **다른 소유자의 LabSpec은 읽기 전용이 아니라 403 접근 거부 화면**으로 분류합니다.
- Terminal·File consumer는 Frontend에 구현되어 있으나 이 번들은 Mock 기반 UI 검토 자료입니다. VM/WSS/SFTP 및 Local/AWS 실제 E2E 검증 결과를 의미하지 않습니다.
- 이번 단계에서는 Figma Plugin 실기, 실제 캡처 번들 생성 및 실제 Prototype 연결은 **재실행하지 않았습니다.** 이전 10/02 테스트 기록은 현재 main에 대한 테스트로 간주하지 않습니다.
- 작업 브랜치: `chore/LBT-26-figma-tooling-refresh`. **아직 리뷰 PR·병합 대상이 아니라 후속 재검증을 위한 WIP**이며 팀 CI/Web Workflow 설정은 변경하지 않습니다.
