# LBT-148 latest-main 통합 독립 검토 — 2026-10-14 KST

## 판정과 범위

최종 판정: **Audit PASS / LBT-148의 승인된 실제 PTY 및 기존 CC-02 중간 통합 범위에서 PR 가능**. base부터 전체 변경과 related consumer를 독립 검토했고 수정 전 findings, 수정된 production source/회귀, 최종 자동 검사 및 실제 VM 원본 로그, source/체크섬/scan/재현 artifact를 대조했다. 발견한 production 및 portable runner findings는 해결되었으며 현재 검토 범위에 미해결 confirmed finding은 없다. reviewer는 구현 코드나 제출 artifact를 수정하거나 테스트/외부 작업을 실행하지 않았다. 이 판정은 Browser/SaaS C2, production SFTP Tree/Read/Save, KT D1, 강제 재시작·장기간 운영 검증의 완료를 포함하지 않으며 원격 push/PR 생성/merge/배포/외부 메시지 승인을 뜻하지 않는다.

- Repository: `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-pty-148`.
- Branch: `feat/LBT-148-pty-cc02`.
- Base: `45dfc486f2dab72ac705310e3f4f3acead1128f0` (최신main의 LBT-147 및 Preview 포함).
- 최초 review HEAD: `f3fa76f89e30ad1f357e915919d189c7832529b5`, clean; rebase 구현 commits `2c00d58`, `b05bda1`.
- 최종 자동 검증 HEAD: `f510f9d9d24ebbde261614fdc9fabb3bc64146c6`, clean, 실행 전후 source unchanged. 실제 VM 검증 HEAD: `efc22587667a53e11dd87d75c025db37512bc221`. 두 commit 사이 diff는 `managed_setup_cancel_test.go` fixture gate snapshot 변경 하나뿐이며 production blobs는 동일하다.
- 이전 HEAD `d4812ae`는 `codex/backup-lbt-148-preaudit-20261014`로 보존됨.
- 사용자 `PRE_MERGE_AUDIT.md`, 현재 repo AGENTS/CONTRIBUTING/TESTING 및 Connector/Realtime/runtime/backend SSOT를 적용했다.
- 외부 Jira/Confluence는 부모가 직접 조회한 원문/버전정보를 전달받았다. LBT-148(updated 2026-10-14 09:19:08 KST), D-18 v5, MVP v19, D-21 v10, 테스트전략 v3. Reviewer가 외부 자료를 별도로 fetch한 것은 아니다.

## A. 전체 diff/consumer 검토

최초 전체 16파일: production `terminal/client.go`, `terminal/pty.go`, `terminal/session.go`, `terminal/ssh_pty.go`, `wss/handler.go`; tests `terminal/data_owner_test.go`, `terminal/data_write_test.go`, `terminal/lifecycle_test.go`, `provider/openstack/pty_cc02_integration_test.go`; historical evidence 7파일 `README.md`, `artifact-checksums.json`, `automated-logs.zip`, `reproduce.ps1`, `secret-scan.json`, `source-manifest.json`, `validation-summary.json`.

추가 scope 4파일도 검토: `mock/preview_gateway.go`/`mock/preview_gateway_test.go`의 fixture concurrent writer fix; `terminal/data_frame_owner_test.go`의 stale-frame/late-error/실패cleanup 회귀; `terminal/managed_setup_cancel_test.go`의 async setup취소/late reply/initialstdout/sibling/Waitworker 회귀. 코드 HEAD 대비 base 변경은20파일이다. 새 `2026-10-14` evidence14파일의 README/감사/독립기록/source/summary/hash/scan/archive/PS/Python runners/residual overlay/attributes도 추가 검토했다.

모든 변경 파일을 읽었다. production 목적은 종료/원격exit와 마지막bytes 보존, stdout+stderr drain, 종료ID resurrection 금지, 단일 Data reconnect owner, pending attach 취소, bounded normal Data writes와 안전한 OPEN error이다. 148 tests/evidence의 목적은 이 분기와 Mock SaaS/Relay→실제 VM 중간 통합을 검증하는 것이다.

관련 consumer/caller로 현재 VMAccess `access.go`와 channel isolation tests, terminal backpressure/final-frame/SSH shutdown tests, Connector app wiring/shutdown, Handler OPEN/CLOSE flow, Preview forwarder/manager/app wiring을 확인했다. 최신main의 resizeMu, admission Shutdown, async channel cleanup 및 final ENDED 1초/lockwait50ms는 보존되었다.

Preview는 현재 Provider TCPForwarder 또는 nonproduction DirectTCP forwarder를 사용하며 `vmaccess.Manager`를 직접 공유하지 않는다. production forwarder 미구현은 capability/manager를 사용 가능하다고 광고하지 않는 기존 경계이며 148이 변경하지 않는다. Preview shutdown과 terminal shutdown은 app에서 함께 보존되어 있다.

HTTP/schema/DB/migration/Web dependency 변경은 없다. SaaS의 resolved-target authority 및 credential authentication 경계를 유지하며 VM byte transport만 변경하므로 이번 변경에 새 DB transaction/migration 검증은 필요하지 않다. repo 전체 미변경파일 전수 검토나 전체 Browser/SaaS C2 검토를 수행한 것은 아니다.

## B. SSOT 추적성

| 요구/결정 | 원문 및 owning SSOT | 구현 | 자동 검증 | 실환경 evidence/공백 |
| --- | --- | --- | --- | --- |
| 실제 VM PTY 생성/input/stdout/resize/종료 | Jira148 AC1; Connector terminal schemas | Handler OPEN, NewManagedSSHPTY, Session pump/input/resize/Close | 최종 f510f9d unit/race/반복 회귀 PASS | 통합 production efc2258 실제 CC-02 -race133.81초 PASS; 최종 commit production 동일 |
| 기존 CC-02 Control/Data WSS 경유 | Jira148 AC2; Connector README§6/§7 | 실제 Handler+Data client, Mock control/data peers, 실제 OpenStack PTY harness | TestOpenStackPTYCC02Integration | 현재 실제 VM+Control/Data harness PASS. Browser/SaaS 전체는 후속 C2 |
| 종료/실패 cleanup+다른 channel 유지 | Jira148 AC3; D18 shared SSH 독립채널 | Session cleanup, VMAccess per-channel worker/release | main isolation/backpressure/shutdown 및 늦은 setup/frame/errorcleanup 회귀 최종 PASS | 실제 A close 뒤 B+SFTP 유지, grace/remoteexit/전체SSH단절 cleanup PASS. shared SSH 전체단절은 모든채널 종료가 맞는 경계 |
| LBT20/C2 참조가능 재현 evidence | Jira148 AC4; TESTING traceability | reproducible harness, source/hash/raw logs | historical 및 최신 raw event/metadata/source/checksum/재현 경계 직접 확인 | 제출 artifact 전체 검토 PASS. 외부 credential/config은 재현자 로컬 입력이며 universal secret detection은 주장하지 않음 |
| 기존terminal 재사용과 ended-ID 부활금지 | Connector README§6 | ended tombstone, 동일target OPEN reuse, owner claim | TestCompletedTerminalCannotAllocateAnotherPTY, replacement-owner test | map이process lifetime동안누적; retention/durable정책은계약변경없이임의추가하지않음 |
| 60초grace/no replay/본문비저장 | D21v10; realtime/Connector/runtime | timer identity fence; memory-only pipe/stream; unchanged60s production grace | reconnect/grace/exit/datawriter tests | harness3초는명시됨; Browser/SaaS전체는별도C2 |

## C/E. 수정 전 findings

### F-148-ASYNC-01 / P2 — async setup의 PTY result 동기화/late cleanup 누락

- 최초 HEAD 위치: `internal/connector/terminal/ssh_pty.go:230-238`. callback이 outer `pty`를 233에서 쓰고 setup실패 caller가 237에서 동기화없이 읽는다.
- 관련 caller: 현재 `vmaccess/access.go:306`은 callback을 `channelOperation` goroutine에서 수행한다. `:365`의 ctx취소 경로는 worker완료 전에 반환한다.
- 재현 조건: managed PTY의 PTY/shell request reply를 지연시키고 context를 취소한 뒤 늦은 성공 reply 및 초기 stdout을 전달한다. caller의 nil검사/실패반환 이후 callback의 `pty` assignment가 가능하다.
- 영향: data race 및 caller가 반환받지 못한 PTY/pipe reader/Wait goroutine의 cleanup 소유권 상실. stdout copy가 io.Pipe writer에 막히면 raw SSH channel.Close만으로 해당 reader를 닫지 못해 Wait가 남을 수 있다.
- 근거 수준: 최초 코드분석 보고 후 부모가 pre-fix source overlay로 실제 재현했다. reviewer는 `logs/pre-fix-async-run.json` 및 원본 `pre-fix-async.log`를 직접 읽어 `ssh_pty.go:233` writer/`:237` reader data race와 shared transport Close 뒤 abandoned PTY output/Wait worker leak 실패를 확인했다. 명령은 `go test -race -json -count=2 -timeout=90s -overlay ... -run ^TestManagedPTYCanceledSetupReleasesOutputWorkers$ ./internal/connector/terminal`; 예상 negative test exit1. 최종 fixture 상태에서도 `verified-logs/pre-fix-async-run.json`에 동일 negative reproduction을 보존했다. reviewer가 테스트를 실행한 것은 아니다.
- 수정 방향: callback/caller 사이 명시적인 mutex/소유권 handoff. caller가 실패/취소된 뒤 callback이 만든 PTY는 callback에서 반드시 Close하고, 성공으로 handoff한 object만 caller가 가진다. raw channel closure만으로 io.Pipe cleanup을 대체하지 않는다.
- 의미있는 회귀: shell또는PTY request취소→late성공/initialstdout; callererror; 늦은PTY pump종료; 동작중 siblingPTY유지; -race clean.
- 수정 후 코드 검토: `setupMu`가 caller/callback의 result를 보호하고 `abandoned`가 실패 반환 이후 callback-created PTY를 callback에서 Close하게 한다. 실패 반환 전에 생성됐으면 caller가 handoff받아 Close한다. 두 경우 모두 raw SSH channel 외 io.Pipe reader의 cleanup 소유권이 유지되며 성공시 결과 handoff가 명확하다.
- 회귀 코드 검토: `TestManagedPTYCanceledSetupReleasesOutputWorkers`는 production VMAccess/실제 loopback SSH/pinned generatedkey로 PTY-req와shell 각24회(48trial) cancel-before-reply/reply-before-cancel 경합과 initialstdout을검증한다. setup결과소유권, 다음channel이boundedworker를사용할수있음, 같은transport의siblingecho, transport정리후Waitworkerbaseline복귀를검증한다. fake callback의내부순서만고정하는test가아니다. workerbaseline종료검사는sharedtransportClose후라는범위를명시한다.
- 상태: 부모에 수정 전 보고, 부모 수용/수정, reviewer 수정 diff/회귀 코드 검토 완료. 최종 f510f9d unit/race/반복 회귀 원본에서 PASS를 확인했으며 negative reproduction과 대조하여 해결 근거가 충족된다.

### F-148-OWNER-01 / P2 — 교체된 Data socket의 늦은 frame이 현재 PTY를 제어

- 최초 HEAD 위치: `terminal/client.go:478-498` 및 resize/close dispatch; `session.go:330` WriteInput은 closed만 검사한다.
- 재현 조건: oldconnection의 Binary/RESIZE/CLOSE read가 성공한 뒤 dispatch 직전 oldpump를 멈춘다. newclient claim/attach가 oldclient/socket을 닫고 새owner가 current가 된 후 oldpump를 재개한다.
- 관찰 가능한 코드 동작: read전 cancel/DetachChan 검사만 있고 read후 currentconn/owner fence가 없다. 늦은 Binary/RESIZE는 같은PTY에 적용되고 CLOSE는 새owner를 포함한 TerminalSession을 종료할 수 있다.
- SSOT: Connector README§7은 currentconnection으로 새attach를 사용하고 종료된connection으로 INPUT/resize를 처리하지 않는 trust/lifecycle 경계를 요구한다. 단일activeDataowner 의도와도 충돌한다.
- 근거 수준: 코드분석의 구체적interleaving; reviewer 신규 재현 없음.
- 수정 방향: dispatch시 connection/owner 현재성 검사 및 session과의 소유권 handoff. 오래된 frame은 현재 session에 effect를 주지 않아야 한다. 종료frame은 state fence 없이 단순GetDataConn 비교 후 unconditionalClose하지 않는다.
- 의미있는 회귀: deterministicgate로 oldread/dispatch 사이 replace→staleBinary/RESIZE/CLOSE무시, newconn input정상/session살아있음, staleerror도새owner에잘못종료하지않음. 기존 replacementtest는 reconnect ownerstop+newEcho만 검증하여 해당분기가없다.
- 수정 후 코드 검토: Read 반환 후 `handleDataFrame` 진입 시 ctx/current conn/current owner fence로 오래된 frame을 거절한다. 종료 effect는 `closeDataConn`이 session state lock 아래 currentness를 확인하고 Closed를 선점한 뒤 lock 밖에서 Cleanup하여 새 owner를 닫지 않는다. frame admission 이후 이미 실행 중인 I/O 완료는 허용한다는 선형화 경계로 해석했고, blocking SSH I/O 동안 global state lock을 잡지 않아 최신 main backpressure 보강을 보존한다.
- 회귀 코드 검토: `TestReplacedDataReaderCannotDispatchBufferedFrames`는 이미수신된frame의prod dispatch경계를직접호출하고 실제MockDataWSS/PTYecho로 Binary/Resize/CLOSE/fatal을거절하면서새owner가살아있음을검증한다. `TestOldInputFailureCannotEndReplacement`는교체전시작되어교체후실패하는input을deterministicchannelgate로제어하여oldfailure가newowner를닫지않음을검증한다. 타이밍의존socketreadhook이나productiontest우회가없다.
- 상태: 부모에 수정 전 보고, 부모 수용/수정, reviewer 수정 diff/회귀 코드 검토 완료. 최종 f510f9d unit/race/반복 회귀 원본에서 해당 테스트 PASS를 확인했다.

### 기존 finding 반영

구 source의 `vmaccess/access.go:164/280` 개별 취소 shared retire 문제는 최신 main의 channelOperation, late channel result cleanup, channel-close worker와 isolation regression으로 해결된 구현을 직접 확인했다. 최종 unit/race 및 `-race -count=20`의 `TestCanceledChannelLeavesActivePTYUsable`, `TestCanceledOpenClosesLateChannelAndReusesTransport`, `TestStalledOpenDoesNotAccumulateCanceledSSHRequests` PASS를 원본에서 확인하여 해결 검증 상태를 PASS로 갱신했다.

## C. 추가 실패/보안/수명주기 검토

- production WSS TLS, credential refresh/authretry suppression, pinnedhostkey/providerresolvedtarget, generation/lab/VM/server mismatch의 failclosed는 유지된다. 새 wire/field/config 없음.
- safe attach failure/SSH ended message literal로 rawtarget/credential/provider error가 사용자frame에 복사되지 않는다. terminal/SFTP bytes는 actual tests에서도메모리assert만 하고 evidence에로그하지않는다.
- data attachsetup은 client cancellation/socketClose를 연결하고 동일clientconnectMu/streamOnce, ownerreplaceclose와 grace timeridentity/statusclosedfence가있다. late dispatch는finding으로별도분리.
- 종료/output은 n>0+EOF를먼저보내고 stdout/stderr pipe가drain된뒤remoteexit를기다린다. managedclose가reader를닫고 sharedmanager에closure를위임하여 SSH packetbackpressure와참조수release를분리한다. async creation/latehandoff는finding으로별도분리.
- finalENDED는best effort1s이며writerlockwait50ms; normalDatawrites는10sdeadline. Close가blockingwriter보다boundedcleanup을우선하고 oldsocketreplace/detach는writeMu없이socket을닫는main보강이보존됨.
- SessionManager tombstone+Shutdownclosedfence를함께보존했다. 동일endedID는recreate불가, shutdown후새IDallocation불가. tombstoneprocesslifetime누적및restart후소실은운영/재시작검증공백이며이PR에서계약없이retention을추정하지않는다.
- PTY Write/Resize error 회귀는 `TestDataInputAndResizeFailuresCloseOnlyTheirSession`의 input/resize table로 보강되었다. safe Data ENDED/SESSION_ERROR message, manager 등록 제거/StatusClosed, sibling echo 유지를 검증한다. reviewer가 물리 PTY cleanup assertion 보강을 권고했고 efc2258에 실패 PTY의 mutex-protected `closed` assertion이 포함된 것을 직접 확인했다. 이 권고는 새 production 결함이 아닌 회귀 검증 강도 보강이며 최종 반영 완료다.
- Previewproductionforwarder/sharedSSH구현은후속LBT24경계이며148이완료했다고표시하지않는다. KT D1/실제Browser/SaaS/C2/SFTPTreeReadSave/DB검증은범위밖.

## D. 실행/evidence 구분

Reviewer는 read-only git/code/hash 및 원본 로그 분석만 수행했다. 부모의 최종 fullauto/realOpenStack 실행 로그를 직접 읽었으며 실행 주체를 reviewer로 표시하지 않는다.

부모가 추가 발견한 Preview mock gateway의 concurrent WebSocket write panic은 `mock/preview_gateway.go`에서 SendBinary/SendText/ATTACHED를 동일 writeMu 경로로 직렬화하는 local diff 및 `preview_gateway_test.go`의 32 mixed writers frame-count regression을 reviewer가 읽었다. 모든 data/text writes를 직렬화하고 gorilla가 동시 사용을 허용하는 WriteControl은 그대로 둔 수정이 타당하다. 부모 보고상 -race -count20 PASS이며 reviewer는 직접 실행하지 않았다. 이 수정은 최신 main의 기존 fixture 불안정성을 고친 추가 scope이고 production Preview/SSH forwarding 완료를 뜻하지 않는다.

Historical2026-10-10 evidence는소스`1af5c8e`에서unit2801PASS/8SKIP,race337PASS/7SKIP,반복회귀160PASS,realPTY111.02sPASS,cleanup7개ABSENT/discoveryservers0이며이전감사에서zip원본과summary/checksum을직접확인했다. rebase후currentHEAD는session/ssh/handler등blob이바뀌었으므로historicalPASS는current통합코드PASS가아니다. 새testedsource/evidence/secret scan과reproduce절차갱신이필요하다.

Mock Peer/loopback SSH 경계와 실제 VM 경계, ordinary suite opt-in cloud skip과 explicit real test PASS를 분리한다. Web/Contracts/DB의 최종 실행 범위는 제출 evidence 검토에서 확정한다.

### 2026-10-14 최종 원본 검증 확인

`verified-logs`의 원본 Go JSON events를 reviewer가 재집계하여 run metadata와 일치함을 확인했다.

| 검증 | 테스트 source | 결과 | 제한 |
| --- | --- | --- | --- |
| Go unit `./...` | f510f9d, clean | test3361 PASS, test12 SKIP, FAIL0, package35 PASS | explicit cloud opt-in 및 KT/OS별 fixtures skip. package no-test skip은 test12에 포함하지 않음 |
| 변경/consumer 7pkg race | f510f9d, clean | test692 PASS, test11 SKIP, FAIL0, package7 PASS | vmaccess/openstack/app/terminal/wss/mock/preview; cloud opt-in 및 KT fixture skip |
| 중요 회귀 `-race -count=20` | f510f9d, clean | test420 PASS, SKIP0, FAIL0, package4 PASS | 새 setup/owner/error/mock 회귀 및 기존 channel/shutdown 회귀 |
| vet/build | f510f9d | exit0 | 원본 log/stderr empty와 command exit 확인 |
| gofmt | f510f9d | 392개 LF-normalized 비교, unformatted0 | byte-level CRLF 차이를 format 결함으로 혼동하지 않음 |
| 실제 OpenStack CC-02 `-race` | efc2258 production; 최종과 production 동일 | test1 PASS, SKIP0, FAIL0, 133.81초 | Mock SaaS/Control+Data peers, 실제 OpenStack VM/shared pinned SSH/PTY 및 SFTP channel |
| 실환경 cleanup/discovery | 위 실제 실행 | tracked7 resource absence, pre/post servers0 networks4 | 해당 test가 소유한 resources와 discovery 범위; 프로젝트 전체 리소스 임의 삭제 없음 |

실제 log에서 OPEN/ATTACH/input/output/초기크기/resize/2PTY/duplicate, A CLOSE의 ENDED/해제 및 sibling PTY+SFTP 사용, 자동 Data reconnect의 동일 shell/session/shared SSH/no history, remote exit7 전 마지막 output, 3초 test grace 후 해제/late OPEN 거절, 전체 SSH 단절의 안전한 ENDED/모든PTY해제/자동 SSH 재시도 없음이 각각 PASS로 기록된다. cleanup 함수가 tracked resource의 ABSENT 상태를 검증한 뒤 7개 완료 로그를 출력하는 source와 원본 실행을 대조했다. pre/post discovery 원본 모두 servers0/networks4이다. terminal/SFTP payload는 로그에 출력되지 않는다.

`final-logs/cloud-source-stability.json`의 `unchanged:false`를 숨기지 않는다. before/after는 HEAD efc2258 동일이고 변경 파일은 테스트 fixture `managed_setup_cancel_test.go` 하나다. 최종 f510f9d와의 commit diff도 이 fixture 하나뿐이다. 실환경에서 실행한 production blobs의 변경은 없으며 최종 자동 suite 전후는 HEAD f510f9d/source unchanged true다. 제출용 source manifest에서 이 구분을 명시해야 한다.

최초 full unit 실패 `final-logs/go-unit.log`를 보존했다. 원문 실패는 managed setup test `/shell`의 `setup did not reach gated request`(2초 timeout)이다. fixture가 channel Accept ACK 뒤 다음 gate를 snapshot하면 caller probe가 아직 처리 중인데 다음 iteration gate를 소비할 수 있다. f510f9d는 gate snapshot을 `incoming.Accept()` 전에 이동하여 ACK 이후 gate 소유권을 바꾸지 않는다. 독립 diff 검토상 production 변경이 없고 수정한 fixture의 최종 반복/race/full unit PASS를 확인했다. 이 실패와 production negative overlay race/leak 실패를 분리한다.

최종 제출용 evidence README/감사/source-manifest/summary/checksums/secret scan/reproduce/Python runners 및 residual overlay 전체를 읽었다. artifact 검토 결과와 새 portable runner finding은 아래에 기록한다.

### 제출 artifact 독립 검증

대상 `docs/connector/evidence/LBT-148/2026-10-14/` 최종14파일(13 artifact와 checksum index). `.gitattributes`의 `* -text`는 해당 evidence directory에만 적용되며 실제 `git check-attr text`가 unset이다. 이 설정으로 mixed LF/CRLF 원본 바이트 SHA256이 checkout 플랫폼에 따라 바뀌지 않는다. 모든13 SHA256과 source manifest의20개 git blob 및20개 working byte SHA256을 별도로 대조해 mismatch0 확인했다.

zip은 최종776618바이트,151 entry이며 CRC 검사 정상이다. archive의 `first-pass`→원본 `logs`, `premerge-attempt`→`final-logs`, `final-auto`→`verified-logs`, `negative-logs`/`runner-env-logs`→동명 원본의 경로를 매핑하여 전체151 entry byte 일치/mismatch0를 확인했다. 이 매핑은 README에도 명시됐다. archive 안 최종 Go events도 재집계해3361/12,692/11,420/0와 일치한다. 이전 fullsuite FAIL과 전사임을 명시한 initial compile/Preview incidents를 확인했다. negative 두 source 파일은 git의 `f3fa76f` 원본 파일 바이트와 직접 비교하여 동일했다. negative ASYNC race+Wait leak 및 Preview data race+concurrent-write panic 원문이 포함된다. runner 환경 회귀는 별도 경로에서 synthetic/stub 결과로 구분되며 실제 cloud 결과와 혼동되지 않는다.

zip 원본에서 Web13file/163test, build/typecheck/lint 성공과 Contracts8schema/OpenAPI/runtime metadata/Reset11test 성공을 확인했다. Web canvas getContext 및 큰 bundle 경고는 stderr에 보존되어 있으며 실패로 숨겨지거나 제거되지 않았다. 전체 source snapshots의 false는 first-pass auto의 생성된 pnpm lock, premerge cloud의 test fixture 한 파일, premerge contracts의 생성된 pnpm lock뿐이다. production 및 실행 boundary source 변동이 없음을 직접 비교했다.

최종 secret-scan은 전달 text 및 모든 archive entry162개, 실제 credential 값1개/provider auth endpoint1개와 private key/PEM 및 lifecycle-only cloud log 검토를 명시하며 universal detector로 주장하지 않는다. reviewer는 실제 secret 값에 접근하지 않았고 해당 specific-value scan을 다시 수행했다고 주장하지 않는다. archive 전체에 private-key PEM marker가 없는 것은 별도 read-only 확인했다. 이는 모든 가능한 credential 유출을 증명하지 않는다.

residual overlay는 adapter initialization 및 keypairs/security-groups List/Extract만 호출하며 mutation이 없다. `.go.txt`는 기본 Go package에 추가되지 않는다. 실제 원본/zip log의 keypairs0/managementSecurityGroups0와 PASS를 확인했다. 이 inventory는 fixture prefix 범위이며 전 cloud 전체 잔여0 주장과 구별한다.

재현 dependency는 Go/GCC/git, Node/pnpm, Python 및 workflow-pinned validators와 cloud local config/private key로 명시된다. PowerShell/Python runner를 읽었으며 reviewer가 재현 명령 자체를 실행하지 않았다. 부모의 snapshot 실행/PS parse/Python compile 성공은 부모 보고에 한정한다. old git object가 없는 경우 negative archive source fallback이 존재함을 확인했다. source/code equality 및 후속 C2/KT/SFTP/DB/장기운영 미실행 주장은 원본 검증과 일치한다.

### F-148-REPRO-01 / P2 — portable auto runner가 inherited KT cloud opt-in을 유지

- 위치: 제출 `validate.py:40-46`, OpenStack-only prefix 조건43; auto 전체 `go test ./...`80. `ktcloud_d1_network_integration_test.go:20` 및 `ktcloud_d1_integration_test.go:14`는 KT gate를 사용한다.
- 재현 조건: caller 환경에 `LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST=1`과 유효한 KT config/새 ownership ledger/fixture CIDR 등이 설정된 상태에서 `reproduce.ps1 -Stage auto`를 실행한다. `clean_env`는 OpenStack gate만 0으로 바꾸어 KT mutation gate가 유지된다. read-only KT integration gate도 동일하다.
- 영향: portable 자동 검증이 의도한 로컬/Mock 경계를 넘어 별도 KT NSM cloud acceptance 및 mutation을 실행할 수 있다. 이번 실제 로그는 KT tests SKIP이므로 기존 테스트/실환경 PASS 증거에는 영향이 없다.
- 근거: 현재 runner와 명시적 KT gate의 코드 분석. reviewer는 실제 cloud나 재현 테스트를 실행하지 않았다.
- 필요한 보강: 두 KT opt-in도 명시적으로 0 또는 두 provider prefix의 opt-in을 공통 비활성화하며 cloud stage는 자기 PTYCC02/discovery gate만 다시 켠다. README의 auto gate 범위도 맞춘다.
- 필요한 회귀 확인: 합성 inherited environment에서 KT/OpenStack opt-in이 모두 0이고 cloud stage가 자기 두 OpenStack gate만 다시1임을 실제 cloud 호출 없이 확인한다. production 검증 재실행이나 KT mutation 실행은 필요하지 않다.
- 수정 후 코드 검토: `clean_env`의 prefix를 OpenStack/KT tuple로 확장하여 두 provider의 test/audit/integration opt-in을 비활성화한다. main guard로 validator의 stage 실행을 import 때 수행하지 않는다. `verify_runner_env.py`는 원래 환경을 보존/복구하고 합성13 flag를1로 설정하여 실제 `clean_env` 반환값의 모든 gate0을 검증한다. 실제 `cloud` 함수의 `run`만 stub으로 교체하여 pre/PTY/post3 command의 active gate가 두 OpenStack gate뿐인지를 검증하므로 실제 API/명령 호출이 없다. PS env-check routing/README 범위도 일치한다.
- 검증: 부모가 실행한 원본/zip `runner-env-regression-run.json`의 exit0/inherited13 suppress/ordinary active0/explicit gate2/3 intercepted command/actualAPI0을 직접 읽었다. 제출 script 수정은 evidence 재현 경계에만 한정되며 production/full/cloud command source와 이번 실제 skips/결과를 바꾸지 않으므로 생산·실환경 재실행을 요구하지 않는다. reviewer가 이 회귀를 실행한 것은 아니다.
- 상태: 부모에 수정 전 severity/정확 line/재현 조건/영향/보강 및 검증을 보고했고 부모 수정 후 source와 원본 회귀 및 재생성 artifact13hash/zip151을 독립 확인하여 **해결**로 판정했다. 전체 현재 범위의 미해결 confirmed finding 없음.

### 최종 제출 경계

판정 대상 production/test HEAD는 f510f9d이며 실제 VM source efc2258와 production/VM harness/Web/Contracts가 동일함을 직접 확인했다. 이 감사의 마지막 repo 상태는 f510f9d, 새 evidence directory만 untracked다. 문서 commit이 추가되면 부모는 `git diff f510f9d..HEAD`가 해당 evidence directory에만 한정되고 final tree/hash가 유지됨을 확인한다. reviewer 기록의 최종 반영으로 artifact checksum/scan을 재생성하는 것은 실행 결과 변경이 아니다. 새 구현 변경이 생기면 이 판정을 재사용하지 않고 해당 diff/회귀를 다시 검토해야 한다.

## F. 독립검토와 제한

구현과분리된 reviewer가전수diff와관련consumer를검토했다. 신규test/실환경작업/외부write/코드수정없음. ExternalJira/Confluence는부모원문전달에한정, 실제실행은부모에한정한다. Confirmed code-analysis findings와미검증위험/정책공백을분리했다. 수정이있으면변경diff와회귀evidence를다시읽은후최종판정을갱신한다. Audit는push/PR/merge/배포/외부message승인이아니다.
