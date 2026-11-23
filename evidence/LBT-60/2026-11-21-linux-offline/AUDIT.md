# 2026-11-21 Linux 전용 테스트 오프라인 실행 검토

## 목적·기준·전체 변경

사용자 요청은 이전 Windows 회귀 검사에서 실행하지 못한 Linux 전용 테스트를 AWS 없이 실행하는 것이다.
실제 환경 변경·push 금지는 유지한다. 사용자 `PRE_MERGE_AUDIT.md` 전체와 App AGENTS/TESTING/CONTRIBUTING을 읽었다.
제품/테스트 코드와 기존 Stage3 구현은 이번에 수정하지 않았다. 새 파일은 이 Evidence 폴더의 실행 기록·재현용 helper뿐이다.

- App base/HEAD: `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac`.
- Branch: `codex/feat/LBT-60-stage3-offline-20261121`, 미커밋 후보.
- 직전 회귀 snapshot의 변경 source29개 모두 size/SHA256 불변(`source-comparison.json`).
  전체 변경 목록과 선행 요구사항 추적은 `../2026-11-21-regression-diagnosis/source-manifest.json` 및 두 이전 AUDIT를 따른다.
- 실행 payload에는 처음99개, 두 번째103개 allowlist source 파일을 raw bytes 그대로 복사했다.
  실행 파일은 Go1.27.1, GOOS=linux, GOARCH=amd64, CGO_ENABLED=0으로 Windows에서 cross-compile한 ELF다.
  **그 ELF를 실제 Linux kernel에서 실행했으며, 이번 실행은 race build가 아니다.**
- 원래 PR196 작업 디렉터리와 사용자 미커밋 수정, GitOps·Infra는 이번에 변경하지 않았다.
  현재 main/배포분 전체를 새로 인수한 결과가 아니다.

## 원문·요구사항 추적

이번 회차에 [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205) 본문(최종 갱신11/20)과
[Confluence 테스트 전략9666637 v4](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637)를 다시 조회했다.
로컬/CI fixture·실제 OpenStack·AWS DR 인수의 분리, 과거 FAIL 보존 및 HOLD 유지와 충돌하지 않는다.
기존 HTTP/Connector/Realtime/Runtime/DB 계약은 변경하지 않았고,
계약 producer/consumer 검토와 앞선 Stage3 전체 diff 검토는 이전 audit를 승계한다.

| 요구·SSOT | 구현·검증 대상 | 실행 Evidence | 남은 공백 |
| --- | --- | --- | --- |
| TESTING: Linux 보호 원장과 실제 Provider 구분 | `ktcloud_d1_cidr_test.go`, readiness/failed_recovery 및 전체 OpenStack fixture | `go-openstack.jsonl` 645PASS/15SKIP, 실제 Linux filesystem·loopback HTTP/SSH | 실제 Provider 통합14개 미실행. 디스크 장애·재부팅 내구성 증명 아님 |
| 보호 원장의 isolation·UNKNOWN·동시성·기존 intent 보존 | `TestKTCloudCIDRPoolConcurrentAndUnknownMutation` 및 CIDR lifecycle/rejection/cleanup | callback 진입/경합·UNKNOWN 보존 실제 실행 PASS. 이전 timeout 수정의 Linux 경로 포함 | 실제 cloud mutation/전역 DR 잠금·Writer 격리 미검증 |
| Linux readiness·가상 시간 테스트가 실제 실행돼야 함 | late ACTIVE, readiness diagnostic, management-route outer test3개 | 모두 실제 PASS. Windows에서 SKIP된 경로를 Linux에서 실행 | 실제 VM readiness·업무 RPO와 무관 |
| Installer 권한·안전 재사용·실패 분기 유지 | `installer_test.go` integrity/runtime/syntax/behavior, `scripts/installer/install_test.sh` | `go-installer-final.jsonl` 5PASS/0SKIP. helper 재사용·중단·보존·변조 거부 등 원본 suite | systemctl·curl·계정 생성은 fixture/mock. 운영 Ubuntu/systemd 배포 시험 아님 |
| Linux 관리 경로 생성·readonly 검증·실패 진단 | `ktcloud_management_route_guest_test.py` | `python-normal.log`, `python-optimized.log`, 각각10/10PASS | Netplan·systemd·ip는 mock. 실제 guest NIC/route 변경 없음 |
| Linux symlink·credential0600 | `TestSymlinkRejectedAndExcludedFromTree`, `TestInstallCredentialFile` | `go-workspacefile.jsonl` 1PASS, `go-credential.jsonl` 5PASS | 선택된 두 Test 범위만. 실제 사용자 credential/ACL 인수 아님 |
| 오프라인 범위·실제 환경 변경 금지 | QEMU noNIC/noShare/noDisk, allowlist payload, 빈 guest 인증환경 | `environment.json`, payload/download/harness manifest, serial boot·종료 원본 | AWS·공유 DB·Kubernetes·Slack 조회/변경 미수행 |

모든 수치는 Go Test terminal event(parent/subtest 포함) 또는 unittest test case 기준이다.
package 이벤트나 초기/재실행을 더해 고유 테스트 수처럼 표시하지 않는다.

## 실행 환경·격리·자료 무결성

WSL 배포판과 Docker/Podman은 없었다. 기존 GNS3 폴더의 QEMU를 별도 프로세스로 실행했다.
폴더명은 qemu-3.1.0이지만 실제 `--version`은 **2.11.0 dirty build**다.
host binary·kernel·image·source·test binary size/SHA는 manifest에 기록한다.

- Guest: Alpine3.23.8 / Linux6.18.60-0-virt / bash5.3.3 / Python3.12.16 / GNU coreutils9.8.
- QEMU TCG, CPU2개, RAM1536MiB, `-nodefaults -no-user-config -display none -monitor none -serial stdio -net none`.
- host disk/공유 디렉터리/QMP/관리 포트/NIC 없음. guest에는 loopback `lo`만 있으며 테스트 peer도 loopback이다.
- initramfs RAM rootfs와 `/tmp` tmpfs만 사용. Windows 기능·설치 서비스·레지스트리·WSL 변경 없음.
- `.aws`, kubeconfig, clouds.yaml, 실제 Secret·계정 인증파일·host 환경변수는 복사하지 않았다.
  원본 source와 합성 fixture만 경로 allowlist로 복사했다. cloud opt-in 변수는 설정하지 않았다.
- 공식 [Alpine 배포처](https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/x86_64/)의 rootfs 공개 SHA256을 대조했다.
  kernel/APK는 공식 HTTPS로 수신하고 raw SHA256을 기록했다. **kernel/APK publisher 서명 검증을 했다고 주장하지 않는다.**
- 재현용 kernel/APK/binary/image는 `C:/Users/user/.codex/tmp/stage3-linux-20261121`에만 남긴다.
  Evidence에는 해시와 재현 helper를 보관하며, 해당 대형 binary/image를 Git에 추가하지 않는다.

## 최초 실패 및 해결·재실행

제품 코드의 새 결함은 확인되지 않았다. 초기 guest 환경 준비에서 다음 문제가 있었다.

| 분류 | 위치·재현 | 영향 | 조치·회귀 |
| --- | --- | --- | --- |
| E-SETUP / P3, 시험 환경 사전조건 | `harness/prepare_linux.py` 최초 package 목록에 curl 누락; installer `install.sh:139` | behavioral suite Test3이 lock 검사 전에 required utility 오류로 종료 | 수정 전 사용자에게 원인·영향·대응을 알렸다. guest용 실제 curl과 종속 라이브러리만 보완해 suite 전체 재실행. PASS |

- Attempt01: OpenStack645PASS/15SKIP, Installer3PASS/2FAIL(behavior와 parent), Python10PASS + -O10PASS.
  `qemu-attempt01.log`와 `go-installer-initial.jsonl`에 원본 FAIL 보존. QEMU exit0은 테스트 전체 성공을 의미하지 않는다.
- Attempt02: curl이 있는 새 guest에서 Installer5PASS, symlink1PASS, credential5PASS. 세 section exit0.
  이미 통과한 OpenStack·Python은 재실행하지 않았다. 다른 artifact로 교체하지 않고 회차를 구분한다.
- 최종 각 실행은 `@@BEGIN/END_*@@`, section exit code, guest result tuple, 버전/네트워크·hash와
  자연 powerdown을 함께 확인했다. `init.sh`는 setup fail-fast가 아니므로 QEMU exit0이나 READY marker만으로 PASS 처리하지 않는다.

## 결과·SKIP·미실행

| 실행 | PASS | FAIL | SKIP |
| --- | ---: | ---: | ---: |
| Linux OpenStack package 전체 fixture | 645 | 0 | 15 |
| Linux Installer package 전체 | 5 | 0 | 0 |
| Linux WorkspaceFile symlink 선택 Test | 1 | 0 | 0 |
| Linux credential 생성 선택 Test | 5 | 0 | 0 |
| Linux Python 관리 경로 일반 모드 | 10 | 0 | 0 |
| Linux Python 관리 경로 -O | 10 | 0 | 0 |

15개 SKIP는 실제 OpenStack/VM/네트워크 opt-in 통합14개와 Windows 전용 negative test1개다.
Linux 보호 원장·권한·readiness 경로는 SKIP되지 않았다. 정확한 목록은 `test-summary.json`에 있다.
이전 Windows race566PASS/80SKIP, 전체 Go4527PASS/88SKIP, 앞선 폐기용 PG·GitOps·Web 결과는
이전 audit의 해당 source·범위에 대한 결과로 유지한다. 이번에 Linux 전체 Go·Linux race·PostgreSQL/Valkey·Web을 실행한 것은 아니다.

## 실패·보안·수명주기 및 독립 리뷰

- 보호 권한·fsync·symlink·INTENT·UNKNOWN 보존 기대값과 제품의 fail-closed 검사를 바꾸지 않았다.
- 모든 provider/VM/sysadmin 작업은 fixture 경계다. 실제 OpenStack credentials·host systemd·네트워크 설정은 사용하지 않았다.
- RAM filesystem의 Linux fsync 성공은 syscall·논리 흐름 검증이다. 물리 디스크 flush·전원 장애 내구성이나
  실제 여러 리전의 Single Writer를 입증하지 않는다.
- Guest의 임시 파일·local listener·dummy key는 VM RAM 수명에 한정된다. 두 guest가 각각 자연 powerdown했고
  종료 후 QEMU 프로세스가 남지 않은 것을 확인했다. host scratch는 재현을 위해 보존하며 상시 비용 자원은 없다.
- raw 로그에는 테스트용 합성 credential·dummy 명령이 있을 수 있으나 실 운영 Secret·세션·terminal 내용을 수집하지 않았다.
- 독립 reviewer `/root/review_stage3_linux`가 구현과 분리해 harness·allowlist·source99개·binary3개·첫 cpio1566개 항목과
  embedded source/binary102개를 대조했다. 소스 바이트를 보존했고, 네트워크·공유 mount가 없는 실행 범위를 확인했다.
  초기 raw 로그의645PASS/15SKIP, Python10+10PASS, Installer FAIL/section exit1 및 kernel/tool/lo-only 조건도 독립 확인했다.
  최종103개 source·5개 binary와 두 번째 cpio1597개 항목, init bytes, Artifact18개·helper6개,
  image/kernel/QEMU 해시와 최종 집계·marker까지 재확인했다. 추가 차단 결함은 발견하지 못했다.
  실제 cloud·운영 OS 검증은 reviewer도 수행하지 않았다.

## 재현과 판정

`environment.json`의 QEMU 인자, 두 payload manifest, `harness/`의 init/build/prepare/finalize helper와
download manifest의 고정 URL·SHA를 사용한다. 현재 scratch에는 사용한 archive가 보존돼 있다.
다른 checkout에서는 `build_image.py`·`finalize_evidence.py`의 고정 Windows `APP` 경로를 해당 checkout으로 조정해야 한다.
새 machine에서 APKINDEX를 다시 받으면 package version이 달라질 수 있으므로, 원본 재현은 기록한
download manifest의 정확한 archive와 SHA를 우선한다. cross-compile은 각 package의 `go test -c`,
`cmd/test2json`의 Linux build이며 guest 내 해당 package 디렉터리를 CWD로 실행했다.

**요청한 Linux 전용 로컬 fixture 실행 범위는 통과했다.** 이번 source 변경은 Evidence·재현 helper뿐이며
전체 Stage3/PR merge 준비·실제 DR 완료를 판정한 결과가 아니다.
push·merge·배포·AWS/공유 서비스 작업·Slack 전송은 하지 않았다.
기존 ReplicaLag FAIL, REQUIRED_SETTINGS/RUNTIME_PROVENANCE/SERVICE_FUNCTIONS/SINGLE_WRITER NOT_VERIFIED,
execution_gate=HOLD, recovery_authorized=false, cloud_verified=false를 유지한다.
