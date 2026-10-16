# LBT-174 pre-merge audit

## 판정 범위와 소스

**로컬 구현·실환경 통합 검증 통과.** 최종 source에서 전체 Go·race·실제 VM fixture를 통과했고, 독립 리뷰에서 확인한 제품 결함은 모두 수정·재검증했다. **Jira 전체 완료 판정은 보완 필요:** AC에 포함된 원격 CI는 push 전 미실행이다. KT/C3는 명시된 후속 범위이며 로컬 PASS에 포함하지 않는다. 이번 작업에서 commit/push/PR/merge는 수행하지 않았다.

- 목적: [LBT-174](https://samsunglions.atlassian.net/browse/LBT-174), parent LBT-24. 로컬 OpenStack Workspace VM + Mock Preview Gateway를 기존 CC-03에 통합한다. KT 최종 검증/closeout은 OP-03 후속, 실제 SaaS/Browser E2E는 LBT-25다.
- 브랜치: `codex/feat/LBT-174-preview-local-integration`.
- 최초 검토 base/HEAD: `4691c0ae8a43955549ae2b9e8bc4f5a464c30001` (LBT-173 merge).
- 최종 base/HEAD: `0051d8d6803fd447f7b690dd895a0a928c3c7302` (검증 중 추가된 LBT-176을 fast-forward 반영). LBT-176의 7파일은 Backend Worker/PostgreSQL 영역이며 이 변경 diff에 포함하지 않는다.
- 구현은 미커밋 상태다. 최종 Go 소스 495파일의 LF 정규 SHA-256 manifest와 aggregate `956c7d6f3b87f6d49d1b842571124da4591db40f5bc15de5c6efce9d65fe98b9`로 식별한다. Evidence 문서는 source hash 계산에서 제외한다. 각 실행의 before/after identity 일치 여부를 별도 metadata에 기록한다.
- ignored `.local/lbt174/local-fixture-key`는 로컬 disposable SSH 개인키 입력이다. 보호된 외부 `clouds.yaml`과 함께 재현 의존성이나 Git/Evidence에 복사하지 않는다. 테스트 helper는 임시 Nova public keypair를 등록한다.

## A. 전체 diff와 영향 경계

| 파일 | 목적·요구사항·영향 경계 |
| --- | --- |
| `internal/connector/app/app.go` | 기존 공유 VMAccess와 PreviewManager의 lifecycle 연결, 영구 shutdown admission 차단. Run/Supervisor/Terminal/File 호출자 검토 |
| `internal/connector/app/vm_access.go` | Reset/Cleanup 결과에 따른 Preview generation invalidation. preflight 실패 보존, partial/unknown/error 정리 |
| `internal/connector/app/vm_access_test.go` | 위 16개 mutation outcome 분기에 Preview pending registry 수명 assertion 추가 |
| `internal/connector/preview/manager.go` | TCP dial 전 pending 등록, duplicate attempt 공유, target/generation fence, 정확한 attempt cleanup, registry 분리와 network close 분리 |
| `internal/connector/preview/session.go` | attempt identity와 socket 소유권, 고정 stream pair, blocked writer를 깨우는 bounded close, stale attach 차단 |
| `internal/connector/preview/client.go` | exact attempt attach, Upgrade/ATTACH 취소·timeout, raw socket handoff, bounded handshake JSON |
| `internal/connector/preview/lifecycle_test.go` | pending late success, stale ACK, attach cancel, blocked writer, duplicate/active 격리, generation/shutdown 회귀 |
| `internal/connector/preview/upgrade_cancel_test.go` | 실제 loopback Upgrade 응답 보류 중 취소가 socket read를 깨우는 회귀 |
| `internal/connector/wss/handler.go` | Preview만 bounded async dispatch, owner 수명, 원자 trust retirement/direct replacement. durable Provider dispatch 수명은 유지 |
| `internal/connector/wss/handler_preview.go` | 기존 Preview handler 이동·보강, captured credential/sender, owner epoch, OPEN receive 순서 예약, CLOSE correlation, 안전한 결과 |
| `internal/connector/wss/handler_preview_test.go` | sequential 테스트에서 먼저 이전 WSS1000 종료를 기다리도록 계약에 맞게 fixture 수정. active OPEN을 implicit replacement로 취급하지 않음 |
| `internal/connector/wss/preview_owner_test.go` | stale Control, reader가 pending OPEN 뒤 CLOSE 처리, shared Control 결과 write 취소 분리, retired owner와 새 owner 격리, 64 worker 상한·회수 |
| `internal/connector/wss/preview_replacement_test.go` | 실제 attach 후 idle logical session의 direct Control replacement 정리 |
| `internal/connector/provider/openstack/preview_transport_export_test.go` | test-only Provider export. 실제 App import cycle을 피하고 결과를 정확한 자원 ID teardown에 전달. production hook 없음 |
| `internal/connector/provider/openstack/preview_transport_integration_test.go` | 실제 App/Control/Provider/SSH + strict Mock Preview Gateway의 opt-in 축소 실환경 fixture |
| `docs/connector/evidence/LBT-174/2026-10-16/` | 재현 절차·원본 결과·초기 실패·SSOT 버전·소스 identity·독립 리뷰·이 audit |

미검토 코드 파일은 없다. 변경되지 않은 전체 제품을 새로 감사한 것은 아니다. 영향을 받는 VMAccess/SSHForwarder, WSS Client/Supervisor, Preview consumer와 owning SSOT를 함께 확인했다. `contracts/http/`, `contracts/realtime/`, DB migration, Runtime 값, dependency는 변경하지 않는다. HTTP 인가/Backend 허용 port/Preview TTL/Browser bootstrap은 기존 SaaS 소유이며 여기서 재구현하지 않는다. DB/Backend durable Operation 의미도 변경하지 않는다.

## B. 최신 원문과 추적성

Jira 본문/AC/댓글을 직접 다시 읽었다. 최종 updated `2026-10-16T14:59:23.423+0900`, 댓글0, 본문·AC는 최초 조회와 동일하고 상태는 진행 중이다. 이 작업에서는 Jira를 수정하지 않았다. Confluence는 결정완료 v11, 추가결정 v3, D-18 v5, D-20 v7, 테스트전략 v3를 직접 조회했다([ssot-versions.json](ssot-versions.json)). 과거 협의의 wire 표현보다 현재 Git Connector §7b와 schema를 우선했다. repo AGENTS/CONTRIBUTING/TESTING, Go CI, `runtime/contract.yaml` Preview/VMAccess 설정, 관련 Backend README도 확인했다.

아래 코드 위치는 최종 source manifest 기준이다. 자동 테스트 PASS에는 loopback 실제 WSS/SSH peer가 포함되나 cloud 검증과 구분한다.

| 요구·AC/원문 | 구현 위치 | 자동 회귀·검증 분기 | 실제 검증·공백 |
| --- | --- | --- | --- |
| LBT-174 기존 CC-03 재사용, D-18 forwarding | `app/app.go:239`, `preview/manager.go:45,90` | 기존 Preview HTTP/SSHForwarder/VMAccess suite | `TestOpenStackPreviewTransportIntegration`: 실제 App/Control → SSH direct-tcpip → VM HTTP. SaaS/Gateway는 Mock |
| §7b 선개설·OPEN_RESULT·outbound ATTACH | `manager.go:90`, `client.go:99`, `handler_preview.go:16` | Handler OPEN/close, safe dial error, initial attach failure/timeout | 실제 HTTP/1.1 200 응답과 exact replyTo OPEN, session/lab/gen/VM/server/port 확인 |
| §7b full correlation·stale attach | `client.go:274`, `session.go:237,274,293` | `TestPreview_CorrelationMismatch`, `TestPreviewLifecycleStaleAttachCannotBindNewAttempt`, concurrent attempt isolation, SSHForwarder target guards | strict Gateway가 실제 ATTACH target/correlation을 대조. stale fault injection은 loopback 범위 |
| §7b 1 WSS=1 TCP, active≤1, sequential | `session.go:191,293`, `manager.go:45` | existing sequential, duplicate attach, active conflict, concurrent 9 duplicate calls | 동일 logical session의 HTTP tunnel 두 번 sequential, 두 session의 CLOSE 격리 |
| CLOSE·Data/upstream 정상/오류, logical 보존 | `session.go:81,104,124,251`, `handler_preview.go:150` | TargetClosedMidStream, TextFrameRejected, GatewayCloseFrame, normal1000 preserve, CLOSE correlation | 실제 `Connection: close` 뒤 다음 HTTP, 명시 CLOSE와 sibling 지속 |
| Reset/Cleanup·stale generation | `app/vm_access.go:68,78`, `manager.go:194` | `TestOperationResultInvalidatesObsoleteAccess` 16분기, generation/shutdown pending fence | 실제 Reset gen1→2 후 old WSS/registry 종료·stale OPEN 거절·새 VM HTTP, Cleanup 뒤 거절 |
| 안전한 오류/비밀 비노출 | `handler_preview.go:44,111`, `preview/session.go:124`, unchanged SSHForwarder | APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE/UNAVAILABLE timeout, sanitized error tests | 실제 closed app port/22/없는 server 오류, Control에 HTTP/PTY/합성 credential 없음. 실제 Provider 장애 전체를 주입하지 않음 |
| Control credential·trust owner | `handler.go:80,1014`, `handler_preview.go:22,95` | old epoch/new credential, trust4001/4002, owner retirement, direct replacement idle, normal EOF reconnect 보존 | Mock Control/loopback boundary. 실제 SaaS revoke/rotate 인가 E2E는 LBT-25 |
| backpressure/cancel/shutdown·누수 방지 | `client.go:123,155`, `session.go:81`, `manager.go:193`, `handler_preview.go:78` | blocked writer close, pending dial late conn, ATTACH read cancel, Upgrade no response cancel, 65번째 OPEN fail·64 slots 회수, shutdown no resurrection | manager/tunnel/worker 완료와 socket 해제 관측. 전역 goroutine 수 0이나 대규모 부하를 주장하지 않음 |
| Terminal/File 회귀 없음, D-18 shared SSH | unchanged VMAccess plus App lifecycle | full Go 및 Connector 9 package race에 Terminal/File 포함 | Preview CLOSE 이후 실제 PTY marker와 SFTP stat 확인. Browser/UI/File Data 전체 E2E 아님 |
| 자동 회귀·관련 CI·Evidence | 아래 D와 원본 로그 | 로컬 전체 기본 검증과 race 실행 | 원격 CI는 push 전 미실행. Jira의 CI AC는 후속 확인 필요. KT/Browser C3는 명시 제외 |

## C. 실패·보안·수명주기 분석

- 입력: Control JSON 상한 1MiB를 decode 전에 적용하는 기존 guard 유지. 누락 correlation, port22/범위 밖, target mismatch, 중복·stale OPEN은 안전한 실패다. 임의 backend allowlist를 Connector에 복제하지 않는다. ACK 전 Text JSON은 bounded, stream은 Binary만 받고 frame 경계를 HTTP 의미로 사용하지 않는다.
- 상태: registry가 pending부터 소유한다. Close/Reset/shutdown 이후 late dial은 같은 tunnel의 done을 검사해 반환된 연결을 닫는다. sequential 새 attempt와 이전 ACK/실패 cleanup은 포인터·message ID로 격리한다. active 다른 attempt는 교체하지 않고 거절한다. 새 generation fence는 process 수명 동안 남는다.
- 동시성: authority lock → manager registry → session/tunnel 순서로 admission/분리만 수행한다. socket I/O와 callback은 authority/registry lock 밖이다. cancel은 exact attempt에만 연결한다. shared Control write는 tunnel cancel에서 분리된 bounded context로 보낸다. streaming writer를 기다리지 않고 socket을 닫아 read/write를 깨운다.
- 재접속: ordinary EOF는 logical state를 보존한다. 활성 Control을 직접 새 Control로 교체하거나 4001/4002 신뢰 종료 시 기존 registry snapshot을 정리하며 새 owner admission을 건드리지 않는다. 이전 epoch OPEN/CLOSE는 새 credential/세션을 사용할 수 없다.
- 부분 실패: Reset preflight/auth 실패는 기존 SSH/Preview를 보존한다. delete 시도·부분 실패·UNKNOWN·내부 오류는 이전 generation을 퇴역한다. 실제 Provider create/delete retry/reconciliation은 기존 Provider/Backend 소유이며 이 PR은 새 mutation retry를 추가하지 않는다.
- 자원: 64 pending Preview worker admission, 1 active/session, bounded chunk forwarding. manager shutdown은 영구 admission fence다. Preview 종료가 공유 SSH transport의 sibling PTY/SFTP를 닫지 않는다. local fixture는 정확히 추적한 ID만 정리하며 다른 Lab/공용 network를 delete하지 않는다.
- 재기동: Preview는 계약상 ephemeral이다. process 재시작 후 세션 복원은 제공하지 않는다. Provider durable operation 복구·DB 동시성·worker lease는 변경 범위 밖이다.
- 비밀: Control/로그/DB로 HTTP path/query/body/cookie/PTY/credential을 추가하지 않는다. provider/SSH 오류는 기존 classifier 및 generic safe message로 제한한다. Data peer close text는 정해진 safe reason만 사용한다. 실제 cloud config/개인키는 ignored/외부에 남고 Evidence에는 포함하지 않는다.
- 미검증: KT quota/실패, 실제 SaaS auth, TLS 운영 배치, 다수 사용자의 부하·장시간 누수, hard crash/DB 복구, 실제 multi-VM topology는 이번 fixture로 검증하지 않는다. 해당 경계를 Mock PASS로 충족 처리하지 않는다.

## D. 실행·실패·skip와 재현

원본 로그·실행 metadata는 이 폴더, 이전 실행은 `history/`, 별도 reviewer 결과는 `independent-review/`에 보존한다. 최초 실패의 정확한 source hash는 수집되지 않았으므로 historical failure로만 사용하며 최종 PASS 근거로 재사용하지 않는다.

| 실행 | 상태/범위 |
| --- | --- |
| canonical LF gofmt | 최종495 Go/module 파일 중 Go 전부 검사, mismatch0 |
| full Go | 최종956c `go test -p=1 -count=1 -json ./...` PASS, 3737 PASS events/41 package PASS/18skip, 223.625초. subtest 포함 event 수이며 독립 testcase 수 아님 |
| Go vet/build | 최종956c 각각 exit0, 15.672초/11.156초 |
| Connector race | 최종956c `-race -p=1 -count=1` 9 package PASS, 122.843초. 단위/loopback, cloud opt-in OFF |
| 웹 typecheck/lint/test/build | 이전 source96ec에서 전부PASS, 14files/188tests. 이후 변경은 Connector Go와 main의 Backend Worker뿐이고 web tracked diff0. canvas 미구현 메시지/큰 bundle 경고는 결과에 남김 |
| 실제 로컬 OpenStack | 최종956c 모든 단계PASS, test221.95초/command228.781초/추적 entries28. 실행 before/after source 동일. 이전96ec 성공은 history에 별도 보존 |
| 원격 CI | 미실행(push 없음). 로컬 명령 결과와 구분 |
| PostgreSQL tagged integration | 미실행. 이 host에 Docker/psql 없음. 최신 main의 Worker DB 검증까지 충족했다고 주장하지 않음 |

역사적 실행:

1. 최초 실제 fixture는 HTTP sequential/격리/PTY/SFTP/오류까지 통과한 뒤 Reset 후 stale OPEN 결과 누락으로 FAIL(250.13초). tracked Lab entries21 정리·부재 확인. 당시 binary는 shared Control 결과 context 분리 전이다. 그 경합을 회귀로 확인해 수정했지만 최초 실패의 정확한 원인을 단일 관측으로 확정하지 않는다.
2. source96ec에서 full Go 병렬 실행은 3722 PASS events/2 FAIL/18skip. vmaccess의 40ms setup timeout, Realtime의 `control log is not JSON`. 동일 source 직렬 전체는 3724 PASS events/41 package PASS/18skip. PASS event는 subtest를 포함하므로 독립 testcase 수가 아니다.
3. 두 기존 테스트 각20회 반복: feature vmaccess20PASS, Realtime19PASS/1FAIL. 기준 main4691 archive도 vmaccess20PASS, Realtime19PASS/동일1FAIL. 관련 source hash는 동일하다. Realtime helper가 아직 빈 log를 JSON으로 decode해 eventually 전에 Fatal하는 기존 race가 재현됐고, Preview 변경과 직접 호출 경로가 없다. vmaccess 원래 실패 원인은 부하 민감 후보이며 확정하지 않는다.
4. source96ec의 Connector9pkg race 및 웹4checks PASS. R008와 main 갱신 이후 최종 source956c에서 Go 전체·vet/build·Connector race·실제 fixture를 다시 실행해 통과했다. 웹은 변경이 없어 D 표의 diff0 근거로 기존 실행을 연결하며 재실행했다고 표시하지 않는다.

일반 suite의 18skip은 cloud opt-in 14개와 Windows 파일시스템/권한 검증 제한 4개다. 실제 Preview fixture는 별도 opt-in 실행에서 skip0/PASS다. skip 이름·조건·원문은 [skips.json](skips.json), summary와 compressed JSONL에 보존한다. tagged PostgreSQL은 일반 suite skip에 포함되지 않는 미실행 영역이다. Mock/Fake, 실제 loopback WSS/SSH, 실제 cloud/VM 경계는 B 표의 구분을 따른다. 재현 필수 코드는 Git 테스트 파일과 [reproduce-local.ps1](reproduce-local.ps1)에 있다. 개인 외부 batch runner는 명령 실행·해시·로그 집계만 담당하며 기능 재현의 필수 dependency가 아니다.

## E. 수정 전 findings와 해결 기록

모든 제품 수정은 사용자에게 finding을 먼저 보고한 후 수행했다. R001–005는 pinned base4691 줄, R006–008은 보존한 first/second pass snapshot 줄이다. 자세한 원문 재현과 로그는 independent-review 기록을 참조한다.

| ID·심각도 | 수정 전 파일·줄·재현·영향 | 수정·회귀 |
| --- | --- | --- |
| R001 High | base `preview/client.go:252`, `session.go:309`, `wss/handler.go:1528`. delayed M1 ACK가 M2에 attach, old 실패 cleanup이 current tunnel을 닫을 수 있음 | immutable attempt/socket identity, exact CloseAttempt; stale ACK 및 concurrent isolation |
| R002 High | base `preview/manager.go:87,154,167`. pending dial 중 CloseSession/All 후 late success가 registry count1로 부활 | dial 전 예약·등록, close/cancel/fence, 늦은 conn close; pending close/generation/shutdown |
| R003 High | base `preview/session.go:60,88`. blocked writer가 writeMu 보유하면 Close도 기다려 socket을 닫지 못함 | bounded WriteControl 후 socket Close; blocked writer 실제 WSS 회귀 |
| R004 Medium | base `preview/client.go:194,204`. ATTACH write/read 취소가 deadline까지 남음 | handshake socket 사전 추적·write deadline·exact attempt cancel; ATTACHED 대기 취소 |
| R005 High | base `wss/handler.go:160,1344`. old epoch OPEN이 새 sender/credential로 실제 Upgrade/attach | captured owner/credential/sender와 admission gate; old epoch dial0 |
| R006 High | first pass `wss/handler.go:1022–1036`. epoch 검사 후 unlock/SetSender/재lock이면 새 owner를 취소하고 새 registry를 CloseAll | epoch/cancel/registry snapshot 분리 원자화·IO밖. scheduling overlay 전3/3FAIL→수정후10/10PASS |
| R007 Medium | first pass `preview/client.go:154–179`. HTTP Upgrade 응답 보류에서 cancel해도 실제 socket이 handshake timeout까지 남음 | raw socket cancellation handoff; 실제 loopback 전3/3FAIL→후10/10PASS |
| R008 Medium | second pass `wss/handler.go:80–97,1006`. attached→idle 후 direct live Control 교체, 늦은 old4002는 무시돼 oldlogical 잔류(권한 우회 관측 아님) | nonnil→nonnil direct replacement에서 snapshot퇴역, nil경유 일반재연결은보존; 실제 idle gateway 회귀 |
| R009 Medium | implementation `handler_preview.go` 결과 전송과 `wss/client.go:241`. tunnel cancel이 진행 중 Control result write context도 취소해 공유 socket을 닫을 수 있음(코드분석+회귀) | bounded WithoutCancel notification context와 captured sender; 결과 write 중 CLOSE 회귀 |
| F174-DOC001 Low | packaging `independent-review/README.md:7–9` 수정 전. 상위 Git 아래 archive copy에서 `git apply`가 15개 patch를 생략하고도 exit0; 실제 복원 495개 중 15개 hash 불일치/새 7파일 부재. 제품 결함이 아닌 Evidence 재현 절차 누락 | 사용자에게 먼저 보고 후 exported copy의 `git init -q` 및 LF hash 전수 비교를 명시. 독립 reviewer가 같은 상위 Git 아래 격리 복원하여 495/495 일치·차이0 확인; 실패/성공 JSON 둘 다 보존 |

별도 기존 제한: Realtime log polling 테스트의 Low 불안정성은 기준 main에서도 재현했으며 이 작업에서 수정하지 않았다. vmaccess의 초기 timeout은 미확정 실행 실패로 보존했다.

## F. 독립 두 번째 리뷰와 최종 결론

구현하지 않은 `/root/review_lbt173` reviewer를 재사용했다. 이름은 이전 작업에서 이어진 것이며 이번 검토 대상은 LBT-174다. reviewer는 원문 SSOT를 직접 확인하고 base/archive 및 first/second snapshot에서 별도 probe를 실행했다. 제품 checkout과 cloud는 수정하지 않았다. R006/R007/R008을 추가 발견해 먼저 보고했고 수정된 전체 diff·source identity·경계 race·normal EOF와 replacement 차이를 다시 확인했다. 초기·중간 review는 최종 승인으로 취급하지 않는다.

최종 reviewer는 0051 base의 전체15개 Go 변경·영향 consumer·SSOT를 다시 검토하고 source495/495 LF hash 일치(차이0)를 확인했다. R006 scheduling fault, R007 실제 Upgrade 취소, R008 실제 attached→idle direct replacement 및 ordinary EOF 재연결 보존을 각각10회 통과했다. Preview/WSS/App `-race -count=3`도 PASS다. 최종 실제 OpenStack 로그와 metadata의 source956c/unchanged=true/모든 단계PASS/entries28, full Go·vet·build·race 결과를 직접 대조했다. 새 confirmed 제품 finding은 없다. 자세한 범위와 제한은 최종 독립 review에 있다.

취합한 Evidence의 원본 일치·JSONL 재집계·patch 복원·probe/overlay 일치는 [최종 독립 packaging 대조](independent-review/final/PACKAGING_ADDENDUM.md)에서 확인했다. 문서 Low F174-DOC001도 해결됐다. reviewer inventory는 취합 중 관측이며, 최종 파일 목록·해시는 `change-inventory.txt`와 `evidence-sha256.json`을 따른다.

로컬 변경 범위의 구현·통합 및 감사는 통과했다. 원격 CI AC는 미충족 상태로 남기므로 LBT-174 전체 완료나 원격 검증 통과를 선언하지 않는다. 추가 source 변경 시 해당 검증과 source/Evidence 대조를 다시 수행한다. KT 최종/실제 Browser C3·tagged DB·장시간 부하와 보조 keypair/관리SG GET 부재 확인의 한계는 남는다. Audit 결과는 commit/push/PR/merge/외부 전송 승인으로 해석하지 않는다.
