# LBT-147 추가 검증 Evidence 독립 게시 감사

2026-10-12 KST. 구현과 분리된 reviewer가 사용자 PRE_MERGE_AUDIT.md에 따라 수행했다. 저장소 제품·테스트·Evidence 파일을 수정하거나 cloud mutation, push, PR/Jira/API write를 실행하지 않았다. 외부 clone·overlay·검증 기록만 만들었다.

**현재 수정 패키지의 로컬 게시 감사 통과.** 검토한 package21개 파일에 미해결 코드·바이트 결함은 없다. 원격 Go CI 포맷 실패와 이어진 Linux manager 회귀 assertion 실패를 보존했고, fixture2개·README·manager test-only 수정은 독립 확인했다. 최신 로컬 전체 Go/race/반복 PASS는 현재 커밋의 동일 실행 바이트와 대조했다. 새 Linux 원격 Go/build/PostgreSQL은 후속 확인 대상이다. 기존 문서 집계 P3, Git 개행 변환 P2, 외부 재현 harness 고정 scratch P3도 수정·회귀를 확인했다. 이 판정은 LBT-147 Jira 전체 완료, CODEOWNER 승인, merge 또는 배포를 뜻하지 않는다.

## 고정된 검토 집합

- 저장소 `ktcloud4-SL/labbit-app`, feature `feat/LBT-147-vm-access`; 실제 정상 VM 실행 제품 코드 커밋 `bd8071d86659f2cce5d2c6e77cbb7f7ac0054d05`, 이번 package 검토 HEAD/source-manifest codeCommit `41f785e6e4e436f32c85ed21d1fed327ffaeb388`, 최신 main `7aaa5ca4e7f58cb88f188f91e653a624e4caed03`. 41f785e는 ssh_manager_backpressure_test.go 한 파일만 바꾼 test-only commit이다. manifest39 중 그 test1개 hash/blob를 갱신했고 production 구현은 변경하지 않았다.
- 게시 폴더 `docs/connector/evidence/LBT-147/2026-10-12-additional-verification/`의 정확한 21개 파일을 직접 읽었다. `artifact-checksums.json`을 제외한 20개 checksum target의 bytes/SHA256이 모두 일치한다. 누락·추가 항목 없음.
- 이번 고정 snapshot ZIP은 정확한351개 entry다. 중복 이름 없음, CRC, 개별 크기/SHA256, archive-manifest를 재검증했다. stale scratch prepared-reproduction/pinned-source-guard-fixture는 제외했고 초기 FAIL, 필수 source/runner/fixture, reviewer red/green, publication-format-red 및 remote-linux-fixture-red의 수정 전 원본은 보존돼 있다.
- 이번21/351 검증은 기존 외부 verifier를 현재351 snapshot으로 메모리에서 실행했고 저장소·추가 외부 파일을 쓰지 않았다. 당시 ZIP SHA256은 `e4eecf6f667f22f96a57e0d8f5e408103380148ea6feed9aadede0b233afb550`, artifact-checksums SHA256은 `94486de143df63da1021ccdf3504426b5b38af5287b69155daa8fc979db99834`다. 이전 publication-package21/staged-final JSON은 각각 당시326/336 집합의 기록이며 현재 집합으로 재명명하지 않는다.
- publication-review.md는 21번째 metadata 보고 파일이다. 이번 문구 갱신본 복사 뒤 root가 checksum/secret scan/file-set/archive를 재고정하고 reviewer가 마지막 exact byte-set을 재확인한다. 현재351 snapshot과 그 후 archive 집합을 구분한다.

## 소스와 실행 원본 추적

source-manifest의 39개 product/context 파일을 현재 byte SHA, Git clean-filter blob, 코드 커밋의 blob에 각각 대조했다. 39개 모두 내용과 매핑이 일치한다. 32개 Go 파일은 실행 checkout과 committed blob의 exact bytes도 같다. OpenAPI, go.mod/go.sum, runtime README/contract, Web package/package-lock 7개는 Windows checkout CRLF와 Git LF 차이만 있다. LF 정규화 내용과 clean-filter blob이 일치함을 직접 확인했다.

최신 정상 VM 실행 당시36개 source는 Go32 exact + go.mod/go.sum/runtime2 총4 EOL-only다. 이는 bd8071d 실행 기준이며 이번 test-only 변경 뒤 같은36개가 모두 현재 바이트라고 주장하지 않는다. 기존 independent-review의 PR84/78 exact+역사 JSON4/runtime2 EOL-only도 당시 snapshot이다. 현재 source39 mapping은 별도로 41f785e에 대조했다. finalGoSource는 실행 HEAD ce02b4c496c4ac931cb66db4adea92a71fbb3cc1 + working test1개를 기록한다. 실행 snapshot에 포함된38개 SHA(Go32 포함)가 현재와 exact 일치하며, snapshot에 없는 OpenAPI1개는 실행 HEAD와 현재의 Git blob 동일성을 확인했다. ce02→41f785e diff도 해당 test1개뿐이다. test SHA256은 `ade7705ea5d4ee846744a8566e7e464f6314e253a50092c88940cdead6688502`다.

| 실행 | 원본과 현재 소스 관계 | 확인한 결과와 제한 |
| --- | --- | --- |
| 전체 Go / 관련 race / 선택 회귀20회 | finalGoSource의 ce02+working test1개; 41f785e와 Go32 실행 byte 동일 | 2885 PASS/7 SKIP, 482 PASS/6 SKIP, 940 PASS/0 SKIP. ZIP raw의 새 resize-close mode와 counts를 직접 확인. subtest와 반복 action 수이며 서로 다른 test 수가 아니다. race는 관련6패키지다. |
| Web / contracts | 최신 main의 package/lock 동일 blob, fresh npm11 ci | 13파일/163tests, typecheck/lint/test/build; OpenAPI/8 schema/runtime·migration metadata 및 Reset11 PASS. DB application SQL integration을 실행했다고 주장하지 않는다. |
| M2 Provision / M3 lifecycle / Control WSS lifecycle / Internet ON/OFF | 실제 da6b670 source; dependency/main 차이 명시 | 각 suite 및 사후 inventory PASS. 과거 source의 실제 cloud 결과를 현재 source 실행으로 바꾸지 않는다. Provider 구조와 소유 exact-ID cleanup을 검토했고 바뀐 Terminal 경계는 아래 실행으로 재검증했다. |
| 실제 2VM R1/R2/R3와 짧은 부하 | 최신 production과 동일; 이후 ssh_backpressure_test의 standalone/log 보완만 다름 | parent12, child3 PASS, skip0. actual exec sleep Wait cancel/deadline 각10회 동안 A PTY 유지; scoped Nova/Neutron GET503은 주입; 실제 pin에 synthetic hostile key 제시; mock SaaS WSS + actual Provider/VM/SSH/PTY runControl. 2VM 각30회 작업은 identity를 비교하지 않는다. tracked10 및 보조자원 정리, 사후 matching0. |
| 최신 정상 VM integration | bd8071에서 직접 -race 실행, source36 일치, 사후 변경0 | 214.69초 PASS/skip0. strict target, PTY IO/resize, PTY close 뒤 SFTP REALPATH, 메모리 SSH_CONNECTION 비교로 share/idle 교체, gen2 Reset·fresh target·stale1 거절. tracked14 각각 GET404 및 최종 inventory matching0. aux keypair/managementSG는 own Delete와 prefix 조회0이며 각각의 별도 GET404로 과장하지 않는다. |

일반 Go의 7 SKIP는 cloud opt-in6 + Windows POSIX0600 assertion1이다. 관련 race의 6 SKIP는 cloud opt-in이다. 별도 실제 실행과 skip을 분리했고 기본 suite의 SKIP를 PASS로 합산하지 않았다. actual parent/child 및 최신 정상 VM은 fail/skip0이다.

initial-real-fixture-failure, tracing false assertion FAIL, initial gofmt CRLF 진단, Data/SSH red, 초기 candidate의 fixture cleanup 실패를 원본에서 확인했다. 최종 성공과 다른 소스·시점으로 보존했다. cleanup 확인 문구는 assertion 직전 로그이므로 문구만으로 성공 판정하지 않았고 testcase action PASS와 사후 조회를 함께 확인했다.

## 수정 전 게시 findings와 수용 상태

| ID / 심각도 | 위치·재현·영향 | 수정 및 독립 회귀 |
| --- | --- | --- |
| PUB-SOURCE-EOL / P3 | independent-review section7이 최신36을 Go/의존34 exact+runtime2 EOL-only로 과대 집계. PR84에 없는 go.mod/go.sum context 차이를 누락했다. 개별 source JSON의 raw SHA/blob 매핑은 정확했으며 제품 문제는 아니다. | reviewer가 수정 전에 보고한 뒤 Go32+EOL4로 정정. publication39의 exact32+정확한7 EOL 집합, raw SHA/clean blob/commit blob/LF 내용 동일성 assert PASS. 수정 전 report와 regression JSON 보존. 외부 Markdown 저장의 CRCRLF도 UTF8 LF로 정정했고 이전 바이트를 보존했다. |
| PUB-FILTER / P2 | 수정 전 .gitattributes:1–4의 text eol=lf가 CRLF artifact를 staging 시 변환. source-manifest:205–206 fixtureSHA와 두 fixture가 달라져 prepare_overlays:33–34가 재현을 거절. 외부 local clone의 실제 git add에서19 target 중11 hash·2 fixture 불일치 재현, cloud0. | `* -text`로 원본 바이트 보존. 독립 staging + fresh checkout의 당시 payload20개 모두 exact bytes, fixture4 SHA 및 fresh helper --verify-source 성공, cloud0. 이번21개 raw Git blob/clean-filter blob 동일 및20 checksum 일치. RED/GREEN JSON 보존. |
| F-EVIDENCE-GOFMT / P1(publication) | [원격 run38224917705](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38224917705), job114729949929 step5 Go format 실패. Makefile:25의 전체 *.go 범위에 게시 integration_metadata_test.go/read_only_residual_test.go가 포함됐다. GitHub job을 직접 read해 step6vet/7test/8build/9PostgreSQL skipped를 확인했다. | 원본2개와 run/job/hash를 ZIP publication-format-red에 보존. 현재 canonical/package2개가 수정 전 원본에 gofmt를 적용한 stdout와 exact bytes로 같다. 동작 변경 없이 integration single-line if/log 확장과 LF 정규화다. 현재 포맷 출력0·fixture/source pin·overlay compile/opt-out을 확인했다. 새 원격 CI는 pending이다. |
| F-EVIDENCE-FMT-DOC / P2(publication) | README:23의 Windows `gofmt -l`(소스만) 안내는 게시 fixture를 포함한 Makefile 범위를 명시하지 않아 같은 실패를 놓칠 수 있었다. reviewer가 수정 전에 보고했다. | 전체 Go 파일을 per-file gofmt -l로 검사하고 게시 _fixtures 포함 출력0을 확인하는 PowerShell 명령으로 정정. 그 명령을 독립 실행해 현재330개/출력0 확인. 보존 원본2개는 gofmt -l에서 검출했다. |
| F-SSH-MANAGER-LINUX-ASSERT / P1(test/CI) | [원격 run38226445266](https://github.com/ktcloud4-SL/labbit-app/actions/runs/38226445266), job114735013574. format/vet PASS 뒤 input-close/pending-resize가 원본 line142의 필수 internal/poll.(*FD).Write stack을4초 내 관찰하지 못해 unit FAIL, build/PostgreSQL skip. reviewer가 job steps/log를 직접 read했고 종료 assertion까지 도달하지 않아 제품 종료 실패를 관찰한 것이 아님을 확인했다. | SSHPTY.Write 200ms 지속 stack 관찰+inputDone 미완료로 변경. resize-close는 wantReply=false request의 조기 정상완료(err=nil을 완료채널 수신 후 검사) 또는 pending을 허용하고 log에서 관측을 구분한다. shutdown750ms, input/resize release, server EOF, ErrClosed 유지. kernel TCP pending resize는 SetWriteBuffer1024 별도 회귀가 담당한다. 수정 전 source SHA a8e344.../원격metadata를 remote-linux-fixture-red에 보존. 로컬 targeted20/race20, 전체·관련race·반복 PASS, 새 Linux/PG는 pending. |

제품 finding R1/R2/R3 및 Data/SSH Close·Resize·attachment ownership, partial-failure fencing/lazy initialization은 independent-review/AUDIT의 코드 감사·red/green·최신 실제 실행으로 해결 상태를 확인했다. 새 wire/runtime option 또는 전체 shutdown SLA를 추가하지 않았다. final Data ENDED의 1초는 session별 best-effort frame budget이며 전체 connector 종료1초를 뜻하지 않는다.

format 회귀 원본 publication-fixture-format-verification.json SHA256은 `368f7ad9f7807e07a1b68a1e807455800277b681fc570af22b491cdbe92cb2af`, 직접 read한 원격 job steps 기록 SHA256은 `0f774c42e4c4e9f91682bd2a1c4bd5bf90a4be3a236bd5faa6cb7163a97accd1`다. 새 fixture SHA는 integration `8d93b8c4053004f27cb2a0968a71f0f4162793799534a851da7b6aea261ff58c`, read_only `117152473e8384bfa309abaf88f2df50d84182ed16094a9efb05a15f74780842`다. 실제 cloud 실행 당시 원본 hash는 별도 tested-source/원본 기록에 보존하며, 포맷 변경 뒤 실제 cloud를 다시 실행했다고 주장하지 않는다. reviewer Python walk의332개는 node_modules junction alias2개가 추가된 superset이고, Makefile find 기본의 symlink directory 비추적과 README PowerShell 범위는330개다. 둘 모두 출력0이며332를 CI 실제 파일 수로 쓰지 않는다.

Linux에서 실제로 어느 write layer에 멈췄는지는 원격 goroutine stack 원본을 관측하지 않았으므로 확인된 production cause로 단정하지 않는다. pinned x/crypto의 channel.WriteExtended→remoteWin.reserve와 WindowChange→SendRequest(wantReply=false)를 읽어 SSH window가 먼저 소진될 수 있고 resize packet은 정상 완료할 수 있음을 확인했다. 입력 FD stack과 resize 필수 stack을 제거하는 test-only 방향을 독립 검토했으며, 별도 kernel fixture와 manager fixture의 combined race5회는 로컬 PASS다. 최종 resizeErr의 write→close(resizeDone)→receive→read 동기화도 별도 검토했다. 마지막 nil 검사 추가 뒤 root의20회·전체 실행은 ZIP raw와 위 실행 source pin에 대조했다. 로컬 WSL/Linux 환경은 없어 수정 후 Linux 실행은 새 CI로 확인해야 한다.

## 재현과 보안

prepare_overlays는 실행할 test를 자동 호출하지 않고 fixture 복사·절대 경로 overlay 준비만 한다. 외부 출력, 비어 있지 않은 출력 거절, fixture hash, 선택적39 source clean-blob 검사를 코드에서 확인했다. pinned-file 검사이며 전체 ignored/untracked producer guard라고 주장하지 않는다. README는 정확한 code commit checkout, 외부 credential/key, 로컬 cloud 환경, 순차 opt-in, 사전/사후 inventory와 소유 exact-ID cleanup을 명시한다. 실제 normal VM command/선택 플래그도 validation-summary에 있다.

별도 외부 회귀7개를 실행했다: 정상 current source, nonempty 출력 거절, repo 내부 출력 거절, 정상 isolated clone, 변경 source 거절, 누락 source 거절, corrupted fixture 거절. 모두 기대 결과다. 준비된 Provider+App overlay 두 패키지는 `-race -run '^$'`로 compile PASS했다. 이 실행은 test0/cloud0이며 실환경 PASS로 합산하지 않았다. root reproduction-checks의 준비4case/명시 opt-out SKIP와도 구분한다.

외부 validate_package.py의 고정 scratch P3는 실행마다 tempfile.mkdtemp를 사용하고 종료 시 임시 준비물을 정리하도록 수정됐다. 코드를 직접 읽고 root final-harness-repeat/run-1.log, run-2.log, summary.json의 연속2회 exit0/cloud0, source/fixture/output 준비 PASS와 explicit opt-out overlay compile PASS를 확인했다. 시작 UTC08:27:45.400/08:27:57.024이며 제품·게시 prepare_overlays.py 변경은 없다. 독립 검증 기록 publication-harness-repeat-verification.json SHA256은 `72c51c7fddeb27c076b9ff720017295dd0c1b5854afaede708527b78d340f30d`다.

필수 fixture4개는 _fixtures에 게시되어 Go ./...의 패키지 발견에서 제외된다. actual runtime child의 metadata는 parent가 소유한 VM에서 임시 생성하며 개인키·credential 내용을 게시하지 않는다. 당시 개인 절대 경로 runner는 원본 기록이고 portable helper의 repo/output 인자를 사용한다.

_fixtures가 Go ./... 발견에서 제외돼도 Makefile go-fmt-check의 find 범위에서는 제외되지 않는다. 이번 CI 실패가 그 차이를 보여줬다. format2개 수정 후 source-manifest fixtureSHA와 root/package/ZIP canonical4개 일치, prepare helper SHA 불변, 준비·명시 opt-out overlay compile을 확인했다. .gitattributes는 `* -text whitespace=cr-at-eol`로 byte를 보존하고 CRLF 판정만 허용한다. 이전 독립 probe는 정상CRLF exit0, 실제 trailing-space exit2와 trailing-whitespace 진단을 확인했다.

게시21개 및 ZIP351개에서 nonzip payload371개를 독립 검사했다. 알려진 로컬 secret/key 값3개와 PEM/Bearer 패턴의 일치0. 값은 메모리에서만 비교했고 출력·보고서에 복사하지 않았다. Terminal/SFTP 내용과 SSH_CONNECTION은 증거에 저장되지 않고 메모리에서 assertion한다. known-value/pattern 검사는 unknown secret나 모든 Git history 부재 증명이 아니다.

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
| archive-manifest.json | 이번 snapshot351 entry exact set/CRC/bytes/SHA |
| artifact-checksums.json | 자신 제외 정확한20 target set/bytes/SHA |
| audit-logs.zip | 현재/초기 실패/원문·source·fixture·runner·reviewer red/green 원본 |
| prepare_overlays.py | 준비만 수행, 입력·fixture/source hash·output guard |
| _fixtures/integration_metadata_test.go | 기존 실제 cloud suite helper·소유 cleanup/재조회·metadata-only 로그 |
| _fixtures/read_only_residual_test.go | GET/List만 사용, 알려진 local baseline과 prefix residual; mutation 없음 |
| _fixtures/real_failure_test.go | owned2VM/실제SSH·scoped503·child bridge·cleanup 및 제한 |
| _fixtures/actual_runtime_shutdown_test.go | production runControl/Provider/PTY와 loopback mock WSS; healthy/unread 분리 |

KT145 gen2/Tier2 실제 부재, 배포 Browser/SaaS routing·CC02/Live, production File WSS, 실제 SSH packet-stall fault·자연 outage/key rotation, hard restart/장시간 soak/대규모 load는 검증 밖이다. actual VM child의 서버 측 SSH EOF는 별도 관측하지 않았고 loopback actualManager 회귀만 EOF를 확인했다. 로컬 PostgreSQL integration은 미실행이다. 원격 run38224917705는 format 실패로 vet/test/build/PostgreSQL을 skip했고, run38226445266은 format/vet PASS 뒤 manager test assertion 실패로 build/PostgreSQL을 skip했다. 현재 fixture/doc/test 수정 후 새 Linux Go 전체 및 PostgreSQL PASS는 아직 확인되지 않았다.

검토한 package21개에 게시 차단 결함은 남지 않았다. 갱신 보고서와 harness 원본 inclusion 후 root의 checksum/secret scan/file-set/archive 갱신과 reviewer의 마지막 exact byte-set 확인으로 게시 집합을 고정한다. 감사는 별도의 외부 쓰기 승인을 대신하지 않는다.
