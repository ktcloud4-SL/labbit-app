# Workspace SFTP 운영 경계 (LBT-149)

요구사항은 [LBT-149](https://samsunglions.atlassian.net/browse/LBT-149), 공유 SSH 결정은 [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730), 외부 파일 계약은 [Connector 계약 §7a](../../contracts/connector/README.md), 설정은 [Runtime Contract](../../runtime/contract.yaml)을 따른다. 이 단계는 Connector 내부 Tree/Read/Save 서비스이며 File Control/Data WSS 연결과 `file-v1` capability는 LBT-150에서 조립한다.

`ConnectorApp.WorkspaceFiles`는 두 Runtime 환경변수가 설정된 경우에만 구성된다. `LABBIT_WORKSPACE_ROOT`와 `LABBIT_WORKSPACE_SFTP_HELPER`는 같은 VM의 canonical absolute POSIX 경로다. 예시 root는 `/var/lib/labbit-workspaces/workspace`, helper는 `/usr/local/libexec/labbit-workspace-sftp`다. 하나만 설정하거나 canonical 경로가 아니면 Connector 조립에 실패한다. 설정 자체는 Provider 인증을 실행하지 않는다.

## Linux VM 준비

일반 SFTP subsystem에 경로 검사를 추가하는 방식은 검사와 open 사이의 symlink 교체에 취약하다. 전용 helper는 SSH exec 채널의 stdin/stdout으로 SFTP protocol을 제공하며 `os.Root`의 디렉터리 fd로 경로 해석을 제한한다. VM에는 새 listener나 daemon이 생기지 않는다. 작업마다 helper process/독립 채널을 열고 종료하며, PTY와 같은 검증된 SSH transport를 사용한다. 표준 subsystem의 임의 filesystem 접근으로 자동 fallback하지 않는다.

VM 아키텍처에 맞는 Go 1.27 Linux 바이너리를 빌드한다. 다음은 amd64 예시다.

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o labbit-workspace-sftp ./cmd/labbit-workspace-sftp
```

이미지 제작 또는 승인된 VM bootstrap에서 관리자가 바이너리를 설치한다. `ubuntu`는 실제 SSH 계정으로 바꾼다. 이 계정으로 helper를 실행하므로 파일 권한도 해당 계정의 Linux 권한이다.

```sh
sudo install -d -o root -g root -m 0755 /usr/local/libexec /var/lib/labbit-workspaces
sudo install -o root -g root -m 0755 labbit-workspace-sftp /usr/local/libexec/labbit-workspace-sftp
sudo install -d -o ubuntu -g ubuntu -m 0750 /var/lib/labbit-workspaces/workspace
```

helper와 모든 helper 상위 디렉터리, Workspace root의 모든 상위 디렉터리는 root 소유이며 group/other 쓰기 권한이 없어야 한다. symlink 상위 경로 및 root `/`는 거부한다. Workspace root 자체와 내부 regular files는 SSH 계정이 접근·수정할 수 있다. helper는 실행 시 설치 경계를 다시 검사한다. 사용자 writable `/home/ubuntu`에 helper나 root anchor를 설치하면 거부된다. root의 mount·ACL 관리와 관리자 권한 보안은 VM 운영자가 책임진다. Linux `/proc/self/fd`가 필요하며 Windows helper 실행은 지원하지 않는다.

Provision/Reset은 CreationSnapshot의 `workspaceVmKey`를 Nova의 `labbit_workspace_vm_key` metadata로 기록한다. File 요청마다 지정 Workspace 여부를 조회하고 VMAccess가 LabInstance/generation/project/NIC/SSH host-key pin을 다시 검증한다. 이전 VM에 지정 metadata가 없으면 File 접근을 거부한다. 기존 VM을 임의로 지정하지 말고 현재 Provision/Reset 경로로 재생성한다. Terminal 기존 접근에는 이 지정 조건을 추가하지 않는다.

## 동작과 실패 의미

- 빈 경로는 Tree root에만 허용한다. 나머지는 canonical 상대 POSIX 경로이며 decode/normalize하지 않는다. symlink 및 특수 파일은 Tree에서 제외하고 접근은 거부한다. Tree는 직접 자식만 반환하고 실제 entries JSON을 1MiB 이하로 제한한다. 최종 WSS envelope의 1MiB 검사는 LBT-150의 transport가 담당한다.
- Read는 요청 `maxBytes`와 서비스 한도 안에서만 읽고, 반환한 정확한 bytes의 SHA256 revision을 생성한다. 기본 서비스 한도는 1MiB, 작업 timeout은 30초다. LBT-150은 요청별 제한을 전달해야 한다.
- Save는 기존 regular file만 열고 생성하지 않는다. 기존 bytes의 revision 비교 후, helper가 첫 mutation 직전에 열린 inode/현재 경로/bytes를 다시 검증한다. truncate도 같은 handle로 수행한다. Linux inode flock은 여러 helper 및 hardlink alias의 Save를 직렬화한다. Terminal 등 flock에 참여하지 않는 writer에는 첫 mutation 직전 검증까지만 제공하며 전체 filesystem transaction을 보장하지 않는다.
- 경로 교체/내용 변경은 `REVISION_CONFLICT`, 없는 파일은 `NOT_FOUND`, 권한은 `PERMISSION_DENIED`, 제한 초과는 `TOO_LARGE` 등 기존 File 오류로 변환한다. helper의 private SFTP status는 내부 protocol이며 SaaS wire 계약을 확장하지 않는다. raw error/path/content는 로그에 남기지 않는다.
- 취소·timeout은 해당 채널과 로컬 pipes를 닫는다. Save 시작 후 취소·단절은 부분 변경이 남을 수 있으므로 새 Read로 현재 revision을 확인해야 하며 Save를 자동 재전송하지 않는다. Manager retirement/Reset/Connector shutdown은 연결 수명주기를 담당한다.

비협조 SSH peer/TCP blackhole에서는 다른 PTY를 보존하는 동안 원격 CLOSE 대기 bridge가 늦게 종료될 수 있다. 호출자는 timeout에 반환하고 작업은 서비스당 16개로 제한되며, 미종료 작업의 slot/path lock은 연결 retirement까지 보존한다. 정상 sshd의 helper timeout과 PTY 보존은 실제 VM acceptance로 검증한다. TCP blackhole은 별도 잔여 검증 경계다.

자동 검증은 `go test ./internal/connector/workspacefile ./internal/connector/workspacefs ./internal/connector/app ./internal/connector/provider/openstack`이다. 실제 Linux VM acceptance는 `TestOpenStackWorkspaceFileIntegration`의 명시적 opt-in이며 폐기용 local OpenStack, 관리자 helper 설치, tracked resource cleanup을 포함한다. 재현 입력과 실행 결과는 LBT-149 Evidence에 기록한다. Browser/SaaS 경로는 후속 LBT-150 범위다.
