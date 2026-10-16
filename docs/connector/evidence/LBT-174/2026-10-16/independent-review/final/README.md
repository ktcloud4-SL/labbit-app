# LBT-174 최종 독립 검토

2026-10-16 KST. 구현을 수행하지 않은 reviewer가 사용자 `PRE_MERGE_AUDIT.md`의 A–F 절차로 검토했다. 제품 소스·기존 Evidence·cloud·원격 Git을 변경하지 않았다. 이 폴더의 snapshot/overlay/probe/report만 저장소 밖에 작성했다.

**검토 범위에서 미해결 blocking 제품 결함 없음. 최종 소스의 로컬 구현·축소 실환경 검증은 통과했다.** 원격 CI는 미실행이므로 Jira의 “관련 CI 통과”는 미확인이다. Jira 전체 완료와 원격 push/PR/merge/배포 승인을 뜻하지 않는다. 저장소 Evidence 문서·복사본·재현 절차의 최종 대조는 [packaging addendum](PACKAGING_ADDENDUM.md)에 기록했다.

## A. 버전·전체 diff 범위

- Repo: `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-connector-gates-20261015`
- Branch: `codex/feat/LBT-174-preview-local-integration`
- 최초 base: `4691c0ae8a43955549ae2b9e8bc4f5a464c30001`; 최종 base/HEAD: `0051d8d6803fd447f7b690dd895a0a928c3c7302`. 구현은 미커밋이다.
- 최종 작성자 manifest Go/module 495파일을 독립 snapshot과 LF 정규 SHA-256으로 비교해 차이0. aggregate `956c7d6f3b87f6d49d1b842571124da4591db40f5bc15de5c6efce9d65fe98b9`. [source-alignment.json](source-alignment.json), [snapshot-manifest.json](snapshot-manifest.json). reviewer 전용 zz fixture3개는 비교에서 제외했다.
- 최신 main의 LBT-176 7파일(Backend Worker/Postgres/repository/문서)은 직접 diff를 읽었다. Connector Preview/wire/runtime/dependency와 겹치지 않는다. 새 base에서 최종 전체 검증을 다시 실행했다.
- ignored disposable 개인키와 외부 clouds.yaml은 실제 실행 입력이나 이 보고서/snapshot에 복사하지 않았다. 재현에는 사용자가 자신의 local cloud·SSH 입력을 준비해야 한다.

검토한 코드 파일은 아래15개 전부다. 코드 누락 없음. Evidence 폴더는 최종 취합본을 별도 재확인한다. 변경되지 않은 제품 전체를 새로 감사한 것은 아니다.

| 파일 | 목적·관련 요구·영향 경계 |
| --- | --- |
| app/app.go | 공유 VMAccess/PreviewManager 연결, 영구 Shutdown; App Run/Supervisor/Terminal/File 영향 |
| app/vm_access.go | Reset/Cleanup 결과에 따른 Preview 세대 퇴역, preflight 보존 |
| app/vm_access_test.go | 기존16개 outcome 분기에 pending Preview 보존/퇴역 assertion |
| preview/manager.go | 네트워크 전에 pending reservation, 중복/동시 active 경계, generation/Shutdown fence, registry detach와 I/O 분리 |
| preview/session.go | attempt별 고정 TCP/WSS, stale attach 차단, bounded close, callback/lock 수명 |
| preview/client.go | exact attempt attach, credential/TLS/subprotocol/correlation, Upgrade/ATTACH 취소, raw socket handoff, bounded JSON |
| preview/lifecycle_test.go | pending late dial, stale ACK, blocked writer, attach 취소, duplicate/isolation, generation/shutdown 회귀 |
| preview/upgrade_cancel_test.go | Upgrade 응답이 없는 실제 loopback peer 취소 회귀 |
| wss/handler.go | Preview만 비동기64 worker bound, epoch와 owner 수명, 원자 trust retirement, direct replacement |
| wss/handler_preview.go | 기존 handler 이동·보강, immutable sender/credential/owner, 동기 admission, exact CLOSE, safe result |
| wss/handler_preview_test.go | 첫 tunnel 종료 후 새 OPEN을 보내 sequential 계약을 지키는 fixture |
| wss/preview_owner_test.go | old epoch/credential, reader 취소, result write와 tunnel 취소 격리, 교체 owner, worker 회수 |
| wss/preview_replacement_test.go | 실제 ATTACH→idle→direct Control replacement에서 logical 퇴역 |
| provider/openstack/preview_transport_export_test.go | 실제 App 재사용을 위한 test-only adapter export, 자원 기록·정확 ID teardown |
| provider/openstack/preview_transport_integration_test.go | opt-in 실제 App/Control/Provider/SSH/local VM + strict Mock Gateway |

위 경로는 모두 `internal/connector/` 아래다. dependency/DB migration/HTTP/Realtime/runtime 계약 변경 없음. owning `contracts/connector/README.md` §7b 및 preview-control/data schema, runtime Preview/VMAccess, AGENTS/CONTRIBUTING/TESTING, 관련 consumer를 읽고 적용을 판단했다. Browser 인가·Backend exact port allowlist·TTL·DB operation 복구는 기존 owner의 경계이며 이 코드에서 새로 구현하지 않는다.

## B. 최신 원문 → 구현 → 테스트 → 실환경

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174)를 최종 직접 재조회했다. updated14:59:23.423+0900, 댓글0, 본문/AC는 최초 조회와 동일하다. Confluence D18v5/결정완료v11/테스트전략v3/D20v7/추가결정v3를 직접 재조회했다([ssot-read.json](ssot-read.json)). 세부 Preview wire는 과거 협의보다 현재 Git §7b/schema가 우선한다.

| 요구·원문 | 구현 / 자동 회귀 | 실제 최종 실행 및 공백 | 판정 |
| --- | --- | --- | --- |
| LBT174 기존 CC03 + D18 실제 Workspace SSH TCP | app.BuildConnector, manager.PrepareOpen/Open, 기존 VMAccess/SSHForwarder 재사용 | 실제 App Control→outbound WSS→SSH direct-tcpip→VM HTTP200 | 로컬 component 충족 |
| §7b 선개설·OPEN_RESULT·ATTACH/ATTACHED | handler_preview.go:16, manager.go:45/90, client.go:99; existing Handler/attach 오류 suite | strict Mock Gateway가 full OPEN correlation/대상 필드 확인, HTTP binary 왕복 | 충족; SaaS/Gateway 자체는 Mock |
| §7b 동일 Control credential·stale attach/late 결과 | handler immutable owner/credential/sender; session.attempt/AttachDataConn; stale/owner 회귀 | loopback 지연 ACK와 old epoch probe; actual strict attach 대조 | 충족; 실제 SaaS rotate/revoke 인가 미실행 |
| 1 WSS=1 TCP, session active≤1, sequential | manager reservation, session.reserveTunnel:191, 고정 stream pair; duplicate9호출·active conflict | 동일 session HTTP 두 번 sequential; 두 session CLOSE 격리·sibling HTTP | 충족 |
| CLOSE·upstream/Data1000·에러·logical 보존 | session.closeWithCode:81/pipe, exact CloseAttempt, normal1000/invalidText/gatewayClose suite | 실제 Connection:close 뒤 후속 HTTP, 명시 CLOSE 격리 | 충족 |
| Reset/Cleanup·old generation·Shutdown | app/vm_access.go:61/71; manager.InvalidateBefore/Shutdown; outcome16분기·late open fence | 실제 Control Reset gen1→2, old WSS 닫힘/registry 제거, stale 거부/g2HTTP, Cleanup 후 stale 거부 | 충족 |
| 안전한 오류/Secret·본문 비수집 | safe Handler mapping, unchanged SSHForwarder guards, JSON bounded; raw-error/cancel tests | actual APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE, HTTP/PTY/합성 credential의 Control 부재 assertion | 실행한 분기 충족; 실제 모든 장애는 아님 |
| cancel/backpressure/worker/shutdown 누수 경계 | close에서 data write mutex를 기다리지 않음, raw socket cancel/handoff, owner retirement, 64slots | independent3pkg race×3 및 R006/R007/R008/ordinaryEOF 각10PASS | 검토 경계 충족; 장시간 부하·전역 goroutine0 미주장 |
| Terminal/File 공유 SSH 회귀 | 기존 shared VMAccess, App lifecycle, 전체 Go +9pkg race | actual Preview 종료 뒤 PTY marker/SFTP stat | component 충족; Browser Terminal/File WSS 전체 E2E 아님 |
| 실제 자원 Cleanup | fixture 결과 추적, inherited cleanup exact GET absence | entries28(중복 포함) Lab ID 부재; SG/keypair는 별도 helper DeleteACK 수준 | 축소 lifecycle 충족; unique28개나 전체 cloud baseline 미주장 |
| 자동 회귀/관련 CI/Runbook | 최종 fullGo/vet/build/race, reproduce-local.ps1 | 아래 D; 원격 CI/KT/BrowserC3/taggedPG 미실행 | 로컬 통과, 원격CI AC 대기 |

## C. 실패·보안·수명주기

authority→manager→session→tunnel 순서로 짧은 상태 변경만 잠근다. session/tunnel callback은 자체 lock을 푼 뒤 호출한다. registry를 먼저 분리한 다음 socket I/O와 callback은 Handler authority lock 밖에서 수행한다. old callback은 정확한 session pointer와 attempt ID를 검사하므로 새 ID 재사용이나 sequential tunnel을 삭제하지 않는다.

Close/Reset/Shutdown이 pending dial을 소유한다. 늦게 반환한 TCP는 해당 tunnel.done을 확인해 닫고, permanent Shutdown은 새 admission을 막는다. initial 실패와 established logical session의 follow-up 실패를 구분한다. 일반 Data1000/ordinary network reconnect는 logical state를 유지하며, 직접 Control 교체/4001·4002 trust 종료는 이전 snapshot을 퇴역한다.

Credential은 승인한 Control sender에서 한 번 캡처하고 다른 epoch OPEN/CLOSE의 admission을 재검사한다. Data setup 취소가 shared Control result write를 취소하지 않도록 captured sender +2초 WithoutCancel notification으로 분리됐다. Ctrl durable Provider dispatch 수명은 이번 Preview 비동기 분리와 바꾸지 않았다.

Frame 크기/종류와 port22/범위/target validation을 확인했다. Backend allowlist를 임의로 복제하지 않았고, management NIC/Workspace/server/generation/host pin 검증은 merged VMAccess/SSHForwarder 경로다. Binary stream은32KiB chunk이며 HTTP message/frame boundary를 의미로 해석하지 않는다. Peer close text는 허용된 safe reason 외에는 반사하지 않는다. HTTP/PTY/credential/raw Provider payload를 Control/영속 저장에 추가하지 않는다.

Provider 실패의 preflight 보존과 partial/UNKNOWN 퇴역을 구분한다. 이 PR은 Provider blind retry/DB reconciliation 정책을 추가하지 않는다. Preview는 process-ephemeral 상태이므로 재기동 복원을 약속하지 않는다. 실제 durable Backend worker/PG 경로는 이 Mock Control fixture가 검증하지 않는다.

## D. 직접 실행/원본 대조

Reviewer 직접 실행은 저장소 밖 고정 snapshot에서 수행했다. cloud를 호출하지 않았다.

| 실행 | 원본·결과 |
| --- | --- |
| R006 old trust retirement vs replacement, unlock 후20ms scheduling overlay | [원본](old-retirement-scheduled-retest.log) 10/10PASS. 수정 전 같은 overlay3/3FAIL은 firstpass 폴더 보존 |
| R007 Upgrade 응답 보류 중 context cancel | [원본](upgrade-replacement-normal-reconnect-retest.log) 10/10PASS; 실제 loopback HTTP socket |
| R008 actual ATTACH→Data1000→idle→direct nonnil replacement +late old4002 | 같은 원본10/10PASS; 수정 전3/3FAIL은 secondpass 폴더 보존 |
| ordinary EOF→nil→reconnect logical 보존 | 같은 원본10/10PASS |
| Preview/WSS/App boundary `go test -race ... -count=3` | [원본](boundary-race.log) 3packages 모두PASS |
| baseline4691 두 timing tests 각20회 | ../lbt174-baseline-probes-4691c0a: VMAccess20PASS; Realtime19PASS+동일1FAIL, source동일 |

작성자 최종 원본/result를 reviewer가 직접 읽었다(`../lbt174-final-audit-20261016/`). final source956c before/after unchanged=true다.

- local-openstack-final: exit0, command228.781초/test221.95초. HTTP twice/sequential, concurrent isolation, CLOSE, PTY/SFTP, safe errors, Control Reset/Cleanup/stale/g2HTTP, forbidden Control content, 정확 Lab 부재 entries28 모두 PASS.
- full Go serial: exit0,41packages/3737PASS events(하위테스트 포함)/18skip/실패0. 실환경 opt-in14항목과 OS조건4항목의 일반-suite skip을 명시적 local fixture 실행과 분리한다.
- vet/build: metadata exit0; 로그가 빈 것을 독립적인 성공 근거로 삼지 않는다.
- race9packages: exit0,122.843초, 관련 Provider/VMAccess/Preview/App/Terminal/File/WSS/tools 포함.
- 웹4checks: 이전96ec 소스에서 exit0,14files/188tests. 최종까지 web source diff0이며 latest-main은 Backend Worker 변경이다. 이 적용성 한계를 명시하며 최종495 Go hash 실행이라고 부르지 않는다.
- gofmt canonicalLF493 Go파일 차이0, git diff --check exit0. CRLF 차이를 behavioral finding으로 보고하지 않았다.

**실패 이력은 최종PASS로 덮지 않는다.** 최초 실제 local run은 Reset 뒤 stale OPEN 결과 누락으로 FAIL이고 entries21 teardown부재를 확인했다. 이후 result context 수정과 최종 실제 성공을 구분하며 최초 원인은 단일 관측으로 확정하지 않는다. 이전96ec fullGo 병렬은2실패(exit1): vmaccess40ms setup timeout/Realtime빈 log JSON. feature 반복은 VMAccess20PASS,Realtime19PASS1FAIL. 독립 base에서도 Realtime19PASS1동일FAIL이 자연재현돼 inherited Low test defect다. 최초 feature의 정확한 scheduling 원인은 추적 부재로 확정하지 않는다. VMAccess의 최초 원인은 미확정이며 load sensitivity는 후보일 뿐이다. unrelated base code를 이 기능에서 바꾸지 않았다.

## E. 수정 전 findings / 재검토

초기5개는 ../lbt174-initial-review-4691c0a/README.md, R006/R007은 firstpass, R008은 secondpass 원문/실패 로그에 severity·정확 revision/line·재현·영향·회귀를 기록해 **수정 전에 parent에게 전달했다**. 모든 제품 수정은 구현자가 수행했다.

| Finding | 최초 위치·재현 | 최종 상태 |
| --- | --- | --- |
| R001 High | base client252/session309–336: M1 late ACK가 M2에 bind | exact immutable attempt/socket·cleanup, stale 회귀 통과 |
| R002 High | base manager87/154/167: pending Close/CloseAll 후 late success 부활 | synchronous reservation +cancel/fence, lateconn close 회귀 통과 |
| R003 High | base session60–66/88–91: writer mutex가 Close를 무기한 차단 | bounded WriteControl/socketClose, blocked writer 회귀 통과 |
| R004 Medium | base client194/204–207: ATTACHED wait cancel 미해제 | setup socket 추적/exact cancel, 회귀 통과 |
| R005 High | base handler160–199/1344–1351: old epoch가 새 credential 사용 | captured owner/sender/credential+admission gate, 회귀 통과 |
| R006 High | firstpass handler1022–1036: epoch검사→unlock→replacement→새owner cancel/CloseAll | atomic snapshot retirement, 같은 overlay10PASS |
| R007 Medium | firstpass client154–179: Upgrade 무응답 중 cancel 미해제 | raw socket cancel→exact tunnel handoff, 같은 probe10PASS |
| R008 Medium | secondpass handler80–97/1006: direct replacement 이후 idle oldlogical 잔류 | direct nonnil replacement만 retirement, sameprobe10PASS+ordinaryEOF10PASS |
| R009 Medium (작성자 발견) | result write가 tunnel ctx 취소를 shared Control close로 전달 | bounded WithoutCancel result +capturedsender, source/회귀148행 재검토·boundaryrace 통과 |

새 confirmed 제품 finding은 없다. inherited Realtime polling helper Low와 VMAccess 최초 timeout 미확정은 별도 제한이다. probe에는 fake/loopback peer·fault scheduling이 있으므로 실제 SaaS revoke나 cloud failure 재현으로 확대하지 않는다.

## F. 결론·남은 경계

최종 source956c에서 요구한 local OpenStack + Mock Gateway component HTTP/lifecycle 경로와 failure/security/concurrency 회귀를 확인했다. 수정한 R001–R009는 관련 source와 의미 있는 회귀로 해소됐다. 독립 reviewer가 원문·전체 변경·실행 소스·실환경 원본을 다시 확인했으며 작성자 PASS 설명만으로 판정하지 않았다.

로컬 구현/검증 범위는 통과다. 원격 CI AC는 미확인으로 남고 Jira 전체 완료는 유보한다. 실제 KT Cloud(OP03후속), 실제 SaaS/Browser C3, tagged PostgreSQL, multiple actualVM topology, 운영 TLS/LB draining/장시간 부하, hardcrash/DB복구는 미실행이다. SSH SG/keypair DeleteACK-only 한계와 duplicate-inclusive entries 수를 유지한다. 이 검토는 push/PR/merge/배포/외부 전송 승인이 아니다.

## Packaging 대조

[check-evidence.py](check-evidence.py)를 최종 문서 취합 뒤 재실행해 복사된 JSON51개 파싱, compressed Go JSONL4개 압축 해제·event 재집계, raw가 남아 있는 최종4191948bytes JSONL과 byte동일, source495개 hash차이0, source-diff15파일(새7파일포함)을 확인했다. 상세 해시·파일별 provenance는 [evidence-packaging-check.json](evidence-packaging-check.json)이다. 이 inventory는 reviewer addendum 자체가 저장소에 복사되기 전의 관측 snapshot이며, 이후 추가된 reviewer 문서는 작성자의 최종 Evidence SHA manifest로 식별한다.

F174-DOC001 / Low를 수정 전에 parent에게 보고했다: repository Evidence independent-review/README.md:7–9의 외부 archive→git apply 재현 절차는 상위 workspace Git discovery를 격리하지 않았다. 실제 이 workspace tmp 안에서 git apply가15개 patch를 모두 Skipped하고 exit0으로 끝났으며495중15파일 mismatch를 관측했다([원본](source-reconstruction-check.json)). source patch 결함이 아니라 외부 harness Git root 문제다. 새로운 외부 export copy에 git init -q를 먼저 실행하면 동일patch가 적용되고495개 전부 hash차이0다([성공 원본](source-reconstruction-check-isolated.json), [재구성 script](reconstruct-source.py)). 구현자가 문서에 local Git init 및 probe 추가 전 전수 LF hash 비교를 추가했고 reviewer가 직접 확인해 해결했다. 제품 source 변경은 없다.

