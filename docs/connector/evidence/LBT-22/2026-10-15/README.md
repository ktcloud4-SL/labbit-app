# LBT-22 C2 VM Access / Terminal 공동 통합 검증 Evidence — 2026-10-15

## 1. 개요 및 배경

- **Jira Issue**: [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22) — `[C2] VM Access / Terminal 공동 통합`
- **DRI**: 이주희 (Connector Control WSS / Terminal Relay)
- **참여 컴포넌트**: BE-06 (SaaS Relay) · CC-02 (Connector Transport) · OP-02 (Provider SSH/PTY)
- **PR**: [PR #103](https://github.com/ktcloud4-SL/labbit-app/pull/103) (`feat/LBT-22-c2-terminal-integration` → `main`)
- **선행 의존성 머지 확인**:
  - 서빈 님의 OP-02 최종 회귀 [PR #104](https://github.com/ktcloud4-SL/labbit-app/pull/104) (`a1de269e7333b658a5ee94854e53e661400db410`) `main` 병합 완료.
  - 최신 `origin/main`으로 clean rebase 완료.

---

## 2. 검증 범위 및 아키텍처

LBT-22는 **Browser WSS ↔ SaaS Relay ↔ Connector Transport ↔ PTY 터미널** 전체 왕복 경로와 연결 수명주기를 종단간(E2E) 검증합니다.

```
[Browser WSS]
      ↕ (Sec-WebSocket-Protocol: labbit.terminal.v1 / SessionCookie)
[SaaS Terminal Relay] (/realtime/v1/terminal ↔ /connector/v1/terminal-data)
      ↕ (Sec-WebSocket-Protocol: labbit.connector-terminal.v1 / Bearer Token)
[Connector Terminal Transport] (SessionManager / ManagedSSHPTY)
      ↕ (RFC 4254 PTY 바이트 스트림 / SSH Channel)
[Workspace VM PTY] (Linux bash / echo PTY)
```

### 검증 시나리오

1. **Scenario 1A (I/O Roundtrip)**:
   - 브라우저에서 `TERMINAL_ATTACH` 후 stdin 바이너리 데이터(`printf 'LBT22-C2-MARKER:...'`) 전송
   - Relay ↔ Connector ↔ PTY 왕복 후 브라우저 stdout에 동일 마커 에코 수신 확인
2. **Scenario 1B (Terminal Resize)**:
   - 브라우저에서 `TERMINAL_RESIZE(120, 40)` 제어 프레임 전송
   - 채널 알림(`c2ResizeAwarePTY`)으로 Data Race 및 고정 Sleep 없이 결정론적(Deterministic) 크기 변경 확인
3. **Scenario 2 (Grace Period & Reconnect)**:
   - 브라우저 소켓 비정상 강제 단절
   - SaaS/Relay 상에서 실제 `DETACHED` 상태 진입 및 Grace period 활성화 확인
   - 유예 시간 동안 Connector 세션(ActiveCount=1) 및 원격 셸 환경 보존 확인
   - 동일 `sessionToken`으로 재연결(`TERMINAL_ATTACH`) ➔ `resumed: true` 및 기존 셸 상태 보존 확인
4. **Scenario 3 (Clean Exit & Cleanup)**:
   - 셸 명시적 종료(`exit 0`) 또는 close 제어 프레임
   - Safe `TERMINAL_ENDED` 이벤트 통지 및 활성 세션(ActiveCount=0) 완전 자원 해제 확인

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

---

## 5. OP-02 → C2 환경 인계 확인

- 서빈 님의 OP-02 인계 댓글(Jira LBT-22 #10479) 및 가이드 수신 확인.
- KT Cloud D1 프로젝트 인증 설정 완료:
  - `clouds.yaml`에 `ktcloud-d1` alias, Identity Endpoint v3, Project ID (`a4dce102c4af4fd09cb65970593e17ba`) 및 CA Trust 정상 구성.
- 실제 OpenStack VM 통합 검증 Evidence는 LBT-22 최종 Acceptance에서 확인 예정.
