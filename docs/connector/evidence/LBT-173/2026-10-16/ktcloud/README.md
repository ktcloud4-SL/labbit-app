# LBT-173 KT Cloud standalone forwarding smoke

> 이 문서는 최초 실패·복구 기록이다. 이후 별도의 새 실행으로 [fixture 11단계 전체 PASS](full-fixture/README.md)를 확보했다. [최신 audit](full-fixture/AUDIT.md)와 구분해 읽으며 아래 원래 실패는 그대로 보존한다.

2026-10-16 Asia/Seoul. **두 세대의 forwarding component 검증 PASS, 복구 후 추적 자원 잔여 0. 원래 Control fixture 실행은 final Cleanup 실패로 exit 1이며 전체 PASS로 바꾸지 않는다.** 종합 검토는 [AUDIT.md](AUDIT.md)에 기록한다.

사용자가 KT 실행을 승인한 뒤 수행했다. Git 변경은 `codex/feat/LBT-173-ssh-tcp-forwarding` 로컬 브랜치에만 남겼다. push/PR/merge/Jira 변경/외부 메시지 전송은 하지 않았다.

## 실제 실행 경계

Windows에서 보호된 KT D1 설정으로 인증하고 빈 Server/Volume baseline을 확인했다. Ubuntu 24.04 이미지 `os-ubuntu24.04-image-20260929`, `2x4.itl`(2 CPU/4 GiB), 50 GiB boot volume을 사용했다. 임시 Connector host의 Linux/amd64 실행 파일을 pinned authenticated SSH로 업로드하고 원격 SHA256 세 개를 로컬 빌드와 대조했다. TLS/SSH 검증을 해제하지 않았다.

실제 production Connector child의 HELLO/Heartbeat/Provider Control 경로로 Provision → Reset → Reconcile/Cleanup을 실행했다. SaaS Control peer는 테스트용 검증된 TLS fixture다. Forwarding은 같은 KT Connector host에서 production `SSHForwarder`/`VMAccess` 라이브러리를 직접 호출한 **별도의 component fixture**다. 해당 fixture 안의 HTTP session, PTY, SFTP, TCP는 같은 VMAccess manager를 공유한다. production child의 Preview Data WSS나 Browser 요청을 통과한 것으로 해석하지 않는다.

| 검증 | 실제 결과 |
| --- | --- |
| gen1/gen2 실제 HTTP | 각 세대 VM의 `127.0.0.1:3000`에만 HTTP server. SSH `direct-tcpip`로 200/정확한 body를 각 2회 확인 |
| 공유·격리 | SSH_CONNECTION 메모리 비교, forwarding Close 후 PTY marker와 SFTP Stat 성공 |
| 대상·port·오류 | 4field wrong target, -1/0/22/65536 거절. 3001은 APP_NOT_RUNNING |
| Reset | 구 VM 부재/신 VM 존재. 구 server+gen1 및 신 server+gen1 모두 거절, gen2 HTTP 성공 |
| 사설 관리망 | pinned management SSH, Internet egress deny를 양 세대에서 확인. 앱 포트 공개 정책 추가 없음 |
| 원래 최종 Cleanup | FAILED / ERR_INFRA_OPENSTACK. Reconcile 후 deferred teardown은 남은 3개를 정리하여 SUCCEEDED |
| 원래 child 종료 | 5초 내 정상 종료, forcedKill=false. 원래 journal.passed=false/exit1 보존 |
| 복구/최종 정리 | 아래 순서로 exact-ID 재조회, Lab 10개 부재 확인, bootstrap 8개 정리. 최종 잔여 0, 7종 exact baseline 일치 |

[원래 실행](live-lifecycle.log), [복구 실행](recovery-lifecycle.log), [phase 시간/exit code](phase-results.json), [배포 digest](deployment-digests.log)를 보존했다. 원래 Control 실행은 10:13:19~10:32:02 KST다. 중단된 대화와 관계없이 실행은 종료됐고, 재개 시 상태부터 확인했으며 같은 journal에 Control을 재실행하지 않았다. 최종 baseline 확인은 11:21:34 KST다.

## 실패·복구 기록

1. 기본 OpenStack 설정에는 KT alias가 없어 최초 inventory 준비가 실패했다. 별도 보호된 KT 설정을 찾아 인증/조회에 성공했다. 이 실패에서 자원 생성은 없었다.
2. 첫 bootstrap SSH 확인은 실패했고 48초 뒤 같은 VM 확인이 성공했다. 앞선 오류가 안전한 일반 메시지로만 남아 있어 정확한 실패 원인은 확정하지 않는다. 두 번째 확인은 cloud-init 완료까지 확인했다. 초기 설정 지연으로 단정하지 않는다.
3. 원래 final Cleanup이 거절됐다. 안전한 Provider 오류만 보존해 상세 원인은 미확인이다. 자동 teardown은 fresh Reconcile 후 남은 세 자원을 정리했다. 실패를 성공으로 덮어쓰거나 새 Lab을 만들지 않았다.
4. `collect → residual → recover-observe`에서 10개 모두 부재였다. 그 다음 `recover-cleanup`은 기존 도구 결함 F-KT-001 때문에 빈 known 목록으로 실패했다. 삭제 mutation은 발생하지 않았다.
5. 삭제된 항목도 소유권 journal의 조회 대상에 유지하도록 복구 도구를 수정했다. 회귀 테스트 후 새 acceptance binary를 배포하고 원격 digest를 확인했다. 새 복구는 10개를 재조회하고 다시 부재를 확인했다. 원래 실행의 passed=false는 유지했다.
6. `collect → residual → bootstrap cleanup → residual → final` 완료. [최종 잔여](final-residual.json) 모든 counter 0. [7종 inventory](final-inventory.metadata.json)는 baseline과 ID 집합까지 동일하다. 공유 DMZ/Private/external 3개만 유지했다.

[원래 journal metadata](original-control-ledger.metadata.json)와 [복구 journal metadata](recovered-control-ledger.metadata.json)는 별도다. 자원 ID는 SHA256으로 대응시키며 credentials/IP/host pin/raw payload를 공개하지 않는다. 전체 원본과 민감한 입력은 사용자 전용 ACL의 보호된 로컬 fixture 디렉터리에 보존했다.

## 실행 소스와 검사

Base/HEAD `88097be5d9bb6996b718fd185515ca324629fda6`, 미커밋 로컬 변경이다.

- 실제 양 세대 smoke: [live-source-manifest](live-source-manifest.json), [live diff](live-source-diff.patch), [live binary manifest](live-binary-manifest.json). acceptance SHA256 `39151619e99af820fe9dbb61142d9a7198af57037b0e9ca7a1be37de75922dcd`.
- 복구 수정 후 최종 소스: [source-manifest](source-manifest.json), [diff](source-diff.patch), [binary manifest](binary-manifest.json). 새 acceptance SHA256 `d3488698b02315a82e367b4fc99b62932231700156c2284edccb2e68c76f5cd8`. production Connector/bootstrap/helper binary는 동일하다.
- [live→final 차이](live-to-final-source-differences.json)는 recovery용 Go 3파일뿐이다. [로컬 audit 대비](format-and-local-alignment.json) production/runtime/contracts/web 차이 0. 원래 smoke를 수정 후 전체 fixture 재실행으로 표시하지 않는다.
- 최종 `go test ./...`: 41 packages/3671 tests(subtests 포함) PASS, 16 tests skip, failure 0. 12 package skip은 Go test 대상 파일이 없는 package다. [집계/skip 목록](go-test-summary.json), [원본 gzip](go-test.jsonl.gz). 원래 smoke 시점의 별도 [집계](live-go-test-summary.json)/[원본](live-go-test.jsonl.gz)도 보존했다.
- `go vet ./...`, 두 tools package race, recovery 회귀 PASS. [race](tools-race.log), [회귀](recovery-fix-test.log). canonical LF Go 473파일 format 차이0; raw CRLF 차이456개는 기존 checkout이다.
- 앞선 production 7package race/로컬 실제 OpenStack/Web 검증은 [상위 Evidence](../README.md)를 참조한다. 관련 production와 Web source 불변을 hash로 확인했으며 KT 실행을 Web/C3 재검증으로 해석하지 않는다.
- 최종 조회에서 main이 `6755fc15062446b4bf0931e8e8be03efdb2563ef`로 진전했다. PR #103은 C2 loopback test와 문서 3개만 추가/수정하고 production/SSOT는 불변이다. 새 test를 고정한 외부 Go overlay로 현 feature와 함께 실행해 PASS했다([결과](latest-main-check.json), [원본](latest-main-c2-test.jsonl), [재현](check-latest-main.ps1)). 실제 브랜치를 rebase하거나 전체 최신 main 통합 suite를 실행했다고 주장하지 않는다.

## 로컬↔KT 환경 차이와 한계

로컬은 표준 Neutron network/subnet/port/SG와 로컬 실행 환경이다. KT D1은 Nova/Cinder + NSM Tier/Firewall, Connector host의 private `/32`와 별도 management Tier를 사용한다. Lab CIDR `172.25.240.0/24`, management CIDR `172.25.241.0/24`는 fixture 입력이다. IPv4 loopback destination과 target validation/SSH host pin/shared-channel library는 공통이다. KT final Cleanup 거절은 로컬 최종 run에서 나타나지 않았으며 원인 미확인/복구 필요를 그대로 기록한다.

timeout/비응답 peer/host pin 실패/오프라인 경로는 자동 in-process SSH tests로 검증했다. KT에서 네트워크 장애·host key 교체를 주입하지 않았다. SFTP Stat/PTY marker는 실제지만 production File API 전체 upload/download는 이번 smoke 대상이 아니다. Preview Control/Data lifecycle, 실제 SaaS Browser/Gateway C3는 LBT-173 범위 밖 후속이다. 원래 Control fixture의 일괄 PASS는 확보하지 못했으며 standalone forwarding PASS와 복구 후 cleanup을 구분한다.

## 재현

기존 [bootstrap 도구 README](../../../../../../tools/ktcloud-op02-bootstrap/README.md)의 보호된 새 디렉터리/명시적 gate/빈 baseline 절차를 따른다. 현재 소스에서 Windows bootstrap와 Linux/amd64 Connector/acceptance/helper를 빌드하고, 해당 출력과 credentials/키는 Git 밖 보호된 디렉터리에 둔다. `LBT173_BOOTSTRAP_PREVIEW_SMOKE=1`, `LBT145_CONTROL_GENERATIONS=2`를 `deploy` 전에 설정하면 OP02=0/Preview=1을 배포한다. 잘못된 mode 혼합은 journal 생성 전 거절한다.

저장소 루트/Windows PowerShell에서 `$fixtureDir`를 사용자 전용 ACL의 **새** 절대 경로로 먼저 준비한다. 실제 빌드 명령은 다음과 같다. 각 명령의 exit code를 확인하고 SHA256 manifest를 보존한다.

```powershell
go build -o (Join-Path $fixtureDir 'bootstrap.exe') ./tools/ktcloud-op02-bootstrap
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -o (Join-Path $fixtureDir 'labbit-connector-ktcloud') ./cmd/labbit-connector
go build -o (Join-Path $fixtureDir 'lbt145-control-linux') ./tools/ktcloud-d1-acceptance
go build -o (Join-Path $fixtureDir 'labbit-workspace-sftp') ./cmd/labbit-workspace-sftp
```

`LABBIT_PROVIDER_CONFIG_FILE`에는 보호된 KT 설정 경로, `OS_CLOUD=ktcloud-d1`, `LBT145_BOOTSTRAP_LEDGER`/`LBT145_BOOTSTRAP_KEY`에는 새 fixture 내부 절대 경로를 사용한다. 필수 gate와 phase별 실행은 bootstrap README의 명령을 사용하고 위 Preview gate를 추가한다. config에는 검증된 D1 endpoint/credentials/CA trust가 필요하며 실행 전에 두 fixture CIDR의 미사용을 확인한다. 키/host pin은 fixture가 생성·등록하며 shared Tier를 삭제하지 않는다.

정상 단계는 inventory/create/access/ssh/deploy/control/collect/residual/cleanup/residual/final이다. 실패 시 같은 journal로 control을 반복하지 않는다. collect/residual 후 recover-observe, recover-cleanup, collect/residual을 수행하고 소유 ID 부재 확인 뒤 bootstrap cleanup/residual/final을 완료한다. recovery는 두 smoke mode를0으로 덮어써 fresh Provision/Reset을 막는다. 결과불명 receipt는 재생성하지 않는다. [capture.py](capture.py)는 source/hash/test 요약만, [export_metadata.py](export_metadata.py)는 보호된 fixture의 검토 가능한 metadata만 추출하며 cloud를 변경하지 않는다.
