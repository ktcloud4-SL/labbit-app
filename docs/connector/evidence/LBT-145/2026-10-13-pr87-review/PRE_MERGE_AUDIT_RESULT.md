# LBT-158 / LBT-145 PR #87 review follow-up audit

2026-10-13 KST. 개인 `PRE_MERGE_AUDIT.md`의 findings-first full-diff·추적성·독립 검토 절차를 적용했다. 이번 기록은 이전 감사에서 놓친 두 PR finding과 연관 경계의 후속 검토다. 이전 감사의 결함 없음 표현을 이번 두 경계의 검증으로 대체한다. 원본 감사·실측은 삭제·덮어쓰기하지 않는다.

## 기준과 전체 변경

- Repository/branch: `ktcloud4-SL/labbit-app`, `fix/LBT-158-ktcloud-d1-verification`(repo CONTRIBUTING의 Jira naming 적용).
- 수정 전 PR HEAD `b01e901b783691e393fb79cc871a4f5835ce1a49`; source fix/test `943d0bdd5e60221638886b03aa37038976cb8a5f`.
- fresh main은 `4501f447`에서 문서 1개만 변경한 `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`로 전진했다. 작업 브랜치에 충돌 없이 통합한 HEAD는 `4209eb58d31c39f03a476c415ee591e096e35459`다. main push/merge는 하지 않는다. 실행 source350개는 tested943d와 동일하다.
- full main→HEAD의 source/contract/runtime/tool40개 전 범위를 재검토했다. 기존38개는 [이전 파일별 표](../2026-10-13-premerge/PRE_MERGE_AUDIT_RESULT.md)의 집합과 같으며 4개를 이번에 수정하고 test2개를 추가했다. 아래 delta와 `candidate-files.json`의 전체 파일 집합을 대조했다.
- 기존 Oct7 Evidence13개와 premerge 원본66개를 source revision·원본 로그·해시·검증 범위 관점에서 확인·보존했다. 새 Evidence는 이번 디렉터리에 추가한다. staged/unstaged/untracked 및 ignored 실행 의존성 역할도 조사했다.
- HTTP/Realtime wire/schema와 DB migration에는 PR 변경이 없다. Connector schema 및 README의 기존 typed actual ID/generation, lifecycle/provision/reconcile caller, WSS handler result encoder, server `connectorwss/inbound.go:106–140`, `connector/router.go:361`, `connector/command.go`를 대조했다. 새 wire field/outcome/error code·durable policy는 없다.

## 신선한 요구·SSOT

이번 원문 조회는 Jira 본문/AC/최근 댓글, 확정 Confluence 원문 및 repo 규칙을 포함한다. Jira145/158은 updated Oct7·진행 중이다. 오래된 실패 댓글은 당시 소스/잔여와 구분하며 이번 작업에서 Jira를 변경하지 않았다.

| 원문 | revision과 적용 |
| --- | --- |
| [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158) | updated2026-10-07T21:01:01.657+0900; 최소 D1 경계 범위5 및 unit/live/secret 검증4 |
| [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145) | updated2026-10-07T21:04:00.690+0900; 실제 lifecycle/SSH/잔여0/SDK 구분/제약 Evidence AC5 |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | v5; actual typedID 소유권·별도 Management·host pin |
| [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) | v7; UNKNOWN observe-before-mutate·부분 확정 참조 보존·blind retry 금지 |
| [D-19](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7176194), [D-26](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/28672005) | immutable snapshot·old/new actual ID 보존 및 pre-release Reset contract correction. parent가 current 원문을 직접 재조회했다. D19v7/D26v1은 이전 감사 metadata이며 이번 응답에 version 번호는 없었다. |
| [D-17](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6914077) | 직접 current 원문; outbound TLS Credential·Control/Data 분리·reconnect≠retry |
| [검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637), [Dependency Map](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/19398845) | reviewer fresh 검증전략v3 및 parent fresh Map 원문. Mock/live 구분, C1 별도 경계 |
| repo SSOT | AGENTS/CONTRIBUTING/TESTING, Connector README/schema, Runtime YAML/README, HTTP OpenAPI/Realtime/schema/DB/Backend 책임 적용 여부 대조. 새 migration·배포 변경 없음 |
| [공식 Tier API](https://cloud.kt.com/docs/open-api-guide/d/computing/tier/) | fresh 공개 원문에서 body httpStatus·shared boolean·pagination/actualID 경계를 대조. 내부 장애 원인은 확정하지 않음 |

## 수정 전 findings → 해결

R1/R2는 사람의 review `5497457281`/comment `4253579548`·`4253579554`, 수정 전 b01e 원문이다. 관련 추가 finding도 수정 전에 사용자에게 보고했다. 외부 오삭제가 실제 일어났다는 주장이 아닌 httptest 재현 및 코드 분석 결과다.

| ID·심각도 | 위치·재현 조건 | 영향·기준 | 수정·회귀 결과 |
| --- | --- | --- | --- |
| R1/P2 | b01e topology137: POST body409+외부ID, 또는 acceptedID 조회의 name/CIDR/shared 불일치 | receipt만으로 Cleanup authority 생성; D18 실제 소유권/D20 | current topology136–168에서 error/기존 inventory 검사 후 exactID+name+CIDR+explicit nonshared 관측 때만 upsert. prior refs 보존, result와 후속 Cleanup DELETE 대조20case PASS |
| R2/P2 | b01e network262: HTTP200 본문404가 transport404의 관측 분기를 우회 | 실제 absent를 FAILED, present/incomplete를 definitive rejection으로 오판; D20 | current network259–286에서 receipt를 먼저 해석하고 공통 full-list exactID 관측. Tier/Policy×3receipt×10inventory60case PASS; 각 DELETE1회 |
| R3/P2 | b01e `ktCloudTier.Shared bool`: shared 누락/null이 false로 해석 | 비공유를 확인하지 못했는데 소유권 확정; R1과 같은 보호 경계 | current network29–44 JSON pointer presence, topology154–160 explicit 검증. 초기 missing/null은 미채택; 후속 누락은 이미확정ref 보존 |
| R4/P2 | b01e `ktCloudPage.Pagination`/list: pagination 또는 total/offset 누락/null+data[] | 불완전 조회가 absent0으로 성공; R2와 같은 관측 경계 | current network94–127의 pointer presence+기존 전체 페이지 검증. missing/null/offset/total 및 later page failure/present60행 안에서 PASS |
| R5/P2 · 수정 초안에서 발견 | matching BUILD 확인 후 다음 응답의 필드 누락을 contradiction과 동일 처리 | D20 이미확정 부분자원 ref 유실 | current topology154–162에서 명시적 다른 mapping은 quarantine, missing은UNKNOWN+기존ref 유지. BUILD→name/CIDR/sharedmissing/null과 BUILD→sharedtrue를 각각 PASS |

| delta 파일 | 처리 |
| --- | --- |
| `internal/connector/provider/openstack/ktcloud_d1_topology.go` | Tier 소유권/confirmed partial refs/contradiction quarantine |
| `internal/connector/provider/openstack/ktcloud_d1_network.go` | shared presence, pagination presence, body/transport404 공통관측 |
| `internal/connector/provider/openstack/ktcloud_d1_tier_ownership_test.go` | 신규20행; operation result의 actual typed refs/outcome 및 public 후속 Cleanup의 DELETE 검증 |
| `internal/connector/provider/openstack/ktcloud_d1_delete_receipt_test.go` | 신규60행; public Cleanup의 UNKNOWN/DELETED·참조·full paging·single DELETE 검증 |
| `internal/connector/provider/openstack/ktcloud_d1_names_test.go` | 기존 성공fixture2곳 explicitsharedfalse, 기존 assertion 삭제 없음 |
| `internal/connector/provider/openstack/ktcloud_d1_policy_ownership_test.go` | 기존 성공fixture1곳 explicitsharedfalse, 기존 assertion 삭제 없음 |

## 요구사항 → 구현 → 실행 → 실환경 추적

`O/`는 internal/connector/provider/openstack/, `A/`는 tools/ktcloud-d1-acceptance/. 현재 automatic source943d, historical actualKT source6a6을 구분한다. 이번 live를 실행하지 않았으므로 기존 actualAC는 historical evidence 유지로 판정한다. 새 fault 분기를 live 검증한 것으로 표시하지 않는다.

| 요구·AC/Decision | 구현과 자동 분기 | 실제 실행·Evidence | 공백·판정 |
| --- | --- | --- | --- |
| 158범위1 최소 인증/endpoint·기존interface | O/client/KTclient, exact endpoint/auth tests | source943d whole/race PASS; 6a6 actual auth/query | interface/auth code 그대로; actualhistorical |
| 158범위2 SDK Compute/Image·reauthlocking | O/ktcloud_d1_client.go,401only bounded retry/safeauth | source943d httptest/race PASS,6a6 actual image/flavor/VM | live tokenexpiryfault는 미실행 |
| 158범위3 공식 NSM·가짜 Neutron 금지 | O/network/topology, R1–R5 80faultbranches | before RED + final provider/whole/race GREEN | fault는 httptest, actual NSM 정상은6a6 |
| 158범위4 networkId≠refId | Tierresult/NovaNIC/Management 조회 | 기존 ID mapping tests 재실행 PASS;6a6 actual2NIC/SSH | 물리 ID 의미 유지 |
| 158범위5 root/NIC/IP/delete/Reconcile | O/delete/lifecycle/reconciliation/topology | 기존 root11/policy14 및 새80 회귀 PASS;6a6 actualReset/Cleanup | 외부 모순응답 새 live주입 없음 |
| 158검증1 method/path/error/IDs/UNKNOWN/FAILED | new Tier20/Delete60 + 기존KTmethod/reauth tests | result/후속DELETE/정확한ref/outcome 전체 actualHTTP fixture PASS | 실제foreign자원 파괴시험 미실행 |
| 158검증2 default 무클라우드 | explicit7live gatesoff | whole source943d test PASS/live7SKIP | KT gate2도 이번엔 SKIP,로컬표준5사용자금지 |
| 158검증3 ConnectorVM→CC01→OP01 KT | productionConnector+ A/harness | 6a6 actualg1/g2/SSH/Reconcile/Cleanup evidence 보존 | 943d실환경 미실행; humanreview 요구는 회귀+CI |
| 158검증4 secret/rawpayload금지 | safeerrors/unchangedprotected config, publication scan | new regressions raw sentinel 미노출, 전체 후보 scan | 인증정보 파일/개인키 미게시 |
| 145AC1 lifecycle기록 | common+KTlifecycle/A reset evidence | 6a6 two-gen PASS, source943d whole lifecycle 회귀 PASS | 새클라우드실행을 주장하지 않음 |
| 145AC2 실제Connector ManagementSSH | unchanged O/server/startup/terminal_target/A/ssh | 6a6 pinned authenticated SSH exec g1/g2 PASS | boot/PAM 초기retry는 새errorfinding아님 |
| 145AC3 테스트소유잔여0 | exactknown refs Cleanup/strict inventory | 6a6 finalowned0/baseline equality 보존, new404unit PASS | 현재이번작업은 자원0개생성 |
| 145AC4 SDK/KT경계실제구분 | SDK compute/image/volume vs NSM | 6a6 actual logs + currentmap/IDtestsPASS | standardlocalOpenStacklive 미실행 |
| 145AC5 실패원인/제약 Evidence | historicallongname + followupR1–R5 | 원본failure RED/GREEN/trace/audit추가 | KT내부구현/PAM원인 확정주장없음 |
| D18 ownership/Management/host pin | O/topology126,network29 및 priorroot/policy/SSH | earlyrejection/mapping/receipt/IDreuse/partialref20 + delete60 PASS | shared/customermapping 오삭제실환경 미주입 |
| D20 partialref/UNKNOWNobservefirst | O/topology154,network259,lifecycle98,reconciliation70 | confirmedBUILD→lookupfail/timeout/missingkeep; contradictionquarantine; fullinventory404 PASS | no newretry/timeout, uncertain no-ID는수동조사 |
| D19/D26 Reset 및 producer/consumer | unchangedcommonReset/WSS/serveroutbound/schema | fullsuite 및 Reset schema11PASS | 새snapshot/wire/backendpolicy없음 |
| D17 Control/Data 및 credentials | unchangedWSS/dispatcher/contracts/runtime | existingwholeWSS/auth tests PASS | C1 BackendBrowser/PTY/SFTP신규live아님 |

## 실패·보안·수명주기 분석과 한계

| 경로 | 확인한 동작·시험 | 남은 한계 |
| --- | --- | --- |
| definite reject/receipt ID/ID 재사용 | error 먼저 처리, POST 전 inventory ID 배제, name/CIDR alone은 소유권아님 | 외부 API가 모든 exactmapping을 거짓으로 반환하는 공격·동시tenant관리 변경은 실환경주입없음 |
| accepted→incomplete/contradiction | 최초 미확정ID 자동Cleanup배제, verifiedBUILD 후누락keep, 명시불일치quarantine | quarantine/응답유실 ID는 자동전체복구 보장없고 수동조사가필요할수있음 |
| 404 transport/body 및 list 오류 | exactID 전체 페이지, present/누락/secondpage실패 UNKNOWN·singleDELETE·refkeep | provider조회 eventual consistency의 모든 조합을 검증하지않음 |
| timeout/cancel/partialerror | 기존 context·poll값 유지, timeout후 confirmedref 유지 | source943d live timeout 주입없음 |
| duplicate/retry/restart/durable ownership | 기존 immutableSnapshot/typedgeneration/UNKNOWNreconcile 경로 재검토, 신규global mutablestate없음; racePASS | ledger는crashatomic transaction이아님, no-receipt인경우 수동조사, 실제restart fault모든조합미실행 |
| auth/TLS/hostkey/security/logging | unchanged auth/locking/hostpin, safeerrors, rawdummy sentinel tests, publication scan | 고객secret/foreignres destructive실험없음 |
| Wire/sharedcaller/profile | pointerpresence는내부JSON형태, bool조회호환유지, outcome/typedrefs기존계약; 정상fixtures명시false | 지원은singleLinux/root50GiB/InternetOutbound=false 유지 |
| SSH/SFTP/PTY·Control channels | SSH Ready선택원복상태·timeout/retry/PAM변경없음, no newchannelcode | 후속147/148/C1최종수용은이번scope외 |

## 실행 결과·skip·최초 실패

- source943d Provider final 및 provider+harness race PASS. 신규80 의미행(20+60)을 모두 실행했다. Race126 top-level/397test PASS/2package PASS다.
- whole Go 최초 실행은 FAIL: unchanged main `tracing/runtime_test.go:647`에서 elapsed299.9859ms <300ms lower bound(14.1µs 차이), context deadline exceeded. timeout초과/무기한 대기 결함은 관측되지 않았다. 최초go-test/summary raw를 보존한다. 원인을 확정하지 않는다.
- 같은 source isolated20/20 PASS, 이어 whole `-count=1 -p=1 ./...` PASS:993 top-level/3017test PASS/31package PASS/FAIL0. Windows시간 하한의 재발 가능성을 잔여위험으로 기록하며 테스트·제품 코드를 통과용으로 수정하지 않았다.
- Go formatting331 LF files/vet/build PASS. Web typecheck/lint/test13files163tests/build PASS. OpenAPI/8JSON/localrefs/Runtime/migration/Reset schema11tests PASS.
- 기본 suite test SKIP10: standardOpenStacklive5(사용자금지),KTlive2(gateoff),Windowsledger2+POSIXmode1(OS조건). 무테스트 package SKIP와 독립test 수를 구분한다. Web node_modules의 flatted Go package no-test는 producttest나live가아니다.
- 로컬PG integration은 미실행. 새HEAD CI에서 PostgreSQL16 5package 검증과Linuxfull checks를 확인한뒤 PR본문에run/head를기록한다. CI를KT/Brower E2E로확대하지 않는다.

## 보존·두 번째 검토·판정

원복이나 일괄 working tree 폐기를 하지 않았다. 시작 1142개 tracked/untracked 파일 중 1137개는 raw hash가 일치했고, 의도한 기존 source4개와 최신 main 문서1개만 달라졌다. missing0, 실행 source350개와 tested943d 간 hash mismatch0을 `preservation-check.json`/`tested-source.json`으로 확인했다. 신규 source2개와 이번 Evidence가 추가된다. 과거433개 미커밋 source는 이전 검증 당시 기준이며 현재1142개 전체파일 또는350개 실행 projection과 동일한 집합이 아니다. 기존 Evidence·KT연동·lifecycle·SSH선택원복은 유지한다.

구현을 하지 않은 `/root/kt_naming_audit`가 latest SSOT/full40file diff/callers/new80branches/raw검증/firstfailure를 독립 확인했다. 상세 결과와한계는 `INDEPENDENT_REVIEW.md`. GitHub CODEOWNER의 CHANGES_REQUESTED 승인 상태·thread를임의로해제하지않는다.

수정source의 확인된 P2 findings는 해결됐고 관련회귀/기본검증/독립리뷰 기준으로 **기존 PR 재리뷰 제출 가능**이다. 현재 GitHub 최종승인·merge 또는 modified-source 실제KT 재실행완료를뜻하지 않는다. main push/merge·cloudmutation·지원팀/Jira메시지전송을수행하지않는다.
