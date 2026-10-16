from pathlib import Path
import json,subprocess as sp,sys
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-routing-postadmin'
S=json.loads((OUT/'final-validation-summary.json').read_text(encoding='utf8'))
assert S['internetON']['run']['exitCode']==S['internetOFF']['run']['exitCode']==S['localOpenStackAll']['run']['exitCode']==0
DEST.mkdir(parents=True,exist_ok=True)
final_review='--final' in sys.argv
verdict='로컬 변경 범위의 검증 통과 / PR 제안 가능' if final_review else '최종 독립 패키지 검토 대기 / 판정 유보'
decision='로컬 범위의 제안 변경은 요구사항·회귀·실제 통합·독립 리뷰를 충족했다.' if final_review else '실제 실행 결과는 통과했으며, 이 초안과 최종 패키지의 독립 검토가 남아 있다. 최종 판정은 유보한다.'
body=f'''# LBT-174 — 관리자 환경 조치 후 라우팅·진단 및 최종 로컬 audit

판정: **{verdict}**. 실제 로컬 OpenStack + Mock Preview Gateway라는 LBT-174 범위에서 재현된 결함의 수정과 필수 로컬 검증을 수행했다. 원격 CI 자체는 실행하지 않았으며 동일 명령의 로컬 Linux 실행과 구분한다. KT Cloud 최종 검증(OP-03 후속), 실제 SaaS/Browser 전체 C3, 미검증 guest/backend에 대한 보장은 이 판정에 포함하지 않는다. commit·push·PR·merge·배포·외부 메시지는 실행하지 않았다.

과거의 실패·당시 보완 필요 판정은 [이전 라우팅 audit](../2026-10-16-routing-fix/AUDIT.md), [Linux/Internet 조사](../2026-10-16-network-race/AUDIT.md), [Preview 전체 audit](../2026-10-16-followup/AUDIT.md)에 그대로 남겼다. 이 문서는 최종 source의 재실행으로 해당 로컬 공백을 갱신한다.

## A. 버전·전체 diff와 검토 경계

- Primary branch `{S['source']['primaryBranch']}`, base/HEAD `{S['source']['primaryHEAD']}`. 미커밋/unstaged 변경이며 primary staging0. [전체 38개 제품·테스트·문서 inventory](code-inventory.json), [tracked patch](tracked-diff.patch), [당시 working tree](working-tree.txt), [38개 파일별 검토 목적과 소비 경계](independent-review/AUDIT_DRAFT.md)를 함께 적용한다. 36 Go + owning 문서2개이며 미추적 Go19개도 포함했다. 새 Evidence와 이전 Evidence의 변경 여부도 패키지 검증에서 별도로 확인한다.
- 최종 Go/modules/runtime/contracts **{S['source']['allFiles']}파일** canonical LF SHA256 `{S['source']['allSHA256']}`. Go/module507파일 `{S['source']['goModuleSHA256']}`. [manifest](run/final-verified/source-manifest.json)와 [모든 최종 실행 요약](run/final-validation-summary.json)을 제공한다. archive는 같은521 source와 vendor를 포함하며 archive/공식 toolchain 해시·독립 안전경로 검사를 기록했다. archive·바이너리·toolchain은 Git에 넣지 않았다.
- Preview manager/session/client와 WSS handler의 attempt/owner/credential/cancel 경계, App→VMAccess 주입과 Reset/Cleanup invalidation, Provider 생성→검증→SSH 주소→ResourceResult→Reset/Cleanup을 전체 변경과 함께 읽었다. server realtime/preview test-support의 producer/consumer와 Windows 차이도 검토했다. HTTP/WSS schema·DB migration·module dependency·Web 제품 변경은0이며 미검토를 뜻하지 않는다. owning runtime/Connector README의 배포 전제 변경은 별도로 검토했다.
- 첫 main 통합 기준은 `ee05485bd9a1668f222e7a54fb5f880ea197e35a`이며 합성 source aggregate는 `{S['latestMainSource']}`다. 별도 detached checkout에서 branch patch를 깨끗하게 합성했다. 531파일 manifest·branch 변경36Go 동일·Web66파일 동일을 독립 검토했다. [통합 source/실행 확인](independent-review/latest-main-integration-check.json), [setup](run/latest-main-integration-setup.json), [통합 manifest](run/latest-main-check/source-manifest.json). primary를 rebase/commit하지 않았다. migration8/9와 SaaS worker·Web 후속을 포함하므로 실제 PG/Web도 해당 합성 source에서 새로 실행했다.
- 검증 중 main이 다시 `1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4`(LBT-179 reconciliation worker)로 전진했다. 별도 두번째 checkout에서 최종 branch patch+19미추적Go를 합성하고 Go/vet/build/contracts/실PG5pkg를 다시 실행했다. source {len(S['latestReconcileMain']['source']['files'])}파일 aggregate `{S['latestReconcileMain']['source']['sourceSHA256']}`. [최신 통합 manifest](run/latest-main-reconcile-check/source-manifest.json), [DB 소유/정리](run/latest-main-reconcile-check/database-owner-result.json). Web66파일과 `HEAD:web` tree·dependency가 이전 실제Web검증과 정확히 같아 [hash bridge](run/latest-main-reconcile-check/web-identity-bridge.json)로 연결한다. 이전 ee05485 PASS를 새 워커의 PASS로 전용하지 않았다.
- 개인 clouds.yaml/SSH private key/PG data 및 VMware 관리자 복원 상태는 보호 입력이며 보고서에 복사하지 않았다. runtime/README.md·db/migrations/README.md는 링크 문맥 사본이고 변경38파일과 구분한다([문맥 inventory](context-inventory.json)).

## B. 최신 요구사항·SSOT 추적성

사용자 PRE_MERGE_AUDIT A–F와 repo AGENTS/CONTRIBUTING/TESTING을 적용했다. 구현과 분리된 reviewer가 Jira LBT-174 본문·AC·댓글과 Confluence 확정 문서를 직접 다시 읽었다. [원문 snapshot·URL·version](independent-review/ssot-refresh.json), [차이 확인](independent-review/context-refresh-check.json), [20:28 KST 최종 원문 재조회](independent-review/ssot-closing-refresh.json), [main·primary·521 source 최종 대조](independent-review/closing-context-verification.json). Jira updated `2026-10-16T14:59:23.423+0900`/댓글0, D18v5·D19v7·D20v7·전략v3·Platformv27·완료결정v11·추가결정v3를 기준으로 한다. 최종 재조회에서 이 원문과 main1e2b9c2는 동일했다. Git Preview wire는 Connector README §7b와 preview control/data schema를 따른다. 아래 줄은 최종38파일 source 기준이며 함수와 회귀 이름으로도 재현 가능하다.

| 요구·AC / owning 원문 | 구현 파일·줄 | 자동 테스트·실제 실행 | 실환경 Evidence·공백·판정 |
| --- | --- | --- | --- |
| [Jira174](https://samsunglions.atlassian.net/browse/LBT-174) 실제 SSH 선연결→outbound Data WSS→binary HTTP | app/app.go의 SSHForwarder 주입, preview/manager.go:45/90, client.go:99, session.go:104/124 | TestOpenStackPreviewTransportIntegration, 기존 forwarder·HTTP frame boundary 회귀 | 최종 full12 cloud batch PASS. 실제 OpenStack VM/SSH/HTTP + Mock Gateway. 실제 SaaS/Browser C3와 구분. 충족 |
| full correlation·1WSS=1TCP·동시active≤1·sequential / Git §7b | session.go:192/215/293, manager.go:45 | TestPreviewLifecycleConcurrentDuplicateAndActiveAttemptIsolation·StaleAttachCannotBindNewAttempt 및 actual Preview fixture | stale/duplicate·순차 재개·다른 터널 격리 PASS. 충족 |
| 대표 오류 APP_NOT_RUNNING·PORT_REJECTED·VM_UNREACHABLE·UNAVAILABLE/timeout, 원문·비밀 비노출 / Jira174·§7b | preview/forwarder.go:43/65(기존), wss/handler_preview.go:16, vmaccess/tcp.go:24 | forwarder 오류·caller timeout 회귀, actual Preview fixture의 안전한 코드 매핑·Control secrecy assertion | 실제 closed app port·invalid/22 port·다른 대상·세대 거절과 timeout fake 경계를 구분한다. HTTP/PTY bytes·synthetic credential의 Control 비포함 PASS. 충족 |
| CLOSE·정상EOF·오류·pending취소·Shutdown·backpressure / Jira174·§7b | session.go:78/251/344/368, manager.go:193/200, client.go:99 | lifecycle pending/blocked writer/generation/upgrade cancel 및 cloud fixture | normalEOF logical session 보존, 종료 attempt의 late 결과 소유권 회귀. 프로세스 hard-kill/장시간 soak는 별도. 충족 |
| Control replacement·credential revoke·owner trust / §7b4001/4002 | wss/handler_preview.go:16/150와 handler authority retirement | TestPreviewOldControlEpochCannotUseNewCredential, DirectControlReplacementRetiresOldIdleSession, RetirementDoesNotCancelReplacementOwner, WorkerBoundAndRetirementRelease | in-process WSS/Mock 경계 검증이며 실제 SaaS 인증 전체로 확대하지 않음. 충족 |
| Reset은 immutable snapshot·old generation 폐기 / D19·§8a | app/vm_access.go:61/71/88, provider topology.go:98/144, provision Reset | TestOperationResultInvalidatesObsoleteAccess·preflight/partial/failure 회귀, 실제 M3/Preview/VMAccess/File fixture | 실제 ON/OFF 각각 reboot+bootID 변화·pinned SSH 회복·immutable Reset g2·first12·renew2순서 PASS. 충족 |
| ON은 Lab egress, OFF는 외부 route 없음, Mgmt SSH 독립 / D18·runtime | management_network.go:21/82/101/109/131, topology.go:144 | ManagementDHCPClasslessRouteUsesValidatedSubnet/InvalidCIDRBeforePortAPI 및 ON/OFF wire 회귀 | 최종 ON은 sole Lab default+Lab socket source+TCP0, OFF는 IPv4 default0+route unreachable+TCP nonzero. 양쪽renew/reboot/Reset 후 동일. [ON 원본](run/final-verified/native-live.log.gz), [OFF 원본](run/off-lifecycle/probe-live.log.gz). 검증된 dnsmasq/Linux 배포조건 내 충족 |
| 원래 subnet/known ID 보존·드리프트 거절 / D20·runtime | management_network.go:131, topology.go:144–164, port.go | ManagementSubnetAffinity, RejectsAddressOutsideOriginalSubnet, RejectsPolicyDriftAfterNova, UsesValidatedObservation | 단일GET의 ID/network/subnet/prefix/SG/options를 검증하고 같은 객체만 주소·ledger에 사용. 독립 red→green6case PASS. 실제 생성·Reset·exact cleanup PASS. 충족 |
| StartupScript 원문/SHA immutable, 공유망·SG 무단 변경 금지 / D19·Platformv27 | ProvisionConfig preflight, owned Management Port create only, snapshot 소비 | ON/OFF startup body·hash 보존, unsupported14입력·Reset DELETE0 회귀 | 실제 startup 성공 뒤 라우팅 확인. LabDHCP·shared subnet/SG·user-data route override 없음. 충족 |
| timeout은 bounded return·worker join·remote route 정리 / 사용자 후속·TESTING | integration_diagnostic_test.go:20/56, 외부 helper owned_route finally | loopback dedicated SSH cancel10경로·독립race5반복·remote generator AST/9fault | final actual intentional1s timeout/정확route부재/다음진단PASS, forcedMgFAIL/LabPASS 후 exact restore. SSHclose만으로 원격 정리를 추정하지 않음. 충족 |
| 특수 Linux race·기본 CI 의미·회귀 / TESTING·CI | vmaccess caller deadline, realtime log polling, OS refusal분류, File Setup/Wait/gate | fullGo/Windowsrace, Linux exact C1 tagged vet/build/race, latest-main PG/Web | LinuxC1 parents12·events67·skip0·fail0·race0, same521 hash0difference. 원격 CI 자체 미실행. 로컬 실행범위 충족 |
| 실제 fixture·재현·KnownLimit·Cleanup / Jira174·TESTING | integration_cleanup_test.go와 checked-in fixture, source manifests | 최종 OpenStack12parents PASS/skip0/fail0; timeout·sharedPTY/SFTP·stalegeneration | 실제 API resource ID별 absence, SG/keypair GET404, native PG 임시DB0·stop. 전체 cloud의 임의 사용자 자원 부재 주장 없음. 충족 |

## C. 실패·보안·수명주기 분석

- 관리망 사전 검증은 extension·정확한 ACTIVE network·단일 canonical IPv4 DHCP subnet·SSH CIDR 완전 포함·Lab과 비중첩·host routes0이다. 잘못된입력/401/5xx/cancel은 port mutation 전에 거절한다. Reset 사전실패는 기존 generation 삭제0을 유지한다. KT topology 분기에 표준 Neutron 정책을 강제로 적용하지 않는다.
- blank router + canonical `<managementCIDR>,0.0.0.0` 121만 owned Management Port에 넣는다. numeric alias/249/empty121/default121/타CIDR/중복/null/IPv6를 거절한다. backend·guest·전용DHCP 전제는 API echo만으로 보장할 수 없어 실제 DHCP/renew/lifecycle을 따로 검증했다.
- 사용자가 관리자 PowerShell에서 VMware DHCP를 중지·비활성화했고 실제 Stopped/Disabled를 확인했다. [환경 기록](run/postadmin-service.json). 이 상태는 유지 중이며 다른 VMware DHCP 사용을 재개하려면 이전 보관된 관리자 script의 Restore가 필요하다. 설정 해시·저장 상태는 해당 helper의 guard를 따른다. 제품이 Windows 서비스를 변경한 것은 아니다.
- 경쟁 DHCP 제거만으로는 해결되지 않았다. Neutron-origin172.16.8.111 lease에도 router3가 있고 blank121은 Mgmt default를 남겼다. actual DHCP agent driver는 Dnsmasq이며 설치 daemon 버전은 API 미노출이다. valid onlink121 wire ACK가 guest에 도착한 뒤 sole Lab default가 됐다. wire router3가 없어졌다는 주장은 하지 않는다. [독립 관측](independent-review/DHCP_BACKEND_REVIEW.md).
- DHCP reload 직후0.8s renew 실패도 원본에 남겼다. 최종 제품은 port 생성 시 정책을 함께 넣고 이후 검증해 Nova에 전달한다. 후보의 UPDATE/wait 방식을 제품에 임의 sleep/retry로 이식하지 않았다. 실제 fresh CREATE/재부팅/Reset/두 renew 순서에서 수용했다.
- post-Nova port를 두 번 조회해 주소와 검증이 다른 관측이 되는 RNET006을 제거했다. 원래 확정 ID는 실패 시에도 추적하고, foreignID/주소/policy drift가 ledger와 SSH endpoint로 들어가기 전에 거절한다. 응답 불명 mutation을 blind retry로 바꾸지 않았다.
- Preview 소유권은 logical session·open attempt·Data connection·captured Control credential/epoch별로 분리한다. retirement authority 확정과 새 admission은 lock으로 직렬화하고 외부IO는 lock밖이다. callback과 정상EOF가 새 owner/attempt를 지우지 않는 회귀를 실행했다. Preview가 소유한 socket·relay·handler와 pending upgrade·blocked writer는 취소·종료하고 완료를 기다린다. VMAccess의 caller I/O 종료와 raw SSH 작업의 수명은 구분한다. 비협조 peer의 late open/pump/channel은 transport당 최대16개 budget을 유지하며 SSH CLOSE 또는 idle·generation invalidation·shutdown에 의한 transport 정리까지 남을 수 있다. 모든 raw worker의 즉시 join이나 전역 goroutine0을 주장하지 않는다. [LBT-173 소유권·fault 회귀 근거](../../LBT-173/2026-10-16/AUDIT.md).
- 알림은 취소된 tunnel ctx를 shared Control write 취소로 재사용하지 않는다. captured sender에 bounded context를 적용하며 old owner가 새 credential을 빌리지 않는다. Secret/terminal/body/raw provider error를 새 API/log/DB로 저장하지 않는다.
- App의 VMAccess는 Reset/Cleanup 부분 결과·unknown 경계에서 obsolete generation을 invalidation한다. 한 SFTP/PTY 종료가 형제 채널을 닫지 않는 실제VM fixture도 통과했다. TestSupport의 Windows refusal 분류는 제품 오류 정책을 넓히지 않는다.
- 임의 프로세스 hard crash의 새로운 durable state/replay 정책은 추가하지 않았다. ephemeral Preview 재접속과 기존 Provider reconciliation 경계를 유지하며 DB/HTTP/schema 변경0이다. 최신 main durable worker 변경은 별도 통합+실DB로 확인했다.
- 진단 SSH는 전용 transport를 취소 후 닫고 Run/output 완료를 join한다. 원격 socket5s/ip8s/alarm30s/apt300s bound와 exactownedroute try/finally를 사용한다. final apt는 임시route0이다. native DB forwarding은 CI후 닫고 실제reboot 전에 worker를 회수했다.
- 과거 최초 InternetON 실패 VM의 당시 socket/source 자료는 남아 있지 않아 단일 역사적 원인은 확정하지 않는다. dual-default 메커니즘·강제 Management 실패/Lab 성공·수정후 반복성은 직접 재현했다. OVN·다른 guest·public topology·부하/장기 soak는 검증한 것으로 쓰지 않는다.

## D. 테스트·실행·Mock·skip·Evidence

| 실행 | 최종 결과·소스·실제 경계 |
| --- | --- |
| Primary fullGo | {S['primaryGo']['testPass']} pass events/42pkg/18skip/0fail;507Go/module hash전후동일. parent/subtest 이벤트 수이며 고유 요구 수가 아님 |
| Primary vet/build/fmt/contracts | vet/build PASS,505Go canonicalLF fmt mismatch0. CI고정 PyYAML6.0.3/jsonschema4.26.0/OpenAPI validator0.9.0로 contract/ref/runtime/migrationmetadata PASS |
| Windows race | 11pkg {S['windowsRace']['testPass']}PASS/16skip + 별도 serverpreview {S['extraServerPreviewRace']['testPass']}PASS/0skip. 합계12pkg1518PASS/16skip/0fail/0race. final7e8 |
| Native Linux exact CI | vet/backend_router_harness build 및 integration+backend_router_harness C1 race, {S['nativeLinux']['passEvents']}PASS/12parents/0skip/0fail/0race; final521/7e8 manifest0difference. 실제PG reverseSSH·MockProvider harness이며 운영 SaaS 전체와 다름 |
| InternetON lifecycle | first12+두renew+실제reboot후두renew+immutableResetg2후first12/두renew 전부 strictPASS. remote timeoutcleanup/freshnext/routeAB exactrestore PASS. 최종 원본+run metadata 제공 |
| InternetOFF lifecycle | first12+두renew+실제reboot후두renew+immutableResetg2후first12/두renew 전부 zero IPv4 default/routeunreachable/TCPdenied PASS. pinned 관리SSH 유지. native/apt/CI/DBforward 미호출 |
| 전체 실제 OpenStack fixture | parents12,pass events{S['localOpenStackAll']['passEvents']},skip0/fail0. 실제Preview·File·Terminal·VMAccess·M1/M2/M3·Internet. 같은507Go/module, 각requiredLinux filesystem exactPASS와skip거절 gate. 보조바이너리5개 LinuxCGO0build·hash 기록 |
| 최신main 통합 Go/Web | 별도ee05485+branch source531,Go{S['latestMainGo']['testPass']}PASS/43pkg/18skip + vet/build/contract PASS. Webtypecheck/lint/Vitest16files217tests/build PASS. jsdom canvas경고 원본보존, actualbrowserrender 아님 |
| 최신main 실PG16 | {S['latestMainPostgres']['testPass']}PASS/5pkg/1optional TestDumpPreviewWireSamples skip/0fail. migration8/9 실제적용. branch와 동일36Go·contracts wire동일인 합성 source. native종료 시 임시DB0·PGstop 확인 |
| 최종1e2b9c2 main 재통합 | Go {S['latestReconcileMain']['go']['testPass']}PASS/{S['latestReconcileMain']['go']['packagePass']}pkg/18skip, actualPG {S['latestReconcileMain']['postgres']['testPass']}PASS/5pkg/optional1skip/0fail, vet/build/contracts PASS. reconciliation worker 포함 새 source에서 재실행. 임시DB0+소유PGstop, Web66+tree동일 bridge |
| 독립회귀 | RNET006 6case red→green; SDK status loopback4case; generatedOFFcommands3AST; nativearchive521 hash/safe path; finalsource/최신main hash 모두대조. reviewer는 제품을수정하지 않음 |
| 역사적 실패 | blank121 strictFAIL, observer TypeError, candidatePUT successcode문제, candidatefast-renewFAIL, 초기compile/fixture오류 모두stage별raw보존. candidatev3PASS는당시source520제품PASS와구분. 이전sealed646파일수정0 |
| 미실행/제외 | 원격GitHubCI·KT Cloud174최종·실제SaaS+BrowserC3·TLS/browser인가·미검증backend/guest·장시간부하/hardcrash. Jira의명시된KT/C3후속과구분하며로컬성공으로대체하지않음 |

기본18skip은 localOpenStack12+KT2+Windows4이며 Windowsrace16skip은 cloud14+OS2다. 실제별도cloud12실행은 skip0이지만 기본명령의 skip 자체를 지우지 않았다. final JSONL·압축원본·실행시간/명령/소스전후해시로 요약을 대조했다. [최종 summary](run/final-validation-summary.json), [독립 raw 집계](independent-review/final-primary-automated-verification.json), [native 원본 독립 확인](independent-review/native-ci-raw-verification.json).

Go/runtime/contracts manifest는 SQL을 포함하지 않는다. 두 통합 checkout의 migration SQL9개를 별도로 HEAD와 대조해 canonical LF 차이0·Git diff0을 확인했다([SQL source](run/latest-main-reconcile-check/migration-source-identity.json), [종료 후 SQL source](run/latest-main-reconcile-check/migration-source-final-identity.json)). raw9개 차이는 Windows checkout의 LF→CRLF 변환만이며 두 관측 사이 실제 raw hash는 동일하다. 첫 관측은 신규DB 실행 도중/이전ee05 DB 종료 후였으므로 실행 전 hash라고 주장하지 않는다.

## E. 수정 전 findings·수정·회귀

아래 확정 finding은 수정 전에 사용자에게 보고했고 재현·분석·한계를 구분했다. 원래 Preview/VMAccess/Realtime/fixture findings의 파일·재현·회귀는 이전 전체 audit E에 그대로 보존하고 이번 fullGo/race/cloud로 다시 확인했다.

| ID·심각도 | 수정 전 위치·재현·영향/근거 | 최종 상태·회귀 |
| --- | --- | --- |
| F174-PACK001 Low | verify_package.py:95 첫 신규 패키지 검사. AUDIT가 링크한 자기 결과 markdown-verification.json을 링크 검사 뒤 생성해 존재 전 거짓 broken link 1건 | 먼저 pending 결과 파일을 생성하고 모든 Markdown 링크를 검사한 뒤 실제 결과로 갱신한다. 초기 missing 보고를 보존하고 해당 결과 파일이 없는 첫 실행 조건에서 재검사 |
| F174-PDOC001 Low | write_audit.py:54 초안. 비협조 SSH peer에서 caller가 끝나도 raw pump·slot이 남는 LBT-173 fault와 tcp.go:99–114에 비해 모든 lateSSHchannel 즉시 join을 보장하는 과장 | caller-facing Preview 종료와 transport 소유 최대16 bounded late work를 구분했다. 기존 Close/late-success/EOF-only fault Evidence·소스 수명주기를 대조했으며 제품 변경이나 새 제품 테스트는 없음 |
| F174-N001 /Medium | 이전 provision.go219/topology127,dualdefault Management source선택시ON실패;D18Labegress | 경쟁DHCP제거+제품onlink121,finalON/OFF full lifecycle수용. 역사적최초1회동일원인단정안함 |
| F174-RNET002 /Medium | topology158–163,response subnet을expected로써다른subnet주소수용 | 원래ID/CIDR고정,affinity red→green/실제ResetPASS |
| F174-RNET003 /Medium | 초기management_network64–66,numeric3/121/249empty NeutronAPI거절 | canonicalrouter/121,249미요청,unitwire+actualCREATE/GETPASS |
| F174-RNET004 /Medium | 이전management_network81,blank121저장후Neutron-originDHCP에서dualdefault | canonicalCIDR onlink121,2CIDR/invalidCIDR/optiondrift회귀,finalfreshON/OFF/renew/reboot/ResetPASS |
| F174-RNET005 /Medium | same subnetID+outsidefixedIP가관리endpoint수용 | originalprefix검증/knownID보존,checkedinred→green/independentinvalidIPcasePASS |
| F174-RNET006 /Medium | source521/8b3 topology155–167,firstGETinvalidIP/secondGETnormal→SUCCEEDED/wrongSSH | 단일verifiedports.Port사용+검증후ledger,checkedin4case/independent6case red→green. [원래 보고](independent-review/OBSERVATION_FINDING.md) |
| METHOD002·RMETHOD001/002 /Low | 외부timeoutRunjoin누락·Aptindent·routeadd가try밖 | dedicatedtransportjoin/6scriptAST/9routefault/actualalarmcleanup후freshdiagnosticPASS;제품우회아님 |
| PMETHOD001 /Low | 외부candidate PUTdefaultOkCodes가정상200제외 | 명시200/201+exactGET,독립4casePASS. 당시actualHTTPstatus미수집한계유지 |
| F174-REPORT001 /Low | 외부summarize_final.py:65가OFF도ON과같이16trackedresource요구;OFF는라우터없어14개 | ON16/OFF14 및SG/keypairGETabsence를원본에대조하도록수정. 실제실행실패가아니며잘못된원래reader가정과구분 |
| 검증 공백 /confirmed defect아님 | InternetOFF기존fixture는TCP실패만확인해zeroMgmtdefault증거부족 | 별도OFFoverlay사전review+AST+실제zero-route lifecycle추가PASS |

이전 validator/fmt/helper 준비 중 실패와 잘못된 초기redfixture는 원본에 남겼으며 의미 있는 제품 red→green 증거와 구분했다. 이번 audit 후 미해결 confirmed product finding은 없다.

## F. 독립 리뷰·판정·재현성과 한계

기존 reviewer `/root/review_lbt173`가 구현과 분리되어 전체38파일, 최신SSOT, same-observation 실패재현, source/로그/cleanup/최신main통합을 독립 대조했다. [최종 독립 판정](independent-review/FINAL_AUDIT.md), [A–F 상세 검토](independent-review/AUDIT_DRAFT.md), [독립 패키지 전수 검사](independent-review/package-independent-verification.json), [reviewer 인계 시점](independent-review/final-reviewer-handoff.json)을 함께 적용한다. 독립 패키지 검토는 raw380·파일431 snapshot 대상이며 최종 리뷰 보고서 추가와 문서 갱신분은 [최종 export 차이](final-export-delta.json)로 분리하고 다시 검사·봉인했다. reviewer의 RNET006을 수용했고 수정 후독립6case와전체관련실행을 다시확인했다. 검증helper/패키징도두번째리뷰대상이다. 제품코드의새변경이나기존sealedartifact변경은리뷰어가수행하지않았다.

{decision} 알려진 제약은 위 D의 원격CI/KT/C3/guest/backend/soak이며 후속검증을 누락없이 기록했다. 실제외부시스템·MockGateway·loopback/mock·실DB·기본skip을분리했고Audit통과를원격작업승인으로해석하지않는다. Jira완료상태를변경하지않았다.

[재현 절차](REPRODUCE.md)는 보호입력·공식toolchain·소스복원·exact명령·helperoverlay를기록한다. [원본 provenance](raw-provenance.json), [소스복원/이전646파일 불변검사](package-verification.json), [보호입력 exact비교+제한된heuristic](secret-screen.json), [링크검사](markdown-verification.json), `package-checksums.json`으로 패키지를검증한다. 모든비밀부재나전체cloud자원0을증명한다고과장하지않는다. Markdown만export된Go/logsuffix에맞추고원본/변환후hash를둘다남겼다.
'''
(DEST/'AUDIT.md').write_text(body,encoding='utf8')
(DEST/'REPRODUCE.md').write_text('''# LBT-174 최종 로컬 재현

Primary base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`에서 `code-inventory.json`의 변경 38개를 원래 경로로 복원한다(`.go.txt`→`.go`). `tracked-diff.patch`만으로는 미추적 Go 19개가 복원되지 않는다. `run/final-verified/source-manifest.json`의 521개 canonical LF hash를 대조한다. 이전 세 Evidence 패키지의 sealed 파일 646개는 수정하지 않는다. 복원 후 helper의 ROOT/OUT 및 Go/GCC/PG/Python 경로를 새 환경에 맞춘다.

Windows Go 1.27.1, race용 GCC, Python 3.12, 실제 PostgreSQL 16, 로컬 OpenStack 및 ubuntu/m1.small/sharednet1이 필요하다. 보호된 clouds.yaml·OS_CLOUD와 SSH private key는 운영자가 준비하며 패키지에는 없다. runtime의 직접 연결 IPv4·단일 subnet·전용 DHCP·host routes 없음·SSH prefix 완전 포함·Lab과 비중첩 조건을 먼저 만족해야 한다. 이번 실제 DHCP driver는 Neutron Dnsmasq, guest는 Linux다. 다른 backend/guest는 별도 검증한다.

1. `run/final-verified/verify.py go`와 `race`, `final_checks.py`, `format_and_summary.py`로 기본 Go/vet/build/505 Go fmt/계약 검증을 재현한다. 추가 server/preview race 명령은 해당 result.json의 command를 따른다. CI 고정 validator 버전은 PyYAML 6.0.3, jsonschema 4.26.0, openapi-spec-validator 0.9.0이다.
2. `build_sources.py`는 vendor를 포함한 source archive와 manifest를 만든다. 공식 Go 1.27.1 linux/amd64 해시는 linux-go-download.json을 따른다. Linux archive·toolchain·test binary는 Git에 없으며 재생성한다. archive를 확인할 때 root의 `source/tested-source.json`을 사용해 역사 Evidence의 동명 파일과 혼동하지 않는다.
3. VMnetDHCP는 사용자가 관리자 PowerShell에서 중지·비활성화했다. 검증 당시 Stopped/Disabled였다. 환경을 설정하거나 복구할 때는 이전 routing-fix 패키지의 Set-LabbitVMnet8Dhcp.ps1과 보관한 상태를 사용한다. 단일 scope·설정 SHA guard를 지키고, Restore는 운영자가 의도한 복구 시 수행한다. 관리자 상태 파일은 보호 입력으로 Git에 없다.
4. `final-verified/local_probe_test.go.txt`를 외부 `local_probe_test.go`로 복사하고 overlay.json의 가상 테스트/실제 helper 경로를 조정한다. production overlay는 없다. `run_validation.py` 전체 실행은 PG를 시작·종료하며 최종 source archive와 공식 toolchain을 VM에 전송한다. first12·timeout 정리·강제 route A/B·양쪽 DHCP renew → native tagged CI → 실제 reboot/boot ID → immutable Reset g2 → first12/renew를 검사한다. apt에 임시 route를 쓰지 않는다.
5. `off-lifecycle/run_validation.py probe`는 OFF용 외부 helper를 실행한다. 이 helper는 probe의 조기 return을 제거해 reboot/Reset/renew까지 수행하며 zero IPv4 default·TCP 차단·pinned SSH를 확인한다. 복사된 linux_ci.sh와 archive hash metadata는 공통 runner의 비실행 부속물이다. OFF에서는 apt/native CI/PG forwarding을 실행하지 않는다.
6. `prepare_linux_binaries.py`로 Workspace helper와 filesystem/provider/workspacefile/credential binary 5개를 재생성한다. `LABBIT_WORKSPACE_SFTP_HELPER_FILE`, `LABBIT_WORKSPACE_SFTP_TEST_BINARY`, `LABBIT_LOCAL_LINUX_REGRESSION_DIR`을 설정하고 `verify.py live-all`을 실행한다. 정확한 TestOpenStack parent 12개가 PASS이고 skip/fail이 0인지 확인한다. `run_batch.py`가 이 입력과 source521 전후 hash를 기록한다.
7. main 통합은 별도 ee05485 checkout에 latest-main-branch.patch를 3-way apply하고 미추적 Go 19개를 복원했다. latest-main-check의 result command로 Go/Web/실PG5pkg를 재현한다. 이후 1e2b9c2에도 별도 checkout을 만들어 같은 변경을 합성하고 Go/vet/build/contracts/실PG를 재실행했다. `verify_new_main.py`와 latest-main-reconcile-check를 따른다. Web 66파일과 tree는 이전 실제 Web 실행과 동일하다. SQL9개의 canonical LF·raw CRLF 변환과 실행 중/종료 후 관측을 별도 대조한다. PG 서버 소유자는 한 번에 하나만 둔다.
8. `summarize_final.py`는 ON/OFF 각각 first12+Reset 후12·양쪽 renew 3단계·실제 boot ID와 Reset·ON16/OFF14 자원 및 SG/keypair 부재·C1 parent12·cloud parent12·source hash를 집계한다. `package_current.py` → `write_audit.py` → `verify_package.py --draft` → 독립 리뷰 → `write_audit.py --final` → 갱신된 리뷰 사본 export → `verify_package.py` 순서로 검증·봉인한다. 실행 중인 로그는 패키징하지 않는다.

실제 SaaS Preview Gateway·Browser 권한을 포함한 전체 C3, KT Cloud174 최종, 원격 CI, 다른 guest/backend, 장시간 부하/hard crash는 이 재현 범위 밖이다. 과거 helper는 당시 source manifest와 함께 사용해야 한다. 최종38파일에 과거 helper를 그대로 섞어 역사 실패를 재현했다고 주장하지 않는다. push/PR는 실행하지 않는다.
''',encoding='utf8')
print(json.dumps({'written':str(DEST/'AUDIT.md'),'source':S['source']['allSHA256']}),flush=True)
