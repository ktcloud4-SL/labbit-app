# LBT-147 PR #84 후속 독립 감사

검토일: 2026-10-12 KST. 구현과 분리된 `/root/independent_review`가 읽기 전용으로 수행했다. 저장소 production/test/Evidence 파일과 cloud 자원을 변경하지 않았고 원격 쓰기를 수행하지 않았다. 재현은 저장소 밖 Go overlay 및 loopback SSH/HTTP/WSS fixture로만 실행했다.

현재 코드에서 새로 확인된 production 결함은 없다. CODEOWNER review 5486377783의 R1/P1, R2/P2, R3/P2 수정은 수용한다. 전체 LBT-147 PR 변경과 실제 재실행을 대조했으며, 원본 실행 로그와 소스 바이트는 일치한다. 이 문서 작성 시점의 신규 Evidence 게시 패키지는 아직 최종 고정되지 않았다. 코드·실행 검토와 별도로 게시 패키지의 최종 manifest/checksum/재현 스크립트 감사를 마친 후 최종 PR 갱신 판정을 확정한다. LBT-145 KT Cloud 선행 조건이나 실제 SaaS/Browser/File 전체 경로가 완료되었다는 판정은 하지 않는다.

## 1. 고정된 범위와 최신 원문

- 저장소: `tmp/labbit-vm-access-147`, branch `feat/LBT-147-vm-access`.
- HEAD: `ed7843e25be99f414bfb0087d2f07b8121fe08a0`; origin/main: `e850567066134edc87b2ba543d0eb42a21f406b8`; merge base: `6d675c74bff11f481d247267faefd4ce960dcd38`.
- 감사 범위는 origin/main...HEAD와 staged/unstaged/untracked를 합친 **54파일 전체**다. Evidence 27파일, Go/runtime 27파일이다. 이번 수정 11파일과 이전 HEAD에서 바뀌지 않은 source 16파일을 모두 포함했다. tracked diff만으로 누락되는 신규 regression 3파일도 포함했다.
- 실행 소스는 HEAD + local 11파일이다. `reviewer-executed-source-manifest.json`이 54파일의 byte SHA256와 Git blob을 고정한다. 실행 전 및 자동/실환경/독립 테스트 후 14:32 KST에 54개 SHA와 blob을 재검증했고 불일치 0이었다(`reviewer-verification.json`). production 파일을 수정 전 HEAD의 PASS로 검증 처리하지 않았다.
- main의 base 이후 추가 commit은 `docs/frontend/deployment-handoff.md` 단일 문서다. 실제 Terminal/File E2E 및 WSS routing 미검증 경계가 유지되고, 이 PR의 backend/runtime/SSH 구현과 충돌하는 새 요구사항은 없었다.
- 개인 `PRE_MERGE_AUDIT.md`, repo AGENTS.md/CONTRIBUTING.md/TESTING.md를 모두 읽었다. 현재 connector/realtime 계약, runtime owning SSOT, 관련 backend 책임/운영 경계를 대조했다. HTTP schema/DB migration은 VM Access transport 소유권·Control wire를 바꾸지 않는 변경이므로 새 schema/migration 변경이 필요하지 않다. 그 적용 여부를 확인했고 계약 workflow 및 Reset schema 실제 실행 결과도 확인했다.

독립 reviewer가 Jira/Confluence 원문과 댓글을 직접 읽고 기존 게시 snapshot과 대조했다. `reviewer-ssot-core.json`, `reviewer-ssot-related.json`, `reviewer-ssot-delta.json`에 근거가 있다. 8개 본문/버전과 해당 Jira 댓글 본문은 동일했다.

| 최신 자료 | 버전·상태 | 이번 감사에 적용한 경계 |
| --- | --- | --- |
| [LBT-147](https://samsunglions.atlassian.net/browse/LBT-147) | 진행 중, 최신 댓글 0 | OP-01 결과의 management VM Access; 대상 일치, shared SSH, 독립 PTY/SFTP, idle/failure/cancel/generation/security |
| [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145) | 진행 중, 최신 댓글 1 | KT reset gen2 및 잔여 Tier cleanup 선행 조건 미완료; local OpenStack PASS로 KT 완료 처리 금지 |
| [LBT-21](https://samsunglions.atlassian.net/browse/LBT-21) | 진행 중, 댓글 0 | OP-02 상위 범위; LBT-148 Terminal CC-02 통합 및 LBT-149 production SFTP/File은 후속 |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | v5 | Connector→관리망 VM 직접 SSH, VM 공유 transport/독립 채널, 기존 서버별 host pin |
| [D-21](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516) | v10 | PTY Connector 소유, WSS reconnect/grace와 실제 SSH 종료 구분, restart 종료, Terminal 본문 비영속/비재생 |
| [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) | v7 | 결과 불명·부분 실패 exact resource ledger/Reconcile; blind mutation 재시도 금지 |
| [테스트 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) | v3 | unit/fixture/실환경/E2E 및 skip를 구분 |
| [MVP 계획](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071) | v19 | 선행 조건·후속 통합 단계와 현재 구현 PR 판정 분리 |

## 2. 수정 전 findings 및 해결 근거

CODEOWNER review 5486377783은 HEAD ed7843e에 CHANGES_REQUESTED로 제출되었고, reviewer가 GitHub의 review/thread 원문을 읽었다(`codeowner-review.json`). 4 inline thread의 내용은 세 결함이며 R1이 OpenSession/Setup 두 지점에 있다. 아래 옛 라인은 그 review revision 기준이다.

| Finding | 수정 전 위치·재현 순서 | 영향·계약 근거 | 현재 수정·회귀 결과 |
| --- | --- | --- | --- |
| R1 / P1 | `vmaccess/access.go:164`, `:280`: A PTY가 shared SSH에서 동작 중, B NewSession confirm 또는 PTY request reply를 peer가 지연, B cancel/timeout | B 종료가 transport retire를 호출해 A가 EOF로 종료. D-18 독립 채널 및 LBT-147 shared VM transport/cancel cleanup 위반 | `channelOperation:332`은 transport당 contextless open/setup worker 1개와 취소 가능한 gate를 사용. B reservation만 반환하고 late B channel을 닫으며 A transport를 보존. `Session.Close:286`은 close queue 및 reference release를 짧은 lock에서 수행. 독립 실제 loopback SSH 두 테스트가 옛 access.go overlay에서 모두 FAIL(A read EOF), 현재 -race에서 모두 PASS. production 회귀는 open/pty/sftp × cancel/timeout, late confirm, 24 canceled waiters, A I/O와 transport 유지/idle 종료를 검증 |
| R2 / P2 | `openstack/terminal_target.go:35`: Nova/Neutron 503 또는 Provider lookup 실패 | 잘못된 target이 아닌 운영 장애를 INVALID_SESSION으로 전송; retry/error 계약 구분 훼손 | `ResolveVMAccess:31`은 명시적 target/ownership 실패만 InvalidTarget으로 유지하고 API/auth/decode/not-ready/management-IP 실패는 safe Unavailable. Nova404 expected target만 InvalidTarget. 독립 ActualAdapter→Manager→WSS Handler fixture에서 옛 소스 Nova503/Neutron503 두 leaf FAIL, 현재 두 장애 ATTACH_FAILED 및 mismatch INVALID_SESSION 모두 PASS. 기존/current table는 17 분기 및 body/IP 비노출/context class를 검증 |
| R3 / P2 | `app/app.go:250-252`: root context cancel→Supervisor가 client.Close/SetSender(nil)→app LIFO defer VMAccess.Close→Terminal close | restart가 EOF/SSH reason과 경쟁하고 Control ENDED 전송 경로도 먼저 제거됨. D-21 SERVICE_RESTARTING 및 Connector PTY lifecycle 경계 | `runControl:246`의 once shutdown은 TerminalManager.Shutdown(SERVICE_RESTARTING) 후 VMAccess.Close. `Supervisor.SetOnShutdown:122`, `Run:137`은 root cancel 시 current Control client close 이전에 호출; 일반 reconnect에는 호출하지 않음. terminal `Shutdown:197`은 admission을 닫아 늦은 OPEN을 거절. 독립 옛 app.go overlay는 실제 authenticated loopback SSH+Mock Control/Data WSS에서 Control ENDED 누락으로 FAIL. 현재 runtime/control/data/peerclose, delayed admission, normal reconnect의 3개 -race 테스트 PASS. 독립 재현에서 옛 EOF reason 경쟁 자체는 재현하지 못했고 코드상 경쟁과 CODEOWNER 근거로 구분 기록 |

이전 감사 F1/P1 destructive FAILED result fencing과 F2/P2 lazy init waiter deadline도 전체 변경 검토에 포함했다. 이들은 74c052f25e3778cafa6ae4fa75bd8775af46827a에서 수정되었고 이번 소스에서도 재검증됐다. FAILED+빈 result/all DELETE_NOT_ATTEMPTED는 preflight 보존, DELETE_FAILED/DELETED/replacement resource 또는 결과 불명은 보수적 fence다. lazyProvider는 짧은 mutex 밖 owner factory를 실행하고 shared attempt result를 waiters에게 전달하며 각 waiter는 자기 ctx로 이탈하고 실패 다음 요청은 retry/cache한다. 일반 한 reader의 직렬 handler 실행을 concurrency 근거로 사용하지 않았다. reconnect 중 이전 accepted operation은 root operation context에 종속되고 새 reader의 Terminal OPEN과 겹칠 수 있다.

## 3. 전체 요구사항 추적

표의 구현 라인은 현재 manifest 소스 기준이며, 테스트 존재와 실행을 구분했다. 자동 원본은 `go-unit.log`/`go-race.log`/`regression-repeat.log`, 실제 원본은 아래 cloud logs다.

| 요구사항·Decision | 구현·경계 | 실제 실행한 자동/fixture | 실제 외부 검증·tested source | 공백·판정 |
| --- | --- | --- | --- | --- |
| LabInstance/generation/logical VM/provider server/project 일치; malformed/stale는 SSH 전 거절 | `terminal_target.go:31,56`; `access.go:86,208`; Handler authenticated envelope `handler.go:909` | target input/envelope/ownership 13+17 분기, delayed resolution/setup, future/stale mismatch | current HEAD+11, `real-vm-access.log`: wrong Lab/gen/VM SSH 전 거절, reset gen2/stale gen1 거절 | 로컬 구현 범위 충족; 실제 SaaS target resolver 통합 후속 |
| Management NIC fixed private IP, 기존 resolver 재사용; Control IP 비신뢰 | `terminal_target.go:56`: configured management network, exactly 1 owned compute port/IPv4 | wrong network/device/project, 0/2 ports/IP, missing/decode/API errors | 현재 normal/app 실제 management SSH | private는 configured management NIC의 fixed IP 의미; public/floating/lab IP fallback 없음 |
| local username/key 결정; 서버별 enrolled host pin 재사용 | `terminal_target.go:120`, `access.go:439`, app config | correct pin/changed pin/missing pin/invalid key/auth failures, pin file unchanged test | normal gen1/gen2 actual key-auth SSH+server pin | 실제 hostile host/key 교체는 fixture; key 원문 비기록 |
| VM별 lazy single shared SSH; 동시 open handshake 공유 | `access.go:86`, entries target/endpoint, `ready` | concurrent 16 channel, fresh target/endpoint, cached target revalidate | normal 실제 PTY+SFTP shared authenticated transport | connection retry는 explicit next open; automatic reconnect 없음 |
| PTY/SFTP channel 독립; 다른 채널 close/cancel 영향 없음 | `Session.Close:286`, `Setup:297`, `channelOperation:332`; managed SSHPTY `ssh_pty.go:210` | real authenticated loopback A echo/B open/pty/sftp 취소/timeout, late open cleanup, waiters limit | normal 실제 PTY IO/resize/SFTP 독립 close PASS | 생산 SFTP/File wire/Browser CC-02는 후속. 실환경의 adversarial SSH reply delay는 미실행이며 loopback fault fixture로 검증 |
| no-channel idle configurable, channel setup/reference/idle race | `access.go:246,286,401`; app idle config `vm_access.go:14`; runtime v0.1.7 | positive/invalid/default duration; shared channel keepalive/last close idle, concurrent open | normal actual no-channel idle retirement; gen2 fresh transport | Terminal60s grace와 VM idle는 별개 |
| auth/unreachable/handshake timeout/cancel/peer disconnect cleanup | `dialSSH:439`, `retireLocked:232`, waiter/context checks | raw-safe auth/host/key, stalled handshake real TCP cancel/deadline, socket EOF/cacheevict, remote disconnect, sole channel idle | normal actual SSH auth; app actual Keystone init response delay | unreachable/hostile handshake는 fixture. 모든 failure 유형을 cloud에서 일으켰다고 주장하지 않음 |
| Reset/Cleanup 및 partial/unknown 실패에서 old generation 재사용 금지 | app lifecycle `vm_access.go:59,66,80`, `access.go:208`; actual lifecycle producer | partial/first reject/replacement/preflight active channel close 또는 보존, another Lab 보존, delayed resolve/setup, actual Adapter HTTP partial result | current `real-app-boundaries.log` 5P1: real preflight/worker DELETE+GET404/old full cleanup+Neutron gen2, scoped 409 주입 | DELETE409 3회/NovaPOST409 1회는 injected HTTP 실패이며 실제 cloud 자체 장애로 주장하지 않음 |
| shared lazy Provider init waiters deadline/cancel/failed retry | `runtime_provider.go:56` | held owner/waiter cancel/deadline40ms, canceled cached/uninit, error/nil/wrong ID result 공유, owner cancel/retry | current app 2P2 actual Keystone auth response gate, waiter/manager timeout/cancel/owner retry | Keystone service는 real; response delay gate는 harness 주입 |
| operational Provider error vs invalid target Control code | `terminal_target.go:31,56`, Handler mapping `handler.go:909` | actual Adapter+HTTP fixture→real Handler Nova/Neutron503/401/decode,404/mismatch,config/notACTIVE/noIP/cancel/deadline; code/redaction | cloud 정상 API와 strict mismatch만 실제 실행 | R2 장애는 controlled HTTP fixture, 실제 Provider outage E2E 아님 |
| root restart 종료 이유·admission·notification order; ordinary reconnect 보존 | `app.go:246`, `session.go:93,197`, `reconnect.go:122,137` | authenticated SSH +Mock SaaS/Data WSS Control/Data SERVICE_RESTARTING exactly once, late OPEN factory blocked/admission closed; normal reconnect hook0 | 직접 관련 real VM 경로 재실행; shutdown notification 자체는 local fixture | reachable connected peer에서만 전송 검증. 실제 SaaS·slow Data WSS·OSkill는 미검증 |
| private key/raw SSH error 외부/Evidence 비노출, Terminal/SFTP 본문 저장/Evidence 제외 | local endpoint, safeError/constants, Handler ended safe payload, terminal Data safe payload | Control/Data raw-error redaction, API sensitive body/private IP 비노출 | cloud/loopback logs에는 metadata만, 실제 terminal probe body 로그 없음 | 신규 게시 패키지 final secret scan/checksum은 별도 최종 확인 예정 |
| exact owned resource cleanup/reobserve·다른 자원 보존 | lifecycle exact IDs, generation ownership, local ledger | partial deletion/reconcile/failure stop tests | current normal14 +app16 distinct tracked ABSENT; app aux managementSG/keypair independent404; final readonly inventory matching prefix0; baseline/reobserve servers0 images3 flavors4 networks4 | 테스트 생성/ledger 범위만 cleanup 보장. KT API404/portal 잔여 문제까지 해소한 근거 아님 |

## 4. 실패·보안·수명주기 독립 분석

1. B open/setup 취소는 A transport를 retire하지 않는다. channelOperation의 unbuffered result ownership handoff 및 ctx 재검사로 caller/worker가 동일 late result를 유실하지 않는다. 기다리는 요청은 worker를 새로 시작하지 않고 자기 ctx로 이탈한다. setup callback은 contextless SSH API일 수 있으므로 peer가 끝내 reply하지 않으면 **worker 1개가 해당 transport lifetime까지 남을 수 있다**. A가 살아 있는 동안 worker가 request timeout 시점에 반드시 소멸한다고 주장하면 틀리다. idle/Invalidate/Manager.Close가 transport를 닫으면 해제된다. 이 제한을 공개해야 한다.
2. Session.Close는 logical close와 reservation release를 먼저 수행하고 raw SSH Close를 transport당 1 cleanup worker로 보낸다. blocked channel-close write가 global manager mutex, timeout 반환 또는 idle retirement를 막지 않는다. close queue는 retirement 시 제거되고 client socket은 종료된다. raw library가 close acknowledgement 전 내부 channel 상태를 갖는 동작까지 즉시 제거한다고 주장하지 않는다.
3. generic SSH fixture는 close message에 library가 자동 ack하므로 “peer가 close ack까지 무시”한 테스트로 확대 해석할 수 없다. 이를 보완하는 별도 external dependency fault fixture를 실행했다(다음 절). client-side crypto 코드와 repo는 변경하지 않았다.
4. delayed old resolve/open/setup는 generation floor와 return 직전 affinity/retired 재검사를 통과해야 한다. destructive FAILED/UNKNOWN result는 old floor/transport retire, preflight 실패는 정상 active access 보존, 다른 Lab는 보존한다. provider producer FAILED resource states를 실제 Adapter fixture와 real app harness로 확인했다.
5. initialized Provider의 connection ID 일치, owner ctx 전달, failed attempt waiters safe error 공유, caller cancellation race와 cache branch를 확인했다. HTTP mutation은 init 실패시 resource 없는 definite safe FAILED, retry는 다음 caller가 init만 다시 수행한다. 이 검토를 cloud operation idempotent retry 전체 구현 완료 주장으로 확대하지 않는다.
6. shutdown once를 Supervisor/root app에서 공유하고 current client close보다 먼저 Terminal closed admission 및 reason을 정한다. 일반 heartbeat/read failure reconnect는 hook를 호출하지 않는다. shutdown 중 factory가 registry lock을 보유한 경우 factory(root ctx) 종료 뒤 admission을 닫고 추가 open을 거절한다. runtime 통합 회귀는 terminal Data/Control ENDED 모두 같은 session/reason으로 한번만 보내고 SSH peer를 닫는 행동을 검증한다.
7. **잔여 위험/미검증:** baseline Terminal Data write는 global Data write mutex/writeJSON에 deadline 없이 기다릴 수 있다. 이 PR의 새 hook도 slow/unread Data peer라면 그 완료를 기다릴 수 있다. 느린 peer backpressure를 독립 재현하지 않았으므로 confirmed 새 defect로 분류하지 않았다. Control 연결이 이미 끊겼거나 송신 실패한 경우 ENDED 전달은 best-effort이고 actual SaaS 재전송·OS 강제 종료 복구·TLS443 routing까지 검증되지 않았다. unrelated source 변경은 하지 않았다.
8. key 원문/PEM/Auth token/provider response bodies는 source/Evidence에 복사하지 않는다. Host verification은 기존 ProviderConnection/Server identity pin이며 terminal lookup이 TOFU 신규등록이나 IP reuse trust를 하지 않는다. terminal raw errors는 local 원인이 아니라 safe fallback으로 wire에 매핑된다. generation map은 process-local이며 process restart 후 provider metadata를 다시 검증한다. immutable/shared service channels를 DB에 저장하는 변경이나 Terminal본문 replay를 추가하지 않았다.

## 5. 실제 실행·fixture·skip 구분

실행 원본 및 명령/exit/time을 독립적으로 읽고 재집계했다. 새 소스 자동 PASS를 이전 ZIP 원본의 PASS에서 끌어오지 않았다.

| 수행자·범위 | 원본·실행 | 관찰 결과 |
| --- | --- | --- |
| 구현자 전체 Go | go-unit.log + run.json, go test -json -count=1 ./... | 2,856 PASS/7 skip/0 fail; gofmt, vet/build exit0 |
| 구현자 변경 관련 race | go-race.log, vmaccess/openstack/app/terminal/wss 5패키지 | 392 PASS/6 skip/0 fail |
| 구현자 핵심 반복 | regression-repeat.log, -race -count=20 | 600 PASS/0 skip/0 fail(부모와 subtest event 포함) |
| 구현자 Web/계약 | typecheck/lint/test/build; exact contracts workflow OpenAPI/JSON Schema/runtime/migration; Reset schema | 모두 exit0, Web163 PASS, Reset11 PASS |
| 독립 R1 red→green | independent-before/after-r1.jsonl; copied old access.go overlay | 옛 두 leaf FAIL, 현재 두 leaf PASS/race |
| 독립 R2 red→green | independent-before/after-r2.jsonl; copied old target.go overlay | 옛 두 outage leaf FAIL/mismatch PASS; 현재 세 leaf+parent=4 PASS/race |
| 독립 R3 red→green | independent-before/after-r3.jsonl; copied old app.go + runtime test snapshot | 옛 Control ENDED누락1 FAIL; 현재 runtime/admission/reconnect3 PASS/race. 구현자의 regression fixture 재사용이라는 한계 명시 |
| 독립 unacknowledged-close fault | independent-after-unacked-setup-copy.jsonl | 외부 x/crypto server B close-ack 무시, A I/O 유지/12 deadlinewaiters/새channel request 증가0/lastAclose idle/freshtransport, -race -count=5 PASS |
| 독립 portable fixture 재현 | portable-unacked-run/test.jsonl; portable-unacked-ps5-run/test.jsonl; portable-unacked-ps7-final-run/test.jsonl | PowerShell7/WindowsPowerShell5.1 최종 동일 script SHA ad2c9124... 각각5회 PASS; JSONL UTF8/BOM 없는 overlay 확인. PS7 최초판5회 PASS 원본도 보존 |
| 실제 OpenStack 정상 | real-vm-access.log + run.json; current manifest bytes, -race -count=1 | 217.25s PASS; gen1 실제 auth/PTY IO/resize/독립SFTP/shared/idle, gen2Reset/new target/stale1 거절;14exact resources ABSENT |
| 실제 OpenStack app 경계 | real-app-boundaries.log + run.json; unchanged harness SHA0a059260..., current product bytes, -race | 231.06s parent+7subtest PASS; 실제 auth/SSH/API와 정확 owned HTTP fault proxy; P1/P2 검증;16tracked+SG/keypair부재 |
| 실제 readonly inventory | pre-discovery.log; post-discovery-and-residual.log | pre1 PASS/post2 PASS, matchingTestResidualTotal0, servers0/images3/flavors4/networks4, 전체 clouds prefix 조회 범위 표시 |

기본 Go 7skip는 OpenStack opt-in 6개(M1, M2Control, M2Provision, M3Lifecycle, InternetPolicy, VMAccess)와 Windows POSIX0600 1개다. race 6skip는 같은 OpenStack opt-in이다. 이번 별도 real suite에서 M1과 VMAccess는 실제 실행되어 통과했지만 나머지 mutation/Internet/ControlWSS opt-in 테스트가 실제 실행된 것으로 바꾸지 않았다. KT/production SaaS/Browser/File/fullTLS443 실제 E2E도 미실행이다.

## 6. 독립 hostile SSH fixture 재현과 의존성

`reproduce-independent-unacked.ps1`을 audit fixture 파일들과 함께 둔다. 필요한 파일은 `dependency-fixture-manifest.json`, `reviewer-executed-source-manifest.json`, `independent_unacked_setup_test.go`, `ssh-channel-no-b-close-ack.go`이다. Go와 설치된 `golang.org/x/crypto@v0.57.0` module cache가 필요하다. 없으면 먼저 프로젝트의 pinned go.sum을 사용해 해당 module을 내려받는다. 스크립트는 dependency를 자동 다운로드하거나 repository/module cache를 수정하지 않는다.

예: `./reproduce-independent-unacked.ps1 -RepoPath <checkout> -OutputDirectory <새로 만들 외부 디렉터리>`; `-PrepareOnly`는 copy/overlay/modfile 준비만 한다. 새 output을 요구하고 삭제·기존 파일 덮어쓰기·global go wrapper 교체를 하지 않는다. 고정된 54file SHA, go.mod/go.sum, original ssh/channel.go와 fixture hashes를 검사하고 vmaccess package에 ignored 포함 예상 외 Go source가 있으면 거절한다. VM Access package만 정확 test pattern으로 실행하므로 cloud mutation 0이다.

실제 실행 준비 과정: 공용 GOMODCACHE의 x/crypto를 외부 `crypto-audit`에 복사→별도 audit.mod/sum에서 local replace→copied server inbound B(channel localId1)의 `msgChannelClose`만 무응답 처리하는 overlay→동일 product access.go를 테스트. client outbound 코드는 원본이다. 스크립트가 경로를 현재 RepoPath/OutputDirectory로 재생성하고 UTF8Encoding(false)로 overlay JSON을 쓴다. 동작·원본/patch/fixture SHA는 `dependency-fixture-manifest.json`, `reviewer-verification.json`, 각 output의 prepared.json에 있다. 초기 공용 GOMODCACHE 직접 overlay 시도는 Go가 거절해 테스트를 시작하지 못했다(`independent-after-unacked-setup.jsonl`); module copy 방식으로 수정 후 PASS했으며 이 기록을 숨기지 않는다.

## 7. 전체 파일별 coverage

아래 54파일의 전체 base→HEAD+local 변경 목적·영향을 검토했다. source 변경 줄뿐 아니라 호출자/provider producer/SSOT와 자동·실환경 Evidence를 대조했다. 이번 수정 표시가 없는 source16도 이전 감사 결론을 그대로 승계하지 않고 현재 바이트와 적용 분기를 확인했다.

| 파일(저장소 상대 경로) | 검토 목적·경계 |
| --- | --- |
| internal/connector/app/app.go | resolver/manager/runtime wiring 및 이번 shutdown once/Control hook |
| internal/connector/app/app_test.go | production enabled VM resolver 요구, 기존 address resolver fallback 테스트 경계 |
| internal/connector/app/main_integration_test.go | real loopback authenticated SSH+Mock Control/Data runtime와 이번 ended ordering |
| internal/connector/app/runtime_provider.go | F2 shared init owner/waiter/context/cache/retry/ID safe failure |
| internal/connector/app/runtime_provider_test.go | owner/waiter failure/cancel/deadline/cache/null/wrong ID 분기 |
| internal/connector/app/vm_access.go | idle env owning contract, F1 lifecycle FAILED/preflight fencing |
| internal/connector/app/vm_access_lifecycle_test.go | active SSH peer 보존/close, anotherLab/oldreopen/delayedresolve/setup |
| internal/connector/app/vm_access_test.go | reset/cleanup outcomes/ResourceStates 및 runtime duration |
| internal/connector/provider/openstack/lifecycle_test.go | actual Adapter HTTP partialDELETED/FAILED/NOT_ATTEMPTED producer 및 stop/rawredaction |
| internal/connector/provider/openstack/terminal_target.go | strict target/management/server pin; 이번 operational error 분리 |
| internal/connector/provider/openstack/terminal_error_test.go | 신규 actualAdapter+HTTPfixture→Manager→Control17case |
| internal/connector/provider/openstack/terminal_target_test.go | envelope mismatch/preSSH,NIC ambiguity/ownership, existingpin 변경/미등록 거절 |
| internal/connector/provider/openstack/vm_access_integration_test.go | opt-in actual normal VMgen1/gen2, actualPTY/SFTP/idle, exactledger finallycleanup |
| internal/connector/terminal/session.go | safeDataEnded 및 이번 closed admission Shutdown/once registry/pump 순서 |
| internal/connector/terminal/shutdown_test.go | 신규 pendingfactory/delayed admission/ENDEDonce 의미 있는 행동 검증 |
| internal/connector/terminal/ssh_pty.go | managedSession channel 소유/Setup/Wait/Resize/close 및 legacy 경계 |
| internal/connector/terminal/terminal_test.go | Terminal Data raw SSH detail redaction 보완 및 기존grace/sessionbehavior |
| internal/connector/vmaccess/access.go | shared VMtransport/handshake/ref/idle/gen/failure +이번 contextlessoperation gate/latecleanup |
| internal/connector/vmaccess/access_test.go | 실제 loopback SSH fixture/auth/concurrent/endpointreplace/disconnect/idle +이번heldopen/requestsignals |
| internal/connector/vmaccess/cancellation_test.go | socketdeadline/ctx timer race, handshake socketcleanup, delayedresetresolver/setup |
| internal/connector/vmaccess/channel_isolation_test.go | 신규 A echo/B6cancel paths, latechannel, stalledrequest accumulationlimit |
| internal/connector/wss/handler.go | authenticatedtarget passage, InvalidTarget mapping, rawSSH safe Control/Ended |
| internal/connector/wss/reconnect.go | 이번 rootcancel shutdown hook, existing accepted operation context 및 sender lifecycle |
| internal/connector/wss/reconnect_test.go | ordinary reconnect callback0, rootcancel callback1 |
| internal/connector/wss/vm_access_test.go | Control OPEN/ENDED privateSSH detail 비노출 |
| runtime/README.md | idle default1m/positiveGoDuration, VMchannelidle≠Terminalgrace |
| runtime/contract.yaml | v0.1.7 SSH_IDLE_TIMEOUT owningSSOT 및 runtime/deploy nonguarantees |
| docs/connector/evidence/LBT-147/2026-10-10/README.md | 최초 정상 acceptance/실행일/제한/history |
| docs/connector/evidence/LBT-147/2026-10-10/artifact-checksums.json | 6artifact byteSHA 재검증 |
| docs/connector/evidence/LBT-147/2026-10-10/automated-logs.zip | 9entryCRC/원본Go eventCounts/초기 realfail 보존 |
| docs/connector/evidence/LBT-147/2026-10-10/reproduce.ps1 | 최초 tested source/opt-in 경계, 현재 source용 증거로 승격하지 않음 |
| docs/connector/evidence/LBT-147/2026-10-10/secret-scan.json | historical scan scope/results, currentscan 대체금지 |
| docs/connector/evidence/LBT-147/2026-10-10/source-manifest.json | 원본 실행HEAD/미커밋byte 식별 |
| docs/connector/evidence/LBT-147/2026-10-10/validation-summary.json | 2792/7skip,328/6skip/actual initialfail→laterPASS 원본정합 |
| docs/connector/evidence/LBT-147/2026-10-12/.gitattributes | archived byte preservation |
| docs/connector/evidence/LBT-147/2026-10-12/AUDIT.md | 당시 fullscope/actualvsinjected/KT/skip/history, 새R1-R3최종판정과 구분 |
| docs/connector/evidence/LBT-147/2026-10-12/README.md | 당시 실행source74 mapping/actualscope/repro/risks |
| docs/connector/evidence/LBT-147/2026-10-12/archive-manifest.json | 28entryexactset/SHA/size 재검증 |
| docs/connector/evidence/LBT-147/2026-10-12/artifact-checksums.json | 19artifact/20file byteSHA/size 재검증 |
| docs/connector/evidence/LBT-147/2026-10-12/audit-logs.zip | 28entry CRC/SHA와 원본 initialfixturepanic/redfindings/chronologicalaudit 보존 |
| docs/connector/evidence/LBT-147/2026-10-12/automated-validation-summary.json | 당시2828/7skip,race364/6skip 원본일치 |
| docs/connector/evidence/LBT-147/2026-10-12/post-discovery-and-residual.jsonl | 당시 readonly2PASS/prefix0, currentrun은새log로확인 |
| docs/connector/evidence/LBT-147/2026-10-12/pre-discovery.jsonl | 당시 discovery1PASS/baseline; currentbaseline별도 |
| docs/connector/evidence/LBT-147/2026-10-12/publication-checks.json | 당시 publication검사과현재source검사 구분 |
| docs/connector/evidence/LBT-147/2026-10-12/publication-review.md | 당시review판정/history byte보존; R1-R3문제수정최신감사로대체 |
| docs/connector/evidence/LBT-147/2026-10-12/real-app-boundaries.jsonl | 당시7subtest actual/injected/ownedledger; currentrun8PASS이벤트별도 |
| docs/connector/evidence/LBT-147/2026-10-12/real-vm-access.jsonl | 당시229.83s actual; current217.25s 새source재실행구분 |
| docs/connector/evidence/LBT-147/2026-10-12/reproduce-guard-regression.json | 당시PS5/7 24case cloud0/wrapper restoration 원본 |
| docs/connector/evidence/LBT-147/2026-10-12/reproduce.ps1 | code74 fulltracked/staged/untracked/ignoredGo guard/dateEvidence exception/zip integrity; 현재followupsource면거절하는historicalscript |
| docs/connector/evidence/LBT-147/2026-10-12/secret-scan.json | 당시scan84files/entries findings0; 신규Evidence최종scan필요 |
| docs/connector/evidence/LBT-147/2026-10-12/source-manifest.json | 3996817+6수정→74 source mapping 및 historicalharnesspin |
| docs/connector/evidence/LBT-147/2026-10-12/ssot-summary.json | 이번직접8원문 body/version/comment대조동일 |
| docs/connector/evidence/LBT-147/2026-10-12/validation-summary.json | 당시actual14+16+aux,actualvsinjected/cleanup/source원본정합 |
| docs/connector/evidence/LBT-147/2026-10-12/verify-reproduce-guards.ps1 | originalgo globalwrapper finallyrestore/removal, sixguardcases×4runs; historicalsource용 |

미검토 source 경로는 없다. source unchanged 운영 producer/caller로 lifecycle.go의 delete failure states/Reconcile/exact ownership, startup.go의 server pin identity/enrollment, adapter auth context/timeout/config, Handler Control envelope/send/redaction, terminal PTY and Data connection/pump, Supervisor client sender lifetime도 확인했다. 변경 없는 HTTP/DB/infra/browser의 전체 독립 acceptance를 검토한 것으로 주장하지 않는다. 모듈 dependency version/go.mod/go.sum의 PR 변경은 없고 hostile SSH fixture만 외부 local replace를 사용했다.

## 8. 기존 Evidence 무결성 및 최종 한계

`historical-evidence-integrity.json`: 10/10 7파일·6checksum·ZIP9entry CRC PASS; 10/12 20파일·19checksum·ZIP28entry CRC 및 모든 entry SHA/size PASS. artifact 불일치0이고 원본 JSONL parse오류0이다. 최초 real failure, F1/F2 red regressions, initial fixture panic, 모든 skip가 보존되어 있다. historical automatic counts 2792/328 및2828/364는 각 summary와 일치한다. 이 기록이 11수정 파일의 최신 검증이라고 표현하지 않았다.

현재 구현/실행 scope에서는 확인된 새 결함 없이 세 findings 수정이 검증됐다. 실제 정상 관리망 SSH/PTY/SFTP/Reset과 앱 failure/init 경계 재실행은 최신54manifest에 핀됐고 cleanup/reobserve도 확인했다. final evidence packaging/source pin/portable dependency preparation·secret scan/checksum가 고정된 후 게시 감사를 다시 수행한다. CI/외부 CODEOWNER의 재승인, LBT-145 KT acceptance, LBT-148/149 및 actualSaaS/Browser/File/TLS443, slowDataWSS/backpressure/kill/restart persistence는 남은 경계다. 감사 통과는 merge/main push/배포/Jira writes 승인으로 취급하지 않는다.

## 9. 게시 재현 helper 추가 finding — 수정 전 보고

PUB-NC-1 / P2: 신규 `reproduce.py:77-86` 초기판은 `--cloud`가 없을 때도 `os.environ.copy()`의 OpenStack opt-in 환경변수를 auto runner에 그대로 넘겼다. 앞선 실제 실행 셸에 `LABBIT_OPENSTACK_VM_ACCESS_TEST=1` 등이 남아 있으면 기본 재현의 `go test ./...`가 `vm_access_integration_test.go:21-25` 조건을 통과하여 실제 Provision/Reset에 진입한다. “cloud mutation은 --cloud 명시 때만”이라는 게시 helper의 선언/사용자 실행 범위와 달라 최종 게시 판정 전 보완이 필요하다. 이 finding은 production 변경이 아닌 Evidence 재현 helper에 한정하며 구현자에게 수정 전에 정확 위치/조건/영향/회귀를 보고했다.

실제 Go/cloud를 실행하지 않고 외부 recording subprocess stub으로 portable runner auto를 호출했다. opt-in10개=1이 go-unit/race childenv에 그대로 전달됨을 확인했다(`noncloud-env-before-fix.json`, `portable-run-before-env-fix.py`, `reproduce-before-env-fix.py`). 필요한 회귀는 default/direct-auto의 모든 cloud flags0, 명시 cloud 선택에서만 해당 suite opt-in 활성화와 explicit local config/key 입력을 확인하는 것이다. 현재 outer helper의 cloud flags0 보완을 읽었고 최종 ZIP/direct-auto 회귀 및 고정 artifact 검증 후 해결 여부를 판정한다.

추가 문서 재현 경로 보완: Go package scan 제외를 위해 fixture를 `_fixtures/`로 이동한 뒤 README5단계가 아직 root의 `reproduce-independent-unacked.ps1`를 가리킨 초안을 읽고 P3 경로 불일치를 보고했다. 현재 파일 위치와 안내 명령을 일치시키면 production 변경 없이 해결된다. 구현자가 별도로 발견한 Go mixed package fixture placement와 portable validator PYTHONPATH 보완도 원본 실패/수정 후 실행을 구분해 최종 게시 감사에 포함해야 한다.

PUB-NC-1 수정 후 독립 회귀: `check-noncloud-env-regression.py`가 최종 outer helper SHA `ab966aa81c4ad7f29cff3b914c12d26e89545cf46fd00a68864c1e98bda0f161` 및 portable runner SHA `6d0e35cb0a86488e640a674c79ed358b1309eff256c4a462803276094fa90897`로 5케이스를 검증했다. outer default/explicit-cloud 선택의 사전 환경에서는10flag 모두0, 직접 portable auto에서도 Go child10flag0, explicit cloud runner만 M1/VMAccess/app/residual4flag1(나머지6flag0), 필요한 cloud input 누락 시 child0이다. outer main의 실제 child env 작성이 검증한 함수를 사용하는 AST wiring도 확인했다. `noncloud-env-after-fix/regression.json` 원본에 있으며 모든 subprocess는 recording stub으로 실제 Go/cloud 실행0이다. 이 범위에서 P2 helper 수정은 수용한다. 최초 import가 Evidence에 임시 Python bytecode를 생성하는 부수효과가 있어 테스트 스크립트를 bytecode 생성 금지로 보완했고 생성 cache만 게시 전 정리하도록 구현자에게 알렸다. production/test/Evidence source 바이트 변경은 없었다.
