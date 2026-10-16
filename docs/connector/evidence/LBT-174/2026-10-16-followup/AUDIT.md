# LBT-174 후속 pre-merge audit — 2026-10-16

최종 판정: 제안된 코드·로컬 검증 범위의 audit 통과. 아래 제한을 명시한 PR 제안이 가능한 범위이며, 전체 LBT-174 완료나 원격 CI 충족을 선언하지 않는다. 요청은 Realtime/VMAccess 테스트 불안정성의 원인 수정과 로컬 가능 미검증 검증이다. commit/push/PR/merge/배포/외부 메시지는 수행하지 않는다. 과거 [최초 감사](../2026-10-16/AUDIT.md)는 당시 source956c의 역사로 보존하며, 아래 기록이 후속 결과와 제한을 갱신한다.

## A. 버전·전체 diff

- 저장소 `labbit-app`, branch `codex/feat/LBT-174-preview-local-integration`.
- base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`, 모두 미커밋 로컬 변경. 원격 main read-only 최종 확인(2026-10-16) 결과도 동일하다. 브랜치를 push하지 않았다.
- 최종 Go/module502파일 LF 정규 manifest aggregate `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51` ([manifest](source-manifest.json)). 전체27 Go 변경(기존15+후속12)은 [inventory](code-inventory.json), 미추적 새 파일까지 포함한 [patch](full-code-diff.patch)로 식별한다. 원래15파일은956c와 동일하고 독립 reviewer가 전체27파일·호출자·consumer를 다시 검토했다. stage 간 변경 목록과 제한은 [source bridges](stage-source-bridges.json)를 따른다.
- 기존15파일의 목적·경계는 최초 감사 A의 각 행을 그대로 적용한다: App lifecycle/Reset/Cleanup2개+회귀1개, Preview manager/session/client3개+회귀2개, WSS handler/Preview handler2개+회귀3개, 실제 Preview fixture/export2개. 후속은 제품 경로 수정 없이 테스트·test-support 보강이다. 파일 suffix가 `_test.go`가 아닌 새3파일도 `internal/server/preview/previewtest` 지원 package이며 production app에서 사용하지 않는다.
- SSOT/Runtime/dependency/migration/CI/Web diff0. ignored local SSH key 및 보호된 외부 cloud config는 실행 입력이며 Evidence/Git에 복사하지 않는다. Linux ELF·PostgreSQL zip/data 역시 commit 대상이 아니다.

| 후속 파일 | 목적·영향 경계 |
| --- | --- |
| `internal/server/realtime/trace_propagation_test.go` | 빈 snapshot과 정상 JSON 이벤트를 구분, ACK/log 비동기 순서 polling |
| `internal/server/realtime/trace_log_test.go` | 첫 빈 snapshot 이후 지연 로그, 비어 있지 않은 잘못된 JSON 거절 |
| `internal/connector/vmaccess/tcp_test.go` | 정상 SSH 준비와 의도적 TCP deadline 분리, 2초 외부 제한으로 caller deadline 무시 mutant 검출 |
| `internal/connector/provider/openstack/integration_test.go` | helper가 생성한 SG/keypair만 DELETE 후 exact GET404까지 부재 확인 |
| `internal/connector/provider/openstack/integration_cleanup_test.go` | 지연404, 401/403/500/취소를 부재와 구별 |
| `internal/connector/provider/openstack/workspace_file_integration_test.go` | opt-in Linux4경로 실행·정확한 PASS gate; SSH Start/Wait 분리 및 bounded fixture exec |
| `internal/connector/provider/openstack/workspace_file_integration_gate_test.go` | zero-match/잘못된 binary/부분누락/skip/접두 이름 혼동 거절 |
| `internal/connector/provider/openstack/workspace_file_fixture_test.go` | 실제 loopback SSH, held exec 중 sibling admission, stdout/stderr 완료, 취소→정확한 channel CLOSE·sibling 보존 |
| `internal/server/preview/previewtest/peer.go` | test-only peer의 TCP refused typed 분류 helper 사용 |
| `internal/server/preview/previewtest/refused_windows.go` | Winsock WSAECONNREFUSED와 portable errno 인식 |
| `internal/server/preview/previewtest/refused_other.go` | 비Windows typed ECONNREFUSED 유지 |
| `internal/server/preview/previewtest/refused_test.go` | wrapped 실제 닫힌 loopback 거절, timeout/cancel/permission 오분류 방지 |

이 폴더의 문서·원본/압축 로그·runner·overlay·source identity·재현/복원도 검토 대상이다. 원본은 변경하지 않고 stage별 보존하며, summary를 raw JSONL에서 다시 계산한다. 이 작업은 변경되지 않은 전체 제품을 새로 감사한 것이 아니다.

## B. 최신 원문·요구→구현→검증 추적

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) 최신 본문/AC/댓글을 직접 조회: updated `2026-10-16T14:59:23.423+0900`, 댓글0, 진행중, AC변경 없음. parent LBT-24, KT 최종은 OP-03 후속, SaaS/Browser C3는 LBT-25다. 독립 reviewer도 Jira와 D18/결정완료/테스트전략/D20/추가결정 원문5개를 다시 읽고 이전 원문과 전체 문자열 동일성을 확인했다([SSOT refresh](independent-review/ssot-refresh.json)). 커넥터 응답에서 최신 version이 노출되지 않은 경우 본문 동일성 근거로 기록하며 버전을 추정하지 않는다. 이전 직접 확인 버전은 D18v5/결정완료v11/전략v3/D20v7/추가v3였다.

Repo AGENTS/CONTRIBUTING/TESTING, `contracts/connector/README.md` §7b와 Preview schemas, Realtime contracts, HTTP Preview 오류/인가, runtime Preview/VMAccess, backend 책임, DB migrations와 tagged CI 범위를 대조했다. 계약 변경 없음. DB 검증은 migrations를 실제 PG16에 적용한다. HTTP/DB/Browser 인가를 Connector가 재구현하지 않는다.

| 요구·AC/원문 | 구현·자동 테스트 | 실제 검증/소스·Evidence | 공백·판정 |
| --- | --- | --- | --- |
| 기존 CC03 + 실제 SSH forwarding, Jira AC1/D18/Connector§7b | `app/app.go`, Preview manager/client + SSHForwarder; 기존/추가 lifecycle·target guard | 실제 App/Control/SSH/VM HTTP fixture; first3e1 원본 및 source bridge | Mock Gateway/실VM 조건. 실제 SaaS/Browser 아님 |
| full correlation·1WSS=1TCP·active≤1·sequential, §7b schemas | immutable attempt/CloseAttempt, client ATTACH identity, Handler owner + stale/duplicate/격리 회귀 | strict Mock Gateway와 VM HTTP sequential/두 session; loopback stale fault injection | 대규모 multiVM 부하 미실행 |
| CLOSE/upstream/Data 종료·Reset/Cleanup generation, Jira AC2–3 | session close/manager pending fence/App invalidation; mutation16분기·normalEOF/replacement | 실제 gen1→2와 old 거절·새 HTTP·Cleanup, manager/WSS race | hard crash durable복구는 이 ephemeral Preview 범위 아님 |
| APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE/UNAVAILABLE 안전 매핑 | SSHForwarder/Handler + 오류/비밀 비노출 회귀; test peer Winsock 수정 | 실제 VM closedport/port22/target mismatch + 실제 PG HTTP502/504분류 | Provider 장애 전체 주입은 아님 |
| cancel/backpressure/shutdown·공유 채널 보존 | bounded Close/Upgrade cancellation/64workers/owner retirement; 새 deadline mutant 및 fixture exec 회귀 | timing100회씩400PASS, 독립 지연·mutant·race, 실제 PTY/SFTP·File Data 취소/회수 | 전역 goroutine0·장기 soak 보장 아님 |
| Terminal/File 회귀 없음, D18 sharedSSH | 기존 Terminal/File 및 추가fixture tests | local PTYCC02/TerminalC2/VMAccess/FileTransport/Workspace 실제 VM; Linux WorkspaceFS | C2의 Browser 역할은 Go fixture, 실제 브라우저 UI 아님 |
| 올바른 Realtime trace/실패 검출, TESTING concurrency | 빈snapshot대기·nonempty malformed거절; 정상setup과timeout대상분리 | before 지연조건FAIL→after10PASS, caller-deadline 무시 mutant50초 falsePASS→2초 FAIL | 초기 VMAccess 실패 당시 정확한 wire 지연은 수집되지 않음 |
| 실제 FS 보안·권한/falsePASS금지 | exact required PASS + skip/zero-match reject | Linux VM의 ledger symlink/rename, WorkspaceFile symlink, credential0600 네 경로 모두PASS | Windows ACL/개발자 모드 권한을 검증한 것은 아님 |
| DB/CI 관련 로컬 가능 검증 | PG repository/application/auth/httpapi5pkg; test-only C1 harness | PG16 real1305PASS + optional wire dump 별도PASS, Linux C1 12parent non-racePASS, Linuxvet/buildPASS | Linux tagged harness -race 및 원격 CI 미실행 |
| Evidence/재현·cleanup, TESTING/Jira AC5 | exacttrackedID cleanup + SG/keypair GET404, inventory/source/hash | 각 실행 metadata·raw·stagebridge·독립 검토, PG temporaryDB0/서버종료 | cloud 전체 사용자 자원0 주장은 아님 |

## C. 실패·보안·수명주기

기존 생산 구현15파일의 입력/상태/epoch/attempt/lock/cancel/resource/secret 분석은 최초 감사 C와 동일하며 다시 검토했다. 후속 변경은 다음 경계를 보강한다.

- Realtime ACK는 로그 저장보다 먼저 관측될 수 있다. 빈 log snapshot만 건너뛰며, 비어 있지 않은 잘못된 JSON은 계속 실패시킨다. mutex를 쓰는 syncBuffer/slog가 완성된 이벤트 단위로 쓰는 경계이며 일반적인 임의 partial-tail 복구를 구현한 것이 아니다.
- TCP timeout은 건강한 SSH/PTY 준비에 적용하지 않는다. 의도적으로 응답을 보류한 direct-tcpip에만 caller deadline을 적용하며, 실제 호출이 기본10초 timeout으로 떨어지면 독립2초 제한이 실패시킨다. late connection/onepending/sibling/새 TCP/transport 정리 assertion을 유지한다.
- SG/keypair cleanup은 재사용한 공용자원에 등록하지 않는다. 직접 만든 exact ID/name만 DELETE1회 후 GET404 확인한다. GET401/403/500/전송오류/취소는 부재로 인정하지 않고 실패한다. mutation blind retry를 추가하지 않는다.
- SSH fixture는 Start만 Setup 슬롯에 두고 Wait는 독립1분 또는 parent취소로 제한한다. stdout/stderr는 별도 buffer이며 Wait 완료 후에만 읽는다. 취소/실패 반환에서 출력 buffer를 읽지 않아 copyworker와 race하지 않는다. 종료 시 해당 session.Close, 다른 PTY/transport 보존. setup슬롯 해제/peer CHANNEL_CLOSE/출력완료를 latch 회귀와 race로 관측한다.
- typed Winsock 분류는 test-only contractpeer에만 적용한다. 실제 생산 SSH refusal은 기존 vmaccess 분류와 별도이며 제품 timeout을 늘리지 않았다. timeout/취소/permission을 APP_NOT_RUNNING으로 바꾸지 않는다.
- Linux VM에 올린 binary는 이 source에서 Linux/amd64 CGO0으로 빌드했다. 고정 testfilter/expectedname과 build identity를 확인하며 zero-match/skip은 통과하지 않는다. KT ledger 테스트는 로컬 파일 I/O만 수행하고 KT API/실인증정보를 사용하지 않는다.
- 외부 harness overlay는 source helper를 재사용하고 추가C1만 실행한다. remote127.0.0.1 임시port→SSH reverseforward→host127.0.0.1:15432의 임시PG다. DB를 공용interface에 열지 않으며 hostpin을 확인한다. VM/SSH/DB cleanup과 실제 KT mutation을 혼동하지 않는다.
- Internet 초기ON 1회실패는 보존한다. 진단ON/OFF는PASS였으나 첫 실패의 정확한 원인은 수집된 safe error만으로 확정할 수 없다. 재실행PASS만으로 이 실패의 원인까지 해결했다고 주장하지 않는다.

## D. 실제 실행·Mock·skip·미실행 및 소스 연결

최종 source는a7d(502파일), 중간은first3e1(497), intermediate3329(497), verified8e9(498), sealed67cd(501), closing live/build5ea(501)다. closing의 나중 unit용 manifest는c3a(502)이며 a7d로 budget이 바뀌어 guard false인 단위 실행을 담는다. 5ea live/build identity는 sealed manifest에 최종 Workspace helper 한 파일만 대입해 당시 command metadata의 aggregate와 정확히 일치함을 재구성했고, 원시 manifest로 가장하지 않는다([재구성](closing-live-source-reconstruction.json)). full hash/전후 identity는 각 metadata/manifest를 따른다. 실행 중 발견한 테스트 문제를 고쳐 runner의 전체source guard가 false가 된 원본도 보존하며, tool wrapper exit와 실제 go command exit를 구분한다.

| 실행 | 결과·tested source·범위 |
| --- | --- |
| 최종 fullGo/vet/build/fmt | a7d fullGo3753PASS events/42packages/18skip/0FAIL; vet/build/fmt 각각PASS; command전후동일. DOC002 artifact 수정 후에도 post-package fullGo3753/42packages/18skip/0FAIL(36.640초), vet/build 재실행 exit0·source 전후동일 |
| 최종 race | a7d 11package(Realtime/previewtest/Connector/KT tooling unit), 11/11 package PASS,166.797초,source 전후동일. 실제 KT API/cloud opt-in 없음 |
| Realtime/TCP100회 | T003후3329 4test400PASS/0skip, 관련파일은a7d와동일. 독립75ms 지연 각10PASS, malformedJSON거절, callerctx무시mutant 최종3/3FAIL은 의도한 회귀검출 |
| 웹4종 | verified8e9 typecheck/lint/test/build exit0,14files/188tests. 이후 Go test지원/fixture만 변경, web/lock/config diff0 |
| 실제 PG16 | sealed67cd 5pkg commandexit0/1305PASS/1optionalDump skip. 이후차이2개는OSfixture/회귀뿐. `finalized/postgres-source-bridge.json`의 실제Go compiled dependency238repo파일+module inputs 차이0. wrapper sourceguard false를 숨기지 않고 최종소스 재실행이라고 표시하지 않음 |
| 선택 wire sample | a7d `TestDumpPreviewWireSamples`를 지정임시출력dir로 별도실행 PASS/skip0. 합성 fixture JSON만 로컬생성 |
| Linux Workspace/OS/C1 | closing5ea external overlaycommandexit0/원본Workspace1PASS/skip0; LinuxWorkspaceFS 및4OS경로,12C1 parent 정확PASS 확인. C1 MockProvider+realPG, non-race. a7d차이는 새loopback회귀파일 하나뿐이며 제품/helper 동일 |
| Linux harnessvet/build | closing5ea GOOSlinux vet, taggedserver build, testbinary buildPASS. Windows는 syscall.Stat_t 미정의로3명령compileFAIL(원본보존). CI의Linux tagged-race는 미검증 |
| 실제 localOpenStack 기본확장 | first3e1 12top-level 중9PASS/3FAIL,skip0. FAIL event4개는 InternetON child와 parent를 포함한다. 실제 go exit1/sourceguard false를 그대로 보존한다. File/Workspace helperenv누락2FAIL은 후속preparedbinary실행으로 보완; Internet초기ON실패는 별도제한. 전체batch를PASS로 바꾸지 않음 |
| 실제 File Data | verified8e9 FileTransportPASS, Workspace설치FAIL원본보존. 이후T005후Workspace별도PASS. FileData의Tree/Read/Save·4동시read·timeout/cancel/revoke회수·PTY유지 및exact7+SG/keypair404 |
| 최종 원본 InternetON/OFF | a7d, 진단warm-up 없는 원래fixture 238.547초/ON·OFF·parent3PASS/0skip,source 전후동일. 최초 실패 원인까지 특정한 것은 아님 |
| 원격 CI/KT/C3/장기부하 | 미실행. push/PR안함. 원격CI AC 충족 또는 Jira전체완료로 선언하지 않음 |

일반Go18skip은cloud opt-in14(12local+2KT), Windows4다. local12와Linux4는 개별opt-in Evidence로 별도대조하며 기본suite skip숫자를0으로고치지 않는다. 실제PG의skip1은Windows가 아니라 선택wire export였고 뒤에실행했다. 로컬actual SaaS/Browser E2E로 확대하지 않는다.

초기실패 이력: 최초webtest는실제exit0이나source변경guard로build전runner중단→verified4종재실행PASS; 첫PG는T004로1304PASS1FAIL→수정후1305PASS; firstcloud helper2개미설정으로fastFAIL, verifiedWorkspace설치execFAIL→T005후실VM재실행PASS. T005의근본적fixture결함은latch regression으로확정했지만 원래설치실패의구체적동일원인은알수없다. 독립probe 초안의import/EOF오해실패는v1/v2 artifact로보존하며 최종PASS나제품결함근거로사용하지않는다.

재현 도구: WindowsGo1.27.1/GCCrace/Python3.12/Node/pnpm adapter, 실제PG16.15 portable와 localOpenStack `labbit-test`, ubuntu/m1.small/sharednet1. PG배포원본은 [PostgreSQL Windows 공식안내](https://www.postgresql.org/download/windows/)에서 연결된 [EDB binaries](https://www.enterprisedb.com/download-postgresql-binaries), zipSHA256 `43bb45f173a6f08cf1d29a97a6d8deb119e8e8093a24c00d2d1001a0ccaa8281`. 127.0.0.1:15432 임시테스트DB, labbit_it_* 부재확인후 pg_ctl stop했다. 개인키/clouds.yaml은 별도보호입력이다. [재현절차](REPRODUCE.md)와runner/overlay를따른다.

## E. 수정 전 findings·재검증

모두 사용자에게 수정 전에 보고했다. F174-DOC002는 마지막 Evidence 취합 과정의 추가 문제다. Low는 test/evidence 신뢰도 영향이며 제품권한우회 결함으로 과장하지 않는다. 최초15파일의R001–R009/F174-DOC001은 최초감사E의해결상태를유지한다.

| ID·심각도 | 수정전파일·revision/줄·재현·영향·근거 | 수정·필요회귀·상태 |
| --- | --- | --- |
| F174-T001 Low | 기존956c `realtime/trace_propagation_test.go:265–268`: ACK후log가비었을때 Split→빈문자열JSONEOF→eventually전에Fatal; 기준main에서도재현, TESTING concurrency | 빈snapshot만대기, nonempty잘못된JSON여전히실패; 지연75ms before3/3FAIL→after10PASS, 새deterministic회귀·100반복PASS |
| F174-T002 Low | 기존956c `vmaccess/tcp_test.go:229–230`:40ms SetupTimeout이건강한SSH/PTY까지적용되어의도한stalledTCP전에FAIL; 75ms지연before3/3FAIL | 정상defaultsetup+의도적caller200/10ms분리, sibling/late cleanup유지; 지연후10PASS/100반복PASS. 원래병렬실패당시wiretiming미확정 |
| F174-T003 Low | T002첫수정 `tcp_test.go:236–241,252–258`:DeadlineExceeded만확인하여callerctx무시mutant가10초×5=50.08초후에도PASS | 각대상call2초독립limit; 같은mutant3/3약2.02초FAIL, 정상지연10PASS. regression oracle보완 |
| F174-V001 Low | 기존 `openstack/integration_test.go:472–478,564–570`:SG/keypair DELETEACK만관측,잔여부재Evidence공백 | exactGET404확인, 지연부재/401/403/500/cancel회귀race10PASS,실제cloudGET404marker |
| F174-V002 Low | 첫Linux확장 `workspace_file_integration_test.go:218`:틀린binary/filter조합도no-tests+PASS exit0 | requiredtest이름전부PASS·no-tests/skip거절. wrongfilter실제재현,negative회귀race10PASS,4Linux경로실제확인 |
| F174-T004 Low | 기존 `server/preview/previewtest/peer.go:391–396`:Windows실거절10061≠portableECONNREFUSED536870934,실PGHTTP502기대→504; root/reviewer각3/3FAIL | typedWSAECONNREFUSED분류, wrappedloopback/timeout/cancel/denied회귀race20PASS,실PG원본test5/5PASS및5pkgPASS |
| F174-T005 Low | 기존 `workspace_file_integration_test.go:337–342`:CombinedOutput가command끝까지Setup opening slot점유; 느린설치·회귀가10초budget소진,취소시output읽기race가능. 독립latch·race재현 | Start/Wait분리,1분/parentbounded,Wait완료후별도stdout/stderr읽기; old admission3/3FAIL+race관측→new2test race10PASS; 실제VMWorkspacePASS. 기존실패의동일원인확정으로확대안함 |
| F174-DOC002 Low | 후속 `package_followup.py:37–41`: 재현 소스 `.go`를 docs 아래 그대로 복사하면 `go list ./docs/connector/evidence/LBT-174/...`가 first/independent-review를 package로 인식해 full suite와 source identity를 오염시킨다. Evidence 재현성/전체diff 근거 | `.go.txt` artifact로 보관하고 raw bytes/SHA 및 overlay 재현경로를 유지. 수정 후 docs Go package0, 최종502 source identity 불변 및 fullGo 재실행으로 확인 |

## F. 독립검토·잔여범위·판정

구현하지않은 `/root/review_lbt173`를재사용했고, 원문SSOT·전체27diff·호출consumer·502/502sourceidentity·mutant/delay/realclosedport/fixturelatch를별도사본에서검토·실행했다. reviewer는주작업checkout/cloud를수정하지않았고 PG재현은postgrestest임시DB만썼다. 최종 소스에서 채택한 T005 두 회귀를 reviewer도 -race count10으로 직접 실행해 PASS했고 소스 불변을 확인했다. PG의 실제 compiled dependency238파일+module source bridge도 독립적으로 재생성해 집합·해시 차이0을 확인했다. 작성자와 reviewer가 각각 빈 git init checkout에 full patch를 복원해502파일 aggregate 일치를 확인했다. reviewer는 freeze된243raw의 gzip/bytes/SHA,17event요약,6stagebridge,5실환경로그17top-level/87cleanup marker와 Markdown 링크·Go package 오염0을 직접 확인했다([독립 최종 보고](independent-review/FINAL_AUDIT.md), [패키징 대조](independent-review/PACKAGING_ADDENDUM.md)). reviewer의272텍스트 heuristic후보0과 작성자의 보호입력7값 exact비교0은 별도 근거다. reviewer의 최종보고·checker·검증JSON을 마지막 복사 후 raw provenance와 checksum을 다시 고정했다. reviewer 검증 당시243개와 마지막 전체원본 수는 서로 다른 snapshot이며, 원본 실행결과가 추가로 실행된 것이 아니다.

남은범위: 원격CI, Linux특수harness tagged-race, 실제KT/C3SaaS·Browser/TLS운영, multiVM규모·장기soak·hardcrash는미실행이다. 초기InternetON원인미확정은별도기록한다. 로컬이확인한조건을이범위로확대하지않는다. audit는push/PR등의승인으로해석하지않는다.

독립성 한계: reviewer가 외부 T005 latch regression을 작성했고 작성자가 이름·시간 여유만 바꾸어 채택했다. 두 개의 독립적인 테스트 설계로 주장하지 않는다. 채택된 실제 파일과 최종 소스의 race 반복을 reviewer가 별도로 확인했다.

패키지 최종 복원/원본 대조는 [package-verification](package-verification.json), 보호입력 선별은 [secret-screen](secret-screen.json), 추적 자원 정리 관측은 [cleanup-observations](cleanup-observations.json), 전체 artifact checksum은 [package-checksums](package-checksums.json)에 기록한다. Git stage/commit/push/PR/merge/배포/외부 메시지는 실행하지 않았다.
