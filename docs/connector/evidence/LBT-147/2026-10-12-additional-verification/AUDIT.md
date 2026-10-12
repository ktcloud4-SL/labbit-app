# LBT-147 전체 변경 추가 pre-merge audit

2026-10-12 KST. 사용자 PRE_MERGE_AUDIT.md, repo AGENTS/CONTRIBUTING/TESTING을 적용했다. 검토 대상은 PR84 전체 VM Access 구현, 기존 리뷰 R1/R2/R3, 이번 Data/SSH backpressure 수정과 승인된 tracing 테스트 수정이다. 사용자 승인 범위는 feature branch commit/push·기존 PR 업데이트까지다. main push/PR merge·배포·리뷰 댓글/Jira 쓰기는 하지 않는다.

## 기준과 원문

저장소 ktcloud4-SL/labbit-app, 브랜치 feat/LBT-147-vm-access. 최신 main은 fetch한 `7aaa5ca4e7f58cb88f188f91e653a624e4caed03`; feature에 main을 통합한 HEAD는 `a5b2d1210c3941641dc96dfc4aa2201358e9293a`이다. main 통합은 feature에서 수행했으며 PR을 main에 merge한 것이 아니다. 최종 코드 커밋과 실행 source bytes/Git blobs·전체 파일은 source-manifest/file-review에 고정한다. 미커밋 실행은 각 실행 직전 source snapshot을 기록했고 후속 생산 코드 identity를 대조했다.

최신 main의 Go/OTel·Web dependency update(PR82/83)와 tracing runtime/test가 들어와 전체 자동 검증을 다시 실행했다. 직접 SSH/WSS/OpenStack 경계의 Gorilla1.5.3, x/crypto0.57.0, Gophercloud2.15.0은 동일하다. cloud4 suite를 da6에서 실행한 뒤 Terminal Close/Resize 및 tracing test만 바뀌었다. Provider integration/lifecycle와 cloud4 테스트의 production logic은 불변이지만 dependency/main 차이가 있으므로 da6의 결과를 최신 소스 실행으로 바꾸지 않는다. 최신 소스에서는 영향을 받는 실제 VM/production PTY/runControl/idle 경계를 새로 실행했다.

Jira LBT-147 최신 본문/AC를 직접 확인했다(updated2026-10-12T11:00:05+0900). D-18 v5(page7241730), D-20 v7(page6881283), D-21 v10(page6848516), 테스트 전략 v3(page9666637), MVP v19(page12845071)를 다시 직접 읽었다. 145·148/149/150 및 상위21 경계를 독립 reviewer도 확인했다. raw 원문 snapshot은 ZIP/root/latest-ssot.json과 reviewer 기록에 있다. HTTP OpenAPI0.1.5, Connector/Realtime 8 JSONSchema, runtime0.1.7·idle1m, DB migration metadata와 관련 consumer를 대조했다. 새 wire/enum/schema/DB/제품 timeout 정책은 추가하지 않았다.

## 수정 전 findings와 해결

위치는 수정 전 a5b2+Data 수정 직전 또는 da6 코드 기준이다. 사용자에게 심각도·위치·재현·영향·수정/회귀를 보고한 후 승인된 범위에서 수정했다. 초기 실패·red/green 원본은 ZIP에 보존한다.

| ID·심각도 | 위치·재현·근거 | 영향·수정·필요 회귀와 실행 |
| --- | --- | --- |
| F-SLOW-CLOSE / P1 | terminal/session.go:386–397. unread 실제 TCP Data peer에서 PTY output4KiB writer가 writeMu를 보유한 채 block, Shutdown750ms3회 실패 | Close가 writer 잠금을 기다려 socket을 못 닫고 PTY·Control ENDED·VMAccess.Close·Control.Close까지 막음. writeMu TryLock 실패 시 concurrent socket Close; final ENDED를 쓰는 경우만1초 deadline. 실제 loopback+production runControl 및 실제 VM unread Data 회귀 PASS |
| F-SLOW-REPLACE / P2 | session.go:265,333. unread writer 상태의 Attach/Detach750ms 실패; Close-only 수정에도 두 분기 실패 | DataConn 교체/분리 후 old writer와 작업이 남음. Gorilla가 허용하는 concurrent Close로 이전 socket 해제. replace/detach release, 새 peer output·PTY 생존·grace내 동일 session resume 회귀 PASS |
| F-SSH-CLOSE / P1 | ssh_pty.go:188–194. shell ACK 뒤 SSH TCP peer read중단,16MiB synthetic input block, PTY.Close750ms3회 실패. 첫 race run은 x/crypto channel.go:624 CloseWrite vs258 WriteExtended race | stdin.Close/session.Close에서 대기해 관리 SSH 종료까지 도달 못함. managed PTY는 manager channel-close callback에만 위임(공유 transport 전체를 채널 종료로 닫지 않음); standalone은 자신의 client socket을 먼저 닫고 stdin.Close 생략. wrapper managed/standalone 및 실제 production VMAccessManager→TerminalShutdown→AccessClose의 input/resize 회귀 PASS |
| F-SSH-RESIZE / P1 | session.go:447–457, ssh_pty.go:165–173. WindowChange가 unread TCP write에 대기하며 state mutex 보유; Shutdown750ms3회 실패 | Close가 상태 잠금을 얻지 못함. 두 Resize 모두 state snapshot 후 network 호출을 mutex 밖으로 이동. 별도 resizeMu는 요청 순서를 직렬화하고 Close는 획득하지 않음. 실제 TCP pending Resize에서 종료·input/resize 해제·server EOF·manager 신규 open 거절 검증 |
| F-TRACE-ASSERT / P2(test) | tracing/runtime_test.go:648; helpers_test.go:254–261 context budget이 elapsed measurement보다 먼저 시작. 실제 context deadline exceeded, elapsed299.7157ms vsbudget300ms에서 전체 Go false FAIL | 사용자가 별도 승인한 테스트 파일만 변경. errors.Is(DeadlineExceeded)와 기존budget+700ms 상한·export_timeout 진단 assertion 유지, 잘못된 elapsed 하한 삭제. 외부 candidate race20회 및 최종 전체/반복 PASS. runtime timeout/budget 변경 없음 |
| F-TEST-METADATA / P3 | 신규 ssh_manager_backpressure_test.go 마지막 log가 red 후 강제정리도 정상 종료 성공처럼 표현 | test PASS/FAIL action이 판정임을 유지하고 normalShutdownReturned/forcedFixtureCleanup을 구분. 동일 최종 테스트 소스로 전체/반복 재실행 |

기존 리뷰 R1/P1(shared B cancel이 A retire), R2/P2(운영 장애를 INVALID_SESSION 오분류), R3/P2(Control이 먼저 닫혀 ENDED 누락)는 이전 review-followup audit의 red/green을 보존하고 이번 전체 diff·자동·실제 VM 테스트에서 다시 확인했다. R1 contextless operation1개/cleanup worker1개 제한과 late result 소유권, R2 safe 분류, R3 admission→Terminal→VMAccess→Control 순서를 유지한다.

## 전체 LBT-147 AC·SSOT 추적

| 요구사항/원문·버전 | 구현·현재 라인 근거 | 자동 테스트 실제 실행 | 실제 실행·Evidence·공백 판정 |
| --- | --- | --- | --- |
| [147](https://samsunglions.atlassian.net/browse/LBT-147) AC1 실제 Management SSH 생성/종료; [D18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5 | vmaccess OpenSession/dialSSH/Close, OpenStack ResolveVMAccess/Server pin, app buildConnector; file-review 함수 라인 | auth/hostkey/privatekey/handshake timeout·cancel·production wiring PASS | 과거 pinned 실제 VM IO·새 gen2 + 이번 실제2VM authenticated SSH/manager shutdown/idle PASS. 실제 key rotation/MITM 미실행 |
| 147 AC2 LabInstance/generation/VM key/Server affinity·stale 사전 차단; D18 | Provider metadata/project/NIC/pin 검증, Manager watermark 재검사, Control authenticated envelope→Target | mismatch/managementOnly/stale·delayed resolution/setup·raw error redaction PASS | 실제 target wronggeneration/wrongVM 및 실제 nonexistent Server404 PASS. 실제 Browser/SaaS authorization 전경로는148/C2 |
| 147 AC3 동일VM reuse·독립 PTY/SFTP·idle·실패 정리; D18/D20 | entries/reservations/channelOperation/closeChannels, managed PTY close, runtime idle1m | share/concurrency/independent close·R1 open/PTY/SFTP cancel6·late reuse/24waiter·unacked fixture·신규 real manager unread input/resize race PASS | bd8071d 실제VM: PTY close 뒤 같은 공유SSH의 SFTP INIT/VERSION/REALPATH, idle 전후 SSH_CONNECTION identity 변화, gen2 새target/stale1거절 PASS. 실제2VM 각각30 open/exec/close+idle후인증, healthyA vs10 exec cancel/deadline PASS. 부하시험 자체는identity미비교. 실제 packet pty-req stall은미주입 |
| 147 AC3 Reset/Cleanup·generation/provenance; [D20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) v7 | accessLifecycleProvider partial result fence, lazyProvider owner/waiter/cancel cache, Provider exact-ID cleanup | partialfail/preflight/late publish·UNKNOWN/reconcile·idempotency·cleanup 순서 PASS | da6 M3 Provision→Reset→Reconcile→Cleanup 및 Control WSS full lifecycle PASS, 소유 exact-ID 재조회/잔여0. KTCloud gen2/Tier2 실제부재는145에 미완료 |
| 147 AC4 PTY/SFTP boundary 준비,148/149/150 분리 | NewManagedSSHPTY/Setup, Terminal/Control production runtime integration | healthy IO/resize/grace/ENDED privacy, process 종료once·admission·ordinary reconnect0hook, blocked Data/SSH closure PASS | 실제 VM productionrunControl healthy/unread Data·ControlENDED1회 PASS; SaaSControl/Data peer는Mock. 실제 Browser CC02/productionSFTPFileWSS는 후속 |
| [D21](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516) v10 grace60s/reconnect/historyAvailable=false·내용 비저장 | Session/Data client, 기존 Relay consumer, privacy/error handling | replace/detach-reattach의PTYPreserving/newPeeroutput/resume, 기존 terminal/web consumer PASS | unread TCP 회귀는loopback; 배포 routing/Live/Browser 재접속 미실행. 프로세스 소실 뒤 shell복구/단절peerENDED 보장 없음 |
| 안전 오류·runtime·teststrategyv3/MVPv19 | existing ATTACH_FAILED/INVALID_SESSION/ENDED, runtime0.1.7, HTTP/WSS/schema/db owningSSOT | R2 Adapter→Manager→Control17, contractsOpenAPI/8schemas/runtime/migration·Reset11, 전체Go/Web PASS | 실제 ownedtarget+scopedNova/Neutron503 각1회, host pin hostile signer 거절/정상복구 PASS. scoped fault를 자연 outage로 주장하지 않음 |

## 실행·skip·실환경 구분

정확한 명령·시간·exit code·JSON pass/fail/skip 집계는 validation-summary.json과 ZIP이다. test count는 subtest와 반복 기록을 포함하며 서로 다른 test 수로 확대하지 않는다.

- 최종 소스의 gofmt/mod download/vet/unit/build, 관련6패키지 race(VMAccess/Provider/App/Terminal/WSS/tracing), 선택 회귀20회 실행. 전체 저장소 race라고 주장하지 않는다.
- Web13파일163tests/typecheck/lint/build 및 fresh npm ci PASS. main dependency update 이후 수행했으며 이후 변경은Go Terminal/test뿐이다. 최종 package-lock/package.json blob identity 확인.
- 일반Go7 SKIP: cloud opt-in6(M1/M2Provision/M3Lifecycle/InternetPolicy/ControlWSS/VMAccess), Windows POSIX0600 assertion1. cloud opt-in은 별도 로컬 실행 기록으로 구분하며 SKIP를PASS로 계산하지 않는다. 최종 relatedrace opt-in6SKIP.
- 빠졌던 cloud4 suites는 da6 source에서 전부PASS(InternetPolicy parent+on/off 포함3). M2 tracked8, M3/Control각16, Internet ON8/OFF7 exact-ID 정리 및 보조 SG/keypair, 각 사후 inventory matching0. Internet OFF는 SSH 접속 실패를 정책 성공으로 처리하지 않는다.
- 최종 생산 코드의 실제2VM suite는12 parent/subtest PASS, child runControl healthy/unread3PASS, finalinventory1PASS. child를 별개 기능3개라고 이중 집계하지 않는다. tracked10 및 임시SG/keypair 정리, 사후matchingserver/network/subnet/SG/port/keypair/router 모두0. 최종 runs의 rawcleanup assertion와inventory를 함께 확인했다.
- 코드 커밋 bd8071d의 정상 actual VM integration1PASS(214.69s): gen1 authenticated shared SSH, strict wrong target, PTY IO/resize, PTY close 후 독립 SFTP REALPATH, idle SSH_CONNECTION identity change, gen2 Reset/fresh target/stale1 거절. generations2 tracked14 exact-ID absence와 final inventory matching0을 함께 확인했다.
- root 실제2VM R1은 remote sleep exec Wait cancel/deadline이며 unacknowledged pty-req packet 검증이 아니다. R2는 실제Provider target에 scopedHTTP503을주입했다. 실제SSH pin에 synthetic hostile signer를제시했으며 실제keyrotation은아니다. runControl은productioncode/realProvider/VM/SSH/DataWSS, SaaSControl/Data peer만Mock. actualVM child의remoteEOF는미관측; loopback actualManager fixture는serverEOF를관측한다.
- PostgreSQL build-tag integration은 로컬 미실행(DB/SQL 변경 없음). 최종 원격 HEAD의 GoCI PostgreSQL16 integration 결과를 push후 별도로 확인한다. 이전 da6 CI PASS로 새 코드의 원격CI를 대신하지 않는다.

초기 실패를 보존했다: 실제fixture 첫run의childcwd 오류와healthySSH readiness/setup budget 부족, mainCRLF gofmt 진단, tracingtest false fail, 독립SSH/Data red 및 initialcandidate fixture cleanup오류. 실제fixture실패 뒤에도 소유자원정리와 matching0을 확인했다. Fixture budget/readiness 수정은 production을 변경하지 않았으며 최종 실제VM은 명시적pinnedSSH ready 성공 후 시험했다. 실패한 fixture에서 정상 Provider 장애를 입증했다고 주장하지 않는다.

## 실패·보안·수명주기 판단

| 시나리오 | 분석·검증·남은 위험 |
| --- | --- |
| sharing/cancel/late result | B cancel이Atransport를retire하지않음. transport당pendingoperation1+cleanupworker1; 늦은channelresult소유권·no-useridle·Invalidate/Close로정리. activeA가유지되고peer가응답하지않으면pending도transport수명동안남고새open/setup은대기/timeout할수있음 |
| networkbackpressure/cleanup ownership | DataClose는writermutex대기없고finalframe만1초deadline. SSHmanagedClose는reservation만release하고rawchannelworker는manager소유. idle와runtimeVMAccess.Close가socket을닫아blockedworker해제. 채널종료만으로healthy공유client를닫지않음. standalone은ownclientClose; stdinClose/write race제거 |
| resize/close/reconnect | resizeMu로요청직렬, stateMutex는snapshot만, Close는resizeMu미획득. old reader가 blocked된 동안 새attachment가올수있지만shutdownstate획득가능. queuedResize는closed재검사. 모든스케줄 증명아님 |
| ENDED/deadlines | healthyControl/Data exact-once·reason계약유지. unreadDatafinalframe은best effort. 1초는각sessionfinalDataframe한정이며전체Nsessionshutdown1초보장아님. Controlsender기존10초writebudget·Nsession순차종료·상대단절의알림전달제한유지 |
| stricttarget/generation/partial mutation | resolverownership+managerwatermark재검사,partialReset/Cleanupresourceoutcome fence 및latepublish거절. UNKNOWN은기존reconcile경계를사용하고blindmutationretry추가없음. generationReuse/서로다른Lab격리자동회귀 |
| security/privacy | production에서localkey+existingServerpin failclosed. rawSSH/Provider주소·secret을safeerror로노출하지않음. live는readonly. Terminal/SFTPcontent·transcript telemetry저장추가없음. 테스트syntheticinput은메모리에서만처리; stack은메모리관측후clear하고Evidence에덤프하지않음(초기race진단의코드stack은보존) |
| resources/external consistency | 테스트가소유exactID만정리하고사후재조회; prefix조회는보조확인으로만사용. KT145의API404/NSM ACTIVE모순은실제부재미확인으로유지. 로컬PASS로KT불일치를해결했다고하지않음 |
| restart/durable/contract/DB | 기존operation/generationstate·reconcile계약을우회하지않음. hardkill/재기동후PTY재생/복구미검증. 해당PR의DB/HTTP/새wire/배포정책변경없어새DB회귀추가불필요;원격기존integration확인. productssot외timeout정책추가없음 |
| ignored/helper/reproduction | credential/key/config는외부입력,필수fixture는hidden _fixtures로Go./...에서제외. prepare_overlays는입력검증·hash확인·외부overlay생성만하고cloud/Go호출0. outputrepo내/비어있지않음거절. pinnedsource대조는명시옵션;새코드변경시원본을덮어쓰지않음 |

## 독립 검토·잔여 범위·판정

구현과 분리된 independent_review가 최신SSOT8자료, 기존전체PR diff77파일 및 새Terminal/tracingtest/producer-consumer를 재검토했다. 별도로Datareplace/detach/FinalFrame/RunControl 및 SSHwrapper·actualManagerTCP red/green을 작성해 blockednetwork/mutex/channelownership을 확인했다. 최종 보고는 independent-review.md; 게시artifact와portablehelper는 추가publication검토에 포함한다. 미검토 diff파일은 file-review에 명시적으로 제외하지 않은 한 없으며 unknownsecret/모든concurrency/모든Git history는검토했다고주장하지않는다.

남은 범위는 README/validation-summary/PR본문에 명시한다. KTCloud145 gen2Reset/Tier2실제부재, 배포Browser/SaaSCC02 및routing/reconnect/Live(148/C2), productionSFTP/FileWSS(149/150), 자연provideroutage/실제keyrotation/packetloss/hardrestart/longsoak/대규모load, 실제VMSSHpacket-stallfault, actualVMrunControlchildserverEOF미관측이다. 이 항목들은 이번PR 범위의Local VMAccess 구현검증과 구분되며 테스트가환경의다른부분에막혀기본cloud4를생략한상태가아니다.

최종 판정은 execution source identity·전체자동검증·해당실환경·독립review·publicationchecks를 함께 확인한 publication-checks.json에 기록한다. 감사 통과는 CODEOWNER 승인/merge/배포 또는 더 큰 LBT-145/148–150 작업 전체 완료가 아니다.
