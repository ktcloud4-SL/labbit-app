# 이주희 — 전체 업무 현황 및 로드맵 (9/30 기준)

> **작성일**: 2026-09-30  
> **담당자**: 이주희 (담당 A)  
> **팀 위키(Confluence) 새 페이지**: [이주희 - 전체 업무 현황 및 로드맵 (9/30 기준)](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/22806531/-+9+30)  
> **GitHub 저장소**: [ktcloud4-SL/labbit-app](https://github.com/ktcloud4-SL/labbit-app)  

---

## 1. 역할 정의 (담당 A)

Connector는 2인이 분담하여 개발하며, 이주희는 **SaaS와의 양방향 제어 통신(Control WSS), 실시간 웹 터미널 스트리밍(Terminal Stream), 운영 보안 및 런타임 하드닝(Hardening)**을 총괄합니다.

* **서빈 님 (담당 B)**: OpenStack Provider 기반 실제 VM 생성/삭제/Reconcile 인프라 제어
* **이주희 (담당 A)**: SaaS ↔ Connector 제어 통신망, 실시간 PTY 터미널 스트리밍, 운영 안정화

---

## 2. 전체 업무 목록 (Jira Task Inventory)

| 구분 | Jira 티켓 | 마일스톤 | 작업명 | 핵심 내용 | 진행 상태 |
| :---: | :---: | :---: | :--- | :--- | :---: |
| **정규 1** | **`LBT-14`** | **M1** | **[CC-01] Control WSS & Dispatcher** | SaaS 연결, 인사(HELLO), 심장박동(Heartbeat), VM 제어 명령 전달 | **✅ 완료 (머지됨)** |
| **정규 2** | **`LBT-20`** | **M3** | **[CC-02] Terminal Transport** | 학생 웹 브라우저용 실시간 리눅스 터미널(PTY SSH) 스트리밍 및 60초 유예기간 | **🔄 코드 완성 (PR 대기)** |
| **정규 3** | **`LBT-23`** | **M4** | **[CC-03] Preview Transport** | 학생들이 VM 안에서 띄운 웹 사이트를 브라우저로 미리보기 해주는 포워딩 | 🚀 로컬 구현 완료 |
| **정규 4** | **`LBT-61`** | **M5** | **[CC-04] Runtime Hardening** | 운영 배포용 패키징, 비밀값 마스킹, Graceful Shutdown, 시스템 보안 강화 | ⏳ 대기 중 |
| **버그 대응** | **`LBT-92`** | Hotfix | **MockSaaS 동시 쓰기 충돌 방지** | 가짜 서버 동시 쓰기 락(`writeMu`) 및 재연결 affinity 버그 해결 | **✅ 완료 (머지됨)** |
| **버그 대응** | **`LBT-93`** | Hotfix | **Handler.Listen nil 참조 패닉 방지** | 테스트 teardown 레이스 방어 및 50회 연속 검증 통과 | **✅ 완료 (머지됨)** |

---

## 3. 지금까지 완료한 내역 (Done)

### 1) M1: 제어 통신망 및 명령 처리 파이프라인 (LBT-14 / PR #27 머지 완료)
* **보안 WSS 클라이언트 구현**:
  * 토큰 기반 인증 (`Authorization: Bearer <credential>`)
  * `labbit.connector.v1` 서브프로토콜 협상 및 1 MiB 메시지 읽기 상한 설정
* **HELLO 핸드셰이크 & Heartbeat 루프**:
  * `HELLO` 발송 및 `HELLO_ACK` 수신 검증
  * 15초 주기 `HEARTBEAT` 자동 발송 및 45초 단절 감지
* **지수 백오프 Reconnect 루프 (`Supervisor`)**:
  * 초기 1초, 최대 30초, ±20% Jitter 적용으로 비정상 단절 시 안전한 자동 재연결
* **명령 전달 디스패처 (`Handler`)**:
  * SaaS `OPERATION_COMMAND` 수신 시 유효성 검증 후 즉시 `OPERATION_ACK` 회신
  * Provider 호출 후 결과를 `OPERATION_RESULT`로 보고 (실패 시 UNKNOWN 안전 매핑)
  * `RECONCILE_REQUEST` 수신 및 자원 정합성 대조 결과 회신

### 2) M3: 실시간 웹 터미널 스트리밍 선행 구현 (`feat/SL-connector-terminal-stream`)
* **Terminal Data WSS 1:1 바이너리 스트리밍**:
  * `TERMINAL_OPEN` 수신 ➔ 관리망 VM(22번 포트) SSH PTY 세션 연결
  * 제어 소켓과 독립된 전용 데이터 소켓(`labbit.connector-terminal.v1`)으로 양방향 키보드/화면 바이트 스트리밍
* **60초 Grace Period (재접속 유예기간)**:
  * 브라우저 탭 닫힘/새로고침 시 PTY 세션을 60초간 유지하고 세션 토큰으로 즉시 복구 지원

### 3) CI 안정화 및 긴급 결함 해결
* **LBT-92 (PR #43 머지 완료)**: MockSaaS WebSocket 동시 write panic 해결 (`writeMu` 직렬화 및 reconnect connection affinity 보존)
* **LBT-93 (PR #45 머지 완료)**: Handler.Listen nil connection dereference 패닉 방어 및 테스트 라이프사이클 결정론화 (50회 연속 통과 검증)

---

## 4. 향후 로드맵 및 다음 진행 순서

1. **[지금 즉시] `LBT-20` 실시간 터미널(M3) PR 제출 및 머지**:
   - `feat/SL-connector-terminal-stream` 브랜치를 최신 `main`에 rebase 후 공식 PR 오픈 및 리뷰 요청
2. **[Checkpoint #1] 서빈 님과 통합 연동 테스트**:
   - 서빈 님이 구현한 실제 OpenStack Provider와 주희 님의 Control WSS를 단일 바이너리로 결합하여 실제 클라우드 인프라와 E2E 검증
3. **[M4] `LBT-23` Preview Transport (웹 미리보기 터널링)**:
   - 학생 실습 VM 내 웹 애플리케이션 화면을 브라우저에서 미리 볼 수 있도록 TCP/HTTP 포워딩 파이프라인 구현
4. **[M5] `LBT-61` Connector Runtime Hardening (운영/보안 마무리)**:
   - 비밀값 마스킹, Graceful Shutdown, 운영 배포용 패키징 및 설정 파일 정리
