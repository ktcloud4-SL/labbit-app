# LBT-173 fresh full fixture 독립 검토

2026-10-16 KST. 구현 및 cloud 실행과 분리된 reviewer가 작성했다. Reviewer는 cloud API를 호출하거나 저장소 소스·테스트를 수정하지 않았다. 이 디렉터리에 기록한 외부 Go overlay만 실행했다. 사용자 `PRE_MERGE_AUDIT.md`를 읽고 적용했다.

**독립 최종 판정: 제안된 LBT-173 구현·standalone 검증 변경 범위는 PR 가능하며, 사용자가 요청한 새 full fixture 단일 실행 PASS를 원본 대조로 확인했다.** 미해결 confirmed finding은 없다. 이전 실패 실행과 새 실행을 합쳐 성공으로 만들지 않았다. 실제 SaaS/Browser 후속 완료나 운영 rollout 승인은 별도다. Push·PR 생성·Jira 변경도 수행하지 않았다.

## 고정 대상과 변경 경계

- 저장소: `tmp/labbit-connector-gates-20261015`, branch `codex/feat/LBT-173-ssh-tcp-forwarding`.
- base = HEAD = `88097be5d9bb6996b718fd185515ca324629fda6`, 미커밋 로컬 변경. Push·PR·merge 수행 없음.
- 전체 현재 코드/설정/도구 변경은 tracked 13개와 untracked Go 8개다. 이전 전체 diff 검토는 [KT 독립 검토](../../independent-review/README.md)와 [기존 KT audit](../../AUDIT.md)에 기록했다. 이번 추가 변경은 `internal/connector/provider/openstack/ktcloud_d1_delete.go`와 `ktcloud_d1_volume_deleting_test.go` 두 파일이다. 이전 forwarding·runtime·contract·web 소스는 동일하다.
- [source-alignment.json](source-alignment.json): source manifest 543개 raw/LF hash가 현재 파일과 모두 일치한다. Build 전후 manifest 482개도 서로 및 현재 파일과 일치한다. 그중 기존 LBT-147 evidence의 Go fixture 6개는 일반 source manifest의 docs 제외 규칙 때문에 추가로 들어 있으며 직접 hash를 확인했다.
- [binary-alignment.json](binary-alignment.json): 보호된 새 실행 폴더의 4종 바이너리 각각 2개 copy가 [binary manifest](../binary-manifest.json)와 일치한다. [final-live-check.json](final-live-check.json)은 실제 배포된 Linux 3종의 digest도 해당 manifest와 일치함을 확인한다. [post-live-source-check.json](post-live-source-check.json)은 종료 후 source 543개의 차이가 0임을 확인한다.

## 최신 요구사항·SSOT와 적용

Jira [LBT-173](https://samsunglions.atlassian.net/browse/LBT-173)의 본문·AC·댓글을 직접 확인했다. Updated `2026-10-16T09:23:53.415+0900`, 댓글 0개이며 원래 forwarding AC는 유지된다. Confluence D18 `7241730` v5는 기존 전체 검토에 포함됐다. 이번 cleanup 검토에서는 [D20](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283) v7과 [테스트 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) v3를 직접 다시 읽었다.

| 요구사항/결정 | 구현·소비자 | 자동 검증 | 실환경 범위·판정 |
| --- | --- | --- | --- |
| Preview 4필드 target, exact port 및 Workspace/generation/VM/Host pin 검증, shared SSH의 독립 TCP/PTY/SFTP | 기존 `preview/forwarder.go`, `preview/manager.go`, `vmaccess/access.go`, `vmaccess/tcp.go`, `app/app.go`; 전체 diff는 이전 독립 audit 참조 | 기존 forwarding/취소/비협조 SSH fault 회귀 그대로, 이번 source와 해당 경로 hash 동일 | 새 KT one-run에서 g1/g2 각각 HTTP exact body 2회, 동일 SSH, PTY/SFTP 생존, wrong target·unsafe/refused port·stale g1 거절 PASS. Pin mismatch 장애 주입은 자동 fixture만 |
| Connector README §10–11 exact tracked cleanup, server→root volume→policy→tier, actual absence 확인 | `server.go:102`의 `DeleteOnTermination:true`, `ktcloud_d1_delete.go:15`, `lifecycle.go:98,125` | 초기 deleting/DELETE rejection 후 exact fresh GET/관측 종료/UNKNOWN 회귀 34개 + 독립 overlay 18개 | 새 KT Reset/Cleanup 성공, exact known 10개 최종 부재, bootstrap 8개 정리, 잔여 0, 7종 baseline 보존. 기존 원본 실패 원인은 확정 불가 |
| D20 실제 Provider 상태 우선, UNKNOWN blind mutation 재실행 금지, 잔여 확인 전 새 Provision 금지 | DELETE는 최대 1회, 400/409만 exact GET 1회 추가; deleting/404만 기존 caller poll로 복귀; 다른 실패 유지 | 400/409 absent/deleting/stable/lookup failure/wrong ID/attached/empty status, 403/500 원래 분기; 독립 probe는 429도 포함 | 이번 수정은 재조회와 기존 polling의 연결이며 자동 DELETE 재시도 정책을 추가하지 않음 |
| 테스트 전략의 Unit/실 Provider/Browser E2E 분리, 실패 기록 보존 | standalone Control fixture 및 실제 production Connector, opt-in Preview helper | Full ordinary Go suite와 overlay/mock 경계를 별도로 기록 | 요청한 standalone Provision→Preview g1→Reset→Preview g2→Cleanup→정확 부재→child exit가 새 한 실행에서 PASS. 실제 Provision/Reset/Cleanup Control WSS를 사용하며 Preview helper는 같은 host의 공통 production library를 호출한다. SaaS Preview Control/Data WSS 및 Browser C3는 후속 범위 |

새 cleanup 분기는 HTTP/WSS payload, runtime 설정, DB migration, frontend를 바꾸지 않는다. 해당 계약 원문은 앞선 전체 변경 audit에서 적용 범위를 확인했으며 이번 두 파일의 동작과 충돌하지 않는다.

## Findings와 재현 근거

| ID·심각도 | 관측 revision·파일/라인 | 재현·영향 | 수정·회귀 상태 |
| --- | --- | --- | --- |
| F-KT-002 / Medium | 최초 live와 동일한 base `ktcloud_d1_delete.go:23` (함수 15–24) | 서버의 delete-on-termination root가 이미 `deleting`인데 DELETE를 다시 보내 400/409를 받으면, 실제 부재로 수렴할 삭제를 FAILED로 판정한다. 끝까지 deleting이면 UNKNOWN과 구분도 어긋난다. 로컬 scripted HTTP로 확인 | 구현자가 finding 보고 후 initial detached `deleting`을 기존 exact absence poll로 넘기는 guard를 추가. 원본 overlay FAIL, Current PASS |
| F-KT-003 / Medium | initial deleting guard만 추가한 중간 revision `ktcloud_d1_delete.go:30` | initial GET available→Nova 자동 삭제 시작/완료→manual DELETE 400/409에서 즉시 FAILED. guard만으로 TOCTOU 경계를 닫지 못한다. 관측 가능한 absent/deleting으로 수렴해도 full fixture를 실패시킴. 수정 전에 parent에게 보고 | 현재 DELETE 400/409 뒤 exact GET 1회, absent/deleting만 기존 poll로 복귀. stable은 원래 rejection, malformed/attached/lookup 실패는 UNKNOWN. 새 DELETE 없음. Current 18개 PASS, guard-only semantic mutant FAIL |
| D-R-001 / Low | `full-fixture/AUDIT.md:44` 수정 전 `최초 Delete:26` 표기 | Pinned base 26번은 IsEmpty이며 실제 DELETE는 23번이다. 동작 영향 없이 finding 추적 위치가 어긋남. 수정 전에 parent에게 보고 | 사용자에게 선보고 후 23번으로 정정. git show 원문·최종 audit 재대조 완료. 소스 불변이므로 추가 실환경 테스트 불필요 |

F-KT-002/003은 재현된 코드 결함이다. **이 결함이 이전 실제 KT rejection의 원인이라는 증거는 없다.** 이전 원본 journal에서 마지막 실패는 generation 2의 `VOLUME / workspace:root-volume / DELETE_FAILED`였고, 그 뒤 deferred teardown이 남은 3개 자원을 정리했다. 당시 HTTP status/body 및 해당 순간 volume 상태가 없으므로 원본 원인은 unknown으로 유지한다. [Cinder 공식 DELETE 조건](https://docs.openstack.org/api-ref/block-storage/v3/)도 deleting 상태와 일반 삭제를 구분하므로 자동 삭제와의 경쟁은 타당한 시나리오지만, 이는 원본 cloud 원인에 대한 추론이다.

`cleanupResources`는 첫 실패에서 중단하고 volume은 policy/tier보다 앞서 정리된다. NSM `deletePolicy`/`deleteTier`의 exact ID·완전 inventory 부재 확인을 검토했으며 원본 실패가 이 경로 때문이라는 근거는 없다. 무조건 재삭제·force-delete·삭제 순서 변경을 제안하지 않는다. 이전 recovery가 all-DELETED journal을 누락하던 F-KT-001은 이미 수정·별도 검증됐고, 새 one-run PASS에 복구 실행 결과를 섞지 않는다.

## 독립 실행과 재현

- [provider-cleanup-race.log](provider-cleanup-race.log): 새 34개 regression 및 기존 root/NSM/server 삭제 경계를 실제 실행, `-race`, PASS 4.690s. In-process HTTP fixture이며 cloud 실행이 아니다.
- [volume-probe-current.log](volume-probe-current.log): 외부 scripted HTTP overlay 18개 PASS. 초기 absent/available/deleting, timeout, 400/409 후 fresh observation, 403/500/429, wrong ID/attachment/malformed 분기. 실제 Adapter Cleanup의 outcome·GET·DELETE 수를 확인한다.
- [volume-probe-original.log](volume-probe-original.log): pinned base의 volume 파일만 overlay한 비교 실행은 예상대로 FAIL. [original-source-check.json](original-source-check.json)은 해당 파일 LF hash가 이전 KT live manifest와 일치함을 확인한다. 다른 파일은 현재 feature 소스이므로 과거 전체 소스를 다시 실행했다고 주장하지 않는다.
- [volume-probe-deletingguardonly.log](volume-probe-deletingguardonly.log): initial deleting guard만 남긴 명시적 semantic mutant도 예상대로 FAIL. 실제 중간 전체 snapshot이 아니다. 최초 동시 관측 로그 [volume-convergence-deleting-guard.log](volume-convergence-deleting-guard.log)도 보존했다.
- 재현: PowerShell에서 `./reproduce-volume.ps1 -Mode Current` 또는 `-Mode Original`, `-Mode DeletingGuardOnly`. 원본 두 모드는 exit 1이 기대값이다. 외부 임시 파일과 Go overlay를 사용하고 저장소 소스와 cloud를 변경하지 않는다. Go `1.27.1 windows/amd64`, `github.com/gophercloud/gophercloud/v2 v2.15.0`, `golang.org/x/crypto v0.57.0`; 루트 go.mod/go.sum 및 binary manifest로 고정된다.
- [full-go-result-check.json](full-go-result-check.json): 구현자가 실행한 gzip 원본 15,910 events를 독립 집계했다. 41 package PASS, 3,707 test/subtest PASS, 16 test/subtest skip. 12 package skip은 모두 no-test-files다. Opt-in cloud tests와 Windows POSIX/symlink 조건 skip은 [원본 summary](../go-test-summary.json)에 열거돼 있다. 실제 local/KT 검증은 일반 Go PASS와 분리한다.

18개 외부 probe와 source 검토에서 현재 수정의 추가 확정 결함은 확인되지 않았다. Full suite는 원본 로그를 검증했으며 reviewer가 다시 전체 실행한 것은 아니다. Parent의 9-package race 원본도 9개 package PASS를 확인했다. Vet/build는 [verification-results.json](../verification-results.json)에 parent가 기록한 실제 exec exit 0을 근거로 하며, 빈 로그만으로 exit code를 독립 확인했다고 주장하지 않는다. 최신 main `8f88833ec433c2de19ea91375dec57ae95a2baea`와 직전 확인 main `6755fc1`의 diff는 frontend header 관련 3파일(+17/−2)임을 직접 확인했으며 이번 Provider/Connector 계약에 영향이 없다. Feature branch를 rebase하지 않았다.

## 새 full fixture 실환경 결과

[verify-live.ps1](verify-live.ps1)을 보호된 새 실행 디렉터리에 대해 직접 실행했다. 이 helper는 cloud API를 호출하지 않으며 공개 metadata와 보호 원본을 읽고 이 reviewer 폴더의 JSON만 쓴다. 재현은 `./verify-live.ps1 -PrivateDirectory <protected-directory>`이며 원본 디렉터리는 공개하지 않는다. [final-live-check.json](final-live-check.json)의 `failedChecks`는 빈 배열이며 exit 0이다.

- 2026-10-16 KST `11:37:43` inventory부터 `11:52:04` final inventory까지 **11개 순서대로 phase가 모두 exit 0**이다. Recovery phase, 실패한 cleanup의 후속 실행을 합산하지 않았다.
- 새 Control journal은 `passed=true`, HELLO/heartbeat 확인, 두 세대 각 5개, 최종 `PREVIEW_PRODUCTION_CONNECTOR_GRACEFUL_SHUTDOWN_JOINED_PASS`다. 고유 typed Lab ID 10개(TIER 4, policy 2, server 2, volume 2)가 모두 DELETED다. 실제 source의 최종 exact known Reconcile은 10개 전체를 `KNOWN_RESOURCE / ABSENT`로 검증해야 이 stage로 진행한다.
- 실제 Provision/Reset/Cleanup Control WSS와 실제 VM SSH·loopback HTTP를 실행했다. HTTP200/exact-body는 세대별 2회, shared SSH 및 Preview close 뒤 PTY/SFTP 생존, wrong target/port/refused/stale guard를 확인했다. Production child는 SIGTERM 뒤 5초 이내 정상 exit, forcedKill=false다.
- 보호 원본과 [live-lifecycle.log](../live-lifecycle.log)의 28개 allowlisted 줄, ledger의 각 typed ID hash·generation·logicalName·state, phase 목록을 직접 대조해 동일했다. [deployment-digests.log](../deployment-digests.log)의 Linux 3종 remote digest는 build manifest와 일치하며 로컬 binary 8개 copy도 동일하다.
- Bootstrap 원본 ledger의 server 1, root volume 1, keypair 1, policy 3, public IP 1, forward 1 = 8개는 `BOOTSTRAP_CLEANUP_CONFIRMED / deleted=true`다. 추가된 [bootstrap metadata](../bootstrap-ledger.metadata.json)의 8개 hash/type/count/deleted도 원본과 직접 대조해 일치했다. 최종 잔여 10종 counter는 모두 0이다.
- 최종 7종 inventory는 actual ID set이 시작 baseline과 정확히 같고, 기존 shared TIER 3개만 남으며 나머지 6종은 0이다. 단순 개수 비교로 보존을 주장하지 않는다. 공개된 inventory hash도 해당 원본 ID set으로 재계산해 일치했다.
- 공개 metadata의 실제 Lab/bootstrap/shared resource ID 및 Lab ID·IP·private-key 표식 비노출을 확인했다. 인증정보·SSH host pin·HTTP/PTY 내용은 내보내지 않았다. Reviewer가 raw credential 파일 전체를 별도 보안 감사했다는 뜻은 아니다.
- [original-failure-preservation-check.json](original-failure-preservation-check.json): 이전 원본 `passed=false`, g2 root volume DELETE_FAILED, 원래 Control exit 1은 유지된다. 새 Lab hash는 다르고 새 Provider ID hash 집합도 이전과 겹치지 않는다. 새 PASS가 과거 실패 원인 진단이나 실패 로그 변경을 뜻하지 않는다.

400/409 자동 삭제 경쟁과 timeout은 scripted HTTP 회귀로 검증했다. 새 KT 실제 실행의 성공만으로 그 경쟁 분기가 실제 cloud에서 발생했다고 주장하지 않는다. 비협조 SSH 및 host-key mismatch 장애도 in-process fixture 검증이며 KT 장애 주입을 수행하지 않았다. 실제 SaaS/Browser 전체 경로, Preview Control/Data WSS, PostgreSQL build-tag integration은 이 standalone fixture 범위 밖이다.

최종 [README](../README.md)·[AUDIT](../AUDIT.md)·[RUNBOOK](../RUNBOOK.md) 및 상위 역사적 4개 문서의 최신 링크·당시 판정 보존을 읽었다. 문서가 원본/새 실행/회귀/skip/미실행 경계를 유지하고 위 결과와 일치함을 확인했다. [문서 링크 검사](document-links-check.json)에서도 깨진 상대 링크는 없다. 기존 F-R001–004/F-KT001과 이번 F-KT002/003은 해결·재검증, 마지막 문서 오류도 정정됐다. 확인된 결함을 남겨 둔 상태에서 PR 가능으로 판정하지 않았다. 이 판정은 로컬 검토 결과이며 원격 작업 승인이 아니다.
