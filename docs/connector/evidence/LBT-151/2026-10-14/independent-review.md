# LBT-151 독립 최종 검토 — 2026-10-14

Reviewer: `/root/review_lbt149` (구현 agent와 분리된 reviewer).

판정: **현재 LBT-151 도구·회귀 테스트·Evidence·C2 인계 PR 범위의 pre-merge audit 통과. 미해결 confirmed blocker 없음.** 원격 push/PR 생성/merge/배포나 Jira 완료 처리의 승인은 아니다. 실제 Browser·Backend·DB를 포함한 LBT-22 공동 검증의 완료 판정도 아니다. 독립 기록을 포함한 최종 문서 manifest/checksum 생성 및 commit 후 소스 동일성 확인은 구현 agent의 마지막 기계적 확인 단계로 남긴다.

## A. 범위와 확인 방식

- Repository: `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-final-151`; branch `feat/LBT-151-ktcloud-final-regression`; base/실행 당시 HEAD/검토 당시 origin/main: `7917531fe630357eeb1f055ea2ad08dbee93a00e`.
- 개인 `PRE_MERGE_AUDIT.md`, repo `AGENTS.md`/`CONTRIBUTING.md`/`TESTING.md`를 적용했다. tracked 전체 변경과 새 acceptance/bootstrap 코드, 관련 production 호출자, 계약, 새 공개 Evidence를 읽었다. production/SSOT/web/dependency와 historical LBT-145/LBT-150 Evidence는 변경되지 않았다.
- 기존 acceptance 파일을 포함한 27개 Go 파일을 검토했다. 마지막 변경은 main의 첫 Cleanup 이전 active-close/helper-drain 순서였다. bootstrap의 historical 10개 복사본은 main/access/deploy 변경과 새 회귀를 구분했다.
- 최신 Jira LBT-151/21/22를 직접 읽고 요구·상태·댓글 경계를 확인했다. Confluence D-18 v5, D-20 v7, D-21 v10, D-26 v1, MVP v19, 테스트 설계 v3를 직접 대조했다. Connector README §6/7/7a 및 Terminal/File schemas, shared 타입, Runtime, HTTP/realtime/DB 경계와 대조했다. SaaS가 정상 File close1000을 소유한다는 계약을 적용했다.
- 검토자는 Cloud/API/VM/SSH를 실행하지 않았고 credentials나 protected 원본 ledger를 읽지 않았다. 실제 실행은 구현 agent가 수행했다. 공개 원본 로그·typed metadata·실행 코드·소스 및 binary manifest를 교차 확인하는 방식으로 actual 검증을 평가했다. 코드/원본 Evidence는 수정하지 않았으며 이 기록만 repo 밖에 작성했다.

## B. 요구 → 구현 → 자동 → 실제 검증

최종 actual attempt6의 full Control 로그와 단계별 구현을 대조했다. production Connector와 실제 KT API/VM/pinned SSH/PTY/rooted SFTP를 사용했고, SaaS Control/Data는 trusted TLS synthetic peer다.

- g1/g2 실제 PTY 입력·출력·resize·종료, reconnect와 production 60s grace 만료 및 종료 ID 재사용 거절을 확인했다. File Tree/Read/Save의 revision, 빈 Binary, NUL/non-UTF8, conflict, symlink/permission/large 경계가 포함된다.
- 두 PTY와 SFTP의 실제 SSH_CONNECTION 일치, 한 PTY 종료 뒤 나머지 PTY/File 유지, default 60s idle 이후 해당 cached TCP tuple 부재를 확인했다. 서로 다른 경로의 두 delay SFTP에 대해 실제 helper count2→1→0과 다른 File/PTY 유지 단계가 있다.
- cancel/disconnect/단일 File Data4001/default30s timeout에서 실제 helper present→0 및 다른 PTY 유지, no-retry를 확인했다. 소유한 테스트 VM의 SSH 서비스를 transient systemd rollback과 함께 중단하고 안전한 실패·명시적 복구를 확인했다.
- 실제 Save 결과를 synthetic application boundary에서 버린 뒤 old FileRequestID가 재실행되지 않음, fresh Read의 committed body와 5s no-redial을 확인했다. production Handler/FileManager/TLS driver의 red/green 자동 회귀와 연결된다.
- Reset 전에 active Terminal/File close와 helper0, g1 물리 자원 부재/g2 존재, g2 정상 접근 이후 ACTIVE g2 server에 stale g1 거절을 확인했다. **첫 empty Cleanup 이전** active-close/helper0 뒤 empty Cleanup SUCCEEDED, 후보의 known5 자원 보존, 실제 Cleanup5와 all-generation10 부재, g2 접근 거절을 확인했다.
- synthetic Terminal/File Data 연결 drain과 production child SIGTERM의 5s 이내 성공 Wait/forcedKill=false를 확인했다. 정상 branch의 실제 증거이며 forced-kill branch를 새로 주입한 증거는 아니다.

실제 attempt6는 19:15:31–19:30:35 KST 실행 기록이다. 전체 Control 로그의 35개 단계와 `validation-summary.json` 목록이 일치한다. 최종 성공은 이전 실패를 삭제하거나 운영 신뢰성을 입증하는 것으로 해석하지 않는다.

## C. 소스·자동 검사 확인

소스는 실행 당시 미커밋이었다. HEAD 하나로 실행 내용을 식별하지 않고 manifest의 전체 LF SHA256을 함께 사용했다.

- 공개 source-manifest의 467개 compiled Go/go.mod/go.sum 항목을 현재 checkout과 대조했다. actual build before/after, auto-run6 before/after의 467개 목록·해시가 모두 같고 stability=true다. 최종 Git index도 467개로 추가/누락 없이 전 항목 LF hash가 같다. CRLF 정규화만 허용된 비교다.
- 최종 deploy 로그의 Connector/harness/helper SHA를 해당 binary-manifest와 비교했다. g1/g2에 설치한 unchanged real helper SHA도 일치한다. 실제 binary 파일은 공개되지 않았으므로 reviewer가 재해시하거나 자체 실행한 것은 아니다.
- 최종 Go JSON 로그를 직접 재집계했다: 기본 3,638 PASS events/15 named SKIP/0 FAIL, 41 passing packages; 11 no-test packages는 SKIP와 별개다. fmt/vet/build exit0, 7-package race708 PASS/13 SKIP, 기존 반복420, File race74/1 SKIP, 도구 race20회2,500, File 반복320이 원본·summary와 모두 일치한다. PASS events는 부모 테스트와 subtest를 포함한다.
- 15 named SKIP는 11 cloud opt-in(9 OpenStack+2 KT)과 Windows에서 제외된 4 POSIX/symlink/replacement 항목이다. DB build-tag suite는 실행하지 않았으며 15개 중 DB 테스트라고 기록하지 않는다.
- web raw는 13 files/170 cases PASS, install/typecheck/lint/build exit0, tracked npm lock의 before/after SHA 동일이다. contracts raw는 OpenAPI/current schemas/Runtime/migration 검사 PASS 및 Reset schema11 PASS다. web/contracts 이후 도구 Go 변경을 이 검사들의 재실행으로 확대하지 않는다; 관련 web/contract 소스는 base와 동일하다.
- reviewer가 별도 실행한 것은 F013 직전 동일 27Go source에서 두 도구 package race, 이후 기존 successful-Cleanup access-fence/provider empty-Cleanup 회귀 race, local diff check 및 exporter/validator 구문·guard 확인이다. **최종 전체/race/20회 자동 검사를 reviewer가 다시 실행했다고 주장하지 않는다.** 최종 자동 결과는 원본 로그와 완전한 source 일치로 확인했다.

## D. 공개 raw/typed Evidence와 cleanup

검토 시 ZIP: 447 entries, 3,560,160 bytes, SHA256 `ff31cb5d23dfda630258e78c41a286d98551ac0a1e9a26d350a69be2c028fd1c`.

- ZIP entry가 모두 고유하며 manifest의 path 집합과 정확히 같다. ZIP CRC와 447개 published SHA/length가 모두 일치한다. 446개는 published=original SHA, 1개 attempt3 observe-ssh 로그는 IPv4 redaction이 명시되어 있다. standalone failed-attempt1..5 및 actual-final full Control 로그가 archive의 해당 bytes와 동일하다.
- original-failed1..5의 typed resource 집합이 각 current 집합에 포함된다. 각 집합은 5/5/5/5/10이며 final6은10이다. 중복 제거한 Lab resource40, bootstrap8, 전체48이 summary와 같다. bootstrap은 Server1/Volume1/Key1/Policy3/Public1/Forward1이고 기존 공유 DMZ Tier는 owned resource가 아니다.
- residual query 구현은 current journal의 모든 exact typed ID를 조회하며 exporter는 original⊆current와 attempt별 residual0을 강제한다. 공개 raw 재조회 로그·JSON과 aggregate를 대조했다. final6은 19:31:24, final seven-kind inventory는19:31:25, previous1..5 재조회는19:32:10 KST로 bootstrap cleanup 뒤 모두0이다. 원래/최종7종 inventory의 ID 집합이 정확히 같고 cleanup/deleted와 baselinePreserved 상태가 일치한다.
- attempt1의 최초 residual11 및 Cleanup UNKNOWN, recovery의 known present3/absent2, attempt2..5 당시 남아 있던 공용 bootstrap8 기록도 남아 있다. 나중의 잔여0으로 최초 nonzero 파일을 덮지 않았다.
- reviewer가 protected original bytes의 SHA나 실제 Cloud absence를 직접 재조회한 것은 아니다. typed export의 원본 SHA·보존 표시는 구현자 측 기록이며 공개 typed subset, 조회 코드, 안전한 raw query 결과와 chronology를 통해 평가했다. 원본 hash에 대한 구현 agent의 별도 대조 보고는 reviewer의 독립 검증으로 바꾸지 않는다.
- 같은 제한된 secret regex scan을 공개447 entry에 적용해 match0을 확인했고 typed field allowlist와 문서/로그의 저장 경계도 읽었다. binary/credentials/private key/provider config/Terminal/File body는 공개되지 않는다. 자동 테스트의 TLS 실패에 남은 loopback 주소는 실제 Cloud IP가 아니다. regex scan은 포괄적인 DLP 보증이 아니다.

## E. 실패·findings disposition

AUDIT의 수정 전 finding 위치·별칭·회귀와 실패1..5 full 로그를 대조했다. durable unknown-create receipt, File 조립, Tree/tuple 검증, 실제 g2 후 stale 검사, errors.Join, ANSI/fragment identity parser, completed-ID ACK 기대, distinct-path concurrent fixture 및 first-Cleanup 순서가 수정되었고 관련 자동/actual 증거가 연결된다. 현재 추가 confirmed blocker는 없다.

- F-OBS-151-003은 reviewer가 Connector-owned 정상 File close를 요구한 잘못된 판단이며 owning §7a의 SaaS close1000 계약으로 **기각**했다.
- F-EVD-151-014는 reviewer가 bootstrap을 Tier1/Policy2로 잘못 가정한 판단이며 기존 Tier 선택·Policy3 생성·원래 baseline membership을 독립 대조해 **기각**했다.
- attempt1의 exact ANSI bytes가 저장되지 않아 parser 결함과 최초 실제 실패의 직접 원인 연결은 추정이다. attempt2 default3m SSH Ready timeout과 attempt4 pinned SSH unavailable의 정확한 원인은 미확인이다. 같은-path 결함을 attempt4 actual primary cause로 기록하지 않는다.

## F. 남은 경계와 인계

proposed PR의 도구/테스트/Evidence 범위는 충족된다. README/C2-HANDOFF는 현재 auth/correlation/helper 설치/current Workspace·generation 입력, default budget, UNKNOWN reconciliation 및 lifecycle 순서를 구체화한다. 삭제된 테스트 VM/key를 C2 접속 입력으로 재사용하지 않는다.

후속 범위는 actual Browser refresh/sessionStorage/authority/Live, deployed Backend/DB/Relay 전체 왕복, full credential revoke+next Upgrade401, transport-level partial/unknown Save, network blackhole·운영 부하다. synthetic peer의 socket census를 Connector 전체 goroutine census로 해석하지 않는다. concurrent SFTP probe 파일은 최신 helper 한 개의 tuple이며 두 helper 각각의 tuple을 따로 기록한 증거는 아니다. File 결과 유실은 이미 받은 SUCCEEDED를 application boundary에서 버린 주입이다. forced-kill branch의 신규 주입 및 실제 PostgreSQL 실행도 없다. 이 경계를 유지한 C2 인계가 타당하다.

최종 CODEOWNER 리뷰/CI/Squash와 원격 작업의 사용자 승인 범위는 별도로 따른다. 문서/기록만 추가한 최종 commit 뒤 실행 소스 동일성과 artifact checksum을 고정하면 이 판정을 그 동일 소스의 PR에 적용할 수 있다.
