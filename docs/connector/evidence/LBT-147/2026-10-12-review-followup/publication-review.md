# LBT-147 PR #84 — 독립 최종 게시 검토

2026-10-12 KST. 판정: **검토한 LBT-147 PR 범위에서 R1/R2/R3 및 게시 helper PUB-NC/PUB-IG/PUB-EG 수정을 수용하며 PR 리뷰 재요청 가능**. 최신 패키지의 source pin·archive·artifact 집합·요약 원본 정합성도 독립 확인했다. 확인된 미해결 production finding은 없다. 이 판정은 CODEOWNER 승인·원격 CI 성공·merge·배포 또는 KT Cloud 선행 작업 전체 완료를 뜻하지 않는다. 사용자의 최신 업로드 승인은 구현자가 별도로 관리한다. reviewer는 push/PR/Jira/댓글 전송 또는 cloud mutation을 하지 않았다.

## 기준과 전체 검토 범위

| 항목 | 확인한 값·범위 |
| --- | --- |
| 저장소 / 브랜치 | ktcloud4-SL/labbit-app / feat/LBT-147-vm-access / PR84 |
| 최신 origin/main / merge-base | e850567066134edc87b2ba543d0eb42a21f406b8 / 6d675c74bff11f481d247267faefd4ce960dcd38 |
| 실제 테스트 소스 | ed7843e25be99f414bfb0087d2f07b8121fe08a0 + 11수정 파일(5 production·6 tests) |
| 게시 코드 커밋 | b191ecdbe87f28c2c2e34d28877f5de84b18f082. source-manifest의 54파일 byte SHA 및 각 commit blob 일치, 불일치0 |
| 전체 diff | 최초 구현·P1/P2·현재 R1/R2/R3를 포함한 코드/설정/테스트27 + 역사적 Evidence27 = 54파일. 새 Evidence23파일(이 보고서 포함)도 별도 게시 검토하여 총77파일. 수정11만 검토한 것이 아님 |
| SSOT | 사용자 PRE_MERGE_AUDIT 및 repo 지침, Jira147/21/145 본문·AC·댓글, D18v5/D20v7/D21v10/strategyv3/MVPv19, HTTP0.1.5/Connector·Realtime8schema/runtime0.1.7, backend 책임·flow. 원문을 직접 읽고 최신 body/version/comment 불변을 대조 |
| 최신 main 영향 | merge-base 이후 frontend deployment handoff 문서만 변경. backend/runtime/VM Access 계약 영향 없음. main에 merge/rebase하지 않은 상태를 명시 |

전체54파일별 기록과 AC→구현/호출자→자동 테스트→실환경/공백 표는 `independent-review.md`와 `file-review.md`, `AUDIT.md`에 있다. 변경 없는 source16도 현재 바이트 및 적용 분기를 다시 읽었다. 실제 Adapter lifecycle의 부분 삭제 결과 producer, server pin enrollment, auth context/timeout, Control envelope/error consumer, PTY/Data pump와 Supervisor sender 수명까지 확인했다. 변경 없는 HTTP/DB/browser/infra의 전체 acceptance를 수행한 것으로 확대하지 않았다.

새 PR 본문 초안 `LBT-147-review-followup-PR-body.md`도 전체147/R1-R3/current2856/392/600·실제30 tracked cleanup·pending SSH/blocked WSS·새CI/review 대기 문구를 대조했다. `__EVIDENCE_COMMIT__`는 게시 Evidence 커밋 후 실제 SHA로 치환해야 한다. 과거 `LBT-147-PR-body.txt` 초안은 이번 게시 본문이 아니다.

## findings 해결과 동작 근거

| finding | 독립 확인·판정 |
| --- | --- |
| 기존 F1/P1 partial FAILED generation fencing | 단순 preflight는 active channel 보존; destructive/불확실 FAILED는 retire/fence. 실제 Adapter HTTP producer와 실제 OpenStack app harness의 부분 삭제·replacement 분기를 대조. 수정 수용 |
| 기존 F2/P2 lazy initialization waiter | network factory가 짧은 mutex 밖에서 실행되고 shared attempt waiter가 자기 ctx로 이탈. owner 취소/실패 공유/다음 요청 retry/cache/ID 검증 및 실제 Keystone 응답 gate 결과 확인. 수정 수용 |
| CODEOWNER R1/P1 channel cancellation | 옛 production overlay에서 A echo EOF 두 분기를 독립 재현. 현재 B open/PTY/SFTP timeout/cancel은 A echo 유지. late accept 정리, transport 재사용, canceled waiter 누적 제한과 별도 close-ACK 무응답 fixture 검증. 수정 수용 |
| CODEOWNER R2/P2 operational error | 옛 Nova/Neutron503이 INVALID_SESSION인 것을 독립 재현. 현재 lookup/auth/decode/config/notACTIVE/noIP는 safe ATTACH_FAILED, 명시 target/ownership/stale/server404만 invalid. actual Adapter→Manager→Handler fixture와 raw body/IP redaction 확인. 수정 수용 |
| CODEOWNER R3/P2 shutdown order | 옛 runtime overlay의 Control ENDED 누락 재현. 현재 Terminal admission 차단→SERVICE_RESTARTING Data/Control 각각1회→SSH 종료→Control close. delayed OPEN 거절·ordinary reconnect hook0 확인. Data reason 경쟁은 모든 red schedule에서 재현한 것은 아님. 수정 수용 |
| PUB-NC-1/P2 default 재현의 inherited cloud flags | 수정 전 recording subprocess로 inherited10flag1이 auto Go child로 전달됨 확인. 현재 outer/default 및 direct portable auto10flag0, explicit cloud runner만 선택4flag1, input 누락 시 child0. 마지막 helper SHA로 독립 stub5케이스 PASS, 실제 Go/cloud0. 해결 |
| PUB-IG-1/P2 broad node_modules guard 예외 | 수정 전 외부 최소 Git fixture에서 visible ignored Go를 guard가 허용했고 실제 `go list ./...`에 포함됨 확인. 현재 `.pnpm/` hidden subtree만 예외; hidden example 허용/visible package 거절 독립 PASS. Go test/cloud0, Go list만 실행. 해결 |
| PUB-EG-1/P2 Evidence prefix guard 예외 | 수정 전 Evidence/visible/audit.go가 guard PASS이지만 go list에 포함됨을 확인. 현재 visible Go/go.mod/go.sum 거절, docs·hidden `_fixtures` sourceguard 허용. Artifact complete 허용/extra·missing·changed 거절까지 독립9case PASS. Go test/cloud0. 해결 |
| 게시 helper·문서 보완 | Go 혼합 package fixture는 `_fixtures` 이동 후 전체 Go 재실행으로 해결. validator PYTHONPATH는 기존 값 보존, Reset11 재실행. README의 이동한 PS helper 경로 일치. 임시 Python bytecode는 새 `.gitignore`로 실제 Git 게시집합에서 제외됨 |

새 findings는 수정 전에 파일/라인·구체 순서·영향·회귀를 구현자에게 보고했다. `independent-review.md`는 당시 snapshot이며 이 보고서가 마지막 게시 helper 보완 상태를 추가한다. 원본 red/build/환경 실패와 skip을 삭제하거나 현재 PASS로 바꾸지 않았다.

## 실행 원본·무결성 및 재현

마지막 PUB-EG 보완 후 독립 스크립트로 **source54 byte+committed blob, checksum 대상22artifact, ZIP220 unique/exact entry set·CRC·SHA256·size 모두 PASS**를 확인했다. 실제 Git 게시집합23파일은 checksum 대상22+checksum파일과 정확히 일치했다. 생성 cache는 `git check-ignore` 결과로 제외를 확인했다. root guard는 source14/artifact7의21case PASS이며 독립 `--verify-only` 실행도 PASS다. 이 최종 보고서 복사 뒤에는 해당 artifact checksum·scoped secret scan만 다시 고정하고 마지막 byte set를 확인한다. 최초 두 Evidence는 10/10의7파일·ZIP9entry와 10/12의20파일·ZIP28entry/hash를 각각 재검증하여 원본 변경0이었다.

| 실제 실행 / 수행자 | 원본 대조 결과·한계 |
| --- | --- |
| 구현자 Go 전체 및 최신 재실행 | fmt/vet/build exit0, 2856 pass/7skip; race5package392pass/6skip; 핵심 race20회600pass. 처음·최신 JSON raw event를 summary와 각각 독립 재집계, fail0 |
| 구현자 Web/계약 최신 재실행 | Web typecheck/lint/build+13파일163tests PASS. current workflow OpenAPI/8schema/runtime/migration 및 Reset11 PASS. 기존 build warning 원본 보존 |
| 독립 reviewer local fault | R1/R2/R3 old red→current green. B close ACK를 무시하는 test-only copied dependency fixture -race5회 PASS. 최종 같은 PS script를 PowerShell7/WindowsPowerShell5.1에서 각각5회 PASS. production dependency/GOMODCACHE 수정 없음 |
| 실제 로컬 OpenStack 정상 | 실제 auth/pinned/shared SSH·PTY IO/resize·독립 SFTP INIT/VERSION/REALPATH·idle·Reset gen2/stale1 rejection. 217.25s race PASS, exact tracked14 ABSENT |
| 실제 로컬 OpenStack app | 231.06s race parent+7subtest PASS. 실제 Keystone/API/SSH/worker 삭제·GET404/replacement Neutron 생성. response delay 및 scoped DELETE4093회/NovaPOST4091회는 injected proxy fault이며 자연 cloud 장애로 표현하지 않음. exact tracked16+aux SG/keypair ABSENT |
| 실제 pre/post 조회 | pre1/post2 PASS; matchingTestResidualTotal0, baseline/reobserve servers0/images3/flavors4/networks4. prefix 목록은 보조 근거이고 exact owned-ID cleanup/reobserve가 주근거 |

Go pass 숫자는 parent/subtest event 포함이며 count20의600을 별도600테스트로 표현하지 않았다. 기본 skip은 cloud opt-in6+Windows POSIX0600 1이며 별도 실제 실행에서 M1/VMAccess만 해소했다. 나머지4cloud suite와 PostgreSQL build-tag integration은 미실행이다. old remote CI 성공은 ed784까지만 적용되고 b191 새 CI/외부 review는 pending이다.

portable reproduce의 source/artifact guard, external output만 허용, archive path 검증, BOM 없는 overlay, 함수 wiring 및 explicit config/key/선택모드를 확인했다. 기본 `--verify-only`는 기록상 PASS이고 독립 hash 검증과 일치한다. 실제 mutation 재현은 `--cloud` 선택만 허용하며 pre-discovery 실패에서 시작하지 않고 finally로 read-only post 확인을 수행한다. owned resources 정리는 fixture가 exact IDs로 처리한다. hard kill/timeout으로 finally 자체가 실행되지 못하는 경우까지 보장한다고 쓰지 않았다.

독립 PS fixture는 설치된 x/crypto v0.57.0을 외부 output에 복사하고 local modfile/overlay로 server-side B close ACK만 무시한다. original crypto file/fixture/54source hashes를 확인하며 dependency 설치 조건·준비 절차·LICENSE가 함께 제공된다. artifact Python/PS syntax와 app/residual overlay race compile(PASS, selected tests0/cloud0)도 대조했다. helper byte/hash:

- `reproduce.py`: `bdb6fa929d5f90a8c3eedc4da17e2f3209f9a1ee136ae3bf058705b40fd8f8a6`
- ZIP `portable_run.py`: `6d0e35cb0a86488e640a674c79ed358b1309eff256c4a462803276094fa90897`
- `_fixtures/reproduce-independent-unacked.ps1`: `ad2c912406278869f0b84837549b7c44225509776c5270dc021ed4714ba628dc`

마지막 outer SHA로 env stub5케이스, ignored-Go2경계, Evidence source/artifact9경계 독립 실행 결과가 PASS다. external `noncloud-env-final-complete-artifacts/regression.json`, `ignored-go-exception-green-complete-artifacts/result.json`, `evidence-go-exception-green/result.json`이 원본이며 Go test/cloud0이다. 이 보고서 복사 후에는 추가 artifact 및 checksum·scoped secret scan을 구현자가 다시 고정하고 reviewer가 마지막 집합/바이트만 확인한다. archive 원본을 새 실행으로 다시 표현하지 않는다.

## 신규 Evidence 파일별 검토

| 파일 | 검토 범위 |
| --- | --- |
| .gitattributes / .gitignore | 원본 byte preservation, Python cache의 실제 Git 제외 |
| README.md / AUDIT.md | full147/R1-R3·실제/injected/skip/KT/후속·commit mapping·재현 prerequisites 및 opt-in |
| source-manifest.json / file-review.md | full54/11delta, byte+blob/b191 mapping 및 각 파일·producer/consumer coverage |
| validation-summary.json / ssot-summary.json | raw event/time/exit/scope·latest rerun 일치, 직접 원문 latest version/body/comment 비교 |
| audit-logs.zip / archive-manifest.json | 220entry exact set/CRC/SHA/size·source11 snapshot·raw red/green·실환경/cleanup·portable runners·마지막 helper guard 원본 추가 |
| reproduce.py / reproduce-guard-regression.json | 전체 source pin·staged/untracked/ignored guard, .pnpm 한정, Evidence visible Go 거절·artifact exactset 및 독립 boundary·inherited flags 재현 |
| independent-review.md | full54/trace/독립재현/한계 및 당시 snapshot; 이 보고서가 최신 helper 해결을 추가 |
| publication-checks.json / secret-scan.json / artifact-checksums.json | root 검사 범위와 독립 무결성 대조; 308files/nested entries known-value/pattern scan0, unknownsecret/allhistory 보장 아님 |
| _fixtures/dependency-fixture-manifest.json / reviewer-executed-source-manifest.json | dependency original/test patch·fixture/hash와54byte source guard |
| _fixtures/reproduce-independent-unacked.ps1 | portable copy/modfile/overlay·새externaloutput·no cloud·정확test/race5·PS5/7실행 |
| _fixtures/independent_unacked_setup_test.go / ssh-channel-no-b-close-ack.go / LICENSE-x-crypto.txt | 실제 A IO/lateB/누적/idle복구 의미 있는 fault, server B만 변형, pinned copieddependency license |
| publication-review.md | 구현과 분리된 마지막 판정/수정 전 보완·scope/한계/byte set |

## 남은 범위와 최종 한계

응답하지 않는 SSH open/setup은 active A의 연결을 유지하면서 **transport당1 pending operation**으로 제한된다. A가 계속 살아 있으면 pending worker도 transport 수명만큼 유지되고 같은 VM의 추가 open/setup은 기다리거나 각 ctx로 timeout할 수 있다. no-user idle/Invalidate/Manager.Close가 transport를 종료할 때 해제된다. 요청 deadline만으로 모든 contextless worker가 즉시 소멸한다고 주장하지 않는다.

healthy connected Mock Control/Data의 종료 이유·횟수만 검증했다. 기존 Data write deadline 부재로 slow/unread peer의 shutdown이 지연될 수 있으며 이미 단절된 Control/강제 kill에서 ENDED 전송 보장은 없다. 이 경로의 실제 장애 재현/soak/load/모든 스케줄은 미실행이다. 신규 R1 stall/R2 outage/R3 shutdown fault 자체는 local fixtures이며 실제 cloud 장애 주입으로 승격하지 않았다.

KTCloud145 gen2 Reset/Tier2 Cleanup, 실제 Browser↔배포 SaaS CC02/routing/reconnect/TLS443, production SFTP/File read/write와 WSS, 실제 hostile host/key rotation/자연 outage/packet loss/hard restart는 후속 또는 미검증으로 남는다. Terminal/SFTP 본문 저장·telemetry 수집을 추가하지 않고 key/raw provider/SSH error를 wire/Evidence에 복사하지 않는 경계와 scoped secret 검사 범위를 대조했다. 모든 unknown secret/Git history 검사라고 주장하지 않는다.

검토 범위의 production 구현·자동/실환경 Evidence·독립 두 번째 리뷰와 마지막 Evidence 재현 guard·패키지 무결성을 수용한다. 이 보고서 복사에 따른 checksum 재고정 뒤 허용된 feature branch 업로드와 PR 본문 갱신은 구현자가 수행하고 최종 원격 CI/CODEOWNER 재승인을 기다려야 한다.
