# LBT-22 C2 VM Access / Terminal 공동 통합 검증 Evidence — 2026-10-15

## 1. 개요 및 배경

- **Jira Issue**: [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22) — `[C2] VM Access / Terminal 공동 통합`
- **DRI**: 이주희 (Connector Control WSS / Terminal Relay)
- **참여 컴포넌트**: BE-06 (SaaS Relay) · CC-02 (Connector Transport) · OP-02 (Provider SSH/PTY)
- **PR**: [PR #103](https://github.com/ktcloud4-SL/labbit-app/pull/103) (`feat/LBT-22-c2-terminal-integration` → `main`)
- **선행 의존성 머지 확인**:
  - 서빈 님의 OP-02 최종 회귀 [PR #104](https://github.com/ktcloud4-SL/labbit-app/pull/104) (`a1de269e7333b658a5ee94854e53e661400db410`) `main` 병합 완료.
  - 최신 `origin/main`으로 clean rebase 완료. 검증 기준 commit: `e91f83eb47989ec5d1028cc64364c5f6be7ec39e`.

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
   - PTY 채널에 크기 변경 반영 확인 (`stty size` 40 120 검증)
3. **Scenario 2 (Grace Period & Reconnect)**:
   - 브라우저 소켓 비정상 강제 단절
   - 유예 시간(Grace Period) 동안 Connector 세션 및 원격 셸 환경 보존 확인
   - 동일 `sessionToken`으로 재연결(`TERMINAL_ATTACH`) ➔ `resumed: true` 및 기존 셸 상태 보존 확인
4. **Scenario 3 (Clean Exit & Cleanup)**:
   - 셸 명시적 종료(`exit 0`) 또는 close 제어 프레임
   - Safe `TERMINAL_ENDED` 이벤트 통지 및 활성 세션(ActiveCount=0) 완전 자원 해제 확인

---

## 3. 검증 결과

### 3.1. 자동 회귀 루프백 테스트 (TestTerminalC2_FullStackLoopback)

```bash
go test -count=1 -v -run TestTerminalC2_FullStackLoopback ./internal/connector/provider/openstack
```

```
=== RUN   TestTerminalC2_FullStackLoopback
    terminal_c2_integration_test.go:575: Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Echo PTY PASS
    terminal_c2_integration_test.go:591: Scenario 1B: Terminal RESIZE PASS
    terminal_c2_integration_test.go:628: Scenario 2: Browser Reconnect within Grace Period PASS
    terminal_c2_integration_test.go:644: Scenario 3: PTY Close & Safe Resource Release PASS
--- PASS: TestTerminalC2_FullStackLoopback (0.06s)
PASS
ok      github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack 3.537s
```

### 3.2. 정적 분석 및 컴파일 검증

| 항목 | 명령어 | 결과 |
| :--- | :--- | :--- |
| Go 정적 분석 | `go vet ./...` | **PASS** (0 errors) |
| 전체 패키지 빌드 | `go build ./...` | **PASS** (exited code 0) |
| C2 루프백 하네스 | `go test -run TestTerminalC2_FullStackLoopback` | **PASS** (0.06s) |

---

## 4. OP-02 → C2 환경 인계 확인

- 서빈 님의 OP-02 인계 댓글(Jira LBT-22 #10479) 및 가이드 수신 확인.
- KT Cloud D1 프로젝트 인증 설정 완료:
  - `clouds.yaml`에 `ktcloud-d1` alias, Identity Endpoint v3, Project ID (`a4dce102c4af4fd09cb65970593e17ba`) 및 CA Trust 정상 구성.
- 향후 실환경 KT Cloud D1 VM 대상 라이브 연동 시 `LABBIT_OPENSTACK_TERMINAL_C2_TEST=1` 옵트인 환경에서 `TestOpenStackTerminalC2Integration` 실행 가능.
