# PR #155 P2 두 건 수정 및 재검증

실행·검토일: 2026-10-23 KST. 사용자 요청 범위는 CIDR 예약 전 helper 검증과
Python 최적화 모드의 필수 검증 유지 두 건이다. 신규 release와 실제
Cloud → Browser File Tree E2E는 별도 운영 검증으로 남긴다.

## Revision 및 전체 diff 범위

- 저장소: `ktcloud4-SL/labbit-app`, branch `codex/LBT-26-workspace-bootstrap`.
- PR base: `f1de5945e80401763efbe33c5d1be4b5888af5be`; 최종 감사 전에 fetch한 main과 동일.
- 리뷰 전 HEAD: `883e4451ec360d7b1cdc2a585c3cd848a7f44556`.
- 테스트 당시 소스는 위 HEAD + 4개 소스/테스트 파일의 로컬 수정이다.
  `review-fix-validation.json.source_files`에 전체 PR의 16개 구현·테스트·운영/계약 파일의
  LF-normalized SHA256을 기록했다. 테스트 후 추가한 문서·JSON은 실행 동작에 관여하지 않는다.
- 이전 `validation.json`의 commit/hash와 실행 결과는 그대로 보존했다. 이전 PASS를
  변경된 코드의 실행 결과로 간주하지 않는다. 이전 CI 9개 성공과 Linux 결과는 사용자가 인정한 기준이다.

| 전체 PR 파일 | 목적·영향 경계 | 이번 확인 |
| --- | --- | --- |
| `.github/workflows/delivery-connector.yml` | 같은 release의 helper 제공 | 변경 없음, 기존 제출 diff 재검토 |
| `scripts/installer/install.sh`, `install_test.sh` | 독립 approved pin, 네 File 설정, 안전한 재시도 | 변경 없음, 기존 Linux mock 검증 유지 |
| `runtime/contract.yaml` | installer/Connector 설정과 CIDR UNKNOWN 소유 계약 | 필수 설정·이전 불확실성 우선 규칙 직접 대조 |
| `client.go` | 환경변수 소비 | 변경 없음, producer/consumer 재검토 |
| `provision.go`, `lifecycle.go` | 생성/Reset 삭제 전 검증과 readiness | scoped 진입·Reset resource 반환 경로 재검토 |
| `ktcloud_d1_cidr.go` | 신규 영속 슬롯 예약 전 artifact 거절 | 이번 수정, 기존 assignment/Reset HOLD 판단 유지 |
| `workspace_bootstrap.go` | artifact 검증과 pinned 관리 SSH bootstrap | 변경 없음, 사전 검증 재사용·manual mode 확인 |
| `workspace_bootstrap.py` | 안전한 guest filesystem 준비·bounded SFTP probe | 이번 수정, 모든 `assert`를 명시적 조건/예외로 교체 |
| `workspace_bootstrap_test.go` | artifact·실패·readiness 회귀 | 이번 수정, 예약 누수 2조건·UNKNOWN 8조건 추가 |
| `workspace_bootstrap_guest_test.py` | 실제 Linux root/non-root acceptance | 이번 수정, 최적화 실행 선택·비정규 경로 회귀 추가 |
| `startup.go`, `ssh_cancellation_test.go` | stdin SSH 실행·취소·정리 | 변경 없음, 기존 호출자/실패 경계와 전체 Provider 회귀 확인 |
| `docs/connector/installer-runbook.md`, `workspace-sftp.md` | 설치·이미지 전제·운영 검증 경계 | 변경 없음, 기존 실제 VM retrofit으로 오인하지 않도록 확인 |
| 기존 evidence `README.md`, `validation.json` | 최초 제출 audit와 결과 | README에 후속 기록 연결, JSON은 역사 기록 보존 |
| `REVIEW_FIX_AUDIT.md`, `review-fix-validation.json` | 이번 수정의 감사·실행 결과 | 신규 기록, 원본 결과와 소스 해시 대조 |

위 구현 경로의 prefix는 `internal/connector/provider/openstack/`, 운영 문서는
`docs/connector/`다. 전체 diff와 영향받는 consumer를 확인했으며 제외한 변경 파일은 없다.
개인 SSH 인증 준비와 원본 실행 로그는 로컬에만 남긴다. 인증 자료를 Evidence에 복사하지 않았다.

## 수정 전 findings와 조치

| ID·심각도 | 발견 revision·파일/라인 | 재현·영향 | 수정·회귀 |
| --- | --- | --- | --- |
| R1/P2 | `883e445`, `provision.go:49`, `ktcloud_d1_cidr.go:332-374` | public Provision의 helper 누락/해시 불일치에서 FAILED 후 `slot-000.json` 잔류. Linux에서 신규 2개 subtest 실패 재현. 영구 예약을 자동 반환하지 않는 기존 수명주기 때문에 풀 소진 가능 | `ktcloud_d1_cidr.go:336`의 신규 슬롯 분기에서 예약 전 검증. 두 거절 후 슬롯/intent/LOCK 없음과 풀의 두 쌍 모두 사용 가능 확인 |
| R2/P2 | `883e445`, `workspace_bootstrap.py:26-145` | `-O`에서 digest/경로/권한/probe 제한 assert 제거. 잘못된 digest가 exit 0으로 허용됨. 비정규 경로 재현은 20초 외부 timeout으로 ERROR | `require`의 명시적 `if`/`ValueError`로 모든 필수 평가 유지. 일반/-O/환경변수 최적화에서 같은 13개 acceptance 실행 |
| R1 보완/P2 | 최초 로컬 수정의 `ktcloud_d1_cidr.go:322`, 독립 reviewer 코드 분석 | 너무 이른 guard가 Reset의 assignment/previous intent 누락에 대한 기존 UNKNOWN을 가릴 수 있음. 수정 전에 사용자에게 보고 | guard를 신규 슬롯 분기에만 한정. public Reset에 정상 snapshot/resource refs를 넣고 두 UNKNOWN 분기 추가. 최종 8조건 모두 PASS |

R1 보완은 최초 수정 후보의 문제이며 별도 제품 요구사항 추가가 아니다.
확정 실패 기록은 `review-fix-validation.json.before_fix`에 보존했다.

## 요구사항 → 구현 → 검증 추적

| 요구사항·원문/버전 | 구현 | 자동 테스트·실행 | 실제 환경·공백 | 판정 |
| --- | --- | --- | --- | --- |
| [PR #155 Request Changes](https://github.com/ktcloud4-SL/labbit-app/pull/155): helper 실패 전에 CIDR 영구 예약 금지 | `ktcloud_d1_cidr.go:336-340` | `TestWorkspaceArtifactFailurePrecedesCIDRReservation`의 missing/hash mismatch, 수정 전 FAIL → 최종 PASS | Linux filesystem fsync/slot 검사 실제 실행; Provider API는 fake | 충족 |
| 사용자 요청의 기존 UNKNOWN 유지, `runtime/contract.yaml:671`, `contracts/connector/README.md` UNKNOWN/reconcile 계약 | 동일 함수의 기존 lock/state/intent 경로 유지 | `TestWorkspaceArtifactFailurePreservesCIDRUnknown`: Provision/Reset의 intent/own lock/corrupt slot, Reset missing assignment/previous intent 8조건 PASS. 기존 CIDR 동시성·재시작·lost response 테스트 포함 Provider 전체 PASS | 실제 cloud Create/Delete/Reset 미실행 | 수정 범위 충족 |
| PR Python 필수 검증을 최적화 모드에서도 유지 | `workspace_bootstrap.py:19` 및 모든 기존 검증 위치 | Linux guest normal, `python3 -O`, `PYTHONOPTIMIZE=1` 각각 13 PASS | root scratch에서 실제 non-root helper INIT/STAT, digest/path/ownership/permission/probe 제한·후손 종료 확인 | 충족 |
| 기존 LBT-26/LBT-149, D-18 v5의 준비·권한·관리 SSH 책임 경계 | installer/Provider/bootstrap/SSH 전체 diff | 기존 감사의 추적성에 이번 39 guest·Provider 전체·vet/build 보강 | release·신규 VM·Cloud→Browser E2E 미실행 | 운영 검증은 후속 |

Jira LBT-26/LBT-149와 D-18의 원문·최신 댓글은 최초 제출 감사에서 같은 날 직접 확인했다.
이번에는 사용자가 전달한 두 리뷰 원문과 GitHub inline review를 최신 수정 요구로 확인했다.
별도 Jira/Confluence 쓰기·재문의는 필요하지 않아 수행하지 않았다. HTTP/Realtime/DB에
새 계약/변경은 없으며 기존 사용자·readiness·프로토콜 계약을 유지한다.

## 실패·보안·수명주기 검토

- 신규 슬롯이 없는 Provision에서 helper 실패는 cloud mutation/slot/intent 전에 FAILED다.
  pool binding 파일 생성은 기존 설정 검증 동작이며 슬롯 점유가 아니다. 보유 슬롯을 회수하는 로직은 추가하지 않았다.
- 기존 assignment에는 원래 intent 검사가 먼저 실행된다. Reset은 새 슬롯을 예약하지 않고
  원래 assignment/직전 generation 기록을 확인한 뒤 scoped Reset의 artifact 검사로 간다.
- 동일 instance lock/intent/손상과 누락 Reset 기록은 UNKNOWN/HOLD를 유지한다.
  기존 intent/slot 자동 복구·삭제·blind retry를 추가하지 않았다. 부분 리소스 반환·Cleanup 책임도 동일하다.
- guest의 digest/경로/UID/GID/mode, packet 크기·누적 출력, deadline, selector, process wait가
  최적화 상태에서도 실행된다. 예외는 기존과 동일하게 SSH exit status로만 노출된다.
- lock/no-follow/no-replace, 현재 workspace·generation·host key 확인, stdin 업로드를 유지한다.
  기존 사용자 데이터/다른 helper/권한을 덮어쓰는 경로를 추가하지 않았다.
- 실제 통신 채널/세션/DB 상태 전이를 변경하지 않는다. 기존 Provider 전체 테스트에서 취소·SSH 정리·readiness 회귀를 실행했다.
  SIGKILL/전원 장애의 임시 파일 잔류 한계는 기존 감사와 동일하다.

## 실행 결과, 재현, 독립 리뷰

- Linux Provider package: top-level 173 PASS, 실제 cloud gate 14 SKIP. 새 예약 2조건과
  UNKNOWN 8조건 포함. Provider는 fake/in-process SSH 경계이며 cloud E2E가 아니다.
- Linux guest: 일반/-O/PYTHONOPTIMIZE 각각 13 PASS, 총 39. 실제 Linux root scratch와
  non-root helper를 사용했다. helper SHA256은 JSON에 기록했다.
- Linux target `go vet ./internal/connector/provider/openstack`, Connector build, `git diff --check` PASS.
- 기존 CI/installer/Web 결과는 인정된 이전 revision 기록으로 유지한다. 이번 로컬에서는
  변경 없는 installer/Web 전체를 반복하지 않았고 Windows filesystem acceptance도 실행하지 않았다.
- 최종 runtime 조회: 기존 Connector active, PID/start time/binary hash가 기존 값과 동일.
  임시 suite/test directory와 scratch process 모두 0. Connector 교체/재시작·설정 변경과 실제 VM 수정 없음.
- 구현과 분리된 reviewer가 base→전체 PR diff, 최신 수정 후보, SSOT, 원본 실행 결과를 검토했다.
  Reset UNKNOWN 누락 finding을 수용·수정했으며 최종 diff 및 추가 테스트를 재검토했다.
  독립 reviewer의 실 cloud/browser 실행은 없고 코드·Evidence 검토 범위다.
  독립 reviewer는 Jira/Confluence 원문을 별도 재조회하지 않았으며 사용자 최신 P2 지시,
  로컬 owning SSOT, 기존 감사 기록과 구현 담당자의 같은 날 원문 조회에 의존했다.

Linux 재현(폐기 가능한 root 환경, 실행 가능한 filesystem의 `/opt/test-helper` 사용):

```sh
go test ./internal/connector/provider/openstack -count=1 -timeout=90s
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' \
  -o /opt/test-helper ./cmd/labbit-workspace-sftp
for mode in '' flag env; do
  sudo env LABBIT_BOOTSTRAP_TEST_HELPER=/opt/test-helper \
    LABBIT_BOOTSTRAP_TEST_OPTIMIZE="$mode" python3 -m unittest discover \
    -s internal/connector/provider/openstack -p workspace_bootstrap_guest_test.py
done
```

원본 실행 로그는 로컬 `outputs/workspace-bootstrap-review-20261023/`에 보관하며
공개 요약 JSON에 원본 로그 hash와 guest 실행 결과를 기록했다. 개인 SSH 환경을
재사용하지 않아도 위 명령으로 소스·helper를 빌드해 재현할 수 있다.

판정: 요청한 두 P2 수정의 재리뷰 제출 범위에서 감사 수행. 신규 release와 실제
Cloud → Browser File Tree는 미검증 운영 후속이며 merge/배포 승인이나 LBT-26 전체 완료를 뜻하지 않는다.
