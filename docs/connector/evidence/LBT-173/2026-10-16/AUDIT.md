# LBT-173 pre-merge audit

> 이 문서는 KT 실행 승인 전의 로컬 검증 기록이다. 최신 [KT 새 fixture 전체 PASS audit](ktcloud/full-fixture/AUDIT.md)와 [실행 결과](ktcloud/full-fixture/README.md)를 참조한다. 최초 [KT 실패·복구 audit](ktcloud/AUDIT.md)와 아래 당시 판정은 역사적 Evidence로 유지한다.

2026-10-16 Asia/Seoul. 사용자 지정 `C:/Users/user/Documents/ChatGPT/3차 2/PRE_MERGE_AUDIT.md` 전체 절차에 따른 기록이다. **판정: 로컬 구현 범위는 PR 가능. KT smoke는 미검증이므로 LBT-173 전체 완료로 판정하지 않는다.** 원격 push/PR 생성은 하지 않았다.

## 대상과 최신 원문

Repository: `ktcloud4-SL/labbit-app`, local branch `codex/feat/LBT-173-ssh-tcp-forwarding`. Base/HEAD는 `88097be5d9bb6996b718fd185515ca324629fda6`이며 staged 없이 미커밋 working tree로 검증했다. 최종 main 읽기 전용 조회도 같은 SHA였다. [source-manifest.json](source-manifest.json)은 실행 소스의 raw/LF SHA256, [source-diff.patch](source-diff.patch)는 신규 파일을 포함한 전체 코드 diff다. [change-inventory.txt](change-inventory.txt)는 Evidence까지 포함한 변경 목록이다.

| 원문 | 직접 확인한 버전과 적용 범위 |
| --- | --- |
| [LBT-173](https://samsunglions.atlassian.net/browse/LBT-173) | updated 2026-10-16T09:23:53.415+0900, 댓글 0. 부모 LBT-24. 로컬 SSH 구현/검증과 KT 단독 smoke, 후속 OP-03/C3 제외 경계 |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | v5 확정. Workspace, private management SSH, 공유 connection/독립 PTY·SFTP·TCP |
| [검증 설계](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) | v3. Unit/Contract/Connector Integration/E2E 구분, 실패/cleanup Evidence |
| [착수 전 정리](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/16744453), [wire 확정 정리](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/11075640) | v3/v11 배경 확인. 과거 wire 미확정 문구로 현재 Git 계약을 덮어쓰지 않음 |
| AGENTS/CONTRIBUTING/TESTING/Makefile | base88097be. 하위 AGENTS 없음. 전체 기본 검증을 동등한 개별 명령으로 실행 |
| contracts/connector/README.md §7b, preview-control/data.schema.json | base88097be, 변경 없음. 4개 대상 field/port/error/capability, Control/Data 분리 및 본문 비저장 |
| runtime/contract.yaml | Backend port allowlist와 기존 SSH idle 기본값 1m. 이번 변경은 idle 설명에 Preview 추가만 적용 |

HTTP API/Browser Realtime/DB migration은 변경하지 않았다. 내부 interface/transport 변경으로 외부 field/message/error code/DB 변경을 추가하지 않았다. 기존 handler, Provider terminal_target/hostpin, Terminal/File 호출자, Preview Session/Data client를 영향 경계로 검토했다. Backend 책임/의존 방향을 유지하며 Browser 인증과 Backend port 승인 정책은 재구현하지 않았다. 새 외부 dependency는 없다.

## 전체 변경 검토

| 파일 | 목적·영향받는 경계 |
| --- | --- |
| internal/connector/vmaccess/access.go | OpenSession에서 acquire 추출. 매번 Provider 재검증, lease/idle/세대 무효화 유지. io.Closer 공통 cleanup과 late managed TCP 즉시 Close |
| internal/connector/vmaccess/tcp.go (신규) | RFC4254 direct-tcpip, loopback/exact port, setup worker/최대16채널, local pipe와 SSH CLOSE까지의 소유권, 안전한 오류 |
| internal/connector/vmaccess/access_test.go | in-process SSH peer direct-tcpip 해석/거절/echo. 기존 PTY/SFTP fixture 회귀 |
| internal/connector/vmaccess/tcp_test.go (신규) | target/port, 공유/격리, timeout/cancel/late-success, hostpin/offline, Close/EOF/회수 한도 |
| internal/connector/preview/forwarder.go | Target 4field interface, Workspace gate→VMAccess, 표준 오류만 공개. DirectTCP는 개발 fixture |
| internal/connector/preview/manager.go | envelope+payload에서 Target 전달. 기존 재사용/경합/double-check 유지 |
| internal/connector/preview/forwarder_test.go (신규) | target 전달/failed dial 미등록, port/Workspace 선행 gate, 오류 비노출, context/미설정 |
| internal/connector/app/app.go | Terminal/File과 같은 Access 주입. generic TCP의 production 우회 제거. preview-v1 제거/준비 시 재선언 |
| internal/connector/app/app_test.go | generic dialer 미호출/fail closed, Workspace/Resolver에 동일 target, capability 중복 제거 |
| internal/connector/provider/openstack/preview_forwarding_integration_test.go (신규) | opt-in disposable Workspace, 실제 SSH/HTTP/PTY/SFTP, Reset 세대와 exact-ID cleanup. 기본 실행은 skip |
| runtime/contract.yaml:655 | SSH idle 설명에 TCP 추가. YAML 구조/기존 값 정합성 |
| docs/connector/evidence/LBT-173/2026-10-16/** (신규) | 원본 log/gzip/집계, 초기 실패, hash/diff, 재현 script, 독립 fixture, 추적성/audit. 실제 credential/본문 미수록 |

모든 변경과 관련 공유 상태/consumer를 검토했다. 무관한 기존 앱 전부와 제삼자 라이브러리 전체 audit는 하지 않았다. ignored web/node_modules/dist는 lockfile 의존/생성물이다. clouds.yaml/private key/known_hosts는 로컬 Secret 입력이며 복사하지 않았다. 테스트 known_hosts는 temp다. README에 다른 환경의 재현 입력을 명시했다.

## 요구사항 → 구현 → 테스트 → 실제 Evidence

아래 최종 실행 소스는 모두 source-manifest.json과 일치한다. Jira/D-18/Git 버전은 위 표 기준이다.

| 요구·AC/SSOT | 구현 파일·라인 | 자동 테스트·분기 | 실제 실행과 Evidence | 공백·판정 |
| --- | --- | --- | --- | --- |
| 4field target, 현 세대/VM/management NIC/pin (LBT-173, §7b338) | manager.go:87, forwarder.go:43, access.go:88, 기존 terminal_target.go:23/47 | TestSessionManagerForwardsFullControlTarget, TestVMAccessRejectsMismatchedEnvelopeBeforeManagementLookup, 기존 NIC/pin tests | final-go-test, live wrong4target/actual Reset. hostpin 실패는 in-process fixture | 로컬 충족, KT 별도 환경 미실행 |
| Workspace만 접속 (D-18, §7b) | forwarder.go:54, Provider ValidateWorkspace | TestSSHForwarderGuardsWorkspacePortAndSanitizesErrors, TestWorkspaceDesignationFailsClosed | fake에서 non-Workspace 선행 거절, 실제 Workspace metadata 확인 | 별도 non-Workspace VM 추가 Provision은 미실행. Provider unit 거절 근거 병행 |
| exact targetPort, 22/유효하지 않은 port 차단, allowlist 재정의 금지 (§7b342) | tcp.go:25/45, forwarder.go:44 | 80/3000/5000/65535 통과, -1/0/22/65536 거절, peer wire destination assertion | 실제 loopback3000 HTTP, 3001 APP_NOT_RUNNING, 22/범위 거절 | 충족. Backend 인가 자체는 기존 SaaS 책임 |
| 공유 SSH/독립 PTY·SFTP·TCP (D-18) | acquire, tcpStream:99, tcpConnection.Close:130 | TestTCPSharedTransportExactDestinationAndIsolation, Terminal/File 포함7pkg race | 실제 SSH_CONNECTION 동일, Preview Close 후 PTY marker/SFTP Stat, HTTP 두 번 | 로컬 충족. File 전체 HTTP upload/download 실환경은 재실행하지 않음 |
| timeout/cancel, 이전 세대 무효화, late open 회수 (LBT-173) | channelOperation:354, discardResult:394, tcp.go:34, InvalidateBefore:219 | TestTCPOpenTimeoutLateChannelAndSiblingIsolation, TestTCPCancelAndShutdownPendingOpen, 독립 late fault | 최종 race/독립 fault, 실제 Reset 후 이전 세대 차단 | timeout/비응답은 fixture. 실 VM SSH 장애 주입은 하지 않음 |
| 종료/자원 소유권/격리 (D-18/TESTING) | tcp.go:57/99/115/130 | TestTCPCloseUnblocksCallersAndBoundsNonCooperatingPeer, TestTCPEOFDoesNotReleaseChannelBudgetBeforeSSHClose, 독립3 probes/mutant | independent-review/final-reproducer-fault.log, final-unit-race.log | 최대16, 초과 fail closed. 비협조 peer 점유는 idle/invalid/shutdown까지 지속 |
| production SSH 주입/fallback 금지/preview-v1 준비 상태 (§7b84/338) | app.go:237-260 | TestBuildConnector_Production_Preview_FailClosed_And_Capabilities | app tests/race. generic dialer 선택 시 panic하는 fixture로 미호출 검증 | production 배포/실제 서비스 기동은 미실행. 구성 경계 검증 |
| safe error, 본문/Secret 비저장 (§7b350-355/402) | forwarder.go:54-69, tcp.go:64-73 | private Provider/Workspace/hostpin 오류 비노출, refused/timeout 분류 | final-go-test, live는 안전한 결과 metadata만 기록. body/PTY/SSH_CONNECTION은 메모리 assertion | 코드/로컬 Evidence 검토. 전체 운영 log pipeline audit는 제외 |
| 실 VM HTTP 응답 (LBT-173 AC1) | preview_forwarding_integration_test.go:24-152 | opt-in TestOpenStackPreviewForwardingIntegration | 최종 local-openstack.log 211.62s PASS, 실제200/body 두 번 | 로컬 충족 |
| KT 단독 smoke/환경 차이 (LBT-173 AC4) | 공통 VMAccess, 기존 KT Resolver | KT target affinity unit은 기본suite 실행 | 실제 KT는 사용자 local-only 지시에 따라 미실행 | 미검증. 표준 Neutron↔KT NSM 차이는 README. Jira 전체 완료 보류 |
| OP-03 후속/C3 제외 (Jira) | 기존 Preview Session/Data client 유지 | 기존 in-process WSS 회귀 | Mock WSS PASS만 있음 | 실 SaaS/Browser C3 및 Control/Data 전체 실환경 lifecycle 미실행 |

## 수정 전 findings와 해결 상태

각 finding은 수정 전에 사용자에게 보고했다. 행 번호는 관측 revision 기준이다. 중간 미커밋 revision 전체 hash는 보관하지 못했으므로, 원본 로그/fixture와 최종 소스 hash를 구분한다.

| ID·심각도 / 관측 위치 | 재현·근거, 영향·SSOT | 수정·회귀 및 결과 |
| --- | --- | --- |
| F-001 High / base forwarder.go:25, manager.go:86 | VM key/server만 전달해 LabInstance/generation 유실. 코드 분석. Jira 4field/세대 검증 누락 | Target 4field 전달. propagation/Provider/live wrong target/Reset PASS |
| F-002 High / base app.go:203 | 실제 SSHForwarder 없어 production Preview 미사용. generic TCP 제공 시 SSH 검증 우회 여지. 코드 분석 | shared VMAccess 주입/cap 연동, production 구성 tests PASS |
| F-003 Medium / base access.go:88 | PTY/SFTP manager에 TCP 소유권 미구현. 코드 분석, 요구 기능 부재 | 독립 direct-tcpip, 격리/timeout/idle/invalidation/shutdown tests PASS |
| F-R001 High / 중간 runtime/contract.yaml:655 | note 들여쓰기 불일치로 YAML parse 실패. 독립 검토로 재현. Runtime SSOT 읽기 실패 | indent 수정, safe_load PASS |
| F-T001 Medium / 초기 live fixture:188 | Reset 결과의 구/신세대 중 구세대 server 선택. 실실행에서 새 VM unreachable로 오판 | generation2로 필터. 초기 FAIL 보존, 후속 live PASS |
| F-R002 Medium / tcp.go tcpConnection.Close, 최초 관측 행 미보관 | peer가 TCP CLOSE 무시, PTY 생존 중 raw Read가 Close 후 계속 대기. 독립 control PASS/fault FAIL 재현 | 최종 수정 tcp.go:130. local pipe로 caller I/O 해제, raw cleanup 분리. Close fault/race PASS |
| F-R003 Medium / tcp.go DialTCP slot 처리, 최초 관측 행 미보관 | timeout→late success에서 slot 선반환. 독립 재현 20peer channels/slots0, 한도 우회 | 최종 수정 tcp.go:34-63. 실제worker reserve/즉시managed wrapping/once release. 16유지/17거절/PTY보존/shutdown회수 PASS |
| F-R004 Medium / 중간 tcp.go:111 | EOF만으로 pumps 종료/slot 반환, CLOSE 전 내부 채널 미집계. 코드 분석, 최종 wait 제거 mutant에서20active/slots0 재현 | OpenChannel requests 종료까지 소유. EOF fault PASS/mutant 예상FAIL로 회귀 검출 |

최종 독립 코드 검토에서 추가 confirmed defect는 없었다. 원본 로그와 재현은 [독립 리뷰](independent-review/README.md)에 있다. Mutant 실행을 수정 전 실제 구현 실행으로 표시하지 않았다.

## 실패·보안·수명주기 분석

| 시나리오 | 기대 결과·검증·한계 |
| --- | --- |
| 잘못된/누락 target, 비활성/복수 관리 NIC, hostpin 불일치 | Provider fail closed, cached SSH도 매번 재검증. 기존 Provider tests/신규 target·pin tests/live target 거절. 사용자 인가는 SaaS 소유 |
| 임의 port/host 전환 | 1..65535에서22 제외, host 고정127.0.0.1. 값 반올림/대체 없음. wire assertion/실HTTP로 확인 |
| SSH open 무응답/cancel·응답 경합 | transport당 setup1worker, 취소 waiter는 추가worker 없음. late 결과도16budget 내 관리. fault/race |
| caller Close/read/write 경합, raw CLOSE 무응답, EOF-only | net.Pipe가 caller 즉시 해제, pumps+requests 종료까지 slot 유지. sibling PTY 보존. 비협조 peer 최대16점유, idle/invalid/shutdown 회수. deadline은 local pipe 제공 |
| open 직후 Reset/Cleanup/SSH 단절 | post-open 세대 재검사/retire, late 결과 Close. 기존 session 부활 없음. unit race/actual Reset. Cleanup handler의 기존 InvalidateBefore 유지 |
| 중복 attempt/다른 session target | 기존 manager 전후 double-check/BindTunnel, 실패conn Close. 변경은4field 전달. 기존 Preview 회귀. 전체 WSS 실환경은 후속 |
| idle·connection 교체·공유 채널 | users는 setup/active caller 포함, Close1회만 release. 다른 PTY/File 유지, caller 소멸 후 idle retire. orphan slot과 분리. 공유/idle/fault 검증 |
| 프로세스 종료/재시작·식별자 재사용 | transport는 메모리 소유/shutdown Close. 재시작은 Provider/pin 재검증. TCP 영속 복구 없음. durable operation 모델 변경 없음. process kill 실환경 미실행 |
| 부분 Provision/Reset 실패/cleanup | 성공 assertion 전에 반환 resource를 추적, exact IDs cleanup. UNKNOWN/reconciliation 기존 책임 유지. 이름으로 소유권을 추정해 삭제하지 않음 |
| API/404·실제 상태 | Lab tracked resource는 Delete 후 GET 부재 확인. 임시 keypair/관리SG는 기존 helper Delete 응답까지만 확인하며 독립GET 미실행. 최종 M1은 server/network 보조조회 |
| logging/Secret | key/cloud 원문 미기록, HTTP/PTY/SSH_CONNECTION 메모리만. Provider/SSH raw error는 safe sentinel. Evidence에서 실제 개인키/자격증명 검토 |
| 계약/runtime | 외부 message/field/error/cap 문자열 동일. 내부 interface caller는 전체 컴파일/tests. runtime note만 변경, YAML 파싱 |

## 실행 레이어와 Evidence 검토

최종 기본suite는 **41packages/3652tests(subtests 포함) PASS, 16skip, failure0**. [skip 목록](final-go-test-summary.json)에 OpenStack/KT opt-in 및 Windows symlink/open-file rename/POSIX permission 제약을 기록했다. PostgreSQL integration tag는 DB 변경이 없어 미실행. Go vet/build, 7Connector packages race, Web install/typecheck/lint/14files188tests/build를 실행했다. 마지막 구현 변경 후 Go 전체/race를 재실행했으며 Web source/lockfile은 불변이다.

make/npm 실행 파일 부재로 개별 동등명령 및 pnpm 제공 npm10.9.2를 사용했다. raw gofmt의 기존CRLF460개를 무관하게 수정하지 않고 Linux CI와 같은LF 기준470개를 검사해 차이0. 변경Go파일은gofmt 완료. Runtime YAML/migration metadata는 Contracts workflow와 같은 로직으로 PASS. 변경 없는 OpenAPI/JSONSchema의 Python validator 전체나 원격CI 성공은 주장하지 않는다. Web의 기존 canvas/bundle warning은 로그에 보존했다.

Fake Provider/in-process SSH·fault peer/Mock WSS와 실제 로컬 OpenStack/VM/SSH/HTTP/PTY/SFTP를 구분한다. 최종 live는 **211.62s PASS**, 실제 Reset gen1→2/이전target 거절/새VM접속 후 tracked Lab resource **21항목 부재 확인**. 21은 결과항목 수이며 deduplicated 고유리소스 개수는 아니다. 임시 관리SG/keypair cleanup도 helper 오류 없이 종료했다. 후속 읽기전용 M1은 images3/flavors4/servers0/networks4로 PASS했다. 기존 공유network는 유지했다. 전체 프로젝트 SG/keypair 잔여 inventory까지 검증한 것으로 확대하지 않는다.

초기 live 실패/중간 성공과 초기Go 결과를 숨기지 않고 README에서 구분했다. 과거 미커밋 source 전체hash 미보관 한계가 있어 해당 로그만으로 당시 정확한 source를 복원할 수 없다. 최종 hash/diff/실행 결과는 일치하며 최종 판정은 최종로그만 사용한다. 재현 입력/도구/cleanup 방식은 README/scripts에 명시했다. 인증 파일·개인키·콘솔 transcript·HTTP본문을 Evidence에 복사하지 않았다.

독립 reviewer `/root/review_lbt173`는 구현을 쓰지 않고 전체diff/최신Jira/D-18/Git SSOT/공유상태를 검토했다. F-R001~004를 보고했고 자체 external overlay/modfile fixture로 control/fault/EOF mutant/race를 실행했다. root가 수정 후 전체/실VM을 재검증했다. 최종 추가 confirmed defect 없음. 독립 검토는 코드/in-process 경계이며 OpenStack 별도 독립실행·Browser/KT acceptance는 아니다.

## 최종 판정과 남은 범위

**PR 가능: 로컬 SSH forwarding 구현 범위.** 관련 요구사항/기본검사/race/독립검토/실VM검증과cleanup을 충족했다. 원격 PR을 만들었다는 뜻은 아니다. 사용자 지시에 따라 로컬 브랜치에만 변경을 남긴다.

**LBT-173 전체 완료 판정은 보류.** KT 단독 smoke는 local-only 범위 밖으로 미실행이며, 환경 차이와 미검증 이유를 명시했다. Preview Control/Data 실환경 lifecycle과 Browser C3는 Jira가 제외한 후속 범위다. 비협조 SSH peer는 최대16채널 점유 후 fail closed하며 transport 수명까지 남을 수 있다. OS별skip/미실행 범위는 위에 기록했다.

Audit 통과는 push/PR/merge/deploy/외부 메시지 전송 승인으로 해석하지 않는다. 이 작업에서 해당 원격 변경은 하지 않았다.
