# LBT-173 fresh full fixture audit

2026-10-16 Asia/Seoul. 사용자 지정 PRE_MERGE_AUDIT.md 전체를 다시 읽고 [로컬 전체 감사](../../AUDIT.md), [최초 KT 감사](../AUDIT.md)에 이 변경/새 실행을 추가 대조했다. **판정: 사용자 요청한 새 KT fixture 일괄 PASS 및 LBT-173 범위의 구현·검증 완료. 로컬 변경은 검토 가능한 상태다.** 아래 scope/skip/후속 경계를 함께 적용하며 이전 실패를 이 실행의 성공으로 바꾸지 않는다. 원격 PR은 생성하지 않았다.

## 대상과 전체 diff

로컬 브랜치 `codex/feat/LBT-173-ssh-tcp-forwarding`, base/HEAD `88097be5d9bb6996b718fd185515ca324629fda6`. staged 없음/미커밋 working tree다. [source-manifest](source-manifest.json) 543개 파일, [source-diff](source-diff.patch), [전체 변경 목록](change-inventory.txt)을 고정했다. 기존 전체 production/fixture/consumer/설정/Evidence 검토를 유지하고, 이전 최종 KT source 이후 다음 변경을 추가 검토했다.

| 파일/범위 | 목적과 영향 |
| --- | --- |
| internal/connector/provider/openstack/ktcloud_d1_delete.go:16–52 | deleting root의 중복 DELETE 방지, 400/409 거절 뒤 같은 ID 재관측. Resource scope/정렬/마지막 actual absence 확인은 기존 호출자가 소유 |
| internal/connector/provider/openstack/ktcloud_d1_volume_deleting_test.go | 실제 Cleanup 외부 결과와 mutation 횟수 검증. 이미 deleting/비수렴, 거절 뒤 absent/deleting/stable/lookup 오류/잘못된 ID/attachment,403/500 경계 |
| full-fixture/** 및 상위 Evidence 안내 | 새 실행과 과거 실패 분리, source/build/test/live identity, 초기 실패 보존, metadata/assertion/재현/독립 검토 |

Forwarding production source/runtime/contracts/web는 이전 최종 KT 검증과 동일하다. 변경 없는 HTTP/DB/Browser 계약, 새 dependency/migration은 없다. Volume 함수는 공통 Volume cleanup dispatch에서도 사용되므로 기존 local OpenStack의 삭제/UNKNOWN/거절 회귀와 전체 Go/race를 함께 실행했다. 이전 로컬 실VM HTTP/SSH 증거는 변경 없는 forwarding 경로의 근거이며, 새 volume 분기 실행 증거로 확대하지 않는다.

최신 원격 main을 읽기 전용 fetch/대조했다. `8f88833ec433c2de19ea91375dec57ae95a2baea`는 이전 `6755fc1` 이후 웹 header 3파일 17추가/2삭제다. 원문 diff를 읽었으며 Connector/계약/현재 feature와 충돌하지 않는다. 앞선 main의 새 C2 test는 고정된 외부 Go overlay로 현재 feature에 재실행 PASS했다. 실제 main 전체 병합이나 원격 CI 통과를 주장하지 않는다. 현재 branch base를 유지한다.

ignored protected run은 cloud credential/private key/host pins와 원본 typed IDs/log를 보유한다. 실행 의존성/보호 경계를 확인하되 파일 원문은 Evidence에 복사하지 않았다. 모든 ignored 파일/third-party 라이브러리/운영 SaaS log sink 전체 감사는 범위 밖이다.

## 최신 원문과 추적성

[원문 재확인 metadata](ssot-check.json): LBT-173 updated `2026-10-16T09:23:53.415+0900`, 댓글0. D-18 v5, 검증 설계 v3, 이번 Cleanup에 직접 적용되는 D-20 v7을 다시 읽었다. AGENTS/CONTRIBUTING/TESTING 및 Connector §7b/Preview schema/Runtime을 전체 변경과 대조했다. 이전 확정 wire/prep 자료는 역사적 배경이며 현재 owning Git 계약을 덮어쓰지 않는다.

| 요구·AC/SSOT | 구현 | 실행한 자동 검증 | 실제 Evidence와 한계 | 판정 |
| --- | --- | --- | --- | --- |
| LBT-173 AC1 로컬 actual SSH HTTP | vmaccess/tcp.go, preview/forwarder.go, 기존 Provider resolver | 전체 suite/기존 TCP fault/race | 기존 local211.62s/HTTP2회/Reset PASS. forwarding source LF 동일 | 충족. 새 volume branch의 local live 증거는 아님 |
| AC2 현 Lab/generation/VM/관리망/pin | manager→SSHForwarder→VMAccess→terminal_target | 기존 target/NIC/pin/offline/timeout tests | 새 KT 양 세대 target guard, 실제 Reset old absent/new present/stale 거절 PASS. actual pin 장애 주입은 안 함 | 충족. 장애 주입은 in-process |
| 정확한 port/22차단/Backend 정책 유지 (§7b) | tcp.go port gate/127.0.0.1, SSHForwarder | wire destination/unsafe port/refused/error sanitization | 새 KT3000 HTTP200/exact body 총4회,3001 refused/범위·22 거절 PASS | 충족 |
| AC3 공유 SSH와 독립 PTY/SFTP/TCP (D18) | 공유 acquire, managed TCP/slot/Close | 9package race 및 기존 close/late/EOF/fault | acceptance 한 manager의 실제 SSH tuple 메모리 대조/PTY/SFTP 양 세대 PASS. child Preview Data WSS는 별도 | 충족: standalone 경계 |
| Production Forwarder/capability | app.go same Access 주입, generic fallback 제거 | injection/fallback/capability tests | 실제 production child HELLO preview-v1 요구 PASS. child의 Preview Data는 실행 안 함 | 충족: 선언/주입과 Data 실환경을 구분 |
| AC4 KT standalone와 환경 차이 | tools Preview opt-in + bootstrap 배포 | mode gate/HTTP exact status/body/취소 tests | 실제 KT D1 VM/management SSH/forwarding PASS, synthetic SaaS Control. Ubuntu24.04/2x4.itl/50GiB | 충족. local↔KT topology/OS·API 차이는 각 runbook |
| 사용자 요청: fixture 일괄 PASS | Control Provision→Reset→empty Cleanup→Cleanup→all IDs Reconcile→graceful exit, bootstrap final | 기존 owned-ID/candidate/unknown/recovery tests | 새 journal true/Control exit0/10Lab IDs DELETED/child5s/8bootstrap absence/7inventory equality 모두 PASS.11개 phase exit0 | 충족: 새 단일 실행 |
| D20 결과불명 재관측/무분별 mutation 금지 | deleteKTCloudVolume:25,30–47 + deleteAndConfirm | 새34case, 독립18case/semantic mutant, 기존 cleanup tests | 새 실제 lifecycle 전부 PASS. 새실행 중 400/409 분기 발생 여부 및 원래 실패 원인은 safe log만으로 미확인 | 자동·독립 분기 충족, 실제 전체 수명주기 PASS |
| Secret/본문 비저장 | safe error, memory assertions, metadata allowlist/hash | 기존 안전오류 tests/독립 malformed lookup probe | protected 원본과 공개metadata 독립 대조/알려진 ID·주소·키 비노출 PASS | 충족: 실행·Evidence 경계. 운영 sink 전체 감사 아님 |
| 실제 SaaS/Browser/C3/Preview WSS 전체 | Jira 명시 후속 | 기존 Mock WSS 회귀 | 이 fixture는 실제 SaaS/Browser나 Preview Data 전체 E2E가 아님 | 범위 밖. 미실행을 숨기지 않음 |

## 수정 전 findings 및 해결

F-R001~004 및 F-KT001 해결은 이전 감사/회귀에 유지한다. 다음 두 finding을 사용자에게 수정 전에 보고했다. 최초 KT 실패의 root volume는 확인됐지만 당시 HTTP 상태/volume 상태가 없어 이 두 경합 중 하나였다고 확정하지 않는다.

| ID·심각도/관측 revision 위치 | 재현·관찰·영향/근거 | 수정과 회귀 |
| --- | --- | --- |
| F-KT-002 Medium / 최초 live와 LF동일한 base88097be volume Delete:23 | owned detached root GET=deleting→다시DELETE409→Cleanup FAILED. convergence 중복 mutation으로 정상 cleanup을 실패시킴. D20 actual-state reconciliation/TESTING failure 기준 | 이미deleting이면 mutation0, 기존 caller가 exact absence까지 poll. 404수렴 SUCCEEDED/계속deleting UNKNOWN. 수정 전 두 regression FAIL 원본 보존 |
| F-KT-003 Medium / F002-only revision Delete:30 | 첫GET available→Nova 자동삭제 시작/완료→DELETE400/409→다음GET404. 기존은 재관측 없이 FAILED 확정. 독립 scripted peer 재현이며 최초 live root cause 확정 아님 | 400/409만 같은ID GET1회 추가, absent/deleting이면 existing absence poll. stable이면 원래FAILED, lookup/malformed/wrongID/attached는UNKNOWN.403/5xx/timeout 재삭제 없음. 모든경우DELETE≤1 |

34개 회귀 subcase(부모 포함36개 신규 test 결과), 독립18case, 원래 source overlay와 guard-only semantic mutant의 예상 FAIL이 결함 검출성을 확인했다. 독립 source는 원래 live manifest의 LF hash와 일치한다. 실제 과거실행 재현/현재 scripted 재현/semantic mutant를 구분한다. 초기 compile 실패(ResponseCodeIs 인자 수)도 보존했으며 실KT 빌드 전 수정/전체 suite PASS했다.

## 실패·보안·수명주기

- ID·generation·프로파일 검증과 cleanup 정렬은 기존대로다. 이름/tag candidate를 삭제 소유권으로 채택하지 않는다. Volume GET의 ID/status/attachment를 mutation 전에 검증하며 거절 뒤 관측도 동일 검증을 거친다.
- deleting은 부재가 아니다. 함수가 mutation 없이 반환해도 deleteAndConfirm이 실제 exact GET 부재까지 기다린다. timeout/조회불명은UNKNOWN이고 다음 자원 삭제/새 generation 생성으로 넘어가지 않는다.
- 400/409 후 추가 DELETE를 보내지 않는다. 실제 같은ID가 absent이면 확인완료, deleting이면 기다리고 stable이면 원래 거절을 유지한다.403/500/429 및 network timeout의 blind retry를 도입하지 않았다. 원본 Provider error는 결과에 노출하지 않는다.
- Context/ActiveTimeout은 기존 caller가 소유한다. 새로운 worker/socket/timer나 durable 상태를 추가하지 않았다. 취소되면 기존 UNKNOWN 경계로 돌아간다. 프로세스 재시작 후에는 기존 durable ProviderResource/Reconcile이 소유하며 캐시로 absent를 추정하지 않는다.
- managed TCP/PTY/SFTP/runtime source는 바뀌지 않았다. setup worker1/actual channel16의 bounded ownership, local Close 해제, remote CLOSE까지 slot 유지/형제 채널 격리는 이전 독립 fault+이번 race에 포함된다.
- 실제 app은 loopback3000에만 bind하며 public app port 정책을 만들지 않는다. 관리망 SSH pin/TLS 검증을 유지한다. HTTP/PTY/SSH tuple 원문은 메모리 assertion만 사용한다.
- HTTP/DB/Backend 권한/Browser 허용목록 정책을 변경하지 않았다. remote push/PR/merge/Jira/외부 메시지는 수행하지 않는다. KT 임시 생성/배포/정리는 사용자 승인된 fixture 실행 범위다.

## 테스트와 실행 소스

전체 Go **41packages/3707tests(subtests 포함) PASS,16test skip,0fail**. test 없는12package skip은 별도다. [skip 목록](go-test-summary.json)에 opt-in 실환경 및 Windows symlink/open-file rename/POSIX permission 제약을 보존했다. 실제 KT는 별도 실행이므로 일반 `go test`의 opt-in skip을 실환경 성공으로 해석하지 않는다. PostgreSQL build-tag integration은 DB 변경이 없고 로컬 DB/Docker가 없어 미실행이다.

전체 vet/build, Provider/VMAccess/Preview/App/Terminal/Filetransport/WSS/양 tools **9package race PASS**, LF 기준 Go474파일 gofmt 차이0. inherited rawCRLF455개는 무관한 수정 없이 유지했다. Web source/lockfile은 이전 local 검증과 LF동일하므로 기존 install/typecheck/lint/14files188tests/build 증거를 재사용한다. 이번 volume 변경을 Web 재검증 사유로 확대하지 않았다. 변경 없는 OpenAPI/JSONSchema 원격 validator/CI 통과는 주장하지 않는다.

source543개와 build 전후482개(제품/테스트 Go+module476개, 역사적 docs Gofixture6개)의 identity를 각각 직접 검증해 차이0이다. 초기 helper가 docs6개를 일반source 차이로 세어 실패한 결과도 보존했다. 수정된 helper는 해당6개 원본도 별도 hash 대조하며 차이를 숨기지 않는다. live binary4개/배포3개 digest와 최종 source alignment도 일치한다. 원본 Go JSON과 gzip byte 동등성을 확인한 뒤 gzip 원본을 보존했다.

## 실환경·독립 검토·최종 판정

실제 새 실행은11:37:43–11:52:04 KST, Control은11:40:13–11:50:44다. [11개 단계](phase-results.json) 전부 exit0, [journal](control-ledger.metadata.json) passed=true, unique Lab10개 전부DELETED, 정상 child exit5초/forcedKill=false다. Bootstrap8개 정리와 [잔여10종 counter0](final-residual.json), [7종 exact baseline](final-inventory.metadata.json)을 확인했다. Tier는 공유3개만 유지하고 다른6종 inventory는 모두0이다. 재시도/recovery 없이 새 lifecycle 한 번에서 얻은 결과다.

구현을 쓰지 않은 `/root/review_lbt173`가 원래 전체 diff와 새 deletion/회귀/Evidence를 분리 검토했다. F-KT003은 reviewer가 독립 재현/보고했다. 현재 source probe18case와 focused race PASS, 원래source/semantic mutant FAIL, source/build/hash/Go원본 집계 불일치0을 확인했다. Reviewer는 cloud mutation을 수행하지 않았다. 실제 실행은 root, protected 원본/metadata 대조는 별도 reviewer가 담당했다. [최종 독립 원본 대조](independent-review/final-live-check.json)의 failedChecks는 비어 있다. Journal/typed10개/11phase/28개 lifecycle줄/8binary copy/3deployed digest/10잔여counter/7inventory가 모두 일치하고, 실제 ID/주소/개인키 비노출 검사도 통과했다. 원래 실패 journal false와 이전 자원 집합이 새 실행과 분리된 것도 확인했다.

**완료:** 사용자 요청한 새 KT fixture의 전체 일괄 PASS, 자원 정리와 증거·감사 기록. LBT-173의 로컬 실제 forwarding, target/port/timeout/Terminal·File 회귀, KT standalone 결과·환경 차이 요구를 범위 내 충족했다. 알려진 수정 전 findings는 해결·재검증됐고 현재 검토 범위의 추가 확정 결함은 없다. 로컬 변경은 PR 검토 가능한 상태이며 생성/push 승인은 아니다.

**한계·후속:** 원래 KT root-volume rejection의 정확한 원인과 새실행의 거절 분기 발생 여부는 미확인이다. scripted HTTP 재현과 실제 성공을 결합해 원인을 확정하지 않는다. 실제 SaaS/Browser C3 및 Preview Control/Data 전체 lifecycle은 Jira 명시 후속이며 이 실행의 완료 대상이 아니다. Windows/opt-in skip과 DB/원격CI 미실행을 유지한다. 기존 live forwarding source는 동일하며 새 volume 분기의 local real-cloud 재실행은 하지 않았다. 비협조 SSH peer/운영 log sink/전체 부하 검증은 이전 감사의 한계를 유지한다.

마지막 독립 문서 검토에서 Low 추적성 오류1개(AUDIT.md의 최초 Delete 줄번호26)를 수정 전에 보고하고 pinned base의 실제23번 줄로 정정했다. 동작/테스트 소스 변경이 없어 추가 실환경 재실행은 필요하지 않다. 원래 source hash와 기존 실패 재현은 그대로다.

사용자 지시에 따라 local branch만 유지하며 push/PR/merge/Jira/외부 메시지 전송은 하지 않았다. Audit 통과를 그 행위의 승인으로 해석하지 않는다.
