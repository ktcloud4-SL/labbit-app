# LBT-149 Workspace SFTP — 2026-10-14

구현 source `a495184e13a159c0cca25256dd8d0766da83283e`, base/main `0599947407e9edd4539690e2f56eecf0622909ce`. [상세 감사](AUDIT.md), [독립 리뷰](independent-review.md), [요구사항 버전](requirements.json), [소스/바이너리 연결](source-manifest.json), [검증 요약](validation-summary.json), [원본 archive](validation-logs.zip), [SHA256](artifact-checksums.json), [민감정보 검사](secret-scan.json).

실제 local OpenStack Linux VM에서 production rooted SFTP Service의 Tree/Read/Save와 path/size/revision/permission 실패, shared SSH PTY 보존, Linux fd-root/rename/symlink/flock/Tree budget, kernel EFBIG 오류 전달과 unchanged 재Read를 검증했다. 마지막 실제 acceptance는123.27초 PASS이며 Provision7개 exact tracked absence를 확인했다. 이후13:08KST read-only inventory의 임시 keypair/SG 및 lbt149 Server/Network/Subnet/Port는 모두0이다. 실제 File WSS/SaaS/Browser는 후속 LBT-150/LBT-22 범위이며 실행하지 않았다.

Go 최종 source의 전체3496pass/14skip, 관련 race, lifecycle20회 반복과 Workspace race를 실행했다. Web13파일/167테스트·typecheck/lint/build 및 최초 Contracts는 이전 c753에서 통과했고 a495의 Go 오류분류 수정4파일이 이 경계의 source를 변경하지 않았음을 manifest에 기록했다. 마지막 runtime EOF 공백1줄 정리 후 current 후보에서 Contracts를 다시 PASS했고 두 runtime bytehash와 parsedYAML 동등성을 기록했다. Linux 전용 tests는 실제 VM binary에서 실행했다. Windows symlink test skip과 opt-in cloud skips를 Mock/live PASS로 섞지 않는다. 초기 live 설치실패, storage classifier expectedred 및 앞선 c753 실행도 raw archive에 보존했다. 첫 loopback immediate reconnect 기대 실패의 raw는 chat tool output만 남아 있다는 제한은 감사에 있다.

## 재현

Go1.27/GCC(race)/Git/Python3.12를 PATH에서 찾을 수 있어야 한다. Web은 Node와 pnpm(또는 기본 pnpm)을 사용하며 tracked npm lock의 임시 import/frozen install adapter를 쓴다. `go mod download`로 Go 의존성을 준비한다. Contracts validator pins는 현재 workflow와 같다.

```powershell
python -m pip install PyYAML==6.0.3 jsonschema==4.26.0 openapi-spec-validator==0.9.0
./reproduce.ps1 -Stage auto
./reproduce.ps1 -Stage web -NodeBin '<Node bin>' -PnpmExecutable '<pnpm.cmd>'
./reproduce.ps1 -Stage contracts
```

별도 Python이나 validator directory는 `-PythonExecutable`/`-ValidatorDir`로 주입한다. Windows make 부재를 portable runner가 동일 targets로 대체한다. baseline helper는 같은 저장소의 tracked `LBT-148/2026-10-14/validate.py`이며 개인 미추적 script에 의존하지 않는다. 모든 stage는 상속된 cloud mutation/integration/audit opt-in을 먼저 끈다.

실제 stage는 준비된 **폐기용 local OpenStack**에서 VM·Network·fixture keypair/SG 생성, sudo helper 설치, fixture files 변경 및 Cleanup을 수행한다. 일반 자동 테스트에는 포함되지 않는다. 실제 key/cloud credential 내용은 로컬 파일로 준비하며 repo에 넣지 않는다. Linux amd64 helper와 Linux regressions binary는 runner가 현재 source에서 자동 빌드한다.

```powershell
$env:OS_CLOUD = '<configured disposable cloud name>'
$env:LABBIT_PROVIDER_CONFIG_FILE = '<local clouds.yaml>'
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = '<local SSH private key>'
$env:LABBIT_OPENSTACK_SSH_USERNAME = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_IMAGE = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_FLAVOR = 'm1.small'
$env:LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK = 'sharednet1'
$env:LABBIT_OPENSTACK_TEST_SSH_CIDR = '<Connector source CIDR>'
$env:LABBIT_OPENSTACK_TEST_LAB_CIDR = '198.20.0.0/24'
./reproduce.ps1 -Stage live
./reproduce.ps1 -Stage residual
```

실행한 비민감 profile은 `labbit-test`, ubuntu/m1.small/sharednet1, SSH source CIDR172.16.8.1/32, Lab subnet198.20.0.0/24다. 이 CIDR은 재현자의 환경에 맞춰 설정한다. fixture는 ubuntu 계정/passwordless sudo를 사용한다. helper/root 설치 기준은 [운영 안내](../../../workspace-sftp.md)를 따른다. known_hosts는 fixture의 임시 directory, 새 Provision이 enrollment하며 요청 때 pin을 검증한다. helper는 SSHexec stdio이며 새 inbound listener가 없다.

`residual`은 tracked resources의 exactID 부재 조회에 더해 integration prefix keypair/ManagementSG 및 lbt149 이름 prefix의 Server/Network/Subnet/Port를 조회하는 보조 점검이다. 현재 prefix 자원이 있는 다른 동시 테스트가 있다면 실패할 수 있다. 자원을 삭제하지 않는다. output directory는 repo 밖의 새 temporary directory가 기본이다. raw binary·credential은 archive에 넣지 않고 binary SHA256과 build metadata/source snapshot을 보존한다. TCP blackhole·실제 ENOSPC/EIO/EDQUOT·Save중packetloss·wholefilesystem atomicity는 이번 검증 밖의 한계이며 상세 감사에 적었다.

감사 통과와 local commit은 push·PR 생성·merge·배포·Jira 변경·외부 메시지 승인이 아니다.
