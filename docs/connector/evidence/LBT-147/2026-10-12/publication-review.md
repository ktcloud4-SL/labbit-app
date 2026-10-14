# LBT-147 전체 변경 및 Evidence 게시 전 독립 리뷰

구현과 분리된 reviewer가 읽기 전용으로 검토했다. 저장소 코드/Evidence 수정, cloud mutation, push, PR 생성, Jira 쓰기는 수행하지 않았다. 별도 외부 검증 파일만 작성했다. 이 리뷰는 최초 전체 diff, P1/P2 수정 후 전체 영향 리뷰, 현재 소스 실제 OpenStack Evidence 리뷰를 이어서 수행한 게시 감사다.

## 대상과 소스 일치

- Branch: `feat/LBT-147-vm-access`.
- Merge-base: `6d675c74bff11f481d247267faefd4ce960dcd38`.
- 최신 origin/main: `e850567066134edc87b2ba543d0eb42a21f406b8`. 기존 base 이후 main 변경은 `docs/frontend/deployment-handoff.md` 한 파일이며 전체 내용을 독립 검토했다. LBT-147 production/contract/runtime 변경이 없다.
- 실제 실행: HEAD `399681789572487bd44469ec018e52b53f97678e` + 6개 수정 파일.
- 검증 소스의 코드 커밋: `74c052f25e3778cafa6ae4fa75bd8775af46827a`.
- 전체 구현/과거 Evidence 29개 파일의 실제 byte SHA256, 코드 커밋 Git blob, base→코드커밋 전체 diff 파일 집합을 직접 대조했다. 누락/불일치 0이다. P1/P2만 별도로 검토한 것이 아니다.

Jira 147/145/21 본문·댓글과 Confluence D18v5/D20v7/D21v10/테스트전략v3/MVPv19의 게시 전 원문 재조회 결과를 실제 실행 당시 원문과 직접 비교했다. 8개 자료의 본문·댓글/버전이 모두 같다. 최신 요구사항 변경은 없고 KT/LBT-145의 기존 미완료 선행조건은 유지된다.

## 전체 구현 범위의 판정

| 요구사항·경계 | 독립 검토 및 검증 근거 | 한계 |
| --- | --- | --- |
| Lab/generation/VM/server/project 일치, 관리망 private IP/user/local key | 전체 resolver/manager/Control caller 검토, mismatch 회귀, 실제 두 세대 관리망 SSH 및 잘못된 대상 접속 전 거절 | KT Provider acceptance와 다름 |
| Server별 기존 host pin | resolver의 기존 검증 재사용과 안전한 endpoint 검토, 정상 실제 pinned SSH 및 unknown/changed-key fixture 회귀 | 실제 공격/host-key 변경 주입 미실행 |
| VM별 lazy SSH/shared transport | cache·동시 open·retire producer/consumer 검토, 실제 connection identity 메모리 비교, app lazy 초기화 공유/취소/실패/재시도 검증 | 장기 부하·모든 실제 waiter 도착 순서 미검증 |
| 독립 PTY/SFTP channel | 기존 managed PTY wiring/SessionManager/Control/Data 회귀, 실제 PTY IO/resize 및 PTY 종료 후 SFTP probe 성공 | production File read/write와 실제 WSS 왕복 아님 |
| 설정 가능한 no-channel idle | Runtime0.1.7 기본1m/양수duration SSOT 및 app config 검토, 회귀와 실제 idle transport 교체 | 장기 soak 미실행 |
| 실패/auth/handshake/timeout/cancel/disconnect/shutdown 정리 | SSH peer/race 실패·취소·late completion 회귀, 실제 Keystone 초기화 deadline/cancel/owner-cancel/retry | 실제 SSH 인증 거절/handshake stall/packet loss/process restart 미실행 |
| 새 generation/target에 기존 channel 재사용 금지 | 정상 실제 Reset/stale1 거절, 실제 surviving workspace의 partial-failure retire/fence, replacement-failure fence, late resolve/setup/다른Lab fixture | partial failure는 아래 명시한 HTTP409 주입과 실제 backend의 조합 |
| key/raw error/Terminal 본문 비노출 | 전체 safe error 소비자 검토, redaction 회귀, metadata-only 원본/harness 검토 및 scoped secret scan | unknown token 전수 및 전체 Git history scan 아님 |

기존 `Session.Setup` 취소/timeout은 context 없는 SSH setup을 해제하기 위해 같은 VM의 shared transport를 폐기할 수 있다. 정상 channel 종료 독립성과 setup 취소 시 타 channel 보존을 혼동하지 않는다. 기존 후속 범위의 제한으로 유지한다.

## 자동·실환경·원본 정합성

`audit-logs.zip` 28개 entry를 직접 읽어 CRC, entry 집합, byte 길이, SHA256 및 두 외부 harness SHA를 검사했다. 모두 일치한다. 수정 전 production 회귀 실패와 초기 fixture panic, chronological audit/reviewer 기록을 삭제하지 않고 포함했다.

압축 내부 원본 자동 JSONL을 파싱하여 unit 2,828 PASS/7 SKIP/31 package 및 race 364 PASS/6 SKIP/5 package와 요약의 모든 action 수치를 대조했다. 불일치와 fail은 없다. opt-in cloud 및 Windows POSIX assertion skip은 실제 검증 완료로 확대하지 않는다. 앞선 Go/Web/contracts PASS를 packaging 단계의 새 실행이라고 표시하지 않는다.

실환경 원본 JSONL 4개를 실행 당시 외부 원본과 byte 단위로 직접 비교하고 테스트명, PASS/fail/skip, 시작·종료 시각, 경과 시간을 요약과 대조했다. 모두 같다. 선택된 실제 실행은 test/subtest 포함 12 PASS, fail0, skip0이다.

- 정상 VM acceptance: 실제 Provision gen1 → pinned/shared SSH → target 거절 → PTY IO/resize·독립 SFTP probe·idle → Reset gen2/stale1 거절 → unique14 exact tracked resource ABSENT.
- 실제 app 경계: P1 5개/P2 2개 PASS. 일부 실제 삭제 뒤 살아 있는 workspace의 기존 SSH 종료와 resolver 호출 전 oldgen fence를 확인했다. preflight FAILED는 접속을 보존했다. 초기화 취소/실패 후 공유 결과와 다음 요청의 실제 재인증을 확인했다.
- 장애 주입: 정확한 소유 server DELETE409 3회, exact Nova host/project path + audit lab/gen2 metadata POST409 1회 및 정상 실제 Keystone 응답 지연을 loopback proxy에서 주입했다. 자연 발생 OpenStack 장애나 가짜 Provider 결과라고 바꾸어 해석하지 않는다.
- app unique16 exact tracked resource ABSENT와 임시 관리 SG/keypair의 별도 독립 GET404를 확인했다. 정상14 + app16 = 30개이며 fixture 보조 자원은 별도다.
- 마지막 조회는 authentication/List/GET 전용이다. matching test server/network/subnet/SG/port/keypair/router 모두0. 서버0·이미지3·flavor4·network4는 사전 baseline과 같다. 기존 자원은 삭제하지 않았다. exact tracked-ID 검증이 주근거이며 prefix inventory는 보조다.

## 게시 과정에서 보고한 보완

| 항목 | 수정 전 근거·영향 | 수정 및 검증 |
| --- | --- | --- |
| PUB-01 / P2 / 원래 reproduce.ps1:17-19 | 29개 changed-file blob만 guard 입력으로 사용해 diff에 없는 기존 lifecycle producer/consumer 변경을 놓칠 수 있음. 실제 cloud mutation이 감사하지 않은 코드로 실행될 가능성. PRE_MERGE_AUDIT의 실행소스 식별·재현 동일성 요구에 해당 | 전체 codeCommit tracked/staged diff, Evidence 폴더 밖 untracked, ignored Go source guard 추가. 29blob/zip/harness 검증 유지. PS7/5.1 각각6case에서 dirty source가 Go 전에 거절됨 |
| PUB-02 / 호환성 / 원래 reproduce.ps1:32-33 | Windows PS5.1 `Set-Content -Encoding utf8`의 BOM이 Go overlay JSON parsing을 깨뜨림 | UTF8Encoding(false) WriteAllText. 두 PS runtime의 clean/restored case에서 overlay BOM0·JSON 파싱 PASS |
| PUB-03 / P3 / 초기 verify-reproduce-guards.ps1 global:go/finally | 기존 사용자 global go function을 덮어쓴 뒤 제거할 수 있음 | 기존 ScriptBlock 보존/finally복원, closure counter. PS7/PS5.1에서 기존 wrapper 없음/있음 각각6case, 총24case와 복원/제거 PASS. 해소 |

PUB-01/02/03는 수정 전에 구현자에게 보고됐고 사용자에게 전달됐다. 검증 fixture는 독립 local clone, dummy config/key와 실패하는 Go stub을 사용해 actual cloud 호출0을 확인한다. 최신 helper 전체 소스와 4회 실행/24case 원본, wrapper 복원 결과를 직접 검토했다. production 소스와 실제 OpenStack 실행 결과는 보완 과정에서 변경하지 않았다. Reviewer가 새로운 production 결함을 확인한 것은 없다.

## 새 Evidence 파일별 검토

전체29 파일별 구현 검토는 본 디렉터리 `AUDIT.md`의 표 및 archive의 최초/수정 후 review에 연결된다. 아래는 게시 Evidence의 추가 범위다.

| 파일 | 검토 내용 |
| --- | --- |
| .gitattributes | 폴더의 `* -text`로 Windows/Linux artifact byte 및 SHA 보존 |
| README.md | LBT-147 전체 구현 범위, 실제/주입/미검증, fixture 재현 조건, cleanup30+aux, 커밋 연결 |
| AUDIT.md | 전체29 coverage·AC 추적성·기존 finding 해소·source/version·원본/skip/잔여 위험 |
| source-manifest.json | 29 execution byte SHA와 74c052f blob·전체diff집합 일치, original execution context 보존 |
| validation-summary.json | 4개 원본의 test/action/time/elapsed 및 실제/주입/cleanup30 범위 일치 |
| automated-validation-summary.json | archive raw unit/race와 정합, earlier NOT_RUN/pushfalse 맥락 명시 |
| ssot-summary.json | 3 Jira/5 Confluence 최신 원문 body/comments/version 불변 및 링크/해시 |
| pre-discovery.jsonl | original byte 및 discovery baseline 일치 |
| real-vm-access.jsonl | original byte, 정상 actual VM/PTY/SFTP/idle/gen/14cleanup 판정 일치 |
| real-app-boundaries.jsonl | original byte, P1/P2/정확 주입 count/16cleanup 및 aux부재 일치 |
| post-discovery-and-residual.jsonl | original byte, read-only inventory/PASS/residual0·기존자원 유지 |
| audit-logs.zip | 28entry CRC/길이/해시, 원본 자동 기록/초기 실패/chronological review/harness 보존 |
| archive-manifest.json | 실제 entry 집합·byte·SHA와 완전 일치 |
| reproduce.ps1 | code identity/archive/harness guard, 별도 output/overlay, 고정 local fixture, finally 사후조회·env복원 |
| verify-reproduce-guards.ps1 | 원본 repo 대신 독립 local clone, cloud 차단, dirty/clean/staged/untracked/ignored 테스트 및 BOM 체크 |
| reproduce-guard-regression.json | PS7/PS5.1, wrapper 없음/있음 각각6case, 총24case PASS 및 복원/제거/actualCloud0 기록 |
| publication-checks.json | packaging parse/overlay compile 및 identity/archive/summary, 실제cloud 재실행과 분리 |
| secret-scan.json | 12:18:37 KST의83개 파일/entry scoped scan 결과findings0. known-local exact-value/keybody/PEM/possible-token 범위·한계 및 결과 검토. 값 대조는 구현자 수행 |
| artifact-checksums.json | 현재18 artifact의 실제 byte/SHA를 독립 재계산해 불일치0; 이 리뷰 추가 뒤 publisher가19 artifact로 갱신. 자기 파일은 자체 해시 목록에서 제외 |
| publication-review.md | 이 독립 게시 리뷰; 실행·판정·범위 및 한계 |

## 최종 판정과 한계

**검토한 LBT-147 로컬 구현·Evidence 게시 범위는 PR 가능이다.** 미해소 confirmed production/게시 finding은 없다. 전체29 구현/과거 Evidence와 새20 Evidence(본 리뷰 포함), 총49개 고유 변경 파일의 범위를 확인했다. 게시 과정의 source guard/UTF8 BOM/helper restoration 보완은 의미 있는 no-cloud 회귀와 정적 검토로 해소했다. 원본/cloud/자동 테스트를 다시 실행했다고 표시하지 않았다.

이 reviewer가 수행한 검증은 source byte/commit blob/diff 집합, archive CRC/entry SHA, 원본 JSONL/summary/실행시각, latest SSOT 내용 직접 대조 및 script AST/정적 검토다. 기존 코드 감사 중 SSH fixture 회귀는 reviewer도 실행했다. 이번 publication의 PS runtime guard회귀·overlay race compile·로컬 exact-secret 검사는 구현자가 실행했고 reviewer는 소스와 원본/결과를 검토했다. 이번 reviewer는 실제cloud나 publication helper를 실행하지 않았다.

이 리뷰가 저장소에 복사되면 publisher가 해당 byte를 포함해 secret-scan과 checksum을 마지막으로 갱신·검사하고 동결한다. 현재18개 artifact 및 raw/source/zip 검사 결과는 모두 PASS며 본 리뷰의 추가는 production 소스를 변경하지 않는다. 최종 Evidence 커밋은 문서·로그·재현 도구에 한정한다.

KT/LBT-145, 실제 SaaS/Browser/CC-02 전체 왕복·reconnect/Gateway, production SFTP/File, 장기 soak/process restart 및 Setup-cancel 타 channel 유지의 범위 제한은 그대로 남는다. 이 판정은 KT 선행조건, Jira/OP-02 전체 완료, merge/배포/Jira 변경 승인이 아니다. Branch push/PR은 사용자의 별도 명시적 승인에 따라 구현자가 수행하며 main 직접 push·merge·Jira 쓰기는 금지 범위를 유지한다.
