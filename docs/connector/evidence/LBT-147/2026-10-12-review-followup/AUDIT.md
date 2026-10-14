# LBT-147 PR #84 review follow-up — 최종 감사

2026-10-12 KST. 사용자 PRE_MERGE_AUDIT.md와 repository AGENTS.md / CONTRIBUTING.md / TESTING.md를 적용했다. 범위는 **기존 LBT-147 전체 PR diff + 이번 CODEOWNER 리뷰 R1/R2/R3 수정**이다. 이번 변경 권한은 VM Access, Provider 오류 분류 및 작동에 필요한 Control/Terminal 종료 경계와 회귀 테스트에 한정했다. 다른 제품 코드는 변경하지 않았다. 초기 검증 단계에서는 commit/push/PR 수정/merge/Jira·리뷰 댓글 전송을 하지 않았다. 이후 사용자의 최신화·재검증·수정 업로드 승인에 따라 feature branch commit/push 및 PR 본문 업데이트를 수행한다. main push/merge·Jira/댓글 전송은 하지 않는다.

## 검토 소스와 원문

| 항목 | 값 |
| --- | --- |
| 저장소 / 브랜치 | ktcloud4-SL/labbit-app / feat/LBT-147-vm-access |
| PR | https://github.com/ktcloud4-SL/labbit-app/pull/84 |
| CODEOWNER review | https://github.com/ktcloud4-SL/labbit-app/pull/84#pullrequestreview-5486377783 ; CHANGES_REQUESTED |
| HEAD | ed7843e25be99f414bfb0087d2f07b8121fe08a0 |
| 최신 origin/main | 이번 turn에서 fetch: e850567066134edc87b2ba543d0eb42a21f406b8 |
| merge-base | 6d675c74bff11f481d247267faefd4ce960dcd38 |
| 실제 실행 소스 | HEAD + 미커밋 11파일(5 production, 6 tests). source-manifest.json의 54파일 byte SHA256/Git blob으로 고정 |
| 전체 diff | 원래 PR diff와 local/untracked의 합집합 54파일: 코드·설정·테스트27, 이전 Evidence27. 새 감사 패키지는 별도 publication 검토 |
| 최신 main 영향 | PR81의 frontend/deployment-handoff 문서. VM Access production/계약 변경 없음. main merge/rebase는 하지 않음 |
| 최신 원문 | Jira LBT-147/21/145 본문·AC·댓글 직접 재조회. D-18 v5, D-20 v7, D-21 v10, MVP v19, 테스트 전략 v3. ssot-summary.json 및 archive 원문 snapshot |
| owning Git SSOT | HTTP OpenAPI 0.1.5, Connector/Realtime 8 JSON Schema, runtime 0.1.7. 이번 수정에서 계약·dependency·DB migration 변경 없음 |
| 환경 | Windows amd64 / Go1.27.1 / race+CGO / Node24.19.0 / pnpm / Python 및 CI validator 버전. 로컬 OpenStack labbit-test, Ubuntu / m1.small / sharednet1 |

**HEAD의 이전 PASS를 이번 수정의 PASS로 재사용하지 않았다.** 새로운 자동·실환경 실행은 54파일 snapshot을 찍은 후 실행했으며 실행 종료 후 모든 byte SHA256을 재대조했다. 과거 2026-10-10 및 2026-10-12 Evidence 27파일과 archive는 변경 없이 보존한다. 과거 감사가 Setup 취소에 의한 공유 SSH 종료를 허용한 설명은 R1 재현으로 잘못된 판정이 확인됐으며 이 감사의 채널 격리 분석으로 대체된다. 과거 raw 결과 자체를 현재 소스의 결과로 바꾸지 않는다.

## 수정 전에 보고한 findings와 수정 결과

라인은 리뷰 당시 ed7843e 기준이다. 사용자에게 재현 조건·영향·회귀 테스트를 보고한 후 수정했다. raw before 로그를 함께 보존한다.

| ID / 심각도 | 위치·재현 | 영향·수정·회귀 |
| --- | --- | --- |
| R1 / P1 | vmaccess/access.go:164,280. A의 정상 PTY를 둔 채 B의 open / PTY / SFTP request가 응답하지 않을 때 B timeout/cancel | 이전 구현은 공유 SSH를 retire하여 A echo가 EOF. D-18의 독립 채널에 위배. B 취소는 B만 해제하고 contextless SSH 작업은 transport당 1개로 제한. late result는 별도 소유권으로 닫고 cleanup worker도 transport당 1개. timeout/cancel 6분기, late accept/reuse, 24 canceled waiters, peer가 B close ACK를 무시하는 독립 fixture 검증 |
| R2 / P2 | provider/openstack/terminal_target.go:35 및 vmaccess resolver 분류. Nova/Neutron 503/401, malformed 응답, 설정/취소 실패 | 이전 구현은 INVALID_SESSION으로 잘못 분류. 명시적 stale/target/ownership/missing server만 INVALID_SESSION. 운영 장애·BUILD/no IP·host pin/config 오류는 safe ATTACH_FAILED. Adapter→Manager→Control 17분기와 독립 503/ownership fixture, raw marker·주소 비노출 확인 |
| R3 / P2 | app/app.go:250–252 및 wss/reconnect.go의 Run 종료 순서. 활성 세션 중 runControl context 취소 | SSH가 먼저 종료되면 ENDED reason 경쟁. Control sender도 먼저 닫혀 ENDED 누락되는 것을 실제 loopback WSS/SSH로 재현. once shutdown: Terminal admission 차단 → SERVICE_RESTARTING 종료·알림 → SSH retire → Control close. Control/Data 각각 정확히 1회 ENDED, 늦은 open 차단, ordinary reconnect에서는 shutdown 미실행 검증 |

새 회귀를 이전 production에 적용한 red-run이 실패했고 수정 후 green-run이 통과했다. R3 Data의 잘못된 reason은 모든 스케줄에서 재현된 것은 아니며 Control ENDED 누락이 red-run에서 확인됐다. 초기 green 실행의 app sync import build 실패와 validator 설치 전 환경 실패, 독립 dependency overlay의 Go cache 경로 거절도 원본에 남겼다. 이를 숨기거나 production 결함 재현으로 확대하지 않았다.

## LBT-147 전체 요구사항 추적

원문 URL·버전·본문 SHA는 ssot-summary.json. 파일·함수의 현재 라인은 file-review.md에 기록한다. `auto`는 새 Go 전체/race 실행, `real-VM`과 `real-app`은 새 원본 로그를 뜻한다. 이번 세 가지 지적만 감사한 것이 아니다.

| 요구사항·AC / SSOT | 구현 | 실제 실행한 자동 테스트·분기 | 실제 검증 Evidence / 판정·공백 |
| --- | --- | --- | --- |
| 147 AC1: 실제 VM Management SSH 생성·종료; D-18 직접 접속·로컬 key/Server pin | vmaccess OpenSession/dialSSH/Close; terminal_target ResolveVMAccess/SSHHostKeyCallback; app buildConnector | authentication/hostkey/privatekey fail-closed, runtime production wiring, handshake cancel/timeouts 모두 auto PASS | real-VM: gen1 ACTIVE/authenticated SSH 및 gen2 새 target PASS. 실제 hostkey 공격/키 회전 장애는 미실행 |
| 147 AC2: LabInstance/generation/VM key/Server affinity, stale 사전 차단; D-18 | terminal_target metadata/project/name/NIC 검증; Manager generation watermark; WSS envelope→Target | mismatch4+ManagementOnly/ServerPin, reset during resolve/setup, 다른 Lab 격리 auto PASS | real-VM: wrong Lab/gen/VM SSH 전 거절, Reset gen2 후 gen1 거절 PASS. SaaS authorization 전체 E2E는 후속 |
| 147 AC3: VM당 공유 SSH·독립 PTY/SFTP·no-channel idle; D-18 | Manager entries/reservations/channelOperation/closeChannels; runtime idle 설정 | share12/concurrency, independent close, R1 6취소·late result·24 waiter, 독립 close-ACK stall auto/race PASS | real-VM: 실제 PTY input/output/resize + SFTP INIT/VERSION/REALPATH, 같은 transport, idle 종료 PASS. SFTP production read/write 및 File WSS 전체는 149/150 |
| 147 AC3: 실패·취소·disconnect 정리; D-20 lifecycle/generation fencing | Manager invalidate/retire; app accessLifecycleProvider; lazyProvider owner/waiter/cache | failed lifecycle/preflight/late publish, lazy pending deadline/cancel/failure retry/cache, disconnect safe errors auto PASS | real-app: 실제 Keystone 인증 응답 지연+waiter cancel/retrycache, 실제 VM2개·부분 Reset/Cleanup·새 gen2 생성 실패 PASS. 3 DELETE409+1 POST409는 scoped proxy fault 주입임을 명시 |
| 147 AC3: 생성 자원 provenance·정리; D-20 | Provider result/ResourceResult 소비·세대 fence, exact tracked resource teardown | lifecycle Adapter HTTP 부분 삭제/결과별 retire·preflight 보존 PASS | real-VM 14개, real-app16개 distinct tracked resource exact-ID ABSENT. tempSG/keypair ABSENT; post-read inventory matchingTestResidualTotal=0. 접두사 일괄 삭제 안 함 |
| 147 AC4: PTY/SFTP 경계 사용 준비; 21 common helper, 148/149/150 dependency | NewManagedSSHPTY/Setup/Session.Close; WSS handler runtime wiring | 기존 echo/resize/grace/ended/redaction + 새 R3 Control/Data shutdown/late admission/reconnect PASS | 실제 SSH PTY/SFTP 채널 정상 경로 PASS; Control/Data shutdown은 Mock SaaS/Relay + authenticated loopback SSH. 실제 Browser↔배포 SaaS↔Connector↔VM CC02는 148/22 후속 |
| D-21: 재접속·60초 grace·historyAvailable=false·내용 비저장 | 기존 Terminal Session/Data client 및 Control consumer, 변경 없음 | 해당 기존 재접속/grace/privacy/wire 계약 전체 auto 및 web tests PASS | 이번 배포 E2E/Live host routing/과거 내용 재생 검증 아님. SSH/process 종료 시 기존 shell 복구 불가라는 SSOT 한계 유지 |
| Connector/Realtime error contract, Runtime idle 1m; owning SSOT | 안전한 ErrInvalidTarget/ErrUnavailable → existing wire enums; idle 양수 duration | R2 17분기; ENDED/redaction; OpenAPI·8 schema·runtime/migration metadata·Reset schema11 PASS | enum/필드/프로토콜·스키마·migration 신규 변경 없음. PostgreSQL integration 미실행(DB diff 없음) |

## 새 실행 결과와 분리된 검증 범위

| 실행 | 결과 | 범위·원본 |
| --- | --- | --- |
| Go fmt/vet/test/build | PASS. 2856 test/subtest PASS;31 package PASS;9 no-test package;7 test SKIP | audit-logs.zip go-unit.log / go-vet / go-build / gofmt |
| 관련5 package race | PASS.392 test/subtest PASS;6 opt-in test SKIP | go-race.log. 전체 저장소 race 실행으로 확대하지 않음 |
| 새 회귀 -race -count=20 | PASS.600 test/subtest records; 실패0 | regression-repeat.log. 20회×30 결과이며600개의 별도 테스트라는 뜻 아님 |
| Web | typecheck/lint/test/build PASS;13 test files /163 tests | web-*.log. build의 기존 dependency/browser-data warning은 stderr 원본 보존 |
| 계약 | OpenAPI,8JSONschema,runtime/migration metadata,Reset schema11 PASS | contracts.log/reset-schema.stderr.txt. CI와 동일 Python validator 버전의 외부환경 사용 |
| 실제 로컬 OpenStack | pre-discovery PASS;real-VM217.25s PASS;real-app231.06s PASS;post-discovery/residual PASS | real-*.log / post-discovery-and-residual.log / cloud-exit-codes.json. 각 -race·-count=1 실행 |
| 독립 reviewer 재현 | old R1/R2/R3 red, current R1/R2/R3 green 및 unacked B-close fixture -race count5 PASS | independent-*.jsonl + portable-unacked-run. reviewer는 production/test 소스를 수정하지 않음 |

Go counts는 JSON pass action을 집계하여 subtest를 포함한다. 7 skips는 M1/M2Control/M2Provision/M3Lifecycle/InternetPolicy/VMAccess opt-in6개와 Windows POSIX0600 check1개다. opt-in M1/VMAccess는 별도 실제 실행으로 PASS; 나머지4개 standalone cloud suite는 이번에 미실행이다. related race의6 skip도 같은 opt-in 조건이다. PostgreSQL build-tag integration은 일반 Go suite에 포함되지 않으며 이번 실행하지 않았다. PR84의 기존 CI PASS는 ed7843e까지만 검증하며 local11 변경에는 새 CI가 없다.

## 실패·보안·수명주기 분석

| 시나리오 | 검토·근거·남은 한계 |
| --- | --- |
| invalid target vs operational error | 명시적 ownership/stale/server404만 invalid; lookup/auth/malformed/config/context는 unavailable. context errors.Is 유지, raw Provider 응답/VM IP를 Control/Data에 노출하지 않음 |
| cancel/open/late accept | canceled caller가 먼저 반환해도 worker가 result 소유. unbuffered ownership transfer로 late session 유실 방지. semaphore를 실제 operation 종료까지 보유하여 요청 수만큼 goroutine/SSH open을 추가하지 않음 |
| 하나의 peer request가 영원히 응답하지 않음 | active A의 I/O는 유지. transport당 pending contextless open/setup1개와 cleanup worker1개. **동일 VM의 새로운 open/setup은 pending 해소 또는 transport 종료까지 대기/timeout**. no-user idle/Invalidate/Manager.Close로 해제. active channel이 계속 있으면 pending operation도 transport 수명만큼 유지될 수 있음. 이를 즉각 완전 회수로 주장하지 않음 |
| channel Close write/ack stall | reference release·caller cancel은 blocking raw Close와 분리. 독립 test-only dependency fault로 B의 closeACK 무시 및 idle retirement/new transport 복구 확인. 공용 GOMODCACHE/production dependency 수정 없음. 실제 OpenStack에서 이 fault 주입 안 함 |
| generation/reset/late resolver | watermark 재검사·retirement notification·preflight 보존·partial results fencing. real scoped failure + unit races PASS. unknown external outcome는 기존 owning protocol/Reconcile 사용; 새 mutation retry 정책 없음 |
| shutdown/open/ENDED | admission을 먼저 영구 차단, snapshot 외부에서 close, once reason·알림 후 shared transport 종료. ordinary Control reconnect와 process cancellation 분리. delayed existing/new ID 거절 및 factory 호출수 검증 |
| blocked Data WSS / unusable Control / hard process kill | healthy fixture의 정확한 종료 reason/count만 검증. 기존 Data Close WriteJSON에는 write deadline이 없어 backpressure 시 shutdown이 지연될 수 있고 단절된 Control/Data에 ENDED 전달 보장 불가. 이번에 이 경로를 실환경 검증/수정하지 않았으며 별도 허락 없이 다른 Terminal 네트워크 정책을 바꾸지 않음 |
| input / ID reuse / privacy | 기존 envelope/schema/Terminal dimension/oversize/session collision/grace 검증 유지. 이번 안전 오류와 shutdown tombstone이 기존 계약을 우회하지 않음. 실제 key/credential은 로컬 읽기만, Terminal/SFTP 내용 영속 저장·telemetry 수집 추가 없음 |
| ignored dependencies / fixtures | 로컬 cloud config/key/known_hosts는 필요 입력, Evidence에 복사하지 않음. 외부 `_test.go` overlay/hashes·runner·독립 fault reproducer를 archive로 제공. node_modules/validator-libs는 설치 의존성, production 파일 아님 |

## 독립 리뷰·남은 범위·판정

구현과 분리된 independent_review가 최신 원문, 전체54파일 source/Evidence, producer/consumer와 실패 경계를 재검토했다. 별도 작성한 SSH/HTTP fault fixture로 이전 결함과 수정 후 결과를 확인했으며 추가 confirmed production finding이 없는지 최종 보고서에서 명시한다. 자세한 경로·검토방법·한계는 independent-review.md. 이 감사의 작성자가 수행한 source·archive·reproduce/secret guard 검증은 publication-checks.json으로 별도 기록한다.

미검증: KTCloud의 LBT-145 gen2 Reset/Tier2 Cleanup, 실제 Browser/배포 SaaS CC02 및 routing/reconnect(148/22/26/27), production SFTP/File WSS(149/150), 실제 VM의 신규 R1 stall/cancel·R2 API503·R3 process termination packet fault, hostkey 공격·키 회전·자연 provider outage·packet loss·hard restart·soak/load, 위의 blocked WSS 전달/종료 시간, 모든 가능한 concurrency schedule,4개 별도 opt-in cloud suite 및 PostgreSQL integration. Mock/fault fixture PASS를 이 범위의 실환경 PASS로 치환하지 않는다.

전체 LBT-147 VM Access PR 범위의 요구사항 추적·회귀·현재 소스 로컬 OpenStack 검증 및 독립 검토가 충족되면 **PR 리뷰 재요청 가능**으로 판정한다. 더 큰 21/22/145/148–151 작업 전체 완료 또는 배포 환경 승인이라는 뜻은 아니다. 최종 publication 검증과 독립 보고를 함께 제공한다. Audit 통과는 push/PR/merge 승인으로 해석하지 않는다.

## 최신화·커밋 및 게시 보조 도구 검토

사용자의 추가 요청 후 origin/main을 재-fetch했고 e850567로 불변이다. Jira3본문/댓글과 Confluence5body/version 및 동일4 review thread를 직접 다시 읽어 불변임을 확인했다. 이후 Go 전체/vet/build/race/20회, Web4항목, contract/Reset11을 실제 다시 실행하여 PASS했다. 원본은 ZIP latest-check/, metadata와 counts는 validation-summary.json latestVerification. Production 변경은 없으며 실제 cloud 실행 후54파일 byte identity는 여전히 일치한다. 실행 byte/Git blob54개 모두 code commit b191ecdbe87f28c2c2e34d28877f5de84b18f082와 일치한다. 새 Evidence 커밋에는 검증 문서/로그/재현 helper만 포함한다.

게시 helper finding은 수정 전에 별도 보고했다. P2 root Go fixture 혼합은 `_fixtures` 이동으로 Go 탐색에서 제외했고 전체Go재실행PASS, validator-path 준비 실패는 기존PYTHONPATH 보존으로 수정해Reset11PASS. 독립 reviewer의 P2 inheritedcloudflags 문제는 reproduce.py와 direct portable auto 양쪽에서10flag0으로 수정했다. stub5조건 regression으로 Go/cloud 호출0·explicitcloudselected4flag만1·missinginputnochild를 확인했다. P3 이동 후README경로도 수정했다. initial failure/원본 before환경회귀 기록을 ZIP에 남겼다. Ignored Python bytecode/cache는 게시파일목록에서 제외한다.

재현 guard의 npm dependency Go example false-positive도 수정 전에 P2로 보고했다. `web/node_modules`만 실행 소스 검사에서 제외하고 다른 ignored Go 및 실제 consumer 변경 거절을 유지한다. final isolated clone8조건 PASS이며 실제 Go/cloud 호출0.

독립 PUB-IG-1/P2로 broad web/node_modules 예외가 visible Go package를 허용함이 확인됐다. 수정 전에 보고하고 예외를 Go가 탐색하지 않는 `.pnpm` hidden경로만으로 한정했다. hidden예제허용/visiblepackage거절을 포함한 final sourceguard9조건 PASS(실제 Go/cloud0), 별도 reviewer proof는 Go list로 실행가능성을 확인했다(Go test/cloud0). generatedPythoncache는 Evidence 내부 `.gitignore`로 게시대상에서 제외했다.

독립 PUB-EG-1/P2: Evidence prefix 전체 예외 때문에 visible Go가 source guard를 통과하고 실제 Go list 대상에 포함됨을 확인했다(reproduce.py 수정 전26/31행). 수정 전에 사용자에게 severity·위치·재현·영향·회귀를 보고하고, Evidence Go/go.mod/go.sum은 hidden `_fixtures`에 한정했다. artifact 검사는 목록 밖 추가/누락/변조를 거절하는 exactset 검사로 보완했다. 최종21조건(소스14·artifact7) PASS, 독립9조건 및 inheritedcloud5/pnpm2조건 PASS이며 Go test/cloud 호출0이다. 제품 코드54개 byte/Git blob은 불변이므로 자동/실환경 production 검증은 재실행하지 않았다. 최종 helper/회귀/독립 red-green 원본은 ZIP publication-final/에 보존하고 현재 root helper를 사용한다. 이전 guard8/9조건은 중간 검증 기록이며 최종 판정은 이 보완 결과를 포함한다.
