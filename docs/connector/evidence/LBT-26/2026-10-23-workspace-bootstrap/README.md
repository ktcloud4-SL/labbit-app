# Workspace File bootstrap 검증 및 pre-merge audit

검토일: 2026-10-23 KST. 목적은 공식 Connector 설치와 신규 Provision/Reset에서
Workspace helper·작업 폴더·File 환경변수 준비를 자동화하는 것이다.
기존 운영 Connector/VM의 File 장애 수동 복구나 FE 통합 전체의 완료 판정과 구분한다.

## 검토 revision과 범위

- base: `f1de5945e80401763efbe33c5d1be4b5888af5be` (`main`).
- 구현 commit: `e0101125917a6be0b4d6432d10be0611a5beca85`.
- branch: `codex/LBT-26-workspace-bootstrap`.
- 최초 검증 base `1e9f7db`에서 최신 main으로 rebase했다. 최신 main의 PTY readiness
  attestation을 유지하며 Workspace bootstrap이 성공한 뒤 PTY probe를 수행한다.
- 아래 15개 구현·테스트·계약·운영 문서 전체와 관련 consumer를 검토했다.
  이 README와 `validation.json`은 검증 후 추가한 기록이며 동작을 변경하지 않는다.
- 실행 당시 미커밋 소스와 위 구현 commit의 대응은 `validation.json`의 LF-normalized
  파일 SHA256으로 식별한다. 해당 JSON은 로컬 원본 로그의 요약이며 원본 전체 로그는 포함하지 않는다.

## 요구사항 → 구현 → 검증

| 요구사항·SSOT | 구현·검토한 파일 | 검증과 한계 |
| --- | --- | --- |
| [LBT-26](https://samsunglions.atlassian.net/browse/LBT-26) 후속 사용자 요청: 신규 VM의 File 준비 자동화 | `provision.go`, `lifecycle.go`, `workspace_bootstrap.go` | artifact 오류 시 cloud mutation/Reset delete 0, 준비 실패 시 partial resources 보존·readiness 없음. Cloud API fake이며 실제 신규 VM 생성/Reset은 미실행 |
| [LBT-149](https://samsunglions.atlassian.net/browse/LBT-149): 관리자가 helper/root 준비, 파일 처리는 non-root | `workspace_bootstrap.py`, `workspace_bootstrap_guest_test.py`, `workspace_bootstrap_test.go` | Linux root scratch에서 12개 PASS. 실제 non-root helper INIT/STAT, fresh/repeat/data 보존, symlink/권한/다른 helper 거절, timeout 정리, restrictive umask 포함 |
| [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5: 인터넷 OFF 관리 경로, VM별 direct SSH·관리키 로컬 보관 | `startup.go`, `ssh_cancellation_test.go`, `workspace_bootstrap.go` | pinned host key/현재 Workspace·generation·VM 소유권 확인을 재사용. SSH stdin/거절/취소 fixture PASS. guest 다운로드 및 사용자 StartupScript/user_data 변경 없음 |
| `runtime/contract.yaml` producer/consumer | `client.go`, `runtime/contract.yaml`, `scripts/installer/install.sh` | 네 File 설정 생성·소비, source/hash 둘 다 없는 기존 수동 모드 보존. installer Runtime/Integrity 테스트 PASS |
| 공식 설치·배포물 무결성 및 재시도 | `.github/workflows/delivery-connector.yml`, `scripts/installer/install.sh`, `scripts/installer/install_test.sh` | 같은 release helper 포함, 독립 approved helper pin, checksum/변조/누락 거절, 401 후 안전한 동일 helper 재사용. Linux installer 12개 범주 PASS; HTTP/enrollment/systemd는 mock |
| 운영 반영·이미지 전제 | `docs/connector/installer-runbook.md`, `docs/connector/workspace-sftp.md` | Linux amd64·Python3·runuser·passwordless sudo, source와 VM 설치 경로 구분, 기존 READY VM은 retrofit하지 않음을 명시 |

Jira LBT-26/LBT-149 최신 본문·댓글과 D-18을 10/23에 직접 재조회했다.
LBT-26의 기존 VM File 저장/Terminal/Live/Resize 증분은 이번 신규 자동화의 E2E 증거로 사용하지 않는다.
HTTP/Realtime/DB schema 변경은 없다. Connector readiness 계약과 Backend consumer는 최신 main 구현을 유지한다.

## Findings와 수정 후 확인

구현과 분리된 reviewer가 전체 diff, runtime/consumer, 실패·보안·수명주기 및 evidence를 검토했다.
발견 내용을 수정 전에 보고했으며 다음 회귀를 확인했다.

| Finding | 재현·영향 | 조치·회귀 |
| --- | --- | --- |
| F-003/P2, F-004/P2, F-005/P2 | 실패한 enrollment 재시도 차단, 대문자 digest 불일치, root/helper 역방향 중첩 | 안전한 동일 helper 재사용, digest 정규화, 양방향 중첩 사전 거절. installer/Provider/guest 회귀 PASS |
| F-006/P1 | helper와 SHA256SUMS 동시 변조 시 Connector pin만으로 새 helper 신뢰 불가 | 독립 approved helper digest 필수화. 누락/불일치에서 enrollment/install 전 거절 PASS |
| F-007/P2 | probe 출력 무제한 수집 및 timeout 후 자식 프로세스 잔류 | 읽는 중 64KiB/10초 제한, 별도 process group 종료·wait. malformed/oversize/descendant timeout PASS |
| F-009/P2, 발견 revision `5874353`, `workspace_bootstrap.py:47-52` | sudo/root umask 0027/0077에서 새 상위 폴더가 root 전용이 되어 non-root helper 실행/조회 실패. Linux에서 두 조건 모두 실패 재현 | 새로 만든 parent fd에만 0755 명시. 두 umask의 fresh/repeat PASS, 기존 0700 parent는 보존·접근 실패. guest 전체 12 PASS |

확정된 미해결 결함은 검토 범위에서 없다. startup bytes 불변과 bootstrap 실패 시 PTY probe 0·readiness nil도 회귀로 확인했다.

## 실패·보안·수명주기 경계

- artifact 크기/형식/hash/경로를 생성·Reset 삭제 전에 확인하고 검증한 bytes만 전송한다.
- root/helper 중첩, symlink, writable ancestor, 기존 다른 helper를 거절한다. 기존 workspace 데이터·권한을 임의 변경하지 않는다.
- helper 설치는 lock과 no-replace 게시를 사용한다. 같은 artifact 재실행은 재사용하며 cloud Create/Delete blind retry를 추가하지 않는다.
- 확정 remote 거절은 FAILED, 전송·소유권 확인 결과 불명은 UNKNOWN이며 확인된 resource ID를 반환한다.
- timeout/cancel 시 socket/session/process group을 닫는다. SIGKILL/전원 장애에서는 finally 실행을 보장하지 않아 보호된 임시 파일이 남을 수 있다.
- 실제 운영 credential/key/env/PTY transcript는 기록에 포함하지 않는다. 테스트 대상은 임시 Linux scratch이며 기존 서비스·VM 설정은 변경하지 않았다.

## 실행 결과와 잔여 검증

- Linux Provider 전체 package PASS. 실제 cloud integration gate 14개 SKIP; fake/in-process SSH 결과를 cloud E2E로 확대하지 않는다.
- Linux guest 12 PASS, installer shell 12개 범주 PASS. 임시 suite 삭제 및 잔여 test directory 0 확인.
- Go vet/build PASS, Linux Connector build PASS. Windows targeted bootstrap/SSH/readiness-state 회귀 PASS.
- Windows 나머지 Go package 실행에서 installer shell suite가 파일 권한 의미 차이로 FAIL, 다른 package는 PASS.
  별도 Windows PTY fixture는 `/bin/sh` 의존으로 FAIL이며 동일 Provider 전체는 Linux에서 PASS.
  이전 Windows CIDR fsync 실패 이력도 있어 전체 Windows `go test ./...` green을 주장하지 않는다.
- Web typecheck/lint/build PASS, Vitest 441 PASS. 이전 10/22의 별도 cleanup EPERM 실패 이력과 구분한 최신 실행 결과다.

Linux에서 재현할 주요 명령:

```sh
go test ./internal/connector/provider/openstack -count=1 -timeout=90s
go test ./internal/connector/installer -count=1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /opt/test-helper ./cmd/labbit-workspace-sftp
# disposable Linux root 환경에서만 실행, helper는 실행 가능한 filesystem에 배치
sudo LABBIT_BOOTSTRAP_TEST_HELPER=/opt/test-helper python3 -m unittest discover \
  -s internal/connector/provider/openstack -p workspace_bootstrap_guest_test.py
```

판정: 코드 리뷰용 PR 제출 범위에서 감사 수행. 실 cloud Provision/Reset→Browser File Tree,
GitHub release 다운로드 및 운영 반영 검증은 미실행이며 merge/배포 승인으로 해석하지 않는다.
기존 default `connector-v0.1.0`에는 helper asset이 없으므로 새 release와 승인된 Connector/helper
digest를 준비해야 한다. 기존 실행 Connector/VM은 교체·재시작·Reset하지 않았다.
