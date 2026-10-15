# LBT-151 pre-merge audit — 2026-10-14

판정: **PR 가능 — 제안된 LBT-151 도구·회귀 테스트·Evidence·C2 인계 범위의 audit 통과**.
이 문서는 개인 `PRE_MERGE_AUDIT.md` A–F 절차의 기록이다. Audit를 push/PR/merge
승인으로 해석하지 않는다. Jira/Confluence 상태나 외부 메시지는 변경하지 않았다.

## A. 전체 diff와 실행 소스

Repository `ktcloud4-SL/labbit-app`, branch `feat/LBT-151-ktcloud-final-regression`.
최신 main을 fetch한 base와 실행 당시 HEAD는
`7917531fe630357eeb1f055ea2ad08dbee93a00e` (LBT-150 PR #101 merge)다.
production Go, web, dependency lock, HTTP/WSS/Runtime/DB SSOT는 이 base와 동일하다.
변경은 acceptance/bootstrap 도구, 해당 fixture 회귀 테스트와 새 LBT-151 Evidence다.
LBT-145 historical source 및 LBT-150 Evidence는 수정하지 않았다.

실행 당시 새 도구는 미커밋 상태다. `source-manifest.json`은 base HEAD와 **모든
compiled Go/go.mod/go.sum의 LF SHA256**을 함께 식별한다. 각 raw 실행에는
before/after manifest와 source-stability 판정이 있다. Windows CRLF는 비교 시에만
LF로 normalize한다. 마지막 commit의 Go tree를 이 목록에 대조하고 Evidence-only
추가를 검증 소스 변경과 구분한다. `binary-manifest.json`은 Linux/amd64 CGO0
Connector·harness·helper 및 Windows bootstrap의 build flags와 hash를 기록한다.
deployed 세 binary와 VM별 installed real helper hash를 actual run에서 비교한다.

| 파일 | 변경 목적·요구·검토 경계 |
| --- | --- |
| `tools/ktcloud-d1-acceptance/main.go` | opt-in OP02 TLS peer 조립; HELLO File capability, g1 정상/실패→Reset g2→Cleanup; bounded unmatched inbox; 최초+teardown 오류 보존; process Wait/forced-kill 구분 |
| `inbox_test.go` | ENDED-before-ACK 도착 순서에서 lifecycle 누락 방지 |
| `op02.go` | Terminal Control/Data tuple, 실제 PTY size/input/output/exit/reattach/grace, Tree/Read/Save/byte/revision/path 경계, g2 정상 후 stale g1 거절 |
| `op02_binding_test.go` | 같은 reply라도 foreign Lab/generation/Terminal/File ID를 거절 |
| `op02_identity_test.go` | ANSI/prompt와 전 프레임 분할, echo/invalid tuple 거절; 실제 trusted TLS Data frames로 connectionIdentity 호출 |
| `op02_peer.go` | synthetic SaaS auth/subprotocol/attach/correlation; bounded memory; 1 요청/1 File socket; SaaS 정상1000 close와 추가 frame 거절; lost-result 주입 |
| `op02_peer_test.go` | 잘못된 auth/attach, empty Binary 1개/0개/2개, 실패 결과 body 금지, Tree 누락·종류·symlink·중복 거절 |
| `op02_unknown_save_test.go` | production Handler+FileManager를 trusted TLS Control/Data로 연결; duplicate ACK 없는 completed SAVE 후 driver 자체 fresh Read/no replay; 수정 전 red, 후 green |
| `op02_ssh.go` | pinned fixture SSH로 current helper 설치·hash/owner/mode 확인, root-owned exec delay; actual same SSH tuple·idle TCP tuple·원격 process 관측 |
| `op02_failures.go` | unknown Save 후 fresh Read/no-redial, cancel/disconnect/단일4001/30s timeout, 독립 PTY, owned VM SSH 중단+systemd rollback, Reset/Cleanup 전 active 작업 종료 |
| bootstrap `main.go` | explicit KT/project/2-generation/phase gates, receipt ledger, copied compute lifecycle, durable public/forward flags, read-only observe 및 recovery/archive phases |
| bootstrap `access.go`, `access_unknown_test.go` | unknown create receipt가 inspection/restart 후에도 blind POST2로 바뀌지 않음; red2POST/green1POST |
| bootstrap `deploy.go` | 세 binary hash, protected remote inputs, helper/runtime keys, recover-observe/cleanup의 명시적 remote flags, actual known-ID residual0 후 archive |
| bootstrap `baseline.go` | seven-kind full inventory baseline와 final equality; 모든 shared baseline 유지 |
| bootstrap `cancellation.go`, `cancellation_test.go` | stalled connected SSH transport의 cancellation close |
| bootstrap `ssh_observe.go`, `ssh_observe_test.go` | registered public key equality, exact Server/project 및 read-only current NIC/policy match |
| bootstrap `access_test.go`, `ownership_test.go` | unknown policy candidate·foreign root 자동 소유/삭제 거절 |
| 두 README, `validate.py`, `export_evidence.py` 및 이 Evidence 폴더 전체 | fresh output, source/hash/exit/skip/실패 보존, typed export 및 secret/body 제외, 재현·audit·C2 handoff |

bootstrap 10개 Go 원본은 LBT-145 immutable `.go.txt`의 실행 가능한 복사본이다.
수정한 main/access/deploy 및 신규 regression 외 복사본의 LF hash를 원본과 비교한다.
기존 acceptance recovery/scope/SSH 파일의 working-tree EOL 변화는 logical diff가
없다. ignored `.local/`는 binary, protected clouds/key/host-pins, per-attempt ledger,
원본 로그와 build manifest의 역할만 수행한다. 실제 의존 소스/phase는 모두 추적된다.
protected input 내용과 binary는 공개 bundle에서 제외한다.
전체 최종 변경 파일 목록과 공개 문서의 LF hash/zip raw hash는
`artifact-manifest.json`, 독립 검토의 상세 범위·판정은 `independent-review.md`에 있다.

## 최신 원문 및 적용 SSOT

직접 읽은 [LBT-151](https://samsunglions.atlassian.net/browse/LBT-151):
updated `2026-10-07T16:04:41.390+0900`, comments0. parent LBT-21, 선행 LBT-145 및
LBT-149/150, 공동 C2 LBT-22의 범위를 대조했다. Jira의 Browser/SaaS→VM 최종 공동
통합 경계는 LBT-22이며 현재 TLS SaaS fixture를 실제 Backend라고 해석하지 않는다.

| 원문 | 직접 확인 버전·적용 |
| --- | --- |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | v5. VM별 private management SSH, key/host pin, shared lazy connection·별도 PTY/SFTP channels |
| [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) | v7. immutable Reset, UNKNOWN 실제 조회, known-only cleanup, candidate 미채택 |
| [D-21](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516) | v10. PTY60s grace, byte stream/ANSI, no history, Browser credential continuity는 C2 |
| [D-26](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/28672005) | v1. pre-release Reset correction; required previous refs·generation은 현재 Git 계약 owning |
| [Connector MVP 실행 계획](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071) | v19. M4 OP02 종합 회귀와 C2 후속 인계 |
| [검증 설계](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) | v3. unit/Mock/actual 구분, 요구→검증 추적 |
| `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md` | 전체 직접 읽음. latest main, 기본전체 검증, meaningful regression, CODEOWNER/CI/Squash PR 규칙 |
| `contracts/connector/README.md` §6/7/7a/9/11–15 및 네 Terminal/File schemas·공용 타입 | Control/Data 분리, tuple, SaaS 정상 close1000, Binary1개, duplicate no-redial, lifecycle, Reset refs/UNKNOWN/candidate ownership |
| `runtime/README.md`, `runtime/contract.yaml` | ssh idle1m, workspace root/helper, provider inputs/secret trust; production setting/contract 변경 없음 |
| HTTP OpenAPI, realtime README/schema, DB migrations, Backend 가이드 | changed producer는 synthetic Connector-side peer만. Browser/Backend/DB 구현·계약은 변경 없음; current workflow 계약 검증과 web/default suite로 정합화 확인. 실제 PostgreSQL 및 deployed Backend는 미실행 |

## B. SSOT 추적성

최종 attempt6는 2026-10-14 19:15:31–19:30:35 KST에 실행해 exit0이었다.
Bootstrap cleanup과 원래 seven-kind baseline 복원은 19:31:25 KST, 모든 이전
attempt의 exact ID 재조회는 19:32:10 KST에 통과했다. Lab40+Bootstrap8의 서로 다른
owned IDs48개가 모두 absent다. 공유 DMZ/Private/external Tier3개는 유지했다.

| 요구·AC | 원문·버전 | 구현 | 자동 검증 행동 | actual execution / evidence | 공백·판정 |
| --- | --- | --- | --- | --- | --- |
| latest main feature branch, build/unit/vet/회귀 | Jira151, repo지침; base791 | validator `auto/web/contracts/build` | Go unit3638 PASS events/15SKIP, vet/build/fmt; web13/170; 계약/Reset11; race+20회 | final source-stability true, current base 재fetch791 | 충족; PASS events는 parent/subtests 포함 |
| 실제 SSH key/pin, PTY 생성·입출력·resize·close | Jira151; D18v5; Connector§6/7 | main:418, op02:43/270/147, ssh:58 | existing VM/Terminal race, binding regressions | g1/g2 SSH/egress denial, PTY80x24→120x40, exit7; actual-final.log attempt6 PASS | g1/g2 모두 actual PASS |
| CC02 reconnect/grace 및 종료 ID 재사용 금지 | Jira151; D21v10; Connector§6/7 | op02:270/403 | Terminal ownership/reconnect20회; actualTLS parse | g1 Data 재attach shell state 유지, production60s grace 만료 및 old-ID OPEN 실패 | 실제 Browser credential refresh는 C2 |
| File Tree/Read/Save, revision/empty/raw bytes/path | Jira151; Connector§7a/schemas | op02:183/215/270/381 | empty Binary·Tree negative, File/Workspace race, unknownSave20회 | g1 actual rooted helper Tree/Read/Save/NUL/nonUTF8/empty/conflict/symlink/permission/large PASS; g2도 actual PASS | HTTP SaaS authority/Browser File는 C2 |
| PTY/SFTP 독립·동일 SSH transport·idle 수명 | Jira151; D18v5; Runtime1m | op02:93/270; ssh:123 | identityANSI/모든분할/actualTLS, vmaccess shared/cancel race | 2PTY+SFTP exact SSH_CONNECTION 일치, 한 PTY close 후 다른 PTY/File usable, exact cached TCP tuple65s 이후 absent PASS | actual distinct-path 두 SFTP helper2→1→0 및 other File Data/PTY 유지 PASS. 공유 probe는 latest helper 한 개 tuple, 각 helper별 tuple 독립 기록 없음 |
| VM 도달불가·SSH 단절·timeout/cancel | Jira151; D18/D21; Connector§7a | failures:31/129 | File owner/cancel/revoke/unknownSave20회; SSH cancellation | owned VM systemd SSH rollback, active PTY ended, safe File/Terminal failure, helper present→0; attempt6 PASS | blackhole/partial-save transport·full credential revoke는 actual 미실행 |
| stale target/generation, Reset/Cleanup 정리 | Jira151; D20/D26; Connector§11 | main:474–573; op02:443; failures:187 | ResetReality/recovery negative/foreign tuple; schema11; successful-Cleanup fence 기존 회귀 | SaaS activeclose→helper0→Reset; g1absent/g2present; 정상g2 뒤 ACTIVEg2+staleg1 fail; 첫Cleanup 전activeclose/helper0; final10IDabsence 및 g2접근실패; syntheticData0/childWait0 PASS | 실제 ID absence와 runtime generation 거절을 각각 확인 |
| known-only mutation, UNKNOWN reconcile, 후보 미삭제 | D20v7; Connector§12/13; KT profile | main:175/394/536; bootstrap recovery/access | 24 uncertain recovery negatives, unknown-create restart Fake red/green | attempt1 volumeUNKNOWN 후 5known 조회 present3/absent2→남은3 cleanup SUCCEEDED. 후보 관측은 candidateOwnership=false | actual provider Create 응답 유실 주입은 안 함; receipt없는 후보 자동처리 금지 |
| 이번 모든 test resource residual0 | Jira151; D20; KTprofile | bootstrap residual:80/final baseline; exporter:107–143 | ownership/candidate/cancel unit | all-attempt-residuals.json: 6attempt/40Lab+8Bootstrap=48exact IDs absent; final-inventory.json: 7종 inventory 원래baseline과 동일; protected original refs 모두 current query에 포함 | 충족. baseline Tier는 owned 집합에 넣지 않음 |
| revision/repro/results/KnownLimit/reviewable PR | Jira151 완료조건; 테스트전략v3 | 이 폴더·tool README·source/raw manifests | source/hash/exit/skip 기록 및 secret pattern scan | base+uncommitted LF source 전체, 4binary hashes, 최초fail+recovery 보존, 447raw entries·typed journals export; 독립 source/Evidence/A–F review PASS | proposed PR 충족; 원격 PR은 사용자 별도 지시 단계 |
| C2 Terminal/CC02·File Workspace 입력 인계 | Jira151/22; MVPv19 M4 | C2-HANDOFF.md | owning contract/runtime 검토 | actual 접속 조건·Data 경로·helper 설치·default deadline·failed attempt/KnownLimit 링크 | C2 공동 Backend/Browser/Lifecycle 작업은 미실행 |

## C. 실패·보안·수명주기

| 시나리오 | 검토·검증·남은 위험 |
| --- | --- |
| unknown access receipt→inspect→restart→create | ledger durable attempt flags를 POST 전에 저장. Fake HTTP red2POST→green1POST; 원래 Stage overwrite도 flags 지우지 않음. actual 응답 유실 주입 없음 |
| malformed/foreign File/Terminal frames | exact Control/Data tuple, trustedTLS/subprotocol/auth; TestControlEvidenceRejectsForeignIdentity / PeerRejects... / EmptyRead... PASS. actual SaaS auth/DB authority 대체 주장 안 함 |
| PTY framing/ANSI/partial fourth field | complete-line marker+IP/port validate; source echo는 split printf로 marker 미포함; TLS WSS/fragment regression PASS. 최초 actual parser failure 특정 bytes는 저장 안 했으므로 ANSI가 당시 원인이라는 것은 추정 |
| ENDED가 mutation ACK보다 먼저 도착 | bounded unmatched inbox가 preserve; ENDED-before-ACK regression. overflow는 실패 처리. Heartbeat 별도 처리 |
| 여러 channel 중 하나 close/cancel | g1 2PTY+SFTP shared tuple, 독립 PTY/File usable, remote delay helper 종료 및 SSHidle TCP 관측. concurrent helper의 shared probe는 latest writer 한 개 tuple이다. 단일4001은 전체 credential revoke아님 |
| Save 결과 유실/중복 old ID | synthetic SaaS application에서 수신 SUCCEEDED 버림; actual file committed bytes fresh Read 확인, oldID no-redial. 부분write rollback·transport-level unknown은 보장 안 함 |
| 30s File timeout / 60s Terminal grace / idle1m | production 기본값 유지. timeouthelper exec는 orphan child 없이 channel 종료 시 process 종료 관측. fixture-only Provider/SSH setup40s가 product default 의미를 바꾸지 않음 |
| VM SSH 자체 종료 | tracked disposable VM의 systemd unit이 listener/sshd 중단 후25s rollback; Terminal/SSH session은 부활로 해석 안 함. explicit fresh SSH/Terminal probe. rollback실패 시 cloud known-ID cleanup 가능 |
| Reset/Cleanup/stale generation | SaaS contract순서 activeclose+remotehelper0→mutation. immutable snapshot과 이전5typed refs. g2 실제정상 먼저 검증 후 ACTIVEg2/staleg1 negative. 후보·emptyCleanup은 소유권 부여하지 않음 |
| partial cleanup/UNKNOWN 재실행 | 최초 actual SERVERdeleted/VOLUMEunknown/후속notattempted 기록; 전체 typed Reconcile 후 present-only3 cleanup. UNKNOWN을 PASS로 변경하지 않음. attempt2는 SSHReady3m failure 후5 cleanup SUCCEEDED |
| archive/restart ownership | collect현재remote ledger→exactIDsactualquery Lab0→고정protected siblingmove; 살아있는Connector면거절. 이전resourceledger는 원본별 별도 보존. unknown candidate는 archive로 해결됐다고 주장 안 함 |
| process/resource 종료 | runErr errors.Join으로 first/defer 오류 함께보존; successfulWait<=5s와 forcedKillfailure 분리. syntheticpeer handler/socket0, delayhelper0, actual cloudIDabsence 별도; 내부 goroutine census는 없음 |
| security/logging/persistence | verified KT TLS 및 SaaSfixtureCA, ServerUUID hostpin, keyauth/privateNIC, root-owned helper, rooted paths/symlink/permissions. stdout은 safe lifecycle/assertion/hash만. 실제 PTY/File response body·목록은 bounded memory만; wrapper의 .helper-connection은 disposable VM의 transport lifecycle metadata이며 VM과 함께 삭제된다; protected clouds/key/raw Provider inputs 제외. publication typed allowlist+scan+사람리뷰 |
| no applicable migration/product change | production/API/web/DB/Runtime 변경 없음. 실제 DB migration apply 및 Browser/Backend E2E는 이번 fixture와 무관, current workflow parsing+normal suite는 실행. C2를 완료로 표시하지 않음 |

## D. 실행·Mock·actual·skip·미실행

자동 기본 검증은 최종 `auto-run6-final`: fmt465, vet, Go unit3638 PASS events/
15SKIP/0FAIL/41package, build; Connector7package race708PASS/13SKIP;
no-test package11개는 named SKIP15개와 별도다.
Terminal/PTY/preview ownership20회420PASS; File/Workspace/parser5package race74PASS/1SKIP;
두 fixture race20회2500PASS; File owner/unknownSave20회320PASS. 각 command/exit/count와
원본 stdout/stderr는 zip에 있다. SKIP15의 이름·출력·조건은
`default-suite-skips.json`: cloud opt-in 11 및 Windows/POSIX filesystem4. 실제 KT
Control suite는 그 default SKIP과 별도 opt-in 실행이다. PostgreSQL integration
build-tag suite는 미실행(DB/Backend 변경 없음), actual Browser/SaaS/Live도 미실행.

웹은 최신 unchanged web source로 typecheck/lint/build와 Vitest13file/170case PASS;
원본 npm lock 동일. Contracts는 current Actions script/OpenAPI/schema/runtime/migration
number parsing/Reset11 PASS. Python syntax는 exporter/validator compile 검사를 수행한다.

원본 실패를 보존한다: unknown-create Fake red2POST; 초기 잘못된 build target은 실행
전 setup 실패(Cloud 영향 없음); `auto-final` 소스가 실행 중 바뀌어 stable=false인
superseded 결과; reviewed-old 및 retry auto 결과; actual attempt1 SSH identity 실패+
cleanupUNKNOWN, read-only then present3 recovery cleanup; attempt2 production SSH Ready
3m timeout+5 cleanupSUCCEEDED; CLI recovery의 argument/private-key format preflight
실패(원격 실행/Cloud mutation 없음). attempt3는 정상/idle/grace 이후 fixture duplicate ACK 대기 timeout,
deferred5cleanup SUCCEEDED다. attempt4는 unknown Save PASS 후 추가 fixture pinned SSH unavailable,
deferred5 cleanup SUCCEEDED다. 원인은 미확인이다. 같은 경로의 helper 직렬화 문제는
별도로 code review에서 확인했다. attempt5는 g1 전체와 Reset/g2 정상·stale·candidate
검증 후 empty Cleanup 성공으로 접근이 종료된 g2 Terminal을 다시 OPEN하려다 실패했다.
deferred5 cleanup SUCCEEDED 및 이전 세대 포함10 IDs absent다. 코드의 lifecycle fence와
raw 단계 순서가 이 실패에 부합하며 exact 실패 ACK는 로그에 남기지 않았다.
새 actual attempt6는 활성 종료를 첫 Cleanup 이전으로 옮긴 별도 fresh Lab/ledger다.
모든 actual 시작은 latest binary hash를 verify했다. 최초 정상 File close finding은
SaaS owns1000 계약과 대조해 기각했다.

final source/binary/hash, per-attempt ledger, residual 및 seven-kind baseline equality를
일치 및 absence/baseline을 확인해 actual PASS로 판정했다. build-final 두 이전 빌드는 실제 control에
사용하지 않았고 build-live-final(첫 실패), retry build-final(두 번째 실패), final
build-final(세 번째 실패), run4 build-final(네 번째 실패), run5 build-final(다섯 번째 실패),
run6 build-final(최종 실행)을 구분한다. 로그 export의 원본 SHA·published SHA·IPv4
redaction 개수는 raw manifest에 남긴다. 공개 zip은447 entries/3,560,160bytes이며
IPv4 제거1건이다. Credential/IP/body 원본은 공개하지 않는다. actual-final.log와
failed-attempt-1..5.log는 전체 Control 실행 로그이며 raw archive와 상호 대조한다.
zip447 published SHA/length/CRC, standalone6 byte equality 및 보호 원본447 SHA와
typed export 원본 SHA를 재대조했다. 공개 문서의 제한된 secret pattern scan도
통과했다. 이 검사는 포괄적인 DLP 보증이나 실제 Cloud 직접 독립 재조회가 아니다.

## E. 수정 전 findings와 disposition

아래 위치는 해당 finding 시점의 local fixture revision이다. production결함으로
확대하지 않는다. 사용자에게 수정 전에 severity/파일/조건/영향/회귀를 보고했다.

| ID·severity | pre-fix 위치·재현/영향 | SSOT·수정·재검증 |
| --- | --- | --- |
| F-FIX-151-001 / P1 | copied access:221–238/main:507. POST receipt unknown→inspection Stage overwrite→restart→POST2; duplicate billable resource risk | D20/no blind retry. Public/Forward durable flags beforePOST; Fake bothred2/green1, race20PASS |
| F-FIX-151-002 / P2 (initial F-ASM alias) | prior main:214–217 Control만 TLS이며 Data404; historic deploy:607–643 helper/runtime 미준비; actualFile final regression 불가 | Connector§7a/Runtime. strictTLS Data peer/currenthelper deployment/capability; automaticpeer+actualg1PASS, g2 actual PASS |
| F-OBS-151-004 / P2 | old op02:263–265 TreeOutcome만 확인→empty/wrongkind/symlink list falsePASS | §7a. validateFixtureTree+negative cases; actual Tree normal+boundaries |
| F-OBS-151-005 / P2 | old op02:51–57/125–145/172–178 OPEN/ENDED에서 foreign Lab/gen tuple falsePASS | schemas/commoncorrelation. controlBinding+foreign tuple negative tests; actualexit/reconnect/grace |
| F-OBS-151-006 / P2 | old op02:413–431 deletedoldServer404만 stalegen proof로 취급 | D18/currentgeneration. g2 normal(helper+Filegood) 다음 ACTIVEg2Server+g1 요청 실패; actualg2 actual PASS |
| F-OBS-151-007 / P2 | old main defer가 최초assertionerror를 cleanup/shutdownerror로 덮음 | auditrawfailures. errors.Join; first actual twoerror 동시보존 확인 |
| F-RUN-151-008 / P2 | original main:281–289 forcedKill 후 Wait join 없음·graceful 여부 미분리 | resource teardown. bounded Wait join, killfailure flag; actualsuccessful5s/noForcedKill 확인. forcedbranch unit/실환경신규주입 없음 |
| F-RUN-151-009 / P2 (reviewer F-FIX-151-008 alias) | old op02:110–116 ANSIprefix rejected, partialport2 조기반환. actualfirstSSHidentitytimeout; 정확한firstrawbytes 저장안해 원인연결은추정 | D21/§7 bytestream. completeline+validIP/port; allsplit/ANSI/prompt/echo/malformed + actualTLSframes20PASS, actualg1sharingPASS |
| F-RUN-151-010 / P2 | old bootstrap deploy:712 localrecoveryoptin이 remoteenvironment에 전달안됨→priorledger recovery 실행불가 | D20/fixture recover. explicit recover-observe/cleanup +OP02off; actual5knownpresent3readonly→present-only3cleanup+allabsencePASS |
| F-RUN-151-011 / P2 (reviewer F-FIX-151-009 alias) | run3 op02_failures:107–122 completed File ID를 새messageID로 resend 후 mandatory 새ACK await→10s timeout. production handler는 reused admission 후 notify 없이 반환 | Connector§7a duplicate no-secondData; 새ACK 보장 없음. fresh Read Control 진행+actual committed state,5s no-redial guard. production Handler+Manager TLS fixture red timeout→green PASS; run6 actual PASS |
| F-RUN-151-012 / P2 | run4 source op02_failures:98/102 두 요청이 같은 path=file, service.go:338 lock 때문에 second helper 시작 못함; first cancel 전 count2 대기는 정상 production에서도 성립하지 않음 | D18 independent SFTP / service same-path consistency. file/empty distinct 경로로 수정; existing concurrent stale-writer unit 및 automated actual 2→1→0/helper independence 재검증. actual run4 primary SSH unavailable와의 원인 연결은 하지 않음 |
| F-RUN-151-013 / P2 | run5 main:534/548 empty Cleanup SUCCEEDED 뒤 g2 Terminal OPEN. app/vm_access:68–75는 빈 refs도 InvalidateBefore(3), vmaccess/access:196–202는 g2 OPEN 거절. 물리 자원 유지와 접근 지속을 혼동한 fixture 순서 | Connector§6/7a lifecycle. beforeMutation을 첫 empty Cleanup 앞으로 이동. 독립 reviewer가 기존 succeeded-Cleanup fence 및 empty Cleanup DELETE0 race PASS; actual6 active Close/helper0→emptyCleanup→known5present→Cleanup5→known10absent PASS |
| F-OBS-151-003 / withdrawn | reviewer initially required Connector-owned normal File close | owning README§7a says SaaS normalclose1000. 근거대조후 reviewer기각. strict1000 reply/noextraframe 관측은 강화했으나 productionbug로 기록하지 않음 |
| F-EVD-151-014 / withdrawn | reviewer가 Bootstrap8을 Tier1/Policy2로 가정해 owned count의 Tier 누락을 제안 | main:292–304는 기존 DMZ/External 선택, access:243–248는 Policy3 생성. 실제 Tier∈baseline 및 policy3 확인; reviewer 독립 대조 후 기각. Server1/Volume1/Key1/Policy3/Public1/Forward1=8이 맞다. shared Tier를 owned로 채택하지 않음 |

attempt2 SSH Ready timeout 및 attempt1 volume absence UNKNOWN은 실제 failed outcomes다.
현재 특정 production defect를 재현했다고 단정하지 않는다. C2 환경은 SSH ready가
default3m 안에 성립하는지 확인해야 한다; Cinder/NSM absence lookup이 불확실하면
reconciliation을 수행한다. 성공한 후속run이 이전 실패를 지우거나 reliability를
증명하지 않는다.

## F. 독립 두 번째 리뷰와 최종 판정

구현하지 않은 `/root/review_lbt149`를 reviewer로 재사용했다. 최신 requirements,
base791, whole new fixture diff를 직접검토했고 protected credentials/Cloud calls나
코드수정을 하지 않았다. Tree/tuple/stalegeneration/firsterror/identity findings를
발견·원문대조·수정재리뷰했다. F013 직전27Go source hash를 실행 전후비교하여 동일한
상태의 독립 `go test -race` 두 tool package PASS, `git diff --check` PASS다.
그 뒤 바뀐 main.go 한 파일의 F013 reorder를 독립 검토하고 기존 lifecycle/provider
회귀를 별도 race 실행해 PASS했다. 최종 auto-run6 전체/race/20회 검증은 구현 agent가
실행했으며, reviewer는 build/auto before/after467개 LF hashes와 현재 source의 완전
일치·stability=true를 독립 대조했다. 독립 전체 tool race를 최종 소스에서 재실행한
것으로 확대하지 않는다.
최종 reviewer는 실제 단계35개와 실패1..5 full 로그, ZIP447 SHA/length/CRC,
raw 자동검사 재집계 및15SKIP, original⊆current, Lab40+Bootstrap8=48와 실제 조회
chronology, seven-kind baseline을 공개 Evidence·조회 구현으로 독립 대조했다.
**제안된 PR 범위의 audit PASS, 미해결 confirmed blocker 없음**이다.
reviewer는 Cloud/API/VM/SSH 및 보호 원본을 직접 재조회하지 않았다. 잔여 확인은
exact current-ID 조회 코드와 aggregate 결과·원본 포함성·시간 순서의 증거이며
각 ID별 signed receipt를 제공하지 않는 한계를 유지한다. 보호 원본447 hash는
구현 agent가 대조한 것으로 구분했다. 상세 독립 기록은 `independent-review.md`다.

Known Limit: synthetic SaaS/application-lost-result/singleFile4001/socket census 범위를
유지한다. 내부전체goroutinecount, fullcredentialrevocation+newUpgrade401, 실제Browser
refresh/sessionStorage/authority/Live, blackhole·partialSave, deployedSaaS/DB 및 운영부하
검증은 여기서 완료하지 않았다. actualKTconcurrentFile는 이전 localVM Evidence와
currentunitcoverage와 구분한다. C2 접속 입력은 handoff에 정리하며 testVM은 보존용
운영자원이 아니므로 cleanup이끝나면 재사용할 수 없다.

최종 actual 정상/실패 회귀, owned residual0와 원래 baseline, 실행 소스 일치 및
독립 Evidence 리뷰가 충족됐다. 로컬 commit 후 같은 Go tree인지 마지막으로
대조한다. 외부 push/PR/merge는 별도 사용자 지시 범위를 따르며 원격 PR 생성과
CODEOWNER/CI/Squash 절차 전 Jira 전체완료로 표시하지 않는다.
