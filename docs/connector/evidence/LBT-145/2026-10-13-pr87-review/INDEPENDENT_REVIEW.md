# PR #87 리뷰 후속 독립 검토

## 판정과 검토 범위

**판정: 요청된 수정의 로컬 pre-merge audit 통과.** 확인된 두 human P2와 독립 검토에서 확인한 응답 누락·부분 성공 보존 경계가 해결됐고, 이번 수정 범위에서 추가로 확인된 차단 결함은 없습니다. PR 업데이트 및 재리뷰 요청을 위한 판정이며, GitHub 승인·merge나 LBT-145/158 상태 변경의 대체물이 아닙니다.

- 검토 저장소: `tmp/labbit-ktcloud-145`, 브랜치 `fix/LBT-158-ktcloud-d1-verification`.
- 최종 비교 base: `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`.
- 최종 로컬 HEAD: `4209eb58d31c39f03a476c415ee591e096e35459`.
- 이번 자동 테스트 실행 소스: `943d0bdd5e60221638886b03aa37038976cb8a5f`.
- reviewer가 직접 확인한 943d → 4209 차이는 최신 main의 `docs/frontend/deployment-handoff.md` 한 파일뿐입니다. 실행 코드·테스트·계약은 동일합니다. 해당 문서의 LBT-74 Acceptance 기록 변경은 이번 KT 오류 경계와 무관합니다.
- initial PR HEAD `b01e901b783691e393fb79cc871a4f5835ce1a49`부터 이번 수정은 6파일: production 2, 기존 fixture 2, 신규 회귀 테스트 2입니다.
- 전체 main → 최종 HEAD 범위는 비-Evidence 40파일입니다. 기존 독립 full audit의 38파일과 현재 소스의 차이를 직접 확인하여, 바뀌지 않은 34파일에는 앞선 검토를 재사용하고 이번 6파일을 전체 재검토했습니다. 전체 diff의 파일 목록, Provider 및 Control caller·계약 경계를 다시 대조했습니다. 동일 소스 확인을 새 실환경 실행으로 취급하지 않습니다.
- reviewer는 소스·테스트를 수정하지 않았고 실제 cloud API/SSH/credential 조회, 원격 push·리뷰 승인·thread resolve·외부 메시지를 수행하지 않았습니다. 직접 수행한 변경은 이 로컬 검토 문서 작성뿐입니다.

## 원문 SSOT와 추적성

이번 검토에서 직접 읽은 원문:

- 개인 `C:/Users/user/Documents/ChatGPT/3차 2/PRE_MERGE_AUDIT.md`, 저장소 `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md`.
- [PR #87 human review](https://github.com/ktcloud4-SL/labbit-app/pull/87): imcherry5778, CHANGES_REQUESTED, 2026-10-13T00:36:42Z, 원래 HEAD b01e9. 두 inline thread를 직접 읽었습니다.
- [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145): updated 2026-10-07T21:04:00.690+0900, 진행 중.
- [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158): updated 2026-10-07T21:01:01.657+0900, 진행 중.
- [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730), v5; [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283), v7; [검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637), v3.
- [KT 공식 Tier API](https://cloud.kt.com/docs/open-api-guide/d/computing/tier/): networkId/refId 구분, shared:Boolean, body httpStatus, pagination.total/offset.
- Git `contracts/connector/README.md` 488–489, 507, 524–525, 535–547; OperationResult/ResourceRef 소비자 `internal/connector/wss/handler.go`와 Cleanup/Reset 입력 검증 `internal/server/connector/command.go`.

D-17/D-19/D-26/Dependency 최신 원문은 parent가 재조회했다고 전달했으며, 이번 패스에서는 reviewer가 해당 원문을 새로 직접 조회하지 않았습니다. 이전 독립 audit 및 변경되지 않은 Reset/SSH 경계의 검토를 재사용합니다.

| 요구 | 이번 구현 / caller | 자동 검증 | 실제 검증 구분 |
| --- | --- | --- | --- |
| D18 실제 ID 소유권, 이름은 검색 보조 | topology.go:126–176; POST 오류 검사 → pre-POST ID 거절 → exact-ID/Name/CIDR/explicit non-shared 관측 후 upsert | Tier 20분기 및 후속 public Cleanup의 실제 HTTP DELETE 여부 | 새 오류 주입은 httptest. 기존 KT lifecycle은 source6a6의 역사적 실제 증거 |
| D20 확정 부분 성공 ID 보존, 불명 mutation 재실행 금지 | topology.go:149–174; incomplete/lookup/timeout에서 확인된 refs 유지; 확정 매핑 모순에서 이번 Tier 자동 삭제 권한 철회 | initial incomplete는 추가 owned0; verified BUILD 이후 missing/null/lookup/timeout 보존; POST1회 | 새로운 VM/Tier 생성 없음 |
| NSM body404·HTTP404의 현실 대조 | network.go:253–286; complete exact-ID lookup; lifecycle.go:98–155 UNKNOWN/DELETED 변환 | Tier/Policy × 3receipt × 10realities = 60분기; DELETE1회, exact ref 보존 | body404 fault의 실제 KT 재현을 주장하지 않음 |
| 불완전 inventory는 ABSENT 근거가 아님 | network.go:94–132; 필수 total/offset 포인터 검사 및 pagination/cardinality 검사 | missing/null pagination, missing total/offset, second-page failure → UNKNOWN | 문서 계약 기반 safe parser 회귀 |
| 기존 Provider/SDK/Control 계약 유지 | 인증·compute·root·SSH·Reset·recover/fixture ledger 및 wire schema 변경 없음 | 전체 Go/race/Web/Contracts 재검증 | 이전 source6a6 실제 두 세대 Reset/SSH/Reconcile/Cleanup·설치 포함 잔여0 보존; 현재 revision 실제 실행으로 확대하지 않음 |

## Findings-first와 수정 후 상태

아래 결함은 수정 전 parent에게 전달했습니다. 실제 KT 사고가 발생했다는 주장은 없습니다.

| ID / 심각도 | 수정 전 위치·재현·영향 | 회귀 요구 / 최종 상태 |
| --- | --- | --- |
| Human R1 / P2 | 원래 topology.go:136–159. body409/500+networkId 또는 후속 Name/CIDR/Shared 불일치 ID를 owned result에 넣어 이후 Cleanup이 외부 ID에 DELETE할 수 있음 | 거절·불일치·pre-existing ID에서 owned 추가0/해당 DELETE0; prior refs 유지. **해결** |
| Human R2 / P2 | 원래 network.go:235–268. HTTP200+body404를 목록 관측 없이 definite rejection으로 분류하여 absent여도 DELETE_FAILED | number/string body404와 HTTP404 공통 목록 검증; absent만 성공, present/조회 실패/불완전 UNKNOWN, DELETE1회. **해결** |
| Independent I1 / P2 | 원래 network.go:20–25. missing/null shared가 bool false로 디코딩되어 미확인 공유 여부를 소유권 확인으로 취급 | decoder sharedKnown 추가, initial missing/null에서 owned0. **해결** |
| Independent I2 / P2 | 원래 network.go:76–82,103–108. missing/null pagination 또는 total/offset 누락이 0으로 초기화돼 빈 목록을 complete/ABSENT로 잘못 승인 | 필수 값의 존재 여부 검사; 모든 receipt/자원 종류에서 incomplete UNKNOWN 및 ref 유지. **해결** |
| Independent I3 / P2, 수정 중 draft 회귀 | 초기 수정 draft topology.go:154. verified BUILD 이후 shared/Name/CIDR 누락 관측도 확정 모순과 같은 priorResources rollback으로 처리해 확인된 new ID를 잃음 | incomplete는 현재 confirmed resources 보존/UNKNOWN, nonempty 실제 mismatch 또는 explicit shared=true만 권한 철회. 4개 post-BUILD incomplete 회귀. **해결** |

최종 topology.go:154–161에서 확정 모순과 불완전 관측을 분리했습니다. Name/CIDR/shared의 오류 판단은 새 Tier 확인 전이며, other prior owned refs는 어느 분기에서도 유지됩니다. verified BUILD/ERROR ref는 일반 lookup failure·timeout·incomplete에서 보존됩니다. 확정 모순 ID는 자동 Cleanup 결과 목록에서 제외하며, 조사·Reconcile 절차가 필요합니다. wire field를 새로 추가하지 않았습니다.

최종 DELETE는 receipt를 읽은 뒤 두 형태의 404를 같은 경계로 처리합니다. complete inventory의 exact ID absent일 때만 성공합니다. present, second-page failure, pagination 누락 등에서 ErrReconcileLookup을 반환하여 public Cleanup은 UNKNOWN과 같은 tracked ref를 유지합니다. DELETE loop/retry를 추가하지 않았습니다.

## 실행 증거와 독립 검증

테스트 실행은 parent가 수행했습니다. reviewer는 결과 JSON뿐 아니라 raw JSONL/log·테스트 코드·commands를 직접 대조하고 pass/test/package/skip 집계를 독립 계산했습니다.

원본 경로: `.local/pr87-review-20261013/`.

- `before-regression.jsonl`과 `before-extended-regression.jsonl`: 수정 전 오류 경로 실패 원본 보존. 최종 20분기와 이전 draft의 case 수는 다르므로 동일한 총 case 수로 표현하지 않습니다.
- `provider-test-final.jsonl`: Provider 118 top-level, test PASS event337, FAIL0. 신규 Tier20/DELETE60 모든 leaf PASS. skip9 = 표준 OpenStack5 + KT2 + Windows symlink/replacement2.
- `checks/go-race.log`: Provider+acceptance, 126 top-level, test PASS397 + package PASS2, FAIL0; skip9는 위와 동일.
- `checks/go-vet-result.json`, `go-build-result.json`: exit0. 출력 없는 원본 로그를 성공 출력으로 꾸미지 않았습니다.
- `checks/web-*.log/result.json`: typecheck/lint/test/build exit0. Vitest13files/163tests PASS. canvas·chunk 경고는 raw에 남아 있으며 기능 실패로 확대하지 않습니다.
- `checks/contracts.log/result.json`: OpenAPI/8 Draft2020-12 schema/local refs/Runtime/migration metadata 및 Reset schema 11tests PASS.
- parent format331/mismatch0와 별개로 reviewer가 실제 read-only로 tracked Go331개 LF정규화 후 gofmt stdin 출력과 비교: mismatch0. 직접 tool transcript에 결과가 있으며 별도 raw 파일을 새로 만들지는 않았습니다.
- reviewer git diff --check 및 source delta 대조 수행. 파일을 포맷 수정하지 않았습니다.

**최초 전체 Go 실행 실패를 보존합니다.** `checks/go-test.log/result.json`에서 unchanged main의 tracing `TestShutdownIsBoundedByTheGivenBudgetWhenCollectorHangs`가 300ms 하한 대비 299.9859ms로 한 번 실패했습니다(차이14.1µs). deadline exceeded는 관측됐고 무기한/초과 shutdown을 관측한 것은 아닙니다. tracing 소스의 main→candidate diff0입니다. 시간 하한 민감성이 시사되지만 한 번의 결과로 원인을 확정하지 않습니다.

같은 실행 소스943d에서:

- `checks/tracing-isolated-20.log/result.json`: 해당 테스트 -count20, 20/20 PASS, exit0.
- `checks/go-test-sequential.log/result.json`: whole -json -count1 -p1 ./..., top-level993 / test PASS3017 / package PASS31 / FAIL0, exit0.
- sequential whole의 실제 test skip10 = live7(표준5+KT2), Windows ledger2, POSIX credential-mode1. [no test files] package skip와 섞어 live skip 수로 집계하지 않았습니다.
- 최초 parallel whole exit1를 순차 재검증의 exit0로 덮어쓰지 않습니다. 현재 required local baseline의 재검증은 통과했고, 기본 병렬 scheduling에서 해당 시간 하한 테스트가 다시 실패할 가능성은 남습니다.

## 최종 6파일 LF-normalized SHA256

| 파일 (internal/connector/provider/openstack/) | SHA256 |
| --- | --- |
| ktcloud_d1_delete_receipt_test.go | c89303b377706e53edf78deb30597b25bca10c6640db447a2d38e9d0270ff6a6 |
| ktcloud_d1_names_test.go | 807320362d6ce3a5305d8eafa901d739728ff30e36a88aa5818f8c594c8a7bb7 |
| ktcloud_d1_network.go | d0aa1e1e49ec1c5c173b9c4afcdc21a9e6b646225b126834eee4a1f2f81b2081 |
| ktcloud_d1_policy_ownership_test.go | 984bf825283b0069296d8aace4cc034ae4a4deda020c01874efc84a7f9b355bb |
| ktcloud_d1_tier_ownership_test.go | 3d2c5a7629415ea549bdd637db8fac127cb3c90993a347baa28ba126293a32bc |
| ktcloud_d1_topology.go | 89eacda68349d6cb66901f3016354365919f03d48d5334e181dd5f39f97e1c0e |

## 검토 한계와 잔여 위험

- 수정된 source943d/동일 실행소스4209에서 실제 KT mutation을 새로 수행하지 않았습니다. 오류 응답·malformed inventory·부분 성공 분기는 httptest이며, 실제 KT 전체 lifecycle은 이전 source6a6의 역사적 증거입니다.
- initial incomplete/contradictory Tier를 자동 소유로 채택하지 않으므로 manual investigation/read-only candidate 대조가 필요할 수 있습니다. name-only discovery가 자동 DELETE 권한으로 바뀌지 않습니다.
- 모든 Provider 응답 유실·metadata drift·ID 재사용·fault 분기를 실클라우드에서 주입한 것은 아닙니다. 새 tests는 HTTP 오류 계약·소유권·partial result와 exact Cleanup을 검증합니다.
- 앞선 candidate의 LC-R2/F3는 이미 보강된 recovery/ledger 검증을 유지합니다. crash-atomic persistence·process hard-kill cleanup·Backend/Browser C1·전체 PTY/SFTP/multi-VM 검증으로 확대하지 않습니다.
- 초기 SSH 재시도 후 성공 현상은 기존 guest 부팅/PAM 준비 맥락이며 이번 수정에서 오류 finding이나 새 SSH 요구로 분류하지 않았습니다. SSH timeout/pin/retry 로직에 변경이 없습니다.
- 표준 OpenStack 실환경은 사용자 지시로 실행하지 않습니다. PostgreSQL integration을 이번 local unit PASS로 간주하지 않습니다.
- 최종 공개 Evidence export/index/secret scan 및 새 HEAD GitHub CI는 이 로컬 코드 판정 이후 parent가 확인할 단계입니다. human CHANGES_REQUESTED의 최종 해제/승인은 해당 GitHub reviewer 절차를 따릅니다.
- 현재 Jira의 과거 Oct7 기록/진행 중 상태는 직접 조회 당시 그대로입니다. 코드 audit 통과를 티켓 상태 변경이나 외부 메시지 승인으로 해석하지 않습니다.

최종 검토는 구현자와 분리된 reviewer가 수행했고, 확인된 모든 후속 finding의 수정과 raw 회귀 증거를 재검토했습니다. 요청된 PR 후속 수정 범위는 반영/재리뷰 가능한 상태이며 위 검증 경계와 최초 실패 이력을 함께 유지해야 합니다.

