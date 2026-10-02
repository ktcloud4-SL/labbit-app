# Labbit UX Importer

Labbit의 현재 React 화면을 자동 캡처한 뒤 Figma에 리뷰 보드 형태로 넣기 위한 **개발용 Figma Plugin**입니다.

이 도구는 Figma AI를 호출하지 않으며 외부 네트워크 요청도 하지 않습니다. 로컬에서 선택한 PNG와 `manifest.json`만 Figma 파일에 배치합니다.

## 1. UI 캡처 번들 만들기

`web` 폴더에서:

```bash
npm run capture:figma
```

기존 `capture:ui`가 Mock 전용 Vite를 자동 실행하고 Chrome/Edge를 headless로 열어 로그인부터 주요 화면까지 캡처합니다. 실제 Backend나 운영 데이터에 mutation을 보내지 않습니다.

결과:

```text
web/ui-captures/
├─ 01-login.png
├─ ...
├─ 17-reset-confirm.png
└─ manifest.json
```

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
4. `web/ui-captures` 폴더의 **manifest.json과 PNG 전체** 선택
5. **새 UX Review 페이지 만들기** 클릭

Plugin은 새 Figma Page를 만들고 다음을 자동 처리합니다.

- 로그인/수업, Workspace, 환경 생성/운영, LabSpec, 공통 상태로 화면 분류
- 화면 제목, Route, Jira 번호, 현재 상태 표시
- PNG를 원본 비율로 배치
- LBT-26/LBT-27에서 앞으로 필요한 Terminal 연결/재연결/종료 상태를 별도 계획 카드로 생성
- 기존 Figma Page는 건드리지 않음

## 안전 경계

- `capture:figma`는 기존 Mock 캡처만 사용합니다.
- 실제 계정 Password, Cookie, Session token은 Plugin/manifest/PNG 파일에 저장하지 않습니다.
- 실제 HTTPS Browser Acceptance(LBT-74)는 이 도구와 분리합니다.
- Terminal의 실제 UI 상태는 LBT-26 계약/transport가 준비되기 전에는 계획 카드일 뿐 구현 완료 Evidence로 보지 않습니다.

## 로컬 파일

Figma가 발급한 ID가 들어간 `manifest.json`은 개인 개발 환경 파일이므로 **Git에 commit하지 않습니다.** Git에는 `manifest.template.json`만 유지합니다. `git status`에 보이면 그대로 untracked 상태로 두면 됩니다.
