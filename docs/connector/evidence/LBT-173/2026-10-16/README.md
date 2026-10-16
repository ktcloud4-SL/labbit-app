# LBT-173 로컬 SSH forwarding 검증

> 아래는 KT 실행 승인 전 로컬 검증 기록이다. 최신 결과는 [KT 새 fixture 11단계 전체 PASS](ktcloud/full-fixture/README.md)와 [최신 audit](ktcloud/full-fixture/AUDIT.md)에 있다. [최초 KT 실패·복구 기록](ktcloud/README.md)도 보존한다.

실행일: 2026-10-16, Windows/amd64, Asia/Seoul. 로컬 브랜치 `codex/feat/LBT-173-ssh-tcp-forwarding`에서 작업했다. Base/HEAD는 `88097be5d9bb6996b718fd185515ca324629fda6`이며 구현은 미커밋 working tree다. 최종 실행 소스는 [source-manifest.json](source-manifest.json)의 원본/LF SHA256과 [source-diff.patch](source-diff.patch)로 고정했다. 원격 push, PR, KT Cloud 실행, Jira 변경은 하지 않았다.

VMAccess의 공유 SSH에서 VM loopback의 승인된 port 하나로 `direct-tcpip`를 연다. Workspace 지정, LabInstance/generation/VM/server, 관리 NIC와 host key pin은 기존 Provider 검증을 거친다. PTY/SFTP와 독립 채널이며 production에서는 실제 SSHForwarder만 배선한다. `preview-v1`은 구현이 배선된 경우에만 선언한다. Backend port allowlist를 복제하지 않는다.

## 결과와 Evidence

| 구분 | 최종 결과 | Evidence |
| --- | --- | --- |
| Go 전체 기본 테스트 | 41 packages PASS, 3652 test/subtest PASS, 16 skip, failure 0 | [요약 및 skip 이유](final-go-test-summary.json), [원본 JSONL gzip](final-go-test.jsonl.gz) |
| Go vet/build | PASS | [vet](go-vet.log), [build](go-build.log) |
| Formatting/runtime | LF 기준 Go 470개 모두 gofmt 일치; Runtime YAML 파싱 PASS | [format](format-validation.log), [runtime](runtime-validation.log) |
| Connector race | VMAccess/Preview/App/Terminal/WorkspaceFile/FileTransport/WSS 7 packages PASS | [race](final-connector-race.log) |
| Web 기본 검사 | install/typecheck/lint/test/build PASS, 14 files/188 tests | `web-*.log` |
| 최종 실제 로컬 OpenStack | 211.62s PASS, 실제 Reset/세대 차단, tracked Lab resource 21항목 부재 확인 | [live](local-openstack.log) |
| 후속 읽기 전용 inventory | images3/flavors4/servers0/networks4 PASS | [inventory](local-openstack-final-inventory.log) |
| 독립 fault 검증 | Close 무응답/취소 후 late success/EOF-only 모두 PASS, PTY 보존; EOF mutant만 예상 FAIL | [독립 리뷰](independent-review/README.md) |

최종 live는 disposable Workspace Provision → VM 내부 `127.0.0.1:3000` HTTP 응답 두 번 → 같은 SSH transport 확인 → Preview 종료 뒤 실제 PTY/SFTP 사용 → 잘못된 네 가지 target와 port/22 거절 → 닫힌 app port 분류 → 실제 Reset gen1→2 → 이전 target 거절/새 VM SSH → exact tracked IDs cleanup 순으로 검증한다. HTTP body와 SSH_CONNECTION, PTY/SFTP 내용은 assertion 중 메모리에서만 사용하며 로그에 저장하지 않는다.

기본 Go 실행의 OpenStack/KT opt-in skip은 live PASS에 포함하지 않았다. 새 Preview 테스트는 별도로 opt-in 실행했다. Windows symlink/open-file rename/POSIX permission 관련 skip은 요약에 그대로 보존했다. PostgreSQL `integration` build tag, 전체 Preview Control/Data WSS 실환경 경로, SaaS Browser C3 및 KT smoke는 실행하지 않았다.

KT smoke는 사용자의 **로컬 브랜치에서만 작업** 지시에 따라 범위에서 제외했다. 로컬은 표준 Nova/Neutron 관리 NIC, KT D1은 NSM Tier/Firewall과 다른 운영 이미지·네트워크 경계이므로 로컬 PASS로 KT를 충족 처리할 수 없다. 전체 LBT-173 완료로 판정하지 않는다. [상세 audit](AUDIT.md)에 추적성과 최종 판정을 기록한다.

## 재현

Go 1.27.1, Node 및 npm, Python/PyYAML이 필요하다. 이번 Windows 환경에는 make/npm 실행 파일이 없어 Makefile의 각 명령을 직접 실행했고 npm은 `pnpm --package=npm@10.9.2 dlx npm`으로 실행했다. Windows race는 mingw-w64 gcc의 `CC`/`PATH` 설정이 필요하다. [reproduce.ps1](reproduce.ps1)은 기본 검사와 선택적 로컬 검증 명령을 담는다. 독립 SSH 장애 주입은 별도 [재현 스크립트](independent-review/reproduce.ps1)를 사용한다.

로컬 실제 검증 전, Connector에서 접근 가능한 **폐기용 로컬 OpenStack** 인증 설정과 Linux 이미지를 준비한다. 이번 입력은 다음과 같다. 인증 파일/개인키 원문은 Evidence에 넣지 않는다.

```powershell
$env:LABBIT_PROVIDER_CONFIG_FILE = '<local clouds.yaml absolute path>'
$env:OS_CLOUD = '<local cloud name>'
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = '<local private key absolute path>'
$env:LABBIT_OPENSTACK_SSH_USERNAME = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_IMAGE = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_FLAVOR = 'm1.small'
$env:LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK = 'sharednet1'
$env:LABBIT_OPENSTACK_TEST_SSH_CIDR = '172.16.8.1/32'
$env:LABBIT_OPENSTACK_TEST_LAB_CIDR = '198.20.0.0/24'
$env:LABBIT_OPENSTACK_PREVIEW_TEST = '1'
go test ./internal/connector/provider/openstack -run '^TestOpenStackPreviewForwardingIntegration$' -v -count=1 -timeout=18m
```

VM에는 Python3/SSH/SFTP가 필요하다. 기존 integration helper가 임시 keypair/관리 SG를 만들고 Lab resource와 별도로 정리한다. 기존 keypair는 `LABBIT_OPENSTACK_TEST_KEYPAIR`로 지정할 수 있으며 이때 helper가 삭제하지 않는다. 공유 관리 network는 유지한다. Lab resource는 추적한 정확한 ID만 삭제하며, 이름으로 소유권을 추정해 삭제하지 않는다.

Runtime YAML은 `yaml.safe_load(Path('runtime/contract.yaml').read_text(encoding='utf-8'))`로 확인한다. `collect_evidence.py`는 클라우드 조작 없이 실행된 Go JSONL의 gzip/집계, 소스 hash/diff, LF 기준 gofmt 결과를 생성한다. Windows checkout의 기존 CRLF 460파일이 raw `gofmt -l`에 나오므로, 일괄 수정하지 않고 Linux CI와 같은 LF 기준으로 검사했다. 변경한 Go 파일은 gofmt를 적용했다.

## 이력과 제한

- `local-openstack-initial-failure.log`: 최초 실행은 fixture가 Reset 결과의 이전 세대 server를 선택해 실패했다. cleanup은 실행했다. generation=2로 필터링한 뒤 재실행했다.
- `local-openstack-before-close-fix.log`: fixture 수정 후 첫 PASS. 이후 channel lifecycle 수정 전 결과이므로 최종 소스의 증거로 사용하지 않는다.
- `local-openstack-before-channel-ack-fix.log`: Close/late-channel 수정 후 PASS. 마지막 EOF/CLOSE 수정 전 결과이므로 최종 증거로 사용하지 않는다.
- `go-test.jsonl.gz` / `go-test-summary.json`, `connector-race.log`, `final-boundary-race.log`: 초기/중간 검사. 과거 working tree 전체 hash는 미보관이다. 최종 판정은 `final-*`와 최종 live를 사용한다.
- Web source/lockfile은 변경하지 않았다. 기존 canvas 미구현 warning과 774.62 kB bundle warning이 있지만 검사는 통과했다.
- SSH CLOSE를 무시하는 peer는 최대16채널을 점유할 수 있으며 이후 요청은 fail closed한다. idle expiry/세대 무효화/SSH 단절/shutdown에서 회수한다. 기존 PTY를 강제로 끊어 용량을 확보하지 않는다. 16은 내부 자원 한도이며 application port 승인 정책이 아니다.
