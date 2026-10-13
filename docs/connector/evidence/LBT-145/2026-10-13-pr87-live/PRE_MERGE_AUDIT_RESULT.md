# PR #87 KT D1 actual revalidation 감사 보완

## 판정·full diff 기준

수정된 production Tier/Firewall 생성·조회·삭제 경계의 실제 KT D1 재검증이 PASS했다. source HEAD `905ed25d1f2e04022bb814b83b61fb8eaa490366`, production 수정 `943d0bdd`, 최신 main/base `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`다. main fetch 후 base 변경 없음. 제품 소스/기존 테스트/계약/런타임 변경0이며 execution source350개의 Git LF 및 실행 working bytes 정규화 해시를 대조했다.

이전 full diff159파일/비-Evidence40파일의 [감사](../2026-10-13-pr87-review/PRE_MERGE_AUDIT_RESULT.md)·[독립 검토](../2026-10-13-pr87-review/INDEPENDENT_REVIEW.md)를 보존하고, 이번 새 Evidence 전체와 실행에 사용한 ignored overlay/runner를 추가 검토했다. `candidate-files.json`은 이번 공개 예정 full diff의 전체 파일 목록이다. 기존 nonignored1183개 파일의 raw hash 변경/유실0이며 SSH Ready 선택적 원복, 기존 LBT 작업과 old Evidence는 그대로다.

개인 `C:/Users/user/Documents/ChatGPT/3차 2/PRE_MERGE_AUDIT.md`, repo AGENTS/CONTRIBUTING/TESTING을 적용했다. 이번 turn의 최신 LBT-145/158 원문 및 D18/D20 원문을 다시 조회했다. 다른 관련 확정 Decision/계약 원문은 source unchanged로 직전 full audit의 적용 분석을 유지했다. 새 HTTP/WSS/DB/runtime 정책이나 wire field를 만들지 않았으므로 새 계약 구현은 해당 없음이다. Evidence/tool scope는 별도로 분석한다. Jira 상태를 완료로 바꾸지 않는다.

## Findings-first와 해결

실제 변이 전에 구현과 분리된 reviewer가 아래 두 도구 P2를 확인했다. 사용자에게 먼저 요약을 보고한 뒤 도구만 수정했다. 제품 결함으로 확대하지 않는다.

| ID·심각도 | 수정 전 위치·재현 조건 | 영향·근거 | 처리·회귀 |
| --- | --- | --- | --- |
| KT-L1/P2 | `run.py:43`, helper context8분 종료 근처 + fallback5분 | 기본 go test10분 제한이 Cleanup을 중단하여 시험 자원이 남을 가능성; resource lifecycle 요구 | final runner 전체20분, operation8분+별도 confirmation2분+fallback5분보다 길게 설정. gate-off compile·vet 원본 exit0, 실제 live exit0. 13분 지연을 실제로 유발하지 않았으므로 static deadline 계산과 실행 명령 증거로 구분 |
| KT-L2/P2 | initial `pr87_live_test.go:419–447`, 반환값을 refs/transport 권한에 넣은 뒤 기본 Tier/매핑 assertion 실패 | fallback이 검증 전 refs를 삭제할 수 있음. production을 잘못됐다고 가정하는 감사 및 D18 exact ownership 기준 | raw 반환값과 cleanup authority 분리. 모든 baseline ID 배제·type/gen/logical/중복·Tier exact name/CIDR/명시sharedfalse·policy exact ID/comment/singlecomment/fullmapping/NIC 반환 확인 **후** 권한 부여. 19 invalid DELETE0 + validpartial/full2 = 21 local leaf PASS |

최종 overlay SHA256 `fa22e3518cecb9a2c5ce2d0370844537f4b940f3f14a259df5b1abffea1f386f`, runner `0e2354a62dde91e020ec8ace811582a43e4b857c75302b49c859b99f78074cbb`. 두 findings 해결 후 independent pre-live 판정을 받고 새 Tier1/Firewall1, VM0 범위에서 실행했다. `INDEPENDENT_REVIEW.md`에서 실제 raw와 branch 검토 결과를 확인할 수 있다.

실행 후 추가 **KT-E1/P2**를 reviewer가 확인했고 사용자에게 수정 전 보고했다. helper648–651/661–662/678–679는 같은 Reconcile 지역변수의 주소를 저장한 후 재대입하여 원장 첫 present snapshot을 최종 absent로 덮어썼다. Cleanup result도657/674에서 alias된다(이번 두 결과 내용 동일). initial present 검사는 executed guard와 raw stage 통과로 증명되지만 원장 첫 값은 유실됐다. 원장/실제 실행 helper를 사후 변경하지 않았다. `result.json`도 snapshotLost/basis를 명시한다.

기록 도구만 별도 copy에 deep snapshot으로 보완했다: `snapshot-fixed-overlay-test.go.txt`, SHA256 `035d7bc297829092ed7ac08f8b01ac0abafb4f6f38326e17a5232402205f1f83`. local24 leaf PASS = 기존ownership21 + phase 재대입/sharedslice/cleanupUNKNOWN/SafeError 복사3. 제품 source 변경0, fixed helper는 cloud 미실행이며 초기 실측 snapshot을 복원한 것이 아니다. 추가 자원 생성 없이 기록 한계를 명시하여 좁은 실제 생성·조회·삭제 결과를 판정한다. 전체 immutable live phase 재측정은 이번 범위에서 미실행이다.

## 요구사항 → 구현 → 테스트 → actual Evidence

현재 Jira: [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145), updated `2026-10-07T21:04:00.690+0900`; [LBT-158](https://samsunglions.atlassian.net/browse/LBT-158), updated `2026-10-07T21:01:01.657+0900`. `O/`는 `internal/connector/provider/openstack/`. 전 AC의 세부 원문·코드/테스트 추적은 직전 full audit 표를 유지하며, 아래는 이번 actual run이 변경한 검증 판단이다.

| AC/Decision | 구현·자동 분기 | 실제 실행(source905) | 공백·판정 |
| --- | --- | --- | --- |
| 158 범위1 최소 auth/endpoint·기존 interface | O/client,ktcloud_d1_client; 기존 safeauth/endpoint unit | public New/ValidateConnection 실제 PASS; read-only 기존 gate도 실제 PASS | token expiry/reauth fault actual 미주입 |
| 158 범위2 Compute/Image SDK 재사용 | O/query/sdk | 실제 servers0/flavors235/images12 | VM/Volume 생성은 historical6a6, 수정본 새 VM 없음 |
| 158 범위3 공식 NSM 최소 adapter | O/network,topology; Tier20/Delete60 자동 회귀 | production prepareKTCloudNICs→Tier POST201→full mapping→policyjob RUNNING/SUCCESS→strict full policy mapping | 정상 실제 생성/조회/삭제 경계 충족; timeout·foreignID 등은 httptest |
| 158 범위4 networkId/refId 분리 | O/topology126,183; current overlay exact ownership guard | Tier ID b10c… ≠ ref d299…,46자/정확CIDR/명시sharedfalse; typedTier+FW2 | Nova에 refId로 VM NIC attachment를 새로 실행한 것은 아님 |
| 158 범위5 root/NIC/IP/Delete/Reconcile | O/lifecycle,network253,ktcloud lifecycle; 기존 root/policy/80fault 회귀 | public Reconcile KNOWN_RESOURCE2 존재→Cleanup SUCCEEDED/DELETED2→정확ABSENT2,baseline equality | root50GiB/actualNIC/privateIP/VMReset/SSH는 source6a6 historical 보존 |
| 158 검증1 method/path/error/ID/UNKNOWN/FAILED | 기존 80httptest + 이번 guard21 | 실제 HTTP404/body숫자404 후 full exact-ID inventory→이미부재Cleanup SUCCEEDED | HTTP200body404/문자열404/불완전pagination은 actual 미발생; 이전 자동 회귀 |
| 158 검증2 기본 무클라우드 | independent gates/환경 제거 | gate-off compile SKIP;21 guard는 in-process,실환경 gate만 명시로 열림 | 표준 로컬 OpenStacklive 미실행 |
| 158 검증3 Connector VM→CC01→OP01 | unchanged control/provider harness | 이번에는 local host의 actual OP01 네트워크 경계 | Connector VM/WSS/VM 두 generation은 source6a6 historical; 새 full경로PASS 주장없음 |
| 158 검증4 secret/rawprovider 금지 | safeerrors,actual trace allowlist,publication scan | numericstatus/pagination/job/ownedIDs만 내보냄; headers/secret/rawerror/body 없음 | protected cfg 및 개인키 필요,공개하지 않음 |
| 145 AC1 lifecycle·AC2 실제 Management SSH | unchanged Provision/Reset/SSH | 새 시험은 네트워크 creation/query/cleanup subset | 전체 VMlifecycle/SSH는6a6 historical; 수정본으로 새 VM/SSH를 검증했다고 표시하지 않음 |
| 145 AC3 시험소유잔여0 | exact typed refs,Cleanup,full lists | 이번 Tier1+FW1 삭제,exactAbsent2,기본 DMZ/Private 및externalentry 보존;policy/server0 baseline동일 | 이번 생성은2개 network resource에 한정;7종 전체audit는기존6a6 |
| 145 AC4 SDK/API 경계·AC5 실패/제약 Evidence | SDKqueries vs NSM,실행버전/원본 | current source·real API branch·19guard·실환경/Mock 구분 보완 | KT내부/PAM원인 확정·C1/SFTP/PTY완료 주장없음 |
| D18 소유권/Management/host pin | O/topology126,183; pure fixtureguard241 | post before-inventory 기존ID제외,exactmetadata비공유 확인 후 typedrefs 기록·onlyexactID삭제 실제 | 공유/foreign자원 파괴시험 미실행; no SSHhostpin새검증 |
| D20 UNKNOWN/partialref/observefirst | O/network253,topology126/lifecycle/reconciliation | 삭제부재확인→별도Cleanup actual404→completeinventory→성공. localguard 정상partialTier보존 | uncertainjob/noID는 자동후보채택 안함;crashdurable recovery/모든delay조합미검증 |
| D17/D19/D26 계약·Reset/Control boundary | unchanged WSS/runtime/schema/Reset | production source350·contracts의 직전검증과 동일 | 이번 overlay/metadata는 계약 변경아님;Browser/C1전체는미실행 |

## 실패·보안·수명주기와 테스트 범위

- 전체 목록의 정확 offset/total·non-nil data를 production 코드가 확인했다. 현재 계정은 각 목록이 단일 페이지이므로 실제 multi-page fault 증명으로 확대하지 않는다.
- 실제 소유권은 반환 문자열만으로 만들지 않았다. production 확인 뒤 fixture에서도 모든 기존 ID·type/gen/logical·metadata와 firewall1 mapping을 확인하여 transport/fallback 권한을 부여한다. 생성 전 원장을 exclusive 생성하고 응답/원본 refs는 authority와 분리해 기록한다.
- 정상 Cleanup은 정책 먼저→완전 부재→Tier,단회 DELETE다. 추가 DELETE는 불명확한 삭제 재시도가 아닌,exact 부재 및 baseline 복원을 확인한 뒤 intentional idempotency 시험이다. 실제 GET 후속 관측은 원장 순서에 있다.
- 실패/ID 미확정/정책attempted미확정/전체조회실패는 수동확인 원장을 남기며 이름으로 후보를 채택하지 않는다. 실제 그 fault를 유발하지 않았다. fallback/ledger는 모든 crash/동시외부관리 race의 자동복구를 보장하지 않는다.
- transport는 기존 verified TLS/실제 SDK transport와 응답을 유지한다. 금지 mutation만 fail-closed로 차단한다. headers나 raw payload를 게시하지 않는다. 제품의 재인증·poll·timeout·SSH/PAM 코드는 수정0이다.
- 실제 실행: read-only gate1 PASS + currentproductionnetwork live1 PASS. initial fixture guard21 PASS,compile1 SKIP,vetPASS. 별도 snapshot-fixed local24 PASS; 이를 새 real KT 실행으로 세지 않는다. 기존 whole/race/Web/contracts 결과는 source동일로 유지하며 재실행하지 않았다. CI는 actualKT 증거와 구분한다.
- 독립 reviewer는 수정 전 findings, guard raw, 최종 helper/runner hash,actualledger/GoJSONL/fullbaseline/typeIDs를 별도검토한다. 본 감사의 판정은 수정 boundary에 한정하며 더 큰 Jira작업전체나 인간 GitHub승인을 대체하지 않는다.

사용자 지시에 따라 기존 작업 브랜치와 PR #87만 업데이트한다. main push/merge, 기존 Evidence 삭제, 로컬 OpenStacklive, 새 SSH기능, 외부 지원 메시지는 수행하지 않는다.
