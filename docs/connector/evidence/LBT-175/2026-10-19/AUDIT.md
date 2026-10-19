# LBT-175 pre-merge audit · 2026-10-19

## 판정과 고정 기준

**PR 가능 — 제안할 tools/fixture/Evidence 및 Terminal 테스트 보강 범위의 로컬 검증,
KT Cloud 실환경 검증과 독립 리뷰를 통과했다.** push·PR 생성은 사용자 지시에 따라
보류한다. 신규 원격 CI/PR 리뷰/승인/병합은 아직 실행하지 않았으므로
**LBT-175 전체 Jira 완료는 보류**하며 LBT-24/C3 완료로 확대하지 않는다.

- 기준 base=HEAD=`89a698dac50a8705ed305a5eccf94ee9505b033f` (PR #114 merge).
- 브랜치 `codex/feat/LBT-175-ktcloud-preview-validation`, 로컬 미커밋 변경 포함.
- `tested-source.json`: KT 실행 시점의 LF 정규화 파일 hash와 aggregate.
- `final-source.json`: 최종 검사 소스. KT 실행 후 바뀐 Go 파일은 Terminal **테스트 한 개**다.
  재빌드한 네 바이너리 SHA256이 배포한 네 바이너리와 모두 같음을
  `source-consistency.json`에 확인한다. production/contract/runtime 변경은 없다.
- 개인 `PRE_MERGE_AUDIT.md`를 읽고 A–F를 적용했다. Jira/Confluence 직접 조회,
  Git 지침·계약 검토와 별도 reviewer의 읽기 전용 검토를 수행한다.

## A. 전체 변경 검토

`change-manifest.json`에 staged/unstaged/untracked 전체 목록과 파일 hash를 남긴다.
Evidence 자체의 manifest·audit가 스스로의 hash에 순환 의존하지 않도록 최종 문서의
변경 목록만 별도로 기록한다. 새 Go 파일을 `git diff`만 보고 누락하지 않는다.

| 파일/그룹 | 목적·영향받는 경계 |
| --- | --- |
| `tools/ktcloud-d1-acceptance/main.go` | exclusive two-generation Preview mode, 실제 child 프로세스 Control/VM lifecycle에 새 transport fixture 배선 |
| `tools/ktcloud-d1-acceptance/preview_transport.go` | 실제 VM 앱 설치·독립 TCP 관측, Data WSS HTTP·guard·Reset/Cleanup·secrecy 판정 |
| `tools/ktcloud-d1-acceptance/preview_transport_peer.go` | TLS Mock Gateway의 credential/ATTACH correlation, per-session 단일 active tunnel, bounded queue, joined shutdown |
| `tools/ktcloud-d1-acceptance/preview_transport_test.go` | 잘못된 binding/auth, late attach, 관측 overflow, 정확한 HTTP/result, delayed TCP publication, mode/recovery 분기 |
| `tools/ktcloud-d1-acceptance/README.md` | 새 mode와 실제/Mock 경계·실행 기록 연결 |
| `tools/ktcloud-op02-bootstrap/{main.go,deploy.go,preview_mode_test.go}` | operator gate 충돌 사전 거절, deployment mode 선택과 recovery에서 gate 해제 |
| `tools/ktcloud-op02-bootstrap/README.md` | 보호 입력·실행 순서·cleanup·새 mode 준비 |
| `internal/connector/terminal/data_connection_backpressure_test.go` | 기존 테스트에서 정상 새 peer를 제공하고 이전 writer를 강제 fixture cleanup 전에 확인. 제품 코드 변경 없음 |
| 이 디렉터리 `verify.py` | source/Go JSON 요약과 cloud allowlist metadata export. cloud API 호출 없음 |
| 이 디렉터리 README/AUDIT/JSON/log | 재현, traceability, 최초 실패/skip, source/binary 일치, 실제 resource cleanup evidence |

ignored `.local/lbt175-20261019`는 Credential·개인키·정확한 Provider ID와 원본 로그,
실행 바이너리·Linux source archive를 보호한다. Git 산출물에 복사하지 않는다.
workspace 외부 operator runner와 Linux 준비 helper는 cloud mode를 명시하고
단계별 exit/log를 기록하는 실행 보조다. 필수 입력·동등 재현 명령은 README와
tracked bootstrap runbook에 기술했다. 기존 LBT-173/174 Evidence는 변경하지 않는다.

## B. 최신 SSOT 추적성

직접 확인한 원문:

- [LBT-175](https://samsunglions.atlassian.net/browse/LBT-175), updated 2026-10-16
  09:24:01 KST, 2026-10-19 재조회, 댓글 없음. parent LBT-24 진행 중.
- [검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) v3.
- [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5 확정.
- Git `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md`, Connector README §7b와
  `preview-control.schema.json`, `preview-data.schema.json`, `runtime/README.md`.
  정확한 파일 위치는 `source` manifest의 해당 hash로 고정한다.

| 요구사항/AC | 원문 | 구현·자동 검증 | 실제 실행 Evidence | 공백·판정 |
| --- | --- | --- | --- | --- |
| 최신 main에서 전체 검증 | LBT-175 범위1,6; TESTING | main/base 고정, fmt/vet/build, Linux 전체 Go, Web·contract·race | source/binary manifests, test summaries | 신규 GitHub CI는 push 보류로 미실행 |
| Management Tier·Resolver·host pin·direct-tcpip | D-18 v5 #8–11; LBT-175 범위2 | main의 `proveManagementSSH`, production Provider·App·VMAccess·SSHForwarder 사용 | g1/g2 management stage, 실제 loopback HTTP 왕복 | KT adapter Tier 대체는 기존 OP-01 구현, 가짜 VM/Resolver로 대체하지 않음 |
| Mock Gateway→production Control/Data WSS→실제 KT HTTP | LBT-175 AC1; Connector §7b | main:transport.normal, HTTP exact parser; peer auth·complete binding; `TestPreviewTransportResultsAndHTTPAreExact` | 두 `PREVIEW_TRANSPORT_G*_WSS_HTTP_TCP_BOUNDARIES_PASS` | SaaS/Browser는 명시적으로 별도 Gate |
| 하나의 logical session에 순차 0..N tunnel, active≤1 | Connector §7b | 정상3회, per-session active+app accepted/peak, duplicate success 후 기존 tunnel HTTP; exact binding mutants | 두 세대 TCP peak=1, duplicate/session isolation | 연결 종료 전 무조건 즉시 재OPEN하는 모든 스케줄은 unit regression 범위 |
| 금지 Port·잘못된 대상·앱 미실행 | Connector control/data schema 및 §7b | 22→PORT_REJECTED, 3001→APP_NOT_RUNNING, 잘못된 server/key→VM_UNREACHABLE, failure plan late ATTACH 거절 | 각 세대 실제 negative guard stage | 살아 있는 VM의 SSH route를 별도로 강제 단절하는 fault는 미주입; 해당 분류는 자동 테스트 |
| CLOSE·Gateway 종료 후 retry·session 격리 | LBT-175 AC2 | double CLOSE와 다른 session HTTP 유지, Gateway1000/1011→TCP0→재요청 | 두 세대 same live stage | 모든 network loss 조합·장시간 부하는 미검증 |
| Reset/Cleanup·stale generation·socket/channel 정리 | LBT-175 AC2; D-18 lifecycle | main before/afterMutation, 실제 active TCP 후 Reset/첫 empty Cleanup, stale UNAVAILABLE·정상 WSS close·TCP0·Data drain | RESET/CLEANUP ACTIVE_WSS_CLOSED stages; final lifecycle·graceful child | SSH channel close는 반대편 TCP0/Data close로 관측. 자체 별도 SSH exec manager와 child shutdown도 종료 |
| Control 일반 단절·결과 전송 실패에서 attached 유지, 4001/4002 retire | Connector §7b; PR114 baseline | WSS `TestPreviewAttachedTunnelControlDisconnect`, `TestPreviewAttachedTunnelDuringOpenResult`, handler/supervisor trust termination, preview owner regressions | 현재 소스 local race/Go 실행 | KT live fault injection으로 주장하지 않음 |
| HTTP·token·key·실제 IP 보호 | LBT-175 범위8; Connector §7b | bounded binary memory; HTTP path/header/body/token Control 비교; protected inputs, hashed export | DATA_DRAINED_CONTROL_CONTENT_SECRECY_PASS, final secret scan | synthetic payload조차 원문 response는 Evidence에 내보내지 않음 |
| 소유 자원 정리·전체 baseline 보존 | LBT-175 범위5/AC2; D-18 | 기존 typed ledger/reconcile, recover gates isolation, no name adoption | Lab10·bootstrap8·최종7종 inventory metadata | shared resources baseline 그대로 유지 |
| PR/CI·C3 인계 | LBT-175 AC4; CONTRIBUTING | README 재현·Known Limit·Evidence | 현재 branch의 local 검증 | push/PR/리뷰/merge/원격 CI는 승인 대기. C3 actual SaaS 별도 |

HTTP OpenAPI·Realtime 계약은 fixture가 변경하지 않는 owning consumer 경계다.
전체 contract workflow를 재실행하되 Backend PreviewSession/인가 구현 충족으로
확대하지 않는다. DB schema/migration은 무변경이고 unit/contract 검사만 적용한다.
DB integration은 실행하지 않았다. Backend 책임 문서도 외부 I/O/Repository 경계를
확인했으며 synthetic Gateway를 Backend 구현으로 추가하지 않았다.

최종 코드 위치: `preview_transport.go:65` OPEN/result, `:102` HTTP 전송,
`:132` exact HTTP 검증, `:185` 앱 설치, `:225` TCP 관측, `:275` 정상/negative
시나리오, `:393` mutation 전 active, `:405` bounded 관측, `:424` stale/close,
`:440` 최종 drain/secrecy다. peer `preview_transport_peer.go:100` 전체 binding,
`:106` auth/attach/read loop, `:188` shutdown admission/join을 확인했다.
main 배선은 `:458` g1, `:496` Reset 전, `:537` Reset 후, `:588` Cleanup 전,
`:596` Cleanup 후, `:632` 최종 drain이다. operator deployment/recovery는
`tools/ktcloud-op02-bootstrap/deploy.go:649,750`, 회귀는 새
`preview_transport_test.go:38,76,103,126,164,195,232`를 대조했다.

## C. 실패·보안·수명주기

| 위험 | 확인한 동작과 한계 |
| --- | --- |
| 입력·상관관계 | ATTACH 전체 envelope/payload binding의 11개 mutant, wrong/missing auth/subprotocol/query 거절. 잘못된 HTTP status/body/truncation은 실패 |
| 실패 후 늦은 attach | failure plan을 공개 전에 immutable로 표시. late ATTACH는1008+invalid, 최종 drain이 PASS하지 못함 |
| 중복·식별자 재사용 | 각 OPEN마다 새 messageID, 같은 session 순차 재사용, duplicate exact success와 기존 HTTP 확인. 다른 session CLOSE 독립 |
| 관측 race | app counter는 atomic rename. 실제 TCP 허용은 되었으나 counter 갱신이 늦는 경우 bounded poll. error/timeout을 성공으로 바꾸지 않음 |
| concurrent shutdown | peer의 stopped·connection map·WaitGroup은 동일 mutex 아래 admission. queue32×64KiB, nonbinary/overflow invalid. app process/SSH closer와 child graceful exit 별도 |
| Reset/빈 Cleanup | generation 변경 시 old attached WSS1000 종료. 빈 소유 목록은 runtime retire하되 물리 candidate 자동 삭제 금지. 실제 VM 존재 확인 후 정확한 refs로 cleanup |
| 불명 mutation·재시작 | fixture journal 존재 시 신규 control 거절. recovery는 Preview/OP02 gate를 끄고 typed knownID 조회 후 정리. 이번 실제 실행은 recovery 경로 미사용; 단위 테스트와 기존 도구 분석 |
| 외부 정합성 | HTTP delete 성공만으로 부재 판단하지 않음. typed reconcile + 각 종류 최종 inventory exact IDs. bootstrap 네트워크 공유 자원은 ownership 밖 |
| secret·content | 정상 TLS/SSH pin 사용, HTTP binary memory만 사용, 로그 allowlist·ID hash, Credential/private key/real IP exact scan. 공개 앱 포트 없음 |
| Terminal 기존 테스트 | 새 peer의 정상 final-write를 이전 peer backpressure와 분리. old unread peer를 유지하고 cleanup 전 old writer0 확인. 제품 코드·timeout 변경 없음 |

제품 timeout·retry 정책을 변경하거나 contract field를 추가하지 않는다.
특수한 재시작/VM 이미지/멀티VM/Internet ON·장시간 부하의 전 조합은 테스트하지 않았다.
이 변경의 주 경로는 Ubuntu24.04 단일 Workspace VM, Internet OFF 두 세대다.

## D. 실행 결과와 Evidence

최종 집계는 `validation-results.json`과 원본별 summary를 기준으로 한다.
PASS 숫자는 Go의 subtest event를 포함하며 서로 다른 실행을 더해 고유 testcase 수로
표시하지 않는다. skip된 cloud integration entrypoint는 별도 live CLI 실행과 구분한다.

- Linux `./...`: test event 3,970 PASS / 14 SKIP / 0 FAIL, 43개 test package PASS,
  테스트 없는 10개 package는 Go JSON의 package `skip`이다. 14개 testcase는 모두
  별도 gate가 필요한 Provider integration이다. POSIX symlink testcase는 PASS다.
- Windows 관련9패키지 race: 534 PASS / 1 SKIP / 0 FAIL. skip은
  `workspacefile.TestSymlinkRejectedAndExcludedFromTree`의 Windows 조건이다.
- fixture2패키지 race20회: 3,400 PASS / 0 SKIP / 0 FAIL.
- 보강 Terminal ownership testcase race20회: subtest 포함60 PASS, skip/fail0.
- fmt528파일, 전체 vet/build, Linux3개+Windows bootstrap cross-build PASS.
- Web16파일217테스트 PASS, typecheck/lint/build PASS. contract workflow와
  reset-schema PASS. 임시 pnpm lock 제거, tracked npm lock hash 동일.
- KT Control 00:29:05–00:38:42 UTC PASS; final inventory 00:43:10 UTC PASS.
  Lab10개·bootstrap8개 삭제,7종 exact baseline,owned residual0.

| 구분 | 실제 범위 |
| --- | --- |
| KT 실환경 | 두 실제 Workspace VM 세대, production child Control/Data/SSH forwarding, exact HTTP, guard, Reset/Cleanup·cleanup/baseline |
| Mock/Fake 자동 검사 | 새 Gateway peer 부정 사례와 기존 production preview/WSS/app/VMAccess 실패·race·lifecycle tests |
| Linux 기본 Go | 전체 `./...` (CGO0), 실제 POSIX symlink/permission semantics. 일반 unit test이며 Cloud/DB E2E 아님 |
| Windows race | 관련9패키지1회, fixture2패키지20회, 보강 Terminal old-socket testcase20회 |
| 정적/build/Web | 전체 Go fmt/vet/build, 네 cross-build binary, 전체 Web type/lint/test/build, 현 contract workflow |
| 미실행 | 신규 원격 CI·PR, PostgreSQL integration, 실제 Backend/SaaS/Browser C3, 다른 이미지·멀티VM·전 장애 조합 |

보존한 초기 실패:

1. 기본 clouds 파일은 local OpenStack alias만 있어 KT inventory parse가 실패했다.
   올바른 보호 KT 파일을 명시한 후 실제 KT 인증·image 조회를 확인했다.
   실패한 parse는 cloud mutation을 수행하지 않았다.
2. bootstrap 최초 SSH는 TCP22 15초 연결 실패였다. 같은 VM의 읽기 전용 SSH 재확인은
   성공했다. 최초 실패에서 원격 상세 원인을 관측하지 못했으므로 정확한 전파 원인으로
   단정하지 않는다. create/access를 반복하지 않았다. 두 attempt 모두 phase 기록에 있다.
3. Web 첫 시도는 pnpm 실행 경로 누락으로 실패했다. bundled 경로 명시 후 전체 통과했고
   tracked npm lock은 동일하며 임시 pnpm lock은 제거됐다.
4. Linux 전체 Go 최초 실행은 Terminal replace subtest1건과 그 parent가 실패했다.
   이전 writer는0이고 교체 작업은 완료, 새 unread socket final-write와750ms oracle
   충돌이라는 코드·시간 증거를 독립 검토했다. 테스트를 보강한 뒤 전체를 재실행했다.
5. Linux 준비용 OpenSSH probe에서 기존 ECDSA pin 대신 ED25519 선택, 공백 포함
   known_hosts 경로 해석, PKCS8 key 형식이 차례로 거절됐다. 기존 pin 알고리즘과 정확한
   경로를 지정하고 동일 private key의 OpenSSH encoding을 보호 디렉터리에 만든 뒤
   엄격한 검증으로 성공했다. host key/auth 우회나 cloud 재생성은 없었다.
   이 준비 probe의 마지막 로그는 같은 파일명으로 갱신되어 초기 원본 전체가 남아 있지
   않으며, 위 원인은 당시 관측 출력과 실행 보조 코드에 근거한다. acceptance 실패를 숨기지 않는다.

bootstrap create/access/첫SSH는 동일 production bootstrap 소스에서 먼저 빌드한
`bootstrap-setup.exe`를 사용했다. deploy 이후와 최종 재빌드는 `-mod=readonly
-buildvcs=false` 고정 플래그를 사용했다. setup binary·source는 보호 원본에 남겼다.
이 setup 바이너리와 deploy 바이너리가 같은 hash라고 주장하지 않는다.

## E. 수정 전 findings와 해결

다음 사항은 수정 전에 사용자에게 보고했고 reviewer finding도 검토·수용했다.
제품 production 코드 결함으로 잘못 분류하지 않는다.

| ID·심각도·검토 시 위치 | 재현·근거/영향 | 수정·회귀 |
| --- | --- | --- |
| F175-001 / P2, 기존 `tools/ktcloud-d1-acceptance/preview.go:24,48` | component smoke는 직접 SSHForwarder만 호출하여 실제 child Control/Data WSS AC를 검증하지 못함 | exclusive production-process transport mode 추가; 실제 KT 두 세대 실행 |
| R1 / P2, 새 `preview_transport.go:75–79`, `preview_transport_peer.go:137–145` 초안 | FAILED 결과 직후 늦은 ATTACH가 성공 plan으로 등록되어 false PASS 가능, 코드 분석 | immutable forbidden plan/1008/finalinvalid, `TestPreviewTransportLateAttachAfterFailedOpenCannotPass` |
| R2 / P2, 새 `preview_transport.go:370–378` 초안 | TCP accept와 counter publication 사이 관측 race가 정상 환경을 실패 처리 | bounded TCP poll, delayed publication/cancel testcase |
| R3 / P2, 새 `preview_transport.go:298–305` 초안 | duplicate 결과를 버려 FAILED/다른 session도 acceptance 가능 | exact success correlation + 기존 tunnel HTTP, duplicate failure/binding testcase |
| R4 / P2, 기존 `data_connection_backpressure_test.go:85–124` | 새 peer까지 unread라 정상 final ENDED 최대1초를750ms leak oracle이 오판. 최초 Linux 재현 + 독립 코드/시간 분석, stack 직접 관측은 없음 | 새 peer만 drain+join, 기존750ms 유지, 첫peer 수동cleanup 전 oldwriter0 확인.20회race·전체Linux 재검사 |
| Metadata 검토 | 모든 phase attempt exit0만 요구하면 최초 SSH 실패 보존과 최종 성공을 양립시키지 못함 | attempt 전체 보존 + 성공 stage 순서와 허용된 read-only SSH 실패를 별도 판정 |
| R5 / P3, `binary-manifest.json:23`, `binary-manifest-final.json:23` | 게시 시 source manifest를 별칭으로 복사했으나 참조는 보호 원본의 이전 파일명을 가리킴. 실제 바이너리/source hash는 동일하며 문서 참조 결함 | 게시 파일명 `compiled-source.json`/`compiled-source-final.json`으로 참조를 연결하고 원본 이름도 보존. JSON target 존재 확인 |

R1–R3는 fixture 초안에서 발견하고 KT 배포 전에 수정했다. R4는 KT 실행 중 일반 Go
검사에서 발견하여 테스트만 수정했고 production binary 동일 hash를 재확인한다.

## F. 독립 리뷰와 남은 범위

구현을 수행하지 않은 `/root/lbt175_design_review`가 Jira·D-18·검증 전략·Git 계약을
직접 확인하고 전체 fixture/배선 초안을 별도로 검토했다. R1–R3 제안, 수정 재검토 후
세 문제 해소와 추가 확정 코드 결함 없음으로 보고했다. Linux 최초 실패도 별도로
분석해 테스트 oracle 충돌 판단과 최소 보완을 제안했다. reviewer는 cloud mutation이나
부모가 수행한 테스트를 직접 실행하지 않았으며, 실행 증거 검토와 재실행을 구분한다.

최종 검토에서 reviewer는 보호 원본을 직접 읽고 다음을 재계산·대조했다.

- live/final canonical, Terminal 테스트1개 delta, 소스 archive648파일/toolchain SHA.
- 원본 Go JSON5개 전체 집계와 summary, 초기3968/14/2fail 보존.
- 배포된4개 바이너리 hash·배포 로그와 최종 재빌드의 일치.
- Lab/bootstrap 정확한 Provider ID의 공개 SHA metadata,7종 inventory ID hashes,
  owned residual0과 shutdown journal.
- R4의 새 peer drain/join·기존 unread peer 유지·강제 정리 전 oldwriter0 assertion.

코드·실환경 판정에서 추가 확정 correctness/보안/수명주기 결함은 없었다.
최종 R5/P3 문서 참조를 수정하고 두 target 존재를 독립 리뷰에서도 확인했다.
최종 manifest/scan을 갱신했고 Git diff whitespace 확인도 통과했다. reviewer는 테스트나 cloud를
재실행하지 않았고 원본 읽기·hash/집계 검토만 수행했다. 장시간 부하·미주입 fault·
실제 SaaS/Browser는 독립 리뷰를 통해서도 실행 검증으로 대체하지 않는다.
