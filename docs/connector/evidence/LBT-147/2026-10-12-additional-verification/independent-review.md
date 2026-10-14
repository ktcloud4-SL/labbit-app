# LBT-147 추가 검증 독립 리뷰

기록 UTC: 2026-10-12T07:39:12.605184+00:00. 이 리뷰는 구현 agent와 분리된 reviewer가 수행했다. 저장소 source/test/Evidence 수정, 실제 Cloud mutation, 원격 push/PR/Jira/메시지 변경은 하지 않았다. 외부 overlay와 synthetic TCP/SSH/WSS fixture만 작성/실행했다. 개인 PRE_MERGE_AUDIT.md와 repo AGENTS/CONTRIBUTING/TESTING를 직접 읽고 적용했다.

## 1. 현재 판정과 소스

**코드/동작 검증 범위는 통과.** 새로 재현된 Data WSS/SSH Close/Resize 결함과 후속 Linux CI가 드러낸 brief-writer Data ENDED race는 수정 후 독립 회귀 및 최신 source actual VM/전체 자동 검증까지 충족했다. 검토 범위에서 미해결 confirmed product defect는 없다. Evidence publication은 별도 감사로 판정한다. 이 판정은 LBT-147 Jira 전체 완료나 KT/Browser/File acceptance를 뜻하지 않는다.

- repo: `C:\Users\user\Documents\ChatGPT\3차 2\tmp\labbit-vm-access-147`; branch feat/LBT-147-vm-access.
- PR 기준 최신 origin/main 및 merge base: `7aaa5ca4e7f58cb88f188f91e653a624e4caed03`. 실행 snapshot HEAD `a5b2d1210c3941641dc96dfc4aa2201358e9293a` + local; 최종 코드 HEAD `bd8071d86659f2cce5d2c6e77cbb7f7ac0054d05`로 아래 section7에서 byte mapping을 확정했다.
- 이전 published HEAD da6b6701f13ba17c38b0778983545da62ac07212, R1/R2/R3 code b191ecdbe87f28c2c2e34d28877f5de84b18f082. 이번 HEAD는 최신 main 통합 후이며 미커밋 변경은 별도 byte SHA로 식별한다.
- 전체 PR 변경 77파일 + local 14파일, 중복 제거 union 89파일. 정확 파일/role/byte SHA/Git blob은 `independent-current-source-manifest.json`.
- 최신 main e850→7aaa의6개 context 파일(go.mod/go.sum, tracing production/test, web package/package-lock)도 따로 검토했다. OTel/Prometheus/gRPC/xNet/web minor updates 포함; Gorilla1.5.3, x/crypto0.57, Gophercloud2.15 유지. Connector는 SDK/exporter를 초기화하지 않으며 기존 propagation consumer다. fresh npm lock install 및 frontend checks는 root 원본으로 확인한다.

## 2. 최신 SSOT 직접 확인과 추적성

Atlassian read-only 원문: Jira LBT-147/LBT-145/LBT-21 및 Confluence D18(page7241730), D21(6848516), D20(6881283), 테스트전략(9666637), MVP계획(12845071). 직접 읽은 body는 `independent-latest-ssot.json`/`independent-ssot-normalized.json`에 보존했다. connector structured read가 Confluence numeric version을 생략하므로 reviewer 직접 확인은 body이며, v5/v10/v7/v3/v19 version metadata는 root raw 최신 snapshot 근거다. 본문/AC 변화 없음. KT/LBT-145 선행 조건 미완료와 LBT-147 로컬 PR 범위를 분리한다.

| 요구사항/계약 | 구현 | 자동 검증/관찰 | 실제 Evidence 범위 | 공백/판정 |
| --- | --- | --- | --- | --- |
| LBT147 strict LabInstance/generation/logicalVM/Server affinity, 접속 전 stale/잘못된 대상 거절 | terminal_target.go ResolveVMAccess/resolveServerAddress; handler.go VMPTY factory; access.go generation | ownership/terminal_target/Control typed error tests, delayed resolve/setup invalidation | 기존 real VM gen1/gen2 및 추가 real affinity wronggen/wrongVM/unknownserver | 로컬 OpenStack 충족; KT 미검증 |
| management NIC private access, local user/key, 기존 server별 pin 재사용; 무검증 fallback 금지 | management network/project/device-owner/fixedIP validation; SSHHostKeyCallback existing knownhosts; production factory fail closed | ambiguity/wrongowner/pin/auth/privatekey safe failures | authenticated/pinned actual VM PTY, synthetic wrong-key actual callback rejection | hostile actual host-key rotation은 실행하지 않음 |
| VM별 lazy shared SSH, 독립 PTY/SFTP | access.go entries+users; NewManagedSSHPTY; raw SFTP Session boundary | simultaneous single handshake, A 유지+B cancel/close, SFTP independence | 이전 SSHClose source actual SSH_CONNECTION 비교/PTY close후SFTP REALPATH; 최신source2VM60cycle 및A/B취소20case | production File operations LBT149; load자체identity재비교없음. 최신Close delegation의realSFTP-after-PTY-close는현재코드normalVM test 재실행PASS(section7) |
| configurable no-channel idle close, PTY grace와 분리 | vm_access.go parser; runtime0.1.7 env; releaseLocked timer token | invalid/positive env, idle/open race, timer retirement | 현재코드actual no-channel idle 후 다른 SSH_CONNECTION(section7) | 모든 duration별 load는 아님 |
| auth/unreachable/handshake cancel/deadline/socket cleanup | dialSSH, safeError, endpoint resolver | handshake/auth/pin/context timer ordering redaction | 실제 auth/pin 정상; additional real shell sleep cancel/deadline 각10 | remote blackhole/auth rejection은 loopback fault; 실제 shell Wait cancel이 stalled PTY request ACK와 동일하지 않음 |
| canceled B 작업이 active A를 죽이지 않음(R1) | channelOperation capacity1, late result ownership, queued channel close | activeA echo after B PTY/SFTP/open timeout/cancel; no-close-ack dependency overlay | additional actual A IO after B sleep cancel/deadline20cases | 무응답 contextless SSH request worker1은 peer reply 또는 idle/invalidate/managerClose까지 유지 |
| resolver API503은 ATTACH_FAILED, invalid ownership404는 INVALID_SESSION(R2), raw error 외부 비노출 | typed ResolveVMAccess mapping + handler safe literal | HTTP provider fixtures + Control envelope assertions | actual SDK Nova/Neutron exact GET503 fault injection + actual nonmatching affinity | 503은 실제 cloud 장애 발생이 아니라 지정 HTTP response 주입 |
| preflight FAILED 보존, 부분 mutation/UNKNOWN/first destructive rejection은 old generation fence | accessLifecycleProvider/lifecycleInvalidatesAccess | first/partial/replacement FAILED; active channel EOF; delayed resolve/setup old reject; otherLab preserve | prior actual API proxy partial worker DELETE+workspace409, preflight missingflavor, replacement actual cleanup/Neutron+Nova409 | fault-injected failures를 native provider occurrence로 주장하지 않음 |
| R3 reason/once/admission 및 Terminal→shared SSH→current Control close | app.runControl shutdown once, Supervisor hook, SessionManager.Shutdown | accepted delayed open rejection; normal reconnect hook 미호출; healthy ENDED reason once | additional actual runControl healthy/unread(mock WSS + actual provider/SSH/PTY) | 실제 Browser/SaaS multi-process routing은 미검증 |
| blocked/unread Data WSS가 cleanup을 영구 지연하지 않음 | Session.Close TryLock / final1s deadline; Attach/Detach concurrent old Close | genuine unreadTCP red3→current green; finalFrame native deadline; recovery new actual WS output/resumed | actual VM synthetic output→mock unread Data WSS branch |1s는 final frame 내부 per-session budget; whole connector shutdown guarantee 아님 |
| blocked/unread SSH INPUT/Resize에서도 manager socket close까지 도달 | managed PTY Close callback delegation; legacy client first; separate resizeMu/state lock snapshot | original genuineTCP red3 + race; current actualManager INPUT/Resize green3, worker return/serverEOF/closed admission | 최종 source actual2VM+runtime healthy/unread run PASS; unread SSH peer 자체는 synthetic fault | 실제 VM SSH daemon blackhole를 만들지 않음 |
| D18/D21 Terminal/Live control/data 분리,60s grace/resume, 내용 비저장 | Session lifetime, Data pumps, safe ended messages | replace/detach-reattach actual new binary consumer output, grace PTY alive, once explicit close | actual synthetic shell observation 메모리만; source/log는 metadata | terminal contents/privatekey/rawSSHerror를 Evidence에 보존하지 않음 |
| D20 immutable snapshot/partial failure/reconciliation/owned cleanup | existing Provider Reset/Cleanup producer + new access wrapper | lifecycle producer exact observed states, no blind mutation retry | 추가 M2/M3/Control lifecycle/Internet ON/OFF 실제 suite, exact-ID cleanup + final inventory | durable SaaS restart/Postgres integration은 이번 diff 변경 없음/대상 아님 |

Git owning SSOT: contracts/connector README + terminal-control/data schemas, contracts/realtime README/terminal-live schema, runtime contract/README 및 backend 책임 경계를 읽었다. wire fields/schema와 DB migration/HTTP API 변경 없음; tracing test-only 수정은 기존 runtime exporter shutdown 상한 의미를 유지한다. backend Terminal Control ENDED consumer의 correlation/idempotent 처리와 Relay Data disconnect grace를 확인했다. best-effort Data final frame 생략은 건강한 Control ENDED로 종료를 전달하며 둘 다 손실된 peer에서 delivery를 보장한다고 주장하지 않는다.

## 3. 수정 전 findings와 현재 상태

| ID/severity | 정확한 수정 전 위치 | 재현/관찰/영향 | 수정/회귀 상태 |
| --- | --- | --- | --- |
| F-SLOW / P1 High | da6 terminal/session.go386-387; app/app.go252/254; wss/reconnect.go164/166 | Data TCP peer attach ACK 후 읽기중단, real production PTY output write blocked; Shutdown750ms 초과3회, PTYclosedfalse/ended0. runControl real loopback SSH fixture는 Control open/SSH open 유지. 타이머/취소없어 peer가영원히읽지않으면 cleanup 무기한 대기 가능 | writeMu.TryLock busy→socketClose; lock 획득→exclusive final1s deadline. nativeTCP/red→green, healthy reason/once 및 actual runControl 검증 |
| F-SLOW-ownership / P2 Medium | da6 session.go265/333 oldConn.Close 앞 writeMu | Close-only candidate에서 currentconn 교체/분리후 oldwriter/socket/Attach/Detach750msblocked. 외부 peerforceclose후만회복 | oldConn.Close concurrent허용. replace+detach-reattach oldwrite해제, 새실제WSSbinaryoutput, 같은PTY유지/20ms grace/resumedtrue -race3PASS |
| F-SSH-CLOSE / P1 High | a5 ssh_pty.go188-194/160; app.shutdown Terminal→VMAccess.Close | managedclient=nil; peer shell ACK후읽기중단, real TCP blocked INPUT16MiB. Close stdinEOF/sessionClose 동기write에서750msblocked3회. 첫 -race는 xCrypto channel.CloseWrite624 vs WriteExtended258 sentEOF race도검출. managerClose가그뒤라소켓정리까지막힘 | managercallback만호출; shared rawchannelClose는기존worker. legacy ownclient socketfirst/stdinClose생략. actualManager realTCP before3FAIL→current3PASS, workers+serverEOF |
| F-SSH-RESIZE / P1 High | a5 session.go447-457, ssh_pty.go165-173 | WindowChange 무컨텍스트write가state s.mu/p.mu보유; genuineTCP3회 ManagerShutdown750msblocked, transportforceClose후에만회복 | 별도resizeMu로순서보존; state snapshot뒤network호출/Close는resizeMu미획득. current actualManager input/resize 두분기3회PASS |
| F-TRACE-TEST / P2 Medium (test-only) | upstream7aaa tracing/runtime_test.go648; helpers_test.go256/258 | WithTimeout 생성후time.Now로elapsed측정; 정상 DeadlineExceeded299.7157ms를budget300ms하한이실패처리. product deadline 오작동아님 | errors.Is(DeadlineExceeded) + 기존 budget+700ms 상한 + export_timeout 유지. production tracing diff없음. 초기 full Go FAIL 보존, test-only rerun은 별도소스 식별 |

원래 R1/R2/R3와 P1 partial-failure fencing/P2 lazy initialization fix는 byte unchanged 부분과 새 Terminal Close 변경 영향을 다시 검토했다. 최신 최소 production 변경은 Session/SSHPTY 두파일 안에 있으며 별도runtime option/wire/protocol을 추가하지 않는다. 기존 legacy own-client Close도 socketfirst로마무리하여 CloseWrite/dataWrite race를 피한다. 현재 추가 production 결함은 확인하지 못했다. 최신Close delegation의actualSFTP-after-PTY-close/idleidentity도현재source TestOpenStackVMAccessIntegration 재실행으로직접검증됐다(section7).

## 4. 원본/실행 정합성 및 실행 범위

독립 실행은 genuine TCP/Gorilla/SSH kernel backpressure이며 Cloud0이다. synthetic peer/key/PTY/relay를 actual cloud로 표기하지 않는다. finalFrame test의16MiB padding은 TCP deadline branch를 실행하기 위한 명시적 write fault injection이며 정상 Data framing acceptance가 아니다. raw 원본은 아래 해시로 보존했다.

| 원본 | SHA256 | test action counts (nested/repeats 포함) |
| --- | --- | --- |
| `unread-shutdown-before.jsonl` | `4179e17bd513492588a6d214c2a76ed3d571e607d54a5027435b61a23098b013` | {"pass": 0, "fail": 3, "skip": 0} |
| `runcontrol-unread-before.jsonl` | `23d0785ea336b9525ade04b413d68c6cf6912d3cd756d14b4beb11dec341b334` | {"pass": 0, "fail": 1, "skip": 0} |
| `ownership-close-only.jsonl` | `f38c2df3bc11ddb5e3aae50087bdcd2b82744ec6fc578600a429e59e115d1f86` | {"pass": 0, "fail": 3, "skip": 0} |
| `final-frame-before.jsonl` | `294343ddd71604149e06b6dadbb8a53c23ee1096e10875d0b1a67c4433377e33` | {"pass": 0, "fail": 1, "skip": 0} |
| `candidate-green.jsonl` | `fe0fbc9f76478b8ef3481e132e72a520a623b231056e6c3044125b64ffd8f191` | {"pass": 15, "fail": 0, "skip": 0} |
| `final-frame-and-healthy-green.jsonl` | `b981eb4d23f7539e6c767ae74c1175ef110f7017e56265a92f126812c4f62d06` | {"pass": 27, "fail": 0, "skip": 0} |
| `ssh-unread-close.jsonl` | `e45bee1abffb494e6292f6e69d7e08d979216b8523b284c38ab83e980a2d12a9` | {"pass": 0, "fail": 3, "skip": 0} |
| `ssh-unread-lifecycle-before.jsonl` | `9b4ee9b22144eeabbd80f267c7ef5ebedf18f793134c19e95855281f923523ad` | {"pass": 0, "fail": 6, "skip": 0} |
| `ssh-unread-lifecycle-candidate.jsonl` | `58fd7daee5fc70b9da3da61d934455601959e1e2644d7623a80aa27128f9f533` | {"pass": 3, "fail": 3, "skip": 0} |
| `ssh-unread-lifecycle-candidate-green.jsonl` | `c4d0b27dc62da8bc1d744f5bab224596418799f8c33b1f8259c707a0bae7eb80` | {"pass": 6, "fail": 0, "skip": 0} |
| `ssh-manager-unread-before.jsonl` | `c96e66a52e6ee35a89f4d5f4926244b9383f814c88c9f3c3c5b27f1fe9ed1227` | {"pass": 0, "fail": 9, "skip": 0} |
| `ssh-manager-unread-final.jsonl` | `ad7d730e9aa6ed3e58a1a26b302a5da49c0b40f78e9f12ed24ecff1b01ff8654` | {"pass": 9, "fail": 0, "skip": 0} |

- `portable-current-recovery/result.json`: HEADa5, Datafix-only terminal SHA49141d09..., selected5tests -race count3 PASS. 이후 SSH 수정 전 결과다.
- `portable-ssh-current/result.json`: 최신 SSH/Session source SHA + fixture/dependency SHA + arguments + before/after git status를 기록하는 actualManager 포함 선택3test -race3 실행. 선택3test -race3의중첩test action15PASS/0FAIL/0skip를확인했다. 별도Before mode는count1 예상FAIL을실제재실행해원본/exit1을보존했다(section7).
- `reproduce_backpressure.ps1` 및 `reproduce_ssh_backpressure.ps1`: Repo/외부Out/Count/Mode 인자; generated overlay는UTF8 BOM없음; cloud opt-in ActualVM test를 선택하지 않는 anchored names; rawjson/stderr/소스hash 기록; finally위치복원. 필요한 동료fixture와 sourcecopies를 같은외부dir에준비. Go1.27.1, race native toolchain, repo go.mod/go.sum 의존성이 필요하다. Before mode는 예상FAIL이다. 키는t.TempDir 생성뿐이며 내용을로그하지않는다.
- SSH early candidate의 최초FAIL은 shutdown은반환했으나 reservation-only fake callback 뒤의 별도 shared socketClose를 테스트가 호출하지 않은 fixture 오류다. 원본 `ssh-unread-lifecycle-candidate.jsonl` 보존; 다음 green fixture는 그다음 owner socketClose를 명시적으로모델링했고, 이후 real VMAccessManager fixture로 whole ownership을 강화했다.
- 첫 SSH repro 실행은 PowerShell result writer의 `$false` 오타로 별도 result metadata 쓰기만실패했다. Go -race rawFAIL/실제timeout/race원본은 완결되어있다. 이후 corrected wrapper/portable runner를 사용한다.
- actualManager before 원본의 끝 cleanup log는 forced transportClose후 관찰이다. Test FAIL action/750ms error가판정원본이며, 이를 normal shutdown PASS로확대하지않는다. final fixture는 normalShutdownReturned/forcedFixtureCleanup 명시.

최신 최종 자동 검증 원본을 독립 집계했다: 전체 Go2885 PASS/7 skips(Cloud opt-in6개+Windows에서POSIX0600 assertion1개), 관련6package race482 PASS/6 skips, 핵심 race20회940 PASS/skip0, Web13files/163tests 및 fresh npm11.21 lock install/typecheck/lint/build PASS, gofmt/vet/build/Contracts/Reset schema PASS. 전체Go07:30:06→07:30:31UTC, repeat07:30:53→07:32:06UTC. race skip6개는Cloud opt-in이며 별도 actual run과 분리한다. Windows의POSIX0600 assertion은이플랫폼에서해당없음으로skip; 실제 credential 생성/비노출 동작은다른subtest로검증한다. `independent-run-summary.json`은 명령/시간/원본해시/action/skip 목록을 기록한다. failure-tested-source와 final-checks/tested-source의 production/dependency/runtime11/13파일(각 snapshot 포함 범위)은 현재 byte mismatch0이다. 전체 목록/source blob은 독립 manifest로 별도 고정했다. final-checks의32개Go파일모두currentbyte일치; actualsnapshot31개Go파일중ssh_backpressure_test.go는실환경실행뒤standalone/log보완이추가됐고, 나머지와production는일치한다. 해당test-only보완은최종전체자동검증에포함되며actualharnessbyte는바뀌지않았다.

Root 실제 실행 추가 suite 원본검토: M2Provision, M3Lifecycle, ControlWSS lifecycle, Internet ON/OFF 모두 실제로실행(이전 opt-in skip4개 해소). 이4개는 HEADda6/source pin이며 runtime shutdown 신규수정전체 검증으로확대하지않는다. actual2VM R1sleep cancel/deadline10회씩+AIO, R2 actualSDK GET503 fault+actualaffinity, actualhostpin callback rejection,60channelcycles, managerclosedadmission은 HEADa5 Datafix-only로PASS12 + childactual3, skip0/fail0. SSH 최신수정 소스로 같은 suite 재실행이07:23:48→07:28:54UTC exit0/12PASS/no skip/fail로 끝났고 actual child healthy/unread3PASS를 다시 확인했다. tracked10개 independent GET404와 최종readonlymatchingTestResidualTotal0, servers0/images3/flavors4/networks4를07:29:04UTC 직접 확인했다. `failure-final-source-state.json`의 changedProductionFiles=[] 및 independent source comparison mismatch0이다. Cloud 소유리소스 Cleanup등록은 Provision outcome assertion 전에하며 returned exact-ID ledger만삭제; aux key/managementSG도fixture별정리. before-SSH run actual10tracked GET404와 최종readonlymatchingprefix0를직접확인했다. 최신3rd run의 소유 ledger10개 cleanup과 matching aux key/managementSG 포함최종재고0를 확인했다. 서로 다른실행의resource수를합쳐단일ledger처럼표시하지않는다.

초기 actual fixture FAIL(childcwd/VM SSH readiness) 및 initial fullGo tracing falsefailure는 root 별도 initial directory에보존되어있다. 수정된 fixture의후속PASS로 원래FAIL을지우지않는다. 실제OpenStackcredentials/private local endpoints/keys/terminalpayload는이보고서에복사하지않았다.

## 5. 전체 파일 coverage와 과거 Evidence

| 과거 package | checksum | ZIP | 현재 검증 |
| --- | --- | --- | --- |
| 2026-10-10 | 6 hashed artifacts | automated-logs.zip: 9 entries, CRC PASS | SHA/size/manifest mismatch 0 |
| 2026-10-12 | 19 hashed artifacts | audit-logs.zip: 28 entries, CRC PASS | SHA/size/manifest mismatch 0 |
| 2026-10-12-review-followup | 22 hashed artifacts | audit-logs.zip: 220 entries, CRC PASS | SHA/size/manifest mismatch 0 |

2026-10-10 ZIP은 separate archive manifest가 없어 CRC와 whole artifact checksum까지만재검증했다. 나머지ZIP은exact entryset/CRC/SHA/size를대조했다. 당시 source identity/요약은당시 snapshot이고 새 source에대해새결과를주장하지 않는다. 역사 package의실환경로그/초기failure/SSOT/reproducer가마침표시된당시검증기록임을유지한다. 새 Evidence 추가시 exactartifacts/zip/secret scan/portable guard/publication source mapping은별도최종게시리뷰를해야한다.

| 파일 | scope/role | 독립 검토 목적/경계 |
| --- | --- | --- |
| `docs/connector/evidence/LBT-147/2026-10-10/README.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/artifact-checksums.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/automated-logs.zip` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/reproduce.ps1` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/secret-scan.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/source-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-10/validation-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/.gitattributes` | 당시 local / pending-publication | 이 표 작성 당시 source snapshot만 확인했으며, 후속 게시 감사 결과는 publication-review.md와 section8 이후 기록을 따른다. |
| `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/.gitignore` | 당시 local / pending-publication | 이 표 작성 당시 source snapshot만 확인했으며, 후속 게시 감사 결과는 publication-review.md와 section8 이후 기록을 따른다. |
| `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/AUDIT.md` | 당시 local / pending-publication | 이 표 작성 당시 source snapshot만 확인했으며, 후속 게시 감사 결과는 publication-review.md와 section8 이후 기록을 따른다. |
| `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/README.md` | 당시 local / pending-publication | 이 표 작성 당시 source snapshot만 확인했으며, 후속 게시 감사 결과는 publication-review.md와 section8 이후 기록을 따른다. |
| `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/prepare_overlays.py` | 당시 local / pending-publication | 이 표 작성 당시 source snapshot만 확인했으며, 후속 게시 감사 결과는 publication-review.md와 section8 이후 기록을 따른다. |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/.gitattributes` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/.gitignore` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/AUDIT.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/README.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/LICENSE-x-crypto.txt` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/dependency-fixture-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/independent_unacked_setup_test.go` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/reproduce-independent-unacked.ps1` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/reviewer-executed-source-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/ssh-channel-no-b-close-ack.go` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/archive-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/artifact-checksums.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/audit-logs.zip` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/file-review.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/independent-review.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/publication-checks.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/publication-review.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/reproduce-guard-regression.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/reproduce.py` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/secret-scan.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/source-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/ssot-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12-review-followup/validation-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/.gitattributes` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/AUDIT.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/README.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/archive-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/artifact-checksums.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/audit-logs.zip` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/automated-validation-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/post-discovery-and-residual.jsonl` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/pre-discovery.jsonl` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/publication-checks.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/publication-review.md` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/real-app-boundaries.jsonl` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/real-vm-access.jsonl` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/reproduce-guard-regression.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/reproduce.ps1` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/secret-scan.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/source-manifest.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/ssot-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/validation-summary.json` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `docs/connector/evidence/LBT-147/2026-10-12/verify-reproduce-guards.ps1` | PR / historical-evidence | 과거 실행/요약/source/재현/원본 무결성 및 당시 범위 한계 확인; 최신 소스 PASS로 확대하지 않음 |
| `internal/connector/app/app.go` | PR / production | 엄격한 VM access factory wiring, production fail-closed, Terminal→shared SSH→Control 종료 순서 |
| `internal/connector/app/app_test.go` | PR / test | 동작/회귀/fixture 테스트: TestControlConfigFromEnvironment_ValidatesControlInputs, TestRunControl_KeepsWSSConnectedWhenProviderAuthenticationFails, TestBuildConnector_ProductionTerminalWiring, TestConnectorApp_GracefulShutdown … |
| `internal/connector/app/backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestRunControlShutdownWithUnreadDataPeer |
| `internal/connector/app/main_integration_test.go` | PR / test | 동작/회귀/fixture 테스트: TestLatestMainControlProviderAndTerminalShareRuntime |
| `internal/connector/app/runtime_provider.go` | PR / production | context-aware shared lazy initialization, 실패 공유/후속 retry, Provider affinity |
| `internal/connector/app/runtime_provider_test.go` | PR / test | 동작/회귀/fixture 테스트: TestLazyProvider_RetriesInitializationAfterFailure, TestLazyProvider_AuthenticationFailureIsDefiniteBeforeMutation, TestLazyProvider_WaitingVMResolutionHonorsContext, TestLazyProvider_VMSetupTimeoutWhileInitializationIsPending … |
| `internal/connector/app/vm_access.go` | PR / production | idle 설정 parsing, Reset/Cleanup 부분 파괴 실패 generation fencing, query interface forwarding |
| `internal/connector/app/vm_access_lifecycle_test.go` | PR / test | 동작/회귀/fixture 테스트: TestFailedLifecycleClosesOldChannelsAndPreservesPreflight, TestFailedLifecycleRejectsDelayedOldResolutionAndSetup |
| `internal/connector/app/vm_access_test.go` | PR / test | 동작/회귀/fixture 테스트: TestOperationResultInvalidatesObsoleteAccess, TestSSHIdleRuntimeDuration |
| `internal/connector/provider/openstack/lifecycle_test.go` | PR / test | 동작/회귀/fixture 테스트: TestCleanupDeletesExactIDsInDependencyOrderAndTreats404AsDeleted, TestCleanupPartialDeleteRejectionReportsDestructiveFailure, TestCleanupDetachesRouterInterfaceBeforeDelete, TestCleanupUncertainDeleteStopsAndPreservesEveryTarget … |
| `internal/connector/provider/openstack/terminal_error_test.go` | PR / test | 동작/회귀/fixture 테스트: TestVMAccessProviderFailuresAndTargetMismatchControlCodes |
| `internal/connector/provider/openstack/terminal_target.go` | PR / production | 실제 Server metadata와 management NIC 검증, typed invalid/unavailable, pinned host key |
| `internal/connector/provider/openstack/terminal_target_test.go` | PR / test | 동작/회귀/fixture 테스트: TestTerminalTargetRejectsInvalidInputsBeforeCloudLookup, TestVMAccessRejectsMismatchedEnvelopeBeforeManagementLookup, TestResolveServerAddressManagementOnly, TestTerminalHostKeyUsesExistingServerPin |
| `internal/connector/provider/openstack/vm_access_integration_test.go` | PR / test | 동작/회귀/fixture 테스트: TestOpenStackVMAccessIntegration |
| `internal/connector/terminal/backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestSessionShutdownWithUnreadDataPeer |
| `internal/connector/terminal/data_connection_backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestSessionReleasesUnreadDataConnectionOnReplaceAndDetach, TestSessionRecoversFromUnreadDataConnectionWithoutEndingPTY |
| `internal/connector/terminal/final_frame_backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestSessionBoundsFinalDataEndedWrite |
| `internal/connector/terminal/session.go` | local / production | 종료 admission, 안전한 외부 error, Data writer/old attachment release, Resize state lock 경계 |
| `internal/connector/terminal/shutdown_test.go` | PR / test | 동작/회귀/fixture 테스트: TestTerminalShutdownRejectsDelayedOpensAndEndsOnce |
| `internal/connector/terminal/ssh_backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestSSHPTYCloseWithUnreadPeer, TestSessionShutdownWithUnreadSSHResize |
| `internal/connector/terminal/ssh_manager_backpressure_test.go` | local / test | 동작/회귀/fixture 테스트: TestManagedSSHShutdownWithUnreadPeer |
| `internal/connector/terminal/ssh_pty.go` | local / production | managed PTY 채널 ownership, setup context, Close delegation/legacy socket first, Resize serialization |
| `internal/connector/terminal/terminal_test.go` | PR / test | 동작/회귀/fixture 테스트: TestTerminal_OpenAndEchoStreaming, TestTerminal_Resize, TestTerminal_GracePeriod_Resume, TestTerminal_GracePeriod_Timeout … |
| `internal/connector/vmaccess/access.go` | PR / production | VM별 shared SSH, generation fence, canceled independent channel, bounded worker/cleanup/idle |
| `internal/connector/vmaccess/access_test.go` | PR / test | 동작/회귀/fixture 테스트: TestSharedTransportIndependentChannelsAndIdleExpiry, TestConcurrentChannelsShareOneHandshake, TestGenerationAndTargetChangeRetireOldTransport, TestDisconnectEvictsAndDoesNotReconnectUntilRequested … |
| `internal/connector/vmaccess/cancellation_test.go` | PR / test | 동작/회귀/fixture 테스트: TestSocketDeadlinePrecedesContextTimer, TestHandshakeTimeoutAndCancellationCloseSocket, TestResetDuringChannelSetupRejectsLateSuccess, TestResetDuringResolutionRejectsLateTargetBeforeDial |
| `internal/connector/vmaccess/channel_isolation_test.go` | PR / test | 동작/회귀/fixture 테스트: TestCanceledChannelLeavesActivePTYUsable, TestCanceledOpenClosesLateChannelAndReusesTransport, TestStalledOpenDoesNotAccumulateCanceledSSHRequests |
| `internal/connector/wss/handler.go` | PR / production | authenticated envelope 보존, Control error mapping/redaction, lifecycle consumer |
| `internal/connector/wss/reconnect.go` | PR / production | process shutdown hook를 current Control close 전에 실행; 일반 reconnect와 분리 |
| `internal/connector/wss/reconnect_test.go` | PR / test | 동작/회귀/fixture 테스트: TestBackoffPolicy_CalculationAndJitter, TestSupervisor_LifecycleAndReconnect, TestSupervisor_ContextCancelBeforeDial, TestSupervisor_BackoffResetAfterSuccessfulConnection |
| `internal/connector/wss/vm_access_test.go` | PR / test | 동작/회귀/fixture 테스트: TestTerminalControlRedactsRawSSHFailures |
| `internal/observability/tracing/runtime_test.go` | local / test | 동작/회귀/fixture 테스트: TestNoneModeCreatesSpansWithoutExporter, TestNoneModeContinuesRemoteParentAndKeepsSampledFlag, TestRuntimeResourceIdentifiesServiceEnvironmentAndComponent, TestOTLPHTTPProtobufExportsSpansToCollector … |
| `runtime/README.md` | PR / runtime-contract | 기본1m 양수 SSH idle 설정 producer/consumer; PTY60s grace와 분리 |
| `runtime/contract.yaml` | PR / runtime-contract | 기본1m 양수 SSH idle 설정 producer/consumer; PTY60s grace와 분리 |

추가 호출자/SSOT(context): app/dependencies_test.go; Provider lifecycle/provision/SSH hostpin enrollment/knownhosts lock; terminal/client.go pumps; wss/client.go boundedControlSend; backend terminal/service.go ControlENDED correlation/idempotence; realtime/data/relay.go grace/end consumer; contracts connector/realtime; runtime; upstream6files. HTTP/OpenAPI 및DBmigration은 변경없고 durable operation flow를새requestscope synchronousmutation으로바꾸지않았다. 기존 DB physical state/Migration의재실행은 이번connector변경의필수경로가아니며 실제Postgres integration은이번PASS라고주장하지않는다.

## 6. 잔여 한계와 남은 최종 단계

- KT Cloud/LBT145미완료, real Browser↔actual SaaS ingress/routing/drain, LBT148CC02/LBT149productionFile acceptance는미검증/후속이다. MockWSS+actualVM은그전체E2E대체가아니다.
- canceled contextlessSSH request는sharedactiveA를보존하기위해transport당pendingoperation1을유지할수있다. peerreply/idle/invalidate/managerClose가해제경계이며 즉시모든goroutine소멸을약속하지않는다. 기존 unacked B close fixture는dependencycopyoverlay의fault로준비/license/해시가과거package에포함되었다.
- finalDataframe1s는session당best-effortbudget, sessions순차close와Control기존send10s는별도다. brief writer는 최대50ms 기다리지만 그보다 긴 contention에서는 DataENDED가 생략될 수 있다. 독립20session fixture는 약1005ms를 관찰했으며 약 N×50ms 뒤 기존 frame/callback 비용이 더해질 수 있다. runtime전체/process-global shutdown SLA나 SaaS LABBIT_SHUTDOWN_GRACE 구현이라고주장하지않는다. healthyControlENDED reason/once를검증한다.
- 현재 최신SSHfix 전체Go/race20/freshmain contextcheck/actualVM/cleanup결과및실행production source pins를확인했다. 새 Evidence 문서/요약이이결과와source identity를정확히연결하는지게시감사만남았다. 생산소스바이트변경시관련검증을다시한다.
- 새Evidence publication package는아직검토하지않았다. source mapping/reproducer opt-in guard/dependencies/원본failure preservation/secret exclusion/ZIPexactset/checksums가최종게시전독립확인대상이다. Audit통과는외부mutation승인이아니다.


## 7. 최종 코드 커밋 및 최신 real PTY/SFTP/idle 검증 보완

독립 확인 UTC: 2026-10-12T07:52:26.193850+00:00. 최종 코드 HEAD `bd8071d86659f2cce5d2c6e77cbb7f7ac0054d05`, origin/main `7aaa5ca4e7f58cb88f188f91e653a624e4caed03`. 현재 committed PR 전체84파일은 앞서 검토한 source/역사 Evidence84파일과정확히같은집합이며, 모두reviewedsnapshot SHA에일치한다(불일치0). Gitcommittedcontent는78파일exact bytes,6파일은workingCRLF→GitLF만다르다(runtimeREADME/contract2개,과거2026-10-10JSON4개). 이전89파일snapshot에는당시초안5파일이포함됐으며그5파일은pending-publication이다. 현재새Evidence폴더전체는이production판정에서제외하고후속게시감사로검토한다. `independent-final-source-mapping.json`에84파일검사와현재실행36source의Go32exactbyte+go.mod/go.sum/runtime2총4EOL-only를보존했다. 최신main통합과9파일코드커밋은이미검토한production/test바이트의고정이며새동작변경은없다.

| 추가 실제 실행 | tested source / 결과 | 독립 확인한 행동 |
| --- | --- | --- |
| current TestOpenStackVMAccessIntegration | `bd8071d86659f2cce5d2c6e77cbb7f7ac0054d05`; -race count1 PASS/no skip; 2026-10-12 07:39:53–07:43:36 UTC, test214.69s | real OP-01 gen1; strict Lab/gen/VM rejection; authenticated/pinned managementSSH; PTY input/output/resize; simultaneous SFTP INIT/VERSION; PTY.Close 뒤SFTP REALPATH 성공; 메모리에서SSH_CONNECTION 두번같음 및 idle후다름; Resetgen2 새Server와stalegen1거절 |
| current cleanup / read-only pre+final inventory | 각PASS/no skip; final07:43:46 UTC | gen1+gen2 distinct14 ResourceRef 각각GET404; finalmatchingprefix0; servers0/images3/flavors4/networks4; pre와final keypairs1/SG4/ports11/subnets4/router1 동일. 이normaltest의임시keypair/managementSG는ownDelete cleanup와prefixList0으로검증했으며aux각각의독립GET404로그는주장하지않는다. |

VM integration은현재production을그대로사용했다. cloud overlay는sharedintegration helper의metadata-only 로그/cleanup 검증과read-only inventory fixture만바꿨으며VMAccess/Terminal/Providerproduction은바꾸지않았다. 이번실제실행36개source는실행전manifest/currentworkingbytes모두일치하고실행후changedSourceFiles=[]다. 32개Go파일은최종committedblobexact bytes, go.mod/go.sum/runtime2개총4개는EOL-only다. PTY내용/SSH_CONNECTION/개인키는로그되지않고식별비교는메모리에서만했다. productionFileCRUD는이minimalSFTPwireprobe의증명범위가아니다.

| 최신 원본 | SHA256 | counts |
| --- | --- | --- |
| `latest-vm-pre-inventory.jsonl` | `6f01fba6b50edd33f7f0fc85c846bf20b34970e3586b57a2ba3f602294a21b9d` | {"pass": 1, "fail": 0, "skip": 0} |
| `latest-vm-access.jsonl` | `2cdbd220b0bab5d6d169f069223fc2cf63b8238bb216de103bfddd773b2e1cae` | {"pass": 1, "fail": 0, "skip": 0} |
| `latest-vm-final-inventory.jsonl` | `04d1d28d944e71f84cadffb731d1ee2291616b2643047e7c8fd64307ff7c97d7` | {"pass": 1, "fail": 0, "skip": 0} |

PortableSSH별도확인: Current는최종Session/SSHPTYbyte로-race count3 실행한15test-actionPASS이며Cloud0/소스수정0; Before는동일fixture/currentVMAccess와a5rawSSHPTY+Datafix-onlySessionoverlay로count1 실제실행한예상5FAIL/exit1이다. 두result모두source/fixtureSHA,명령,before/aftergitstatus를보존했고prepareddependency/scenarioidentity를구분했다. Before실행당시HEAD는bd8071이지만production두파일만의oldbyteoverlay이므로최종코드의실패로해석하지않는다.

**최종 production/동작 판정: 검토한PR범위에서미해결confirmed결함없음.** R1/R2/R3, partialfencing/lazyinit, 새Data/SSHbackpressure/ownership/Resize수정과최신actualPTY/SFTP/sharedidle/Reset/Cleanup을검증했다. PR84의로컬OpenStack구현범위는충족하며KT/Browser/actualSaaS/Fileproduction미검증범위는section6과같다. 새Evidence문서/ZIP/재현helper/secret/checksum의최종publication판정은아직별도검토대상이다.

## 8. Linux CI 후속 수정 독립 검토

후속 scoped reviewer는 commit `e8384cbbb1ddaa16153eae037ac8a37dbee08d6a`의 `session.go`, final-frame 회귀, SSH manager 회귀, SSH resize 회귀 네 파일이 검토 snapshot과 Git blob에서 동일함을 확인했다. 이전 immediate-TryLock production overlay는 새 brief-writer 회귀에서 예상 close1006으로 실패했고 current source는 통과했다. 관련 terminal/app 일반20회와 race20회는 각각180 PASS·fail0·skip0이며, 실제 unread TCP·pending SSH input·blocked TCP resize·shutdown/worker release 경계를 포함한다.

50ms writer-lock poll은 logical close 뒤 state mutex 없이 실행되고, 획득한 경로만 Gorilla writer deadline/WriteJSON을 호출한다. 획득하지 못한 경로는 concurrent-safe connection Close로 blocked writer를 해제하고 PTY/Control cleanup을 계속한다. 새 lock cycle·Gorilla concurrent writer·wire/schema/runtime option은 발견되지 않았다.20개 busy session 외부 fixture는 약1005ms, callback20, registry0으로 통과해 누적 N×50ms 경계를 확인했다. 50ms 초과 Data ENDED best effort와 process-global shutdown budget 부재는 잔여 한계다.

SSH fixture의 server1KiB receive-buffer 제거는 read pause, input200ms pending, shutdown750ms, worker release, backlog consume/`sc.Wait`, ErrClosed assertion을 유지한다. resize wrapper는 PTY.Resize 진입과200ms pending을 동기화하지만 wire의 WindowChange packet 자체를 관찰했다는 주장은 하지 않는다. reviewer는 추가 confirmed defect를 찾지 않았다. 코드 commit의 Linux Go run38231218638은 format·vet·unit·build·PostgreSQL16 integration을 통과했고 Web run38231218734, Contracts run38231218756, backend image build run38231218620도 통과했다. 최종 Evidence commit의 CI와 publication source pin은 root의 후속 검증 범위다.

외부 보고서: `independent/final-write-contention-review/review.md` SHA256 `d3debe716d5458193c4fb1d8d6bcffd28a1ff6b57f37e5fa4aabd7d7f05e49ba`, `summary.json` SHA256 `181d6a769cdaa2fee53a78be806922e2e7c377a720f765c7c1cea2cdff0b5fc1`, committed mapping SHA256 `1299b380901c9aca075f51bad0762049a3fa2df41e328e523354772a74a53e89`.


### 게시 감사 P3/EOL 집계 정정

source-only section7의초기집계는Go/의존34exact와runtime2EOL-only라고잘못표현했다. 게시감사에서전체PR84집합에는go.mod/go.sumcontext가없는데그집계를최신36source에확대했음을발견해수정했다. 개별JSONrawSHA/cleanGitblob매핑은이미정확했으며제품내용변경이나실행소스유실은없다. 실제36source는Go32exact+go.mod/go.sum/runtime2총4EOL-only; publication39는그36+OpenAPI/Web2context를포함하여Go32exact+7EOL-only다. 39rawSHA와Gitcleanblob/committedblob및LF정규화동일성을재대조했고7개정확한집합을assert했다. 기존84PR파일은78exact+역사JSON4/runtime2 EOL-only로초기집계가맞다. 수정전보고서원본은외부independent-review-before-eol-correction.md에보존한다.
