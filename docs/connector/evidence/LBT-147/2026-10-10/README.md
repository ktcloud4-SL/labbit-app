# LBT-147 · 로컬 OpenStack VM 접속 검증

Jira: [LBT-147](https://samsunglions.atlassian.net/browse/LBT-147).
실행일: 2026-10-10 KST. Branch: `feat/LBT-147-vm-access`.
Base main: `6d675c74bff11f481d247267faefd4ce960dcd38`.
Tested source: `b0b5686d010f5c247ac007b76d373fbec109ea0f`.
이 디렉터리를 추가하는 후속 commit은 evidence-only이며, tested source 이후 구현·테스트·계약·의존성 변경은 없다.

사용자 요청에 따라 기존 고객 로컬 OpenStack을 사용했다. KT Cloud D1/LBT-145 완료 또는 KT Cloud 호환성을 이 기록으로 주장하지 않는다. 실행 환경은 Windows, Go 1.27, CGO/GCC, bundled Node/pnpm/Python이다. 개인키·clouds.yaml은 고객 로컬 파일에서 읽었고 저장소에 추가하지 않았다.

## 요구사항과 검증

| LBT-147 검증 대상 | 관찰한 결과 | 자동 검증 위치 |
| --- | --- | --- |
| 인증된 LabInstance/generation/VM key/Server 일치 | Provider 소유권·metadata 및 기존 Management NIC 검증. 잘못된 lab, generation, VM key를 SSH 전에 거절 | `terminal_target_test.go`, `TestOpenStackVMAccessIntegration` |
| 기존 Management private IP·로컬 key·Server별 host key 검증 재사용 | 실제 gen1/gen2 VM에 public-key 인증 및 pinned host-key로 접속 | `TestAuthenticationHostKeyAndPrivateKeyFailuresAreSafe`, 실환경 테스트 |
| VM별 lazy shared SSH | 동일 VM의 여러 채널이 한 연결을 공유. 동시 12개 open은 handshake 1회 | `TestConcurrentChannelsShareOneHandshake`, 실환경 SSH identity 비교 |
| PTY/SFTP 채널 소유권 분리 | 실제 PTY input/output/resize 및 SFTP INIT/VERSION. PTY 종료 뒤 SFTP REALPATH 응답 유지 | `TestSharedTransportIndependentChannelsAndIdleExpiry`, 실환경 테스트 |
| 활성 채널이 없을 때 configurable idle 종료 | 활성 채널 동안 유지, 마지막 close 이후 transport 종료, 다음 요청에서 새 연결 | unit + 실제 SSH identity 변경 |
| generation/target 변경·Reset/Cleanup | 이전 transport 폐기, 지연된 resolve/channel setup 거절, Reset gen2의 새 Server 접속 및 gen1 거절 | vmaccess cancellation/target tests, `TestOperationResultInvalidatesObsoleteAccess`, 실환경 테스트 |
| 실패·취소·단절 정리 | 인증/host key/key 파일/unreachable/handshake·channel setup timeout 및 cancel 후 정리. 원격 단절 후 요청 전까지 자동 재접속 없음 | vmaccess tests + race 검사 |
| 외부 오류 민감정보 차단 | Control OPEN 실패 및 Control/Data ENDED에 raw SSH error가 아닌 안전한 message만 전달 | `TestTerminalControlRedactsRawSSHFailures`, Terminal close tests |

Runtime 설정의 owning SSOT는 `runtime/contract.yaml` 0.1.7이다. `LABBIT_OPENSTACK_SSH_IDLE_TIMEOUT`은 기본 `1m`인 양수 Go duration이며 TerminalSession grace와 별개다. 제한된 channel setup 중 취소/timeout이 발생하면 x/crypto/ssh의 context 없는 요청을 해제하기 위해 공유 transport를 폐기한다. 정상적인 채널 종료는 다른 채널을 닫지 않는다.

## 실행 결과

최종 결과와 source SHA는 [validation-summary.json](validation-summary.json), Go·계약·실환경 원본 로그는 [automated-logs.zip](automated-logs.zip), 변경 source blob은 [source-manifest.json](source-manifest.json)에 있다. Web은 앞서 실행한 전체 검증 결과를 summary에 기록했으며 별도 원본 로그는 포함하지 않는다. [secret-scan.json](secret-scan.json)은 변경 source/evidence의 로컬 민감값 일치 검사 범위를, [artifact-checksums.json](artifact-checksums.json)은 evidence 파일 SHA-256을 기록한다.

- 전체 Go format/vet/unit/build PASS. Go 테스트 2,792개, 변경된 5개 package race 테스트 328개 PASS(subtest 포함).
- socket deadline이 context timer보다 먼저 처리되는 타임아웃 회귀: race 포함 20회 PASS.
- Web typecheck/lint/build 및 13개 파일 163개 테스트 PASS. Web source와 dependency 변경 없음.
- OpenAPI, JSON Schema 8개, Runtime/Migration metadata, Reset schema 11개 테스트 PASS.
- 실제 Provision gen1 → authenticated shared SSH/PTY/SFTP → idle 종료 → Reset gen2 → 새 SSH 및 stale gen1 거절 PASS.
- 두 generation에서 추적한 고유 리소스 14개는 cleanup 후 모두 ABSENT. 테스트용 keypair/Management SG는 별도의 fixture cleanup에서 삭제한다. 최종 read-only discovery의 servers=0을 확인한다.

Windows에 make/npm이 없어 `make setup`/`make test`의 Go 및 Web target을 직접 실행했다. Web install은 기존 package-lock에서 임시 pnpm lock을 import해 실행했고 임시 lock은 제거했다. 일반 Go suite에서 opt-in cloud tests는 SKIP이며, 위 실환경 테스트와 post-cleanup discovery는 별도 opt-in 실행으로 PASS를 확인했다. DB schema/transaction 변경은 없으며 PostgreSQL integration은 실행하지 않았다.

최초 실환경 시도는 Reset 결과에 포함된 gen1 Server를 test harness가 선택해 실패했다. 생성한 리소스는 정리했으며 generation 2 필터로 수정한 후 통과했다. 최종 전체 Go 검사에서는 socket/context timer의 실행 순서에 따른 timeout 분류 누락을 발견해 구현과 회귀 테스트를 수정했다. 최종 PASS 기록은 수정된 tested source 기준이다.

범위는 LBT-147의 VM 접속 관리다. 실제 SFTP probe는 채널 독립성 검증이며 production File 명령은 LBT-149 범위다. SaaS/Browser end-to-end Terminal 또는 Live 전체 완료를 주장하지 않는다. Jira 상태 변경, remote push, PR 생성은 하지 않았으며 사용자 검토 후 진행한다.

## 재현

1. [reproduce.ps1](reproduce.ps1)을 repository 밖에 저장한다.
2. tested source SHA를 깨끗한 checkout에서 선택한다.
3. 동일 VM image/flavor/Management network가 있는 로컬 OpenStack과 matching 고객 로컬 SSH key를 준비한다.
4. repository root에서 아래를 실행한다. 테스트는 두 VM generation을 생성하고 추적한 리소스를 정리한다.

```powershell
& '<repository 밖의 reproduce.ps1>' `
  -ProviderConfigFile '<customer-local clouds.yaml>' `
  -Cloud '<local cloud name>' `
  -SSHPrivateKeyFile '<customer-local private key>'
```

환경별 선택값은 `LABBIT_OPENSTACK_TEST_IMAGE`, `LABBIT_OPENSTACK_TEST_FLAVOR`, `LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK`, `LABBIT_OPENSTACK_TEST_SSH_CIDR`, `LABBIT_OPENSTACK_TEST_LAB_CIDR`로 주입한다. 구체적인 defaults와 cleanup assertions는 `vm_access_integration_test.go` 및 기존 integration helper가 SSOT다. Secret과 Terminal/SFTP 출력은 evidence에 기록하지 않는다.
