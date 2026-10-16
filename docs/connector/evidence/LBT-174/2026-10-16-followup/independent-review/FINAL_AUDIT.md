# LBT-174 후속 독립 검토

2026-10-16 KST. 제품 구현을 수행하지 않은 reviewer가 사용자 `PRE_MERGE_AUDIT.md` A–F에 따라 검토했다. source와 기존 Evidence, cloud, 원격 Git은 변경하지 않았다. 저장소 밖 fixture/report를 작성했고, 승인된 임시 로컬 PostgreSQL 테스트 DB와 실제 loopback SSH/WSS만 직접 사용했다. 실제 OpenStack은 작성자가 실행한 원본을 독립적으로 읽었다.

**판정: 제안한 코드·로컬 검증 범위는 PR 가능. 검토한 범위에서 미해결 confirmed 제품 결함 없음.** 전체27 Go diff·적용 SSOT·실행 source·실환경 원본·최종 Evidence 패키지의 독립 검토를 마쳤다. 원격 CI AC와 Jira 전체 완료, 실제 SaaS/Browser C3·KT 후속 범위의 완료를 뜻하지 않는다. push/PR 등 원격 작업의 승인이 아니다.

## A. 버전과 전체 변경

- Repo `tmp/labbit-connector-gates-20261015`; branch `codex/feat/LBT-174-preview-local-integration`.
- base/HEAD/origin-main `0051d8d6803fd447f7b690dd895a0a928c3c7302`. 변경은 미커밋이며 push/PR을 수행하지 않았다.
- 최종 a7d manifest의 Go/module **502/502파일과 독립 계산 해시 차이0**, aggregate `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`. [전수 대조](finalized-source-alignment.json).
- 전체 feature의 Go 변경 **27파일 전부**를 검토했다. 기존15파일은 이전956c source와 동일하고, 후속12파일은 아래 표다. 변경 파일 전체의 정확한 이름은 위 JSON `whole_feature_go_changes`에 남겼다.
- 후속 non-test suffix3개는 `internal/server/preview/previewtest` 지원 코드다. 제품 app가 import하는 Connector 생산 코드 변경은 이전956c 대비0이다. dependency/runtime/HTTP·Connector·Realtime계약/DB migration/CI 설정도 HEAD 대비 차이0을 별도 대조했다.
- 이전 main4691→0051의 LBT176 Backend worker7파일 영향 분석과 기존15파일의 독립 리뷰/원본 실패는 이전 검토 이력이다. 이번에는 새 base의 최종 전체 diff와 호출 consumer, source/Evidence 적용성을 다시 확인했다.

| 후속 파일 | 검토 목적·영향 경계 |
| --- | --- |
| server/realtime/trace_propagation_test.go | ACK보다 늦은 trace log snapshot 대기; nonempty malformed JSON 실패 유지 |
| server/realtime/trace_log_test.go | 첫 빈 snapshot 후 지연 이벤트, malformed/blank/무관 이벤트 분기 |
| connector/vmaccess/tcp_test.go | 정상 SSH 준비와 direct-tcpip caller deadline 분리; 외부2초 oracle·late cleanup·sibling 유지 |
| connector/provider/openstack/integration_test.go | 직접 생성한 SG/keypair만 DELETE1회 + exact GET404 poll |
| connector/provider/openstack/integration_cleanup_test.go | 지연404와401/403/500/취소를 구별 |
| connector/provider/openstack/workspace_file_integration_test.go | Linux opt-in4경로, required PASS gate, bounded exec Start/Wait 수명 |
| connector/provider/openstack/workspace_file_integration_gate_test.go | zero-match/wrongbinary/누락/skip/접두 이름 거부 |
| connector/provider/openstack/workspace_file_fixture_test.go | 실제 loopbackSSH: held exec와 sibling admission·stdout/stderr·취소/Close·transport 보존 |
| server/preview/previewtest/peer.go | test-only TCP refusal 분류 호출 변경 |
| server/preview/previewtest/refused_windows.go | typed WSAECONNREFUSED 및 기존 portable errno |
| server/preview/previewtest/refused_other.go | 비Windows errno 유지 |
| server/preview/previewtest/refused_test.go | 실제 닫힌 포트 wrapped refusal·timeout/cancel/permission 오분류 차단 |

모든 표 경로는 `internal/` 아래다. 기존15파일은 App2+관련회귀1, Preview manager/session/client3+회귀2, Handler2+회귀3, 실제 Preview fixture/export2다. 단순 변경 줄만 보지 않고 Supervisor·VMAccess·Terminal/File·Backend Preview consumer·contract peer까지 읽었다. 변경되지 않은 제품 전체의 신규 전수 감사는 아니다.

## B. 최신 요구와 추적성

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174)의 updated14:59:23.423+0900/댓글0/진행중 및 본문 전체를 직접 읽었다. D18/결정완료/테스트전략/D20/추가결정5개도 직접 읽어 이전 원문과 전체 문자열 동일성을 확인했다. [원문 갱신 기록](ssot-refresh.json). 현재 connector 응답이 Confluence version번호를 반환하지 않은 제한은 남긴다. 이전 읽기의 v5/v11/v3/v7/v3를 현재 새로 확인한 번호라고 주장하지 않는다.

owning Git `contracts/connector/README.md` §7b와 preview-control/data schema가 wire 원본이다. HTTP Preview의 오류/인가, Realtime trace, runtime Preview/VMAccess/JSONL, migrations·TESTING의 실제 PG 조건과 CI tagged harness 범위를 대조했다. 이 작업에서 Backend의 allowlist·Browser auth·TTL·durable operation ownership을 Connector로 옮기지 않는다.

| 요구·AC | 구현·자동 회귀 | 실제 실행·tested source | 공백·판정 |
| --- | --- | --- | --- |
| Jira목표/AC1: 기존CC03와 실제 VM SSH HTTP | 기존 App BuildConnector + PreviewManager/Handler/SSHForwarder 재사용 | strict MockGateway→실App Control/WSS→SSH→실VM HTTP200 twice; 3e1 Preview fixture216.54s | 로컬 component 관측; 실제 SaaS/Browser 아님 |
| AC2:1WSS=1TCP,active≤1,sequential | 동기 PrepareOpen reservation, immutable attempt/session pair, duplicate/conflict 회귀 | actual sequential HTTP·동시 두session 격리; loopback stale ACK/duplicate probes | 실행한 동시성 경계 충족; 장기/multiVM 부하 아님 |
| correlation·동일Control credential·stale/late 격리 | exact target/gen/M1, owner/epoch/capturedsender/credential, stale callback guard | strict attach tuple, independent R006–R008/normalEOF 재현; actual stale generation 거부 | 실제 SaaS revoke/rotation 인가 미실행 |
| AC3:종료·CLOSE·Reset/Cleanup·shutdown | exact CloseAttempt, manager registry detach/fence, App outcome16분기 invalidation | actual Control Reset g1→2·old WSS/registry 종료·g2HTTP·Cleanup; Close/cancel/backpressure unit/race | ephemeral Preview 재시작 복원 약속 없음 |
| 오류·비밀/원문 비노출 | 안전한 Handler mapping, 기존 Workspace/hostpin/port22검증, bounded JSON/opaque Binary | actual closedport/22/잘못된target, realPG502/504; Control 내 HTTP/PTY/합성credential 부재 assertion | 모든 Provider 장애/운영 TLS 검증 아님 |
| AC4:회귀·Terminal/File 비회귀 | 기존 및 추가 lifecycle/owner/Upgrade/callerdeadline/exec tests | 최종3753PASS/42pkg/18skip,11pkg race; actual PTYCC02/C2/VMAccess/FileData/Workspace | CI의 tagged harness-race·원격CI 미실행, Jira전체 완료 유보 |
| 불안정성 원인 수정 | Realtime blank snapshot parser, TCP healthy setup와caller budget 분리, typed Windowspeer | 지연 beforeFAIL→afterPASS, deadline mutant 검출, 실제 PG before502→504 FAIL→after5PASS | 최초 VM timeout 당시 정확한 scheduling trace 없음 |
| 로컬 가능한 미검증 경로 | SG/keypair exactGET, Linux exact PASS, realPG/harness | 임시PG16 5pkg1305PASS, Linux4경로/WorkspaceFS/C1실PGnon-race, actual helper404 | WindowsACL·KT·실Browser·특수taggedrace 미검증 |
| AC5:Evidence/reproduce/KnownLimit | source/exec identity, failure history·cleanup·재현 runner | 원본과 요약 재집계, PG compiled238file bridge0diff | 독립 patch502복원·243raw·17event 요약·재현절차 대조PASS |

## C. 실패·보안·수명주기

authority→manager→session→tunnel 순서의 짧은 상태 갱신과 lock 밖 socket IO/callback을 다시 확인했다. pending registry reservation은 네트워크 전에 끝나므로 CLOSE/Reset/Shutdown이 늦은 dial을 퇴역할 수 있다. 정확한 session pointer·attempt ID를 확인하여 old callback/ACK가 새 tunnel을 닫거나 바인딩하지 않는다. logical session의 normal1000/ordinary EOF 보존과 direct Control 교체/4001·4002 trust 종료를 구분한다. permanent Shutdown은 후속 admission을 막는다.

owner/epoch 검사를 authority lock 안에서 재확인하고 registry snapshot을 먼저 분리한다. captured sender/credential과 bounded WithoutCancel2초 결과 통지가 Data tunnel cancel을 shared Control write로 전파하지 않는다. Supervisor는 reconnect마다 새 Client(`reconnect.go:156`)를 만들므로 captured pointer가 같은 객체의 새 connection을 빌린다는 의심은 확인되지 않았다. worker64개는 내부 admission bound이며 전체프로세스 goroutine0/무한 peer의 즉시 전역회수를 보장한다는 표현은 피한다.

Workspace/server/generation/management address/hostpin은 merged VMAccess/SSHForwarder가 검증한다. 승인된 정확한 port만 쓰며 Connector가 Backend allowlist를 재정의하지 않는다. JSON은1MiB 제한, stream은32KiB chunk로 HTTP를 해석하지 않고 Binary 전달한다. Close는 blocked data writer를 기다리는 mutex를 잡지 않고 bounded WriteControl 후 socket을 닫는다. untrusted close text/Provider raw error를 Control로 반사하지 않는다.

후속 helper는 기존 공유 SG/keypair에는 cleanup을 등록하지 않는다. 직접 생성한 exact ID/name만 DELETE1회하고 GET404까지 확인한다. GET401/403/500·전송오류·취소는 부재가 아니며 실패한다. blind retry나 자원 이름 검색 삭제를 추가하지 않았다.

SSH fixture Start만 Setup에 남기고 전체 명령 Wait를 별도1분/caller취소로 제한했다. stdout/stderr는 Wait 완료 뒤만 읽는다. timeout/취소 반환은 managed session Close를 예약하므로 다른 transport sibling을 취소하지 않는다. protocol-violating SSH peer의 모든 worker가 즉시 종료된다는 의미는 아니다. 실제 loopback latch와 race로 admission·출력·CHANNEL_CLOSE·sibling을 확인했다.

typed Windows refusal은 Backend test-support에 국한되며 제품 TCP timeout이나 SSH error 정책을 바꾸지 않는다. Linux test filter는 네 정확한 PASS 이름/skip/no-tests를 검사한다. KT ledger 회귀는 파일 I/O만 사용하고 KT API를 호출하지 않는다. external C1 overlay는 VM loopback reverseforward를 통해 임시 host loopback PostgreSQL에 연결하며 실제 Provider는 Mock이다.

Preview는 process ephemeral 상태다. durable Backend worker 재시작·DB recovery를 이 feature가 새로 해결했다고 주장하지 않는다. DB physical schema/dependency/runtime는 변경 없으며 실제 PG 통합이 관련 기존 consumer를 보완한다.

## D. 직접 실행과 실제 실행 원본 대조

Reviewer 직접 실행은 cloud를 호출하지 않았다. 아래 결과는 테스트 이름 존재와 실제 process exit를 구별한다.

| 독립 실행 | 결과·근거 |
| --- | --- |
| Realtime ACK→trace75ms지연 | 원래3/3FAIL→blank/malformed/trace 각10PASS. before/after realtime 로그 |
| 건강한 SSH handshake75ms지연 | 원래3/3FAIL230행→수정10PASS; 기존 configured handshake timeout/cancel10PASS |
| caller ctx무시 단일 mutant | 첫 수정50.08초 falsePASS→외부2초 oracle3/3FAIL, 정상10PASS |
| helper GETabsence/gate negatives | Fake closure401/403/500/지연404/cancel 및 wrongbinary/zero-match/skip 회귀 각race10PASS |
| Windows actual closedport | 최소probe3회 모두0ms/Timeoutfalse/errno10061; 원래 실PG test3FAIL→수정5PASS; typed helper race20PASS |
| held exec와 sibling admission/cancel | 기존CombinedOutput wrapper before3FAIL+output race관측→새helper 두회귀race10PASS |
| repository 채택최종 T005 회귀 | a7d소스502개전후불변, `TestWorkspaceFixtureExec* -race -count=10` 모두PASS. [원본](finalized-workspace-exec-race.log), [result](finalized-workspace-exec-result.json) |
| 최종 source/PGbridge | 502/502 hash0diff, 독립 go list compiled238repo Go/module파일67cd→a7d0diff. [bridge](postgres-bridge-independent-check.json) |

T005 before는 새 Output return wrapper에 원래 CombinedOutput-in-Setup 동작만 복원한 overlay다. 실제 정상 컴파일 뒤 admission failure를 관측했다. peer 초안의 stdin EOF 오해와 adapter import 오류는 v1/v2별도 기록이며 제품실패·최종PASS 근거가 아니다. [후속 상세](FOLLOWUP_ADDENDUM.md), [probe 실행 결과](probe-results.json), [T004](t004-results.json), [T005](t005-results.json).

작성자 원본은 `execution-observations.json`에 실제 메타데이터 파일 SHA·exit·source guard 및 JSONL test event를 독립 수집했다. 소스가 실행 중 바뀐 원본은 false를 유지한다.

- 최종 a7d fullGo:3753 test PASS events(하위test포함)/42 testpackages/18test skip/0fail, command exit0·전후source동일. vet/build 각각exit0이며 빈 stdout만으로 성공을 추정하지 않았다. race11packages exit0/166.797초·source동일.
- 일반18skip은 local cloud opt-in12+KT2 및 Windows 파일시스템/권한4다. 다른 opt-in 실행에서 PASS한 항목도 기본suite skip기록을0으로 바꾸지 않는다.
- Realtime/TCP100회씩4tests400PASS는 중간3329 source다. 관련 파일은 최종과 동일하고, 최종 전체/race에 다시 포함됐다. 웹4checks/188tests는8e9이고 이후 web/lock/config 변경0이다.
- 실제PG 첫실행1304PASS/1FAIL/선택wire1skip, 수정후67cd 5pkg1305PASS/선택wire1skip/0FAIL/실제goexit0. 원본 sourceguardfalse는 Workspace fixture2파일 변경 때문이며, compiled238Go/module 입력은 a7d와0diff이다. 최종source로전체PG를다시실행했다고 주장하지 않는다. 선택 wire sample은 a7d에서 별도PASS/skip0. 임시DB0 및 PostgreSQL정상종료 원문을 읽었다.
- broad local12top-level batch는3e1에서 **9PASS/3FAIL**/goexit1/sourceguardfalse다. fail event4는 InternetON child+parent를 포함한다. File/Workspace2개는 Linux helperenv 미준비로 즉시 실패했다. 이후 File8e9 PASS, Workspace5ea PASS 및 원래 Internet fixture a7d PASS가 각 공백을 보완한다. 첫 batch 전체PASS로 바꾸지 않는다.
- broadbatch에서 M1/M2/M3/ControlWSS/PreviewForwarding/PTYCC02/TerminalC2/VMAccess/PreviewTransport actualPASS를 읽었다. Preview HTTP/sequential/isolation/generation/cleanup216.54s·Lab entries28 exact부재(중복포함) 및 보조SG/keypair GET404를 확인했다. unique28자원이나 전체cloud 사용자자원0으로 확대하지 않는다.
- Linux Workspace/OS/C1은5ea sourceguardtrue/commandexit0/132.90초test다. WorkspaceFSsuite·네OS경로·12C1 parent 정확PASS,7tracked+보조SG/keypair404를 읽었다. C1은 MockProvider+realPG/non-race이며 crossLinuxvet/serverbuild/testbinarybuildPASS다. a7d는 새 T005 unit회귀 하나만 추가돼 실행한 제품/helper/선택test함수는같다. 같은최종binary를실행했다고 주장하지 않는다.
- Internet진단ON3targetconnect0/OFF비연결과 원래assertPASS를 읽었다. 이어 warm-up진단없이 원래a7d fixture ON/OFF가 commandexit0/238.547초/source동일로PASS했다. 최초SSH/exec실패의원인은확정하지않는다.

## E. 수정 전 findings와 해결

제품 R001–R009 및 DOC001의 수정 전 위치/재현/영향은 이전 독립 검토 원문에 보존되어 있다. 관련 코드/회귀가 이번 source에서도 유지됨을 다시 확인했다. 후속 finding은 모두 수정 전 parent에게 전달됐거나 parent가 사용자에게 먼저 보고했고 구현자는 root다.

| ID·심각도 | 수정 전 위치·재현·영향 | 해결·회귀 |
| --- | --- | --- |
| T001 Low | 956c realtime trace test265–268:빈snapshot을JSON으로읽고eventually전에Fatal | blank만대기,nonempty malformed거절;지연beforeFAIL/afterPASS |
| T002 Low | 956c tcp test229–230:40ms가정상SSH준비까지제한 | healthysetup분리/callerdeadline만stalledTCP;지연beforeFAIL/afterPASS |
| T003 Low | T002첫수정tcp236–241/252–258:기본10초에떨어져도error종류만같아PASS | 2초외부oracle,같은mutant3FAIL·정상PASS |
| V001 Low | integration helper472–478/564–570:DELETEACK만으로실제부재미관측 | exactGET404·오류/취소회귀race10PASS·actual404 |
| V002 Low | 첫Linuxfixture218:wrongbinary/filter도PASS exit0 | exactrequiredPASS/zero-match·skip거절·race10PASS·실Linux4경로 |
| T004 Low | base0051 peer391–396:Windows10061를portableerrno로인식못해502→504 | typedplatformhelper·race20PASS·실PGbefore3FAIL/after5PASS |
| T005 Low | 이전Workspacehelper337–342:전체exec가Setup admission점유·취소outputrace | Start/Wait분리·출력수명·latchbeforeFAIL/afterracePASS·실VMWorkspacePASS |

전체27Go diff와 영향을 받는 경계에서 열린 confirmed 제품 결함은 없다. 최초 VM/Internet 실환경 실패의 정확한 원인, 운영환경/실제SaaS/CI 미검증을 해결된finding으로 포장하지 않는다.

## F. 독립 검토 결과와 판정 경계

코드·자동회귀·실환경 관측의 적용성은 위 범위에서 확인했다. 원격CI와Linux tagged C1-race, KT 최종실환경, 실제SaaS/Browser인가 C3, 운영TLS/LB, multiVM규모/장기soak, hardcrash/durable복구는미검증이다. 초기Internet실패원인은미확정이다.

최종 Evidence의 base archive→독립 git init→patch 적용→502/502 LF hash/aggregate 재구성이 통과했다. 27개 patch 경로와 inventory가 일치하며 243개 artifact의 gzip 복원 raw bytes/SHA가 모두 맞다. 17개 JSONL 요약을 원본에서 재집계했고, 5개 cloud 실행의17 top-level 결과·87 cleanup marker가 원본과 일치한다. 6개 stage bridge와 재구성5ea를 독립 계산했다. Markdown local link누락0, Evidence 아래 Go파일/Go package검색0,272텍스트파일 heuristic secret후보0이다. 보호입력 원문은 reviewer가 읽지 않았으며 작성자의 exact-input 비교와 구분한다. [독립 수치](packaging-independent-check.json), [패키징 상세](PACKAGING_ADDENDUM.md).

작성자 F174-DOC002(재현용 `.go` artifact의 Go package 오염)는 `.go.txt`로 고쳤고 raw bytes는 유지했다. reviewer도 수정후 docs package0을 확인했으며, post-package 전체Go3753PASS/42pkg/18skip/0FAIL 및 vet/build exit0·source불변을 읽었다. 이 후속 실행은 원래 a7d 전체 실행을 덮어쓰지 않는다.

독립성 한계: 제품 구현과 production 수정은 root가 수행했다. reviewer가 외부 T005 latch fixture를 작성했고 root가 이름·시간 여유만 바꿔 회귀로 채택했다. 따라서 해당 테스트의 설계 자체를 서로 무관한 두 설계로 주장하지 않는다. reviewer는 채택된 실제 파일과 최종source에서 race10회 실행을 별도로 확인했다.

제안한 코드·로컬 검증 범위는 PR 가능으로 판정한다. 원격 CI·Linux tagged-race·실제 SaaS/Browser C3 등 미검증 범위와 최초Internet 실패 원인미확정을 명시해야 한다. Jira 전체 완료나 rollout승인이 아니다. 감사 통과는 push/PR/merge/배포/외부메시지 승인이 아니다.
