from pathlib import Path
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
text='''# 재현·원본·범위

현재 Go/module502파일은 이전 followup의 [patch 복원절차](../2026-10-16-followup/REPRODUCE.md)로 base0051d8d에 전체27diff를 적용하면 복원된다. LF aggregate가 a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51인지 먼저 확인한다. 이번 작업의 repo Go 변경은0이다. 이전 full test 결과와 구별한다.

## 도구·보호 입력

Windows Go1.27.1/Python3.12, 실제 local OpenStack cloud `labbit-test`, image `ubuntu`(관측VM Ubuntu24.04/glibc2.39), flavor `m1.small`(관측RAM961MiB), Management `sharednet1`, SSH 사용자ubuntu/Connector CIDR172.16.8.1/32가 사용됐다. VM마다 새owned SG/keypair/knownhosts와 LabCIDR198.24.0.0/24를 사용한다. 격리Lab subnet은 같은CIDR이어도 별도network/router다.

`LABBIT_PROVIDER_CONFIG_FILE`의 개인clouds.yaml과 `LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE`의 개인키를 별도로 제공해야 한다. 기본 원본 helper는 당시 `.local/lbt174/local-fixture-key`를 가리킨다. **보호 입력의 내용은 Evidence/Git에 없다.** 자신의 경로·허용CIDR/image/flavor/network로 맞추되 실험한 설정과 차이를 기록한다. 실제KT opt-in은 설정하지 않는다. SGegress0을 열지 않는다.

nativeLinux toolchain은 공식Go1.27.1 linux-amd64 tar(70,553,950bytes), SHA25663d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445. [metadata](run/linux-go-download.json)를 따른다. Go툴/vendored deps/archive는 원격네트워크를 쓰기 전에 Windows에서 준비한다. portable PG16.15의 준비·원본 출처·hash는 이전 followup 재현절차와 같다. 호스트127.0.0.1:15432에만 bind한다.

## 외부 helper 복원과 실행

이 패키지의 모든 `.go.txt`는 **외부 tmp에서만** `.go` 이름으로 되돌린다. docs 아래에서 `.go`로 바꾸면 Go package 검색이 오염된다. `.log.gz`는gzip해제하면 원본bytes다. 단 v1linux-apt.log는 traceback의 실제Labgateway 하나를 `<lab-gateway>`로 치환한 사본이며 raw-provenance가 원본/사본 해시·길이·변환을 구분한다. 원본은 외부 tmp에 보존했다.

1. `run/` 파일들을 `<workspace>/tmp/lbt174-network-race-20261016/`로 복사하고 repo를 sibling `labbit-connector-gates-20261015`에 둔다. 경로가 다르면 runner의 ROOT/OUT/Go/PG 절대경로를 조정한다. 원본overlay JSON은 당시절대경로이므로 `create_overlay.py`로 재생성한다. 새helper/overlay hash를 기록한다.
2. `prepare_linux.py toolchain`, `prepare_linux.py source`로 공식Go SHA와 source/vendor archive를 만든다. 후자는 go mod vendor를 **repo외부**에 쓰며 source guard가0차이여야 한다. archiveSHA는tar gzipmtime에 따라 달라질 수 있다. **새 archive의 실제SHA를 linux-source-archive.json에서 확인해 runner의 LABBIT_LOCAL_SOURCE_SHA256에 반영한다.** source identity는502개LFhash와 aggregate로 동일성을 판정한다. 원본 archive SHA7a46757ab270cebe40cf66d5b1ec0a073dcb8c91a2e68fc3711297ea3c475dc3는 당시bytes를 식별한다.
3. 원래 `run_validation.py`는 v1을 재현하며 apt needrestart 후 helperdelete가 실패할 수 있다. 이를 nativeLinuxPASS 경로로 쓰지 않는다. 전체 v1과 실패는 원본으로 유지한다.
4. `prepare_probe_v2.py` 후 `probe-v2/run_validation.py`는 새로운ownedVM에서 socket source/tuple/rp_filter와 targethostrouteA/B만 검사한다. `LABBIT_LOCAL_PROBE_ONLY=1`; PG/nativeCI를 실행하지 않는다. observed첫12회 결과는단순parentexit가아닌 ssh-observations/internet-summary와대조한다. A/B expectedMgmtfail은 원래InternetON PASS를 의미하지 않는다.
5. `prepare_linux_v3.py` 후 `linux-v3/run_validation.py`는 새ownedVM, gcc/libc개발도구, needrestart list모드, exactrouteabsence,1GiBprivate swap, 해시검증archive를 준비하고 nativeLinux명령을실행한다. 새sourcearchiveSHA는이runner에도맞춘다. v3A/B는45초timeout이실제로발생했으므로 hostwrapper전체성공으로표시하지않는다. Linux자식성공은아래JSON기준으로별도판정한다. 재시도시작전동일portPG가미실행이고다른사용자작업이사용하지않는지확인한다.

원본 `run_validation.py`의 주요 opt-in은 LABBIT_LOCAL_NETWORK_RACE=1이다. Windowshost는 go test -overlay <JSON> -json -run '^TestLocalLinuxRaceAndInternetDiagnostics$' -count=1 -timeout=60m ./internal/connector/provider/openstack를실행한다. overlay관측이들어간실행을순수제품소스만의실행으로표시하지않는다. key/TOFU인증과원래safe오류mapping은유지한다.

## Linux에서 실제 실행하는 CI 세 명령

`linux_ci.sh`는 nativeGo/CGO1/GCC와502source를먼저확인한다. GOTOOLCHAIN=local/GOPROXY=off/GOSUMDB=off, GOFLAGS='-mod=vendor -p=2', GOMAXPROCS=2다. DBdsn은remote127.0.0.1:임시port의SSHreverseforward이며호스트PGloopback으로만연결된다.

```sh
go vet -tags 'backend_router_harness integration' ./internal/server/app ./internal/server/connector
go build -tags backend_router_harness -o /tmp/labbit-c1-server ./cmd/labbit-server
go test -race -tags 'backend_router_harness integration' -run '^TestC1' -count=1 -json ./internal/server/app
```

명령순서set-e이므로다음marker는직전자식exit0뒤에만출력된다. 테스트는marker만으로판정하지않는다. `summarize.py linux-v3`는repo의TestC1정확12parents모두PASS/누락0/skip0/fail0/DATA RACE0과vet/build/racemarker를함께검사한다. 새source에서필수집합이달라지면12고정gate가실패하므로검토가필요하다. C1actualstack은realPG+MockProvider이며actualKTAPI와구분한다.

## 원본·정리·독립검토

raw-provenance.json는외부원본경로와rawbytesSHA/압축·변환후SHA를대조한다. v1/v2/v3초기실패와skip미실행을숨기지않는다. source-manifest는원본a7d502이고각run-state/run-result전후guard및overlay4hash를확인한다. nativearchive와vendor의전체전달내용은독립reviewmethod-source-verification으로검증한다.

ownedVM/network/router/ports/SG8tracked의cleanup은기존helper의exactGET404확인이며추가SG/keypair도각GET404다. sharedresources전체삭제를주장하지않는다. timeout의Runworker는join이보장되지않는한계가있어VM폐기경계로기록한다. disposableVMswap/패키지/임시경로도VM폐기로회수된다. PG는labbit_it_*개수0을조회한뒤pg_ctlfaststop/status를확인한다. 다른사용자자원은수정하지않는다.

최초삭제VM의원래socket5tuple/stage는복원할수없다. 새로운동일오류재현과경로별인과A/B를그최초원인의직접증거로가장하지않는다. supportedguest/image/Management DHCP및route우선순위의소유권을정하기전에는InternetON의항상성공을보장하지않는다. 원격GitHubCI·actualKT·actualBrowser/SaaS·장기부하는이번로컬검증범위밖이다.
'''
(DEST/'REPRODUCE.md').write_text(text,encoding='utf8')
print('reproduction and historical failure boundaries written',flush=True)
