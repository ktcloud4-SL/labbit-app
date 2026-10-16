# 재현 및 Evidence 읽기

기준 base/HEAD는 `0051d8d6803fd447f7b690dd895a0a928c3c7302`, 최종 LF 정규 Go/module source identity는 `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`다. `full-code-diff.patch`는 미추적 새 Go 파일까지 포함한다. 각 stage의 `code-diff.patch`는 당시의 참고용 tracked diff이며 단독 복원에는 사용하지 않는다.

## 소스 복원

별도 빈 디렉터리에 base를 `git archive`로 추출하고 **그 디렉터리 안에서 `git init`**한 뒤 `git apply --check`와 `git apply`를 실행한다. 상위 checkout의 Git을 발견하도록 두면 patch가 잘못된 repository 기준으로 적용될 수 있다. `source-manifest.json`의 모든502경로를 CRLF→LF 정규화해 SHA256과 비교한다. 파일명→해시 map을 Python `json.dumps(files, sort_keys=True).encode()`한 SHA256이 aggregate다. `package-verification.json`에 실제 독립 복원 결과를 기록한다.

## 일반 검증

실행 디렉터리는 repository root다. Go1.27.1, Windows/amd64, GCC UCRT POSIX, Python3.12, Node 및 web lockfile을 사용했다. PATH에 Go와 GCC를 추가하고 race에서는 `CGO_ENABLED=1`, `CC=<gcc.exe>`를 설정한다.

```powershell
go test -count=1 -json ./...
go vet ./...
go build ./...
go test -count=100 -json ./internal/connector/vmaccess ./internal/server/realtime -run '^(TestTCPOpenTimeoutLateChannelAndSiblingIsolation|TestConnectorControlEventsPropagateTraceContext|TestControlLog.*)$'
go test -race -p=1 -count=1 ./internal/connector/provider/openstack ./internal/connector/vmaccess ./internal/connector/preview ./internal/connector/app ./internal/connector/terminal ./internal/connector/filetransport ./internal/connector/wss ./internal/server/realtime ./internal/server/preview/previewtest ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap
```

`web`에서 lockfile 기준 dependency 설치 후 `npm run typecheck`, `npm run lint`, `npm run test`, `npm run build`. 이 환경에서는 pnpm의 `--package=npm@10.9.2 dlx npm run <script>` adapter를 썼다. 기본 Go suite의18skip은 opt-in cloud14 및 Windows filesystem4다.

## 실제 PostgreSQL

PostgreSQL16.15 portable binary를 loopback `127.0.0.1:15432`에 시작한다. `first/prepare-postgres.py`와 `postgres-setup.log`는 당시 절차/원본이다. 이 로컬 테스트는 synthetic postgres user와 임시 DB를 쓰며 운영 DB를 대상으로 실행하지 않는다.

```powershell
$env:LABBIT_TEST_DATABASE_DSN='postgres://postgres@127.0.0.1:15432/postgres?sslmode=disable'
go vet -tags=integration ./internal/postgres ./internal/postgres/postgrestest ./internal/server/app ./internal/server/auth ./internal/server/httpapi
go test -tags=integration -p=1 -count=1 -json -timeout=30m ./internal/postgres ./internal/postgres/postgrestest ./internal/server/app ./internal/server/auth ./internal/server/httpapi
$env:LABBIT_WIRE_SAMPLE_DIR='<별도 임시 출력 디렉터리>'
go test -tags=integration -run '^TestDumpPreviewWireSamples$' -count=1 -json ./internal/server/app
```

마지막 sample의 실제 package/인자는 `finalized/preview-wire-samples-result.json`을 우선한다. `labbit_it_*` 임시DB가0인지 조회하고 PG를 정지했다(`finalized/postgres-residual.log`, `postgres-stop.log`). 실제 PG suite 결과는 sealed source에서 실행됐다. 실행 중 unrelated OS fixture 수정으로 global guard false이며, 최종 소스와 동일한 compiled238repo inputs+module inputs를 독립 대조했다. 최종 소스에서 재실행한 결과로 표현하지 않는다.

## 로컬 OpenStack 준비

보호된 외부 `clouds.yaml`과 해당 cloud의 disposable integration 권한, VM SSH 개인키가 필요하다. 키/인증정보는 Git/Evidence에 넣지 않는다. 당시 OS_CLOUD=`labbit-test`, image=`ubuntu`, flavor=`m1.small`, management network=`sharednet1`, 관리 SSH CIDR=`172.16.8.1/32`, lab CIDR=`198.20.0.0/24`였다. 각 환경에 맞는 기존 keypair/SG를 지정하면 helper는 해당 자원을 소유하지 않으므로 삭제하지 않는다. 생략 시 helper가 만든 keypair/SG만 정리한다.

설정: `LABBIT_PROVIDER_CONFIG_FILE`, `OS_CLOUD`, `LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE`, `LABBIT_OPENSTACK_SSH_USERNAME`, `LABBIT_OPENSTACK_TEST_IMAGE`, `LABBIT_OPENSTACK_TEST_FLAVOR`, `LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK`, `LABBIT_OPENSTACK_TEST_SSH_CIDR`, `LABBIT_OPENSTACK_TEST_LAB_CIDR`. opt-in flag는 `first/finalized-verify.py`의 live/live-all/live-files와 각 `_integration_test.go` gate를 따른다. **live-all 원본에는 file helper 입력이 빠져2FAIL이 발생했으므로 아래 Linux build 및 helper 입력을 먼저 준비해야 한다.** 실제 KT opt-in은 설정하지 않는다.

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go build -o '<out>/workspace-sftp' ./cmd/labbit-workspace-sftp
go test -c -o '<out>/workspacefs-tests' ./internal/connector/workspacefs
go test -c -o '<out>/provider-tests' ./internal/connector/provider/openstack
go test -c -o '<out>/workspacefile-tests' ./internal/connector/workspacefile
go test -c -o '<out>/credential-tests' ./tools/dev-auth-class-fixture
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED
$env:LABBIT_WORKSPACE_SFTP_HELPER_FILE='<out>/workspace-sftp'
$env:LABBIT_WORKSPACE_SFTP_TEST_BINARY='<out>/workspacefs-tests'
$env:LABBIT_LOCAL_LINUX_REGRESSION_DIR='<out>'
$env:LABBIT_OPENSTACK_FILE_TRANSPORT_TEST='1'
$env:LABBIT_OPENSTACK_WORKSPACE_FILE_TEST='1'
go test -json -count=1 -timeout=40m -run '^TestOpenStack(WorkspaceFile|FileTransport)Integration$' ./internal/connector/provider/openstack
$env:LABBIT_OPENSTACK_INTERNET_POLICY_TEST='1'
go test -json -count=1 -timeout=30m -run '^TestOpenStackInternetPolicyIntegration$' ./internal/connector/provider/openstack
```

## Linux C1 harness와 추가 overlay

Windows의 `syscall.Stat_t` 때문에 기존 tagged harness는 Windows에서 compile되지 않는다. Linux/amd64 CGO0으로 `go vet -tags="backend_router_harness integration" ./internal/server/app ./internal/server/connector`, `go build -tags=backend_router_harness -o <out>/labbit-c1-server ./cmd/labbit-server`, `go test -c -tags="backend_router_harness integration" -o <out>/harness-tests ./internal/server/app`를 실행했다. **정확한 build tags/package는 closing/harness-linux-*-result.json을 우선한다.** `LABBIT_LOCAL_HARNESS_BINARY=<out>/harness-tests`를 설정한다.

`first/workspace-linux-harness_test.go.txt`는 source Workspace fixture의 별도 사본에 추가C1을 연결한 test overlay다. 원래 Workspace assertion과 final Start/Wait helper를 유지한다. `workspace-linux-harness-overlay.json`의 Replace key는 해당 checkout의 `internal/connector/provider/openstack/workspace_file_integration_test.go`, value는 overlay 사본의 **현재 절대경로**로 다시 만든다. 개인 로컬 절대경로를 그대로 실행하지 않는다. 위 PG가 host loopback에서 살아 있는 상태로 `closing/local-workspace-linux-harness-result.json`의 `go test -overlay <재작성 JSON>` 명령을 실행한다.

overlay는 기존 hostpin 확인된 SSH를 사용해 VM loopback 임시port에서 host loopback PG로 reverse-forward한다. 실제 provider는 MockProvider, DB는 실제PG이며 외부 KT API는 호출하지 않는다. exact12 parent C1 PASS가 모두 필요한 gate다. **이 Linux C1 binary는 non-race**이며 원격CI의 Linux tagged-race 실행을 대신하지 않는다. 파일 회귀4개는 같은 disposable Linux VM의 실제 symlink/rename/0600 조건에서 검증했다.

## 원본·source guard·secret

`first`, `intermediate`, `verified`, `sealed`, `closing`, `finalized`는 시간순 source stage다. 첫 InternetON 실패, helper 입력 누락, Windows harness compile 실패, 독립probe v1/v2 오류를 삭제하지 않았다. `*-result.json`의 `exitCode`는 실제 child command 결과다. 일부 runner는 실행 도중 다른 테스트 코드 변경을 감지하면 command exit0이어도 assertion으로 wrapper exit1이다. `sourceUnchanged=false` 원본을 보존하고 적용 가능 범위만 `stage-source-bridges.json`/audit에서 설명한다.

24KB를 넘는 raw log는 lossless `.gz`다. 압축을 풀어 `raw-provenance.json`의 raw bytes/SHA256과 비교한다. `*-events.json`은 raw JSONL의 pass/fail/skip event를 독립 재집계한 산출물이다(부모/자식 모두 별도 event). package pass 숫자와 test event 숫자는 별개다. 전체 압축 전/후 source raw hash와 파일 checksum을 별도로 확인한다.

원본 `.py` runner의 ROOT/OUT/Go/GCC/SSH/cloud 입력은 당시 절대경로다. 재현할 때 별도 작업 디렉터리로 복사하고 경로를 수정하거나 위 동등명령을 직접 실행한다. Evidence 폴더 안에서 그 runner를 무수정 실행하지 않는다. 원본 실행 로그를 덮어쓰지 않는다. SSH개인키/clouds.yaml/PGdata/zip/Linuxbinary는 필수입력·생성물이며 패키지에 포함되지 않는다.

재현용 Go 사본은 `.go.txt`로 보관해 Go package 검색/502 source manifest에 포함되지 않는다. overlay Replace의 value는 이 `.go.txt` 실제 절대경로를 사용해도 Go가 내용을 읽는다. raw-provenance의 source는 원본 `.go` 이름, artifact는 `.go.txt` 이름이며 bytes/SHA는 같다.
