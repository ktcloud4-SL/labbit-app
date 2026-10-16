# LBT-173 KT smoke 독립 검토

2026-10-16 Asia/Seoul. Base/HEAD `88097be5d9bb6996b718fd185515ca324629fda6`, local branch `codex/feat/LBT-173-ssh-tcp-forwarding`. 사용자가 추가 승인한 KT 단독 forwarding smoke 검토다. 원격 push/PR은 승인 범위에 포함하지 않는다. 운영 cloud mutation은 main task가 전담한다. 이 reviewer는 코드를 수정하거나 cloud mutation을 실행하지 않으며 이 evidence 디렉터리만 작성한다.

## 실행 전 검토

Preview-only hook의 실행 전 소스 검토와 자동 검증을 마쳤다. 현재 검토 소스의 KT 단독 smoke 시작을 막는 confirmed finding은 없다. 실제 cloud 결과와 exact absence/최종 baseline은 아직 검토하지 않았으며 이 결론은 KT acceptance 통과 판정이 아니다.

직접 확인한 기존 자료:

- 사용자 PRE_MERGE_AUDIT.md, 저장소 AGENTS.md/TESTING.md 및 기존 로컬 독립 검토에서 읽은 Jira LBT-173, D-18 v5, 결정 문서 v11, Connector README §7b/Preview schema, Runtime SSH/KT 설정.
- tools/ktcloud-op02-bootstrap의 README, setup/inventory/create, known-owner 검증, access의 durable unknown-receipt guard, deployment/binary hash, collect/archive/recovery, typed residual, final seven-kind baseline 검사.
- tools/ktcloud-d1-acceptance의 README/main, typed known-state 검증, 실패 후 Reconcile-before-Cleanup, recovery, Workspace SSH fixture 및 scope/ownership/unknown/cancellation 회귀 테스트.

기존 도구는 explicit KT D1/TLS 검증, 보호된 절대 경로, 빈 compute/volume baseline, immutable snapshot, private Management NIC와 기존 Server host pin을 사용한다. Bootstrap 소유 자원은 정상 완료 시 8개, Lab은 세대당 5개다. 수량만으로 cleanup을 증명하지 않으며 각 typed exact ID와 최종 7종 inventory의 baseline 일치를 확인한다. name/tag로 발견한 candidate는 cleanup 소유권으로 승격하지 않는다. 불명확한 create receipt는 durable attempted 상태를 보존해 재생성을 막는다.

## 새 hook에서 확인할 경계

아래 조건을 구현 전에 전달했고, 준비된 소스에서 각각 확인했다. 실행 소스는 [preflight-source-check.json](preflight-source-check.json)에 raw SHA256으로 고정했다. 전체 코드/도구 변경 17파일을 기존 전체 production diff와 함께 검토했으며, 기존 독립 로컬 검토의 12개 source hash는 불일치 0이었다.

| 경계 | 확인할 행동 |
| --- | --- |
| scope | Preview-only explicit opt-in, OP02=0. OP02/Preview 동시 선택, 단일 세대, recovery 혼합을 새 fixture 등록 전에 거절. 기존 기본 동작은 유지. |
| 실패 판정 | 기존 main의 OP02 전용 teardown/forced-shutdown 실패 판정을 Preview에도 적용. 실패한 cleanup을 passed=true로 남기지 않음. |
| network | HTTP는 VM loopback만 bind. Workspace app port의 public IP/forward/firewall 개방을 추가하지 않음. 기존 bootstrap SSH source /32 및 필요한 운영 API 접근 경계 유지. |
| 실제 forwarding | 실제 SSHForwarder+VMAccess, 정확한 4field target/승인 port, 같은 manager의 독립 PTY/SFTP/TCP, 안전한 closed-app 오류를 확인. full Preview Control/Data WSS 또는 Browser C3 실행으로 확대하지 않음. |
| Reset/종료 | immutable snapshot의 두 세대, old Server/target 거절, 새 VM fresh pin/접속, Reset/Cleanup 전후 fixture 채널의 명확한 소유권과 취소. 본문과 SSH_CONNECTION 비교는 메모리에서만 수행. |
| 소스 identity | 새 tool source를 포함한 manifest/diff, Linux/Windows binary SHA256 및 원격 배포 SHA256을 연결. 기존 로컬 live evidence와 새 KT tool 변경의 버전을 구분. |
| 실패·UNKNOWN | 같은 journal에서 control 재시도/새 fixture 생성 금지. collect→read-only recovery observation→typed known present만 cleanup→모든 기록 ID absence. candidate/UNKNOWN은 자동 adoption/deletion하지 않음. |
| 정리 | Lab exact IDs 부재와 bootstrap 8개 소유 ID 정리, 전체 7종 원래 baseline 복구를 각각 구분. 실패 기록과 protected journal을 보존. |

## 새 코드 검토 및 독립 실행 결과

| 확인 항목 | 코드/검증 | 결과·범위 |
| --- | --- | --- |
| exclusive fresh fixture | preview.go validatePreviewScope, main.go run의 journal 등록 전 검증; deploy.go fixtureModes/controlCommand | Preview/OP02 동시 선택·1세대·recovery를 실제 run() 외부 overlay로 호출. ledger 미생성/HTTP 미호출 PASS. recovery 명령은 두 mode를 명시 0으로 덮어씀. |
| capability/forwarding | main.go requirePreviewCapability; preview.go production SSHForwarder+VMAccess에 full Target 전달 | HELLO capability unit, exact HTTP 200/14-byte body 및 malformed/truncated/status/oversize 거절 unit PASS. 실제 VM 동작은 live 대기. |
| setup/I/O 취소 | provePreviewForwarding의 3분 context AfterFunc가 VMAccess.Close, 각 managed HTTP Conn에 10초 deadline | 무응답 HTTP header와 body 경로를 독립 net.Pipe fixture로 검증 PASS. deadline 요청값이 약 10초임을 검증하고 실제 테스트 대기는 40ms로 축소했다. SFTP/PTY 전체 취소는 코드 분석 및 기존 VMAccess regression 근거이며 독립 KT 장애 주입 실행은 아니다. |
| cleanup/강제 종료 오류 | strictRuntime = OP02 또는 Preview; 기존 deferred cleanup와 process Wait/forced kill이 실패를 runErr/Passed에 반영 | 모든 Preview 분기로 확장됨을 코드에서 확인. known inventory/UNKNOWN recovery 기존 회귀 테스트 PASS. |
| gen1/Reset/gen2 및 소유권 | 기존 Control Provision/Reset/Cleanup+typed Reconcile 그대로 재사용; 각 세대에 별도 component smoke | Snapshot/서버교체/old absent+new present 확인을 유지. Preview manager는 세대별로 닫고 gen2에서 old Server+gen1 및 current Server+gen1 둘 다 거절. 실동작은 live 대기. |
| shared channels/secret | 같은 VMAccess manager의 PTY/SFTP/TCP, SSH_CONNECTION 전후 메모리 비교, 응답본문/PTY output 비저장 | 코드 검토 완료. 이것은 실제 KT 호스트에서 component Forwarder를 호출하는 경계다. production Connector의 Preview Control/Data 경로 실행으로 확대하지 않는다. |

- 독립 실행: `go test ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap -count=1 -timeout=2m` 두 package PASS(9.744s / 3.562s). 원본 stdout은 tool transcript에 있으며 별도 raw log로 재구성하지 않았다.
- 독립 추가 probe: [preflight-probe.log](preflight-probe.log) 모든 case PASS. [probe source](preflight-probe_test.go.txt) 및 [재현 script](reproduce-preflight.ps1)를 보존했다. Go overlay의 가상 파일은 외부 temp 실제 파일을 사용하므로 저장소 tool source를 수정하지 않는다. script는 parse error 0이며 동등한 명령을 직접 실행했다.
- Confirmed findings: 없음. 초기 Content-Length fixture 오기는 구현자가 검토 전에 수정했으며 이 reviewer는 수정된 14-byte 기대값과 원본 15-byte truncated case의 구별을 확인했다.

최종 live 검토에서는 검토 source→로컬/배포 binary SHA→두 세대 smoke→typed known absence→bootstrap cleanup→seven-kind 원래 baseline의 연결을 확인한다. 기존 로컬 forwarding audit의 통과를 KT 실행 성공으로 취급하지 않는다. reviewer는 cloud API를 호출하지 않았다.

## 실행 후 독립 검토 — 원본 실패와 복구 구분

최종 확인 시각: 2026-10-16 11:27 KST. 사용자가 승인한 KT smoke의 cloud 실행은 main task가 수행했다. 이 reviewer는 cloud API를 호출하지 않고 보호된 로컬 원본, 게시 metadata, source/binary hash를 직접 대조했다. 소스 19개 변경을 검토한 범위이며, 최초 preflight 이후 추가 변경은 기존 recovery 경계의 아래 finding 수정이다. 기존 로컬 전체 production 검토의 결론은 production/Runtime/contracts/web hash 변경 0을 확인해 유지한다.

| 실제 실행 구간 | 결과와 독립 확인 |
| --- | --- |
| 최초 two-generation live | 실제 Provision(g1), Reset(g2), 두 세대 pinned management SSH direct-tcpip loopback HTTP 200/정확한 14-byte body 두 번, shared transport, 독립 PTY/SFTP 및 target/port/refused guard PASS. 전체 Preview Control/Data WSS와 Browser C3는 실행하지 않음. |
| 최초 finalCleanup | **FAILED**: `ERR_INFRA_OPENSTACK`, `OpenStack resource cleanup was rejected`. 이어 deferred teardown은 현재 남은 3개에 성공했지만 최초 run의 `exitCode=1`, `passed=false`를 보존한다. 원본 journal은 10개 중 DELETED 9/DELETE_FAILED 1이며 fresh observation과 구분한다. 거절 원인은 확인하지 않았고 production Provider 수정이나 해결을 주장하지 않는다. |
| read-only recovery | 10개 소유 typed exact ID를 조회해 present 0/absent 10. mutation 미요청. 이 조회가 journal 전부를 DELETED로 갱신한 뒤 기존 mutating recovery의 결함이 드러남. |
| 기존 mutating recovery | **FAILED**: `teardown inventory incomplete`. 아래 F-KT-001. 실패한 원본 로그/phase를 유지. |
| 수정 후 recovery | 새 control harness 배포 후 모든 journal exact ID를 다시 조회해 absence 확인, `RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED`. `passed=false`가 그대로여서 최초 live 실패를 성공으로 바꾸지 않음. 실제 재출현 자원 삭제 분기는 이번 cloud에서 실행되지 않았고 unit/semantic mutant로 검증. |
| bootstrap cleanup/최종 상태 | Cleanup phase exit 0. 최종 tracked 10종 counter 모두 0/ownedResidualTotal 0. 2026-10-16 11:21:34 KST의 7종 inventory가 baseline의 정확한 ID 집합과 일치: 원래 shared TIER 3개 유지, 다른 6종 0. bootstrap deleted=true. |

최초 SSH 준비 phase의 exit 1과 재시도 exit 0도 phase-results에 보존돼 있다. 이 reviewer는 그 실패 원인을 재현하지 않았으며 추정하지 않는다. 최초 lifecycle 전체 PASS, 독립 OP02 전체 acceptance PASS, 실제 C2/C3 PASS로 확대하지 않는다.

[final-live-check.json](final-live-check.json)은 보호된 원본의 ledger stage/passed/10개 typed ref/ID hash와 게시 metadata, binary 원본/수정 후 각 4개(총 8 digest), 최초 lifecycle whitelist 31줄, 원래 baseline/final의 실제 ID 집합과 게시 hash, final residual의 불일치 0을 기록한다. 실제 자원 ID·주소·개인키·host pin은 이 폴더에 복사하지 않았다.

## F-KT-001 / Medium — 수정 후 해결 확인

- 원본 live revision 위치: `tools/ktcloud-d1-acceptance/main.go:360`의 mutating recovery known-resource 선택. 실행 소스는 `../live-source-manifest.json` 및 `../live-source-diff.patch`에 고정돼 있다. 기존 `refs()`는 DELETED를 제외했으며 read-only 경로만 별도로 전부 포함했다.
- 재현 순서: 부분 cleanup 실패 → read-only recovery가 모든 소유 ID의 ABSENT를 확인하여 journal을 DELETED로 저장 → 같은 journal로 mutating recovery 실행. known 목록이 비어서 exact inventory 검증이 `teardown inventory incomplete`로 fail closed한다.
- 영향: 이미 삭제된 리소스의 복구/absence 절차를 마칠 수 없다. 불명확한 소유 자원에 대한 blind 삭제나 다른 소유자 삭제로 이어지는 결함은 아니다. 최초 live의 Provider cleanup 거절과 별개의 기존 harness 결함이다. Main task가 수정 전에 사용자에게 보고했다.
- 수정: `main.go:360`에서 `recoveryResourceRefs` 사용. `recovery.go:18`은 prior observed state와 무관하게 journal 소유 ID 전부를 전달하며 기존 `ownedPresentResources`의 typed exact matching/KNOWN_RESOURCE/state 검증 후 **fresh present만** Cleanup한다. 이후 전부를 다시 조회해 absence를 확인한다. candidate/UNKNOWN adoption이나 blind mutation을 추가하지 않는다.
- 회귀: `TestRecoveryAfterReadOnlyDeletedLedgerRechecksAllIDs`의 allAbsent/reappeared가 첫 번째·두 번째 조회에 모든 ID가 전달됨을 검증하고, 재출현한 1개만 현재 관측에 따라 cleanup함을 확인한다. 기존 uncertain/unknown/duplicate/type/source mismatch 검증도 유지된다.
- 독립 실행: 두 tools package PASS(8.143s/2.658s), [recovery-unit.log](recovery-unit.log). 현재 소스의 focused test [control](recovery-control.log) PASS. 외부 Go overlay로 DELETED 필터만 재도입한 **semantic mutant**는 allAbsent/reappeared 모두 예상대로 [FAIL](recovery-deleted-filter-mutant.log). 이 mutant를 역사적 원본 소스의 실행으로 표시하지 않는다. [재현 script](reproduce-recovery.ps1), [결과](recovery-probe-result.json). 저장소 소스는 수정하지 않았다.

## 최종 소스·검증 추적성과 판정

[recovery-source-check.json](recovery-source-check.json): 현재 542개 source hash와 최종 manifest의 차이는 0. 최초 live manifest와 다른 파일은 `main.go`, `recovery.go`, `recovery_test.go` 3개이며, 모두 recovery fix 경계다. 실제 두 세대 forwarding을 실행한 production Connector/Runtime/contracts/web은 그대로다. 원본 live binary/source와 수정 후 binary/source를 각각 보존했고 원격 배포 확인 digest와 일치한다. 새로운 two-generation fixture를 만들거나 원본 실패를 덮어쓰지 않았다.

[final-test-check.json](final-test-check.json): 최종 gzip JSON 원본 15,766 event를 독립 집계하여 packages pass 41/fail 0/skip 12(no-test packages), tests/subtests pass 3,671/fail 0/skip 16을 확인했다. 게시 summary와 차이 0. Main task의 tools race PASS 및 vet 빈 오류 로그도 읽었다. 이 reviewer가 최종 전체 Go suite·vet를 다시 실행했다고 표시하지 않는다. Opt-in real integration/POSIX skip은 별도 실환경 실행과 구분하며, KT 도구 smoke나 로컬 Preview 실제 VM evidence로 전체 skip을 PASS로 전환하지 않는다.

검토 중 최신 main은 `6755fc15062446b4bf0931e8e8be03efdb2563ef`로 진행됐으며 기준 base 이후 차이는 문서 3개와 C2 integration test 1개다. Production/Runtime/contracts 변화는 없다. Main task가 새 main test를 외부 overlay로 현 feature source에 추가해 Mock EchoPTY loopback test를 실행한 PASS를 읽었고, pinned git blob SHA와 실행 event를 독립 확인했다([latest-main-check.json](latest-main-check.json)). 브랜치를 rebase하거나 그 테스트를 real VM C2/C3로 해석하지 않았다.

독립 검토 범위의 **LBT-173 forwarding 구현 및 opt-in KT component smoke 변경은 PR 가능**으로 판정한다. 이전 F-R001~004와 F-KT-001은 수정/재검증됐으며 추가 confirmed source defect는 없다. **최초 KT two-generation fixture 전체 lifecycle은 FAILED**이며 Provider cleanup 거절 원인 미확인이라는 후속 위험은 남는다. Recovery로 정확한 최종 absence와 원래 baseline이 복구됐다는 사실은 원본 fixture PASS 또는 Provider cleanup 문제 해결을 뜻하지 않는다. Jira 작업 전체 완료나 전체 Preview/C3 acceptance 충족을 자동으로 선언하지 않는다. 소스 변경이 없는 한 추가 유료 fixture를 audit만을 위해 요구하지 않는다. Push/PR 생성·merge·외부 메시지 승인으로 해석하지 않는다.
최종 문서 대조(2026-10-16 11:30 KST): `../README.md`, `../AUDIT.md` 완성본과 상위 로컬 README/AUDIT의 역사적 기록 안내를 읽었다. 요구 추적 표, F-KT-001, 원본 실패/수정 후 복구, source/binary identity, 전체 fixture 실패·원인 미확인 및 후속 범위가 위 독립 판정과 일치한다. 상위의 KT 미실행 문구는 당시 snapshot임을 표시하고 최신 KT addendum으로 연결한다. 마지막 게시 source-alignment 542파일/차이0도 확인했다. 독립 reviewer는 추가 소스나 cloud를 변경하지 않았다.