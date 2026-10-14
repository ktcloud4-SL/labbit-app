# LBT-148 최신 main 반영 후 PR 제출 감사 — 2026-10-14

판정: **PR 가능** — LBT-148 실제 PTY·기존 CC-02 중간 통합 범위. 최신 main의 영향 검토, 현재 source 자동 검증 및 독립 리뷰가 통과했다. 별도 VMAccess 실환경 테스트는 코드 변경 없음과 사용자 지시에 따라 재실행하지 않았으며 전체 Browser/SaaS C2 완료를 뜻하지 않는다.

이 보고서는 `PRE_MERGE_AUDIT.md` A–F 절차의 최신 main 통합 추가 감사다. 전체 최초 findings/수정/실제 VM/실패 기록은 [이전 상세 감사](../2026-10-14/pre-merge-audit.md)와 [이전 독립 리뷰](../2026-10-14/independent-review.md)에 보존했다. 이전 PASS를 새 Backend/Web/HTTP 변경에 적용하지 않고 현재 source에서 기본 검사를 재실행했다.

## A. 버전·전체 diff·main 통합

- Repository `ktcloud4-SL/labbit-app`, branch `feat/LBT-148-pty-cc02`.
- 최신 main/base `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6`; 이전 base `45dfc486f2dab72ac705310e3f4f3acead1128f0`.
- 검증 source HEAD `283da48f98441395249a6ad897f62c3db18f52eb`; 이후 evidence commit은 이 디렉터리와 historical2026-10-10의 바이트 보존 `.gitattributes`만 추가한다.
- fetch 추가 main3개 commit: #93 Web Mock LabSpec ownership, #92 Provision HTTP/CreationSnapshot, #91 main GitOps update. 34파일의 변경 및 영향을 확인했다. LBT-148 commit7개는 충돌 없이 rebase했고 `range-diff`에서 전부 동일 patch였다.
- 이전 branch HEAD `b0d5ed8`와 현재 검증 HEAD의 `internal/connector/`, `go.mod`, `go.sum`, `runtime/`, `contracts/connector/`, `contracts/realtime/` diff는 0이다. 따라서 SSH·VMAccess 코드 및 실환경 test도 변경 없음이다.
- 이전 실제 VM source `efc2258`와 현재 Connector boundary의 유일한 차이는 이미 전체 재검증된 `managed_setup_cancel_test.go` loopback fixture다. production, OpenStack provider, 실제 CC02 harness, MockSaaS/TerminalRelay는 동일하다. Web/HTTP source는 달라져 이번에 재검증했다.
- base→검증 HEAD 전체34파일(Connector 코드/테스트13개, historical evidence21개)을 검토했다. 정확한 파일·blob·바이트 SHA256은 `source-manifest.json`에 전수 기록한다. 이 추가 evidence도 별도 문서/원본 로그/scan/checksum 검토 대상으로 포함한다. 미검토 구현 파일 없음.

| 변경·경계 | 목적·검토 근거 |
| --- | --- |
| terminal/ssh_pty.go, pty.go | PTY final drain/termination, late async setup ownership/cleanup; 원본 findings와 loopback 회귀 유지 |
| terminal/session.go, client.go, wss/handler.go | Data owner/reconnect/dispatch fence, tombstone/ENDED/close/write timeout; 현재 wire·producer/consumer 유지 |
| terminal의 lifecycle/data_owner/data_write/data_frame_owner/managed_setup_cancel tests | 최종 출력·실패·교체·취소·물리 PTY close·형제 생존 회귀; 이번 unit/race/20회 반복 실행 |
| openstack/pty_cc02_integration_test.go | 기존 실제 VM acceptance harness 변경 없음, 기존 raw result와 production/blob 일치 확인 |
| mock/preview_gateway.go 및 test | Mock WS writer 직렬화 및 mixed32frames 회귀; production Preview forwarding scope와 구별 |
| historical evidence2026-10-10/2026-10-14 | 당시 source/초기 실패/skip/실VM/scan/재현 helper 유지; rebase mapping으로 현재 commit과 연결 |
| 이번 evidence 디렉터리 | 새로운 base/source/자동 실행 원본·독립 리뷰·범위 구분; 소스 변경 아님 |

새 main의 integration 영향: #92는 Provision 접수/immutable CreationSnapshot의 durable 등록을 추가하고 기존 Terminal routes·ControlStack Terminal bindings·Connector transport를 바꾸지 않는다. #93 TS Mock LabSpec owner 조회는 Go MockSaaS/TerminalRelay와 무관하다. #91 GitOps update는 `push && refs/heads/main` 조건이므로 이번 branch push/PR로 main GitOps update를 실행하지 않는다. owning OpenAPI의 최신 추가 계약은 현재 Contracts 검증에 포함했다. 이 main 작업들을 LBT-148 구현으로 주장하지 않는다.

## B. 최신 SSOT 및 추적성

Atlassian Rovo 원문을 다시 조회했다. LBT-148 updated `2026-10-14T09:19:08.151+0900`, comment0, 진행 중, AC/중간 통합 범위는 동일하다. D-18 v5, D-21 v10, Connector MVP v19도 동일하다. Git의 AGENTS/CONTRIBUTING/TESTING 및 owning Connector/Realtime/runtime/HTTP 최신 경계를 확인했다. 이전 8행 상세 추적성과 코드 line은 코드 blob 동일성으로 유지되며 아래 표에서 이번 실행을 연결한다.

| 요구사항·AC/Decision | SSOT·구현 | 이번 자동 검증·실제 Evidence | 공백·판정 |
| --- | --- | --- | --- |
| AC1 PTY 생성·input/stdout·resize·종료 | [LBT-148](https://samsunglions.atlassian.net/browse/LBT-148), ssh_pty/session | 현재 SSH echo/resize/final-output 테스트 PASS, 이전 실제 VM133.81초 PASS와 blob 동일 | Connector 범위 충족. cloud 재실행 아님 |
| AC2 Control/Data WSS 왕복 | Connector README§6/7/schema, handler/client | 현재 attach/OPEN/resize/close/owner 회귀 PASS, 이전 실제 VM+Git MockSaaS/Relay rawlog | 충족. 실제 Browser/SaaS는 LBT-22 후속 |
| AC3 실패/종료 cleanup·다른 channel 유지 | [D-18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730), managed PTY/VMAccess/session | 이번 cancel·late result·old frame/error·shutdown/isolation 반복 PASS; 이전 실제 sibling PTY/SFTP 유지 및 cleanup | 충족. cloud fault packet injection 미실행 |
| AC4 참조 가능한 Evidence | Jira/TESTING, 두 날짜 기록 및 이번 source manifest | rawlogs·source mapping·재현 runner·scan·checksum·독립 리뷰 | 충족; private config/key는 재현자 로컬 입력 |
| lazy shared SSH·독립 PTY/SFTP·host key | D18/runtime, VMAccess/app/SSHPTY | 현재 VMAccess/host-key unit+race, 이전 실제 shared transport identity/SFTP | 해당 경계 충족. 별도 VMAccess Reset/idle live acceptance는 이번 미실행 |
| grace60초·current attachment1·no replay | [D-21 v10](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516), session/client/runtime | 현재 owner/grace/reconnect/tombstone/admission PASS; 이전 cloud harness3초/production60초 유지 | 실제60초 wall-clock·Browser continuity/Live는 미검증 |
| final bytes·exit/SSH error 분류 | Connector schemas, TerminationProvider/session | 현재 drain/n+EOF/exit0·7/SSH failure 회귀; 이전 실제 exit7/안전한 ENDED | unread final ENDED best effort, 원격 수신 보장 아님 |
| Control/Data 분리·본문 비저장·credential/host key | AGENTS/Connector/Realtime/runtime | 기존 auth/correlation/caps/host-key 회귀 이번 실행, 이전 실제 payload 메모리 assert | scan0 및 lifecycle rawlog 유지. SaaS/DB/운영배포 검증과 구별 |

## C. 실패·보안·수명주기 분석

전체 변경과 consumer를 재대조해 async Setup 취소/late 성공, close/reconnect owner 교체, buffered INPUT/RESIZE/CLOSE/fatal, 이전 I/O 늦은 실패, final stdout/stderr drain, SSH backpressure·pipe cleanup·channel release, closed-ID 재사용, grace timer/shutdown admission을 확인했다. 새 main이 이 경계들을 바꾸지 않았고 새 자동 회귀에서 검증한다.

VMAccess 실환경 harness의 OP-01 Reset/2세대·idle retirement는 LBT-148 CC02와 별도 경로다. 사용자 지시 및 파일/production 동일성에 따라 이번에 실행하지 않았다. historical LBT147 실행을 현재 source의 추가 live PASS로 표시하지 않는다. VMAccess 자동 회귀는 일반 로컬/SSH fixture로 실행했다.

남은 위험: tombstone process lifetime 누적/restart 소실, 강제 프로세스 종료·rolling deployment, 긴 운영 부하, 실제 SaaS/Browser 권한·Live·60초 wall-clock, 운영 SFTP File API, Preview VMAccess TCPForwarder는 이번 검증 범위 밖이다. 계약 없는 retention/재시도 정책을 새로 추가하지 않았다. 신규 HTTP/DB 구현 변경은 이 PR에 없으며 통합 소비 경계 확인과 현재 기본 검사/Contracts를 수행했다. PostgreSQL tagged integration은 이번 로컬 실행에서 제외했다.

## D. 실행·skip·원본 재현

정확한 command/start/end/source snapshots/exitcode/event counts, Web 결과 및 skip reason은 `validation-summary.json`과 archive에 있다. Go 숫자는 subtest event이며 package 수와 별도다. 현재 source에서 fmt/vet/unit/build, Connector7pkg race, 주요4pkg20회 반복, Web typecheck/lint/test/build, Contracts를 순차 실행했다. Go와 Web source snapshots 전후는 고정되었고 helper/의존성 lock은 변경하지 않았다.

기존 SSH `TestSSHPTY_RealSSH_EchoAndResize` 및 managed/VMAccess loopback 회귀는 skip하지 않고 PASS했다. 일반 suite에서 OpenStack/KT opt-in을 끈 결과 VMAccess와 PTYCC02 live tests가 SKIP된 것은 cloud 실패를 감춘 것이 아니다. 이번 별도 cloud stage는 실행하지 않았으며 이전 PTYCC02133.81초 PASS만 source 동일성 근거와 함께 참조한다. raw 최초 실패·negative expected failures는 기존 archive에 그대로 있다.

재현 runner는 이전 evidence에 version controlled로 포함되어 있으며 main rebase 이후 같은 helper를 사용했다. 개인 미추적 helper는 필수 재현 의존성이 아니다. 실제 cloud config/key는 repository에 포함하지 않는다. 새 archive의 모든 entry를 원본 바이트 및 CRC와 대조하고, 이전 evidence도 checksum 검증한다. 실제 로컬 credential/key/endpoint의 특정 값과 PEM marker는 출력하지 않고 신규/기존 제출 evidence 전체를 검사한다. universal secret detector라고 주장하지 않는다.

## E. Findings 및 수정 상태

이전 P2 findings6개(상속 shared retire, async PTY leak/race, stale Data owner effect, Preview Mock write race, SSH fixture gate, runner inherited KT opt-in)는 구현/자동 회귀/negative evidence 및 이전 독립 리뷰에서 해결됐다. SSH skip 표현 오류(P3)는 사용자 후속 확인에서 현재 코드/rawlog에 맞게 정정했다. 이번 rebase는 해당 patch를 전부 동일하게 유지했다. 신규 confirmed finding은 독립 리뷰에 기록하며 발견하면 수정 전에 사용자에게 보고한다.

| 신규 finding·심각도 | 수정 전 위치·재현·영향 | 필요한 보강·검증·상태 |
| --- | --- | --- |
| F-148-EVIDENCE-EOL-01 /P2 | 2026-10-10/artifact-checksums.json:3–7, 해당 폴더 text attribute unspecified. Windows core.autocrlf checkout에서 원본LF 텍스트5개의 CRLF 변환으로 raw-byte SHA256이 mismatch. 재현된 artifact 정합성 문제이며 코드·실제 테스트 실패가 아님. git blob 원본SHA는 기존checksum과5개 전부 일치하고 zip도 일치 | hash값을 다시 쓰지 않고 원본LF바이트를 복원, historical폴더 `.gitattributes * -text` 추가. 원래 text/code Git blob과rawzip 변경0, 해당6artifact checksum전수검증/attrunset 및 전체PRdiff whitespace검증. source snapshot의 당시CRLF bytehash는 그대로 보존. 사용자에 수정 전 severity/line/repro/영향/보강보고 후 수정, reviewer의 원본byte/hash/attr 독립 확인 PASS로 해결 |

## F. 독립 리뷰·제출 경계

구현과 분리된 기존 `review_148` reviewer가 새 base→HEAD 전체 diff, 새 main의 producer/consumer 영향, 동일 blob 주장, 최신 SSOT 전달 원문 및 신규 자동 검증 원본을 read-only로 다시 검토했다. reviewer는 Go event를 재집계하고 Web13file/167tests 및 Contracts 원본과 source stability true를 독립 확인했다. 현재 미해결 confirmed finding 없음이다. 신규artifact9개/8checksum, 이전6+13checksum, source34개/6boundarypins, rawzip50entry281001바이트·CRC·원본일치도 독립 확인했다. artifact 검토·최종 PASS 판정은 `independent-review.md`에 있다. reviewer의 실제 실행은 없으며 실행 주체는 root다. 외부 Jira/Confluence는 root가 직접 조회한 원문 전달 범위로 구별한다.

사용자 승인 범위는 **작업 branch push와 main을 base로 한 PR 생성**이며 main merge/자동 merge/배포는 금지다. 마지막 evidence commit 후 diff가 새 evidence 디렉터리와 historical바이트보존 `.gitattributes`뿐인지, tree/체크섬이 유지되는지 확인한다. PR 설명에는 current auto와 historical real VM/Mock/skip/후속 scope를 분리한다.
