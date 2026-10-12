# LBT-147 추가 검증 Evidence 독립 게시 감사

2026-10-12 KST. 구현과 분리된 reviewer가 사용자 PRE_MERGE_AUDIT.md에 따라 수행했다. 저장소 제품·테스트·Evidence 파일을 수정하거나 cloud mutation, push, PR/Jira/API write를 실행하지 않았다. 외부 clone·overlay·검증 기록만 만들었다.

**현재 수정 패키지의 로컬 게시 감사 통과.** package21개 파일에서 확인된 snapshot/source 표기 P2를 아래와 같이 정정했다. 원격 Go CI 포맷 실패와 이어진 Linux stack assertion·server EOF fixture·healthy Data ENDED product race를 모두 보존했다. 과거 counted 전체 Go/race/반복 실행과 실제 cloud 실행은 각각의 당시 source에만 연결하고, 이후 product commit `e8384cb`은 current loopback 회귀·독립 검토·전체 로컬 Go와 Linux Go format/vet/unit/build/PostgreSQL16 및 Web·Contracts·backend image build로 구분해 검증했다. 이 판정은 LBT-147 Jira 전체 완료, CODEOWNER 승인, merge 또는 배포를 뜻하지 않는다.

## 고정된 검토 집합

- 저장소 `ktcloud4-SL/labbit-app`, feature `feat/LBT-147-vm-access`; 실제 정상 VM 실행 제품 코드 커밋 `bd8071d86659f2cce5d2c6e77cbb7f7ac0054d05`, 실제2VM 실행 snapshot `a5b2d1210c3941641dc96dfc4aa2201358e9293a`+pinned working changes, 이번 package 검토 HEAD/source-manifest codeCommit `e8384cbbb1ddaa16153eae037ac8a37dbee08d6a`, 최신 main `7aaa5ca4e7f58cb88f188f91e653a624e4caed03`. e838에는 bounded50ms final Data writer-lock wait 제품 변경과3개 관련 test/fixture 변경이 있다.
- 게시 폴더 `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/`의 정확한 21개 파일을 직접 읽었다. `artifact-checksums.json`을 제외한 20개 checksum target의 bytes/SHA256이 모두 일치한다. 누락·추가 항목 없음.
- 이번 고정 snapshot ZIP은 정확한405개 entry다. 중복 이름 없음, CRC, 개별 크기/SHA256, archive-manifest를 재검증했다. stale scratch prepared-reproduction/prepared-current/pinned-source-guard-fixture와 PR 본문 초안은 제외했고 초기 FAIL, 필수 source/runner/fixture, reviewer red/green, publication-format-red, remote-linux-fixture/eof/finalframe-red 및 최종 publication reviewer 보고 원본을 보존한다.
- 이번21/405 package는 artifact-checksums 자신을 제외한20개 target을 고정한다. 이전 publication-package21/staged-final 및351/392/394 interim snapshot 검증은 당시 집합 기록이며 현재 집합으로 재명명하지 않는다.
- publication-review.md는 21번째 metadata 보고 파일이다. 이번 정정 뒤 root가 checksum/secret scan/file-set/archive를 재고정하고 reviewer가 마지막 exact byte-set을 재확인한다.

## 소스와 실행 원본 추적

source-manifest의 39개 product/context 파일을 현재 byte SHA, Git clean-filter blob, 코드 커밋의 blob에 각각 대조했다. 39개 모두 내용과 매핑이 일치한다. 32개 Go 파일은 실행 checkout과 committed blob의 exact bytes도 같다. OpenAPI, go.mod/go.sum, runtime README/contract, Web package/package-lock 7개는 Windows checkout CRLF와 Git LF 차이만 있다. LF 정규화 내용과 clean-filter blob이 일치함을 직접 확인했다.

최신 정상 VM 실행 당시36개 source는 Go32 exact + go.mod/go.sum/runtime2 총4 EOL-only이며 `bd8071d` 실행 기준이다. 실제2VM은 `a5b2d12`+working production/test source를 `actual2VMSource`에 고정했다. counted 전체 Go/race/반복의 `finalGoSource`는 `ce02b4c`+working manager test snapshot이다. 현재 source39 mapping은 별도로 `e8384cb`의 raw SHA/clean/commit blob에 대조한다. 이후 e838의 `session.go`50ms 제품 delta와 final-frame/SSH manager/resize test3개를 과거 실제 VM 또는 counted suite가 실행했다고 주장하지 않는다. 이 네 파일은 current loopback 일반/race 반복, 이전 TryLock expected-red, 전체 로컬 Go, 독립 blob mapping 및 Linux run38231218638로 검증했다.

| 실행 | 원본과 현재 소스 관계 | 확인한 결과와 제한 |
| --- | --- | --- |
| 전체 Go / 관련 race / 선택 회귀20회 | counted snapshot은 finalGoSource의 ce02+working manager test; e838 current는 별도 전체 local Go와 targeted normal/race로 검증 | 과거 counted 2885 PASS/7 SKIP,482 PASS/6 SKIP,940 PASS/0 SKIP. e838은 app100회, 관련 normal/race20회, resize race20, package race5, reviewer normal/race 각180 및 전체 `go test ./...` PASS. subtest/반복 action 수를 서로 다른 기능 수로 확대하지 않는다. |
| Web / contracts | 최신 main의 package/lock 동일 blob, fresh npm11 ci | 13파일/163tests, typecheck/lint/test/build; OpenAPI/8 schema/runtime·migration metadata 및 Reset11 PASS. DB application SQL integration을 실행했다고 주장하지 않는다. |
| M2 Provision / M3 lifecycle / Control WSS lifecycle / Internet ON/OFF | 실제 da6b670 source; dependency/main 차이 명시 | 각 suite 및 사후 inventory PASS. 과거 source의 실제 cloud 결과를 현재 source 실행으로 바꾸지 않는다. Provider 구조와 소유 exact-ID cleanup을 검토했고 바뀐 Terminal 경계는 아래 실행으로 재검증했다. |
| 실제 2VM R1/R2/R3와 짧은 부하 | actual2VMSource의 a5b2d12+working production/test source; e838의 후속50ms Data wait는 미포함 | parent12, child3 PASS, skip0. actual exec sleep Wait cancel/deadline 각10회 동안 A PTY 유지; scoped Nova/Neutron GET503은 주입; 실제 pin에 synthetic hostile key 제시; mock SaaS WSS + actual Provider/VM/SSH/PTY runControl. 2VM 각30회 작업은 identity를 비교하지 않는다. tracked10 및 보조자원 정리, 사후 matching0. 이후50ms delta는 loopback/current Linux 검증 범위다. |
| 최신 정상 VM integration | bd8071에서 직접 -race 실행, source36 일치, 사후 변경0 | 214.69초 PASS/skip0. strict target, PTY IO/resize, PTY close 뒤 SFTP REALPATH, 메모리 SSH_CONNECTION 비교로 share/idle 교체, gen2 Reset·fresh target·stale1 거절. tracked14 각각 GET404 및 최종 inventory matching0. aux keypair/managementSG는 own Delete와 prefix 조회0이며 각각의 별도 GET404로 과장하지 않는다. |

일반 Go의 7 SKIP는 cloud opt-in6 + Windows POSIX0600 assertion1이다. 관련 race의 6 SKIP는 cloud opt-in이다. 별도 실제 실행과 skip을 분리했고 기본 suite의 SKIP를 PASS로 합산하지 않았다. actual parent/child 및 최신 정상 VM은 fail/skip0이다.

initial-real-fixture-failure, tracing false assertion FAIL, initial gofmt CRLF 진단, Data/SSH red, 초기 candidate의 fixture cleanup 실패와 원격 run38224917705/38226445266/38229011898/38229988462를 원본에서 확인했다. 최종 성공과 다른 소스·시점으로 보존했다. cleanup 확인 문구는 assertion 직전 로그이므로 문구만으로 성공 판정하지 않았고 testcase action PASS와 사후 조회를 함께 확인했다.

## 수정 전 게시 findings와 수용 상태

| ID / 심각도 | 위치·재현·영향 | 수정 및 독립 회귀 |
| --- | --- | --- |
| PUB-SOURCE-EOL / P3 | independent-review section7이 최신36을 Go/의존34 exact+runtime2 EOL-only로 과대 집계. PR84에 없는 go.mod/go.sum context 차이를 누락했다. 개별 source JSON의 raw SHA/blob 매핑은 정확했으며 제품 문제는 아니다. | reviewer가 수정 전에 보고한 뒤 Go32+EOL4로 정정. publication39의 exact32+정확한7 EOL 집합, raw SHA/clean blob/commit blob/LF 내용 동일성 assert PASS. 수정 전 report와 regression JSON 보존. 외부 Markdown 저장의 CRCRLF도 UTF8 LF로 정정했고 이전 바이트를 보존했다. |
| PUB-FILTER / P2 | 수정 전 .gitattributes:1–4의 text eol=lf가 CRLF artifact를 staging 시 변환. source-manifest:205–206 fixtureSHA와 두 fixture가 달라져 prepare_overlays:33–34가 재현을 거절. 외부 local clone의 실제 git add에서19 target 중11 hash·2 fixture 불일치 재현, cloud0. | `* -text`로 원본 바이트 보존. 독립 staging + fresh checkout의 당시 payload20개 모두 exact bytes, fixture4 SHA 및 fresh helper --verify-source 성공, cloud0. 이번21개 raw Git blob/clean-filter blob 동일 및20 checksum 일치. RED/GREEN JSON 보존. |
| F-EVIDENCE-GOFMT / P1(publication) | [원격 run38224917705](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38224917705), job114729949929 step5 Go format 실패. Makefile:25의 전체 *.go 범위에 게시 integration_metadata_test.go/read_only_residual_test.go가 포함됐다. GitHub job을 직접 read해 step6vet/7test/8build/9PostgreSQL skipped를 확인했다. | 원본2개와 run/job/hash를 ZIP publication-format-red에 보존. 현재 canonical/package2개가 수정 전 원본에 gofmt를 적용한 stdout와 exact bytes로 같다. 동작 변경 없이 integration single-line if/log 확장과 LF 정규화다. 현재 포맷 출력0·fixture/source pin·overlay compile/opt-out을 확인했고 code commit `e8384cb`의 새 Linux Go/PG가 PASS했다. |
| F-EVIDENCE-FMT-DOC / P2(publication) | README:23의 Windows `gofmt -l`(소스만) 안내는 게시 fixture를 포함한 Makefile 범위를 명시하지 않아 같은 실패를 놓칠 수 있었다. reviewer가 수정 전에 보고했다. | 전체 Go 파일을 per-file gofmt -l로 검사하고 게시 _fixtures 포함 출력0을 확인하는 PowerShell 명령으로 정정. 그 명령을 독립 실행해 현재330개/출력0 확인. 보존 원본2개는 gofmt -l에서 검출했다. |
| F-SSH-MANAGER-LINUX-ASSERT / P1(test/CI) | [원격 run38226445266](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38226445266), job114735013574. format/vet PASS 뒤 input-close/pending-resize가 원본 line142의 필수 internal/poll.(*FD).Write stack을4초 내 관찰하지 못해 unit FAIL, build/PostgreSQL skip. reviewer가 job steps/log를 직접 read했고 종료 assertion까지 도달하지 않아 제품 종료 실패를 관찰한 것이 아님을 확인했다. | SSHPTY.Write 200ms 지속 pending+inputDone 미완료로 변경. resize-close는 wantReply=false request의 조기 정상완료 또는 pending을 허용한다. shutdown750ms, input/resize release, server EOF, ErrClosed 유지. kernel TCP pending resize는 SetWriteBuffer1024 별도 회귀가 담당한다. 수정 전 source SHA a8e344.../원격metadata를 remote-linux-fixture-red에 보존. 로컬 targeted20/race20, 전체·관련race·반복과 후속 code commit `e8384cb`의 Linux Go/PG PASS. |
| F-SSH-MANAGER-LINUX-EOF / P1(test/CI) | [원격 run38229011898](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38229011898)에서 두 manager mode의 server EOF가 timeout. backlog `io.Copy`를 추가한 뒤에도 [run38229988462](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38229988462)에서 재현했다. 두 run 모두 shutdown·worker release 뒤 EOF 관찰 단계의 test 실패다. | server의 인위적1KiB receive buffer만 제거하고 실제 read pause,200ms input pending,750ms shutdown, worker release, backlog drain/`sc.Wait`, ErrClosed는 유지했다. 두 원격 source/log metadata는 remote-linux-eof-red와 remote-linux-finalframe-eof-red에 보존했다. |
| F-DATA-ENDED-BRIEF-CONTENTION / P1(product/CI) | run38229988462의 app integration에서 shutdown Data ENDED 누락. 단일 immediate `writeMu.TryLock`이 직전 healthy echo writer와 잠깐 겹치면 frame을 생략했다. 독립 previous-code overlay가 새 회귀에서 close1006을 재현했다. | close 뒤 최대50ms bounded lock poll; 획득 시 기존1초 frame deadline, 초과 시 best-effort socket close. current app100회, 관련 normal/race20회, reviewer normal/race 각180 PASS.20 busy sessions는 약1005ms·callback20·registry0이며 N×50ms와50ms초과 ENDED 생략을 잔여한계로 공개했다. |
| F-SSH-RESIZE-STACK-ASSERT / P1(test/race) | 로컬 package race5에서 runtime stack `WindowChange` 문자열 의존이 간헐 실패했다. | wrapper로 PTY.Resize 진입을 직접 동기화하고 실제 blocked TCP write와 resize200ms pending을 검증한다. race20/package race5 PASS. wire WindowChange packet 관찰 주장은 하지 않는다. |
| PUB-SNAPSHOT-STALE / P2(publication) | publication-review가 current codeCommit을41f785e, ZIP을351, ce02→current delta를 test1개로 남겨 실제 e838 product delta·새 Linux 결과와 모순됐다. | current e838/product39/package21/최종ZIP405와 historical execution source를 분리하고, current4-file delta와 run38231218638 step 결과를 명시했다. 재생성 publication check와 독립 exact-set review를 반복한다. |
| PUB-ACTUAL-SOURCE-CLAIM / P2(publication) | AUDIT/README가 과거 actual2VM를 “최종 production”에서 실행한 듯 표현하고 Data Close를 writer lock 무대기로 적어 e838의50ms 변경과 불일치했다. | actual2VMSource a5+working 및 latestVM bd807 실행을 명시하고 e838 delta는 실제 cloud 미재실행, loopback/current Linux 검증으로 한정했다. Data Close 최대50ms와 잔여 N×50ms를 통일했다. |

제품 finding R1/R2/R3 및 Data/SSH Close·Resize·attachment ownership, partial-failure fencing/lazy initialization은 independent-review/AUDIT의 코드 감사·red/green과 source별 실제 실행으로 해결 상태를 확인했다. 새 wire/runtime option 또는 전체 shutdown SLA를 추가하지 않았다. final Data writer lock은 최대50ms 기다리고 frame write는 기존 session별1초 best-effort budget이며 전체 connector 종료1초를 뜻하지 않는다.

format 회귀 원본 publication-fixture-format-verification.json SHA256은 `368f7ad9f7807e07a1b68a1e807455800277b681fc570af22b491cdbe92cb2af`, 직접 read한 원격 job steps 기록 SHA256은 `0f774c42e4c4e9f91682bd2a1c4bd5bf90a4be3a236bd5faa6cb7163a97accd1`다. 새 fixture SHA는 integration `8d93b8c4053004f27cb2a0968a71f0f4162793799534a851da7b6aea261ff58c`, read_only `117152473e8384bfa309abaf88f2df50d84182ed16094a9efb05a15f74780842`다. 실제 cloud 실행 당시 원본 hash는 별도 tested-source/원본 기록에 보존하며, 포맷 변경 뒤 실제 cloud를 다시 실행했다고 주장하지 않는다. reviewer Python walk의332개는 node_modules junction alias2개가 추가된 superset이고, Makefile find 기본의 symlink directory 비추적과 README PowerShell 범위는330개다. 둘 모두 출력0이며332를 CI 실제 파일 수로 쓰지 않는다.

Linux에서 실패 당시 실제로 어느 write layer에 멈췄는지는 원격 goroutine stack 원본을 관측하지 않았으므로 production cause로 단정하지 않는다. pinned x/crypto의 channel.WriteExtended→remoteWin.reserve와 WindowChange→SendRequest(wantReply=false)를 읽어 SSH window가 먼저 소진될 수 있고 resize packet은 정상 완료할 수 있음을 확인했다. 입력 FD/resize stack 문자열을 제거하는 test-only 방향과 resizeDone 동기화를 독립 검토했고, 별도 kernel fixture와 manager fixture의 combined race5회도 로컬 PASS다. 수정 후 Linux run38231218638에서 unit 포함 전체 Go job과 PostgreSQL16 step이 PASS했다.

## 재현과 보안

prepare_overlays는 실행할 test를 자동 호출하지 않고 fixture 복사·절대 경로 overlay 준비만 한다. 외부 출력, 비어 있지 않은 출력 거절, fixture hash, 선택적39 source clean-blob 검사를 코드에서 확인했다. pinned-file 검사이며 전체 ignored/untracked producer guard라고 주장하지 않는다. README는 정확한 code commit checkout, 외부 credential/key, 로컬 cloud 환경, 순차 opt-in, 사전/사후 inventory와 소유 exact-ID cleanup을 명시한다. 실제 normal VM command/선택 플래그도 validation-summary에 있다.

별도 외부 회귀7개를 실행했다: 정상 current source, nonempty 출력 거절, repo 내부 출력 거절, 정상 isolated clone, 변경 source 거절, 누락 source 거절, corrupted fixture 거절. 모두 기대 결과다. 준비된 Provider+App overlay 두 패키지는 `-race -run '^$'`로 compile PASS했다. 이 실행은 test0/cloud0이며 실환경 PASS로 합산하지 않았다. root reproduction-checks의 준비4case/명시 opt-out SKIP와도 구분한다.

외부 validate_package.py의 고정 scratch P3는 실행마다 tempfile.mkdtemp를 사용하고 종료 시 임시 준비물을 정리하도록 수정됐다. 코드를 직접 읽고 root final-harness-repeat/run-1.log, run-2.log, summary.json의 연속2회 exit0/cloud0, source/fixture/output 준비 PASS와 explicit opt-out overlay compile PASS를 확인했다. 시작 UTC08:27:45.400/08:27:57.024이며 제품·게시 prepare_overlays.py 변경은 없다. 독립 검증 기록 publication-harness-repeat-verification.json SHA256은 `72c51c7fddeb27c076b9ff720017295dd0c1b5854afaede708527b78d340f30d`다.

필수 fixture4개는 _fixtures에 게시되어 Go ./...의 패키지 발견에서 제외된다. actual runtime child의 metadata는 parent가 소유한 VM에서 임시 생성하며 개인키·credential 내용을 게시하지 않는다. 당시 개인 절대 경로 runner는 원본 기록이고 portable helper의 repo/output 인자를 사용한다.

_fixtures가 Go ./... 발견에서 제외돼도 Makefile go-fmt-check의 find 범위에서는 제외되지 않는다. 이번 CI 실패가 그 차이를 보여줬다. format2개 수정 후 source-manifest fixtureSHA와 root/package/ZIP canonical4개 일치, prepare helper SHA 불변, 준비·명시 opt-out overlay compile을 확인했다. .gitattributes는 `* -text whitespace=cr-at-eol`로 byte를 보존하고 CRLF 판정만 허용한다. 이전 독립 probe는 정상CRLF exit0, 실제 trailing-space exit2와 trailing-whitespace 진단을 확인했다.

게시21개와 ZIP405개에서 secret-scan이 nonzip+archive payload425개를 검사했다. 알려진 로컬 secret/key 값3개와 PEM/Bearer 패턴의 일치0. 값은 메모리에서만 비교했고 출력·보고서에 복사하지 않았다. Terminal/SFTP 내용과 SSH_CONNECTION은 증거에 저장되지 않고 메모리에서 assertion한다. known-value/pattern 검사는 unknown secret나 모든 Git history 부재 증명이 아니다.

## 파일별 게시 검토

| 파일 | 검토 목적 |
| --- | --- |
| .gitattributes | 원본 byte 보존; 실제 stage/checkout 회귀 |
| .gitignore | __pycache__/pyc 게시 제외 |
| README.md | 환경·opt-in·준비/실행 경계·cleanup·미검증 범위 |
| AUDIT.md | 전체147 AC·SSOT·실패/보안/수명주기 및 findings 해결 |
| file-review.md | 전체 committed PR84와 현재21 파일 누락 없이 포함 |
| independent-review.md | 독립 코드 판정, 최신VM 보완, 정확한 집계·한계; 외부 최종 파일과 exact bytes |
| publication-review.md | 21번째 metadata 보고 파일; 현재 snapshot과 후속 마지막 byte-set 확인 구분 |
| source-manifest.json | code/main·39 raw SHA/clean blob·실행 시점 및4 fixture pin |
| validation-summary.json | 자동/cloud4/2VM/latestVM raw action·skip·시간·source 구분 |
| ssot-summary.json | ZIP 원문의 Jira description SHA·5 Confluence body SHA/version 대조 |
| publication-checks.json | 현재 code/source/result/archive 판정의 원본 정합성 |
| reproduction-checks.json | 준비 helper SHA·거절 case·opt-out compile 범위 |
| secret-scan.json | 알려진 값/패턴 범위의0 match 재검증 |
| archive-manifest.json | 이번 snapshot405 entry exact set/CRC/bytes/SHA |
| artifact-checksums.json | 자신 제외 정확한20 target set/bytes/SHA |
| audit-logs.zip | 현재/초기 실패/원문·source·fixture·runner·reviewer red/green 원본 |
| prepare_overlays.py | 준비만 수행, 입력·fixture/source hash·output guard |
| _fixtures/integration_metadata_test.go | 기존 실제 cloud suite helper·소유 cleanup/재조회·metadata-only 로그 |
| _fixtures/read_only_residual_test.go | GET/List만 사용, 알려진 local baseline과 prefix residual; mutation 없음 |
| _fixtures/real_failure_test.go | owned2VM/실제SSH·scoped503·child bridge·cleanup 및 제한 |
| _fixtures/actual_runtime_shutdown_test.go | production runControl/Provider/PTY와 loopback mock WSS; healthy/unread 분리 |

KT145 gen2/Tier2 실제 부재, 배포 Browser/SaaS routing·CC02/Live, production File WSS, 실제 SSH packet-stall fault·자연 outage/key rotation, hard restart/장시간 soak/대규모 load는 검증 밖이다. actual VM child의 서버 측 SSH EOF는 별도 관측하지 않았고 loopback actualManager 회귀만 EOF를 확인했다. Data ENDED는 writer contention50ms 초과에서 best effort이며 순차 N-session close는 약 N×50ms 뒤 기존 frame/callback 비용이 추가될 수 있다. process-global shutdown budget은 검증하지 않았다. 로컬 PostgreSQL integration은 미실행했지만 코드 commit `e8384cb`의 Linux Go run38231218638에서 PostgreSQL16 integration까지 PASS했다. 최종 Evidence commit의 원격 CI는 게시 후 별도 확인한다.

검토한 package21개에 게시 차단 결함은 남지 않았다. 갱신 보고서와 harness 원본 inclusion 후 root의 checksum/secret scan/file-set/archive 갱신과 reviewer의 마지막 exact byte-set 확인으로 게시 집합을 고정한다. 감사는 별도의 외부 쓰기 승인을 대신하지 않는다.
