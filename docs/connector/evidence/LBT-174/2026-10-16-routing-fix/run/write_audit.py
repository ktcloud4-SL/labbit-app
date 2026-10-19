from pathlib import Path
import json,hashlib,datetime,subprocess as sp
OUT=Path(__file__).resolve().parent;ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-routing-fix';DEST.mkdir(parents=True,exist_ok=True)
manifest=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'))
def load(p):
 return json.loads((OUT/p).read_text(encoding='utf8')) if (OUT/p).exists() else None
native=load('native-independent/native-run-result.json')
native_log=(OUT/'native-independent/linux-ci.log').read_text(encoding='utf8',errors='replace') if (OUT/'native-independent/linux-ci.log').exists() else ''
native_events=[]
for line in native_log.splitlines():
 try:
  event=json.loads(line)
  if isinstance(event,dict):native_events.append(event)
 except ValueError:pass
native_pass=[e for e in native_events if e.get('Action')=='pass' and 'Test' in e]
native_fail=[e for e in native_events if e.get('Action')=='fail']
native_skip=[e for e in native_events if e.get('Action')=='skip']
native_summary={'wrapper':native,'passEvents':len(native_pass),'topLevelPass':len([e for e in native_pass if '/' not in e['Test']]),'fail':native_fail,'skip':native_skip,'raceWarning':'WARNING: DATA RACE' in native_log,'completionMarker':'LBT174_LINUX_RACE_PASS' in native_log,'routingAcceptanceIncluded':False}
(OUT/'native-independent-summary.json').write_text(json.dumps(native_summary,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
native_text=('PASS' if native and native.get('exitCode')==0 and native_summary['completionMarker'] and not native_fail and not native_skip and not native_summary['raceWarning'] else '실행 중/미통과')
body=f'''# LBT-174 라우팅·진단 timeout 후속 audit — 2026-10-16

판정: **보완 필요**. 진단 취소/timeout 정리와 API 요청 형식은 수정·회귀 검증했다. 실제 VM에는 관리망 default route가 남았고 DHCP 갱신 gate가 실패했다. Windows 관리자 조치 후 실제 ON/OFF·갱신·재부팅·Reset·전체 fixture 최종 PASS는 아직 없다. push/PR/commit/stage/merge/배포/외부 메시지를 실행하지 않았다.

이 문서는 이전 [전체 LBT-174 audit](../2026-10-16-followup/AUDIT.md)와 [Internet/Linux race 조사](../2026-10-16-network-race/AUDIT.md)를 갱신한다. 과거 PASS를 변경된 라우팅 소스의 실환경 PASS로 전용하지 않는다.

## A. 버전·전체 diff

- Repo labbit-app, branch `codex/feat/LBT-174-preview-local-integration`, base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`; staged0, 모든 변경 미커밋.
- Go/module506파일 aggregate `61aef239f619ff8a37ba703d12668d854ef6134ac7a7bf8a59850e4628b4bc3c`. Go/module/runtime/contracts520파일 aggregate `{manifest['sourceSHA256']}`. [실행 manifest](run/source-manifest.json), [변경37파일 inventory](code-inventory.json), [tracked patch](tracked-diff.patch), [전체 변경 소스](changed-source/)를 함께 사용한다. 새 Go artifact는 `.go.txt`로 보관한다.
- 이전27 Go 변경의 목적/경계는 이전 audit A를 재검토했다. 이번에는 production `port.go`, `provision.go`, `topology.go`, 새 `management_network.go`, fake `provision_test.go`, 새 관리망2회귀/진단취소1회귀를 추가·수정했다(Go 총35파일). `runtime/contract.yaml`과 Connector README에 관리망 지원 조건을 명시했다. HTTP/WSS schema, DB, dependency, CI, Web 변경0. [37파일별 목적·영향 및 독립 A–F](independent-review/AUDIT_DRAFT.md)의 전체 표를 함께 적용한다. changed-source의 runtime/README.md와 db/migrations/README.md는 변경 파일이 아닌 링크 문맥 사본이며 [별도 inventory](context-inventory.json)에 구분한다.
- 관리망 조회/전처리, KT 분기, VM 주소선택, ResourceResult→Reset/Cleanup, SSH/startup/metadata consumer와 기존 Preview/VMAccess/App/WSS 호출 경계를 검토했다. 키/clouds.yaml/PG data/toolchain/archive는 보호·로컬 입력이며 Git에 포함하지 않는다.
- 원격 main은 read-only 확인 시 `27039422d48c39eb4edd41606fbc829156e43355`(LBT-177)로 전진했다. 특정 SHA만 fetch했고 branch/HEAD/origin/main은 변경하지 않았다. README/runtime/server app/operation/Postgres 변경과 새 migration이 있다. 현재 실행은0051기반이며 최신 main과 통합 실행은 미검증이다.

## B. SSOT 추적성

Repo AGENTS/CONTRIBUTING/TESTING과 PRE_MERGE_AUDIT A–F를 적용했다. 독립 reviewer가 Jira LBT-174 본문/AC/댓글 및 Confluence owning 문서를 다시 직접 확인했다([원문 버전](independent-review/ssot-refresh.json)): Jira updated2026-10-16T14:59:23.423+0900/댓글0, D18v5, D19v7, D20v7, 전략v3, Platformv27, 완료결정v11, 추가결정v3. 변경된 최신 main 계약은 별도 영향 검토하며 현재 브랜치 테스트로 통합 PASS를 주장하지 않는다.

| 요구·AC / 원문 | 구현·자동 테스트 | 실제 실행·Evidence | 공백·판정 |
| --- | --- | --- | --- |
| [Jira174](https://samsunglions.atlassian.net/browse/LBT-174) CC03 실제 SSH Preview / D18v5 | 이전 전체27파일 + App/Preview/WSS lifecycle 회귀 그대로 재검토 | 이번 fullGo/race/realPG, 이전 실제 SSH/MockGateway Preview 증거는 이전 audit B | 새 라우팅 소스로 전체 Preview cloud batch 재실행 없음. SaaS/Browser/C3 미실행 |
| full correlation/attempt owner/1WSS1TCP/active≤1/sequential | immutable attempt·CloseAttempt·Handler owner, stale/duplicate/backpressure 회귀 | fullGo3803PASS,11pkg race1487PASS, PG1305PASS | 이 결과는 in-process/Mock 경계를 포함하며 실제 운영 SaaS와 다름 |
| Reset/Cleanup generation isolation / D18 / Connector7b | 기존 generation fence, 관리 subnet 원래 선택값 고정, port resource ledger 보존 | Fake Reset preflight 거절 DELETE0/refs 보존, 조회 drift 거절 및 독립10반복 | 새 관리DHCP 정책의 실제 reboot/Reset 라우팅 검증 대기 |
| Internet ON=Lab Router, OFF=외부차단 / D18v5 / runtime | 관리망 IPv4/directSSH/비중첩/hostroute0 사전검증; ownedMgmtport canonical router/classless121 empty; Labport/userdata 무변경 | first12 TCP 성공했으나 Mgmt default 유지. 갱신 후 단일Labdefault assert FAIL | **미충족**. 단일 성공이나 API echo로 해결 판정하지 않음 |
| 관리SSH ingress22/egress0 및 topology owner / Platformv27/runtime | shared network/subnet/SG/immutable user-data mutation0, ownedport만 DHCP 정책; unsupported14입력 사전거절 | unit ON/OFF wire·exactSubnet·startup원문보존·조회404/401/500/cancel 검사 | 공유 SG를 열어 우회하지 않음. 외부 DHCP 배포조건 보장 필요 |
| StartupScript immutable body/SHA / D19v7 | provision existing snapshot 및 옵션 request 경계 테스트 | actual Provision startup success 후 first-series 수행; Fake ON/OFF에서 body정확동일 | 별도 guest staticnetplan 주입 없음; 관리 routes 제거 시 Lab metadata를 배포별 검증해야 함 |
| 진단 timeout은 실행 종료·정리를 관측 / TESTING | test helper integrationDiagnosticSSH는 dedicated transport cancel-close 후 동기 Run join; owned_route add도 try/finally | 실제1s alarm/정확route부재/다음진단PASS(v2). 독립 SSH5경로 race5반복, 생성Python6개 AST, route오류9조건 PASS | refined원격helper 실제timeout 재실행 대기. SSHclose가 임의remoteprocess종료를 보장한다고 주장하지 않음 |
| Linux 특수 C1 tagged-race / CI | exact vet/build/race3명령·officialGo1.27.1·vendored source hash·realPG reverseSSH | native 독립 실행 {native_text}, 상세 summary아래 | 이 별도 실행은 routing/renew/reboot/Reset acceptance를 명시적으로 제외 |
| Cleanup/실행소스/Evidence / TESTING | confirmedResourceID 보존·ownedcleanup GET404, source전후hash | 실패probe각6/8/8resources 및 SG/keypair 부재확인. PGDB0+stop. raw/provenance | 전체cloud 사용자자원0·globalgoroutine0·provider장애완전주입 주장 없음 |

## C. 실패·보안·수명주기

- 관리망은 IPv4 DHCP subnet1개, canonical CIDR, Connector SSH CIDR 전체가 직접연결 subnet 내부, Lab 비중첩, subnet host routes0이어야 한다. extension 부재/잘못된 alias/ID/status, 다중subnet, DHCPoff/IPv6/offsubnet/겹침을 mutation 전에 거절한다. Reset 사전 실패는 기존 generation을 지우지 않는다. KT 분기는 해당 Neutron 경로에 진입하지 않는다.
- 옵션3/121/249 numeric empty는 Neutron validator와 충돌했다. canonical `router`/`classless-static-route`만 명시적 빈값으로 보내고 null/누락/duplicate/IPv6/unknown/nonempty를 거절한다. 249 empty는 지원하지 않아 요청하지 않는다. 검증된 Linux DHCP guest만 지원하며 API 저장성공과 DHCP agent/guest 적용은 구분한다.
- CREATE 성공 후 옵션 누락을 발견해도 확정된 port ID를 먼저 ledger에 보존한다. Nova 이전 정책 재조회 및 Nova 이후 원래 선택한 subnetID affinity를 확인한다. 다른 subnet으로 drift한 주소를 SSH 대상에 사용하지 않는다. partial response 불명은 blind mutation retry로 바꾸지 않는다.
- Neutron 전용 DHCP라는 배포조건이 필요하다. 실제 관리 lease는 SERVER_ADDRESS172.16.8.254/router172.16.8.2였다. VMware config scope172.16.8.0/24/range128–254/router8.2, 서비스Running, 이번 VM주소135의 lease 시작08:43:22UTC가 관측되었다. 서로 일치하는 강한 외부 DHCP 증거지만 `network:dhcp` port 부재는 OVN native에서도 가능하므로 그 boolean만으로 DHCP backend를 확정하지 않는다. VMware 서비스 중지 전후 대조는 아직 없다. [호스트 재관측](run/host-dhcp-observation.json)에 서비스·설정 SHA·lease IP/시각 대조와 정확한 MAC 미비교 한계를 남겼다. 추가 read-only MAC 대조는 native VM의 정리 완료와 겹쳐 대상 port0이었다. [이 관측](run/lease-match.log.gz)은 원인을 뒷받침하는 PASS로 사용하지 않는다.
- Windows 세션은 Administrator=false이며 서비스 변경 권한이 없다. 검토한 configSHA+단일scope guard, 원래 service상태 저장, 실패복구 및 Restore SHA guard를 갖춘 [관리자 스크립트](run/Set-LabbitVMnet8Dhcp.ps1)를 준비하고 사용자에게 직접 실행을 요청했다. 실제 서비스 mutation0. 전체 VMnetDHCP stop+startupDisabled이므로 복구 전 향후 VMware DHCP에도 영향이 있다. 현재 OpenStack API8.105는 VMwarepool밖이며 lease파일에 없음. 이 관측만으로 모든 기존VM이 영향없다고 확대하지 않는다.
- 진단 전용 SSH client는 context취소 때 transport를 닫고 NewSession/Run/output worker 완료를 동기적으로 기다린다. production/shared PTY client의 수명을 변경하지 않는다. remote Python은 socket5s, ip/sudo8s, alarm30s, aptGNUtimeout300s로 제한한다. diagnostic cancel만으로 원격 cleanup을 확정하지 않는다.
- owned_route는 exact(gateway/dev/metric) 사전부재→try안 add→finally재조회/필요시exactdelete→부재 assertion이다. add응답 유실/미적용/reject/bodyerror/preexisting/lookup/deletefail을 구분한다. Apt 일반helper는 route를 수정하지 않는다. native 독립 검증만 temporary Lab metric40 route를 사용하며 이를 제품수정의 성공으로 표시하지 않는다.
- 기존 Preview/VMAccess/Terminal/File의 generation, staleowner, cancel, hostpin, credential비노출 분석은 이전 audit C를 재대조했다. durable Operation/DB/HTTP schema는 변경하지 않았다. hard crash/multiVM soak는 이번 검증 대상이 아니며 보장으로 기록하지 않는다.

## D. 실제 실행·skip·이력

| 실행 | 결과·한계 |
| --- | --- |
| fullGo | 3803 PASS events,42packagePASS,18skip,0FAIL,source61aef 전후동일 |
| vet/build/fmt | go vet/build PASS; canonicalLF504Go fmt PASS; CRLF바이트 그대로 gofmt-l을 사용한 초기 관측은 baseline다수 불일치로 실패해 별도 보존했고 canonicalLF로 다시 확인 |
| Windows race | 11package,1487 PASS events,16optin/OSskip,0FAIL/0race warning,139.094s,source61aef전후동일 |
| Web4종 | typecheck/lint/Vitest(14파일188test)/build PASS. Go정책수정 전caea에서실행. Web/lock/dependency/config/CI변경0 및 HEAD:web treea0ebfebd89642e48d9e432c58d92e421deaf1b77 불변으로 연결 |
| 계약 | officialCI버전 PyYAML6.0.3/openapi-spec-validator0.9.0/jsonschema4.26.0; OpenAPI/로컬ref/Draft2020-12/runtime/migrationmetadata PASS |
| realPG16 | 5pkg1305PASS/optionalwire1skip/0FAIL,366.5s. 새로운base0051+currentGo61aef에서실행. 임시DB0,종료확인. wireexport는 이전별도실행증거이며 이번skip을0으로바꾸지않음 |
| 독립 SSH/route | 실제loopbackSSH5경계 race-count5 PASS, 기존cancel10경로. generator6AST PASS, route오류주입9경계 PASS, 관리자service Restore stub PASS(실서비스0) |
| 최초numeric DHCP probe | Goexit1/resources6,VM생성전실패/cleanup확인. NeutronAPI호환결함을수정. [원본](run/failed-api-v1/probe-live.log.gz) |
| canonicalempty 실제probe | Goexit1/resources8; first12 TCP성공/intentionalalarmcleanup+freshnext/routeAB복구PASS, 양default남음. renewlab-first lease2변경후assertFAIL. [원본](run/failed-empty-policy-v2/probe-live.log.gz) |
| lease origin probe | Goexit0은관측helper완료라는뜻. 실제socket Mgmtsource5s/code11실패 재현, DHCP서버/router기록; Internet정책PASS아님. raw in run/probe-live.log.gz 및 dhcp-lease-origin.log.gz |
| native independent | {native_text}; passEvents{len(native_pass)},parent{native_summary['topLevelPass']},fail{len(native_fail)},skip{len(native_skip)},raceWarning{native_summary['raceWarning']},completionMarker{native_summary['completionMarker']}. 정확한 종료metadata는 [summary](run/native-independent-summary.json). 실제Linux+realPG+MockProvider C1이며 actualBrowser/SaaS 아님 |
| 실행대기 | VMware DHCP 중지후실제ON/OFF·첫12/renew2순서·rebootbootID·Resetg2·전체12OpenStack fixture, 최신main통합/원격CI/KT cloud/C3/TLS/부하 |

일반18skip=localOpenStack12+KT2+Windows4. Windowsrace16skip=cloud14+OS2. 기본skip을 실제실행PASS로 가장하지 않는다. 이번실제cloud는외부diagnostichelper가만든별도ownedVM이며원래12fixturebatch재실행이아니다. 이전historical첫실패VM은삭제되어당시socket/source가없다. 새결함재현이과거단한번의동일원인을증명한다고주장하지않는다.

## E. 수정 전 findings

사용자에게각finding을수정전에보고했다. 독립F174-RNET003은numericAPI문제이며 API400의safe원인판단과rawpayload미수집한계를구분했다.

| ID·심각도 | 당시파일·줄/조건·영향 | 수정·회귀/상태 |
| --- | --- | --- |
| F174-N001 Medium | 기존 provision.go219/topology127,2NIC equalmetricdefault가관리source를선택하면InternetON5s실패; D18Labegress·runtimeMgSSH와충돌 | ownedMgmtDHCP요청/지원조건추가. 실제default유지/갱신FAIL→환경조치후미해결검증대기 |
| F174-METHOD002 Low | 이전externalhelper95–135,timeout시sessionClose만하고Run/outputworkerjoin없음 | dedicatedtransportClose+동기Run; loopbackSSH5경계race5반복PASS |
| F174-RNET002 Medium | 첫수정 topology.go158–163,현재응답subnetID를expected로다시써다른subnet주소수용 | operationlocal원래subnetID고정,red재현→after10반복PASS |
| F174-RNET003 Medium | management_network.go 초기64–66/Unmarshal추가후as-run84–86,numeric3/121/249empty가표준Neutronvalidator거절 | canonical2wire정확검증/249미요청,unit+독립requestPASS; actual생성·조회통과 |
| F174-RMETHOD001 Low | external local_probe_test.go293,Apt Python상위indent로IndentationError | indent수정+실제Go생성6scriptAST PASS |
| F174-RMETHOD002 Low | externalhelper273/292/347,add가try이전→적용후응답timeout이면finally미진입 | owned_route try/finally exactcleanup+9faultPASS,Apt임시route제거; refined실원격후속대기 |
| 관리자helper Restore 보강 Low | 준비script Restore28–31에서설정drift/currentRunning복구누락분석 | SHAguard/WasRunningfalse Stop추가; 무해stubPASS. 최초beforefixture는이미수정본+잘못된hash라red증거로쓰지않음 |

## F. 독립검토·잔여위험·판정

구현과분리된기존reviewer `/root/review_lbt173`가전체diff/owningSSOT/source520일치와실패경계를검토했다. 새subnetaffinity회귀는reviewer초안을채택했으므로서로독립된두테스트설계라고주장하지않는다. reviewer는별도overlay에서canonicalrequest/실제loopbackSSH/fault주입/생성Python을실행했고실cloud실행은작성자의raw를대조했다.

필수 미통과는 실제 라우팅/DHCP 갱신과 후속 최종 fixture다. 필요한 관리자 환경 조치가 남았으므로 완료·PR 가능 판정을 내리지 않는다. 최신 main 통합 실행도 별도 미검증이다. audit는 push/PR/merge 승인이 아니다. commit/push/PR 등은 진행하지 않았다.

[패키지 검증](package-verification.json)은 원본 압축 해제 해시, 이전 두 frozen 패키지398파일 불변, 실행 소스520파일, base+변경37파일의 복원 일치를 대조한다. [민감정보 검사](secret-screen.json)는 사용 가능한 보호 입력7값의 exact 비교와 제한된 heuristic이며 모든 비밀의 부재를 증명하지 않는다. [링크 검사](markdown-verification.json)와 최종 `package-checksums.json`을 함께 제공한다. 원본 log는 gzip으로 보관하며, 검토 Markdown의 Go/log 링크만 내보낸 suffix로 조정하고 원본/변환후 해시를 raw-provenance에 모두 기록한다. 새 package의 hash 일치가 제품 acceptance 통과를 의미하지 않는다.
'''
(DEST/'AUDIT.md').write_text(body,encoding='utf8')
(DEST/'REPRODUCE.md').write_text('''# 재현과 다음 실행

현재 Git base0051d8d에서 code-inventory.json의 changed-source를 원래경로로 복원한다(.go.txt→.go). fullmanifest는LF정규화520파일을검증한다. 기존frozen275/123artifact는수정하지않는다.

WindowsGo1.27.1/GCCrace/Python3.12, 실제localOpenStack labbit-test, ubuntu/m1.small/sharednet1, SSHCIDR172.16.8.1/32, 보호clouds.yaml/SSHkey가필요하다. 민감입력은본package에없다. runtime관리망지원조건을먼저읽는다.

run의최종local_probe_test.go.txt→외부임시local_probe_test.go로복사하고 overlay.json의가상테스트경로/실제helper경로를새checkout에맞춘다. production overlay는없다. run_validation.py의ROOT/OUT을설정한다. build_sources.py source는고정Go/module/runtime/contractsmanifest와vendor포함archive를생성한다. officialtoolchain파일과SHA는linux-go-download.json을따른다. archive는Git에포함하지않는다.

- verify.py go/web/race(기본9pkg이므로최종11pkg은final_checks.py+LBT174_CHECK=race), run_db.py, check_format.py, final_checks.py로기본검증을재현한다. 계약validator는CI고정버전을설치한외부target디렉터리로준비한다.
- 관리자는검토된Set-LabbitVMnet8Dhcp.ps1 -Action Disable을명시적으로실행한다. 단일VMnet8scope/SHAguard가맞지않으면중단한다. VMnetDHCP서비스전체중지+비활성화이며미래VMwareDHCP에도영향이있다. -Action Restore는설정SHA를확인한후저장된시작유형/실행상태를복구한다. OpenStackAPI8.105가계속접근되는지확인한다. 관리자조치를이문서에서실행했다고주장하지않는다.
- 관리조치후 run_validation.py probe로exactfirst12/단일Labdefault/remotealarmcleanup/후속진단/routeAB/renew2순서를검사한다. helper full은nativeCI후실제rebootbootID+SSH/renew2순서/immutableResetg2후first12+renew2순서를포함한다. 실패시exactresourceledger로정리하고SG/keypairGET404를확인한다.
- native-independent/run_validation.py는routing/renew/reboot/Reset을제외한독립nativeCI다. apt단계만ownedLabmetric40route를사용하고finally부재확인한다. 이를라우팅수정성공이나전체fixturePASS로표시하지않는다. PGstartownership이있을때만DB0조회/stop한다.
- 보호env+필요한LinuxWorkspace/KTtooling testbinary를이전followup REPRODUCE대로준비한뒤 verify.py live-all로12fixture최종batch를실행한다. zero-match/skip은실fixturePASS가아니다.

이후원격main2703942와통합한별도소스로검증을갱신한다. 현재기록은0051기반이다. push/PR는사용자의다음명시지시전수행하지않는다.
''',encoding='utf8')
print(json.dumps({'audit':'보완 필요','native':native_text,'sourceSHA256':manifest['sourceSHA256']}),flush=True)
