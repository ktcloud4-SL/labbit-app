# Internet 경로 진단·Linux native race 방법 독립 검토

검토자: 구현과 분리된 `/root/review_lbt173`. 주 checkout·제품 소스·클라우드 자원·원격 저장소를 변경하지 않았다. 이 보고서는 방법 및 관측 경계를 기록하며 진행 중 실행의 최종 판정은 아니다.

기준은 `0051d8d6803fd447f7b690dd895a0a928c3c7302`와 기존 미커밋 LBT-174 전체 27 Go 변경이다. 502 Go/module 입력 LF SHA aggregate는 `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`이다. 사용자 AGENTS와 `PRE_MERGE_AUDIT.md` A–F를 다시 읽었다. 최신 Jira/D18 등 직접 조회 결과는 `ssot-refresh.json`에 있고 D18 v5를 방법 검토 중 다시 조회하여 변경 없음을 확인했다.

## 소스·전송 동일성

`check_method_sources.py`를 독립 실행했다. `method-source-verification.json`은 원본 입력을 읽어 산출한 결과다.

- 현재 checkout 502 입력과 source manifest가 전부 일치한다.
- Linux source archive의 tracked 및 Go 입력 1,237 파일은 checkout 원문과 전부 일치한다. Go 입력만 LF 정규화한다. archive 내부 `tested-source.json`도 동일하다.
- vendored 1,923 파일은 외부 vendor 디렉터리와 byte 일치하며 SQL migration 7개가 포함된다.
- archive SHA/size와 saved metadata가 일치한다. Go archive 70,553,950 bytes의 SHA는 저장된 공식 Go metadata 응답과 일치한다. 이 독립 스크립트는 metadata를 네트워크에서 다시 받지는 않았다.
- tar 중복 경로·absolute/traversal 경로·link/device는 없고 `.local`·`.ssh`·`.git` 및 검사한 credential basename은 없다. 이 검사는 경로 검사이며 모든 내용의 secret 부재를 증명하지 않는다.
- v1 실행의 4 overlay/source hash는 `run-state.json`과 일치한다. 원격 Linux archive에는 관측용 `startup_observed.go` overlay가 적용되지 않는다.

## 방법·보안·수명주기

관측 overlay는 단계·안전한 오류 type/exit status·고정 assertion 여부와 role 기반 네트워크 JSON을 추가한다. production의 safe error 반환·SSH host pin·정확한 관리 NIC·원본 StartupScript는 유지한다. 실제 IP/key/hostpin/raw stderr를 진단 JSON에 출력하지 않는다. SSH 응답 stdout/stderr의 메모리 버퍼는 이 고정 diagnostic command를 위한 외부 도구이며 production 기능으로 일반화하면 안 된다.

경로 A/B는 사전 `1.1.1.1/32` 부재 확인 후 선택 role의 gateway/device/metric50으로 exact route를 추가한다. replace/flush나 공유 OpenStack network/SG 변경은 없다. 각 probe 뒤 finally에서 추가한 exact route를 삭제하고 마지막 부재/route role 복원을 확인한다. 이 방법은 관리 SSH 및 PG 대상의 default 경로를 바꾸지 않는다. route-role 복원 marker는 전체 Linux routing table의 byte 동일성을 뜻하지 않는다.

apt 준비만 소유 disposable VM에 임시 Lab default metric40을 추가한다. 관리 SSH는 같은 subnet의 연결 경로이며 PG reverse forward 생성 전이다. v3는 `NEEDRESTART_MODE=l`과 정확한 gateway/device/metric route 조회 후 삭제·부재 assert를 사용한다. 존재하지 않는 삭제 대상에 대한 오류를 막연히 무시하지 않는다. 추가 swap은 소유 disposable VM disk이며 VM 폐기가 최종 회수 경계다.

remote phase timeout/cancel은 SSH session을 닫지만 `Run` goroutine 완료를 즉시 join하지 않는다. reverse PG forward도 별도 connection별 join 장부가 없다. 성공한 정상 경로는 `session.Run` 반환/SSH client close와 최종 VM 삭제·DB0·PG stop으로 검증한다. timeout 후 즉시 remote process/route가 모두 회수됐다고 주장할 수는 없다. 실패 시 test 종료와 final exact cleanup을 확인해야 한다.

## 현재 원본별 확인 결과

| 원본 | 확인한 결과 | 판정 경계 |
| --- | --- | --- |
| v1 `local-network-race.log`/`run-result.json` | 12 Internet probe가 remote exec exit1/fixed Python assertion/connectCode11 약5초로 실패. baseline/forcedManagement 실패, forcedLab 성공. source 전후 동일. 전체 exit1/282.578초 | 같은 safe-error 형태의 실패를 더 좁은 단계로 재현했다. 첫 probe의 routeBefore Lab snapshot과 실제 socket tuple의 관계는 미수집이다. 역사적 최초 VM의 원인을 소급 확정하지 않는다. |
| v1 `linux-apt.log` | package install 뒤 서비스 restart 기록과 exact metric40 delete `No such process`; native Linux 세 명령 미진입 | setup 실패 원본을 보존한다. 서비스 restart와 route 소실의 정확한 중간 상태를 직접 기록하지는 않았다. VM tracked8 및 보조SG/keypair exactGET404, 임시DB0/PG stop 성공. |
| v2 `probe-v2/` | 전체 exit0/134.062초. 첫 12 probe는 source/tuple/route Lab이며 성공. forcedManagement는 source/tuple Management, code11. forcedLab는 source/tuple Lab, code0. rp_filter는 양쪽2. exact route 삭제·restored marker 확인. tracked8+SG/keypair 부재 확인 | 진단 fixture PASS이며 일반 Internet ON 안정성 또는 Linux native race PASS를 뜻하지 않는다. |
| v3 `linux-v3/` | 전체 child exit1/945.985초. route A/B는 forcedLab 성공 출력 뒤 45초 phase timeout/t.Error, restored marker 미확인. native Linux CI phase exit0/591.285초, vet/build/race 통과·C1 부모12/12·전체67 test pass·skip0/race0. tracked8+SG/keypair GET404·DB0/query0/stop0·source전후동일 | native CI 성공과 실패한 전체 fixture를 분리한다. timeout의 remote process/route 정리는 최종 소유VM 폐기와 구분한다. |

v1 apt raw traceback에는 Lab gateway 원문 IP가 있다. 외부 원본 보존과 tracked Evidence 게시를 구분하고, 게시 시 hash+안전 metadata 또는 role 치환 사본으로 처리해야 한다. 치환 사본을 원본 byte 동일이라고 표시하지 않는다.

### F174-METHOD003 / Low — PG start 실패 뒤 무조건 stop

외부 `run_validation.py:20–33` 및 생성된 v3 동일 블록은 `pg_ctl start` 실패에도 finally에서 psql 조회와 `pg_ctl stop`을 실행한다. 같은 전용 data directory의 기존 PG가 동작하여 start가 거절되면 이번 실행이 시작하지 않은 기존 PG를 중단할 수 있다. 현재 v3 실제 실행은 start에 성공했으므로 이 조건을 관측한 실환경 피해는 없다.

수정 전 사용자 보고를 위해 `review_runner_ownership.py`가 원본 try/finally AST만 실행하고 subprocess를 전부 stub으로 대체했다. 실제 PG/Go/cloud 호출은 없다. `runner-ownership-observations.json`은 start reject에서 stop1/query1/test0, 정상에서 stop1/test1을 재현한다. 원본 SHA와 사본 `reviewed-run-validation.py.txt`를 보존했다.

권장 수정은 새 실행용 runner에 `startedHere` 소유권을 start 성공 뒤 설정하고, 그 경우만 조회·stop하는 것이다. start 실패 metadata는 남기고 원본 as-run runner는 바꾸지 않는다. 의미 있는 회귀는 정상 start→stop1, start reject→stop0, Go child 실패/timeout 뒤 소유 PG stop1이다. 새 `run_validation_safe.py` 두 파일을 독립 검토하고 `review_safe_runner_ownership.py`로 4경우씩 총8경우를 실제 AST·완전 stub subprocess로 검증했다. `safe-runner-ownership-observations.json`에 모두 PASS이며 원래 as-run 두 파일의 SHA도 불변이다. 이 finding은 새 재현 runner 범위에서 해결됐고 제품 결함은 아니다. 실제 v1/v3 실행은 이 safe runner로 실행한 것이 아니다.

## 확인된 네트워크 한계의 분류

이는 **Medium의 기존 구성 의존 가용성 한계·배포 계약 공백**으로 기록하는 것이 적절하다. 단순한 flaky test 가설로 축소할 근거가 없다. 반면 특정 provider 코드 버그의 원인·수정 책임을 단정할 근거도 아직 부족하다.

관련 base/a7d 위치는 `provision.go:219–223`의 Management 입력 수락, `topology.go:127`의 두 NIC 구성, `runtime/contract.yaml:592–598`의 Management 입력·SG 보안 조건이다. 실제 accepted Ubuntu 및 Management DHCP 입력에서 양쪽 metric100 default가 형성됐고 일반 연결의 실패를 재현했다. forced Management 경로 실패와 forced Lab 성공은 재현 가능한 경로별 차이를 증명한다. 이는 LBT-174의 Preview 변경으로 도입된 회귀가 아니다.

[D18 v5 결정7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730/D-18+VM)은 Internet ON outbound 경로 제공을 요구한다. Git 계약은 Lab router/SG 분리를 구현하지만 Management DHCP gateway 금지·guest metric/image 준비 책임은 정의하지 않는다. 따라서 지원 image/Management 배포 조건과 guest route 정책의 소유권 결정이 후속으로 필요하다. Management SG egress를 열거나 immutable StartupScript를 임의 변형하고 공유 subnet을 바꾸는 해결을 추론하지 않는다.

필요한 후속 회귀는 두 NIC DHCP default의 순서/동률 조건에서도 선택한 supported 정책에 따라 ON reachable/OFF blocked, 관리 SSH·Terminal/File/Preview 유지, Reset 뒤 동일 정책, Management egress0·타 Lab 격리, 재시도 없이 안전한 실패 관측을 검증해야 한다. 지원 조건 밖 입력으로 결정한다면 해당 조건의 owning SSOT 및 생성 전 명확한 validation도 함께 검토해야 한다.

## Linux native 최종 판정 조건

`linux_ci.sh`는 `.github/workflows/go.yml:64–66`의 vet/build/tagged race selection을 유지한다. native Go1.27.1/CGO1/GCC, source502 hash 확인 후 3명령을 `set -euo pipefail`로 순서대로 실행한다. offline vendor·GOMAXPROCS2·build parallel2와 JSON 관측 옵션은 환경 차이로 기록한다.

최종 판정에는 3 child exit0 또는 각각의 성공 marker와 최종 child exit0, 정확한 `TestC1` 부모12개 전부 PASS/skip0/race0, realPG 실제 연결·임시DB0/PG stop, 원격 child 종료, tracked 자원+보조SG/keypair exact 부재를 원본에서 확인해야 한다. 실제 Provider는 Mock인 C1 control harness 검증이며 KT API smoke 또는 원격 GitHub CI 성공으로 표시하지 않는다.

native Linux의 위 조건을 원본에서 확인했다. `live-independent-observations.json`은 source에서 정확한 C1 부모12개를 추출해 실제 JSONL과 대조한 결과다. 남은 단계는 새 Evidence 패키지·최종 문구 대조다. 기존 코드/로컬 검증 판정과 이번 네트워크 가용성 한계·실패한 전체 diagnostic fixture를 분리한다. Audit는 push/PR/merge/배포 승인으로 해석하지 않는다.
