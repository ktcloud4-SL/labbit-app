# C2 PR pre-merge audit — 2026-10-20

## 검토 기준과 범위

사용자 개인 `PRE_MERGE_AUDIT.md` 전체를 적용했다. 저장소 AGENTS/CONTRIBUTING/TESTING, PR template, [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22)(수정2026-10-19T21:52:37.638+09, 최신 관련 댓글10631), [D-20 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283/D-20+Lab)을 직접 재조회했다. Connector/realtime 계약과 관련 HTTP/runtime/DB 소비 경계를 대조했다. Jira/Confluence를 수정하지 않았다.

기준 base/검토 당시 HEAD는 `9172e050f5660c80b84ba70d52f0c549d8f032d9`. branch `codex/fix/LBT-22-c2-ktcloud-acceptance`의 base→HEAD와 staged/unstaged/untracked 전체를 검토했다. 원래 C3 이름의 checkout은 변경하지 않고 최신 main에서 별도 C2 worktree를 만들었다. 이번 PR은 아래14 코드·테스트와 이 Evidence 폴더만 포함한다. 소스 식별은 `pr-source-manifest.json`; 실제 배포 source와의 차이는 README에 명시했다. ignored node_modules는 기존 dependency 재사용이며 비밀 설정은 source에 포함하지 않았다.

| 파일 (`internal/connector/` 아래) | 목적·영향 |
| --- | --- |
| `provider/openstack/ktcloud_d1_topology.go`, `server.go` | KT network 경계의 gzip/base64 크기 사전 검사와 원문 보존 |
| `provider/openstack/ktcloud_userdata_test.go` | 압축 roundtrip/최종크기/HTTP200 malformed 생성 응답·불명 상태·중복 요청 금지 회귀 |
| `provider/openstack/terminal_target.go` | resolve_server/resolve_management/host_pin 단계 구분, 기존 소유권·generation 확인 유지 |
| `vmaccess/access.go`, `diagnostic.go`, `diagnostic_test.go` | SSH 단계별 안전한 진단, raw cause 제거, 허용 sentinel/context 보존과 악성 중첩 입력 회귀 |
| `terminal/ssh_pty.go` | PTY request/shell start 단계 구분, 기존 정리 경계 유지 |
| `wss/handler.go`, `terminal_failure.go` | factory/Data attach의 sanitized session correlation; 공개 오류 유지 |
| `app/app.go`, `control_error.go`, `control_error_test.go` | 고정 진단값·유효 UUID만 로깅, raw secret/오류 비노출 회귀 |
| `provider/openstack/terminal_c2_integration_test.go` | loopback fixture가 ACK 수신 뒤 서버 protocol-ready route를 기다린 후 단 한 번 open 요청 |

계약·schema·dependency·권한 모델·배포 설정은 바꾸지 않는다. 관련 호출자/consumer, gzip→SDK 인코딩, SafeError 변환, shared SSH channel users/reservation, close 경계를 검토했다. 전체 변경 중 미검토 파일은 없다. Runtime/DB migration 자체 변경은 없으므로 migration 재실행은 해당 없음이다. Gateway/HTTPRoute 적용 원문과 과거 per-request 로그는 확보하지 못했다.

## 수정 전 finding과 대응

| ID·심각도 | 파일/재현·영향·근거 | 수정·회귀 |
| --- | --- | --- |
| F-OBS / P2 | 기존 `app/app.go` OnError가 generic 경고만 남겨 factory/SSH/Data attach를 구분할 수 없음. 과거 A2 원인은 복구 불가 | 이전 작업 중 수정 전 보고 후 허용 필드 진단 추가. nested raw/error/token 비노출과 outer sentinel 보존 테스트. 과거 원인 해결로 간주하지 않음 |
| F-DIAG / P2 | 초기 제안 `vmaccess/diagnostic.go`에서 기존 wrapper 재사용 시 raw cause가 남거나 outer sentinel이 유실될 수 있음. 독립 reviewer 발견 | 이전 작업 중 수정 전 보고·실환경 배포 전에 raw cause 전체 제거와 허용 sentinel 재구성. 악성 중첩·context 회귀 통과 |
| F-C2-TEST-READY / P2 | 기존 main `terminal_c2_integration_test.go:488–490`은 ACK 수신 뒤 서버 `connectorwss/handler.go:371–381` MarkReady 완료를 기다리지 않음. 이번 Linux 실행에서 기존 open 호출(:569)이 ErrNotReady로 실패. 준비 순서에 따른 테스트 오탐이며 historical A2 원인과 별개 | 이번 작업에서 수정 전 사용자에게 보고. ACK 뒤 Registry.WithReadyRoute no-op bounded 대기 helper를 두 C2 fixture에 추가. command 재시도/제품 동작 변경 없음. Linux 전체 및 반복 결과는 `automated-validation.json` |
| F-FMT-EVIDENCE / P2 | 초기 run-checks.py는 gofmt -l exit0만 기록했고 Windows autocrlf 때문에534경로가 출력됨. 단독 로그로 format PASS라고 해석하면 부정확함. reviewer가 지적하고 사용자에게 보고 | 초기 로그 보존. 전체546 Go 파일을 LF로 정규화하고 모든 byte가 Git index blob과 같은지 확인한 뒤 gofmt -l exit0 **및 stdout 없음**을 검사해 통과. 제품 변경 없음 |

실환경 강사의 dual-NIC 응답 경로는 이전 작업에서 수정 전 보고하고 현재 VM에만 영구 복구·재부팅 검증했다. 해당 운영 helper의 실패/rollback 고장 주입은 미실행이며 PR 소스에 포함되지 않는다. 독립 reviewer가 위 준비 경합을 별도로 확인했다. 추가 confirmed product finding은 검토 범위에서 발견하지 못했다.

## 요구사항 추적성

| 요구사항·원문 | 구현/검토 | 자동 검증 | 실제 검증·Evidence | 한계·판정 |
| --- | --- | --- | --- | --- |
| LBT-22 prepared Workspace Browser/SaaS→Connector→SSH/PTY 왕복 | wss handler→terminal ssh_pty→vmaccess→Provider target | app/vmaccess/wss/terminal suite, Provider loopback/Fake SSH | 강사7·학생23 attach/종료, actual Browser/VM 관측 JSON | prepared 대표 기능 PASS; 정상 Provision 전체 완료 아님 |
| LBT-22 reconnect/close 대표 흐름 및 댓글10479의 독립 shell/resize | shared transport/독립 channel, realtime grace/TerminalPanel 소비 | Connector unit/in-process lifecycle, web unit | 같은ID grace resume/상태 유지, resize guest stty 일치, A 종료 후 B 생존, exit7 ENDED | 대표 경로 PASS. grace 만료60초·장기 soak는 미실행 |
| D-20 v7 prepared READY 의미·소유권/이력 보존 | Provider target의 generation/server/pin 검증 및 관리망 | Provider ownership/host pin/관리NIC 회귀 | 두 VM strict SSH/cloud-init/PTY, 강사 route 재부팅 확인 | 현재 fixture PASS. 미래 VM 일반 route 자동화/READY writer 범위 밖 |
| Startup/snapshot 불변·KT API 전송 경계 | server.go KT guard→gzip→SDK base64; topology final size preflight | compressed 원문 동일, 빈값, UTF8/HTML, final overflow·malformedHTTP200 unknown/no retry | live startupSHA 일치 및 실제 VM 생성/실행 준비; live-source manifest | KT 전송 수정 검증. 전체 신규 Provision→READY 동작으로 확대하지 않음 |
| 안전한 오류·금지정보 비저장, Connector/realtime 계약 | diagnostic allowlist→TerminalOpenFailure→safe log; wire 변화 없음 | adversarial raw wrapper/잘못된UUID/timeout/reason/context sentinel 회귀 | 배포 후 오류0, metadata만 저장 | 원래 A2 세부 원인 미확정. 실패 분류 실환경 주입은 미실행 |
| D-20 결과 불명 mutation과 cleanup 소유권 | 기존 Provider reconcile/ownership/terminal close 경계 | 불명 생성 응답1POST, 소유권·generation·close 회귀 | actual PTY0, 임시 정책/probe 삭제, 소유10 유지 명시 | owned0/C3/G3/G4 gate 판정 없음 |

## 실패·보안·수명주기 검토

- 입력/취소: 표준 Neutron의 기존 user_data 전송은 유지하고, KT의 비어 있지 않은 user_data는 항상 gzip한다. 압축 후 base64가 한도 초과면 API 호출 전에 실패. gzip 원문은 immutable. timeout과 취소 sentinel은 안전하게 남긴다. SDK malformed 응답을 성공 또는 새 재시도 근거로 쓰지 않는다.
- 상태/재시도/재시작: 이번 제품 변경은 진단과 전송 경계다. session/generation 상태 전이·durable pool·retry·reconciliation 계약은 바꾸지 않는다. 기존 lifecycle unit 및 실환경 대표 close/resume를 확인했다. crash/재시작·모든 mutation 실패 주입은 새로 실행하지 않았다.
- 동시성/소유권: 공유 SSH transport users/reservations와 독립 channel 정리를 유지한다. A 종료가 B를 닫지 않는 실제 증거와 기존 unit 경계가 있다. 새 fixture 대기는 read-only route predicate이며 command 중복 전송이 없다.
- 보안: Provider/SSH 원문 오류를 버리고 고정 enum, bool, 수치, 검증된UUID만 남긴다. public SafeError 및 pin 검증 우회 없음. 생성한 진단 wrapper가 raw cause를 Unwrap으로 노출하지 않는지 확인했다. Evidence에는 transcript/nonce/token/key를 저장하지 않았다.
- 외부 정합성: 과거 generic ATTACH_FAILED와 로그 부재로 특정 Provider·SSH·WSS 원인을 확정하지 않는다. 원래 ERROR/FAILED 이력은 유지하고 prepared 재검증을 별도로 표시했다. 자원10 유지 목적과 임시 정책/probe 삭제를 대조했다.

## 테스트·두 번째 검토·판정

포맷 검증은 초기 Windows CRLF 로그와 최종 Git index 동일 LF 검증을 구분한다. 전체546파일의 blob 동등성 및 gofmt 출력 없음으로 최종 통과했다. 최신 main 기반 실행 결과와 skip은 `automated-validation.json` 및 `validation/` 원본 로그에 기록한다. Go 기본 unit/in-process(Fake HTTP/SSH/WSS)와 실제 Browser/KT Cloud 증거를 구분한다. opt-in 실제 OpenStack tests 및 integration build-tag PostgreSQL tests는 새 PR 준비에서 실행하지 않았다. 제품/schema DB 변경은 없다. 실제 cloud fixture 재생성/추가 배포도 하지 않았다.

구현과 분리된 reviewer `/root/c2_c3_audit`가 최신 SSOT·전체 코드 diff·consumer를 직접 재검토했다. Evidence 최종 검토 결과는 `independent-review.md`에 기록한다. 남은 제한은 과거 오류 원인 미확정, 정확한 최종 PR binary의 실환경 미배포, 장기 soak/전체 Provision·cleanup/C3 미검증 및 web dependency 재사용 버전 차이다. **최종 판정: PR 가능.** 최신main 기준 전체 기본 Go 검증(Windows 일반 package + Linux Provider), canonical 포맷 검증 및 독립 전체 Evidence 검토를 마쳤으며 이번 제안 범위에 잔여 차단 finding은 없다. 웹 검증의 기설치 버전 차이를 공개했고 exact-lock CI 결과는 별도로 확인해야 한다. 더 큰 Jira 전체·과거 오류 원인 규명 완료를 뜻하지 않는다. Merge·배포·Jira 완료 전환은 이 audit의 승인 범위가 아니다.
