# LBT-151 최신 main 통합 최종 독립 검토 — 2026-10-15

Reviewer: `/root/review_lbt149` — 구현·검증 스크립트 작성과 분리된 reviewer.

판정: **현재 제안된 LBT-151 PR 범위의 pre-merge audit 통과. 확인된 추가 blocker 없음.** 이 판정은 최신 main 통합, 자동 검사, 동일한 실제 KT 실행 경로에 대한 기존 증거 재사용 및 공개 Evidence 추적성에 한정된다. Linux tagged/PostgreSQL 실행·실제 Browser/Backend→KT 공동 C2 완료·원격 CI PASS를 뜻하지 않는다. push/PR/merge/배포·외부 메시지 권한도 부여하지 않는다.

## 1. 범위와 확인 방식

Repository는 `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-final-151`, branch는 `feat/LBT-151-ktcloud-final-regression`이다. 최신 main/base는 `c628031e9fb2b511f941504a29c91e1c59dbd734`, 검증 HEAD는 `62722d259a004e2a04d5f26a1af83e177ebc0e35`다. 기존 151 HEAD `8971ad595902391d85c8b2fb0c50145e32455557`와 기존 base `7917531fe630357eeb1f055ea2ad08dbee93a00e`를 구분했다.

개인 PRE_MERGE_AUDIT.md와 현재 repo AGENTS.md/CONTRIBUTING.md/TESTING.md를 다시 읽었다. 기존 전체 59파일 diff는 이미 수행한 10월14일 독립 구현·A–F 검토와 연결하고, 이번에는 동일 patch 여부 및 최신 main 21파일의 producer/consumer·조립·계약 영향을 독립 검토했다. `git range-diff`에서 이전 151 커밋과 rebased 커밋이 동일 patch임을 확인했다. tools 두 디렉터리와 원래 10월14일 Evidence의 Git diff도 0이다. 이번 공개 bundle까지 포함한 75개 PR 경로와 artifact-manifest의 전체 목록이 일치한다. 이 보고서를 복사하면 그 파일과 갱신할 문서·manifest를 마지막으로 재봉인해야 한다.

리뷰 과정에서 실제 Cloud/API/VM/SSH/배포·secret·보호 private journal에 접근하지 않았고, 코드나 repo Evidence를 수정하지 않았다. 실제 인프라 호출 금지와 구분하여 Atlassian 요구사항을 read-only로 직접 조회했다. 이 외부 검토 보고서만 작성했다. 구현자의 결론만 채택하지 않고 Git diff, source, 빌드 바이너리 실제 파일, 공개 로그·metadata·ZIP을 직접 대조했다.

## 2. 최신 요구사항 및 계약

Jira LBT-151/167/53/21/22를 직접 다시 조회했다. LBT-151 본문 AC는 같고 상태는 진행 중, updated `2026-10-15T14:38:36.564+0900`, comments0이다. D-18 v5/D-20 v7/D-21 v10/D-26 v1/MVP v19/검증 설계 v3도 직접 재조회해 버전과 내용을 확인했다. 공개 latest-ssot.json은 그 버전·수정시각과 일치한다. LBT-22의 PR103 Loopback 보고는 이 tested main에 병합되지 않은 후속 증거이며 실제 KT/Browser 공동 완료 증거로 채택하지 않았다.

Connector README와 Control/Terminal/File/Reset schemas, Runtime, realtime/HTTP/DB/auth 계약의 관련 경계에 전체 변경을 대조했다. main에서 Connector/Provider/VM access/PTY/SFTP/File/shared jsonnum/Runtime/contracts/DB/dependency에는 변경이 없다. main의 Backend Harness 문서는 제품 계약을 대체하지 않는다.

AUDIT B의 11개 추적성 행을 구현 위치·자동 로그·원래 actual source/binary·한계와 대조했다. VM PTY/CC02/File/공유 SSH·독립 채널/실패 취소/Reset·Cleanup·stale generation/known-only reconcile/잔여 0/재현/C2 인계가 각각 연결된다. 원래 실제 검증의 미검증 부분이 최신 main과 전체 C2의 실환경 완료로 확대되지 않는다.

## 3. main 통합 및 수명주기 검토

main의 FE 8파일 변경은 기존 HTTP Mapping options consumer/선택기/Mock/회귀다. `/lab-specs/mapping-options` OpenAPI, Org/User query scope, 단일 Provider의 Mapping UUID 선택, 후보 부족·401/403/500·기존 읽기·미저장 draft 보존 경계를 검토했다. Connector target, File body, Workspace root나 Terminal/File wire 변경은 없다.

Backend 변경은 build별 기본 no-op hook, optional OperationSink, Router의 내부 optional SessionID fence다. nonzero SessionID만 `WithReadyRoute` 안에서 확인하며 교체된 Session의 pending 등록·송신을 거절한다. zero 값인 기존 producer 경로와 Terminal/File/Preview wire는 유지된다. tagged Harness의 operator gate·durable attempt·known IDs·correlation·UNKNOWN 중단 및 기본 build 분리를 검토했다. 이 Linux/POSIX+PostgreSQL opt-in 코드는 151 production Connector/실험 도구 실행 의존이 아니다.

Reviewer는 `TestDefaultBuildIgnoresHarnessGates`와 `TestRouterPinnedSessionRejectsReplacement`를 두 package에서 **race20회 직접 실행해 PASS**했다. app 3.858s, connector 2.076s다. 별도 reviewer가 전체 자동 suite나 tagged PostgreSQL 실행을 다시 했다는 의미는 아니다.

기존 동일 patch의 unknown-create durable receipt, no blind retry, exact-owned cleanup, bounded inbox/Wait, identity·correlation, unknown Save 재전송 금지, distinct-path SFTP 독립 취소, Control owner, Reset/Cleanup generation fence 및 최초·teardown 오류 보존은 원래 검토에 이번 회귀 결과를 연결했다. 새 confirmed 결함은 없다.

## 4. 실제 KT 증거 재사용의 독립 확인

새 ordinary build의 4개 바이너리 실제 파일 SHA를 직접 계산했다. production Connector는 `46f8fc426cc7acd628cb27a6a58ca9f4d81aa04075f01a71633a8bc44218b303`, SFTP helper는 `2ad7fce4f34425f300e597ff4093d655e649a15b43b519b55b4a9cb939b42804`로, 10월14일 실제 배포 byte SHA와 같다.

harness/bootstrap ordinary binary 두 개는 CRLF checkout 때문에 raw SHA가 다르다. 이를 숨기거나 일반 4개 모두 동일하다고 주장하지 않는다. `compare_builds.py`의 정확히 21개 changed tool Go 파일에 대한 CRLF→LF overlay를 독립 검토했다. 각 overlay bytes는 current raw의 LF 변환과 같고, 기록된 hash와 실제 실행 source LF hash도 전부 같다. 두 overlay 결과 binary를 직접 재해시하여 harness `99a098bbdeaf81e4f0cadfcf0325954588a75669f8a69d976162443d8669b07c`, bootstrap `464d08c9fe4b99cc89554ec71f4eeca5fbdae522bf207b4cdba4ae7ae1e501bd`가 원래 실제 fixture byte SHA와 같음을 확인했다. tracked source 수정은 없다.

production 2개 ordinary byte 동일, fixture 2개 LF-only overlay의 원래 byte 재현, 151 patch 동일 및 main 소비자 분석은 기존 실제 검증 재사용의 충분한 근거다. 이 변경만으로 같은 실제 KT 시나리오를 위해 새 유료 자원을 생성할 필요는 없다고 판정한다. 다만 actual 실행일은 계속 **10월14일**이며 오늘 Cloud 실행이나 오늘 residual 재조회가 아니다. 새 Backend/Web 전체 경로의 actual 검증으로 확장하지 않는다.

원래 10월14일 Evidence와 tools Git diff가 0이며, 원래 ZIP byte SHA도 `ff31cb5d23dfda630258e78c41a286d98551ac0a1e9a26d350a69be2c028fd1c`로 그대로임을 재확인했다. 6 attempt, 원래 5개 실패, exact-owned 48 IDs 부재 및 7종 baseline 복원은 그 날짜의 관측이다. 이번 검토자가 현재 Cloud 부재를 재조회하지 않았다. 원래 F003/F014 기각과 나머지 수정·red/green disposition도 변경되지 않았다.

## 5. 새 자동 검사와 공개 Evidence 무결성

새 source manifest **474개 Go/mod/sum LF hash**를 현재 checkout과 전부 직접 대조했다. auto/web/contracts/build의 before/after source 목록과 HEAD가 모두 같다. 이전 467개와의 차이는 main의 기존 3Go 수정·7Go 추가뿐이고 삭제는 없다. fmt 대상은 472개 Go다.

공개 Go JSON 이벤트를 직접 다시 집계하여 validation-summary와 대조했다.

| 실행 범위 | 직접 대조한 결과 |
| --- | --- |
| 전체 기본 Go | 3640 PASS events, 15 named SKIP, 0 FAIL, 41 PASS packages; fmt/vet/build exit0 |
| Connector race | 708 PASS/13 SKIP, 7 packages |
| PTY/Terminal/Preview race20 반복 | 420 PASS |
| File/Workspace/parser race | 74 PASS/1 SKIP, 5 packages |
| acceptance/bootstrap race20 | 2500 PASS, 2 packages |
| File owner/unknown Save race20 | 320 PASS, 2 packages |
| 최신 main Backend 기본 race | 338 PASS, 2 packages |
| Session fence race20 | 20 PASS |
| Web | 14 files/188 tests PASS; typecheck/lint/build exit0, package-lock hash 불변 |
| Contracts | 현재 OpenAPI/schema/Runtime/migration workflow exit0, Reset schema 11tests PASS |
| Linux tagged Harness | GOOS linux/amd64 CGO0 vet/build/test-compile exit0; 실행 아님 |

PASS는 parent/subtest events를 포함한다. defaultsuite의 `[no test files]` 11 package를 직접 확인해 named SKIP15와 구분했다. SKIP15는 실제 cloud opt-in11 및 Windows POSIX/filesystem4이고, Linux tagged/PG suite 미호출은 SKIP 수에 포함되지 않는다.

새 ZIP은 **98 entries / 630,912 bytes**, SHA256 `4e288045a164034259d788dfc615ea5f095e508bafeaa9b4ba8c5c1377b60946`다. 모든 entry의 고유 경로·CRC·공개 length/SHA와 raw-log manifest를 직접 교차확인했다. 외부 자동 검증 출력 원본 98파일의 실제 byte length/SHA와 originalSHA도 전부 대조했고, IPv4 redaction2건이 명시한 변환과 일치한다. Web188 및 Reset11은 해당 로그에서 따로 확인했다. 후보 artifact-manifest의 15 artifact raw/LF hash와 전체 PR75 경로도 일치한다. 이 기록의 repo copy·AUDIT 최종 판정 갱신 후 manifest 재봉인은 필요하다.

제한된 private-key/token/password 패턴 scan을 ZIP 전체에 독립 재실행해 0건임을 확인했다. collector의 입력은 이번 로컬 unit/Mock/static build text·metadata이며 binary/overlay source/보호 credentials는 공개 입력에서 제외된다. 완전한 DLP 검증을 주장하지 않는다. 공개 원본 SHA 확인은 접근이 허용된 새 자동 출력에 한정했고 보호된 Cloud 원본 journal 접근으로 확대하지 않는다.

## 6. 미검증 범위와 판정 한계

- Oct15 실제 Cloud/VM/SSH 및 residual 재조회는 미실행이다. Oct14 production Connector→실제 KT API/VM/PTY/rooted SFTP 증거를 동일 source/binary 경계로 재사용한다. SaaS peer는 synthetic trusted TLS다.
- Windows에 WSL/Docker/PostgreSQL/DSN이 없어 Linux tagged Harness/POSIX gate·PostgreSQL integration 실행은 **NOT RUN**이다. 교차 vet/build/test-compile은 실제 Linux/DB 실행을 대체하지 않는다. 이 upstream opt-in의 로컬 미실행은 새 151 실행 의존 실패가 아니다. 원격 Linux/PG CI는 별도로 확인해야 한다.
- 상속 LBT-167 Evidence는 다른 base `ebe3f42`의 Linux `TestSessionReleasesUnreadDataConnectionOnReplaceAndDetach/replace` 실패와 깨끗한 당시 main에서의 재현을 보존한다. 이번 Windows 전체/race PASS가 그 과거 Linux 실패의 해결 증거는 아니다. 현재 c628/151에서 독립 재현한 결함으로 단정하지도 않는다.
- actual Browser/Backend/DB/Relay/Live 전체→KT C2는 LBT-22 후속이다. PR103 Loopback 보고를 실제 KT 공동 통합 완료로 쓰지 않는다.
- 원래 application-lost Save/partial write 구분, 단일 File4001/full credential revoke+401 구분, latest-helper 한 개 tuple/두 SFTP process count 구분, socket 관측/전체 goroutine census 구분을 유지한다. blackhole·부분 Save·운영 부하·강제 kill 신규 분기는 미실행이며, 실패 attempt2/4의 SSH 원인도 미확인이다.

위 한계를 명시한 현재 제안 scope에서 요구사항→동일 구현→새 자동 검사→보존된 actual 증거의 연결이 성립한다. 미해결 confirmed blocker는 없으며, 최종 문서 copy·manifest 재봉인 후 파일 무결성을 한 번 더 확인할 수 있다. 이 검토는 원격 CI나 별도 실행·게시 승인 절차를 대신하지 않는다.
