# LBT-147 로컬 OpenStack 실환경 검증

2026-10-12 KST. **현재 P1/P2 수정본의 로컬 OpenStack 검증 PASS.** 실제 VM 정상 접속·채널·Reset과 실제 Provider/app 경계의 실패·취소를 확인했다. 생성 리소스의 exact-ID 부재 및 마지막 전체 목록 조회도 통과했다. 독립 reviewer가 추가 confirmed production finding 없이 PASS로 판정했다.

사용자의 PRE_MERGE_AUDIT.md 전체 절차를 적용했다. 실제 실행 당시에는 `labbit-test` 로컬 cloud 검증만 허용됐으며 commit/push/PR/Jira 수정은 하지 않았다. 이후 사용자가 작업 브랜치 commit/push/PR 생성을 명시적으로 요청했다. 이 문서는 audit-logs.zip의 `automated/AUDIT.md`에 보존한 전체 29파일 감사와 `real/AUDIT.md`의 실제 실행 기록을 배포 가능한 형태로 정리한다.

## 소스와 최신 기준

| 항목 | 검증 결과 |
| --- | --- |
| Checkout / branch | repository root / `feat/LBT-147-vm-access` |
| HEAD | `399681789572487bd44469ec018e52b53f97678e` |
| PR merge-base | `6d675c74bff11f481d247267faefd4ce960dcd38` |
| 최신 origin/main | 다시 fetch 성공: `e850567066134edc87b2ba543d0eb42a21f406b8` |
| 새 main 영향 | PR #81 `docs/frontend/deployment-handoff.md` 한 파일만 변경. 전체 delta와 실제 WSS routing·후속 E2E 경계를 직접 읽었고 reviewer도 확인. LBT-147 production/contract/runtime 변경 없음. rebase/merge 하지 않음 |
| 실행 소스 | HEAD + 앞서 수정한 미커밋 6개 파일. 전체 고유29개 파일 byte SHA가 이전 감사, pre-run, post-run 모두 동일 |
| 검증 보조 코드 | Go `-overlay`로 외부 `_test.go` 2개를 가상 추가. production 소스 수정 없음. [source-manifest.json](source-manifest.json)에 29개 파일·6개 local change·보조 코드 SHA 기록 |
| 원문 요구사항 | Jira LBT-147/145/21 본문·최신 댓글을 다시 조회. 본문/AC·145 댓글 내용 불변. 147/21 상태는 진행 중으로 갱신. D-18v5 / D-20v7 / D-21v10 / MVPv19 / 테스트 전략v3 동일. [최신 원문 링크·버전·본문 비교 해시](ssot-summary.json) |
| 실행 환경 | Windows amd64, Go1.27.1, CGO/race 가능. 로컬 실제 Keystone/Glance/Nova/Neutron와 Ubuntu/m1.small/sharednet1 Management VM. 기존 로컬 SSH 개인키 사용 |

인증 설정과 개인키는 테스트/스캔 프로세스에서만 읽었다. Evidence에 credential/token/private key 또는 Terminal/SFTP 본문을 복사하지 않았다. `clouds.yaml`, 개인키, host pin 파일은 별도 로컬 입력이며 저장소의 계약 필드가 아니다.

## 실제 실행 결과

| 실행 | 결과·범위 | 원본 |
| --- | --- | --- |
| 사전 M1 discovery | PASS. 이미지3·flavor4·서버0·network4. mutation flag=0 | [pre-discovery.jsonl](pre-discovery.jsonl) |
| 기존 실제 VM acceptance, race | PASS 229.83s. gen1 Provision → authenticated/pinned SSH → strict target rejection → shared SSH/PTY/SFTP/idle → gen2 Reset/stale1 reject → 14 exact tracked resources ABSENT | [real-vm-access.jsonl](real-vm-access.jsonl) |
| 현재 app P1/P2 실제 boundary, race | PASS 227.71s. P2 2개 + P1 5개 subtest 모두 PASS. 실제 Provider 결과와 app wrapper를 통과. 16 tracked resources·임시 SG/keypair ABSENT | [real-app-boundaries.jsonl](real-app-boundaries.jsonl), [보조 테스트 archive](audit-logs.zip) (`real/app_real_integration_test.go`) |
| 사후 M1 discovery + read-only residue, race | PASS. 서버0·이미지3·flavor4·network4. test 이름 prefix의 server/network/subnet/SG/port/keypair/router 모두0. 인증 POST 외에는 List/GET 전용 | [post-discovery-and-residual.jsonl](post-discovery-and-residual.jsonl), [보조 테스트 archive](audit-logs.zip) (`real/read_only_residual_test.go`) |

선택 실행의 fail/skip은 0이다. race detector 오류가 없다. [validation-summary.json](validation-summary.json)에 테스트/시간/범위/최종 inventory를 기계 판독 형태로 기록했다. 다른 opt-in Provider integration suite는 선택하지 않았다. 이전 전체 Go/Web/contracts 검증은 같은 29개 소스 해시의 [앞선 결과](automated-validation-summary.json)를 이어 사용했으며 이번에 재실행했다고 표시하지 않는다.

## P1/P2 실환경 결과와 장애 주입 구분

원래 findings는 수정 전에 사용자에게 보고했고 사용자 지시로 이미 수정했다. 이번 검증에서 새로운 production 수정은 없다.

| Finding / 원래 revision3996817 위치 | 실제 검증 | 판정 |
| --- | --- | --- |
| F-001 / P1 / `app/vm_access.go:63,70` | 실제 worker DELETE 및 독립 SDK GET404. workspace DELETE는 loopback proxy에서 HTTP409를 주입해 실제 workspace를 유지. 실제 Adapter가 FAILED/DELETED/DELETE_FAILED/DELETE_NOT_ATTEMPTED를 생성하고 app.accessLifecycleProvider가 살아 있는 workspace SSH를 종료. stale gen1은 실제 resolver를 부르기 전에 거절 | 해소, partial Reset PASS |
| F-001 추가 lifecycle 분기 | 실제 없는 flavor Reset preflight는 모든 자원을 NOT_ATTEMPTED로 보고하고 active SSH/reopen 유지. first DELETE409는 보수적으로 retire/fence. Cleanup은 이미 삭제된 worker의 실제 DELETE404 뒤 workspace409에서 retire/fence. replacement Reset은 old10개 실제 cleanup 뒤 gen2 Neutron6개를 실제 생성하고 Nova POST409 주입; FAILED 결과에도 oldgen fence | P1 5개 PASS. Cleanup partial의 worker는 앞선 Reset에서 이미 삭제된 자원이며 새로운 worker 삭제로 계산하지 않음 |
| F-002 / P2 / `app/runtime_provider.go:53-58` | 실제 Keystone가 발급한 정상 인증 응답을 proxy에서 잠시 지연. ListImages waiter80ms, VM manager SetupTimeout40ms 종료. blocked waiter cancel은 owner/live waiter를 취소하지 않음. 실제 Glance/Nova 조회 성공 및 factory1/cache. 별도 owner cancel은 다른 waiter를 safe shared failure로 해제하고 다음 요청이 실제 재인증/factory2/cache | 해소, P2 2개 PASS |

HTTP409는 OpenStack 자체가 발생시킨 장애가 아니다. 정확한 테스트 소유 server ID의 DELETE409 3회와 Nova의 exact host/project path + 테스트 lab metadata/gen2에 맞는 서버 POST409 1회를 **전달 전에 주입**했다. 나머지 API·SSH·삭제/조회·새 Neutron 생성은 실제 외부 시스템이다. proxy는 private/loopback HTTP endpoint만 허용하고 원래 upstream transport를 사용하며 인증 payload를 기록하지 않는다. 실제 token을 그대로 사용하고 가짜 Provider 결과로 치환하지 않는다.

실환경 보조 코드의 waiter 참여는 짧은 scheduling 대기로 조율한다. owner cancel 사례는 살아 있는 waiter가 shared safe failure를 받는 결과까지 확인하지만, 모든 동시 도착 순서를 실제 환경에서 강제한 것은 아니다. 참여/취소 순서를 결정적으로 제어한 기존 회귀 테스트와 함께 판정한다.

## 요구사항 → 구현 → 테스트 → Evidence 추적

전체29개 파일별 diff/producer/consumer/설정/계약 검토와 기존 자동 회귀의 상세 표는 [앞선 전체 감사 archive](audit-logs.zip) (`automated/AUDIT.md`)에 고정했다. 소스가 바뀌지 않았고 이번에 reviewer가 29개 byte SHA와 새 main 영향, 실제 Evidence를 다시 확인했다. 미검토 diff 파일은 없다. 아래 표는 최신 실제 실행으로 갱신한 범위다.

| AC / owning SSOT | 구현 | 자동/실제 검증 | 공백·판정 |
| --- | --- | --- | --- |
| LBT-147 target affinity, 완료2 | `openstack/terminal_target.go:29-108`, `vmaccess/access.go:82-183` | 기존 target mismatch unit/race + 현재 실제 wrong lab/gen/VM pre-SSH 거절 | 로컬 충족 |
| Management private IP/user/key, D-18v5 | `terminal_target.go`, `access.go:311` | 실제 Management VM authenticated SSH 두 세대 + app fixture 두 VM | 로컬 충족, KT145 별도 |
| Server별 host pin 재사용, 작업2 | `terminal_target.go:111-145`, `access.go:328` | 실제 정상 host pin/gen2 + 기존 unknown/changed key unit | 정상 실제 충족. 공격/host-key 교체 실환경 주입 미실행 |
| VM lazy/share, 작업3 | `access.go:70-183`, `runtime_provider.go:56-105` | 실제 SSH_CONNECTION 메모리 비교, P2 실제 auth 지연·deadline·cancel·owner cancel/retry/cache | 로컬 충족. 다중 VM/장기 부하 미실행 |
| 독립 PTY/SFTP Channel, 작업4·완료4, D-18 | `vmaccess.Session`, `terminal/ssh_pty.go` | 실제 PTY IO/resize, SFTP INIT/VERSION, PTY close 뒤 SFTP REALPATH 성공 | 연결 경계 충족. production SFTP 파일 read/write는 LBT-149/150 |
| configurable idle, 작업5, Runtime0.1.7 | `app/vm_access.go:14-23`, `access.go:236-250` | 기존 기본1m/양수duration unit + 실제 idle 이후 transport identity 변경 | 로컬 충족 |
| 실제 SSH 생성/종료, 완료1 | manager + 실제 VM acceptance | 현재 source의 실제 gen1/gen2 연결 및 종료·cleanup PASS | 과거 commit Evidence 의존 공백 해소 |
| auth/network/handshake/cancel/disconnect cleanup, 작업6 | `access.go:120-183,222-340`, lazy.get | 기존 SSH 실패 fixture/race + 이번 실제 auth initialization 취소/deadline/retry | 취소 경계 보완. 실제 SSH auth 거절/packet loss/handshake stall 주입은 미실행 |
| old channel/new generation 차단, 작업7, D-20v7 | `app/vm_access.go:59-94`, `access.go:191-214` | 실제 정상 Reset/stale1 + 이번 살아 있는 workspace partial-failure retire/fence + replacement-failure fence | P1 해소. late resolve/setup·다른Lab 보호는 기존 SSH fixture/race |
| secret/raw error/본문 비노출, 작업7, D-21v10 | safe SSH errors, lazy sentinel, Control/Data errors | 기존 redaction 회귀 + 이번 metadata-only logs, known-local-secret exact scan/PEM/possible-token scan | scoped scan0. Git history/모든 unknown token 검증 아님 |
| PTY/SFTP 소비자 경계, 완료4 | app wiring/managed PTY | 기존 Mock SaaS/Relay+SSH peer runtime 회귀 + 이번 실제 SSH/PTY/SFTP manager probe | 실제 Browser/SaaS/CC-02 왕복은 LBT-148/22/26/27 후속 |
| Jira145/21 선행·후속 순서 | 구현/계약 변경 없음 | 최신 원문 및 새 main handoff 확인 | KT145 Reset/Tier2 부재 미완료 유지. 이번 로컬 PASS로 KT 또는 OP-02 전체 완료 처리하지 않음 |

## 실패·보안·리소스 수명주기

실제 mutation 전 exact-ID cleanup을 등록했다. app subtest 종료 시 session/manager를 먼저 닫고, 부모 cleanup에서 주입을 해제한 뒤 별도 background timeout으로 tracked ID를 Reconcile한다. 현재 존재가 확인된 exact ID만 삭제하고 불명 결과는 재관측한 후에만 재시도한다. 최대3회 cleanup 뒤 마지막 Reconcile도 수행한다. discovered candidate나 prefix 검색 결과를 삭제 대상으로 쓰지 않는다.

정상 acceptance는 두 세대의 unique14개 tracked 자원을 삭제 후 개별 조회한다. app acceptance는 gen1 10개와 실패한 gen2의 Neutron6개, 총16개 exact ID를 모두 ABSENT로 재조회했다. 임시 Management SG와 keypair도 독립 SDK GET404로 확인했다. 최종 prefix inventory는 이 exact-ID 검증의 **보조 확인**이다. 서버0/network4 등 사전 baseline과 일치했고 matching test resource는0이었다. 공유 image/flavor/network 및 기존 키를 삭제하지 않았다.

P1은 VM 삭제에 따른 자연 SSH 종료만으로 PASS하지 않는다. partial Reset/first failure 시 **workspace가 실제로 살아 있음을 독립 SDK로 확인**하고 해당 SSH의 종료와 app 내부 generation fence에 의한 resolver 미호출을 검증했다. replacement 실패는 실제 old deletion/new partial allocation의 추적과 cleanup을 함께 확인했다. P2에서는 취소한 waiter와 initialization owner를 구분해 다른 요청의 소유권·캐시·안전한 재시도까지 확인했다.

실제 SSH handshake 지연/단절/잘못된 키, connector process restart, 장기 soak, real SaaS/Browser routing은 이번 실행 대상이 아니다. 이전 in-process SSH failure/race 회귀를 실제 network fault PASS로 확대하지 않는다. HTTP/WSS/DB/runtime의 wire/config/dependency 변경이 없어 추가 schema/migration 검증 변경은 없고 기존 전체 검증 근거를 유지한다.

기존 `Session.Setup` 취소는 context를 직접 받지 않는 SSH setup 요청을 해제하기 위해 동일 VM의 공유 transport를 폐기할 수 있다. 정상 PTY/SFTP close의 독립성은 실제 검증했지만, setup 취소 때 다른 채널 유지까지 검증했다고 주장하지 않는다. 이 기존 제한은 reviewer가 후속 채널 의미론 검토 범위로 유지했다.

## 독립 리뷰·재현·최종 판정

[독립 리뷰 archive](audit-logs.zip) (`automated/reviewer.md`, `real/reviewer.md`)는 구현과 분리된 reviewer가 수행했다. 이전 full29 검토, 현재29 SHA, 새 main 한 파일 delta, 실제 정상/app/잔여 조회 원본을 직접 확인했다. 추가 confirmed production finding 없음. 보조 harness의 POST 주입 범위와 마지막 Cleanup 재조회 개선 의견은 실제 mutation 전에 반영했고 최종 hash가 일치한다. Reviewer는 cloud를 변경하지 않았으며 최종 exact-secret scan/packaging은 root가 수행했다.

[reproduce.ps1](reproduce.ps1)은 `source-manifest.json`의 committed Git blob과 archive 및 보조 코드 SHA를 확인하고 별도 출력 폴더에 overlay를 생성한다. Cloud config/private key는 필수 외부 입력이다. `README.md`의 고정 fixture 조건과 resource mutation 범위를 먼저 확인한다. 저장소 checkout/production 파일을 변경하지 않으며 finally에서 환경을 복원하고 사후 조회를 수행한다. Go/race/CGO toolchain이 필요하다. 스크립트는 문법 및 overlay compile만 검증했고 packaging 단계에서 cloud를 다시 실행하지 않았다.

[secret-scan.json](secret-scan.json)은 새 Evidence 전체, 압축 내부 원본, 전체 29개 변경 source와 기존 archive까지 포함한 known-local credential exact-value/private-key body/PEM/possible-token 검사다. 값은 저장하지 않는다. [archive-manifest.json](archive-manifest.json)은 각 원본 entry를, [artifact-checksums.json](artifact-checksums.json)은 배포 artifact를 고정한다. 모든 unknown token 또는 Git history 전체를 검증한 것으로 확대하지 않는다.

**최종 판단:** 제안된 로컬 구현 PR 범위의 이전 감사 판정을 유지하고, 현재 수정 소스의 로컬 실제 VM 및 P1/P2 실제 app 경계 검증 공백을 해소했다. 확인된 실패/잔여 자원은 없다. KT Cloud 선행조건과 실제 SaaS/Browser 전체 Terminal/File E2E, 후속 SFTP 구현은 남아 있으므로 LBT-147의 KT 준비 또는 OP-02 전체 완료로 확대하지 않는다. 이후 명시적 사용자 요청에 따라 기존 feature branch로 commit/push/PR 생성한다. main 직접 push, merge, 배포, Jira 완료 전환은 범위에 없다.

## 커밋과 배포 Evidence의 추적성

실제 실행 HEAD `3996817`만으로 P1/P2 검증 소스를 식별할 수 없다. 실행 당시 6개 미커밋 파일을 이후 `74c052f25e3778cafa6ae4fa75bd8775af46827a`로 커밋했다. 전체 29개 byte SHA와 committed Git blob을 모두 대조해 동일 소스임을 확인했다. 다음 Evidence 커밋은 문서·원본 로그·archive·재현 스크립트만 추가한다. 기록된 실환경 실행 시각·기존 실패·skip·당시 금지 범위는 그대로 유지한다. origin/main을 이번 게시 전 다시 fetch했고 `e850567`이며 새 main 문서 변경 영향도 재확인했다.

## 전체 구현 diff 파일별 검토


29개 파일 모두 최초 전체 검토와 이번 영향 재검토·독립 리뷰를 연결했다. 6개 로컬 변경은 전체 파일을 새로 읽었고 이전23개 파일은 source identity 및 이전 full review를 이어 사용했다. 미검토 diff 파일은 없다. 파일명은 위 checkout 기준이다.

| 파일 | 목적·관련 경계·최종 검토 |
| --- | --- |
| internal/connector/app/app.go | VMAccess/PTY production wiring·runtime 종료, lazy/query/transport 소비자 재검토 |
| internal/connector/app/app_test.go | runtime resolver 필수 경계 테스트, 기존 범위 불변·전체 PASS |
| internal/connector/app/main_integration_test.go | Mock SaaS/Relay와 pinned SSH peer Control/Data runtime·shutdown, PASS |
| internal/connector/app/runtime_provider.go | F2 owner/waiter 초기화·취소·결과 publish·safe retry, 해결 |
| internal/connector/app/runtime_provider_test.go | 새 deadline/cancel/shared failure/cache/owner cancel 회귀 전체 파일 검토·PASS |
| internal/connector/app/vm_access.go | F1 resource 결과별 lifecycle fencing, 해결 |
| internal/connector/app/vm_access_test.go | outcome/preflight/mutation resource state 및 idle 설정 회귀 PASS |
| internal/connector/app/vm_access_lifecycle_test.go | 새 authenticated SSH peer·부분 실패/preflight·다른Lab·late resolve/setup 회귀 PASS |
| internal/connector/provider/openstack/lifecycle_test.go | 새 실제 Adapter HTTP partial deletion failure/provenance·mutations중단·redaction PASS |
| internal/connector/provider/openstack/terminal_target.go | authenticated target·Management NIC/project/Server·host pin 재사용, 기반 불변 |
| internal/connector/provider/openstack/terminal_target_test.go | mismatched target 접속 전 거절, unit/race PASS |
| internal/connector/provider/openstack/vm_access_integration_test.go | 실제 gen1/gen2 SSH/PTY/SFTP/idle/cleanup·opt-in 경계 검토, 일반 suite SKIP, 별도 현재-source opt-in 실제 실행 PASS |
| internal/connector/terminal/session.go | safe Data ENDED 오류 및 SessionManager consumer, 불변·PASS |
| internal/connector/terminal/ssh_pty.go | independent managed channel 및 shell 종료·setup/cancel 소비, 불변·PASS |
| internal/connector/terminal/terminal_test.go | ENDED serialization·shared transport consumer 회귀 PASS |
| internal/connector/vmaccess/access.go | cache/generation watermark/idle/retire concurrency·F1/F2 호출 영향 재검토 |
| internal/connector/vmaccess/access_test.go | share12·채널 독립·generation/target·disconnect/실패/종료, PASS |
| internal/connector/vmaccess/cancellation_test.go | deadline/handshake/late resolve/setup·transport 취소, PASS |
| internal/connector/wss/handler.go | envelope target·safe errors 및 reconnect/operation lifetime 재검토 |
| internal/connector/wss/vm_access_test.go | raw error Control OPEN/ENDED 차단, PASS |
| runtime/README.md | no-channel idle 설정·pinning 설명, 정책 수정 없음 |
| runtime/contract.yaml | 0.1.7 idle1m·양수 duration, 이번 수정 없음·계약 검사 PASS |
| docs/connector/evidence/LBT-147/2026-10-10/README.md | 과거 actual 테스트 scope/한계/초기 실패, 그대로 보존 |
| docs/connector/evidence/LBT-147/2026-10-10/validation-summary.json | 과거 tested source·실행 요약, 과거 log와 정합 |
| docs/connector/evidence/LBT-147/2026-10-10/source-manifest.json | 과거19 source blob을 HEAD와 대조, 현재 수정 source와 구분 |
| docs/connector/evidence/LBT-147/2026-10-10/artifact-checksums.json | 과거6개 artifact SHA256 모두 일치 |
| docs/connector/evidence/LBT-147/2026-10-10/secret-scan.json | 과거 scoped exact-value scan 결과/한계 유지 |
| docs/connector/evidence/LBT-147/2026-10-10/reproduce.ps1 | 과거 tested SHA guard·로컬 입력·finally 환경 복원·opt-in 범위 |
| docs/connector/evidence/LBT-147/2026-10-10/automated-logs.zip | 9entry CRC 및 원본/요약 정합·실환경 범위 대조 |


## 새 Evidence 변경 검토 범위

| 파일 | 목적·검토 |
| --- | --- |
| README.md / AUDIT.md | LBT-147 전체 구현, AC 추적, P1/P2, 실제·Mock·skip·미검증·잔여 위험과 커밋 매핑 |
| source-manifest.json | 실행 29개 source byte SHA / 확정 code commit Git blob / 보조 코드 SHA |
| validation-summary.json / automated-validation-summary.json | 실제 실행과 앞선 전체 자동 실행 결과를 분리, 기존 실행 시각·skip·실패 보존 |
| ssot-summary.json | 게시 시 원문 재조회, 3 Jira 본문/댓글 및 5 Confluence body/version 불변 대조 |
| pre-discovery.jsonl / real-vm-access.jsonl / real-app-boundaries.jsonl / post-discovery-and-residual.jsonl | 실제 원본 그대로 복사, test action·시간·cleanup ID 관측·summary 대조 |
| audit-logs.zip / archive-manifest.json | 기존 full audit/reviewer 및 원본 자동 로그, 수정 전 실패와 초기 fixture panic, 두 외부 overlay harness 보존; entry hash·CRC 검사 |
| reproduce.ps1 | 외부 config/key 입력, Git blob/zip/harness 검증, 별도 출력/overlay, 환경 복원·사후 조회; 문법·compile 검증 |
| secret-scan.json / artifact-checksums.json | 로컬 secret 값 비저장, 압축 내부 포함 scoped scan 및 배포 SHA 고정 |
| publication-review.md | 독립 reviewer의 게시 전 source/evidence 재검토 기록; cloud mutation 없이 검토 |
| .gitattributes / publication-checks.json | 기록 artifact byte 변환 방지, 게시 시 syntax/overlay compile/hash/원본 summary 검증 결과 |
| verify-reproduce-guards.ps1 / reproduce-guard-regression.json | 실제 Go/cloud를 차단한 isolated clone source guard 회귀, PS7 및 WindowsPS5.1 각6케이스 PASS |

새 Evidence의 cloud 재실행은 문서 packaging 자체와 무관하여 수행하지 않았다. 기존 full Go/Web/contracts PASS를 그대로 사용하고 별도 packaging 문법·hash·archive·secret·overlay compile 검증으로 재현 의존성을 확인한다. PostgreSQL integration은 DB 변경이 없으므로 이번 범위에서 미실행이다.

게시 독립 리뷰에서 재현 script의 P2 보완을 수정 전에 보고했다. 원래 `reproduce.ps1:17`의 29파일 blob guard는 diff에 없는 기존 producer/consumer 변경을 놓칠 수 있어, 전체 code commit 대비 tracked/staged/untracked 및 ignored Go source 변경을 Evidence 폴더 밖에서 거절하도록 보완했다. 실제 cloud mutation 전에 실패해야 하는 의미 있는 guard 회귀 검증을 추가했다. Windows PowerShell 5.1의 UTF-8 BOM으로 Go overlay parsing이 실패할 수 있는 경계도 BOM 없는 UTF-8로 보완했다. production source는 그대로다.

게시 helper의 P3도 수정 전에 보고했다. `verify-reproduce-guards.ps1`이 사용자의 기존 global `go` wrapper를 덮어쓸 수 있어 ScriptBlock을 보존하고 finally에서 복원하도록 수정했다. stub 호출 counter는 closure 안에서 관리한다. 두 PowerShell runtime에서 원래 wrapper 복원 및 없던 wrapper 제거를 재검증한다.
