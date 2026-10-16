# LBT-173 fresh full fixture 재현

이 디렉터리는 이전 실패/복구와 분리된 새 실행이다. 이전 [KT 기록](../README.md)의 실패한 journal은 변경하지 않는다. 최종 결과는 이 디렉터리의 README/AUDIT와 metadata를 함께 확인한다.

Windows/Go 1.27.1, Python, 계정 ACL로 보호한 새 출력 디렉터리, 검증된 KT D1 `clouds.yaml`의 `ktcloud-d1` alias가 필요하다. Credential/private key/host pins/원본 Provider 응답은 Git 밖에 둔다. 실제 자원 생성은 별도의 사용자 승인 범위에서만 실행한다. 이 실행은 사용자의 KT 진행 및 fixture 일괄 PASS 지시에 따른다.

1. 테스트 대상 source를 `capture.py snapshot`으로 고정한다. working tree와 신규 Go 파일까지 포함한다. 빌드 전후 Go source의 LF SHA와 해당 manifest를 비교한다.
2. `LABBIT_EVIDENCE_REPO`를 checkout, `LABBIT_EVIDENCE_OUT`을 보호된 새 `build` 디렉터리로 정해 `docs/connector/evidence/LBT-151/2026-10-14/validate.py build`를 실행한다. 생성된 `bootstrap.exe`, `labbit-connector-ktcloud`, `lbt145-control-linux`, `labbit-workspace-sftp`를 출력 디렉터리 최상위에 복사한다. GOOS/GOARCH/CGO/flags/digest는 `binary-manifest.json`에 남는다.
3. [기존 bootstrap runbook](../../../../../../../tools/ktcloud-op02-bootstrap/README.md)의 환경을 설정한다. `LABBIT_KTCLOUD_D1_BOOTSTRAP=1`, `LBT145_PREMERGE_LIVE_TEST=1`, `LBT145_CONTROL_GENERATIONS=2`, `LBT145_BOOTSTRAP_STRICT_DOCUMENTED=1`, `OS_CLOUD=ktcloud-d1`, `LBT145_BOOTSTRAP_FLAVOR=2x4.itl`, **`LBT173_BOOTSTRAP_PREVIEW_SMOKE=1`**. `LABBIT_PROVIDER_CONFIG_FILE`, `LBT145_BOOTSTRAP_LEDGER`, `LBT145_BOOTSTRAP_KEY`는 보호된 절대 경로다.
4. `LBT145_BOOTSTRAP_PHASE`를 각각 설정하고 `bootstrap.exe`를 순차 실행한다: `inventory`, `create`, `access`, `ssh`, `deploy`, `control`, `collect`, `residual`, `cleanup`, `residual`, `final`. 매 단계 시작·끝 시각/exit code/보호된 log 파일명을 JSONL로 기록한다. 하나라도 실패하면 같은 journal로 `control`을 반복하지 않는다. 원본을 보존하고 문서화된 exact-ID Reconcile/복구를 수행한 뒤 별도의 새 실행을 준비한다.
5. `control`은 실제 production Connector child의 TLS Control/HELLO/Provider lifecycle을 실행한다. Preview smoke는 같은 host의 acceptance harness 프로세스에서 production VMAccess/SSHForwarder 라이브러리를 사용한다. 2세대별 HTTP2회/PTY/SFTP/공유 SSH/잘못된 대상·port·stale 세대 거절을 확인한다. Preview Data WSS/실 SaaS/Browser C3는 이 fixture의 범위가 아니다.
6. Cleanup 성공만으로 끝내지 않는다. 10개 Lab exact-ID 부재와 child 정상 종료를 확인하고, bootstrap 8개 소유 자원도 정리한 뒤 잔여 0 및 7종 inventory의 exact baseline 일치를 확인한다. `collect`는 bootstrap 삭제 전에 수행한다.
7. `export_metadata.py <보호된 출력 디렉터리>`가 journal/11개 단계/정상 종료/10개 Lab 상태/잔여 0/7종 baseline을 assert한다. 이 도구는 cloud API를 호출하지 않으며 ID는 hash로만 내보낸다. 원본 log는 prefix allowlist로 lifecycle metadata만 내보낸다.
8. `capture.py check`로 실행 소스 불변을 확인한다. `capture.py summarize`는 전체 Go JSON log를 집계하고 gzip 원본을 만든다. `capture.py format`은 inherited CRLF를 LF로 정규화해 gofmt를 검사한다. source 변화가 생기면 이전 실행을 새 source의 PASS로 사용하지 않는다.

실행 topology는 bootstrap 1대와 차례로 교체되는 Lab VM 1대, 2 CPU/4 GiB/50 GiB, Ubuntu 24.04 image `os-ubuntu24.04-image-20260929`다. 기존 DMZ/Private/external network는 공유한다. Lab/management CIDR는 각각 `172.25.240.0/24`, `172.25.241.0/24`이며 다른 용도로 사용 중이면 실행하지 않는다. Workspace app은 VM의 `127.0.0.1:3000`에만 bind하고 공개 app port 정책을 만들지 않는다. TLS/SSH pin 검증을 우회하지 않는다.
