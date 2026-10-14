# LBT-148 PR 생성 전 최종 감사 — 2026-10-14

판정: **PR 가능** — LBT-148의 PTY 및 기존 CC-02 중간 통합 범위. 실제 Browser/SaaS 공동 C2, 운영 SFTP Tree/Read/Save 및 KT D1 Acceptance의 완료 판정은 포함하지 않는다. push·PR 생성·merge·배포·외부 메시지는 수행하지 않았다.

사용자 `PRE_MERGE_AUDIT.md` A–F 절차와 저장소 AGENTS/CONTRIBUTING/TESTING을 적용했다. 구현 담당과 분리된 `review_148` reviewer가 전체 diff, 수정과 회귀, 원본 실행 결과를 검토했다. 실행은 구현 담당이 수행했고 reviewer 실행으로 표시하지 않는다. 별도 reviewer 기록은 `independent-review.md`에 있다.

## A. 버전 및 전체 diff

- 저장소: `ktcloud4-SL/labbit-app`, 작업 브랜치 `feat/LBT-148-pty-cc02`.
- 최신 fetch base: `45dfc486f2dab72ac705310e3f4f3acead1128f0` (LBT-23 Preview #75 포함). 마지막 fetch에서도 동일했다.
- 기존 LBT-148 commit 3개를 LBT-147 작업 base `3996817`에서 최신 main 위로 rebase했다. 원본 `d4812ae`는 로컬 backup branch에 보존했다.
- 최종 코드/test HEAD: **`f510f9d9d24ebbde261614fdc9fabb3bc64146c6`**. 이후 추가되는 이 evidence 디렉터리는 문서·재현용 자료뿐이다. 최종 저장소 HEAD와 tested source의 관계는 `source-manifest.json` 및 `git diff f510f9d..HEAD`로 확인한다.
- 실제 VM·Web·Contracts 실행: `efc22587667a53e11dd87d75c025db37512bc221`. 최종 코드 HEAD와의 유일한 차이는 `managed_setup_cancel_test.go`의 **loopback SSH test fixture** 승인 순서다. production 및 VM harness·Web·Contracts blob은 모두 동일하다. 그 fixture 수정 뒤 전체 Go 검사를 최종 HEAD에서 재실행했다.
- 이전 2026-10-10 기록은 source `1af5c8e`의 historical evidence다. 최신 main 통합 결과의 PASS로 재사용하지 않았다.
- 코드 검토 시 clean tree. 실행 중 변동 및 초기 실패까지 원본 로그에 보존했다. 현 evidence 외 미검토 구현 파일 없음.

| 전체 변경 파일(base → 코드 HEAD) | 목적·요구·영향받는 경계 |
| --- | --- |
| `internal/connector/terminal/ssh_pty.go` | stdout/stderr drain 후 Wait, managed 독립 channel Close, async setup result ownership; AC1/3, VMAccess worker/SSH packet backpressure |
| `internal/connector/terminal/pty.go` | TerminationProvider; byte reader 종료와 remote shell result 연결 |
| `internal/connector/terminal/session.go` | 종료 분류·final bytes, ended-ID tombstone, grace timer fence, 단일 Data owner, 10초 write deadline; CC-02/session/manager |
| `internal/connector/terminal/client.go` | attach 취소, owner 교체·종료, input/resize 실패, 이전 reader 차단; outbound Data WSS/PTY |
| `internal/connector/wss/handler.go` | active OPEN 재사용, closed-ID INVALID_SESSION, 안전한 attach 오류; Control producer 및 Data consumer |
| `internal/connector/provider/openstack/pty_cc02_integration_test.go` | production Handler/Manager/PTY와 실제 VM을 연결하는 opt-in harness; AC1–4 |
| `internal/connector/terminal/data_owner_test.go` | 교체 owner의 reconnect 종료, pending attach 취소/socket 정리 |
| `internal/connector/terminal/data_write_test.go` | unread Data peer의 write timeout 후 정리 |
| `internal/connector/terminal/lifecycle_test.go` | final bytes + exit 0/7/SSH error, stdout/stderr drain, ended-ID 부활 금지 |
| `internal/connector/terminal/data_frame_owner_test.go` | 교체 후 buffered INPUT/RESIZE/CLOSE/fatal 차단, 이전 input 늦은 실패, safe ENDED/PTY Close/형제 유지 |
| `internal/connector/terminal/managed_setup_cancel_test.go` | 실제 loopback SSH request cancel/late reply/initial stdout 48 trial, 형제 echo, Wait worker 정리 |
| `internal/connector/mock/preview_gateway.go` | 최신 main fixture의 concurrent WebSocket writes를 ACK/Binary/Text 공통 mutex로 직렬화 |
| `internal/connector/mock/preview_gateway_test.go` | 32 mixed concurrent writers의 frame count/유실/패닉 회귀 |
| `docs/connector/evidence/LBT-148/2026-10-10/README.md` | 이전 검증 범위·재현 전제 확인, historical source 명시 |
| 같은 디렉터리 `artifact-checksums.json` | 기존 6개 artifact checksum 대조 |
| 같은 디렉터리 `automated-logs.zip` | 이전 unit/race/VM 원본과 요약 직접 대조 |
| 같은 디렉터리 `reproduce.ps1` | 당시 harness/개인 환경 입력·재현 의존성 검토 |
| 같은 디렉터리 `secret-scan.json` | 당시 evidence의 민감정보 검사 결과 확인 |
| 같은 디렉터리 `source-manifest.json` | 9개 기존 source blob과 이전 tested commit 대조 |
| 같은 디렉터리 `validation-summary.json` | 당시 count/skip/VM cleanup과 원본 일치 확인 |

현재 evidence 추가 파일은 README, 이 감사, 독립 리뷰, source manifest, validation summary, raw log archive, checksums, secret scan, 재현 PowerShell/Python·환경 플래그 회귀 및 읽기 전용 residual overlay다. 코드·계약·런타임·dependency 변경을 숨긴 artifact가 없다. 모든 목록과 해시는 checksum 파일에서 열거한다. `residual_harness.go.txt`는 명시적 overlay로만 사용되어 기본 Go package에 추가되지 않는다.

이 evidence 디렉터리의 `.gitattributes`만 `* -text`로 지정하여 원본 artifact bytes/checksums가 Git checkout의 자동 줄바꿈 변환으로 바뀌지 않게 한다. 다른 저장소 파일에는 적용되지 않는다.

## B. 최신 요구사항과 SSOT 추적

Jira/Confluence는 Atlassian Rovo로 원문을 직접 읽었다. Jira LBT-148 updated `2026-10-14T09:19:08.151+0900`, 댓글 0, 상태 진행 중이다. 감사로 Jira 상태를 변경하지 않았다. D-18 v5, D-21 v10(최종 재조회 동일), Connector MVP v19, 테스트 전략 v3를 적용했다. Confluence의 일부 옛 `rabbit-app` 링크는 현재 fetch된 `labbit-app` owning Git SSOT로 해석했으며 wire 요구 변경으로 취급하지 않았다.

| 요구사항·AC/결정 | 원문·버전 | 구현·위치 | 자동 테스트·분기 | 실제 실행/source·Evidence | 공백·판정 |
| --- | --- | --- | --- | --- | --- |
| AC1 실제 VM PTY 생성/input/stdout/초기 size/resize/종료 | [LBT-148](https://samsunglions.atlassian.net/browse/LBT-148), 10/14 최신 | ssh_pty.go:63/225, session.go:345/548/576 | TestSSHPTY_RealSSH_EchoAndResize, TestSSHPTYDrainsBothStreamsBeforeExitStatus, TestFinalOutputAndRemoteTermination | 최종 Go PASS; 실제 VM CC02 -race 133.81s PASS, efc2258 production=f510f9d | 충족, production API/SSH 실제 실행 |
| AC2 기존 Control/Data WSS 경유 IO/resize/close | Jira; Connector README §6/7 및 terminal-control/data schemas | handler.go:1078/1118, client.go:465/499, integration_test:124–202 | attach ACK 전에 성공 금지, OPEN 재사용, frame correlation/resize/close | GitHub main의 MockSaaS/TerminalRelay + production WSS handler/Data client + 실제 OpenStack VM; 단계 원본 log | 충족. 중앙 peer는 Mock, Browser/SaaS 전체는 후속 C2 |
| AC3 종료/실패 정리·다른 Channel 유지 | Jira; [D-18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | ssh_pty.go:192/225, session.go:319/403, vmaccess/access.go(main의 channelOperation) | managed cancel/late result 48 trial, input/resize failure + PTY.closed, unread SSH/Data shutdown, main의 canceled-channel isolation | cloud: A PTY 종료 뒤 B PTY+SFTP 요청 성공; shell exit7/전체 SSH단절 후 active0; tracked7 GET ABSENT | 충족. 신규 Cancel/late reader 분기는 실제 loopback SSH/Mock WSS, cloud packet-loss 주입은 미실행 |
| AC4 LBT-20 Acceptance/C2에서 참조 가능한 Evidence | Jira; TESTING.md/전략 [v3](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) | 이 디렉터리 + tracked VM harness | source/hash/checksum/raw event count 검증 | tested source, rawlogs, 초기 실패, 재현 runner, secret scan 및 독립 감사 보존 | 충족. 인증파일은 재현자의 로컬 입력으로만 준비 |
| VM별 lazy shared SSH, 독립 PTY/SFTP, pinned 관리망 접속 | D-18 v5; runtime SSH 설정; LBT-147 | main의 vmaccess Manager, app.go:249/261, SSHPTY managed Close | canceled-channel timeout/cancel, stale generation/target mismatch, known_hosts fail closed, unread SSH shutdown | 실제 VM에서 shared connection identity + 다른 PTY/SFTP 유지 | 충족. Preview production TCPForwarder는 같은 VMAccess Manager에 직접 연결되지 않아 LBT-24 후속 경계로 기록 |
| 60초 grace, current attachment 1개, 동일 PTY 재접속/no replay | [D-21 v10](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516); Connector §7; realtime/runtime | app.go:164(60s), session timer/owner/tombstone, client pump | owner 교체4 frame, late error, reconnect/grace, shutdown closed admission | cloud 동일 shell state/SSH 유지, historyAvailable=false; **harness grace3s**, production60s 설정 확인 | Connector 범위 충족. 실제 60초 wall-clock/Browser credential continuity·Live fanout는 C2 범위 |
| final output을 전달하고 shell exit/SSH disconnect를 안전하게 분류 | terminal-control/data schema; D-21 | TerminationProvider, session pump n>0 beforeEOF, safe ENDED | final n+EOF, stdout/stderr, normal/nonzero/no-exit error, Write/Resize failure | cloud final bytes + exit7 + Control/Data ENDED, safe SSH_DISCONNECTED | 충족; unread Data final ENDED는 best effort이고 원격 수신 보장 아님 |
| Control/Data 분리, 본문 비저장, local credential/host key | Connector README §2/6/7/JSON cap; realtime; runtime; AGENTS | safe literals, in-memory pipe, unchanged auth/correlation/limit consumers | malformed/correlation mismatch/1MiB/auth-revoke/known_hosts 기존 tests 실제 실행 | cloud assertions는 메모리만 사용, lifecycle metadata만 log; evidence scan0 | 충족 범위. SaaS Browser 권한·학생 read-only/DB/운영배포 검증을 이 PR로 대체하지 않음 |

Owning Git 원문: `contracts/connector/README.md`, terminal-control/data schema, 관련 validation consumer 및 `contracts/realtime/README.md`/terminal-live schema, runtime SSH·Terminal 설정. 현재 `.github/workflows/contracts.yml`의 exact validator scripts도 실행했다. HTTP/API·DB migration·backend auth 구현 diff가 없어 실제 PostgreSQL integration/tagged suite는 해당 없음; 기본 Go/Web 및 OpenAPI 파싱은 실행했다. Runtime·schema·dependency·migration을 변경하지 않았다.

## C. 실패·보안·리소스 수명주기

- 입력/경계: binary는 byte stream, JSON bound before decode, resize 양수/correlation/session/generation 검증 유지. 잘못된 frame은 기존 nonfatal ERROR, unread/oversized socket은 기존 detach/grace 경로다. 재생 history나 wire field를 추가하지 않았다.
- 상태/멱등성: 같은 active OPEN은 PTY/Data conn 재사용, ended-ID는 INVALID_SESSION; 종료는 closeOnce이며 Shutdown의 새 allocation 차단을 보존했다. grace timer identity와 StatusClosed 선점으로 늦은 attach 부활을 막는다. 이전 reader와 늦은 I/O 실패는 새 owner를 종료하지 않는다. handoff 전에 이미 admission된 I/O의 완료는 허용하며 global mutex를 blocking SSH I/O 동안 잡지 않는다.
- 부분 생성/취소: 최신 main channelOperation의 bounded worker를 보존했다. async setup callback과 실패 caller는 mutex로 PTY 소유권을 전달하고 abandoned object의 pipe reader를 반드시 닫는다. 실제 SSH cancel/late output regression과 수정 전 race/leak 재현으로 확인했다.
- shared lifetime: 개별 Close는 managed channel reservation만 해제, PTY stdout reader Close로 Wait/copy를 해제. 전체 manager Shutdown은 shared socket을 닫아 unread input/resize를 풀며 재입장을 거절한다. 단일 setup timeout이 형제 channel을 retire하는 이전 finding은 main에서 해결됐다.
- backpressure: normal Data write10s deadline, final ENDED1s + write-lock 대기50ms; replace/detach는 old socket을 write mutex 대기 없이 닫는다. 안전한 best effort 종료와 Control 통지/PTY cleanup을 구분한다. final output delivery는 정상 연결 테스트에서 검증했다.
- 보안: producer에 attach raw error/SSH detail을 노출하지 않고 safe literal을 사용한다. pinned host key/관리망 resolved target과 credential revoke/reconnect suppression을 유지한다. localhost generated key/explicit insecure WSS는 fixture에 한정한다. private config/key/provider endpoint/terminal bytes는 evidence에 복사하지 않았다.
- 외부 자원: 실제 Provision tracked7개는 DELETE 후 resource type별 GET ABSENT를 확인했다. 사전/사후 inventory servers0/networks4 일치. 임시 keypair/Management SG는 cleanup 성공 뒤 **추가 읽기 전용 inventory에서 해당 fixture prefix 잔여0/0**를 확인했다. residual audit는 mutation하지 않는다.
- 재시작/잔여 위험: ended tombstone은 process lifetime 동안 누적되며 restart 후 durable 복구를 제공하지 않는다. 장시간 운영 부하/강제 kill·rolling deployment의 orphan/reconciliation은 실행하지 않았다. 현재 정책을 임의의 retention/새 영속 계약으로 바꾸지 않았다. 중앙 Browser/SaaS/Live 전체, KT D1, production SFTP Tree/Read/Save는 후속 작업이다.

## D. 실제 실행, 실패, skip, evidence

| 검사 | 결과 | tested source/경계 |
| --- | --- | --- |
| fmt/vet/build | PASS; Go392파일 LF normalize 후 fmt 비교 | 최종 f510f9d, Windows CRLF를 이유로 전체 파일을 rewrite하지 않음 |
| 전체 Go unit | 35 package, **3361 pass event /12 skip /0fail** | f510f9d, `-mod=readonly -count=1` |
| Connector7 package race | **692 pass /11 skip /0fail** | f510f9d; vmaccess/openstack/app/terminal/wss/mock/preview |
| lifecycle·cancel·owner·Preview 반복 race | 20회, **420 pass /0skip /0fail** | f510f9d; 4 package. 새 SSH test 내부48 trial 포함 |
| Web typecheck/lint/test/build | 모두 PASS, **13 file/163tests** | efc2258; 최종 Web blob 동일. tracked npm lock import 후 pnpm frozen install, lock unchanged |
| Contracts | OpenAPI, JSON schemas8개, runtime/migration metadata, Reset11tests PASS | workflow pins PyYAML6.0.3/jsonschema4.26.0/openapi-spec-validator0.9.0; 최종 계약 동일 |
| 실제 OpenStack PTY/CC02 | **-race PASS133.81s**, skip0 | efc2258; production + harness 전체 blob 최종과 동일 |
| 사전/사후 실제 discovery | PASS, images3/flavors4/servers0/networks4 | local labbit-test cloud, 실제 Keystone/Nova/Neutron/Glance |
| 읽기 전용 보조 fixture residual | PASS, keypairs0/Management SG0 | f510f9d + 포함된 read-only overlay harness |
| 수정 전 negative tests | expected FAIL, SSH **data race+Wait leak**, Preview **data race+concurrent write panic** 재현 | f3fa76f production 파일을 Go overlay; 최종 회귀 테스트 사용, 실패를 PASS 통계에 합산하지 않음 |
| 재현 runner 환경 플래그 회귀 | PASS, 합성 inherited opt-in13개 비활성화; cloud command3개 stub | 일반 auto의 KT/OpenStack gate0, explicit cloud의 discovery/PTY 두 gate만1; 실제 API/명령 호출0 |

PASS event에는 subtest가 포함되므로 독립 test case 수로 표현하지 않는다. Skip 목록/이유는 validation-summary와 원본 JSON에 있다. 일반 suite에서 OpenStack mutation tests와 기존 SSH integration opt-in을 끈 것으로 실환경 실패를 skip한 것이 아니다. 명시적 PTY CC02·discovery·residual은 실제 실행/skip0. 다른 opt-in Provision/Reset/Internet/VMAccess/KT tests, tagged PostgreSQL, 실제 Browser/SaaS C2는 이번에 실행하지 않았다.

초기 기록을 숨기지 않았다: latest-main Preview fixture의 기존 sequential tunnel test에서 concurrent-write panic; 새 Data test의 nonexistent constant로 첫 compile 실패 후 SESSION_CLOSED 상수 수정; async test fixture gate handoff로 두 번째 full Go run의 shell subtest timeout. 마지막 것은 production 수정 없이 gate snapshot을 ACK 앞에 옮겨 해결하고 full suite/race/20회 반복을 재실행했다. 각각 negative/초기 rawlog 또는 명확히 전사로 표시한 incident 기록을 archive에 보존했다.

Source stability: final Go 전후 HEAD/20개 diff file blob 일치, 최종 clean. 앞선 attempt의 temporary pnpm lock과 cloud 중 fixture-only edit에 대한 stability=false는 원본 그대로 두고 `source-manifest.json`에서 차이와 실행 경계 영향 없음의 증거를 기록했다. 개인 helper 대신 portable runner와 residual overlay를 포함했다. 실행 환경/versions/command/start/end/exitcode/counts는 validation summary 및 rawrun JSON에 있다. secret scan과 모든 artifact SHA256은 함께 제공한다.

## E. 수정 전 findings 및 해결

| ID·심각도 | 수정 전 revision/파일·라인·재현 | 영향·근거 | 수정·회귀·상태 |
| --- | --- | --- | --- |
| F-148-INHERITED-01 /P2 | old1af5c8e vmaccess/access.go:164/280; 한 session setup timeout/cancel에서 shared transport retire | 형제 PTY/SFTP 종료, D18 독립 channel 위반; 코드 분석 | 최신 main의 bounded worker/late cleanup/독립 channel Close 보존. canceled-channel timeout/cancel 등20회 race PASS. 해결 |
| F-148-ASYNC-01 /P2 | f3fa76f ssh_pty.go:230–238; async worker가 caller 실패 반환 뒤 outer pty assignment/initial stdout | data race 및 소유자 없는 io.Pipe/Wait worker; AC3 | setupMu/abandoned ownership + 두 실패 순서의 Close. 최종 실제 loopback SSH48trial·20회 PASS. old overlay race/leak 둘 다 재현. 해결 |
| F-148-OWNER-01 /P2 | f3fa76f client.go:478–558; old read 완료→replacement attach→old dispatch 또는 old I/O error | 새로운 current PTY에 stale INPUT/RESIZE/CLOSE/fatal/실패 종료; D21/current attachment·Connector §7 | owner/conn/ctx admission, current-only terminal close. buffered4frame/late-inputerror/safe ENDED·PTY close·형제 echo 회귀 PASS20회. 코드 분석 및 실제 handler regression, old production overlay는 별도 실행 안 함. 해결 |
| F-148-PREVIEW-MOCK-01 /P2 | main45dfc48 preview_gateway.go:123/279; attach ACK와 SendBinary/Text 동시 Write | 최신 main Preview fixture panic으로 최종 suite 검증 불가; 실제 panic 재현 | 모든 message write 공통 mutex; 32mixedwriters 및 existing sequential tunnel20회 race PASS. old overlay race/panic 재현. 해결; production Preview scope 확장 없음 |
| F-148-TEST-01 /P2 | efc2258 managed_setup_cancel_test.go:87–99; probe ACK 후 다음 trial gate enqueue가 서버 gate snapshot보다 먼저 실행 | 새 회귀 fixture가 다음 gate를 probe에 배정하고 shell 도달timeout으로 false failure | ACK 전 gate snapshot, targeted20회 및 최종 전체 unit/race20회 PASS. production 코드 변경 없음. 해결 |
| F-148-REPRO-01 /P2 | 제출 초안 validate.py:40–46/80; clean_env가 OpenStack opt-in만 끄고 KT integration/mutation gate는 상속 | 환경에 따라 ordinary auto의 ./...가 KT 실제 API/mutation 경계를 열 수 있음; 코드 분석. 이번 실행은 KT skip 확인 | KT/OpenStack 두 prefix의 test/audit/integration gate 모두0. 합성 inherited13개 및 cloud gate2개 재설정 회귀 PASS, actual command/API0. README 명시. 생산 소스·기존 PASS는 영향 없음. 해결 |

Findings는 사용자에 수정 전에 보고했다. Reviewer 권고인 Write/Resize failure 뒤 물리 PTY Close 확인도 `failedPTY.closed` assertion으로 채택했다. 최종 검토 범위에서 미해결 confirmed finding 없음.

## F. 독립 리뷰와 최종 한계

Reviewer는 구현에 참여하지 않고 base부터 전체20파일/consumer/SSOT 추적과 수정 diff·신규 회귀의 의미를 별도로 검토했다. 직접 원본 Go event를 다시 집계하고 실제 VM PASS/cleanup/전후 inventory, production blob 동일성과 final source stability를 확인했다. Confluence/Jira는 구현 담당이 직접 읽은 원문 전달 범위로 검토했고 실환경 실행은 구현 담당에 한정했다. 최종 evidence 자료·checksums/scan·재현 의존성 추가 검토는 독립 기록에 명시한다.

LBT-148의 승인된 중간 통합 범위에서는 PR 가능하다. 실제 Browser/SaaS C2는 LBT-22, production SFTP 기능은 LBT-149/150, Preview TCPForwarder VMAccess 연결은 LBT-24 후속이다. 감사 결과로 원격 write의 승인을 추론하지 않는다.
