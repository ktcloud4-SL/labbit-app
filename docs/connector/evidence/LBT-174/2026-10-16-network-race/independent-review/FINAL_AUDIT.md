# LBT-174 Internet 진단·Linux native race 최종 독립 검토

2026-10-16 KST. 구현과 분리된 `/root/review_lbt173`가 사용자 AGENTS 및 `PRE_MERGE_AUDIT.md` A–F를 적용했다. 주 checkout·기존 Evidence·cloud·원격 Git은 변경하지 않았다. 외부 분석/회귀 fixture만 작성했다.

**Linux native 특수 검증은 충족했다. Internet 동일 safe-error 실패 재현과 경로 A/B 조사는 수행했으나, 최초 역사적 VM 실패의 단일 원인 확정은 자료 부족으로 불가하다. 확인된 구성 의존 가용성 한계는 미해결이다.** 이번 검토를 원요청 전체 완료나 무조건적인 PR·rollout 승인으로 해석하지 않는다.

## A. 전체 diff·소스·Evidence 고정

저장소 `labbit-connector-gates-20261015`, branch `codex/feat/LBT-174-preview-local-integration`, base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`다. 제품 변경은 미커밋이며 staged 파일0이다. 이번 요청에서 Go·module·contract·runtime·DB·web·CI 소스 변경은0이다.

502 Go/module LF 해시 및 aggregate `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`를 직접 재계산했다. 이전 frozen followup source manifest와 전부 같고, 전체27 Go 변경의 경로 집합·각 LF hash도 이전 inventory와 정확히 같다. 기존 제품15파일과 후속 test/support12파일에 대한 전체 diff·cross-file 검토는 [이전 독립 A–F 감사](../../2026-10-16-followup/independent-review/FINAL_AUDIT.md)에 기록되어 있다. 새 소스로 바뀌었다고 이전 PASS를 옮겨 쓰지 않았다.

이번에는 외부 diagnostic overlay와 Linux 도구/전송/실행, 새 Evidence 전체를 추가 검토했다. host OpenStack 실행은 관측 overlay를 쓴 실행이고, native Linux는 overlay 없는 실제 a7d source를 검증·컴파일한 실행이다. archive source1,237개·vendor1,923개·SQL migration7개를 현 checkout/외부 vendor와 byte 대조했고 Go502도 일치했다. tar 중복/traversal/link/device0, 검사한 private directory/credential basename0이다. Go toolchain archive bytes/SHA는 저장된 공식 metadata 응답과 일치한다. reviewer가 공식 metadata를 새로 다운로드한 것은 아니다. [전송·소스 검증](method-source-verification.json).

새 Evidence의 **freeze된111 raw artifact**를 외부 원본과 전수 대조했다. gzip45개를 복원해 원본 bytes/SHA를 확인했고, v1 apt gateway role 치환과 Markdown 보관 확장자 링크 변환도 직접 재계산했다. 기존 frozen followup275파일은 seal과 파일집합·byte hash가 그대로다. 이번 보고서·checker·검증 JSON의 마지막 복사는111 snapshot 이후 추가되는 문서이며 새 제품 실행이 아니다. [패키지 독립 검증](package-independent-verification.json).

## B. 최신 요구사항·SSOT 추적

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) 원문·AC·댓글을 직접 읽었다. updated14:59:23.423+0900, 댓글0이며 본문은 이전 읽기와 같다. D18v5/결정완료v11/전략v3/D20v7/추가결정v3 원문도 직접 갱신했다. D18은 방법 검토 중 다시 조회해 v5를 확인했다. [SSOT 기록](ssot-refresh.json). repo 지침·Connector §7b/control/data schema·HTTP/Realtime·runtime·DB/backend의 적용 범위는 이전 전체27diff 추적표와 같다.

| 요구·계약 | 구현·검증 연결 | 실제 확인·남은 경계 |
| --- | --- | --- |
| LBT174 Preview HTTP 왕복·1WSS=1TCP·동시1/sequential·attach 식별·Reset/Cleanup·stale/cancel/오류·Terminal/File 회귀 | 이전 전체27diff 및 회귀/실제 VM Evidence, 현재502 source와 경로·hash 동일 | 기존 기본Go3753/42pkg/18skip·11pkg race·web188·realPG1305 및 실제 VM 결과를 이번에 다시 실행했다고 주장하지 않는다. 이전 frozen trace/실패 이력은 유지된다. |
| D18 결정7 ON LabRouter outbound, OFF no gateway | `provision.go:119–137,219–223`, `topology.go:127`, `integration_test.go:387–424`; 이번 첫 socket/role A/B | 새 실패는 remote assertion/code11까지 좁혔다. Lab/Management 경로별 차이를 확인했지만 일반 ON 가용성과 역사적 최초 단일 원인은 미해결이다. |
| runtime595–598 Management SG ingress22/egress0·관리경로 분리 | 기존 preflight/SG helper·host pin/관리 NIC 검증 유지 | SG 완화·공유 network 변경·StartupScript 변형 없이 조사했다. guest route/image/배포 지원조건의 소유권 결정은 후속이다. |
| TESTING/CI native Linux tagged C1 | `.github/workflows/go.yml:64–66` 세 명령, C1 정확12 부모, CGO1/GCC/nativeGo | vet/build/race 실제 성공. C1은 realPG/WSS/Handler/Dispatcher와 Mock Provider다. 원격 GitHub CI·KT API·SaaS/Browser 성공을 뜻하지 않는다. |
| Evidence 소스/실패/보안/cleanup 추적 | 원본 JSONL·run-state/result·SHA·정확한 부재 helper·PG 정리 |111 raw/45gzip·275 old seal·source502 전수 대조. per-run tracked8+보조SG/keypair 부재를 확인한 것이며 전역 cloud0을 주장하지 않는다. |

## C. 실패·보안·수명주기 분석

과거 오류 문자열은 SSH 또는 cloud-init 자체 실패를 뜻하지 않는다. `startup.go:59–105`는 key/TCP/handshake/channel/remote exit를 같은 safe readiness error로 평탄화한다. 독립 loopback SSH 7모드×10의 실제 race 실행에서70 child+10 parent PASS/0race로 이 alias를 확인했다. 어느 모드가 역사적 최초 VM에서 발생했는지는 알 수 없다. [초기 분석](README.md), [alias 실행](failure-alias-result.json).

새 v1은 authenticated readiness와 cloud-init 성공 뒤 remote exec exit1·고정 Python assertion·connectCode11/약5초 실패를12회 관측했다. 일반 route snapshot 중2회는 Lab이지만 실제 socket tuple은 수집하지 않았다. v2의 첫12 socket은 source/tuple Lab이고 성공했다. exact target `/32`를 Management에 고정하면 source/tuple Management로 실패하고 Lab에 고정하면 성공한다. rp_filter는 두 NIC 모두2였다. 이는 **경로별 가용성 차이**를 증명한다. 실제 SG packet drop과 NAT/상위 경로 각각의 원인 또는 역사적 최초 VM의 동일 원인을 모두 증명하는 것은 아니다.

hostroute A/B는 사전 부재·exact add·single probe·finally exact delete이며 공유망/SG를 바꾸지 않는다. v1/v2는 restored/ownedRouteAbsent marker가 있다. v3는 forcedLab 성공 뒤45초 timeout으로 restored marker가 없어 복원 완료로 계산하지 않는다. phase helper는 timeout 때 `session.Close` 뒤 `Run` worker를 즉시 join하지 않는다. 실패 상태를 유지하고 최종 소유VM 폐기를 route/프로세스·swap/패키지의 회수 경계로 본다. 이 외부 helper timeout을 제품 Preview lifecycle PASS 근거로 쓰지 않는다.

apt용 임시 Lab default는 disposable VM에만, 관리 SSH의 직접 subnet 연결을 유지한 채 PG reverse forward 생성 전에 사용했다. v1 raw는 networkd 포함 restart 기록 뒤 exact route가 이미 부재하여 finally delete가 실패한 것을 보여준다. restart가 소실 주체였다는 것은 강한 추론이다. v3는 list-only needrestart와 exact route 조회/삭제/부재 assert로 준비가 성공했다. 사용자 immutable StartupScript·host pin·Management egress0은 유지했다. SSH reverse PG forward는 양쪽 loopback에만 연결된다.

이전27diff의 owner epoch·attempt-bound 결과/Close·stale attach·pending upgrade cancel·worker admission·callback lock order·Reset/Cleanup/Shutdown·sibling SSH 분석은 source불변으로 유지된다. 이번에는 새 제품 프로토콜/DB/지속 설정/재시작 정책을 도입하지 않았으므로 그에 따른 새 migration·API 정책 검증은 해당 없다. 새 tool의 PG 소유권 finding과 정상/실패 cleanup을 별도로 검토했다.

## D. 실제 실행·Mock·skip·정리

| 실행 | 독립적으로 원본에서 확인한 결과 |
| --- | --- |
| v1 진단 | host Go child exit1, runner elapsed282.578초. 첫12 probe 실패. 정상 A/B/restored 후 apt helper exit1로 native 세 명령 미진입. tracked8+SG/keypair GET404, 임시DB0/query0/stop0. |
| v2 probe-only | host Go child exit0, runner elapsed134.062초. 첫12 Lab socket 성공, Management/Lab A/B 차이 및 restored 확인. tracked8+SG/keypair GET404. 이 stage에서 PG/nativeCI는 미실행. |
| v3 native Linux | native CI phase 실제 exit0/591.285초. Go1.27.1 linux/amd64·GCC13.3·CGO1·source502/a7d/차이0. vet/build/race marker 각각1. 정확 C1 부모12/12, 전체67 test pass(부모 포함)+package PASS6.132초, 누락/FAIL/skip/race0. |
| v3 전체 fixture | 별도 A/B timeout 때문에 host Go child exit1, runner elapsed945.985초와 parent937.38초 FAIL을 유지한다. native 성공을 전체 fixture PASS로 바꾸지 않는다. tracked8+SG/keypair GET404, 임시DB0/query0/stop0, source전후 동일. |
| safe runner 회귀 | 새 safe runner2개×start reject/normal/Go exit1/TimeoutExpired=8경우 독립 AST/stub PASS. 실제 PG·Go·cloud 호출0. 과거 실제 runner로 실행했다고 표시하지 않는다. |

`check_live_results.py`는 source에서 C1 부모 집합을 추출해 JSONL과 대조했다. root 요약·cleanup metadata도 이 raw 집계와 일치한다. [독립 raw 집계](live-independent-observations.json), [safe runner 회귀](safe-runner-ownership-observations.json). PG의 실제 stop 로그와 DB0 원본을 읽었고, 최종 no-server exit3는 root의 capture script/생성 metadata를 대조했다. reviewer가 별도 live PG status 조회나 cloud API를 실행한 것은 아니다.

`go list ./...`를 reviewer도 새로 실행해 exit0/53 package, LBT174 Evidence package0을 확인했다. 새 Evidence `.go`0이며 재현용 Go는 `.go.txt`다.111 snapshot의118 텍스트/압축해제 내용에서 검사한 secret 형식 후보0이다. 이는 휴리스틱이며 모든 secret 부재를 증명하지 않는다. 보호 입력은 reviewer가 읽지 않았고, root가 따로 수행한7 protected 값 exact 비교0과 구분한다. [패키지 검증](package-independent-verification.json).

원격 GitHub CI, 실제 KT 최종 검증, 실제 SaaS/Browser C3, 장기 부하·hard crash 및 역사적 삭제VM의 원래 socket/stage는 이번 실행으로 검증하지 않았다. 기존 전체18 skip을 모두 해소했다고 주장하지 않는다. native C1 실행 자체는 skip0이다.

## E. 수정 전 findings와 상태

| ID·심각도 | 재현·위치·영향 | 상태·회귀 |
| --- | --- | --- |
| F174-NET001 = root F174-N001 / Medium | accepted Ubuntu+Management DHCP의 두 default metric100에서 ON outbound 실패. 관련 입력 수락 `provision.go:219–223`, NIC `topology.go:127`, runtime592–598. D18 ON 기대에 대한 구성 의존 가용성 한계 | **미해결 한계.** LBT174 신규 회귀/특정 product code bug 책임은 확정하지 않는다. image/guest/Platform 지원조건·route 정책 소유권 결정과 두 DHCP순서/ONOFF/SSH/격리/Reset 회귀가 필요하다. [상세](NETWORK_FINDING.md). |
| F174-METHOD003 / Low | 외부 original `run_validation.py:20–33`은 start reject에도 PG query/stop. stub으로 start reject→stop1 재현; 기존 PG 중단 가능 | 사용자 수정 전 보고 뒤 새 safe runner에만 소유권 gate 적용. start reject→stop0, 정상/Go실패/timeout→소유 stop1을 두 파일8경우로 독립 검증. **새 재현 runner 범위 해결**, as-run 원본 불변. |
| F174-NDOC001 / Low | AUDIT 초안31/55가 networkd restart→route 소실 주체를 관측보다 강하게 확정 | 수정 전 보고 후 관측/추론 분리와 safeerror/새 remote assertion 구분을 직접 재대조. **해결.** 제품 finding 아님. |
| METHOD001/002·OBS001 | apt helper 초기 실패, v3 phase timeout/no join/restored 미확인, 역사적 safeerror 관측 제한 | 원본 실패를 보존했다. 제품 수정·전체 fixture PASS로 숨기지 않는다. 해소되지 않은 역사적 원인/timeout 경계는 잔여 위험이다. |

그 밖에 이번 추가 검토에서 새로 확인한 제품 코드 결함은 없다. 미해결 네트워크 한계를 단순 flaky test나 fixture 전용 workaround로 종결하지 않는다. Management SG를 풀거나 공유 subnet·사용자 script를 임의 변경하는 해결도 승인된 것으로 추론하지 않는다.

## F. 최종 독립 판정

구현자 설명에만 의존하지 않고 최신 원문·source/전체27diff 동일성·archive/vendor·초기 실패와 actual raw·정확한 C1 집합·cleanup·무해한 실패 회귀·111 artifact를 독립 대조했다. 이111 snapshot에는 이 최종 보고서 링크만 마지막 복사 대기이며 다른 깨진 로컬 링크는0이다. 마지막 report/checker 복사 후 작성자는 provenance/checksum을 갱신해야 한다. source/기존 실행 결과의 재검증을 새로 주장할 필요는 없다.

**Linux native vet/build/tagged race 미검증은 해소됐다. Internet 재현·A/B 조사는 수행했으나 최초 역사적 단일 원인 확정은 판정 불가이며, 일반 Internet ON 가용성 한계는 보완 필요하다.** 기존 Preview 코드·로컬 검증에 대한 제한된 감사와 이번 남은 한계를 분리한다. 전체 사용자 요청·Jira AC 완료, 무조건 PR 가능, 원격 CI/rollout 승인은 하지 않는다. push/PR/merge/배포·외부 메시지는 수행하지 않았다.
