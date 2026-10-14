# LBT-148 PR #95 리뷰 보완 감사

검토 소스 `f4999aa080d4f026a8ee06019d12afa01fb8f3b0`, 이전 PR head `d509aac6faa873697273ee9c9fc1e681d8d33426`, fetched main `6b2c6dbd1cb495538367143f427dcd04905c3886`, diff merge-base `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6`. `PRE_MERGE_AUDIT.md` A–F를 적용한다. 이 문서는 [기존 전체 감사](../2026-10-14/pre-merge-audit.md)와 [최신 main 제출 감사](../2026-10-14-prepr/pre-merge-audit.md)의 추가 검토이며 이전 문서의 owner 검증 결론은 이번 P2로 보완한다. 실제 runtime 변경을 이전 VM PASS로 재검증했다고 주장하지 않는다.

## A. 전체 diff·버전

기존 PR44파일 및 이번에 PR 범위에 추가되는 WSS test2파일, 총 46파일을 검토했다. 전체 파일별 목적·git blob·SHA256은 source-manifest.json에 전수 기록한다. 기존 code13파일의 SSHPTY 종료/drain·late setup cleanup, Session/Data owner·input/resize/fatal fence·reconnect, Mock Preview writer와 실제 VM harness를 기존 AC/SSOT/전체 diff에 다시 대조했다. source 변경은 이번 4파일181삽입3삭제다.

| 이번 파일 | 목적·경계 |
| --- | --- |
| terminal/session.go:333 | owner 검증+CLOSED 전이를 claimDataClient/attachDataConn의 동일 mutex에서 원자적으로 수행. Close I/O/callback은 mutex 밖 |
| wss/handler.go:1122 | Attach 실패가 현재 owner일 때만 session cleanup. failed client 자체 Close는 항상 수행 |
| wss/handler_terminal_owner_test.go:19 | 다른 Handler가 같은 manager를 쓰는 Control reconnect 형태; ACK 대기 취소·새 attach 성공 후 옛 오류, PTY1개·echo·no ENDED·old socket cleanup |
| wss/handler_terminal_test.go:180 | 현재 owner의 endpoint validation 실패 시 기존 manager removal 외 실제 PTY closed-pipe 확인. AllowInsecure=false이므로 TCP dial/ACK 실패 검증으로 확대하지 않음 |
| 이전 evidence31파일 | original source/로그/실패·skip·재현 helper/scan/checksum 보존; 지난 감사의 미발견 경합을 본 문서와 negative log로 보완 |
| 이번 evidence9파일 | source manifest, 로컬 원본/요약, A–F 감사, 독립 리뷰, repro 안내, secret scan/checksum/byte preservation; 코드 추가 없음 |

main #94의 HTTP durable Reset/Cleanup15파일은 Connector/runtime/Go deps를 바꾸지 않는다. merge-tree write-tree 성공(충돌0), full PR 변경과 최신 main producer/consumer 통합 영향 확인. 원격 feature history는 보존한다. 실제 최신 base 통합은 PR의 GitHub merge ref CI에서 push 후 확인한다. HTTP/DB 변경은 이 PR source diff에 없으며 최신main 기능 구현을 이 PR 기능으로 주장하지 않는다. 미검토 구현 파일 없음.

## B. 최신 SSOT 추적

Atlassian Rovo 원문을 직접 재조회: LBT-148 updated 2026-10-14T09:19:08.151+0900/진행 중/댓글0, [D18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5, [D21](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516) v10, [MVP](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071) v19, [테스트 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) v3 동일. AGENTS/CONTRIBUTING/TESTING, owning Connector README§6/7와 terminal schemas, Realtime 수명/current/no-history, runtime SSH/secret/logging, 영향받는 consumer를 확인했다. Git 계약/runtime/deps/migration 변경0이다.

| 요구사항·AC | 원문·버전 | 구현·위치 | 자동 테스트·분기·실행 | 실제 VM Evidence | 공백·판정 |
| --- | --- | --- | --- | --- | --- |
| AC1 생성·INPUT/OUTPUT·초기 size/resize·종료 | [LBT148](https://samsunglions.atlassian.net/browse/LBT-148) 최신 | ssh_pty.go:63/225, session WriteInput/Resize/pump | 현재 SSH echo/resize/drain/final-output unit/race PASS | efc2258에서 실제 VM133.81s PASS; SSHPTY/harness 유지 | 이번 session/handler 변경은 새 Mock PTY+actual WSS 검증, 현재 exact-HEAD VM 미실행 |
| AC2 CC02 Control/Data 왕복 | Jira/Connector§6/7/schema | handler.go:1078/1122, client DialAndAttach/dispatch | 새 중복 OPEN2case 및 기존 OPEN/attach/reconnect/input/resize/close PASS | 이전 Git MockSaaS/Relay→실제 VM 결과 보존 | 새 동시성 경로 loopback WSS PASS, full Browser/SaaS C2 후속 |
| AC3 종료/실패 cleanup·다른 Channel 유지 | Jira/D18v5/D21v10 | session.go:333/Close, managed SSH Close | current-owner closed PTY, replaced-owner no close/no ENDED/PTY1/echo, 기존 sibling/late error/cancel PASS | 이전 sibling PTY/SFTP 및 exact-ID resource7 ABSENT 보존 | 이번 cloud mutation/packet fault 미실행, 경합 테스트에서 current state 유지 확인 |
| AC4 참조 가능한 Evidence | Jira/TESTING/전략v3 | historical+현재 evidence 및 tracked test | 원본 negative/positive/source equality/stability/checksum/scan/독립 리뷰 | 원본 VM 및 cleanup 기록 유지 | 당시 소스와 현재 변경 차이를 manifest에 명시; 로컬 재현 가능 |
| VM lazy shared SSH·독립 PTY/SFTP·host key | D18v5/runtime | VMAccess unchanged, ssh_pty.go managed factory | VMAccess/SSH loopback isolation/cancel/known_hosts PASS | 이전 shared SSH·SFTP 결과 | VMAccess live 코드 변경0, 사용자 지시로 재실행 제외; 별도 Reset/idle live를 충족 표시하지 않음 |
| 60초 grace·current1·same PTY·no replay | D21v10/Connector§7/Realtime | session.go:333/345/attach/grace/tombstone | 새 replacement owner 보존2case, old frame/failure/reconnect/grace/shutdown 회귀 PASS | 이전 reconnect shell/no-history/harness grace3s | 실제60초 wall-clock/Browser token/Live 후속; 설정·wire 변경0 |
| final bytes·exit/SSH 오류 분류 | Connector schemas/D21 | TerminationProvider, session.go:589 pump | final n+EOF/stdout+stderr/exit0·7/SSH error PASS | 이전 real exit7/final output/safe ENDED | final unread Data ENDED best effort; 원격수신 보장 아님 |
| Control/Data 분리·본문 비저장·credential/host key | AGENTS/Connector/Realtime/runtime | safe literals, in-memory stream/unchanged auth consumer | 기존 correlation/caps/revoke/host-key PASS, 새 실패 safe message | 기존 lifecycle-only VM 원본 | 이번 scan 특정 secret/PEM0; SaaS 권한/DB/배포 검증과 구분 |

## C. 실패·보안·수명주기

A는 owner가 된 뒤 ATTACHED/credential lookup에서 기다리고 B가 owner를 claim한다. claim은 A.Close로 socket/context를 취소한다. A의 실패 후 새 method는 dataClient!=A라서 B의 상태/PTY/transport에 영향이 없다. 반대로 A 실패가 먼저 owner mutex를 얻으면 CLOSED를 선점하고 B claim/attach는 ErrSessionClosed로 거절된다. 소유권 확인과 별도 mgr.CloseSession 사이의 TOCTOU가 없다. 실제 Close가 lock 밖이므로 SessionManager callback lock inversion이나 socket I/O hold가 추가되지 않는다. closeOnce와 정상 tombstone/physical PTY cleanup 경로를 그대로 사용한다.

실패한 A client 자체는 owner 여부와 무관하게 Close해 취소/context/socket cleanup을 보장한다. streaming은 successful attach에만 시작한다. 기존 safe FAILED OPEN_RESULT는 A requestId로 전달하고 B는 SUCCEEDED; 종료되지 않은 B에는 ENDED를 보내지 않는다. nil owner/이미Closed/claim실패는 새 owner를 닫지 않는다. Input/resize/current reader 검증, active sequential OPEN 재사용, generation/target/correlation reject, retry/backpressure/grace/security 제한은 바뀌지 않는다. wire field/credential 전달/본문 저장/Provider API/새 Manager/재시도 hierarchy를 추가하지 않았다.

미검증: forced restart/rolling deployment, 긴 tombstone 누적 및 process restart loss, 운영 Browser/SaaS/Live, 실제60초 wall-clock, 운영 SFTP/Preview VMAccess 통합. 기존 경계·후속 LBT22/LBT149/150와 같으며 본 P2에서 범위를 확대하지 않는다.

## D. 실행·skip·Evidence

수정 전 d509aac runtime+새 test만으로 두 케이스 모두 FAIL(negative expected exit1, 초기 실패 원본 유지). 동일 test blob은 수정 후 targeted working-tree(4파일 blob이 CODE와 일치)에서 race100회400PASS/skip0/fail0. CODE에서 전체 Go unit3432PASS/skip12/36pkg, Connector race695PASS/skip11/7pkg, 기존 race20회420PASS, fmt409/vet/build PASS, Web13file/167tests/typecheck/lint/build PASS, Contracts PASS. raw command/time/exit/event/source snapshot은 archive와 summary에 기록한다. auto/web/contracts source 전후 동일이고 lock 파일도 unchanged.

SSH TestSSHPTY_RealSSH_EchoAndResize는 이번에도 PASS하며 skip하지 않았다. 전체 unit skip12는 OpenStack/KT live opt-in9 + Windows symlink/replacement2 + dev-auth POSIX permission1이며 test별 이유는 summary에 전수 기록한다. Connector race skip11은 dev-auth1을 제외한 동일 범위다. VMAccess live 및 PTYCC02 live는 이번 실행 안 함이다. PTYCC02 이전133.81초 PASS는 historical source에 한정한다; 새 handler/session production은 바뀌었으므로 예전 exact-equivalence 표현은 이번 PR 본문에서 정정한다. 동시성 경로는 실제 WSS socket과 Mock PTY로 검증하며 VM/OpenStack 결과로 확대하지 않는다. DB tagged integration은 로컬 미실행이며 Go CI PostgreSQL16 integration 결과는 push 후 별도 확인한다.

재현 dependency는 tracked runner와 test, 고정된 도구/validator 입력이다. 개인 미추적 helper는 필요하지 않다. 새/기존 evidence 전체 ZIP entry CRC·원본 byte·체크섬을 검증하고 local actual credential/private-key/endpoint 특정값과 PEM marker를 제출 evidence 전체에서 검사한다. Secret 원문/config/key는 복사하거나 출력하지 않는다. universal secret detector라고 주장하지 않는다. 이번 cloud 생성/잔여 리소스 정리 행위 없음.

## E. 수정 전 finding 및 해결

| ID·심각도 | 수정 전 파일/라인·재현 조건 | 관찰·영향·근거 | 수정·회귀 |
| --- | --- | --- | --- |
| F-PR95-OPEN-OWNER /P2 | d509aac wss/handler.go:1121–1123. 같은 terminalSessionId A attach 미완료→B claim/attach→A cancel/늦은오류→mgr.CloseSession | B의 정상 PTY/session까지 CLOSED/ENDED; Connector 중복 lifecycle/idempotence와 D21 current attachment 위반. 리뷰 코드 분석 및 두 negative case 실제 재현 | session.go333 동일mutex owner+CLOSED fence, handler 실패client 자체 Close. 두case race100회+current-owner cleanup+전체기본/race 회귀 PASS, 독립 리뷰 추가결함 없음 |

finding을 사용자에게 severity/위치/순서/영향/수정·테스트로 먼저 보고한 뒤 수정했다. 이전 audit에서 이 handler 실패경로의 owner fence를 놓친 부분을 인정하고 별도 negative 기록을 남겼다. 기존 findings/초기실패 기록은 삭제하지 않았다.

패키지 검토에서 audit D의 전체 skip12를 cloud opt-in12로 묶은 표현 오류(P3)도 발견해 수정 전에 보고하고 정정했다. 원본은 cloud9/Windows2/POSIX1이며 summary의 전수 이유와 독립 재집계에 맞춰 수정했다. current-owner endpoint validation 실패를 실제 TCP dial/ACK 실패로 확대하지 않도록 테스트 한계도 명시했다. 코드·실행 결과 변경은 없다.

## F. 독립 리뷰·판정·승인 경계

구현하지 않은 review_148 reviewer가 전체 PR diff, source 변경4파일, 최신 SSOT 직접조회, owner/claim/Close/late failure/current-owner cleanup, raw negative/targeted/current full validation 및 source/evidence 한계를 독립 재검토했다. 신규8+기존27 artifact SHA256, ZIP58entry CRC/원본byte일치, source46개 blob/SHA도 확인했고 mismatch0이다. skip 분류 P3 정정과 current-owner endpoint 검증 한계까지 확인했다. 기록은 independent-review.md. root의 실제 테스트 실행과 reviewer의 read-only 분석을 구분한다.

로컬 검증과 독립 리뷰 범위에서 P2 해결 및 PR 업데이트 가능. 이후 docs-only evidence commit source 동일성을 확인하고 feature branch만 push한다. 원격 최신 PR head에서 CI와 merge ref base를 확인한 뒤 imcherry5778 재리뷰를 요청한다. main merge/auto-merge/배포는 금지하며 실행하지 않는다. 이는 Jira 전체/MVP/full C2 완료 판정이 아니다.
