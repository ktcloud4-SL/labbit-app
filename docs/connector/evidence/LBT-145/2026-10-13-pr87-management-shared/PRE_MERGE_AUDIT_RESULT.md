# PR #87 Management Tier shared 검증 후속 감사

2026-10-13 KST. 개인 `PRE_MERGE_AUDIT.md` 전체를 다시 읽고 findings-first, 전체 diff 대조, 추적성, 독립 reviewer 절차를 적용했다. 이전 최종 감사에서 decoder의 `sharedKnown` 도입을 두 주소 사용 지점까지 추적하지 못한 결함을 정정한다. 과거 감사와 실측은 덮어쓰지 않는다.

## 기준·수정 범위

- 수정 전 PR HEAD: `42a865041b3855dad185adf2034fbf39a36746c3`.
- 첫 제품 guard commit: `7a692097`; test-only fixture 보강: `3fed94d`.
- 최종 자동 검증 source: `804f9239b3386c4d029ef7d52df842d2f3f3c17f`. 최신 main `6e8ef04d8af18ecf377ea21d38ec75193b00e383`를 현재 fix 브랜치에 conflict 없이 merge하여 ancestor로 포함했다. main에 새로 합쳐진 LBT-163의 18파일은 main 선행 변경이며 KT PR diff에 포함하지 않는다.
- branch: `fix/LBT-158-ktcloud-d1-verification`. 기존 PR #87 follow-up이다. main push/merge, 새 PR, 클라우드 작업을 실행하지 않는다.
- 최신 main→source full diff 192파일, 비-Evidence 41파일. 이번 요청 source delta7파일: 제품 조건2개, 기존 성공 fixture1, 새 JSON 회귀1, gated 시험 도구·시험2, 기존 소유권 시험의 상태 동기화1. 전체 집합은 candidate-files.json.
- 새 HTTP/Realtime schema·DB/CI 변경은 KT diff에 없다. main의 DB operation 변경은 별도 LBT-163이고 통합 소스의 전체 Go/계약 검증으로 대조했다.

## 최신 요구·SSOT

parent가 이번 요청에서 Jira145/158 본문·AC·댓글, 확정 D-17/D-18/D-19/D-20/D-26, 테스트 전략, Dependency Map을 직접 재조회했다. 참조 및 Jira updated는 `requirements-reference.json`에 있다. reviewer도 Jira145/158 및 D18/D20를 별도로 조회했다. parent의 Confluence 응답에 version 번호가 없어 현재 번호를 추정하지 않는다.

- [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145): actual Provision/Reset/Reconcile/Cleanup, Connector 위치 SSH, test-owned residual0, SDK/KT API 구분, revision·제약 Evidence. C1 전체 수용은 LBT-16이다.
- [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158): 기존 OP-01/SDK/lifecycle 재사용, D1 endpoint/NSM 최소 호환, NSM `networkId`와 physical `refId` 구분, root/NIC/delete/UNKNOWN/secret 경계. 두 Jira는 여전히 진행 중이며 이번에 상태·댓글을 변경하지 않는다. Oct7 댓글의 미검증 상태를 최신 실측 결과로 오해하지 않는다.
- [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730): 별도 Management 접근·격리, 실제 typed ID 추적, host pin. [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283): Provider 현실 우선, 부분 확정 자원 보존, 불명 결과 후 observe-before-mutate.
- [D-17](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6914077), [D-19](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7176194), [D-26](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/28672005): outbound TLS와 credential 분리, immutable Snapshot·generation, 현재 RESET contract 경계 유지.
- [검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637)과 [Dependency Map](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/19398845): Mock/실환경/C1 구분. repo AGENTS/CONTRIBUTING/TESTING과 contracts/runtime/DB/Backend 가이드를 적용되는 경계에서 대조했다.
- [KT 공식 Tier 조회](https://cloud.kt.com/docs/open-api-guide/d/computing/tier/): 이번에 재조회한 원문의 `shared`는 Boolean이다. 누락/null을 비공유의 긍정적 증거로 사용하는 계약은 없다.

## 수정 전 findings → 회귀 → 수정

human review `5498014083`, inline `4254032823`/`4254032827`를 읽고 parent/reviewer가 직접 재현·보고한 뒤 수정했다. 이는 실제 타인 VM 인증 우회나 실환경 사고가 입증됐다는 뜻이 아니다.

| Finding | 최초 파일/라인 | 재현·영향 | 필요한 회귀와 처리 |
| --- | --- | --- | --- |
| SH-1/P2 | `terminal_target.go:47` | matching ACTIVE Tier의 shared가 missing/null이면 decoder는 Shared=false/sharedKnown=false. 기존 !Shared만으로 IP를 반환해 전용 관리망 확인을 우회 | HTTP/JSON false/true/missing/null × short/legacy 8개. `tier.sharedKnown && !tier.Shared`가 false인 응답은 empty address+ErrManagementIP |
| SH-2/P2 | `ktcloud_d1_topology.go:313–318` | 생성 때 nonshared가 확인되어도 VM 생성 후 Tier 재조회는 true/missing/null을 허용하고 SSH 준비로 진행 가능 | fresh HTTP/JSON 4개. `!tier.sharedKnown || tier.Shared`를 거절하며 prior5refs+이번 확인된 root1을 exact DeepEqual로 보존 |
| SH-3/P2 | `ktcloud_d1_network_integration_test.go:138–149`(원래 위치), cleanup의 TierID 사용 | 생성 error에 포함된 raw receipt ID도 error 확인 전에 원장에 기록되어 deferred cleanup이 DELETE 가능. 성공 관측도 Name/CIDR/shared 확인 없음 | 수정 전 보고 후 test-only helper가 production createKTCloudTier의 확정 ResourceResult만 채택하도록 변경. invalid9에서 ID·policyPOST·DELETE0, 정상1 및 confirmed BUILD partial1에서만 삭제 허용 |
| SH-4/P2 | `ktcloud_d1_tier_ownership_test.go:84/181`(원래 위치) | BUILD timeout으로 취소된 HTTP handler의 cleanup 읽기와 테스트 main의 cleanup 쓰기가 겹쳐 실제 DATA RACE. 제품 결함이 아닌 시험 상태 결함 | 원본 실패 보존·수정 전 보고 후 모든 공유 상태에 동일 mutex. timeout subcase race20 및 전체 Provider/acceptance race 재검증 |

수정 전 신규 12 leaf 중 7개가 잘못된 IP 성공으로 실패했다(Terminal short/legacy missing/null 4개, refresh true/missing/null 3개). 나머지 5개는 기존 동작에서 통과했다. 수정 후 새12개 및 기존 name5/root11개가 모두 통과했다. raw RED/GREEN를 보존했다. 기존 root 검증 test를 삭제하지 않고 정상 응답에 `shared:false`를 명시했다.

독립 full-diff 두 번째 패스에서 SH-3을 추가 확인하고 사용자에게 findings를 먼저 보고했다. 기존 fixture 동작을 helper로 추출한 local HTTP 회귀11개 중9개가 실패했고, 보강 뒤11개가 통과했다. 원본 gated 시험을 unmodified42a에서 실행한 결과가 아니다. 중간 unused-import compile 실패도 보존했다. 최신main 통합 후 별도로 SH-4 실제 DATA RACE를 검출했다. 시험 공유 상태만 mutex로 보호하고 제품 timeout/재시도는 유지했다. 최초 race failure와 후속 timeout subcase20회/full race를 구분했다. 초기 GREEN의50ms test context 이후 최종1s context를 checks-final/audited 전체 및 race에서 검증했다. test-only 수정이며 새 production 구조·리트라이·timeout 정책은 없다.

## 요구사항 → 구현 → 자동 테스트 → Evidence

| 요구·전체 diff 범위 | 현재 구현/사용 경계 | 실행한 자동 검증 | 실제 Evidence·한계 |
| --- | --- | --- | --- |
| D18 전용 Management 확인 | decoder `network.go:28–44` → creation `topology.go:126–176` → post-create `:298–318` → terminal `terminal_target.go:40–54` | 신규12 JSON/HTTP leaf, 기존 root11/name5, ownership20 및 policy matrix는 whole/race에서 실행 | 이번 새 guard는 synthetic HTTP peer 검증. 최신 source의 KT 전체 live를 실행했다고 주장하지 않음 |
| D20 불명 결과와 부분 확정 자원 | refresh root310–312 기록 후 error, Provision112–115에서 refreshed refs+UNKNOWN, 이후119 SSH/129 Startup 이전 반환 | refresh4개의 prior5+root1 보존; 전체 기존 lifecycle/UNKNOWN/partial refs 회귀 | public Provision SSH probe0은 이번 신규 E2E 실행 사실이 아니라 기존 caller의 early-return 코드 검토 근거 |
| Terminal 보안과 소비자 | app resolver 배선, runtime_provider, terminal `ssh_pty.go:259–262` error→키 읽기/Dial 이전 종료; host pin 유지 | 새 public ResolveServerAddress8개, 기존 terminal resolver/host pin/권한 회귀 whole에서 실행 | 새 원격 SSH/PTY는 실행하지 않음. 기존 실제 pinned SSH는 아래 역사 source |
| D1 최소 SDK/auth 호환 | client.go + ktcloud_d1_client.go/client_test, compute.go/query.go/quota.go | exact endpoint, gateway, token-only, reauth 및 definitive401 mutation 회귀; 전체 unit/race | 실제 SDK New/Validate/ListServers/Flavor/Image는 기존905 network Evidence. endpoint/reauth 구현은 이번 source에서 바뀌지 않음 |
| NSM actual ID/ownership/삭제 | network.go/topology.go/lifecycle.go/ktcloud_d1_delete.go/reconciliation.go, names.go | network mapping/이름경계/전체 pagination/unknown/mixed profile, ownership20, delete receipt60, policy/root 회귀 | 기존905 actual Tier/FW create/query/cleanup와 HTTP404/body202. HTTP200 body404 및 string404는 synthetic 회귀 범위 |
| Snapshot Reset/Generation | provision.go/topology.go/server.go/startup.go/quota.go/lifecycle.go, coreprovider/query.go | 기존 exact Reset set/preflight/old/new ref 및 quota credit 회귀; acceptance reset reality tests | source6a6 actual Connector WSS g1/g2 Reset·SSH·잔여0. 현재 source 전체 실측과 동일하다고 표시하지 않음 |
| owning contract와 실행 설정 | contracts/connector/README.md, runtime/README.md/contract.yaml | OpenAPI, JSON8 schemas/local refs, runtime/migration metadata, Reset schema11; whole Web163 | 이번 수정에 새 wire field/error/outcome/timeout 없음. 실제 Backend→Connector C1 전체는 별도 |
| 실행 도구·복구·시험 수명주기 | tools acceptance main/recovery/scope/ssh 및 tests, KT integration gate/fixture ledger tests | recovery exact-ID, incomplete/candidate/duplicate/partial cleanup/save failure/Reset false-positive 회귀, fixture ledger concurrent claim + 신규 fixture ownership11개(policy/실제 cleanup DELETE까지) | 도구 main을 실행하지 않음. gate를 열지 않아 cloud mutation 없음. historical bootstrap/owned0 Evidence 보존 |

전체 기존40개와 새 shared test1개를 합친41파일의 full diff와 호출자/테스트/문서 연결을 다시 확인했다. 이번 요청은 기존6파일 수정과 새 test1개이며, 나머지34파일은 수정 전42a 대비 동일하다. 독립 검토는 별도 `INDEPENDENT_REVIEW.md`에 있으며 이전 결과를 자동 승인으로 사용하지 않는다.

## 실패·보안·수명주기 분석

- 입력 경계: JSON bool false만 접근을 허용한다. true/missing/null은 current observation을 확정할 수 없으므로 주소를 반환하지 않는다. malformed 타입은 decoder/inventory error로 같은 주소 사용 지점에 진입하지 않는다. 현재 exact Tier ID/refID/name/CIDR 및 Terminal server Project/metadata/generation/name/status 검사를 유지한다.
- 상태 전이: fail→empty address/ErrManagementIP, Provision→UNKNOWN+확정 refs. 기존 READY/SSH/Startup 성공 조건을 느슨하게 하지 않는다. Terminal resolver 오류에 VMKey/IP fallback을 새로 추가하지 않는다.
- 실패·복구: 확인된 root를 공유 검증 오류로 제거하지 않는다. 무조건 rollback이나 automatic adoption을 추가하지 않는다. Reconcile candidate는 `DISCOVERED_CANDIDATE`이며 known ownership/자동 Cleanup/SSH 입력이 아니다. Cleanup은 기존 known exact typed IDs만 사용한다.
- 반복·멱등성·동시성·재시작: 새 필드는 decoder 관측값이고 두 검사는 local read-only predicates다. 새 cache/shared mutable runtime state/직접 retry/lease/operation persistence 변경이 없다. 재시작 시 새 provider observation을 그대로 검증한다. 기존 SDK reauth locking과 one-mutation/UNKNOWN Reconcile 경계를 유지한다.
- ID 재사용: 기존 Project/metadata/generation/Tier mapping/host-key pin을 유지한다. 공유 여부 검사만으로 시간 경과 후 모든 ID 재사용을 해결했다고 주장하지 않는다. 외부 Provider 조회와 SSH 사이 TOCTOU를 새로 원자화하지 않는다.
- 설정된 Connector Tier/기본 DMZ·Private는 Management VM의 전용 Tier와 다르므로 nonshared 조건을 모든 inventory에 일괄 강제하지 않는다. advisory candidate 및 known existence 조회도 SSH 허가와 다르다.
- 보안·외부 정합성: 민감값·raw provider payload logging, host-key bypass, 권한 우회 경로를 추가하지 않는다. 관측 불명확성은 success가 아닌 기존 error/UNKNOWN으로 보낸다. create/delete API 의미와 리소스 생성량·순서를 바꾸지 않는다.

## 실행·skip·실환경 구분

- 최종 source `804f9239b3386c4d029ef7d52df842d2f3f3c17f`의 fresh `checks-audited`: vet/build exit0; whole Go top-level1002, TestPASS3065, packagePASS32, fail0; Provider+acceptance race top-level129, TestPASS423, packagePASS2, fail0.
- gofmt tracked343파일 mismatch0. OpenAPI/JSON schemas8/local refs/runtime·migration metadata/Reset schema11 PASS.
- Web typecheck/lint/test/build의 원본은 첫 guard pass의 checks/에 보존:13파일163tests PASS. 최종 source와 tracked Web/workflow 입력62개 byte-identical을 확인하여 재사용한다. canvas/Vite warning은 원문대로 보존하며 E2E/시각 검사 PASS라고 하지 않는다.
- 첫 guard baseline(995/128)과 fixture 후3fed baseline(996/129)은 각각 checks/와 checks-final/에 보존한다. main 통합fa6078a 검사에서는 race가 실제 실패했고 checks-merged/ 원본을 보존했다. fixture 상태 동기화804f923 뒤 checks-audited/ fresh 검사와 구분한다. Raw JSON pass event에는 package가 포함되어 TestPASS와 별도로 집계했다.
- whole Go leafSKIP10: 표준 OpenStack live5+KT live2, Windows ledger symlink/replacement2, POSIX0600 permission1. no-test package event는 별도다.
- 로컬 PostgreSQL integration build tag는 미실행. 최종 PR HEAD의 PostgreSQL16 CI를 별도 확인하며 unit/Mock PASS를 DB/KT/C1 성공으로 확대하지 않는다.
- 이번 요청에서 KT 및 로컬 OpenStack 실환경/VM/SSH/Reset/생성/삭제를 실행하지 않았다. 새23개 회귀는 localhost HTTP·실제 SDK JSON peer 시험이며 remote cloud peer가 아니다.

## 역사 Evidence와 잔여 위험

1. `../2026-10-13-premerge/` source6a6의 실제 Connector→CC01→OP01→KT Provision/Reset/Reconcile/Cleanup·pinned SSH·owned0는 역사 실측이다. Mock SaaS를 쓴 Control acceptance를 실제 SaaS/Browser C1 PASS로 확대하지 않는다.
2. `../2026-10-13-pr87-live/` source905의 실제 Tier/FW 생성·조회·정리는 별도 네트워크 실측이다. 그 보고서의 ledger pointer-alias 한계와 보존 원본의 불완전 beforeCleanup snapshot을 유지하며 현재 observed-present JSON이라고 재해석하지 않는다.
3. 이번 명시적 shared guard는 현재 source에서 synthetic JSON/HTTP 회귀로 검증했다. 실제 KT의 missing/null/true 후속 응답을 장애 주입해 실행하지 않았다. current-source VM/SSH/Reset까지 재실행한 Evidence는 없다.
4. 기존 프로필 한계(single Linux VM/root50GiB/InternetOutbound=false/fixed unused CIDRs/NSM·Cinder quota endpoint 제약), C1 전체 수용, OP02 전체 SFTP/PTY는 그대로 별도 범위다.
5. 외부 Tier 공유 상태가 관측 뒤 변경되는 race를 원자적으로 봉쇄하지 않는다. 이번 수정은 사용 직전의 응답에 explicit false가 없으면 허용하지 않는 최소 guard다.

## 보존·재검증·최종 gate

시작 시 tracked+nonignored untracked1215개 raw SHA256 및 이번 기존 대상6파일 backup을 보존했다. 현재1203개 byte-identical, 변경12개(요청 기존6개+main 기존6개), missing0/예상 외 변경0. 새main 파일12개와 신규 shared test1개는 별도다. 과거 Evidence·미게시 폴더·lockfile 모두 보존했다. 과거433개는 당시 미커밋 source 집계로 현재 전체 파일 수와 다른 단위다. 일괄 reset/restore/clean은 수행하지 않았다.

tested-source.json은 source/테스트/계약/설정 및 main 변경을 포함한364개 Git LF blob hash를 고정한다. source checkout 후 checks-audited의 runner 두 개와 프로젝트 TESTING 명령으로 재검증할 수 있다. 런타임 경로는 각 환경에 맞게 조정하고 cloud gate는 열지 않는다. SH1/2 RED는 parent42a에 신규 shared test만 적용하여 재현한다. SH3 RED는 old gated fixture의 create/receipt/ACTIVE/refId 동작을 callable helper로 추출한 재현이다. 공개 원본과 중간 compile failure를 유지하며 과거 실제 cloud 실행이라고 표시하지 않는다.

최종 로컬 검사와 독립 full-diff 재검토는 별도 INDEPENDENT_REVIEW.md에 대조한다. publication scan과 Git-LF artifact manifest를 확인한 후 기존 PR 브랜치만 갱신한다. 새 최종 HEAD의 CI는 PR 본문에 연결한다. 이 로컬 감사 또는 CI PASS를 사람의 Request Changes 해제·merge·Jira 완료 승인으로 해석하지 않는다. historical raw 로그의 공백은 원본 보존 때문에 full diff --check 경고에 남으며 제품/신규 prose whitespace 검사는 별도로 수행한다.
