# PR #87 shared 필드 필수 확인: 독립 검토

리뷰어 `/root/kt_naming_audit`는 구현과 분리된 read-only 검토를 수행한다. source/tests/기존 공개 Evidence를 수정하지 않고 보호 설정·credentials·클라우드 API·SSH·원격 writes에 접근하지 않는다. 리뷰 산출물은 이 신규 로컬 파일에 한정한다.

## 범위 고정과 최신 원문

- 최초 검토 HEAD `42a865041b3855dad185adf2034fbf39a36746c3`, base `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`, branch `fix/LBT-158-ktcloud-d1-verification`. 최초 tracked diff0. 기존 미게시 Evidence10개 디렉터리와 web/pnpm-lock.yaml untracked는 보존 대상이며 이번 구현·검증 대상이 아니다.
- base→HEAD full191파일 중 비-Evidence40파일을 식별했다. 이번 follow-up은 Management 주소의 shared false 명시적 존재 검증 두 경로와 meaningful httptest 회귀에 한정한다. 기존 감사 통과를 자동 재사용하지 않고 current decoder/creation/refresh/terminal/candidate/callers를 다시 읽었다.
- 개인 `PRE_MERGE_AUDIT.md` 전체, repo AGENTS/CONTRIBUTING/TESTING을 이번 요청에서 다시 읽었다. 완료·PR 판정은 findings·최종 diff·실행 증거 대조 이후에 한다.
- reviewer 직접 최신 조회: [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145), updated `2026-10-07T21:04:00.690+0900`, [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158), updated `2026-10-07T21:01:01.657+0900`, 진행 중. 두 작업의 AC/범위/댓글·선행158 링크를 확인했다.
- reviewer 직접 최신 [D18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5, [D20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) v7를 읽었다. D18의 VM별 private Management 경로·실제 typed ID 및 검색 보조 경계, D20의 현실 조회·부분 성공 보존·불명 결과 후 blind mutation 금지가 적용된다.
- owning Git 계약 `contracts/connector/README.md:513–526`의 KT D1 프로필, `runtime/contract.yaml`와 README의 KT/standard 설정 분기를 current full diff에 대조했다. 새 wire field·schema·DB/runtime 정책을 도입하는 작업이 아니다. C1/전체PTY/SFTP/새 SSH retry 로직/standard OpenStack live/KT 전체 live 재실행을 새 완료조건으로 요구하지 않는다.

## 수정 전 findings

human Request Changes 두 P2를 최초 HEAD에서 reviewer가 직접 코드로 확인해 parent에게 수정 전 전달했다. 추가 동일 종류 우회 경로를 별도로 찾았다.

| ID | 심각도·최초 위치 | 재현 조건·영향·근거 | 필요한 회귀 |
| --- | --- | --- | --- |
| SH-1 | P2, terminal_target.go:47 | KT inventory에서 matching Management Tier의 shared가 누락/null이면 decoder sharedKnown=false/Shared=false. 기존 !Shared 검사만으로 주소 반환, Terminal 관리 주소로 사용 가능. 전용 nonshared Management 확인 실패를 false로 간주하는 접근 경계 결함. | 실제 JSON false/true/누락/null, 정확 서버 metadata/Project/name/status 및 bounded/legacy Tier 이름을 포함해 false만 IP 성공. 다른 경우 ErrManagementIP+empty IP, SSH 연결 전 차단. |
| SH-2 | P2, ktcloud_d1_topology.go:313–318 | create에서 비공유 Tier를 확인했더라도 VM 생성 후 재조회에서 shared true/누락/null을 검증하지 않고 ManagementIP 반환. Provision caller가 SSH Ready로 진행 가능. D18 private Management·D20 실제 조회 기준에 어긋남. | 재조회 실제 JSON false/true/누락/null, false만 성공. 그 외 empty IP+ErrManagementIP, 이미 확정한 Tier/FW/Server 및 루트 매핑 refs 보존, public Provision UNKNOWN/remote SSH probe0. |

## 영향받는 호출자·다른 Shared 경계 검토

- `ktCloudTier.UnmarshalJSON`(network:28–44)은 bool pointer로 sharedKnown을 기록한다. missing/null을 false의 명시적 증거로 바꾸지 않는다. malformed field는 full inventory decode 실패 경로이며 JSON bool false/true와 구분된다.
- Tier create(topology:126–176)는 exact-ID inventory에서 explicit shared true를 모순으로 보고 방금 추가한 소유 ref를 취소하고, 누락/null·불완전 metadata는 신규 refs를 확정하지 않는다. 이미 verified BUILD 뒤 불완전 관측은 confirmed partial refs를 유지한다. 이번 두 guard 수정이 생성·부분 실패 계약을 낮추지 않아야 한다.
- `ktServerManagementAddress`의 production 호출자는 terminal_target:54와 topology:317 두 곳뿐이다. pure function은 전달받은 Tier/주소의 형식·fixedIP/CIDR·다중주소를 처리하며, 공유 여부의 허용 권한은 두 caller가 검증해야 한다. KT가 이 둘을 우회해 사용하지 않는 `waitSSHReady(port)`는 standard port helper이며 production KT 흐름은 `waitSSHReadyAddress`이다.
- Provision:112–115에서 refresh 오류는 `refreshedResources`를 보존한 UNKNOWN을 반환한다. :119 SSH Ready 및 :129 Startup Ready 호출 전에 실패하므로 두 경로 수정으로 생성 후 shared 불명확 시 새 원격 시도를 막는다. root mapping은 :310–312에서 검증 후 추가되며 공유 검증 실패 때문에 지워지면 안 된다.
- App runtime_provider→Provider resolver 배선(app:110–134) 및 terminal.NewSSHPTYFactory:259–262에서 resolver error가 키 읽기/SSH Dial(:316) 이전에 반환된다. production fallback 주소를 새로 만들지 않는다. 이미 존재하는 host pin 정책은 별도로 유지하며 새로운 key enrollment를 추가하지 않는다.
- 후보 discovery(lifecycle:113)는 shared 누락/null의 이름 일치 Tier를 **DISCOVERED_CANDIDATE**로 표시할 수 있다. 이는 Reconcile 출력·운영 대조용이며 current consumer는 이를 자동 소유·삭제·SSH 대상에 넣지 않는다. known ResourceRef와 후보의 wire source를 보존한다. 이 advisory 후보 노출을 두 ManagementIP 우회와 같은 추가 P2라고 단정하지 않는다.
- preflight Connector Tier는 설정으로 선택한 기존 공유 기본 Tier이다. 전용 VM Management Tier의 nonshared 검사를 Connector Tier에도 동일하게 적용하는 변경은 요구사항이 아니다. known Reconcile/Cleanup은 실제 ID 존재·부재를 관측하며 새 공유 자원의 자동 adoption을 수행하지 않는다.

현재 확인된 결함은 human 두 P2이며 추가 확인된 접속 우회는 없다. 최종 수정 소스·실행 회귀·전체 체크 및 Evidence를 검토하기 전 최종 판정은 보류한다.

## 수정 후보와 직접 확인한 실행 증거

HEAD42a8650 이후 현재 staged4파일을 직접 읽었다. production 변경은 `terminal_target.go:47`의 `sharedKnown && !Shared`와 `ktcloud_d1_topology.go:314`의 `!sharedKnown || Shared` 거절 조건 두 개다. 성공 root-ownership fixture 한 곳에 명시적 shared:false가 추가되었고 새 `ktcloud_d1_management_shared_test.go`126줄이 들어갔다. 다른 SSH/재시도/Pin/Control wire/schema/자원 생성·삭제 로직 변경은 없다. 이 후보의 SH-1/SH-2는 해결되었다.

- 새 Terminal 테스트: bounded/legacy 이름 각각 false/true/누락/null의8leaf. 실제 HTTP JSON을 decode하여 false만 IP 성공, 나머지는 ErrManagementIP+emptyIP를 확인한다. 서버 Project/name/metadata/ACTIVE·Tier CIDR/refId/ACTIVE를 포함하고 handler의 mutation 금지와 Tier GET1회를 확인한다.
- 새 생성 후 refresh 테스트: 동일4형태의4leaf. 확인된 기존5refs와 새 루트 볼륨 ref를 포함한6refs가 거절 시에도 reflect.DeepEqual로 보존되는 것을 검증한다. false에서만 주소 성공한다. 실제 Cinder/Nova/NSM JSON을 사용한 localhost httptest이며 실제 클라우드·SSH 시험이 아니다.
- public Provision에서 SSH probe0 및 Terminal factory에서 resolver error 후 Dial0은 **현재 호출자의 오류 반환 순서에 대한 source review 근거**다. 이번 테스트가 public Provision 전체 파이프라인을 실행해 SSH counter0를 측정했다고 주장하지 않는다. 위 두 direct caller 테스트와 기존 Provider 전체 테스트를 대조했고, 최소 조건 수정에 대형 Provision fixture 복제를 추가 완료조건으로 요구하지 않는다.
- RED `.local/pr87-shared-20261013/before-regression.jsonl`: 새12leaf 중7FAIL/5PASS. Terminal missing/null4건, refresh true/missing/null3건이 결함을 재현한다. 상위2테스트/package 실패 event는7leaf와 중복된다.
- GREEN `after-regression.jsonl`: 새12leaf, 기존 short/legacy naming5leaf, root ownership11leaf 전부 PASS. leaf28+상위4=Test pass32, packagePASS1, fail0/skip0. 전부 구현자 실행 로그를 reviewer가 독립 재계산했으며 reviewer가 별도로 중복 실행한 시험은 아니다.

reviewer 직접 계산한 현재 LF-normalized SHA256:

| 파일 | SHA256 |
| --- | --- |
| terminal_target.go | e0df014dc68c8fbd3fd1771a60773f0036dfb8f11a698315deddc3db8e703c1f |
| ktcloud_d1_topology.go | 6fd83080916b1d291e5424d8038849702bca7ee033d2d84540f99e4a6475a574 |
| ktcloud_d1_root_ownership_test.go | ea8d2503d62f228486128b96197dbca41c64d7df374e085f395a6a2936ee1633 |
| ktcloud_d1_management_shared_test.go | 73de69235fca56ca514462204829998cf6dacda5c08cf1a6514f69de8fcd1a26 |

## 전체 후보의 재검토와 추적성 경계

base f7a9a953→현재 staged 포함 후보의 비-Evidence41파일을 확인했다. 기존40+신규 shared test1이며 이전 감사 판정을 자동 재사용하지 않았다. 변경 diff를 읽고 current KT auth/reauth/client/service mapping, NSM 전체 inventory/presence decoder/Tier receipt ownership/Policy mapping, Compute/root mapping, Cleanup/404/forceDelete/Reset/Reconcile, bounded·legacy naming, 표준 Neutron 분기 추출, quota/config, 실행 harness/recovery/typed 현실 확인, Terminal consumer 배선을 다시 대조했다. 기존 Test들과 새 raw 결과에 대해 mapping·UNKNOWN/FAILED·partial refs·candidate/known 분리·known-only 삭제와 timeout/cancel 경계의 추적성을 검토했다. 전체 Evidence191파일을 새 실환경으로 실행한 검토는 아니며, 새 guard에 직접 관련된 실행 원본과 역사적 KT source 경계를 대조했다.

| 현재 요구/SSOT | 구현/호출 경계 | 자동 검증 | 실제 검증·한계 |
| --- | --- | --- | --- |
| D18 VM private Management/확인된 주소 | Terminal47, refresh314 및 두 `ktServerManagementAddress` 호출자, Provision112–119/PTY resolver early return | 새 HTTP/JSON12leaf, 기존 name5leaf | 새 guard의 live 실행은 없음. localhost JSON 경계와 caller source 확인. |
| D20 실제 조회/부분 성공 보존 | refresh root exact mapping→upsert→shared guard; createTier confirmed/incomplete/contradiction 구분 | 새 refresh4leaf에서6refs exact 보존; 기존 root11leaf/Tier ownership tests | 기존 KT source6a6 lifecycle 및 source905 network 결과와 새 소스를 구분. |
| LBT158 SDK/KT 최소 호환·안전 리소스 생애 | current auth/NSM/model typed IDs/Cleanup404/Reset/Reconcile/profile 제한 | current full Provider/harness 포함 Go 검사; 이전 fault80cases가 이번 전체 실행에도 포함 | 새 공유값 fault를 실제 KT에 주입하지 않음. 표준 OpenStack live5+KT live2 gates SKIP. |
| LBT145 실제 Connector VM 및 자원 정리 Evidence | tracked acceptance/TLS Mock SaaS + real Provider/VM | current harness typed 현실·recovery unit/httptest | 역사적 source6a6 two-generation live PASS/source905 network-only live PASS는 보존. 이 guard 수정으로 전체 live를 새로 실행했다는 주장은 하지 않음. C1/Backend-Browser/PTY/SFTP 전체 AC는 별도. |
| Credentials/확인되지 않은 후보 자동 소유 금지 | safe Context/Mutation error, `DISCOVERED_CANDIDATE` source 분리/current consumers | existing safe-error/ownership/profile/fixture guard tests 및 publication scan 최종 확인 예정 | reviewer는 보호 설정을 읽거나 원격 동작을 수행하지 않음. |

## 현재 로컬 baseline 확인

`checks/run`은 LABBIT_/LBT145_/LBT158_ 환경을 제거한 local 검증이다. raw `checks/*.log`, 개별 `*-result.json` 및 summary를 직접 대조했다. summary의 sourceHead42a는 테스트 당시 parent HEAD이며 그 위 staged4파일을 실행했다. 최종 새 commit/source manifest로 동일성을 고정하기 전에는 HEAD42a를 테스트된 완전 source라고 단정하지 않는다.

- Go vet/test(-count=1,-p=1)/build 모두 exit0. 직접 raw JSON 재계산: top-level995, Test pass3031, packagePASS31, fail0. summary passEvents3062는 Test3031+package31이다.
- Provider+acceptance `-race -count=1`: exit0, top-level128, Test pass411+package2=413events, fail0.
- Go 전체 leafSKIP10: 표준 OpenStack live5/KT live2, Windows에서 ledger symlink/replacement2, POSIX credential0600 permission1. race에는 두 패키지 내 cloud7+Windows2 leaf가 SKIP이며 POSIX 시험 패키지는 포함되지 않는다. no-test package의 skip event를 실제 시험 SKIP에 더하지 않는다. Linux symlink/replacement 실행 성공이나 새 remote CI 성공을 Windows 로컬 결과로 확대하지 않는다.
- Web typecheck/lint/Vitest/build exit0,13파일163tests PASS. canvas unavailable 메시지와 Vite chunk warning은 raw에 보존되어 있으나 exit 실패가 아니다.
- Contracts OpenAPI/8JSON schemas/local references/runtime·migration metadata, Reset schema11tests PASS. LF-normalized gofmt332파일 mismatch0.

현재 확인된 추가 필수 결함은 없다. 이 기록은 source/diff 및 로컬 결과까지 확인한 상태이며 최종 commit·source/Evidence manifest·보존/공개 스캔·최종 prose 대조가 남았다. 실환경 미실행 경로와 CI 예정 범위는 그대로 유지한다.

## 전체 gated fixture 검토에서 추가 확인된 finding

위 '추가 없음'은 제품/caller 중간 검토 결과이다. 이후 전체 비-Evidence diff의 테스트 실행 도구를 직접 읽은 두 번째 패스에서 아래 결함을 확인하고 parent에게 수정 전 전달했다. SH-1/SH-2의 해결 판정은 유지하되 전체 후보 최종 판정은 이 추가 항목의 disposition을 확인하기 전 보류한다.

| ID | 심각도·현재 위치 | 재현 조건·영향 | 필요한 local 회귀 |
| --- | --- | --- | --- |
| SH-3 | P2, ktcloud_d1_network_integration_test.go:138–149, ktcloud_d1_fixture_ledger_test.go:110–114 | `n.createTier`가 error와 receipt ID를 함께 반환하면 error 확인 전에 ID가 state.TierID에 들어간다. cleanup은 nonempty TierID만으로 권한을 허용하므로 body409/500에 기존/shared/foreign UUID가 있으면 t.Fatal 뒤 해당 Tier DELETE가 가능하다. 성공 receipt의 이후 관측도 ACTIVE/refId만 검사하여 Name/CIDR·baseline ID·explicit shared:false를 확인하지 않고 policy 생성/cleanup 권한을 준다. 이 경로는 제품 Terminal/Provision 우회가 아니라 real-cloud gated fixture의 외부 자원 소유·삭제 결함이다. | error-with-ID 및 baseline ID, Name/CIDR mismatch, shared true/누락/null에서 owned TierID 미부여, policyPOST/DELETE0. 정확하게 일치하는 explicitfalse 성공만 confirmed TierID 및 정리 허용. localhost/mock 실행만 필요하며 cloud 재실행을 요구하지 않는다. |

이 finding은 source control flow로 직접 확인했으며 reviewer가 실제 클라우드나 gated mutation을 실행하여 재현한 것은 아니다. 소유권 없는 UUID를 새 fixture가 사용할 수 있다는 논리 경계를 보고하며, 과거 정상 KT PASS 결과에서 외부 자원이 실제로 삭제됐다고 주장하지 않는다.

### SH-3 최소 보강과 회귀 확인

parent가 사용자에게 수정 전 전달한 뒤 test-only 두 파일을 보강했다. `createKTFixtureTier`는 제품 `a.createKTCloudTier`를 재사용하고 반환된 **confirmed ResourceResult** 단일 ref에서만 ownedID를 추출한다. gated fixture는 raw receipt ID를 소유권으로 기록하지 않고 이 ownedID를 원장/cleanup에 사용한다. 성공 Tier의 RefID는 제품이 확인한 결과다. error 후 기존 matching BUILD partial ref는 소유권을 유지하지만 error receipt ID, pre-POST baseline ID, 명시적 모순 mapping, 초기 shared 누락/null에는 권한이 생기지 않는다. 제품 두 조건 이외의 production 수정은 추가하지 않았다.

추가 `TestKTFixtureTierOwnershipBeforePolicyAndCleanup`11leaf는 실제 HTTP→fixture 호출 helper→policy 생성 분기→실제 `cleanupKTFixtureKnownResources` DELETE를 관측한다. valid1에서는 policyPOST1/DELETE1; confirmed BUILD 후 lookup failure1에서는 policyPOST0, 확인된 Tier의 DELETE1로 D20 partial 보존; invalid9(errorbody409/500+ID,baselineID,wrongName,CIDR,sharedtrue/missing/null,initiallookupfail)는 policyPOST0/DELETE0 및 ownedIDempty를 검증한다.

- `before-fixture-regression.jsonl`:9leafFAIL/2PASS, 상위 및 package 실패는 별도 집계. source상 결함을 local HTTP 회귀로 재현했다.
- `fixture-compile-unused-import.jsonl`: 교체 중 unused strings import의 build 실패를 별도 보존했다. 테스트가 실행된 GREEN로 취급하지 않는다.
- `after-fixture-regression.jsonl`:11leaf+상위1=12 TestPASS/package1,fail0. parent 실행 원본을 reviewer가 직접 재계산했다. 새 KT gate/클라우드 시험은 실행하지 않았다.

현재 LF hashes: `ktcloud_d1_fixture_ledger_test.go`=`430c666eb7db70f824a7db49cdbc1498937d5065348962b9f8e1a21dcd2a4162`; `ktcloud_d1_network_integration_test.go`=`8c41d087cce2b58d16023392099d292b81db50ea4a5f6c4fd3cb96fa5b3df764`. fixture ledger hash는 최종 Git blob 및 worktree LF를 직접 재계산하여 정정했다.

SH-3도 current source/local regression 기준 해결되었다. 최초 제품 guard4파일은 commit `7a692097587d55896bd8b233bbd636d321436f2d`로 고정됐고 앞서 검토한 staged LF hashes와 일치한다. 추가 test-only2파일 보강 이후 최종 commit/source projection과 fresh wholechecks·Evidence는 별도로 확인할 예정이다. 기존 최초 baseline 결과를 SH-3 이후 최종 source 검사라고 확대하지 않는다.

### SH-3 RED의 실행 원본 경계 정정

구현자가 최종 원본 점검에서 확인한 대로 `before-fixture-regression.jsonl`은 새 회귀에 이전 fixture helper 동작을 추출해 연결한 local 재현이다. 추출 당시 별도 helper source snapshot은 저장되지 않았다. 따라서 RED9FAIL은 과거 source의 위험 동작을 localhost HTTP에서 재현한 근거이며, unmodified42a commit을 그대로 직접 실행한 완전 재현 근거라고 주장하지 않는다. initial source의 cleanup 권한 문제는 reviewer가 당시 현재 파일의 control flow에서 직접 확인했다. GREEN은 고정된3fed source 및 이후 merge source의 실제 테스트다.

## 최신 main 통합 확인과 추가 race finding

origin/main이 `6e8ef04d8af18ecf377ea21d38ec75193b00e383`으로 진행되어 clean merge한 HEAD는 `fa6078a1775b70c6c4963f134a276edb293be4de`이다. reviewer가 `merge-base --is-ancestor` exit0 및 main→HEAD 비-Evidence41파일을 직접 확인했다. main의 Operation register/status18파일은 KT 변경으로 포함하지 않았다. 새 main의 HTTP/Control wiring, repository·migration 경계를 대조했으며 Connector/Web source는 변경되지 않았다. 제품 shared guard와 SH-3 두 fixture 파일 LF hashes는3fed 대비 불변이다.

구현자가 이 merged HEAD에서 실행한 `checks-merged` raw를 독립 재계산했다. go vet/build exit0, whole Go top1002/TestPASS3065/packagePASS32/fail0, format343 mismatch0 및 Contracts PASS이다. 첫 race는 exit1이며 **실제 DATA RACE**가 있어 재실행 PASS만으로 덮지 않는다.

| ID | 심각도·파일/라인 | 재현 조건·영향 | 필요한 회귀 |
| --- | --- | --- | --- |
| SH-4 | P2, ktcloud_d1_tier_ownership_test.go:84/181 | `matching_BUILD_then_timeout`의 HTTP request가 deadline으로 반환된 뒤 test goroutine이 `cleanup=true`를 쓰는 동안 이전 handler가 같은 bool을 읽는다. merged raw는 read84/write181의 race detector stack을 직접 기록한다. 해당 fixture의 created/cleanup/postReads/posts/deleted 상태에 동기화가 없으므로 race 결과를 신뢰할 수 없고 Provider race gate를 실패시킨다. 이는 localhost 시험 코드의 결함이며 생산 Provider의 동시성 결함이나 KT 실환경 실패로 확대하지 않는다. | fixture shared state를 mutex/atomic으로 일관되게 보호. timeout subcase를 -race 반복하고 Provider+acceptance 전체 -race를 다시 실행. 최초 실패 log/result 보존. |

SH-4는 수정 전 parent에게 전달했다. 이 추가 결함 해결과 최종 source/check/Evidence 정합성 확인 전에는 후보의 최종 승인 추천을 보류한다.

### SH-4 수정 후 source·회귀 대조

구현자가 사용자에게 finding을 보고한 뒤 test-only commit `804f9239b3386c4d029ef7d52df842d2f3f3c17f`를 작성했다. reviewer는 fa607→804 diff를 직접 확인했다. handler의 shared created/cleanup/postReads/posts/deleted 접근 전체와 main의 posts snapshot, cleanup 전환, deletion snapshot에 같은 mutex를 사용한다. main이 HTTP create/Cleanup 호출 자체를 lock 안에서 실행하지 않으므로 새 호출 간 deadlock을 도입한 경로는 확인되지 않는다. 생산 코드·자원 생성/삭제·SSH 정책은 변하지 않았다.

`checks-audited/go-timeout-race-result.json` exit0 및 원본 `go-timeout-race.log`를 직접 재계산했다. 실제 `-race -count=20` timeout subcase에서 leaf20+parent20=TestPASS40, packagePASS1, fail0이다. 최종 전체 Go/race 이후 disposition과 Evidence를 별도로 확정한다. 최초 fa607 race failure는 checks-merged에 그대로 유지한다.

reviewer가804에서 시작 baseline1215개를 다시 raw SHA256로 대조하여 unchanged1203/modified12/missing0를 확인했다. 변경12개는 요청의 기존6개와 최신 main의 기존6개이다. source/테스트/계약/main 변경의364개 Git HEAD blob을 worktree LF와 대조한 mismatch는0이었다. tracked Web 및 Web workflow62개는7a→804에 변경0으로 기존 Web163 결과 재사용 근거를 확인했다. 보존 검사는 파일 동일성을 확인한 것이며 기존 미게시 Evidence가 새 시험에서 실행됐다는 뜻이 아니다.

## 최종 source·실행·Evidence 대조와 판정

최종 검토 source는 **`804f9239b3386c4d029ef7d52df842d2f3f3c17f`**, base는 **`6e8ef04d8af18ecf377ea21d38ec75193b00e383`**이다. main ancestor를 포함한 전체 후보의 비-Evidence41파일을 검토했다. 이전40개+신규 shared test1개 중 이번 요청은 기존6파일 변경+신규1파일이며 나머지34개는42a 대비 동일하다. 여기에 포함된 기존 KT 구현·모든 변경 분기·consumer·config/contracts·acceptance/fixture ownership 경계를 이번 작업에서 다시 읽고 요구사항/시험/Evidence 추적성을 확인했다. main LBT-163의18파일은 base 변경으로 구분하고 영향을 받는 Operation HTTP/Control/repository/migration 연결을 추가 대조했다. 과거 audit PASS를 자동 승인으로 재사용하지 않았다.

최종 `checks-audited` 실행 원본 및 receipts를 독립 재계산한 결과:

| 실행 범위 | 구현자 실행 원본의 reviewer 대조 결과 |
| --- | --- |
| whole Go `-json -count=1 -p=1 ./...` | exit0; top-level1002, TestPASS3065, packagePASS32, fail0. 합산passEvents3097과 일치. |
| Provider+acceptance `-race -count=1` | exit0; top-level129, TestPASS423, packagePASS2, fail0. 합산passEvents425와 일치. |
| BUILD timeout subcase `-race -count=20` | exit0; leaf20+parent20=TestPASS40, packagePASS1, fail0. |
| vet/build·format·Contracts | vet/build exit0; tracked Go343개 LF-format mismatch0; OpenAPI/JSON schemas8/localrefs/runtime migration metadata/Reset schema11 PASS. |
| Web | 첫 guard pass의 typecheck/lint/test/build exit0,13파일163tests. 최종62 tracked inputs 변경0을 확인해 원본을 재사용. 새 Web 실행·Browser E2E·시각 검증으로 주장하지 않음. |

전체 Go leafSKIP은 cloud7(표준 OpenStack5+KT2)+Windows ledger2+POSIX credential1=10, race는 cloud7+Windows2=9이다. package no-test event를 test skip에 더하지 않았다. 표준 OpenStack 실환경, 새 KT live/VM/SSH/Reset, 로컬 PostgreSQL integration build tag는 실행하지 않았다. 신규 공유12 및 fixture11의23leaf는 실제 JSON/HTTP localhost peer로 검증한 자동 회귀이다. production shared:false 거절 후 public Provision SSH0은 source caller early-return 근거이며 이번 신규 public Provision E2E counter 관측이 아니다. SSH 재시도 후 성공한 과거 관측은 사용자의 지시대로 boot/PAM readiness 맥락을 유지하고 새 결함 finding으로 분류하지 않았다.

새 Evidence `2026-10-13-pr87-management-shared`의 README·PRE_MERGE_AUDIT_RESULT·tested-source·preservation·candidate·execution summary를 직접 대조했다. Git-LF364source hashes와 worktree mismatch0, 시작 파일1215의 unchanged1203/modified12/missing0를 독립 확인했다. 원본4단계 check 폴더의58파일 export가 byte-identical이며 추가 fixture RED/GREEN/중간 compile failure3파일도 직접 대조해 동일했다. 최종 Audit는 SH-1/SH-2/SH-3/SH-4의 disposition, fa607의 실제 raceFAIL 원본,804의 fresh whole/racePASS,50ms 초기 helper GREEN과 최종1s context,SH-3 old-helper extraction RED의 원본 한계를 구분한다. 역사 source6a6의 actual two-generation lifecycle 및 source905의 network-only 실측과 alias-loss 한계는 이번 소스 실측으로 바꾸지 않았다.

### 최종 문서 finding의 처리

**SH-D1/P3:** 최종 Audit의 이전54행에 '기존 source40개는 이번 commit에서 내용이 바뀌지 않았다'라는 초기 단계 문구가 남아 source delta7 및 보존검사와 모순됐다. SH-3/SH-4 앞 빈 줄도 finding4개를 같은 Markdown 표로 표시하지 못했다. reviewer가 수정 전 parent에 전달했으며 구현자가 '기존40+새1=41, 요청 기존6변경+새1, 나머지34는42a 대비불변'으로 정정하고4행을 연속 표로 수정했다. reviewer가 변경 후 원문을 확인했다. source/test 수정·추가 실행 없이 해결된 문서 항목이다.

parent가 실행한 publication scanner 결과(2026-10-13T03:02:08.518048Z)는 workingCandidate276/commit snapshots20/blobs507/findings0이다. reviewer는 scanner source와 결과를 읽었지만 보호 credential/key 파일을 직접 읽거나 이 scanner를 실행하지 않았다. 이 시점의 스캔은 이 최종 독립 record가 복사되기 전 결과이므로, record copy 뒤 final Git-LF artifact manifest·staged blob/source equivalence·scan은 parent의 마지막 공개 gate로 별도 확인해야 한다. 최종 PR HEAD의 Go/Web/Contracts/Image 및 PostgreSQL16 CI도 원격 새 실행 결과로 구분하며 이 로컬 기록이 미실행 CI를 대신하지 않는다.

**최종 독립 판정: source804f923의 PR #87 shared follow-up 코드·전체 비-Evidence41 diff·local checks·위 Evidence 추적성 검토는 PASS. SH-1/SH-2/SH-3/SH-4 및 SH-D1은 해결됐다. 이 범위에 확인된 미해결 필수 코드 결함은 없다.** record copy 후 공개 manifest/scan/index 대조와 최종 HEAD CI를 확인하는 조건으로 기존 승인된 PR 브랜치 갱신을 추천한다. 이는 GitHub human Request Changes 해제·review 승인·merge·Jira 완료·전체 C1/PTY/SFTP 성공 또는 새 KT live 결과를 의미하지 않는다. reviewer는 source/tests/old Evidence·자격증명·클라우드·원격 상태를 변경하지 않았고 이 로컬 보고서 한 곳만 작성했다.
