# LBT-174 라우팅·진단 timeout 추가 독립 A–F 검토 — 현재 최종 상태

2026-10-16 KST. 구현과 분리된 reviewer `/root/review_lbt173`. 사용자 AGENTS 및 `PRE_MERGE_AUDIT.md`를 직접 다시 읽었다. 제품 checkout·기존 Evidence·cloud·Windows 서비스·원격 Git을 변경하지 않았다. 이 폴더의 독립 fault fixture만 작성·실행했다.

**현재 판정은 보완 필요다.** 새 Go 단위/race 검증은 통과했지만, canonical DHCP 정책의 실제 guest 적용은 실패했다. 관리망의 배타적 Neutron DHCP 운영 조건을 준비한 뒤 새 소스로 ON/OFF·DHCP renew·재부팅·Reset·정리까지 검증해야 한다. Linux 독립 CI 성공이 나와도 그 자체는 이 라우팅 수용 검증의 대체가 아니다. 전체 사용자 요청 완료·Jira 완료·PR 가능을 아직 단정하지 않는다.

## A. 전체 diff·소스 고정

repo `tmp/labbit-connector-gates-20261015`, branch `codex/feat/LBT-174-preview-local-integration`, base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`. staged 0, 미커밋 code/contract **37파일(Go 35, contract/runtime 2)**이다. untracked Go 18개도 포함했다. [직접 재계산](source-current-identity.json): 520개 LF 입력 hash 차이 0, aggregate `86ec2c865b611a512eab3baabed7d238a102765d5095fd0c92a36372aba5b791`; Go/module 506개 aggregate `61aef239f619ff8a37ba703d12668d854ef6134ac7a7bf8a59850e4628b4bc3c`.

이전 a7d/502개 입력 중 변한 것은 `port.go`, `provision.go`, `provision_test.go`, `topology.go` 네 파일뿐이다. 새 Go는 management policy/회귀/affinity/diagnostic helper 네 파일이다. 따라서 기존 LBT-174 Preview 및 후속 timing/OS fixture 27개 변경의 code hash는 유지된다. 520 manifest에는 이번부터 관련 계약 14개도 포함되어 집계 기준이 넓어졌다. 502→520을 Go 18개 추가로 해석하면 안 된다.

| 변경 파일 | 목적·영향 경계 |
| --- | --- |
| `internal/connector/app/app.go` | shared VMAccess/Preview lifecycle 및 영구 shutdown admission |
| `internal/connector/app/vm_access.go` | Reset/Cleanup 결과별 logical Preview retirement, preflight 보존 |
| `internal/connector/app/vm_access_test.go` | mutation outcome 16분기의 pending/session 보존·퇴역 |
| `internal/connector/preview/manager.go` | dial 전 동기 reservation, duplicate/fence, retirement snapshot과 IO 분리 |
| `internal/connector/preview/session.go` | immutable attempt/socket identity, exact stream pair, bounded close |
| `internal/connector/preview/client.go` | pending Upgrade/ATTACH socket, cancellation handoff, full correlation |
| `internal/connector/preview/lifecycle_test.go` | late dial·stale ACK·blocked writer·generation·shutdown 격리 |
| `internal/connector/preview/upgrade_cancel_test.go` | 실제 loopback Upgrade reply hold 취소 |
| `internal/connector/wss/handler.go` | bounded Preview async workers, captured Control owner, direct replacement |
| `internal/connector/wss/handler_preview.go` | 선개설·결과·attach, credential/epoch snapshot, CLOSE matching |
| `internal/connector/wss/handler_preview_test.go` | 이전 WSS 종료 후 sequential OPEN fixture 정합성 |
| `internal/connector/wss/preview_owner_test.go` | old owner/trust retirement·shared result cancellation·64 worker 회수 |
| `internal/connector/wss/preview_replacement_test.go` | attached→idle session의 direct sender 교체 퇴역 |
| `internal/connector/provider/openstack/preview_transport_export_test.go` | 실제 App 재사용을 위한 test-only provider export, cleanup ID 전달 |
| `internal/connector/provider/openstack/preview_transport_integration_test.go` | opt-in 실제 App/Control/SSH/VM HTTP + Mock Gateway lifecycle |
| `internal/connector/provider/openstack/integration_test.go` | 보조 SG/keypair DELETE 후 exact GET 부재 확인 |
| `internal/connector/provider/openstack/integration_cleanup_test.go` | 지연 부재·권한·5xx·취소에서 false cleanup PASS 방지 |
| `internal/connector/provider/openstack/workspace_file_integration_test.go` | fixture exec Setup/Wait 분리·독립 deadline·session 회수 |
| `internal/connector/provider/openstack/workspace_file_fixture_test.go` | latch slow exec·sibling admission·cancel/peer close 실제 loopback |
| `internal/connector/provider/openstack/workspace_file_integration_gate_test.go` | 필수 Linux 경로 zero match/skip를 PASS로 처리하지 않는 gate |
| `internal/connector/provider/openstack/management_network.go` | 단일 직접 IPv4 관리 subnet preflight, canonical DHCP wire/echo 검증 |
| `internal/connector/provider/openstack/management_network_test.go` | 지원조건 mutation0, exact options, created ID 보존, Reset DELETE0 |
| `internal/connector/provider/openstack/management_subnet_affinity_test.go` | Nova 생성 뒤 subnet drift를 원래 선택값과 비교해 SSH 전 실패 |
| `internal/connector/provider/openstack/integration_diagnostic_test.go` | 전용 SSH transport로 취소 시 동기 exec join, 실제 loopback 회귀 |
| `internal/connector/provider/openstack/port.go` | owned Management port만 DHCP 정책, 생성 후 검증실패에도 ID 반환 |
| `internal/connector/provider/openstack/topology.go` | original subnet 전달·보존, ledger upsert 후 오류, Nova 전/후 검증 |
| `internal/connector/provider/openstack/provision.go` | 표준 Neutron preflight에 management 지원조건 적용, KT 경로 분리 |
| `internal/connector/provider/openstack/provision_test.go` | 표준 fake의 extension/subnet/Port 응답 및 DHCP 정책 지원 |
| `internal/connector/vmaccess/tcp_test.go` | 정상 SSH setup과 stalled TCP timeout 대상 분리, 외부 bounded join |
| `internal/server/realtime/trace_propagation_test.go` | 빈 log snapshot 대기, 완성된 malformed JSON 실패 유지 |
| `internal/server/realtime/trace_log_test.go` | 지연 matching log·malformed parsing 회귀 |
| `internal/server/preview/previewtest/peer.go` | test-support typed connection-refused 분류 사용 |
| `internal/server/preview/previewtest/refused_windows.go` | Winsock WSAECONNREFUSED의 typed 분류 |
| `internal/server/preview/previewtest/refused_other.go` | 비Windows ECONNREFUSED 분류 |
| `internal/server/preview/previewtest/refused_test.go` | 실제 wrapped loopback 거절과 timeout/cancel/permission 구분 |
| `runtime/contract.yaml` | 직접 IPv4·배타적 Neutron DHCP·검증된 Linux guest 지원조건 |
| `contracts/connector/README.md` | D18 표준 Neutron owned-port route 정책과 실제 적용 검증 경계 |

기존 Evidence 세 단계(초기 Preview, followup, network-race)는 역사적 결과로 보존된다. 새 routing-fix 패키지의 reviewed snapshot은 raw195/전체 seal244파일이며 [독립 전수 대조](package-independent-check.json)를 마쳤다. reviewer의 현재 최종 보고서·검증 파일 추가 뒤에는 root가 copy/raw hash/seal을 갱신해야 한다. 외부 helper·admin script는 실제 재현 의존성이며 제품 37파일 집계와 구분한다. 새 DB migration/HTTP/WSS wire/schema/dependency/web/CI 변경은 없다.

## B. 최신 원문과 추적성

[SSOT 직접 조회](ssot-refresh.json): Jira LBT-174 updated `2026-10-16T14:59:23.423+0900`, 댓글 0. D18 v5, D19 v7, D20 v7, 전략 v3, 플랫폼 v27, 결정완료 v11, 추가결정 v3. repo owning SSOT는 Connector §7b와 Preview control/data schema, runtime Management 설정, TESTING 및 관련 CI/HTTP/Realtime 계약이다. DB·web·dependency 변경 없음은 전체 diff와 hash 경계로 확인했다.

| 요구·AC/확정 원문 | 구현·자동 회귀 | 실제 검증·tested source | 현재 공백·판정 |
| --- | --- | --- | --- |
| Jira 기존 CC03 재사용·실제 HTTP 왕복 / §7b | App→Manager→SSHForwarder→Preview Data, 기존/새 lifecycle tests | 기존 source956c 및 followup 실제 VM+Mock Gateway 결과; 이번 기존27 code 동일 | 새 DHCP 정책에서 실제 Preview/HTTP·Terminal/File 재검증 필요 |
| full attach identity / §7b control/data | attempt-bound client/session, target/correlation 회귀 | 기존 strict Gateway 및 stale fault loopback, 기존 code hash 동일 | 실제 SaaS/Browser 인가 C3는 제외 |
| 1 WSS=1 TCP, active≤1, sequential / Jira AC2 | Manager reservation·session fence·sequential/duplicate tests | 기존 actual sequential/sibling isolation 및 현재 기본/race | 장기 부하·전역 goroutine0 주장 없음 |
| CLOSE/Data/upstream/Reset/Cleanup·stale generation / Jira AC3 | App mutation 결과, exact attempt retirement, owner epoch | 기존 actual gen1→2와 cleanup; 현재 자동 회귀 | routing-fix 새 actual lifecycle 대기 |
| safe APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE/UNAVAILABLE / Jira 범위 | Forwarder·handler mapping, test peer typed refused | 기존 actual 오류 경계, 새 기본 Go/11pkg race | 모든 실 Provider 장애를 주입한 것은 아님 |
| cancel/backpressure/shutdown·leak / Jira | bounded close·raw Upgrade handoff·64worker·diagnostic helper | 새 전용 SSH fault 5경로×5 `-race` + 기존 취소10×5, 현재11pkg race | 원격 프로세스 finally/route 부재는 별도 actual 증거 필요 |
| D18 Lab Router ON/OFF·관리 SSH 독립 | management preflight/canonical options, Lab port/SG/script 무변경 | numeric API 요청 실패→canonical 요청 성공; canonical2 actual 관리 default 잔류·renew FAIL | **미충족**. exclusive DHCP 준비 후 actual ON/OFF/renew/reboot/Reset gate 필요 |
| D19 immutable Snapshot/StartupScript·Reset 선검증 | Port 정책만 변경, original subnet identity, Reset rejection DELETE0 | checked-in 지원조건·Reset·fake request/body 회귀 PASS | 새 actual Reset에서 같은 policy 재적용 확인 필요 |
| 플랫폼 P5 runtime/인프라 소유권 | Git runtime 지원조건, 외부 DHCP 운영 조치 별도 | admin script stub만 검증; 실제 Windows service mutation0 | 배타적 DHCP는 운영 전제이며 API preflight가 경쟁 서버를 탐지하는 것은 아님 |
| 자동회귀·관련 CI/TESTING·재현 / Jira AC4–5 | 현재 full Go/vet/build/race 및 helper/cleanup tests | Go506 `61aef...` 기본3803/42pkg/18skip, race1487/11pkg/16skip, 실제 PG1305/5pkg/1skip; native C1 부모12/67PASS/0skip/0race, 원본 직접 집계 | 라우팅 실제 수용은 별도 실패. remote CI/push 미실행. 최종 reviewer 추가본 root 재seal 필요 |

초기 세 옵션 빈값 설계 권고는 [F174-RNET003](F174-RNET003.md)으로 철회·정정했다. 특정 DHCP 기법은 원래 Confluence에 확정된 정책이 아니며 D18 목표를 구현하는 새 Git 지원조건이다. 그 지원조건이 실제 배포에서 충족됐다는 증명은 아직 없다.

## C. 실패·보안·수명주기

기존 Preview 27 diff의 attempt/epoch·callback lock order·late result·pending cancellation·normal EOF 보존과 direct replacement retirement는 변경되지 않았다. 전체 code hash bridge를 확인했고 현재 전체 자동 회귀도 다시 통과했다. ephemeral Preview 세션을 durable 재시작 작업으로 확장하거나 새 wire/migration을 도입하지 않는다.

새 preflight는 extension/network/subnet 관계·단일 IPv4 DHCP·canonical prefix·SSH CIDR 전체 포함·Lab 비중첩·host routes 부재를 mutation 전에 검사한다. Reset의 기존 환경 삭제 전 이 검사가 실행되고, 지원조건 거절이면 DELETE0이다. KT D1은 별도 기존 경로로 분리된다. 실제 외부 DHCP를 API만으로 발견할 수 있다는 주장은 하지 않는다.

Port는 새로 생성한 Management NIC에만 두 canonical empty 옵션을 넣고 Lab/공유 subnet/SG/immutable StartupScript를 수정하지 않는다. 생성 후 정책 검증에 실패해도 확인된 Port ID를 ledger에 먼저 보존한다. 기존 이름 candidate는 owned로 채택하지 않는다. Nova 전/후 exact 조회와 원래 subnet affinity를 검증하여 drift 후 임의 새 주소에 SSH하지 않는다. SG egress0·host pin·direct SSH 지원조건을 완화하지 않는다.

실제 guest의 관리 DHCP lease에는 default router와 Neutron DHCP Port 목록 밖 server가 있었고 host VMware 설정/실행 서비스와 주소가 일치한다. 다만 Lab lease도 `serverIsNeutronDHCPPort=false`이므로 그 boolean만으로 Neutron 밖임을 확정하면 안 된다(OVN native DHCP에는 해당 Port가 없을 수 있다). 배타적 서버 변경 전후 새 VM의 lease/route와 lifecycle 검증으로 영향을 검증해야 한다. 최초 삭제된 역사적 VM의 정확한 socket/stage를 소급 복원한 것은 아니다.

나중에 실행한 read-only `lease-match` helper는 `READ_ONLY_DHCP_ASSOCIATION_PORTS=0`과 "no lease-origin conclusion"을 출력했다. 해당 시점에는 소유 VM/port cleanup이 진행·완료돼 live MAC→host lease 연계를 얻지 못했다. 그 helper의 PASS는 조회 helper 종료일 뿐 DHCP 응답 주체나 Internet 실패 원인의 추가 증거가 아니다.

추가 read-only `host-dhcp-observation.json`과 capture script도 읽었다. 관리자 false, 서비스 Running/Automatic, config SHA 동일, 단일 scope/pool/router 및 같은 IP의 과거 lease 시작 시각·MAC SHA가 기록됐으며 guest MAC 비교 false와 service mutation false가 명시됐다. FileShare.ReadWrite로 lease를 읽은 것은 서비스 상태를 바꾸지 않는 관측이다. 주소/시간/config의 일치는 경쟁 DHCP의 강한 정황이지만 guest DHCP ACK/정확MAC 또는 서비스 변경 전후 인과 대조를 대체하지 않는다. 이 두 추가 파일은 위 package195/244 snapshot 이후 들어왔으므로 마지막 root 재패키지/seal 대상이다.

전용 SSH diagnostic helper는 context가 owned transport를 닫아 Open/exec/Wait/output EOF의 블록을 해제하고 Run 완료를 join한다. 출력은 완료 후 읽는다. 이는 단순 timer 확대와 다르다. 원격 Python의 route cleanup은 별도 책임이다. 외부 helper의 timeout-after-add 응답 불명 누락은 수정됐고, exact lookup/delete·실패 전파 9경계의 무해 stub PASS를 확인했다. 실제 원격 alarm/absence marker와 최종 VM 삭제는 별도 actual gate로 유지한다.

관리자 helper는 전체 Windows `VMnetDHCP` 서비스를 stop+startup Disabled 하는 조치다. 단일 검토된 subnet scope와 설정 SHA gate는 현재 범위를 좁히지만 per-port/VMnet8 전용 API 조작은 아니다. 사용자가 관리자 실행해야 하며 현재 reviewer/agent에 의한 서비스 mutation은 없다. Restore는 설정 drift 거절 및 원래 Running/Stopped 복원에 대한 무해 stub 3경로가 PASS했다. 관리자 권한/실서비스 동작을 실제 검증한 것은 아니다.

## D. 실행·Fake·실환경·skip

[현재 원본 JSONL 독립 집계](current-raw-results.json)는 runner metadata source와 현재 Go506 hash가 같음을 확인했다. 실행 주체는 root이며 reviewer가 전체 Go/실cloud를 재실행했다고 표시하지 않는다.

| 범위 | 확인한 결과/한계 |
| --- | --- |
| 현재 full Go | `go test -count=1 -json ./...` exit0, PASS event3803/42 package/18skip/FAIL0. subtest 포함 이벤트 수 |
| 현재 race | 명령에 명시된11 package exit0, PASS event1487/16skip/FAIL0/data race 경고0. cloud opt-in OFF |
| 현재 vet/build | metadata exit0, Go506 source 동일. 빈 출력이 곧 exit0이라는 추론 대신 runner 종료값과 구분 |
| reviewer canonical DHCP/fake gate | exact wire2 empty/IPv4·Reset DELETE0/lookup 취소·지원조건/created ID 보존 PASS. 실제 guest 효과와 구분 |
| reviewer diagnostic SSH | 실제 loopback SSH protocol hold 5경로와 checked-in10 cancellation, `-race -count=5` PASS. cloud 아님 |
| reviewer remote Python | 실제 Go 생성payload6개 AST PASS. route mutation/failure9경계 stub PASS, 실제 프로세스·네트워크 호출0 |
| 새 실제 API v1 | numeric3/121/249 empty 요청의 생성 실패 기록 보존. created6+SG/keypair 정리. reviewer가 raw API body로 원인 확정하지 않음 |
| 새 실제 canonical2 | first12/timeoutcleanup/AB PASS였으나 관리 default 잔류 및 renew FAIL. 전체 성공으로 세지 않음. tracked8+SG/keypair404 정리 |
| lease-only 추가 실행 | lease/route 관측 phase와 소유VM 정리 PASS. 라우팅 수용·native CI 실행을 뜻하지 않음 |
| 새 실제 PostgreSQL | [원본 독립 집계](current-db-check.json): source61a 동일/실행 전후 불변, integration1305 PASS/5pkg/1skip(`TestDumpPreviewWireSamples`)/0FAIL, tagged vet exit0. runner가 시작한 PG의 소유 임시DB count0, query/stop exit0. 최신main migration8/worker 통합 미포함 |
| 새 native-independent | [원본 독립 집계](native-independent-check.json): Linux/amd64 Go1.27.1/GCC13.3/CGO1, CI의 tagged vet/build/race3명령 성공. C1 부모12개 전부 현재 소스의 함수명과 일치, 총67 PASS/0skip/0FAIL/0race, outer exit0/758.781s. source52086ec 원격 관측+host 전후 동일, archive26fd/Go pin63d hash 일치. tracked8+SG/keypair exactGET부재, Apt ownedroute부재, PGcount0/query0/stop0. 실제Linux+realPG+MockProvider이며 routing/renew/reboot/Reset acceptance 제외 |
| 과거 검증 | a7d Linux native tagged C1 12parent67test/race/skip0와 actual Preview/Workspace/PG 결과는 과거 source Evidence다. 이번520 source PASS로 옮겨 쓰지 않음 |

기본18skip은 opt-in 실제 cloud/KT14개와 Windows POSIX/파일권한4개다. 새 실행에서 전부 해소됐다는 주장은 없다. 원격 GitHub CI·KT 최종·실제 SaaS/Browser C3·장기 부하·hard crash는 미실행/범위 제외다. 공유 관리망이나 전체 cloud 모든 자원0을 주장하지 않고 run별 exact tracked+보조 자원 부재만 기록한다.

새 routing-fix 패키지를 원본과 전수 대조했다: raw195(75gzip), 모든 raw 원본SHA/사본SHA·길이 및 Markdown export 링크 변환 일치, gzip mtime0. base0051의 blob+37개 changed-source export로 520개 manifest를 복원해 LF hash 차이0이고 문맥 전용2파일도 일치한다. 기존 frozen followup275+network-race123=398개 원본 seal 불변, 현재 seal244개 hash 차이0·미seal파일0, 실제 `.go`0, 로컬 Markdown brokenlink0이다. reviewer heuristic secret 후보0이며, protected exact7 비교는 root가 기록한 metadata를 검토했으나 reviewer가 비밀값을 다시 읽어 전수 비교한 것은 아니다. 임의 secret 부재의 완전 증명으로 확대하지 않는다. 최종 reviewer 산출물 추가는 이 reviewed snapshot 이후이므로 root 재copy/검증/seal 대상이다.

최신 full lifecycle와 native-independent 외부 helper를 분리해 읽고 [실제 Go generator 호출](latest-generator-check.json)로 각각 Python 6payload(총12개)를 AST parse했다. 이 실행은 generator test만 anchored 선택했으며 실제 Python/cloud 호출0이다. [full route stub](latest-full-owned-route-check.json)과 [native route stub](latest-native-owned-route-check.json)에서 같은 응답불명9경계를 확인했다. native Apt는 compiler 설치만을 위해 owned route context1을 사용하고 full Apt는0이다. reboot는 실제 boot-id 변경과 pin된 SSH 회복을 요구하며 Reset 부분 실패도 두 generation의 확인된 자원을 cleanup ledger에 보존한다. 해당 helper는 Internet ON이므로 OFF·Preview·PTY/SFTP 실제 회귀를 대신하지 않는다.

## E. 수정 전 findings와 해결 상태

| ID·심각도 | 위치·재현·영향 | 상태·필요한 회귀 |
| --- | --- | --- |
| 기존 R001–009 / High~Medium | stale attempt/late dial/blocked Close/Upgrade cancel/old owner·replacement·shared Control write | 이전 독립 재현·수정 기록 유지. 해당27 source 불변, 새 전체 기본/race PASS |
| 기존 T001–005/V001–002/DOC001–002 / Low | timing false failure/falsePASS, helper exec·cleanup·OS gate, Evidence 복원/package 오염 | 이전 before/after 원본 유지, 해당 source 불변. 새 scope에서 재현성을 계속 구분 |
| 기존 F174-NET001(alias root N001) / Medium | accepted Linux + 이중 DHCP default metric100, Mgmt 경로 ON TCP 실패 | **미해결 actual 수용 경계**. 배타적 Neutron DHCP 준비 및 lifecycle 실제 검증 필요 |
| F174-RNET002 / Medium | 초기 `topology.go:160`에서 관측 subnet을 expected로 재사용, after-Nova drift가 성공/임의 SSH주소로 진행 | 원래 선택 subnet 보존으로 수정. 독립 before FAIL→after10회PASS, checked-in 회귀 추가 |
| F174-RNET003 / Medium | 초기 `management_network.go:64–66` numeric3/121/249 empty는 Neutron API blankable 계약 위반 | canonical router/classless 두 이름으로 수정·wire PASS. guest 적용과 별도 |
| F174-RMETHOD001 / Low | 외부 `local_probe_test.go:293` Apt raw Python top-level indentation | 수정 후 실제 generator6개 AST PASS |
| F174-RMETHOD002 / Low | 외부 `local_probe_test.go:273/292/347` add가 try 밖, timeout-after-apply에 cleanup0 | owned route contextmanager로 수정, exact lookup/delete9경계 PASS. actual 최종 검증 대기 |
| admin helper 복원 사전 관찰 / Low 수준 | 최초 Restore가 config drift/원래Stopped를 완전히 복원하지 않음, 전체 서비스 영향 표기 필요 | root 수정 후 SHAguard+원래 상태3경로 stub PASS. 초기 before JSON은 이미 수정본에 잘못된 fixture hash를 쓴 관측이므로 수정 전 결함재현으로 사용하지 않음 |

새 finding은 모두 source 수정 전에 root에게 severity/위치/조건/영향/회귀와 함께 전달했다. 최초 설계의 잘못된 세 빈값 권고도 역사 메모 상단에서 명시 철회했다. 확인된 code 결함과 가설·환경 운영 전제·실제 실패를 섞지 않는다.

## F. 독립 2차 검토 결론

구현자와 분리해 최신 원문을 직접 읽고 전체37파일 영향·이전27 source 불변·새 policy/ledger/Reset/cancellation을 검토했다. 실제 wire, after-Nova subnet drift, SSH protocol hang, generated Python 구문 및 route 응답 불명, admin restore에 독립 fixture를 적용했다. 새 코드에서 위 해결된 finding 외 추가 확인된 제품 결함은 없다. 그렇더라도 현재 실제 DHCP 적용/라우팅 수용 실패는 남아 있어 통과 판정할 수 없다.

**현재 최종 판정은 보완 필요다.** source52086ec의 기본 Go/vet/build/11pkg race/실제 PG와 별도 native Linux C1은 위 명령·Mock 경계에서 통과했다. 진단 timeout은 전용 SSH close+join 및 loopback fault 회귀로 개선됐지만, refined remote helper의 실제 최종 timeout/lifecycle 검증까지 완료된 것은 아니다. canonical DHCP 옵션을 사용하는 실제 guest의 관리 default 잔류·renew 실패가 남았으며 관리자 서비스 조치도 실행되지 않았다. 새 ON/OFF/renew/reboot/Reset·Preview/PTY/SFTP·exact cleanup의 필수 실환경 수용 검증을 충족하지 못했으므로 사용자 요청 완료·Jira 완료·PR 가능을 판정하지 않는다.

reviewer는 shared code/기존 Evidence/cloud/Windows서비스/원격 Git을 변경하지 않았다. parent가 관측한 서비스는 여전히 Running/Automatic이며 상태 저장 파일도 없다. administrator의 조치 전후 관측과 후속 실제 검증이 필요하다. 최신main 통합 실행도 미검증으로 유지한다. 현재 package195/244 snapshot은 독립 검토했으며 이 최종 보고서·검증 산출물 추가 후 root의 마지막 재copy/hash/seal이 남는다. audit를 push/PR/merge/배포 승인으로 해석하지 않는다.

### 최신 main 영향 추가 검토

이후 fetch된 main `27039422d48c39eb4edd41606fbc829156e43355`(LBT-177)의 원문 17개 변경을 영향 관점으로 확인했다. checkout HEAD와 origin/main은 여전히 `0051d8d...`이며 실행소스와 합치거나 rebase하지 않았다. [독립 비교](latest-main-impact.json), [재현 스크립트](latest_main_impact.py).

- HTTP OpenAPI, Connector/Realtime JSON schema와 go.mod/go.sum 11개 입력은 base 대비 byte/hash 차이 0이다. Connector·Preview/PreviewSession·Connector Router 제품 소스 변경도 없다. 문서 의미 변경과 wire 형식 변경을 구분한다.
- 최신 main README는 Provider `SUCCEEDED`와 Lab `READY`를 구분한다. 새 SaaS PROVISION writer는 자원과 성공 결과를 저장하지만 Lab은 `PROVISIONING`, 성공 Operation은 `VERIFYING_READINESS`에 둔다. 기존 PreviewSession 생성/attach authority는 `READY`를 요구한다. 현재 실제 Connector+MockGateway Preview 검증은 새 SaaS PROVISION→Preview 제품 통합 성공의 증거가 아니다.
- 새 migration 8 `operation_items.dispatch_not_before`, PROVISION-only worker와 api 동거 Router, lease/dispatch/write/shutdown은 최신 main의 새 실행 경계다. 현재 0051 기반 DB/native 결과가 migration8과 통합 worker를 검증했다고 표시하지 않는다.
- 현재 routing README/runtime diff는 외부에 복사한 최신 main 두 문서에 `git apply --check` exit0이었다. 이것은 문서 patch 충돌 부재만 증명하며 merged build·DB·실행 성공은 증명하지 않는다. 다른 제품 변경은 main과 경로가 겹치지 않는다.

최신 main의 제품 구현 전체에 대한 별도 승인 리뷰나 통합 실행은 수행하지 않았다. 이번 기능과 연결되는 계약·호출·상태 경계에서 추가 확인된 제품 결함은 없으며, 위 최신 main 통합 미검증과 현재 라우팅 실제 수용 실패는 최종 판정에 유지한다.
