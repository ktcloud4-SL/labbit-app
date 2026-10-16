# LBT-173 KT 실행 후 audit addendum

> 이 문서는 최초 KT 실패·복구 실행의 감사다. 이후 별도의 [새 fixture 전체 PASS audit](full-fixture/AUDIT.md)와 [실행 결과](full-fixture/README.md)를 최신 판정으로 참조한다. 아래 당시 실패·한계는 그대로 보존한다.

2026-10-16 Asia/Seoul. 사용자 지정 PRE_MERGE_AUDIT.md 전체를 다시 읽고 [이전 전체 audit](../AUDIT.md)에 이번 변경/실행을 추가 대조했다. **판정: LBT-173 forwarding 구현과 opt-in KT component smoke 변경 범위는 PR 가능. 원래 KT Control fixture 전체 lifecycle은 FAILED이며 전체 일괄 PASS로 판정하지 않는다.** 원래 Control fixture exit1과 standalone forwarding PASS/복구 후 cleanup은 구분한다.

## 기준과 범위

브랜치 `codex/feat/LBT-173-ssh-tcp-forwarding`, base/HEAD `88097be5d9bb6996b718fd185515ca324629fda6`, staged 없음/미커밋 working tree다. [최종 source](source-manifest.json) 542파일/[전체 code+tool diff](source-diff.patch), [실제 smoke source](live-source-manifest.json)를 따로 고정했다. 마지막 alignment 차이0. production/runtime/contracts/web는 로컬 audit 이후 변경0이므로 이전 production traceability/failure 분석과 local live/race/Web 결과가 그대로 적용된다.

최신 [LBT-173](https://samsunglions.atlassian.net/browse/LBT-173) 원문/댓글을 다시 읽었으며 updated `2026-10-16T09:23:53.415+0900`, 댓글0으로 불변이다. [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730)도 v5를 다시 읽어 private management SSH/shared independent channel 결정을 대조했다. TESTING/Connector §7b/schema/Runtime 적용은 이전 audit 표와 같다. 외부 field/DB/HTTP/Browser 계약이나 dependency는 추가하지 않았다. 새 env는 opt-in operator fixture 선택값이며 production Runtime 설정이 아니다.

최신 main `6755fc15062446b4bf0931e8e8be03efdb2563ef`는 LBT-22 C2 loopback test/문서만 변경한다. 전체 파일 목록과 영향 경계를 확인했고 production/계약 불변이다. 새 C2 test를 외부 overlay로 현재 feature에 추가한 실행이 PASS했다. 현재 branch base는 유지했다. 이 overlay 결과를 최신 main 전체 integration/실 KT C2로 확대하지 않는다.

## 전체 diff 추가 검토

이전 12개 production/test/runtime 파일과 Evidence 전부에 더해 다음을 검토했다. 새 파일과 untracked Evidence까지 포함한다.

| 파일 | 목적·영향 |
| --- | --- |
| tools/ktcloud-d1-acceptance/main.go | opt-in gate, HELLO capability, gen1/Reset/gen2 component hook, cleanup/shutdown 실패 전파, recovery 조회 범위 |
| tools/ktcloud-d1-acceptance/preview.go | 3분 fixture 취소 시 manager Close, loopback HTTP, managed PTY/SFTP, transport 메모리 비교, target/port/refused/stale 검증 |
| tools/ktcloud-d1-acceptance/preview_test.go | mode/HELLO/HTTP exact status/body/truncated/oversize 회귀 |
| tools/ktcloud-d1-acceptance/recovery.go, recovery_test.go | prior DELETED도 fresh query, 현재 present만 mutation. absent/reappeared 회귀 |
| tools/ktcloud-op02-bootstrap/deploy.go, preview_mode_test.go | 기존 OP02 default 보존/Preview exclusive 배포/recovery 두 mode clear, 배포 digest 검증 유지 |
| tools/ktcloud-op02-bootstrap/README.md | Preview 전용 재현·범위·복구 경계 |
| ktcloud Evidence와 상위 안내 | 원본 실패와 복구 분리, source/binary identity, metadata exporter, 최신 main overlay, 독립 review |

자원 lifecycle 코드/NSM API/Provider mutation 의미는 바꾸지 않았다. 변경 없는 third-party 구현 전체/운영 SaaS log pipeline/Browser 전체를 감사한 것은 아니다. ignored credential/private key/host pin/remote TLS fixture 파일은 실행 의존성으로 확인하되 Git/Evidence에 복사하지 않았다.

## 요구사항 → 구현 → 자동 테스트 → 실환경

| 요구/SSOT | 구현 | 자동 실행 | 실제 실행·tested source/Evidence | 공백·판정 |
| --- | --- | --- | --- | --- |
| KT standalone smoke (Jira AC4) | preview.go:51, main.go:443/517 | TestPreviewScopeRequiresExclusiveFreshTwoGenerations/Capability/HTTP | live source+remote binary digest, 두 세대 각2회 HTTP200/exact body, live-lifecycle | 충족: forwarding component. 원래 전체 Control run FAIL 별도 |
| 4field/현재 VM/관리망/pin (AC2/D18/§7b) | 공통 Provider/VMAccess/SSHForwarder, preview.go guards | 이전 wrong-target/pin/offline tests + 최종 full suite | 실제 wrong4target, 실제 Reset replacement/old absent+new present, stale 두 조합 거절 | KT host pin 실패/SSH 장애 주입은 미실행. 자동 fixture 구분 |
| exact port/22차단/정책경계 | 공통 tcp.go/forwarder.go, preview.go | wire destination/unsafe port/error sanitization | KT3000 실제전달/3001 APP_NOT_RUNNING/-1,0,22,65536 거절 | Backend allowlist 승인 자체는 SaaS 책임 |
| shared SSH/독립 PTY·SFTP·TCP (AC3/D18) | 한 fixture manager에 모든 channel | 이전7package race와 신규 tools race | 양 세대 SSH tuple 메모리 비교, forwarding Close 후 PTY/SFTP 성공 | production child Preview WSS/전체 File API 실환경 미실행 |
| Production 주입/capability | 공통 app.go + main.go:335 | production fallback/cap tests | 실제 production child HELLO에서 preview-v1 존재 요구 | child Forwarder의 Data WSS를 실행하지 않음 |
| failure/timeout/cancel/sibling | preview.go context.AfterFunc/managed Conn10s | 기존 fault/race, 독립 HTTP stall probe | 실제4회 close와 sibling 회귀 | 비협조 SSH peer는 in-process. 실 KT fault 주입 없음 |
| 소유권/복구/cleanup (TESTING/D18) | 기존 Control+typed Reconcile, recovery.go:18 | unknown/candidate/exact-ID tests, TestRecoveryAfterReadOnlyDeletedLedgerRechecksAllIDs | original Cleanup FAIL→teardown SUCCESS→readonly10 absent→고친 recovery10 absent→bootstrap8 cleanup→7종 exact baseline | 원래 rejection 원인은 미확인. 원래 journal false/exit1 유지 |
| 본문/Secret 비저장 | fixture memory assertions/safe static errors/export whitelist | body/error검사, reviewer 코드/metadata 대조 | 실제 HTTP/PTY/SSH tuple 원문 비저장, IDs hashed, 보호된 원본 외부미전송 | 모든 운영 log sink audit는 제외 |
| local 실VM/production 구현 (AC1~3) | 이전 audit 전체 표 | 기존 local full/race/Web, 현재 full tests | 이전 local211.62s PASS; production source 동일 | 이전 local 증거를 수정된 recovery branch 증거로 쓰지 않음 |
| C3/Preview Control/Data 후속 | 이번 scope 제외 | 기존 Mock WSS 회귀 | 실제 전체 Preview/C3 미실행 | Jira 명시 후속, 완료 주장 없음 |

## Findings와 실제 실패

기존 F-001~F-R004 해결 상태는 이전 audit 그대로다. 수정 전에 사용자에게 추가 finding을 보고했다.

| ID·심각도/관측 위치 | 재현·영향·근거 | 수정·회귀·재검증 |
| --- | --- | --- |
| F-KT-001 Medium / live main.go:360~369, refs:166 | 실제 readonly recovery가 모두 DELETED로 기록한 뒤 recover-cleanup. `refs`가 삭제 항목을 빼 known=[]가 되어 `teardown inventory incomplete`, 복구 완료 확인 실패. 명시적 fresh exact-ID 검증 요구와 불일치. 추가 deletion 없음 | main recovery는 all-journal helper 사용. recovery.go:18, recovery_test.go:24. allAbsent/reappeared unit+race PASS, 독립 DELETED-filter mutant는 예상FAIL. 새 binary 실제 recovery에서10개 부재 최종확인 PASS |
| KT execution limitation / 기존 lifecycle.go:113 safe rejection | original final Cleanup FAILED/ERR_INFRA_OPENSTACK. 원인/실패한 구체 HTTP 상태는 safe output만으로 확정 불가. 새로운 forwarding 결함으로 단정하지 않음 | 원래실패 보존. fresh Reconcile 후 teardown3개 SUCCESS, 이후10개 부재2회/잔여0/7종baseline 확인. 원래 run을 PASS로 변경하지 않음. Provider code의 임의 retry/실패은폐 수정 없음 |

초기 alias/SSH 준비 실패와 초기 새 HTTP unit 기대값 Content-Length 오기도 README에 구분했다. 초기 unit 오기는 검증 코드 기대값만 수정했으며 실KT 전 두 tools suite가 PASS했다. 초기 runtime source 전체hash를 보관하지 않은 과거한계는 이전audit에 유지한다. 이번 original/final source는 모두 별도로 보존했다.

## 실패·보안·수명주기 재검토

- gate-before-journal가 mixed/단일세대/recovery smoke를 거절한다. recovery shell은 OP02/Preview를 둘 다0으로 덮어쓴다. 기존 journal이 있으면 fresh Control을 거절하며 archive/recreate로 실패를 숨기지 않았다.
- setup/HTTP/PTY/SFTP가 정지해도 3분 fixture 취소가 manager Close로 I/O를 해제한다. HTTP managed Conn은10초 deadline. fixture세션/manager는 Reset 전에 닫힌다. 독립 stall probe와 기존 EOF/late-peer race를 구분했다.
- 앱 host는127.0.0.1 고정,3000 공개정책 없음. 실제 private management NIC와 Server별 pin을 사용한다. credential이 없는 SaaS synthetic peer는 임시 secret/CA로 검증했으며 실제 SaaS나 DB 쓰기는 없다.
- Partial mutation 결과를 판정 전에 durable journal에 저장하고 UNKNOWN이면 blind replay하지 않는다. 현재 typed exact-ID 관측만 삭제 대상으로 선택한다. DELETED 캐시도 fresh query에서 제외하지 않도록 F-KT-001을 고쳤다. 소유권 검증/duplicate/candidate 거절은 유지한다.
- 원래 cleanup/shutdown 실패는 Passed/runErr에 반영한다. 원래 child 정상join PASS. recovery는 원래 full acceptance의 성공 flag를 올리지 않으며 원래 passed=false가 남는다. 최종 bootstrap VM 자체도 부재여서 host상의 fixture/키/프로세스 수명은 종료됐다.
- 원래 실패 안전오류는 세부 원인을 보존하지 않아 root cause 분류에 한계가 있다. 이를 일시적 오류라고 단정하거나 Mock 성공으로 대체하지 않았다. 소유자와 실제 잔여 확인은 모두 완료했다.
- 새 모듈/계약/배포 서비스/DB schema 없음. 로컬 Git만 수정하며 원격 push/PR/Jira/외부 문의는 하지 않았다. KT 임시 실행은 사용자 승인 범위였다.

## 실행·Evidence·최종 대조

최종 full Go41packages/3671tests PASS/16tests skip/0fail, vet/tools race PASS. 12package skip은 test 없는 package다. opt-in realCloud/Windows OS제약 skip 전체목록을 보존했다. local live/production7package race/Web 검증 source는 hash상 불변이다. 새 C2 Mock loopback은 최신main고정overlay PASS, 실 C2/E2E 성공이 아니다.

실KT는 real Provider API/VM/사설management SSH/HTTP/PTY/SFTP였고 synthetic SaaS Control이다. 양 세대 Forwarding PASS와 원래 Control fixtureFAIL을 원본로그/집계에 함께 기록했다. recover tool 수정 후 해당 branch와 unit/race/fullsuite를 다시 실행했으며 Forwarding 전체fixture는 재실행하지 않았다. 변경은 recovery3파일뿐이라 원래Forwarding source증거와 최종recovery source증거를 구분해 결합했다.

Cloud result는 metadata exporter가 원본10개Lab journal/8개bootstrap 자원과 baseline7종을 대조했다. 자원ID 집합 동등성은 보호된 원본으로 검사하고 공개metadata는 hash+count를 제공한다. 최종 residual0, baseline3sharedTier만 남음. 원본로그/keys/clouds는 보호된로컬경로에 유지하며 본문/자격증명을 Evidence에 복사하지 않았다. 재현은 README와 checked-in fixture source/capture/export/overlay scripts로 기록했다.

독립 reviewer `/root/review_lbt173`는 기존 production전체diff와 새 tools 전체diff를 검토하고, unit/gate/stall/fault/mutant/sourcehash를 독립실행했다. cloud API는 직접 호출하지 않았으므로 최종metadata/로그를 재대조한 독립문서검토와 root의 실제KT실행을 구분한다. [최종 독립 검토](independent-review/README.md), [보호된 원본 대조](independent-review/final-live-check.json)는 원본/복구10개 typed ID metadata, 실제 binary8개digest, lifecycle31줄,7종exact baseline/잔여counter에 불일치0을 확인했다. Go 원본15,766event 독립집계도 summary와 일치했다. 추가 confirmed source defect는 없고 F-KT-001 해결을 확인했다.

## 판정 경계

**PR 가능: forwarding 구현과 KT standalone component smoke 변경 범위.** 기존 구현 traceability와 양 세대 실제 forwarding/공유·격리/대상 차단, 복구수정 자동·실KT검증, 최종정리/독립검토를 충족했다. 사용자 요청의 KT 검증 실행과 자원 정리는 마쳤다. 원격 PR을 생성했다는 뜻이 아니다.

**전체 KT Control fixture 일괄 PASS는 미확보.** 최초 Cleanup 거절은 안전한 오류만 남아 원인미확인이며 후속 Provider lifecycle 분석/깨끗한 전체 재실행의 위험 항목이다. 이 실행을 Jira 전체 완료/운영 rollout 승인으로 자동확대하지 않는다. 실패를 숨기지 않고 standalone 결과와 복구필요를 함께 기록했다. 실제 SaaS/Browser/C3 및 Preview Control/Data 전체후속은 이번 Jira범위 밖이다. 사용자 지시에 따라 local branch만 유지하고 push/PR/merge/배포서비스/Jira/외부메시지 전송은 하지 않았다. audit 통과를 외부write 승인으로 해석하지 않는다.
