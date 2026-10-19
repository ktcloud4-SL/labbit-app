# 독립 추가 검토 후속 결과

## F174-T003 해결 확인

Root가 사용자에게 finding을 수정 전에 보고한 뒤 각 timeout 대상 DialTCP 호출에 goroutine + 2초 독립 select를 넣었다. 반환된 connection이 있으면 닫고 결과 channel은 buffered로 하여 timeout 후 cleanup이 반환한 goroutine도 걸리지 않는다. 정상 준비에 이 2초 select를 적용하지 않는다. 기존 late channel 정리, pending slot, canceled waiter worker 수, PTY sibling, 새 TCP 성공 검증을 유지한다.

- `after-T003-tcp_test.go`, `bounded-test-manifest.json`에 검토한 소스와 해시를 보존했다.
- 같은 정상 SSH handshake 75ms 지연에서 late-open/sibling test 10회 PASS. configured handshake timeout/cancel도 각 10회 PASS. `bounded-normal.log`.
- 같은 caller ctx 무시 단일 mutant에서 3회 모두 2.02초에 `caller deadline fell back to the normal SSH setup timeout`으로 FAIL. `bounded-mutant.log`.
- 첫 수정본의 50.08초 false PASS와 최종 bounded test의 FAIL을 구별하여 보존한다. 제품 timeout 정책/생산 코드는 수정하지 않았다.

## 보조 리소스 GET 부재 확인

실제 integration helper의 변경 diff를 읽었다. 직접 생성한 management SG ID 및 Nova keypair 이름에만 DELETE 한 번을 보내고, GET 404까지 100ms 간격으로 확인한다. configured shared SG/keypair early return 경로는 cleanup을 등록하지 않는다. DELETE 오류는 404 이외에 실패하며, GET 401/403/500 및 다른 오류는 부재로 취급하지 않는다. 1분 cleanup context에서 끝나야 한다. 이 범위에서 새로운 소유권 확대나 blind delete replay는 없다.

`integration_cleanup_test.go`의 지연 부재/401·403·500/취소 회귀를 외부 복사본에서 `-race -count=10`으로 실행하여 모두 PASS했다. `cleanup-absence-race.log`. 이 결과는 HTTP GET helper의 Fake closure 검증이고 실제 SG/keypair 404 관측을 대체하지 않는다. 그 실제 결과는 root의 opt-in 실행 Evidence에서 후속 확인한다.

## 수정 전 신규 finding F174-V002 / Low

- 위치: 신규 `internal/connector/provider/openstack/workspace_file_integration_test.go:218`.
- 재현: Linux OS 회귀 binary gate가 exit0, skip 문자열 부재, 일반 `PASS\n`만 검사한다. 다른 Go 테스트 binary에 존재하지 않는 `-test.run` filter를 적용해도 Go binary는 exit0, `PASS` 및 `warning: no tests to run`을 출력한다. 따라서 binary/filter 조합이 어긋나면 OS 회귀 실행 0건을 통과시킬 수 있다.
- 독립 재현: 로컬 `tools/dev-auth-class-fixture` test binary에 KT fixture ledger filter를 적용하여 위 결과를 확인했다. `wrong-filter-fixture.log`. PowerShell에서 native dotted flag를 반드시 quoted literal로 전달해야 하며 실제 실행은 `'-test.v' '-test.run' '^TestKTFixtureLedgerRejectsSymlinkAndReplacement$'`였다. binary 자체는 재현 산출물이고 저장소에 넣지 않는다. Linux ELF의 실제 실행을 이 Windows 재현으로 주장하지 않는다. Go testing의 zero-match 출력 의미를 확인한 것이다.
- 영향: 선택 테스트가 실행됐다는 검증 근거가 자동 gate에 없다. 실제 올바른 binary 실행 로그와 build/source identity를 전수 대조하면 이번 실행 증거는 별도로 판정할 수 있다.
- 필요한 회귀: 각 expected parent/subtest 이름의 `--- PASS: <name>`를 확인하고 no-tests warning, skip을 거부한다. zero-match/wrong-binary 출력이 거부되고 필요한 네 OS 경로가 모두 보이는 출력만 인정되어야 한다.
- root에 수정 전에 finding을 보고했다. 이후 required 네 OS 경로의 정확한 PASS 행을 확인하고 empty required/no-tests warning/skip을 거부하는 수정 diff를 검토했다. 이름 뒤 ` (` 경계도 확인하여 비슷한 접두 이름을 잘못 인정하지 않는다.
- zero-match, wrong binary, 일부 누락, skip, 접두 이름 혼동을 거부하는 새 `TestLinuxRegressionRequiresEveryRequestedTest`를 외부 복사본에서 `-race -count=10`으로 실행하여 모두 PASS했다. `linux-gate-race.log`. 실제 Linux 네 경로 실행은 root의 실VM Evidence를 후속 확인한다.

현재 단계에서 열린 추가 제품 결함은 확인되지 않았다. 관측된 T001/T002/T003 및 V002의 수정 경계를 확인했고, V001은 기존 DELETE ACK 검증 한계를 정확한 GET 부재 확인으로 보완했다. `probe-results.json`은 실제 관측한 process exit와 로그 SHA256, 복사한 일곱 소스 파일의 LF 해시를 보존한다. 전체 변경 및 최종 source/Evidence 연결 판정은 root가 새 소스를 고정하고 로컬 검증 결과를 전달한 뒤 수행한다.

## 후속 real PostgreSQL 실행에서 드러난 F174-T004 / Low

Root의 첫 PostgreSQL integration 실행은 `TestPreviewCreateReportsAnApplicationThatIsNotListening`가 기대한 502 `preview_app_not_running` 대신 504 `preview_target_unreachable`을 받았다. 이 결과를 단순 scheduling/timeout 추정으로 처리하지 않고 별도 조사했다.

- 수정 전 위치: base `0051d8d`의 `internal/server/preview/previewtest/peer.go:391–396`, `errors.Is(err, syscall.ECONNREFUSED)` 분류. 이 파일은 Connector 생산 코드가 아니라 Backend 테스트용 contract peer이다.
- 확인된 원인: Windows 실제 `net.DialTimeout`의 닫힌 loopback port 오류는 `syscall.Errno(10061)`, 즉 WSAECONNREFUSED다. 반면 Go Windows의 `syscall.ECONNREFUSED`는 application error 값 536870934이므로 기존 errors.Is는 false다. 무관한 VM_UNREACHABLE로 잘못 매핑한다.
- 독립 최소 재현 `windows_refused_probe.go`: 실제 listener를 열고 닫은 후 DialTimeout 2초를 세 번 실행했다. **모두 0ms, Timeout=false, raw errno10061, ECONNREFUSED 일치=false, Errno10061 일치=true**였다. `windows-refused-probe.jsonl`. 따라서 observed 약0.6초 테스트 실행 시간을 TCP timeout으로 단정하면 틀린다.
- 원본 tagged test 재현: 고정 외부 source와 로컬 임시 PostgreSQL16 server에서 원본 테스트를 세 번 실행하여 모두 같은 502→504 FAIL을 확인했다(0.79/0.63/0.61초). `before-preview-refused-integration.log`. `postgrestest`가 만든 무작위 `labbit_it_` DB만 생성·정리했으며 클라우드 호출은 없다.
- 영향: Windows에서 Backend error mapping integration의 false FAIL. 생산 SSH direct-tcpip의 `ssh.OpenChannelError`/forwarder mapping 결함을 입증한 결과가 아니다. 기존 Unix errno 분류를 유지하면서 Windows typed WSAECONNREFUSED를 인식하는 작은 helper와 wrapped refusal/timeout/무관 오류 분기 회귀가 필요하다. 문자열 검색이나 production timeout 확대는 이 원인 해결이 아니다.
- root에 수정 전에 finding을 보고했다. 수정 diff 및 최종 재검증은 후속 대상이다. 이 후속 finding으로 인해 위 시점의 8e9 source 498파일 검증을 앞으로 바뀔 소스의 검증으로 간주하지 않는다.

### T004 해결 재검증

Root가 사용자에게 먼저 보고한 뒤 `connectionRefused`를 platform별 typed errno helper로 분리했다. Windows는 기존 portable errno와 `golang.org/x/sys/windows.WSAECONNREFUSED`를 모두 인정하고, 다른 OS는 기존 portable errno를 유지한다. 새 dependency·Runtime·제품 forwarding 정책 변경은 없다. `peer.go`는 이름이 `_test.go`가 아니지만 Backend contract test 지원 코드이며, Connector 생산 SSH forwarding과 구별한다.

`check_t004_fix.py`가 네 fixture 파일을 외부 소스 복사본에 반영하고 LF 해시·실행 전후 working source 일치를 기록했다. `TestConnectionRefused* -race -count=20`에서 실제 닫힌 loopback과 wrapped error를 인정하고 nil/deadline/cancel/timeout/denied를 거부했다. 원본 PostgreSQL integration은 수정 전 3회 모두 FAIL에서 수정 후 **5회 모두 PASS**로 바뀌었다. `t004-results.json`, `after-refused-helper-race.log`, `after-preview-refused-integration.log`에 실제 process exit0와 로그 SHA256을 남겼다. 이 검증은 로컬 임시 PostgreSQL DB를 쓰며 cloud 호출이 없다.

## F174-T005 / Low — fixture 명령 실행과 SSH Setup 수명 분리

Root가 실제 Workspace fixture의 exec 실패를 관측한 뒤 사용자에게 수정 전에 보고했다. 이전 `workspace_file_integration_test.go:337–342`의 `CombinedOutput`를 `Session.Setup` 안에서 호출하는 코드는 전체 명령 종료까지 transport의 channel operation admission을 보유한다. Setup timeout은 채널·request 준비 budget이므로 오래 걸리는 설치/회귀 실행의 lifetime과 같게 취급할 수 없다. 원래 실VM 실패의 직접 원인이 이 경로였다는 wire trace는 없어 그 원인은 확정하지 않는다.

Root는 `workspaceFixtureOutput`를 분리하여 Setup 안에서는 stdout/stderr 연결과 `raw.Start`까지만 수행하고, command Wait는 별도 1분 caller context로 기다리게 바꿨다. 취소·timeout은 deferred managed session Close로 해당 채널을 정리한다. 출력 buffer는 Wait가 완료된 분기에서만 읽고 early error/취소에서는 읽지 않는다. 공유 transport를 취소하지 않는다.

독립 `review_workspace_exec_test.go`는 실제 loopback SSH peer로 다음 두 순서를 검증한다.

- exec Start ACK 후 exit-status/출력을 latch로 보류한다. 같은 Manager의 sibling OpenSession+Setup+Wait가 끝나기 전까지 latch를 열지 않는다. 명령 종료를 admission 안에서 기다리면 sibling caller 300ms budget 안에 진행할 수 없다. 새 helper는 sibling을 먼저 완료하고 이후 release된 명령의 stdout/stderr 전체를 받는다.
- exec ACK 후 caller를 취소한다. 외부 1초 select 안에 `context.Canceled`가 반환되고 SSH channel request stream 종료로 실제 CHANNEL_CLOSE를 확인한다. stdin EOF만으로 Close를 주장하지 않는다. 그 뒤 같은 SSH transport의 sibling exec도 성공한다.

수정 전 동작을 비교하기 위해 새 return wrapper 안에 원래 `CombinedOutput`-in-Setup 본문을 복원한 외부 overlay를 사용했다. base 원본에 없던 helper를 그대로 호출하는 compile 실패를 의미 있는 before failure로 세지 않았다. 최종 before는 3회 모두 `running fixture command retained SSH setup admission`으로 FAIL했다. 취소 경로에서 setup worker의 `output` 대입과 반환 caller의 output 읽기 race도 관측했다. 기존 diagnostic read와 같은 비동기 소유권 문제이며 root의 새 helper는 early return에서 buffer를 읽지 않아 해당 경계를 제거했다.

새 helper는 두 회귀를 `-race -count=10`으로 모두 PASS했다. `t005-results.json`이 fixture/probe LF 해시, working source 일치, 명령·실제 exit·로그 SHA를 기록한다. 최종 `before-workspace-exec-race.log` / `after-workspace-exec-race.log`만 이 before/after 판정 근거이다. 초안 peer의 stdin EOF 오해 및 adapter import 실수는 `v1-fixture-error-*` / `v2-adapter-import-error-*`로 따로 보존했고, 제품 실패나 최종 성공 근거로 사용하지 않는다. Root에 회귀 파일 경로를 전달했으며 repository 반영 및 최종 sealed source 연결은 후속 검토 대상이다.

## 재확인한 cross-file 경계와 남은 실제 실행

Supervisor는 `reconnect.go:156`에서 reconnect마다 새 `Client`를 만든다. handler가 captured sender pointer를 보유해도 새 Control connection으로 교체되는 동일 Client 객체가 아니므로 old result가 새 Control을 빌린다는 의심은 확인되지 않았다. owner cancellation/epoch 검증과 authority lock 내 registry detach, lock 밖 IO/callback 순서를 전체 코드에서 다시 읽었다. 확인된 추가 제품 결함은 없다.

일반 전체 Go의 18 skip은 현재 Windows 실행과 cloud opt-in gate를 나타낸다. 다른 실제 실행에서 일부를 통과했다는 사실과 기본 suite의 skip 기록을 동시에 보존해야 한다. 초기 real PostgreSQL T004 FAIL, 초기 File helper 미준비 FAIL, 초기 Workspace exec FAIL, 초기 Internet ON SSH/exec FAIL을 후속 PASS로 덮어쓰지 않는다. Internet 진단 재실행에서 ON 세 endpoint 연결 성공/OFF 비연결 및 원래 assertion PASS를 읽었으나, 최초 실패 원인은 여전히 확정되지 않았다. root의 최종 source/Evidence bridge, 실제 Linux·모든 관련 cloud lifecycle 및 source guard 기록이 완성되기 전 전체 A–F 판정은 보류한다.


최종502개 a7d source/전체27diff 및 Evidence 패키징 검토는 완료했다. 이후 최종판정·실행범위·한계는 [FINAL_AUDIT](FINAL_AUDIT.md), [패키징 보충](PACKAGING_ADDENDUM.md), [검증JSON](packaging-independent-check.json)을 따른다. 이 문서 앞부분의 단계별 pending 문구는 당시 이력이다.
