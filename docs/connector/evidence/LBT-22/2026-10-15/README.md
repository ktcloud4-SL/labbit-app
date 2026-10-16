# LBT-22 C2 VM Access / Terminal 사전 루프백 검증 Evidence — 2026-10-15

## 1. 개요 및 배경

- **Jira Issue**: [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22) — `[C2] VM Access / Terminal 공동 통합`
- **DRI**: 이주희 (Connector Control WSS / Terminal Relay)
- **참여 컴포넌트**: BE-06 (SaaS Relay) · CC-02 (Connector Transport) · OP-02 (Provider SSH/PTY)
- **PR**: [PR #103](https://github.com/ktcloud4-SL/labbit-app/pull/103) (`feat/LBT-22-c2-terminal-integration` → `main`)
- **선행 의존성 머지 확인**:
  - 서빈 님의 OP-02 최종 회귀 [PR #104](https://github.com/ktcloud4-SL/labbit-app/pull/104) (`a1de269e7333b658a5ee94854e53e661400db410`) `main` 병합 완료.
  - 최신 `origin/main`으로 clean rebase 완료.
- **본 문서의 성격**:
  - 본 PR #103에서 구현 및 검증된 **사전 루프백 테스트 하네스(`TestTerminalC2_FullStackLoopback`)**의 실행 및 회귀 검증 Evidence입니다.
  - **검증 대상**: simulated Browser WebSocket + 실제 Relay/Connector 구현 + **Mock Echo PTY**
  - **미검증 / 후속 범위**: 실제 KT Cloud VM의 SSH 채널, Linux bash 셸, 원격 셸 프로세스 상태 보존, 클라우드 가상머신 자원 전체 해제는 이번 Loopback 실행에서 검증되지 않았으며, LBT-22 최종 Acceptance 단계에서 실측 Evidence를 별도로 확보합니다.

---

## 2. 검증 범위 및 아키텍처

LBT-22 사전 루프백 하네스는 **Simulated Browser WSS ↔ SaaS Relay ↔ Connector Transport ↔ Mock Echo PTY** 전체 프로토콜 왕복 경로와 연결 수명주기를 메모리 상에서 종단간(E2E) 검증합니다.

```
[Simulated Browser WSS]
      ↕ (Sec-WebSocket-Protocol: labbit.terminal.v1 / SessionCookie)
[SaaS Terminal Relay] (/realtime/v1/terminal ↔ /connector/v1/terminal-data)
      ↕ (Sec-WebSocket-Protocol: labbit.connector-terminal.v1 / Bearer Token)
[Connector Terminal Transport] (SessionManager / ManagedSSHPTY)
      ↕ (인메모리 바이트 스트림 / Channel 동기화)
[Mock Echo PTY] (c2ResizeAwarePTY / In-Memory Mock)

※ [실제 KT Cloud VM PTY (Linux bash)]는 후속 실환경 Acceptance 대상 (미실행)
```

### 2.1. 검증 경계 (Mock Loopback vs 실환경 KT Cloud VM)

| 구분 | PR #103 사전 루프백 하네스 검증 (본 Evidence) | LBT-22 최종 Acceptance (후속 예정) |
| :--- | :--- | :--- |
| **검증 환경** | CI / 로컬 인메모리 루프백 | 실제 KT Cloud D1 OpenStack 인프라 |
| **PTY 구현체** | `Mock Echo PTY` (`c2ResizeAwarePTY`) | 실제 Workspace VM (`Linux bash / SSH PTY`) |
| **I/O 스트리밍** | 브라우저 ↔ Relay ↔ Connector ↔ Mock Echo 바이트 스트림 | 브라우저 ↔ Relay ↔ Connector ↔ SSH 채널 ↔ Linux VM PTY |
| **RESIZE 제어** | 제어 프레임 채널 동기화 수신 확인 | 실제 VM OS 커널 PTY 윈도우 크기(`TIOCSWINSZ`) 변경 실측 |
| **재연결 (Reconnect)** | `DETACHED` 전이 관측 후 Grace Period 내 재연결(`resumed=true`) | 실제 브라우저 재연결 후 VM 원격 셸 프로세스 유지 실측 |
| **종료 및 정리** | `echoPTY.Close()` ➔ `TERMINAL_ENDED` 수신 및 인메모리 `ActiveCount() == 0` | 실제 셸 `exit 0` 종료 ➔ VM 세션 및 클라우드 자원 해제 실측 |
| **실행 방법** | `go test -run TestTerminalC2_FullStackLoopback` (기본 실행) | `LABBIT_OPENSTACK_TERMINAL_C2_TEST=1 go test` (Opt-in) |

### 2.2. 루프백 검증 시나리오

1. **Scenario 1A (I/O Roundtrip - Mock Loopback)**:
   - Simulated 브라우저에서 `TERMINAL_ATTACH` 후 stdin 바이너리 데이터(`printf 'LBT22-C2-MARKER:...'`) 전송
   - Relay ↔ Connector ↔ Mock Echo PTY 왕복 후 브라우저 stdout에 동일 마커 에코 수신 확인 (바이트 단위 프로토콜 파이프라인 검증)
2. **Scenario 1B (Terminal Resize - Mock Loopback)**:
   - 브라우저에서 `TERMINAL_RESIZE(120, 40)` 제어 프레임 전송
   - 채널 알림(`c2ResizeAwarePTY`)으로 Data Race 및 고정 Sleep 없이 결정론적(Deterministic) 크기 변경 동기화 확인
3. **Scenario 2 (Grace Period & Reconnect - Mock Loopback)**:
   - 브라우저 소켓 비정상 강제 단절
   - SaaS Relay 상에서 실제 `DETACHED` 상태 진입 및 Grace period 활성화 확인
   - 유예 시간 동안 Connector 세션(`ActiveCount=1`) 및 Mock PTY 세션 보존 확인
   - 동일 `sessionToken`으로 재연결(`TERMINAL_ATTACH`) ➔ `resumed: true` 확인
   - *(※ 실제 Linux bash 원격 프로세스 상태 보존은 실환경 VM C2 Acceptance에서 검증)*
4. **Scenario 3 (Mock PTY Close & Connector Session Cleanup)**:
   - `echoPTY.Close()` 호출
   - Safe `TERMINAL_ENDED` 이벤트 수신 및 Connector 인메모리 세션 정리(`ActiveCount() == 0`) 확인
   - *(※ 실제 원격 Linux 셸 `exit 0` 프로세스 종료 및 KT Cloud VM 가상머신 자원 전체 해제는 이번 Loopback에서 검증하지 않으며, 실환경 VM C2 Acceptance에서 검증)*

---

## 3. PR #103 리뷰 피드백 반영 내역

박희철 팀장 님의 리뷰 코멘트(`CHANGES_REQUESTED`) 사항을 모두 보완했습니다:

| 항목 | 리뷰 지적 사항 | 조치 및 해결 내역 |
| :--- | :--- | :--- |
| **Go CI (gofmt)** | `gofmt` 불일치 수정 및 포맷 검증 | `gofmt -w` 적용 완료 (`gofmt -l` 0 diff 확인) |
| **보안 (PTY 원문 차단)** | 테스트 실패 시 PTY 출력 원문이 로그에 남지 않도록 수정 | `readBinaryUntil`, 1A/1B/2 시나리오에서 바이트 원문 대신 `len(output)` 메타데이터만 로깅 |
| **RESIZE (Race/Sleep)** | 고정 Sleep 및 Data Race 가능성 제거 | `c2ResizeAwarePTY` 래퍼 및 채널 이벤트 동기화 도입 (`go test -race` 0 data race 통과) |
| **Reconnect (DETACHED)** | 실제 DETACHED/Grace 진입 확인 후 재연결 검증 | 브라우저 단절 후 `auth.isDetached(c2SessionID)` 상태 전이 확인 후 재연결 수행 |
| **테스트 환경 플래그** | 기존 CC-02와 신규 C2 실환경 실행 플래그 분리 | `TestOpenStackTerminalC2Integration`에서 `LABBIT_OPENSTACK_PTY_CC02_TEST` 제거, `LABBIT_OPENSTACK_TERMINAL_C2_TEST` 전용 분리 |
| **문서 정합성** | Loopback 검증과 실제 VM Acceptance 구분 | `JUHEE_DEV_STATUS.md`, `README.md`, `validation-summary.json`에서 Mock PTY 루프백 범위와 미실행 실환경 C2 범위를 명확히 분리 |

---

## 4. 검증 결과

### 4.1. 루프백 E2E 및 Race 검증

```bash
go test -race -count=1 -v -run TestTerminalC2_FullStackLoopback ./internal/connector/provider/openstack
```

```
=== RUN   TestTerminalC2_FullStackLoopback
    terminal_c2_integration_test.go:608: Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Echo PTY PASS
    terminal_c2_integration_test.go:638: Scenario 1B: Terminal RESIZE PASS
    terminal_c2_integration_test.go:689: Scenario 2: Browser Reconnect within Grace Period PASS
    terminal_c2_integration_test.go:705: Scenario 3: PTY Close & Safe Resource Release PASS
--- PASS: TestTerminalC2_FullStackLoopback (0.02s)
PASS
ok      github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack 4.050s
```

### 4.2. 정적 분석 및 컴파일 검증

| 항목 | 명령어 | 결과 |
| :--- | :--- | :--- |
| Go 포맷 검사 | `gofmt -l` | **PASS** (0 diff) |
| Go 정적 분석 | `go vet ./...` | **PASS** (0 errors) |
| 전체 패키지 빌드 | `go build ./...` | **PASS** (exited code 0) |
| C2 루프백 하네스 (Race 포함) | `go test -race -run TestTerminalC2_FullStackLoopback` | **PASS** (0.02s, 0 race) |
| **실환경 KT Cloud VM C2 검증** | `LABBIT_OPENSTACK_TERMINAL_C2_TEST=1` | **미검증 (NOT RUN)** — 후속 LBT-22 Acceptance에서 검증 예정 |

---

## 5. OP-02 → C2 환경 인계 및 후속 검증 계획

- **환경 인계 확인**:
  - 서빈 님의 OP-02 인계 댓글(Jira LBT-22 #10479) 및 가이드 수신 확인.
  - KT Cloud D1 프로젝트 인증 설정 완료:
    - `clouds.yaml`에 `ktcloud-d1` alias, Identity Endpoint v3, Project ID (`a4dce102c4af4fd09cb65970593e17ba`) 및 CA Trust 정상 구성.
- **후속 Acceptance 계획**:
  - 본 PR #103은 C2 프로토콜 파이프라인의 사전 루프백 하네스 구축을 목적으로 하며, 코드가 머지된 후 실제 KT Cloud OpenStack VM과의 실환경 통합 검증(실제 Linux bash 셸 종료, VM 프로세스/네트워크 자원 해제)은 LBT-22 최종 Acceptance 단계에서 `LABBIT_OPENSTACK_TERMINAL_C2_TEST=1` 플래그로 공동 진행하여 완료 처리할 예정입니다.
