# LBT-22 C2 VM Access / Terminal KT Cloud D1 실환경 검증 Evidence — 2026-10-19

## 1. 개요 및 판정

- **Jira Issue**: [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22) — `[C2] VM Access / Terminal 공동 통합`
- **DRI**: 이주희 (Connector Control WSS / Terminal Relay)
- **참여 컴포넌트**: BE-06 (SaaS Terminal Relay) · CC-02 (Connector Terminal Transport) · OP-02 (OpenStack/KT Cloud D1 Provider SSH/PTY)
- **선행 루프백 검증**: [2026-10-15 사전 루프백 Evidence](../2026-10-15/README.md) ([PR #103](https://github.com/ktcloud4-SL/labbit-app/pull/103))
- **최종 판정**: **PASS (`REAL_VM_C2_ACCEPTANCE_PASS`)**
  - KT Cloud D1 실환경에서 실제 Ubuntu 24.04 Workspace VM(`os-ubuntu24.04-image-20260929`, `2x4.itl`)을 프로비저닝하고, **`Browser WebSocket (/realtime/v1/terminal) ↔ SaaS Terminal Relay ↔ Connector Control/Data WSS ↔ 실제 KT Cloud VM SSH/PTY (Linux bash)`** 전 구간 종단(E2E) 왕복 및 수명주기 시나리오 4종을 모두 통과했습니다.
  - *(참고)* 본 통합 검증 하네스의 Browser Client는 Go WebSocket 테스트 클라이언트이며, SaaS Relay는 `httptest.NewServer` 기반의 동일 Relay/Router 파이프라인으로 구동되었습니다. (실제 배포 브라우저/SaaS 연동은 PR #137 자료 참조)
  - 검증 종료 후 실습 VM 소유 리소스 5종(`SERVER`, `VOLUME`, `KT_FIREWALL_POLICY`, `KT_TIER` 2개)과 Bootstrap 소유 리소스 8종(총 13개)을 모두 삭제하고, 최종 Reconcile 및 7종 Inventory 대조 결과 **`ownedResidualTotal = 0` (`ALL_TRACKED_IDS_CONFIRMED_ABSENT`, `baselinePreserved = true`)**임을 확인했습니다.

---

## 2. 실환경 검증 아키텍처 및 경로

```
[Browser WSS Client] (Sec-WebSocket-Protocol: labbit.terminal.v1 + Session Cookie)
       ↕  /realtime/v1/terminal (TERMINAL_ATTACH / Binary I/O / TERMINAL_RESIZE)
[SaaS Control & Terminal Relay] (connectorwss + realtime.Relay)
       ↕  /connector/v1/control (TERMINAL_OPEN / TERMINAL_OPEN_RESULT / TERMINAL_ENDED)
       ↕  /connector/v1/terminal-data (labbit.connector-terminal.v1 + Bearer Credential)
[Connector Terminal Transport] (wss.Handler + terminal.SessionManager + ManagedSSHPTY + vmaccess.Manager)
       ↕  KT Cloud D1 Management Tier (172.25.241.0/24, Pinned Host Key SSH + PTY Channel)
[KT Cloud D1 Workspace VM] (Ubuntu 24.04, 2x4.itl, 50 GiB Root Volume, Linux bash)
```

---

## 3. 실행 절차 및 시나리오별 실측 결과

### 3.1. Bootstrap 및 배포 해시 검증

`tools/ktcloud-op02-bootstrap` 절차(`inventory` → `create` → `access` → `ssh` → `deploy` → `control` → `collect` → `residual` → `cleanup` → `residual` → `final`)를 따라 실행했으며, 배포된 바이너리 SHA256이 로컬 빌드와 정확히 일치함을 확인했습니다:

| 바이너리 | SHA256 |
| :--- | :--- |
| `bootstrap.exe` | `1616af91b543a694836443d6fa9beeeb4ee30cb168a316a6fb34c01e5663efbe` |
| `labbit-connector-ktcloud` | `74b3874ee94386caebdd0420e60010f6d472407a0adfca2e12328dd4e8e77c18` |
| `lbt145-control-linux` | `c5c84ab350ca22a4ec18c85615c33f69bcae70469b53eb721c5da4e98e5de9a5` |
| `labbit-workspace-sftp` | `71eda845b08547ba12d3a632ab2351f590bc6f43d2902331a1eb9b832a290f1b` |

### 3.2. `TestOpenStackTerminalC2Integration` 실환경 실행 로그

```
=== RUN   TestTerminalC2_FullStackLoopback
    terminal_c2_integration_test.go:608: Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Echo PTY PASS
    terminal_c2_integration_test.go:638: Scenario 1B: Terminal RESIZE PASS
    terminal_c2_integration_test.go:689: Scenario 2: Browser Reconnect within Grace Period PASS
    terminal_c2_integration_test.go:705: Scenario 3: PTY Close & Safe Resource Release PASS
--- PASS: TestTerminalC2_FullStackLoopback (0.02s)
=== RUN   TestOpenStackTerminalC2Integration
    terminal_c2_integration_test.go:901: OP-01 Provision ACTIVE and authenticated SSH ready for C2: resources=5
    terminal_c2_integration_test.go:1109: Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Real VM PTY I/O PASS
    terminal_c2_integration_test.go:1129: Scenario 1B: Terminal RESIZE (120x40 -> stty 40 120) PASS
    terminal_c2_integration_test.go:1193: Scenario 2: Browser Reconnect within Grace Period & State Preservation PASS
    terminal_c2_integration_test.go:1215: Scenario 3: Shell Exit, Safe TERMINAL_ENDED, and Resource Release PASS
    terminal_c2_integration_test.go:872: Tracked resource absence verified after C2 test: distinctResources=5 residual=0
--- PASS: TestOpenStackTerminalC2Integration (262.10s)
PASS
```

### 3.3. 시나리오별 검증 상세

| 단계 | 검증 내용 | 실측 결과 |
| :--- | :--- | :--- |
| **OP-01 Provision** | KT Cloud D1에 Lab Tier(`172.25.240.0/24`), Management Tier(`172.25.241.0/24`), Management SSH 방화벽 정책, 50 GiB Root Volume, Ubuntu 24.04 Workspace VM(`2x4.itl`) 생성 및 SSH Host Key 등록 | **PASS** (`resources=5`) |
| **Scenario 1A (실제 VM PTY 입출력)** | `TERMINAL_OPEN` → Data WSS 연결 → Browser `TERMINAL_ATTACH` (`resumed=false`) → `printf 'LBT22-C2-MARKER:%s\n' 'E2E_ROUNDTRIP_SUCCESS'` 전송 후 원격 `bash` 실행 결과 수신 | **PASS** |
| **Scenario 1B (실제 VM PTY 크기 조절)** | Browser에서 `TERMINAL_RESIZE (cols=120, rows=40)` 전송 → SSH `window-change` 반영 후 원격 VM `stty size` 출력이 `40 120`으로 변경됨을 확인 | **PASS** |
| **Scenario 2 (비정상 단절·유예·재연결 및 상태 보존)** | 원격 `bash`에 `export LBT22_PRESERVED_STATE='C2_RECONNECT_OK'` 설정 → 브라우저 소켓 강제 종료 → SaaS Relay `DETACHED` 전이 및 Grace Period 동안 Connector 세션(`ActiveCount=1`) 유지 확인 → 동일 `sessionToken`으로 재연결(`resumed=true`) 후 `$LBT22_PRESERVED_STATE` 값 보존 확인 | **PASS** |
| **Scenario 3 (원격 셸 종료 및 클라우드 리소스 해제)** | 원격 셸에 `exit 0` 전송 → `TERMINAL_ENDED` 이벤트 수신 및 Connector 활성 세션 해제(`ActiveCount=0`) → OP-01 `Cleanup` 및 최종 `Reconcile`로 5종 리소스 완전 부재 확인 | **PASS** (`residual=0`) |

---

## 4. 클라우드 리소스 정리 및 Baseline 보존 검증

민감정보(실제 리소스 UUID, IP, 인증키, 터미널 출력 원문)는 저장소에 포함하지 않으며, SHA256 해시로 변환된 증빙 메타데이터를 함께 기록했습니다:

- [control-ledger.metadata.json](control-ledger.metadata.json): 실습 LabInstance 소유 리소스 5종(`SERVER`, `VOLUME`, `KT_FIREWALL_POLICY`, `KT_TIER` × 2) 모두 `DELETED` 및 최종 Reconcile 부재 확인 (`CLEANUP_AND_FINAL_RECONCILE_ABSENT`)
- [bootstrap-ledger.metadata.json](bootstrap-ledger.metadata.json): 임시 Bootstrap 호스트 소유 리소스 8종(`SERVER`, `VOLUME`, `KEYPAIR`, `PUBLIC_IP`, `FORWARD`, `POLICY` × 3) 모두 삭제 확인 (`BOOTSTRAP_CLEANUP_CONFIRMED`)
- [final-residual.json](final-residual.json): 전체 10개 리소스 카테고리 잔여 수량 `0` (`ownedResidualTotal: 0`, `status: "ALL_TRACKED_IDS_CONFIRMED_ABSENT"`)
- [final-inventory.metadata.json](final-inventory.metadata.json): 실행 전후 KT Cloud D1 프로젝트의 7종 전체 리소스 Inventory ID 집합이 100% 동일함 확인 (`baselinePreserved: true`, 공유 기본 Tier 3개 유지)
- [live-lifecycle.log](live-lifecycle.log): C2 실환경 검증 단계별 타임스탬프 로그
- [validation-summary.json](validation-summary.json): 전체 검증 결과 요약
