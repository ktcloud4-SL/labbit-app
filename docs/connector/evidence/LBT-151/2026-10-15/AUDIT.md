# LBT-151 latest-main pre-merge audit — 2026-10-15

판정: **PR 가능 — 최신 main을 포함한 제안 LBT-151 도구·회귀·Evidence 범위의 audit 통과**.
개인 PRE_MERGE_AUDIT A–F를 적용했다. 원격 CI 또는 Jira 전체완료 판정은 아니다.
이 문서는 [원래 전체 audit](../2026-10-14/AUDIT.md)의 요구·실패분석·findings와
실제 KT 증거를 유지하고 최신 main 통합 후의 재검토를 추가한다. 원본을 수정하지 않는다.

## A. 전체 diff·버전 고정

Repo ktcloud4-SL/labbit-app, branch feat/LBT-151-ktcloud-final-regression.
기존 local HEAD8971ad595902391d85c8b2fb0c50145e32455557, 기존 base7917531fe630357eeb1f055ea2ad08dbee93a00e.
10월15일 fetch한 main c628031e9fb2b511f941504a29c91e1c59dbd734 위로 rebase하여
검증 HEAD62722d259a004e2a04d5f26a1af83e177ebc0e35를 만들었다. 충돌 없었고
`git range-diff 7917531..8971ad5 origin/main..62722d2`는 동일 patch를 반환했다.
시작 tree는 clean. 이후 local 변경은 이 날짜의 Evidence뿐이며 Go/웹/계약은 수정하지 않았다.
각 기본 검증의 before/after474Go/mod/sum hash와 HEAD stability가 true다.
웹은 같은 Git HEAD의 main tree이고 install adapter가 package-lock 불변을 확인했다.
최종 commit은 Evidence-only이며 같은 tested source인지 확인한다.

현재 PR base는 c628031이다. 전체 PR은 원래59파일(21logical changedGo+bootstrapREADME+
37Evidence)에 이번 Evidence가 추가된다. 27toolGo(기존6개 포함)는 실제 실행 LF source와
일치한다. 파일별 원래 역할은 원래 audit A의 전체표를 재대조했고 이번 변경은 아래와 같다.
staged/unstaged/untracked를 포함한 최종 전체 목록·artifact SHA는 artifact-manifest.json이다.
ignored .local의 원래 보호 credential/key/raw journal/binary는 원래 재현 경계로만 유지했고
오늘 내용·Cloud 상태를 읽거나 변경하지 않았다. 새 tmp 출력은 자동·Mock·정적 build 기록뿐이다.

| 범위·전체 파일 | 목적·검토 경계 |
| --- | --- |
| 기존59개 LBT151 파일 | tools/acceptance10, bootstrapGo11+README, 원본Evidence37. main통합 전후 Git diff0; 원래 A–F 의미·line·regression·known limits 그대로 |
| validate_main.py | inherited main의 Backend 기본 race·Session fence20회, Linux tagged vet/build/test compile; 실제 실행과 컴파일 분리 |
| compare_builds.py / binary-comparison.json | 실제4binary 대응. 일반 production2byte같음, fixture2는 CRLF-only overlay로 원본 재현. source나 Cloud 수정 없음 |
| collect_evidence.py | safe text/metadata allowlist·original SHA·IPv4 제거·counts/skip/source stability·upstream-only source변경 확인; binary/credentials 미수집 |
| README/AUDIT, independent-review.md | 재현·최신 판정·변경 영향·독립 검토와 한계 |
| source/integration-impact/latest-ssot/not-executed/validation-summary/default-suite-skips JSON | revision·source·요구·실행·Mock·skip·미실행을 연결 |
| validation-logs.zip / raw-log-manifest/publication-scan/artifact-manifest JSON | 원본결과·요약 일치·source 및 공개 artifact 무결성; 제한된 secret scan |

상속한 main21파일도 영향 검토했다. 이들은 이번 PR diff에 다시 포함되지 않는다.

| main 변경 | 소비자·영향과 재검증 |
| --- | --- |
| web8파일: LabSpecEditorPage/MappingFields 및 tests, router.test, contracts/labbitApi/labbitApi.test/mockLabbitApi | Mapping 후보 consumer UI, 같은 Organization/Provider의 활성 mapping·403/503·readOnly·draft 보존. HTTP producer/LabSpec auth 계약을 재검토; 기존 Terminal/File 경로와 직접결합 없음. 전체 web188PASS |
| app.go:207/229/291, control_stack.go:36/113, router_harness_disabled.go/test | 기본 hook 무동작, optional nil OperationSink, listener 수명. 전체 unit+Backendrace338PASS; 독립 default gate race20PASS |
| router.go:33/47/204/258, router_session_test.go:14 | 내부 optional SessionID, wire 미추가. zero는 기존 route; fence 안에서 교체 거절·pending0. 기본unit+Sessionfence20PASS 및 독립20PASS |
| router_harness.go/plan.go/test.go/integration_test.go, .github/workflows/go.yml, 안내/Evidence | Linux/POSIX opt-in 단발 known-ID 흐름. 기존 default빌드와 분리, 151 fixture는 import하지 않음. Linux taggedvet/build/test컴파일 PASS; POSIX·실PG 동작 실행 NOT RUN |

upstream source list는 integration-impact.json의21개 전체 이름이 기준이다. 위 web 범위는
실제8개 파일이다. tracked web count·source 집계는 원본 main diff에 근거한다.
main에서 Connector/Provider/VMaccess/File/PTY/runtime/contracts/db/dependencies 변경은 없다.

## 최신 요구·SSOT

Jira LBT-151 직접 조회: updated2026-10-15T14:38:36.564+0900, 진행중, comments0.
본문·AC는 원래 audit와 같다. 최신 main 반영·실제 OP02 정상/실패·0residual·재현/한계·C2인계가
요구이며 최종 실제 Browser/SaaS→VM 공동통합은 LBT-22다. Jira/Confluence를 수정하지 않았다.
직접 재조회한 D18v5/D20v7/D21v10/D26v1/MVPv19/검증전략v3는 모두 원래 버전과 같다.
링크·버전·수정시각은 latest-ssot.json이다. repo AGENTS/CONTRIBUTING/TESTING을 전체 재확인했다.

Git owning SSOT는 Connector README§6/7/7a/9/11–15와 관련 schemas, runtime contract/README,
realtime README/schema, HTTP OpenAPI의 mapping/auth/Terminal/File, DB migrations 및 Backend
README/auth-session이다. main 전후 동일함을 diff로 확인하고 영향 경계를 직접 대조했다.
upstream Harness docs는 제품 계약을 대체하지 않는다. SessionID fence는 내부 identity이고
wire/DB/Runtime field를 새로 만들지 않는다. source467→474는 Backend7Go 추가 때문이다.

## B. 요구 → 구현 → 자동 → 실제 증거

| 요구·AC·원문 | 구현·line | 이번 자동·검토 | 실제 검증·tested source | 공백·판정 |
| --- | --- | --- | --- | --- |
| 최신 main feature branch·build/unit/vet (Jira151, repo) | 이 폴더 validator 및 기존 validate.py:source/auto/build | c628031포함; fmt472/vet/build/3640PASS15SKIP,web188,계약Reset11; 전체source474stable | old실행에는 새Backend없음. main영향분석/자동재실행/실제binary등가 연결 | 현재 제안scope 충족, 원격CI 미실행 |
| 실제 VMSSH/PTY 생성·I/O/resize/close (D18v5/Connector§6/7) | op02.go:43/93/147/270; op02_ssh.go:65 | vmaccess/Terminal race, identityANSI·분할·TLSframe 반복 | Oct14attempt6 실제 g1/g2,80x24→120x40,exit7. oldsource tools/prod동일,productionbinarybyte동일 | actual날짜 유지, 오늘Cloud실행 아님 |
| CC02 reconnect/grace (D21v10) | op02.go:270/403 | ownership/reconnect race20회 및 defaultsuite | Oct14 Data재attach shell유지,60s grace/oldID거절 | 실제Browser refresh/credential continuity C2 |
| FileTree/Read/Save·revision/empty/rawbytes/path (§7a) | op02.go:183/215/270/381 | peernegative, File/Workspace race,unknownSave20 | Oct14 actual g1/g2 rootedhelper, binary/empty/conflict/symlink/permission/largePASS | Backend HTTP/Browserauthority는 C2 |
| 여러 PTY/SFTP 독립·공유SSH·idle (D18/Runtime1m) | op02.go:93/270; failures.go:97; ssh.go:101/150 | shared/cancel/race + distinctpath 동작 검토 | Oct14 2PTY+SFTP tuple,2SFTP helper2→1→0/other FilePTY생존,65s idleTCP소멸 | helper각각2tuple아님, latesthelper1tuple 관측 |
| unreachable/timeout/cancel/단절 (Jira151) | failures.go:31/187 | cancel/owner/revoke File20회,SSHcancellation race | Oct14 SSHsystemd중단+25srollback,30s timeout/helper0,PTY종료후freshrecovery | fullcredentialrevoke/blackhole/부분Save아님 |
| Reset/Cleanup·stale·generation 재사용금지 (D20v7/D26v1/§11) | main.go:464/505/538/566; failures.go:245 | foreigntuple/Reset/schema11, cleanupfence regression 유지 | Oct14 activeclose/helper0→Resetg2good→activeg2 staleg1거절→첫Cleanup전close→10knownabsence/currentg2거절 | 물리absence와runtimegeneration fence를 구분 |
| 결과불명reconcile·known-only (D20/§12/13) | bootstrap access.go:139/187/224; mainrecovery | unknownreceipt restartFake20회,negativeownership | Oct14attempt1 5known조회present3/absent2→3cleanup→allabsence; candidate미채택 | actualCreate응답유실주입 아님 |
| 모든 테스트자원0 (Jira151) | bootstrap baseline/residual,originalexporter | 원본oldEvidenceGit동일·ledger/absence코드 재검토 | Oct14Lab40+Bootstrap8=48exactIDs allabsent,7kindbaseline복원 | 오늘새Cloud생성0,오늘residual재조회 없음 |
| revision/repro/결과/KnownLimit/PR준비 (Jira151/검증전략v3) | source/binary/integration/rawmanifests/README/AUDIT | newfullchecks·source474一致·98rawlogs·restrictedscan | 원본6attempt/실패/48IDS 보존;fixture2overlayexact/prod2ordinaryexact | 원격PR 아직 생성안함, 본판정은local 제안scope |
| C2 Terminal/CC02·FileWorkspace 인계 (MVPv19M4/Jira151/22) | 원래 C2-HANDOFF/새 README | 최신mainconsumer경계 재검토 | 원래actual접속조건·helper·실패·한계 유지 | actualBackendBrowser→KT/LBT22 완료 주장안함 |

## C. 실패·보안·리소스 수명주기

원래 C의 전체 failure/security/lifecycle 시나리오를 최종 동일 소스에 재대조했다.
unknownCreate intent는POST전에durable 저장, inspection/restart가 blindretry를 허용하지 않는다.
inbox256 overflow·foreign tuple·falseTree·binary0/1/2·samepathlock·completedSaveIDno-redial·
oldControlowner·Reset/Cleanupgeneration·primary+cleanup오류·boundedWait·exactownedCleanup을 검토했다.
기존 regressed failures는 이번 race/repeat에서도 통과했다. tool-only scope라 새 migration이나
productauthority 우회는 없다. protectedcloudsecrets/PTYbody/Filebody는 이번bundle 입력이 아니다.

추가 통합 분석: 기본 Backend hook는env/file/Sink/goroutine을 만들지 않음, optional SessionID가
zero인 기존producer는 wire/pending 의미를 유지한다. nonzero SessionID의 교체거절은
WithReadyRoute fence안에서 mutation/reconcile전 등록·송신을 막으며 20회race로 확인했다.
defaultnewControlStack, optionalSink 및 shutdown의 기존tests도 Backendrace338PASS.
taggedHarness gate/ledger/POSIX·DB 동작은 이번 실행증거가 없으며 그 범위를 old PASS로 대체하지 않는다.
입력validation·operator attestation/knownIDs·correlation·UNKNOWNstop 설계만 원문과 code를 검토했다.
이 코드가 LBT151productionConnector또는fixture의 빌드의존에 추가되지 않았음은 binary등가로 확인했다.

실제KT부분은 원래내부goroutinecensus 없음, singleFile4001/fullrevocation 구분,
application-lostSave/partialwrite 구분, latesthelper1tuple/2SFTPcount 구분을 유지한다.
실패attempt2/4 SSH원인과 forcedkill새분기 미실행, 부하/blackhole/전체C2 미실행은 잔여범위다.

## D. 실행·Mock·actual·skip·미실행

모든 실제 실행 command/exit/time는 validation-summary와zip98entries에 연결한다.
Go3640PASS/15SKIP/0FAIL/41PASSpackage(+11no-test), Connectorrace708/13SKIP,
Terminalrepeat420,FileRace74/1SKIP,fixture20회2500,FileRepeat320PASS다.
Backenddefault2package race338PASS와 SessionFence20회20PASS가 추가됐다.
web14files188tests 및 typecheck/lint/build/npm-lock不変, exactcurrentcontractworkflow와 Reset11PASS다.
현재source474는before/after같고 기존467과의 차이는main소유3수정+7추가에한정된다.

자동은 unit/Fake/Mock/in-process TLS/SSH testfixture경계다. 원래actual은 productionConnector→
실제KTAPI/VM/SSH/PTY/rootedSFTP이나 SaaS는synthetictrustedTLS다. 오늘실제Cloud재실행0.
LinuxGOOS taggedvet/build/testcompile은성공했고, **taggedtest실행과PostgreSQLintegration은
NOT RUN**이다. host에Linux/PG환경이없다. default15SKIP과buildtag미실행을 섞지 않는다.
taggedHarness는upstream옵트인/151의존아님; 이번제안범위의 필수actualConnector재실행을
촉발하지않는다고 source/binary/consumer분석상판정했다. Linux/PG 전체원격CI는별도조건이다.

일반새production2byte동일. tool2일반hash차이는숨기지않고21GoCRLF-onlyoverlay를수행해
각각원래actualhash재현을확인했다. GoLFhash전체및source stability와함께원래actual의
적용근거로쓴다. 이것이새BackendTagged/Browser까지actual검증을확장하는근거는아니다.
raw/published98SHA·ZIPCRC·limitedsecret scan과summary일치를확인한다. 공개 bundle에는
binary/credentials/overlaysource가없다. scan은완전DLP보장이아니며 reviewer는보호원본·Cloud를직접읽지않는다.

## E. 수정 전 findings·disposition

이번 최신main통합검토 범위에서 **확인된 신규 결함 없음**. 코드수정도 없었다.
fixture일반buildhash2개차이는CRLF-only원본재현으로해소된 검증환경차이며 production결함이아니다.
원래 F001/002/004–013 수정·red/green·실환경재시도, F003/014기각 및 근거는
원래 audit E를 유지하며 이번 동일patch/race회귀에 다시 연결했다. 옛문제를재발로표시하지않는다.
Linux/PG실행부재 및 실제C2/credentialblackhole/부하한계는미검증범위이지 재현된결함이아니다.
새finding이확인되면 severity/file/line/조건/영향/필요회귀를수정전에보고한다.

## F. 독립 두 번째 검토

구현하지않은 /root/review_lbt149에게 동일PRE_MERGE_AUDIT와base/HEAD/full59파일+main21변경,
최신SSOT/새Evidence를제공했다. reviewer는Jira/producerconsumer/patch동일성을직접확인했고
defaultgate와pinnedSession교체거절을독립race20회실행해두packagePASS했다.
새4binary실제파일SHA와21overlayLFsource=current=oldactualLF,fixture2원본hash재현을독립대조했다.
최종 독립 판정도 제안된 PR 범위의 audit 통과이며 미해결 confirmed blocker가 없다.
reviewer는 공개 ZIP98개 및 접근 가능한 새 자동 출력 원본98개 SHA/length·CRC,
IPv4 제거2건·전체 테스트 집계·474 source·원래 actual ZIP 불변을 직접 대조했다.
후보75경로/15artifact 해시를 검증했으며 이 보고서 copy와 판정 갱신 후 최종76경로/16artifact
를 다시 봉인한다. 상세 범위·결과는 independent-review.md다.
CloudAPI/VM/SSH/보호credentials/원본privatejournal은reviewer가직접접근하지않았다.
별도reviewer의전체autorerun또는LinuxPGtaggedtest실행으로확대하지않는다.

상속 LBT-167 문서가 보존한 다른 base ebe3f42의 Linux unread Data replacement 테스트
실패는 이번 Windows PASS로 해결됐다고 표시하지 않는다. 현재 c628/151에서 재현된 결함으로도
단정하지 않는다. 최신 Linux/PG/원격 CI와 실제 C2는 미실행 범위로 남는다.

최신 main 통합·전체 기본/관련 회귀·source/binary 적용 근거와 독립 Evidence 리뷰가 충족됐다.
원래 실제 KT의 날짜·Mock 경계·48owned absence 관측과 미검증 한계를 유지한 제안 PR 범위는
리뷰 가능한 상태다. 최종 Evidence-only commit에서 같은 tested source와 전체 artifact를
마지막 확인한다. push/PR/merge/외부메시지 승인은 별도이며 이번 요청으로 수행하지 않는다.
