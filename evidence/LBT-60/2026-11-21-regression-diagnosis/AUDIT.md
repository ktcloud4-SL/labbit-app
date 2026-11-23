# 2026-11-21 Connector·GitOps 회귀 실패 진단 및 수정 검토

이 기록은 이전 `2026-11-21-stage3-offline/AUDIT.md`의 실패 원인 미확인 상태에 대한 후속 기록이다.
이전 로그와 manifest는 당시 실행 결과로 보존한다. 이전 FAIL·timeout을 삭제하거나 PASS로 수정하지 않는다.
이번 실행 소스는 원격 PR HEAD가 아니라 새 `source-manifest.json`에 고정한 미커밋 후보다.

## 검토 기준과 전체 변경 범위

- 사용자 `PRE_MERGE_AUDIT.md`, App AGENTS/CONTRIBUTING/TESTING, GitOps AGENTS를 읽었다.
- App base/HEAD: `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac`, branch `codex/feat/LBT-60-stage3-offline-20261121`.
  fetched main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- GitOps base/HEAD: `ab830d7c9e046b0621477192aca39c6b269cbf30`, branch `codex/feat/LBT-60-stage3-profiles-20261121`.
  fetched main도 같은 commit이다. fetch는 읽기 전용으로 수행했다.
- 깨끗한 App base worktree `C:/Users/user/.codex/tmp/labbit-stage3-regression-base-20261121`에서
  Stage3 변경 없이 대표 CIDR/late ACTIVE 실패, concurrent timeout, installer 실패, PTY 실패를 재현했다.
  최초 전체 실패 39개 각각을 모두 baseline에서 실행한 것은 아니다.
- 이번 delta는 아래 **App 테스트 6개 + GitOps 테스트 1개**, 진단 Evidence뿐이다.
  이전 Stage3 구현/문서/설정 23개와 GitOps 후보 3개의 raw SHA는 `prior-source-comparison.json`으로
  비교했다. 그 밖의 기존 변경은 이전 audit의 요구사항 추적·독립 리뷰 범위를 승계한다.
- PR196 자체와 이전 종속 PR 전체를 새로 인수했다고 주장하지 않는다. Infra 구현은 이번에 수정하지 않았다.
  기존 원본 작업 디렉터리의 다른 미커밋 수정도 건드리지 않았다.

| 파일 | 이번 수정 / consumer 영향 |
| --- | --- |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_test.go` | POSIX 보호 원장 fixture의 Windows SKIP, 실제 fail-closed 검증 2개, callback 미진입 시 대기 종료 |
| `internal/connector/provider/openstack/active_readiness_test.go` | fixture 조건 확인과 synctest 외부 SKIP. ACTIVE 정책·시간값 변경 없음 |
| `internal/connector/provider/openstack/readiness_diagnostic_test.go` | 같은 fixture를 쓰는 synctest 외부 SKIP. 플랫폼 중립 정책 테스트는 유지 |
| `internal/connector/provider/openstack/ktcloud_management_route_test.go` | 같은 fixture를 쓰는 해당 테스트만 외부 SKIP |
| `internal/connector/provider/openstack/readiness_test.go` | loopback SSH peer가 Windows의 Git sh를 사용. 실제 Connector의 명령·인증·host pin 검증은 유지 |
| `internal/connector/installer/installer_test.go` | install.sh/install_test.sh 문법 검사와 Linux 권한·systemd 동작 시험을 별도 subtest로 구분 |
| GitOps `charts/common-app/tests/test_scheduling.py` | 실제 서울 Application의 valueFiles로 render/lint. DB/Valkey SPC 두 개를 각각 검사 |

## 최신 SSOT 및 추적성

LBT-205 최신 본문(11/20), Confluence 테스트 전략 [9666637 v4](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637)를
연결 도구로 다시 확인했다. [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59)·
[LBT-60](https://samsunglions.atlassian.net/browse/LBT-60)의 인수 경계와 이 대화의 AWS/push 금지 지시를 유지한다.

| 요구사항 / SSOT | 구현 및 자동 검증 | 실제 Evidence / 공백 |
| --- | --- | --- |
| 테스트 전략 v4·TESTING: 실제 실행/미실행 분리, production 우회 금지 | `requireProtectedCIDRFixture`, 3개 outer test SKIP, installer syntax/behavior subtest | `go-all-final.jsonl`에 Windows SKIP 명시. Linux positive 실행은 미수행 |
| 기존 보호 원장 계약: 0700/0600·directory fsync, UNKNOWN 보존 | production `ktcloud_d1_cidr.go:80,106,301` 유지. `TestKTCloudCIDRPoolUnsupportedWindowsPermissionsFailClosed` | fresh/기존 INTENT 2개 실제 Windows filesystem + loopback peer. HTTP·callback 0, ledger 불변 PASS |
| 테스트 실패 시 무기한 대기 금지 | concurrent fixture의 completion/deadline select와 release cleanup | `base-openstack-timeout.jsonl`에 기존 timeout. 수정 Linux positive 동시성 경로는 compile만, 실행은 Linux CI 필요 |
| Connector SSH PTY proof / host pin·인증·취소 의미 유지 | `TestWorkspacePTYProof`의 7개 사례, 로컬 SSH peer에만 shell 경로 선택 | `base-pty.jsonl` 기존 EOF, 최종 loopback 7사례 PASS. 실제 VM SSH/Connector DR 복구 아님 |
| installer Ubuntu/Linux 런타임 계약 유지 | 문법 2파일, Linux fixture 분리 | syntax PASS / Windows behavior SKIP. Git Bash 7C `-x` 판정 실패 baseline trace 보존 |
| 실제 GitOps producer/consumer values 일치 | `apps/aws-seoul/backend.yaml`의 source·valueFiles 읽기, 두 SPC·peer mTLS/NetworkPolicy 검사 | Helm template·strict lint 포함 전체11/11 PASS. Application·values·배포 설정 변경 없음 |
| 기존 Stage3 승인·binding·HOLD 계약 | 이전23개 source와 GitOps3개 source SHA 유지, 전체 Go 회귀에 포함 | 앞선 scoped race/폐기용 PG 결과는 이전 audit의 한정 결과. 이번에 PG/Valkey/AWS 재검증은 하지 않음 |

HTTP/WSS/Realtime message, Runtime, DB migration 및 제품 코드 변경은 이번 delta에 없다.
기존 Stage3 Runtime0.1.13 후보와 그 producer/consumer 검토는 이전 audit에서 다뤘으며,
이번 원인 해결을 위해 security gate나 테스트 기대값을 낮추지 않았다.

## 수정 전 findings / 재현 / 해결

수정 전 사용자 commentary로 원인·영향·회귀 방향을 보고했다. 원본 라인은 App `c11b70a` / GitOps `ab830d7` 기준이다.

| ID·심각도 | 위치·재현 조건 | 영향·필요 회귀 | 해결 및 한계 |
| --- | --- | --- | --- |
| F-GITOPS / P2 | `test_scheduling.py:103,105,135`, 삭제된 backend-valkey-values.yaml 참조 | 전체 테스트가 FileNotFound로 중단. 실제 Application values render/lint 필요 | #121 `b8c9cdf`의 통합 values를 사용. 독립 baseline6PASS/1ERROR → 최종 전체11PASS |
| F-HOST / P2 | `ktcloud_d1_cidr_test.go:40`, `active_readiness_test.go:154`, `installer_test.go:211`; Windows/Git Bash | 보호 권한을 구성할 수 없는데 Linux positive 기대를 실행. production은 정상적으로 거부 | Linux-only fixture Windows SKIP, fail-closed/무변경 시험 추가. Linux runtime PASS로 계산하지 않음 |
| F-WAIT / P2 | `ktcloud_d1_cidr_test.go:151`, callback 전 조기 거부 후 `<-entered` | 전체10분 timeout으로 후속 검증이 실행되지 못함. base8초 재현 | finished/deadline/release once를 추가. Linux 동시성 positive는 미실행 |
| F-PTY / P2 | `readiness_test.go:116,120`, Windows에 `/bin/sh` 없음·Start error 무시 | loopback SSH positive가 EOF로 실패. base 재현 | peer에 Windows Git sh 선택, Start error 명시. shell roundtrip 포함7개 PASS |
| F-SYNCTEST / P2 | 최초 수정의 readinessFixture 내부 skip 및 `active_readiness_test.go:179`, diagnostic:33, management-route:111 | synctest 내부 skip이 outer PASS로 보임. 독립 reviewer가 재현 | 외부 real T에서 SKIP. 세 outer Test의 최종 JSON Action=skip 확인 |

F-SYNCTEST는 최초 수정 시 발견해 바로잡은 문제다. 당시 `connector-final.jsonl`에는 PTY FAIL과
내부 skip 집계 한계가 남아 있으므로 최종 결과로 사용하지 않는다. `connector-recheck.jsonl` 이후
shell 경로가 Windows에서 미발견으로 SKIP된 것을 확인해 정확한 절대 경로로 정리했다.
최종 현재 소스는 `connector-race-final.jsonl`과 `go-all-final.jsonl`로 검증했으며 PTY positive도 PASS다.

## 실행 결과와 제외 범위

정량 값은 `test-summary.json`을 따른다. Go Test terminal event(parent/subtest 포함)만 세며 package·재실행을 중복 합산하지 않는다.

- Windows Go 전체 `go test -count=1 -json -timeout 120s ./...`: **4527 PASS / 0 FAIL / 88 SKIP**, **52 package PASS**.
- 현재 소스 Connector/OpenStack·installer `-race -count=1 -timeout60s`: **566 PASS / 0 FAIL / 80 SKIP**.
- GitOps 전체: **11 PASS / 0 FAIL**, 실제 Helm render·strict lint 포함.
- `go vet ./...`, `go build ./...`: exit0.
- 전체 Go640개 LF canonical gofmt 비교: 모두 일치(`gofmt-canonical.json`). CRLF checkout을 일괄 수정하지 않는다.
- Linux/amd64 OpenStack·installer `go test -c`: exit0. **교차 컴파일만 수행, Linux에서 실행한 결과가 아니다.**
- 88 SKIP에는 보호 CIDR·readiness Linux fixture, Linux installer behavior, 기존 opt-in OpenStack/Valkey·symlink 관련 등이 포함된다.
  정확한 test명·사유 원문은 JSON에 남긴다. missing capability를 PASS나 실환경 검증으로 바꾸지 않는다.
- 앞선 폐기용 실제 PostgreSQL16 integration1699PASS/10SKIP, Web 초기1FAIL 및 해당 파일30PASS 재검증,
  Infra69+6PASS는 이전 source manifest에 대한 역사적 검증이다. 이번 변경에서 해당 source는 유지되었으며,
  DB/Web/Infra는 재실행하지 않았다. Linux CI·실환경 테스트가 필요 없다는 뜻이 아니다.

## 실패·보안·수명주기 및 독립 리뷰

- product의 POSIX 권한, host pin, SSH 인증, PTY challenge, mutation UNKNOWN 판단을 변경하지 않았다.
- Windows 거부 시 신규/기존 ledger 상태를 실제 파일로 비교했다. Provider HTTP·callback은 0이다.
  기존 unresolved INTENT를 지우거나 새 작업으로 재사용하지 않는다.
- concurrent fixture는 조기 완료/기한 초과를 구분하고 fatal 경로에서도 release를 보장한다.
  성공 진입 후 join은 callback release 이후다. 실제 Linux lock/fsync·경쟁 상태 검증은 미수행이다.
- 로컬 SSH peer의 shell 프로세스·listener/channel은 기존 fixture cleanup을 사용한다.
  경로 보정은 test peer에만 적용되며 실제 Connector/VM에 shell을 설치하지 않았다.
- 이번 실행은 로컬 임시파일·loopback 서버만 사용했다. 공유 DB·AWS·Kubernetes·Slack 접근/변경 없음.
  이전에 중지한 폐기용 PG server를 재기동하지 않았다. Linux test binary는 `.codex/tmp`에만 보관한다.
- baseline installer xtrace에는 합성 fixture와 로컬 경로가 있다. 실 운영 인증·Secret·외부 응답은 수집하지 않았다.
  원본 실패 trace를 보존하며, 공개 PR 인계 전에는 크기·경로·민감정보를 다시 점검해야 한다.
- 독립 reviewer `/root/review_stage3_regression`는 구현과 분리하여 7개 test diff, baseline GitOps,
  Windows SSH7개·fail-closed2개·syntax/skip 및 세 outer JSON SKIP, 기존 source SHA를 확인했다.
  F-SYNCTEST를 제기했고 수정 후 해결을 재확인했다. 추가 차단 결함은 발견하지 못했다.
  reviewer는 Linux 동작과 부모의 전체 Go 최종 실행을 독립 재실행하지 않았다. 전체 Go는 부모가 실행·집계했다.
  최종 Evidence 재검토에서는 raw JSON의 전체/race 집계와 네 manifest의53개 항목 크기·SHA 일치를 독립 확인했다.
  `created_at_utc`가 로컬 offset이라는 reviewer의 비차단 우려는 원본 문자열이
  `2026-11-21T06:39:26.521992+00:00`임을 확인해 기각했다. PowerShell JSON 파싱 시 현지 시각 변환이었다.

## 재현 및 판정

App root에서 Go1.27.1로 전체 Go·위 두 package race·vet·build를 실행한다.
GitOps root에서 Helm3 및 Python3.12/PyYAML6.0.3을 PATH에 준비한 뒤
`python -m unittest discover -s charts/common-app/tests -v`를 실행한다.
Linux에서는 기존 `.github/workflows/go.yml`의 Ubuntu Go/PG/Valkey job으로 protected fixture와 installer behavior를 실행한다.
이번에 workflow를 실행시키기 위한 push는 하지 않았다.

**요청한 실패 진단과 Windows 실행 가능 범위의 수정·회귀 검증을 마쳤다.** Linux 전용 테스트 실행은 남아 있어
이 결과만으로 Linux CI 전체 PASS, PR merge 준비 또는 Stage3/DR 완료를 판정하지 않는다.
원격 push·PR 수정·merge·배포·실환경 작업·메시지 전송은 수행하지 않았다.
기존 ReplicaLag FAIL, 필수4개 NOT_VERIFIED, execution_gate=HOLD,
recovery_authorized=false, cloud_verified=false를 유지한다.
