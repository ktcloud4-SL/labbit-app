# LBT-158 / LBT-145 — pre-merge audit

검토일: 2026-10-13 KST. 이 문서는 개인 `PRE_MERGE_AUDIT.md`의 전체 diff·추적성·findings-first·독립 검토 요구를 적용한 기록이다. 제품 계약을 새로 정의하지 않는다.

## 버전과 범위

- 저장소: `ktcloud4-SL/labbit-app`.
- 작업 브랜치: `fix/LBT-158-ktcloud-d1-verification`. `CONTRIBUTING.md`의 Jira 브랜치 규칙을 적용했다.
- 최신 main base: `4501f447d7a8c527b17ff2e08ee1d2d2fd88a8aa`.
- 최종 구현·자동 테스트·실제 Linux 바이너리의 tested source: `6a6fb4f97e676a80cfce27f85269f0627970fc9c`.
- 작업 시작 HEAD: `cad79b0acf5b165c8031f5b3594190e85b2d0151`; 종전 main base: `6d675c74bff11f481d247267faefd4ce960dcd38`. 두 커밋으로 전체 원복하지 않았다.
- 기존 dirty/untracked를 보존한 채 새 브랜치를 만들고 수정한 뒤 최신 main에 충돌 없이 rebase했다. main의 delivery·tracing·dependency·Web 변경은 그대로 포함하며 이번 PR diff의 별도 변경이 아니다.
- full candidate는 main→구현 HEAD의 38개 source/contract/runtime/tool 파일과 기존 October 7 Evidence, 이후 추가하는 이번 감사·실행 Evidence를 포함한다. staged/unstaged/untracked도 조사했다. 상세 최종 목록은 `candidate-files.json`에 기록한다.
- 구현자와 분리된 reviewer `/root/kt_naming_audit`가 전체 source diff, 관련 caller/consumer, 최신 원문 및 원본 시험 결과를 직접 검토했다. 독립 기록: `INDEPENDENT_REVIEW.md`.

이 PR은 기존 Provider Interface·CC-01·VM lifecycle 안의 **KT D1 호환 처리**다. LBT-147/148의 별도 구현을 섞지 않는다. SSH Ready 대응 지시서로 추가했던 코드는 이전 선택적 원복 상태를 유지했다. 고정 60초 대기·새 SSH 재시도/timeout·PAM 정책 변경을 추가하지 않았다.

## 최신 원문과 적용 경계

이번 검토에서 Jira 본문·AC·댓글·링크 및 Confluence 원문을 직접 재조회했다. Jira의 October 7 실패 댓글은 당시 기록이며 이번 실제 결과로 덮어쓰지 않는다. 이 작업에서 Jira 상태·댓글 또는 외부 지원 문의는 변경하지 않았다.

| 원문 | 확인한 revision / 적용 사항 |
| --- | --- |
| [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158) | updated `2026-10-07T21:01:01.657+0900`, 진행 중. 최소 인증/endpoint 경계, Gophercloud 재사용, 공식 NSM·실제 ID, root/NIC/삭제/Reconcile 및 명시적 live gate |
| [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145) | updated `2026-10-07T21:04:00.690+0900`, 진행 중. Provision/Reset/Reconcile/Cleanup, 실제 Connector→Management SSH, 테스트 소유 잔여 0 및 제한 Evidence |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | v5. 실제 typed ID 소유권, 이름/metadata는 검색 힌트, Lab·Management 분리, 관리망 SSH·ed25519·host key 검증 |
| [D-20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) | v7. immutable CreationSnapshot Reset, UNKNOWN 실제 관측 후 판단, 부분 성공 ID 보존, blind mutation retry 금지 |
| [D-17](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6914077) | 확정 원문 직접 확인. outbound WSS·TLS/인증, Control/Data 분리, reconnect≠retry |
| [검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) | v3. 요구사항 추적 및 unit/Mock/live 구분 |
| [Dependency Map](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/19398845) | v13. LBT-158→145 선행, C1 전체 공동 수용은 LBT-16 |
| D-19 v7 / D-26 v1 | 독립 reviewer가 Reset 재현성과 actual ID/generation 소유권 관련 원문을 추가 확인했다. 자세한 링크·범위는 독립 검토 기록 |
| 저장소 규칙·SSOT | `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md`; Connector README/schema, Runtime README/YAML, HTTP OpenAPI, Realtime schemas, DB migrations 및 Backend 책임 문서 |

HTTP/Realtime/DB wire·schema·migration 변경은 없다. 적용 여부를 diff와 producer `internal/server/connector/command.go`, inbound consumer, Connector WSS handler/dispatcher까지 대조했다. generic `resourceType`·actual ID·generation을 그대로 보존하므로 실제 KT 종류 추가가 fictitious Neutron ID나 wire field 변경으로 이어지지 않는다. 새 durable backend 정책·Browser flow를 만들지 않았다. 기계 계약 검증은 별도로 실제 실행했다.

최신 GitHub 공개 API에서 active `main-review`/`main-safety` ruleset을 직접 조회했다(`repository-rulesets.json`). main은 PR, CODEOWNERS 승인1명, test/quality/validate checks, unresolved thread 해소와 squash가 필요하다. CODEOWNERS는 `@imcherry5778`이다. 독립 sub-agent audit는 이 GitHub 사람의 최종 승인을 대체하지 않으며, 이번 작업은 PR 생성까지만 진행한다.

## 수정 전 findings와 처분

다섯 코드 finding은 코드 수정 전에 사용자에게 보고했다. 아래 위치는 당시 발견 revision의 위치와 현재 해결 위치를 구분한다. 외부 자원 오삭제 사고를 실제로 일으켰다는 주장이 아니라 코드 분석 및 Fake 재현으로 확인한 위험이다. 문서 정확성 PR-E1은 독립 reviewer가 정정 전에 발견·전달했고, 소스 비교 재실행 및 정정 결과를 별도 보고했다.

| ID·심각도 | 수정 전 위치·재현 조건 | 영향·SSOT | 수정과 실행한 회귀 |
| --- | --- | --- | --- |
| LC-R2 / P2 | acceptance `main.go` 약 284–334, recovery에서 개수만 맞거나 empty/missing/foreign/duplicate/UNKNOWN 관측 | 외부 ID Cleanup 및 불완전 final 관측의 거짓 성공; D-18/D-20 | `recovery.go:18`, `scope.go:41`에서 전체 typed ref·KNOWN source·state/Exists 일치 검증 후만 record/save/Cleanup, final 모든 exact ID 확인. `TestRecoveryRejectsUncertainInventoryWithoutMutationOrFalseSuccess` 전/후 24 negative, 정상 partial/absent/read-only 및 observe/cleanup/save 실패 PASS |
| F3 / P2 | network integration 이전 `main` 진입 약 85, 기존 ledger path에 `os.WriteFile` | prior test ID 영수증 유실; 기존 Provider DB 손상과는 구분 | cloud 접근 전 `O_EXCL`, 소유 FD 유지, `Lstat`/`SameFile`/regular 검증. `TestKTFixtureLedgerPreservesPreviousRunBeforeCloudAccess` subprocess·sentinel, concurrent one claim 및 owned update PASS. Windows symlink/replacement 2 subcase는 SKIP; Linux CI 확인 필요 |
| PR-R1 / P2 | network fixture 이전 약 100–155, Create ID 또는 policy job 결과가 불명인데 이름/CIDR/comment candidate 채택 | 검색 힌트가 삭제 권한으로 바뀜; D-18/D-20 | `cleanupKTFixtureKnownResources:219`는 confirmed ID 없으면 자동 정리 중단·원장 보존. unknown Tier/FAILED job/unknown job/ID 없는 결과에서 candidate lookup/DELETE 0, confirmed 정상 policy→Tier 삭제·absence PASS |
| PR-R2 / P2 | `ktcloud_d1_topology.go` 이전 295–305, 모든 attached volume ID를 검증 전에 upsert | foreign 또는 잘못 매핑된 볼륨이 Cleanup refs에 들어갈 수 있음; 실제 root 소유권 | `refreshKTCloudNICs:290`에서 단일 exact root·image·50GiB·단일 actual server attachment 검증 후 채택. `TestKTCloudRootOwnershipRequiresVerifiedMappingBeforeCleanupReference` 11 case PASS; 후속 management lookup 실패에도 이미 확정한 root 보존 |
| PR-R3 / P2 | production 이전 `topology.go:229`, fixture 이전 약 189, policy full mapping 검증 전에 ID 저장; matcher가 !disable로 empty/UNKNOWN/ERROR 허용 | 불명·잘못된 방화벽 policy를 소유 자원으로 채택 | `ktSSHPolicyMatches:236`가 exact spec 및 explicit enable 요구, single candidate/job success/full mapping 확인 후만 채택. production 14 case/fixture 11 case PASS. conservative preflight unknown-policy 차단 의미는 유지 |
| PR-E1 / P3 · 문서 | 최초 source-after manifest `verifiedAt`이 actual 최종 Cleanup보다 앞인데 post-execution으로 표시 | 실행 소스 자체 불일치가 아닌 Evidence 시간 주장 부정확 | 최종 Cleanup 이후 `2026-10-12T17:15:04Z`에 source348·binary3 재계산 mismatch0, source-after 파일·문서 정정. 추가 cloud 실행/구현 변경 없음 |
| PR-E2 / P3 · 재현 안내 | 최초 이번 README reproduction6에서 helper를 external module+replace로 빌드하도록 안내; helper는 Provider/Protocol internal import | Go internal 경계 때문에 다른 사람이 안내대로 helper 빌드 불가; 제품 구현 결함 아님 | 수정 전 사용자에게 보고, checkout/.local에 export copy 후 기존 go.mod로 빌드하도록 정정. export-copy 실제 compile 회귀 로그를 보존, cloud 재실행 없음 |
| PR-E3 / P3 · 문서 수치 | 최초 감사 초안이 Oct7 Evidence14개로 표기, actual directory/main diff는13개 | full diff 수치 추적 부정확 | actual full51=source38+Oct7Evidence13으로 문서 정정·독립 목록 확인 |
| PR-E4 / P3 · 공개본 해시 | 최초 artifact manifest가 CRLF 원본 bytes만 해시했으나 Git autocrlf로 공개 blob은 LF | Linux/same-commit artifact 검증에서 원본 hash와 공개본이 불일치할 수 있음 | 수정 전 사용자에게 보고, Git LF bytes/hash와 originalWorktree bytes/hash 함께 기록; 최종 index blob 직접 대조. 원본 로그 수정·삭제 없음 |

실행 fixture의 추가 안전성도 검토했다. 새 bootstrap helper만 별도 복사해 단일 root/image/size/server 소유 검증 후 기록하게 했고, 이전 helper는 수정하지 않았다. outer 50분/remote 45분 cap은 tracked harness 30분과 별도 teardown 8분보다 길다. 이는 이번 시험 도구의 process safety이며 제품 SSH timeout 변경이 아니다.

## 요구사항 → 구현 → 실행·Evidence 추적

구현 경로는 저장소 상대 경로이며 `O/`는 `internal/connector/provider/openstack/`, `A/`는 `tools/ktcloud-d1-acceptance/`를 뜻한다. 자동 시험은 실제 `checks/go-test.log` 및 race JSONL에서 PASS를 확인했다. 아래 live 증거는 이번 tested source에 해당하며 역사적 October 7/9 PASS로 대체하지 않는다.

| 요구사항·AC | 구현 위치 | 실행한 자동 시험·분기 | 실제 실행 Evidence / 공백·판정 |
| --- | --- | --- | --- |
| 158 범위1 최소 auth/endpoint, 기존 Provider·lifecycle | `client.go:135`, `O/ktcloud_d1_client.go:25` | `TestKTCloudD1EndpointSelectionIsExact`, SDK gateway path/validation | KT readonly New/Validate/SDK PASS; 실제 WSS query 3종 PASS. generic initializer 유지 |
| 158 범위2 Compute/Image Gophercloud·재인증 안전 | `O/ktcloud_d1_client.go:84`, query/server/quota 공통 경로 | Reauth refresh subject, only definitive401 once, non401 no retry, safe auth/token-only | images12/flavors235, VM/root 실제 lifecycle. cloud token-expiry/401 장애주입은 하지 않음; reauth는 httptest |
| 158 범위3 NSM 실제 모델·공식 경계 | `O/ktcloud_d1_network.go`, topology126/170/175 | pagination/HTTP method/path/body, job/malformed/unknown/failed, mapping | Tier/firewall 명시적 gate create/list/job/delete PASS, 정상 confirmed IDs 정리. fictitious Port/Subnet/Router/SG 없음 |
| 158 범위4 networkId≠refId | Tier creation 결과·Nova payload·NIC lookup, `topology.go:240` | `TestKTCloudTierPaginationAndDistinctIDs`, `TestKTCloudServerVolumeAndPhysicalNetworkMapping` | network ledger UUID/refId 다름, 실제 2 NIC로 VM 생성·관리 IP SSH 성공 |
| 158 범위5 root/NIC/IP/delete/Reconcile/잔여 | `topology.go:290/313`, KT delete/lifecycle/terminal target | root ownership 11, management no-Lab-fallback, soft delete one force+404, actual resource order, deleted root no DELETE | 실제 g1/g2 root50GiB/관리 NIC·SSH 및 Reset old/new actual state, final Cleanup 및 full baseline restored PASS (`live/`) |
| 158 검증1 method/path/error/ID/UNKNOWN/FAILED/reauth | 모든 KT *_test.go + recovery/scope | 잘못된 mapping/status,200 fault≠empty,400≠ABSENT,404 full-list 모순, no-blind-retry, typed recovery | 실패/응답 유실 대부분 Fake/httptest. 실제 고객 프로젝트에 foreign/candidate 사고 또는 장애주입하지 않음 |
| 158 검증2 기본 go test 무클라우드 | 두 KT test의 explicit gate, 기존 표준 live 5 gate | full default tests PASS, 7 live tests SKIP | 별도 명시적 KT2 gate actual PASS. 표준 OpenStack5는 사용자 지시로 live 미실행 |
| 158 검증3 실제 Connector→CC01→OP01 KT | production Connector + `A/main.go`/ssh | CC/WSS/dispatcher 기본 suite + Reset typed state tests | 전용 KT VM Linux production process, TLS Mock SaaS peer, 실제 KT API·Management SSH. Backend/Browser C1은 아님 |
| 158 검증4 secret/raw payload 금지 | safe errors, protected local cfg/key, exports | safeauth tests, 최종 후보 전체 publication scan | credential/private key/raw cloud failure payload 미포함; 공개 저장소 업로드 전 exact-value·PEM·비밀 패턴 및 이미지 확인 |
| 145 AC1 Provision/Reset/Reconcile/Cleanup 기록 | 공통 lifecycle + KT boundary + acceptance | unsupported Reset preflight/no mutation, exact Reset set, reset false-positive rejection | 이번 WSS g1/g2와 10 typed actual IDs의 old/new·최종 absence 관측. 전체 실행 원장·로그 유지 |
| 145 AC2 실제 Connector 위치 SSH | startup/server/terminal target·`A/ssh.go` | host key/config·management exact address, short/legacy names | 각 세대 Connector→Management pinned key login·command exec PASS. 초기 준비 중 실패·재시도는 부팅/PAM 준비 관점이며 새 오류 finding이 아님 |
| 145 AC3 테스트 소유 잔여0 | tracked known IDs Cleanup·bootstrap 별도원장 | no candidate ownership, strict final absence, empty Cleanup 보호 | Lab10 refs + bootstrap8 + network fixture2 exact absences, owned0, 7종 baseline equality PASS. 공유 DMZ/Private 및 external entry 보존 |
| 145 AC4 SDK 재사용·KT 경계 실제 구분 | client/auth/Compute/Image/Cinder SDK, NSM Tier/Firewall | exact endpoint/profile unsupported preflight | 과거 standard Neutron404 차이 보존; 이번 실제 D1 Gateway/NSM 정상. 기본 Tier는 DMZ/Private2, NSM external entry는 별도 |
| 145 AC5 실패 원인·해결·제약 Evidence | historical Oct7 record + 이번 감사/실측 | 이번 5 findings regression, 이름 경계·legacy advisory | long-name 삭제 실패는 사용자 전달 KT 지원 설명. 49자 이하 보수 경계 및 실제46자 Tier 삭제 성공. 내부 KT 구현 원인을 단정하지 않음 |
| D18 actual ownership·host pin·망 분리 | root/policy mapping/terminal target·runtime | foreign/multiattachment/duplicate/unknown/status/identity negatives | 실제 confirmed 생성 IDs, Lab fallback 없이 Management SSH. 고객 프로젝트 foreign삭제 실험은 하지 않음 |
| D20 uncertain observe-before-mutate·immutable Reset | lifecycle/reconciliation/recovery/scope | count-only/missing/foreign/final FALSE PASS 방지, failed save stops, exactoldrefs | actual Reset snapshot 동일·g1 absent/g2 present. crash/모든 API fault 조합 live 검증은 아님 |
| D17 Control/Data·인증·reconnect≠retry | 기존 WSS/dispatcher/startup 재사용 | 기존 WSS suite, tokens safe, no new protocol field | outbound production TLS WSS HELLO/ACK/heartbeat. 단회 locally generated mock SaaS credential/CA 사용, KT TLS 우회 없음 |

## 전체 파일 검토와 영향

각 행은 main 기준 full diff의 파일 단위를 명시한다. 변경 줄 외 공통 caller·Backend consumer·설정·표준 경로·기존 테스트도 검토했다.

| 파일 | 목적·요구사항·영향 |
| --- | --- |
| `contracts/connector/README.md` | 기존 Operation contract의 실제 KT resource kinds/지원 profile·ownership 경계 문서, wire schema 유지 |
| `O/client.go` | exact D1 identity 선택만 KT 초기화로 분기; generic 경로 및 원복된 SSH 설정 유지 |
| `O/ktcloud_d1_client.go` | SDK token/locking/401와 명시적 gateway clients |
| `O/ktcloud_d1_client_test.go` | auth/path/TLS/safe error/reauth 경계 |
| `O/ktcloud_d1_compute.go` | KT Nova fault envelopes·SDK mapping |
| `O/ktcloud_d1_delete.go` | exact owned soft-delete 관측·단회 forceDelete·actual404·already absent root |
| `O/ktcloud_d1_fixture_ledger_test.go` | exclusive fixture ownership·candidate미채택·full policy mapping 회귀 및 test helper |
| `O/ktcloud_d1_integration_test.go` | gated SDK readonly 실제 테스트 |
| `O/ktcloud_d1_lifecycle.go` | supported kinds/exact reset refs·known state/candidate advisory |
| `O/ktcloud_d1_names.go` | bounded hashed full lab/generation identity 이름, legacy lookup advisory |
| `O/ktcloud_d1_names_test.go` | 길이/정체성/namespace/collision·legacy·mutate-before-reject 방지 |
| `O/ktcloud_d1_network.go` | 공식 NSM API typed mapping·job/pagination·unknown safe errors |
| `O/ktcloud_d1_network_integration_test.go` | explicit fresh ledger gate, known-only fixture cleanup·actual NSM test |
| `O/ktcloud_d1_network_test.go` | method/path/payload/404 모순·blind retry 차단 |
| `O/ktcloud_d1_policy_ownership_test.go` | job success·single exact full policy enabled mapping 전에 소유 참조 없음 |
| `O/ktcloud_d1_root_ownership_test.go` | exact root 검증 전 foreign/ambiguous volume 소유 참조 없음 |
| `O/ktcloud_d1_topology.go` | profile/preflight, Tier/NIC/Firewall/Server, ID·root·management mapping |
| `O/ktcloud_d1_topology_test.go` | 실제 KT 모델의 정상/실패/UNKNOWN/reset/cleanup 경계 |
| `O/lifecycle.go` | 공통 Reset/Cleanup 순서에서 KT exact-set·typed kinds 지원 |
| `O/provision.go` | 공통 Provision reuse·profile boundary·부분확정 resources 유지 |
| `O/query.go` | KT SDK/NSM read mappings, 표준 query 유지 |
| `O/quota.go` | KT extended flavor actual capacity credit, incomplete values 거절 |
| `O/reconciliation.go` | KT exact known observation/candidate advisory, 표준 observe 유지 |
| `O/server.go` | KT boot/NIC branching, SSH Ready는 원복된 기존 구현 |
| `O/startup.go` | 실제 KT 관리 NIC 조회 연동, 기존 authenticated SSH/cloud-init·host pin 유지 |
| `O/terminal_target.go` | actual owned server 및 exact Management IP·짧은/legacy tier lookup |
| `O/topology.go` | common topology orchestration에서 KT branch만 연결 |
| `internal/connector/provider/query.go` | generic 실제 KT resource kind 상수; 문자열 소비자/producer 호환 확인 |
| `runtime/README.md` | 고객 로컬 KT config/profile·unsupported boundaries |
| `runtime/contract.yaml` | application-facing typed Provider settings, 새 SSH wait 정책 없음 |
| `A/README.md` | explicit-gate production Connector acceptance/recovery 재현·제한 |
| `A/main.go` | actual WSS two-gen execution·ledger·strict recovery callback |
| `A/recovery.go` | 입력 inventory 전체 검증 이후만 소유 자원 정리 |
| `A/recovery_test.go` | foreign/empty/duplicate/unknown·save/observe/cleanup failure·정상 복구 |
| `A/reset_evidence_test.go` | count-only Reset evidence 거짓 성공 방지 |
| `A/scope.go` | exact typed membership/state/source 검증, one/two-gen gate |
| `A/scope_test.go` | 잘못된 scope prefixture stop, uncertain cleanup observe-first, strict absence |
| `A/ssh.go` | 실제 pinned authenticated Management SSH·비저장 명령 진단 |

기존 Oct7 Evidence 13개는 당시 실패·소스·잔여·포털 화면·미전송 문의 초안까지 원본 맥락을 검토했다. 포털 이미지는 직접 보아 인증정보/연락처 노출이 없음을 확인했다. 이번 Evidence의 logs/ledgers/source exports/manifest/audit는 파일 hash·실행 source·결과 및 publication scan으로 검토한다. 새 Evidence는 제품 구현으로 오인하지 않도록 별도 경로에 둔다.

## 실패·보안·수명주기 검토

| 도출한 경로 | 확인·실행 범위 | 잔여 위험·한계 |
| --- | --- | --- |
| invalid/missing config, Linux 단일 VM·50GiB·InternetOutbound=false 외 snapshot | quota/profile preflight가 Reset cleanup 이전에 실패, invalid IDs/kinds/mixed set no mutation 회귀 PASS | multiVM/Windows/InternetOutbound=true는 지원 주장 없음 |
| Create 응답 유실·job FAILED·unknown 상태·모순200/400/404 | UNKNOWN 보존, actual known ref만 반환/관측, candidate 자동 소유·삭제 금지 회귀 PASS | 실제 KT에 실패를 의도적으로 주입하지 않았다. ID 영수증이 없으면 수동 조사 필요 |
| 부분성공 후 root/management/policy 조회 실패 | 이미 확정된 ref 보존, 미검증 새 ref 채택 거절 회귀 PASS | prior server/root 식별과 고객 credential 접근은 caller 계약 및 로컬 보안에 의존 |
| Reset old generation/identifier 재사용·collision | immutable snapshot·exact old typed set·hash에 full identity, new5 present old5 absent 실제 확인 | 이름 hash는 검색용, 소유 증명이 아님; arbitrary tenant enumeration을 권한으로 쓰지 않음 |
| Cleanup dependency·soft-delete·이미 없는 volume | policy/server/root/Tier known-only 순서·관측 수렴·단회 force·404/lists 검증 | 매 API 응답의 안정성까지 보장하지 않으며 모순/불확실하면 UNKNOWN·재조회 필요 |
| recovery read-only/invalid/final incomplete·저장 실패 | LC-R2 validation 전 전체 원장 기록·mutation 차단; strict all-known absence, failure callbacks PASS | 정상 live acceptance와 recovery success는 동의어 아님. 실제 프로세스 중단 복구는 이번 live에 주입하지 않음 |
| ledger race/prior receipt/symlink/replacement | exclusive creation·held FD/SameFile; concurrency/subprocess PASS, Unix attacks CI 예정 | `WriteAt/Truncate/Sync` 및 tracked control ledger update는 crash atomic transaction이 아니다. 원장 손상/응답유실은 자동 전체복구 보장 없음 |
| 인증 refresh·mutation 재시도·TLS | only definitive401 SDK bounded retry, no non401 blind retry; KT 정상 TLS 및 SSH host pin 유지 | 실제 expired token/401 동시성 injection은 Fake 범위 |
| Management address ambiguity·Lab fallback·SSH boot readiness | exact NIC/ref mapping 및 singleIP; host key/key auth; 두 세대 actual exec | Internet denial은 1.1.1.1 TCP22/443 두 관측만. PAM 내부 policy 수정 없음, 초기 재시도는 새 bug 아님 |
| process context/teardown·timer/channel | 기존 Control/SSH orchestration·deadline 유지, helper cap 개선·finalcleanup 별도phase | aggregate deadline에 child 종료가 teardown 완료를 보장하지 않는다. 원본 ledger 보존 후 exact-ID explicit recovery/manual cleanup 필요 |
| public Evidence·secret/raw error | publication 전에 보호된 auth exact values/PEM/민감 patterns scan, image 및 source exports 직접검토 | sample dummy credentials와 marker 문자열은 실제 secret과 구분; 실제 secret 값을 보고서에 출력하지 않음 |
| producer/consumer·shared state | generic kind strings/actualID/gen·Reset command validation·runtime/durable orchestration 유지 | Backend→Browser C1, PTY/SFTP/channel lifecycle 신규 실환경 acceptance는 LBT16/147/148 범위 |

## 실행 범위

최종 소스에서 Go formatting 329 tracked Go files, `go vet ./...`, fresh `go test -json -count=1 ./...`, `go build ./...`, provider+harness `go test -race -json -count=1`을 실행했다. 전체 기본 suite **991 top-level test / 2966 PASS events**, targeted race **124 top-level / 317 PASS events**, FAIL 0이다. PASS event에는 subtest/package 이벤트가 포함되므로 독립 test 수로 과장하지 않는다. Web typecheck/lint/test/build, 13 files/163 tests PASS. 최신 main lockfile로 npm ci 239 packages/audited240/0 vulnerabilities 확인. OpenAPI, JSON Schema8개/local refs, Runtime YAML 및 migration prefix 검증도 PASS다.

일반 Go 실행의 test SKIP 10개 이벤트를 구분한다(테스트가 없는 package 이벤트는 별도):

- 표준 OpenStack live5: 사용자 지시로 실제 로컬 OpenStack 실행 안 함.
- KT live2: 기본 suite에서는 gate off로 SKIP, 이후 별도 actual gate에서 두 개 모두 PASS.
- ledger symlink/replacement Windows2 subcase: OS 조건 때문에 SKIP, Linux GitHub Go CI 확인 대상으로 남김.
- 기존 credential POSIX mode0600 Windows1 subcase: OS 조건 때문에 SKIP, 이번 변경과 무관.

실제 KT 테스트에서는 Ubuntu24.04 / 2x4.itl / image-backed root50GiB, D1/DX-M1을 사용했다. 별도 Connector1대와 sequential Lab g1→삭제→g2를 실행했다. production Connector와 acceptance harness를 같은 tested source에서 Linux amd64로 빌드하여 pinned authenticated SSH로 배포한 뒤 remote SHA256을 확인했다. source348개와 실행 binary hash는 실행 전 고정한 `tested-source-before-live.json`이며 실행 후 mismatch0을 별도 기록한다. Go build metadata `vcs.modified=true`는 당시 미추적 Evidence가 존재하므로 이를 clean working tree라고 주장하지 않는다. 최종 Evidence-only commit이 바이너리의 byte-identical 재빌드를 보장한다는 주장도 하지 않는다.

Mock/Fake 경계: Go negative mapping/reauth/fault/ownership/recovery suite 및 locally generated TLS Mock SaaS peer. 실제 경계: KT identity/Compute/Image/Cinder/NSM, production Connector process/CC01/OP01, VM 생성/Reset/관리망 pinned SSH·명령/Cleanup/잔여 조회. 실제 Backend/Browser를 지나지 않았으며 C1 전체 PASS가 아니다. PostgreSQL integration은 로컬에서 실행하지 않았고 PR CI의 PostgreSQL16 service에서 별도로 확인한다. DB 변경은 없다.

## 보존과 재현

수정·rebase 전에 tracked/untracked 1066파일 원본(25,347,919 bytes), status, HEAD→dirty 및 이전 main→candidate binary patches, SHA256 manifest를 `.local/premerge-20261013/before`에 백업했다. `preservation-check.json`에서 1046 byte-identical, line-ending-only7, 내용변경12(최신 main7 + 승인된 recovery/ownership4 + Oct7 Evidence 최신 index1), main의 delivery workflow rename1을 분류했다. 예상 밖 내용변경·파일 유실은 0이다. 새 회귀 파일과 이번 Evidence는 별도 추가다. 기존 Evidence 원본 byte는 백업에 남으며 EOL/index 업데이트를 원본 byte 불변으로 과장하지 않는다.

이전 SSH Ready 검증의 433개 소스는 선택적 원복 시 신규 SSH Ready2파일을 제거하여 431개로 줄었으며 원복 확인 기록은 기존 경로에 유지한다. 이번 검사에 기록한 1066 전체 파일·329 Go파일·348 frozen source의 범위는 각각 달라 같은 수로 비교하지 않는다. 기존 KT Provider/lifecycle/SSH·SFTP·PTY/WSS와 다른 미커밋 작업을 일괄 폐기하지 않았다. 전체 reset/restore/clean은 실행하지 않았다.

October9/12 및 선택적 원복의 기존 미추적 Evidence·helper는 로컬에 보존한다. 이번 PR은 당시 Oct7 tracked history와 최종 tested source의 새 proof를 게재하며, 원복된 SSH Ready 기능의 Evidence를 현재 기능으로 다시 게재하지 않는다. npm 대신 검증에 사용한 pnpm이 만든 미추적 `web/pnpm-lock.yaml`도 제품 dependency diff에 포함하지 않는다.

다른 사람이 실행하려면 해당 commit checkout, Go version/go.mod 및 Node lockfile, 고객 로컬의 보호된 clouds.yaml·SSH key, 명시적 D1 gate와 겹치지 않는 CIDRs, absolute **fresh** ledgers가 필요하다. `A/README.md`, 이번 `README.md` 및 `bootstrap-helper-source`가 실행 절차를 보존한다. 공개 source export는 `.go.txt`로 저장하여 `go test ./...`가 이 helper 복사본을 별도 production package로 수집하지 않는다. 로컬 absolute paths는 입력 위치이며 인증정보가 아니다.

## 최종 판정

**PR 가능 — 현재 KT D1 호환 처리 범위의 구현·회귀·최신 SSOT 추적·실제 검증·독립 재검토를 충족했다.**

두 KT gate와 실제 Connector 두 세대 Provision/Reset/Reconcile/Management SSH/Cleanup PASS, Lab10·bootstrap8·별도 network fixture2의 소유 자원 잔여0 및 seven-kind baseline exact equality를 확인했다. 다섯 코드 finding과 문서 PR-E1/E2/E3/E4는 해결·재검증됐고, 독립 reviewer도 미해결 PR 차단 결함을 확인하지 못했다. 최종 source348/binary3 mismatch0, 기존 작업의 예상 밖 유실0, 공개 exact-secret/PEM/pattern 검사 findings0을 확인하고 목록·artifact hash를 고정한다.

Jira에 기재된 145 선행 검증 AC와 158 호환 구현/검증 항목은 이번 지원 profile·실행 경계에서 충족했다. 이는 Jira 상태를 자동으로 Done으로 변경하거나 범위 밖 전체 기능을 완료 처리한다는 뜻은 아니다. 표준 OpenStack live5는 사용자 지시로 미실행이며, OS-specific3 subcase와 PostgreSQL integration은 Linux PR CI의 별도 확인 대상이다. Backend/Browser C1, SFTP/PTY, 모든 cloud fault/crash injection 및 지원 profile 밖 조합을 PASS로 확대하지 않는다.

GitHub 필수 checks와 CODEOWNERS 사람의 승인은 merge 전 별도 절차다. 이 판정의 실행 허용 범위는 사용자가 승인한 작업 브랜치 push와 새 PR이며, main push·merge·배포·Jira/지원팀 메시지 전송은 수행하지 않는다. 최종 commit에서 tested source 이후에는 Evidence/index만 추가하며 실행 구현 변경이 발생하면 이 판정을 재사용하지 않는다.
