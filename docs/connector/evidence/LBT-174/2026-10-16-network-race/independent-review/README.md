# Internet 최초 실패 및 Linux tagged-race 독립 검토

2026-10-16. 제품 구현과 분리된 reviewer 분석이다. 사용자 `PRE_MERGE_AUDIT.md`, repository AGENTS/CONTRIBUTING/TESTING과 owning SSOT를 다시 읽었다. 제품·기존 Evidence·cloud·원격 Git은 수정하지 않았다. 새 진단/실환경 실행 결과와 이후 전체 diff의 최종 판정은 아직 대기한다.

## 고정 범위와 직접 확인한 원문

기준 base/HEAD는 `0051d8d6803fd447f7b690dd895a0a928c3c7302`, branch는 `codex/feat/LBT-174-preview-local-integration`이다. 현재502 Go/module 파일 source는 `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`다. 최초 broad batch는3e1 source/exit1/global sourceguard false라는 원본을 유지한다. 분석한 startup/server/provision/topology/integration5개 Go 파일과 go.mod/go.sum은 최초와 최종 manifest에서 모두 같다([대조](source-path-alignment.json)).

[LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) 본문·AC·댓글을 직접 갱신했다. updated14:59:23.423+0900, 댓글0, 진행중, 본문은 이전 직접읽기와 같다. D18v5/결정완료v11/전략v3/D20v7/추가결정v3도 전체 원문이 같다. 이번 raw connector 응답의 version번호를 직접 확인했다([갱신](ssot-refresh.json)). 원격CI AC 및 실제 SaaS/Browser C3·KT 후속 완료를 이번 로컬 실행으로 대체하지 않는다.

## 최초 실패에서 확정할 수 있는 것

[원본 timeline](initial-internet-timeline.json)은35952byte raw 전체 로그의 SHA와 Internet 관련 event만 보존한다. ON 시작15:56:17.937, 실패15:58:07.705, cleanup 뒤 ON fail15:58:22.419다. OFF는136.26초 PASS, 부모는262.29초 FAIL이다.

`integration_test.go:387–405`의 분기에 따라 ON은 Provision SUCCEEDED 뒤 별도 reachability command 실행까지 도달했다. 따라서 이 오류를 Provision 자체의 cloud-init 실패로 읽을 수 없다. Provision은 `provision.go:119–137`에서 authenticated `true`와 StartupScript의 `cloud-init status --wait` 성공을 이미 확인했다. 이것이 뒤의 새로운 SSH 연결·egress 성공까지 보장하는 것은 아니다.

`startup.go:59–105`의 `runSSHCommand`는 private-key read/parse, TOFU 준비, TCP dial, SSH handshake, session 생성, exec 거절, 원격 exit!=0을 모두 정확히 같은 `ErrStartupNotReady`로 반환한다. Internet command 자체(`integration_test.go:424`)도 `connect_ex != 0`이면 assertion으로 exit1이 된다. 원래 로그의 `VM initialization did not complete`만으로 SSH 오류인지 egress assertion 실패인지 구분할 수 없다.

안전한 wire 오류를 유지하는 동작 자체를 제품 결함이라고 판정하지 않는다. 이 fixture의 역사적 원인 관측이 부족하다는 제한이다. 삭제된 원래 VM의 guest route/errno와 실패 stage는 이 Evidence에 없다. 보존된 운영 로그가 별도로 있다면 추가 근거가 될 수 있지만, 새 PASS나 새 fault 재현이 과거의 물리적 원인을 증명하지 않는다.

## 독립 fault probe

외부 overlay의 `TestReviewInternetFailureAliases`를 실제 loopback SSH에서 `-race -count=10` 실행했다. success positive control10회는nil, 나머지 TCP refusal/handshake drop/channel rejection/exec rejection/remote exit1/remote exit42 각10회는 모두 정확히 같은 readiness sentinel이었다.70 subtest와10 parent PASS, race warning0, 실제 process exit0/16.422초다.502개 source는 전후 manifest와 동일했다.

- [fixture](review_error_alias_test.go.txt), [overlay](failure-alias-overlay.json), [원본](failure-alias-race.log.gz), [실행 metadata](failure-alias-result.json), [독립 count](failure-alias-observations.json).
- overlay Replace key는 해당 checkout package의 가상 `review_internet_observation_test.go`, value는 외부 fixture의 현재 절대경로로 재생성한다. `go test -race -overlay <JSON> -count=10 -run '^TestReviewInternetFailureAliases$' -v ./internal/connector/provider/openstack`.
- 이 probe는 서로 다른 실패가 같은 관측을 만든다는 것을 증명하며, 어느 실패가 원래 VM에서 일어났는지 증명하지 않는다. Git Evidence로 복사한다면 `.go.txt`로 보관해 Go package 검색에 포함시키지 않는다.

## 가설과 다음 관측

| 후보 | 현재 근거·반대 근거 | 구분에 필요한 관측 |
| --- | --- | --- |
| Management default 선택으로 egress 차단 | 후속 ON의 Lab/Management default가 둘 다metric100. Management SG는 egress0. 최초 route는 없음 | 첫 socket 전에 MAC→role과 `route get`/default metric을 read-only 관측하고 단일 socket connect code 기록 |
| Lab router/SNAT 또는 외부 target의 일시적 비도달 | 원격 connect_ex 실패도 동일 sentinel. 후속3target과 원래 fixture PASS는 당시 일시 장애를 배제하지 못함 | 실패한 첫 연결의 errno, Lab route 선택, owned Router/port의 safe 상태·시각. 필요하면 이후 별도 진단으로 명시 |
| 새 SSH transport/hostkey/session/exec 실패 | 정상 Provision 뒤 새로운 연결이라 가능. 기존 로그가 오류 stage를 지움 | TCP/handshake/channel/exec-start/exec-wait 단계·elapsed·timeout flag·안전한 exitstatus |
| 원격 Python/command 자체 오류 | exit!=0이 동일 sentinel. syntax/tool 존재 출력은 없음 | fixed diagnostic JSON marker 존재와 schema, exitstatus. stderr 원문을 중앙 로그에 게시하지 않음 |

첫 진단은 워밍업 SSH/target socket 없이 원래 첫 연결을 관측해야 한다. 원래 첫 실패를 결과로 보존한 뒤 추가 진단을 한다. connect code/단계/exitstatus와 bounded structured output만 기록하고 credential·raw SSH/provider error·개인경로는 기록하지 않는다. hostpin/관리 NIC/정확한 VM 소유권 검증을 우회하지 않는다. 공개 error/wire enum을 새로 만들 필요는 없다.

## Management default 소유권 판정

`runtime/contract.yaml:595–598`은 stateful Management SG가 Connector CIDR ingress22 하나/egress0이어야 한다고 명시한다. `integration_test.go:488–502`와 `provision.go:265–290`은 이를 구현한다. 이 보안을 풀어 public egress를 살리는 것은 올바른 수정이 아니다.

D18v5는 Internet ON의 Lab Router outbound와 별도 Management 관리경로를 요구한다. `contracts/connector/README.md:512–514`도 ON Router/OFF gateway없음과 별도 SG·Startup Ready를 정한다. 반면 guest route metric·Management subnet의 DHCP default gateway 금지는 현재 SSOT에 없다. `runtime/contract.yaml:592–594`는 Management Network ID 입력만 정의하고, `provision.go:219–223`은 ACTIVE/subnet존재와 SG를 검사한다. `topology.go:127`은 Lab/Mgmt NIC 순서를 전달하며 guest route policy를 지정하지 않는다.

따라서 현재 증거로 환경문제 또는 제품문제 어느 한쪽으로 확정할 수 없다. A/B로 잘못된 role route의 차단이 확인되면, 실제 사용자가 허용된 image/Management Network 조합으로 같은 상태가 되는지 확인해야 한다. 된다면 fixture 전용 workaround로 종결할 수 없다. 반대로 특정 로컬 환경의 비표준 DHCP 구성이라면 지원 입력/배포 준비 요구를 명확히 해야 한다. guest route 정책이나 새로운 preflight 거절을 도입한다면 owning SSOT·image/Platform 책임을 함께 결정해야 한다. 사용자의 immutable StartupScript를 임의 변형하거나 공유 Management subnet을 변경해서 해결하지 않는다.

인과 A/B는 고정 public target의 exact `/32` route만 role gateway/device별로 추가→single probe→finally exact delete하는 방식이 좁다. default route 변경은 SSH 응답과 PG reverseforward까지 바꿔 원래 측정·cleanup을 손상시킬 수 있다. 사전 hostroute 부재/추가 성공 확인, replace/flush 금지, 추가한 exact route만 삭제, 원래 route 결과 복원 확인이 필요하다. A/B로 재현해도 과거 최초실패의 동일원인으로 단정하지 않는다.

## Linux 특수 race 검증 검토 기준

현재 CI 원문 `.github/workflows/go.yml:60–66`의 세 명령은 아래다.

```sh
go vet -tags 'backend_router_harness integration' ./internal/server/app ./internal/server/connector
go build -tags backend_router_harness -o /tmp/labbit-c1-server ./cmd/labbit-server
go test -race -tags 'backend_router_harness integration' -run '^TestC1' -count=1 ./internal/server/app
```

이제 Windows cross compile/non-race binary의 PASS로 대체하지 않는다. Linux native Go1.27.1, CGO_ENABLED=1, GCC, 실제PG16과502 source LF hash를 검증하고 세 명령의 실제 child exit를 각각 남겨야 한다. source/vendor/migrations/embedded inputs transfer 전후 manifest, go env/toolchain, remote process join/exit, exact12 C1 parent PASS/skip0/race0를 대조한다. `-json`을 추가한 관측 실행은 동일한 test selection/race/tag/count 조건을 유지한다.

C1 actual Control stack은 `router_harness_integration_test.go:19–20`대로 realPG/WSS/Handler/Dispatcher지만 Provider는Mock이며 KT API를 호출하지 않는다. disposable Linux VM의 cloud 생성·삭제는 root만 수행한다. SG/keypair와 tracked VM/network 부재, 임시DB0/PG정지까지 최종 Evidence 범위에 포함한다. 원격 GitHub CI 자체의 성공으로 표시하지 않는다.

## 현재 상태

현재 단계에서 새 confirmed 제품 finding은 없다. 역사적 failure stage 불명과 routing 가설은 구분해 root에 전달했다. 새 diagnostic source·실환경 A/B·Linux native race 결과가 준비되면 전체 diff와 A–F 추적성/보안/수명주기를 다시 검토한다. 이 초기 분석은 새 범위의 완료 또는 PR 판정이 아니다. push/PR/merge/배포 승인이 아니다.
