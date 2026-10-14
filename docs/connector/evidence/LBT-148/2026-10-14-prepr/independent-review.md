# LBT-148 최신 main 재통합 독립 감사 — 2026-10-14 KST

최종 판정: **Audit PASS / 최신 main 위의 LBT-148 실제 PTY 및 기존 CC-02 중간 통합 범위에서 PR 가능**. 새 Go/Web/Contracts 전체 검증과 source/consumer/SSOT, 과거 실제 VM source와의 동일성, 제출 artifact를 독립 확인했다. 새 historical evidence EOL finding은 원래 indexed bytes 복원 및 하위attributes로 해결되었으며 현재 미해결 confirmed finding 없음이다. Reviewer는 read-only source/git/원본 로그 분석을 수행했고 구현 수정·테스트 실행·실환경 작업·원격 쓰기를 수행하지 않았다. 이전 날짜의 감사 및 로그는 historical tested-source 자료로 보존하고 이 기록을 별도로 작성한다. 실제 Browser/SaaS C2·운영 SFTP·KT D1·VMAccess Reset/idle actual Acceptance의 새 실행/완료를 주장하지 않는다.

## A. 기준 버전과 전수 변경

- Repository: `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-pty-148`, branch `feat/LBT-148-pty-cc02`.
- 최신 fetch base `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6`; 검토 및 새 자동 실행 HEAD `283da48f98441395249a6ad897f62c3db18f52eb`, clean.
- 이전 audit base `45dfc486f2dab72ac705310e3f4f3acead1128f0`; 이전 코드/test tested source `f510f9d9d24ebbde261614fdc9fabb3bc64146c6`; 실제 OpenStack PTY/CC-02 tested source `efc22587667a53e11dd87d75c025db37512bc221`.
- old branch에서 최신 main 위로 rebase하여 commit identity가 바뀌었다. 현재 HEAD를 과거 실환경 실행 당시 commit이라고 표시하지 않는다.
- 사용자 최신 지시는 VMAccess 실환경 테스트 코드가 바뀌지 않았으면 재실행하지 않기, 최신 main 충돌 확인 후 branch push/PR 생성, main merge 금지다. 원격 작업은 부모 agent의 역할이며 이 reviewer는 수행하지 않는다.
- 사용자 `PRE_MERGE_AUDIT.md`, 현재 repo AGENTS/CONTRIBUTING/TESTING 및 owning SSOT를 적용한다.

최신 main에 추가된 3개 commit은 #93 frontend Mock LabSpec ownership, #92 LBT-164 Provision HTTP/CreationSnapshot 접수, #91 LBT-32 main-only GitOps update다. old base→새 main 34개 변경 파일을 열거하여 148 boundary 영향 범위를 식별했다. 새 main 자체의 무관 SQL·HTTP·frontend 기능 전체를 다시 감사하지 않았다.

새 base→review HEAD의 전체 PR diff는34파일: production6/회귀 및 실제 harness7/2026-10-10 historical evidence7/2026-10-14 evidence14다. 이전 independent review에서 전수 읽은 구현과 회귀13파일은 모두 f510f9d와 동일 blob이므로 그 full-diff 검토 결과를 연결하고 새 main의 producer/consumer 영향과 evidence source 관계를 재검토했다.

| PR 변경 범위 | 목적 및 검토 근거 |
| --- | --- |
| `terminal/client.go`, `pty.go`, `session.go`, `ssh_pty.go`, `wss/handler.go` | PTY 종료/마지막 output/remote exit, owner 교체·늦은 frame 및 error fence, async setup ownership, safe errors, write timeout. f510f9d와 production 동일; 이전 수정/회귀 검토를 재사용 |
| `mock/preview_gateway.go`, `mock/preview_gateway_test.go` | Mock data/text/ACK writer 직렬화와 32 concurrent frame 회귀. 새 frontend TS Mock과 다른 코드이며 최신 main 통합으로 변경되지 않음 |
| `provider/openstack/pty_cc02_integration_test.go` | 기존 Control/Data WSS와 실제 VM PTY/shared SSH/channel lifecycle opt-in harness. efc2258 actual test source와 동일 |
| `terminal/data_owner_test.go`, `data_frame_owner_test.go`, `data_write_test.go`, `lifecycle_test.go`, `managed_setup_cancel_test.go` | owner/cancel/late error/physical PTY cleanup/backpressure/final bytes/loopback SSH 회귀. f510f9d와 동일; 새 Go unit/race/repeat 원본 재검증 |
| `2026-10-10` evidence7파일 | 과거1af5c8e tested source 자료. 새 main의 전체 PASS로 해석하지 않음 |
| `2026-10-14` evidence14파일 | 과거f510f9d 자동/efc2258 실제 VM 및 portable runner/scan/hash/raw 자료. 뒤의 SSH/VMAccess 범위 정정은 원문과 일치하며 새 base 실행으로 덮어쓰지 않음 |

13 구현/test 파일은 위 목록에 모두 포함되어 있다. 새 제출 `2026-10-14-prepr`의9파일(README/pre-merge audit/independent review/source manifest/validation summary/raw zip/checksums/secret scan/.gitattributes) 및 historical2026-10-10의 새attributes2줄1파일도 별도 검토했다. 최종 제출 작업범위는 tested HEAD의34파일+이10개 evidence 변경으로44파일이다. ignored cloud config/private key는 로컬 입력이며 읽거나 복사하지 않았다. 기존 portable runner 및 required tooling은 historical 재현 절차에 보존된다.

## B. 최신 요구사항 및 SSOT 추적

외부 Jira/Confluence는 부모가 직접 재조회한 원문·버전 전달 범위다. Reviewer는 외부 자료를 별도 fetch하지 않았다. Jira LBT-148 updated `2026-10-14T09:19:08.151+0900`, In progress/comment0, AC 및 MockSaaS/Relay 중간 통합 허용 범위 동일. D-18v5, D-21v10, MVPv19 동일. 부모 조회를 reviewer 직접 조회라고 표시하지 않는다.

| 요구/결정 | 원문/버전 | 구현 및 새 main 영향 | 자동/실제 검증 | 공백·판정 |
| --- | --- | --- | --- | --- |
| AC1 실제 VM PTY 생성/input/stdout/resize/종료 | LBT-148 10/14 최신 | production SSHPTY/Session/Handler 모두 과거 실제 source와 동일 | 새 Go suite + efc2258 actual CC-02 -race133.81초 PASS | 현재는 새 cloud 실행 아님. byte-identical 실행 경계의 과거 결과를 source 관계와 함께 참조 |
| AC2 기존 Control/Data WSS 통한 실제 VM | Jira; Connector README§6/7 및 schemas | Go MockSaaS/Relay/Handler/Data client/실제 harness/connector schemas unchanged | 기존 actual VM+Mock central peer PASS, 새 owner/frame 회귀 | TS LabSpec Mock 변경은 중앙 Go peer를 바꾸지 않음. 실제 Browser/SaaS 전체 C2 미실행 |
| AC3 종료/실패 정리 및 형제 PTY/SFTP 유지 | Jira; D-18v5 | VMAccess/shared SSH/channel workers/PTY Close 모두 unchanged | 새 -race/count20 channel/cancel/owner/error regressions; 기존 cloud Aclose→B+SFTP 유지/전체SSH단절 cleanup | 전체 shared SSH 단절은 모든 channel 종료가 맞음. 실제 Reset/gen/idle suite와 구별 |
| AC4 LBT-20/C2 참조 가능 evidence | Jira; TESTING | historical tested source와 rebased HEAD/새검증을 별도 기록 | 새/기존 artifact27checksums,34sourcepins,rawzip50entry,7commitpatchmapping 직접확인 PASS | old evidence를 새 main 전체 검증으로 바꾸지 않으며 실제 byte-preservation 보강을 기록 |
| 60초 grace/current owner/no replay/본문 비저장 | D-21v10; realtime/runtime | 관련 source/contract/runtime unchanged | 새 자동 회귀, 기존 real harness3초 grace+shell state PASS | production60초 wall-clock 및 Browser credential/Live C2 미실행 |
| 실제 VMAccess 재실행 조건 | 사용자 최신 지시 | `vm_access_integration_test.go`, VMAccess production/dependencies unchanged | 별도 `TestOpenStackVMAccessIntegration` 재실행 없음 | CC-02로 Reset gen1→gen2/stale gen1/idle retirement의 실제 테스트를 충족 처리하지 않음 |

직접 확인한 latest Git SSOT: Connector/Realtime/runtime는 old base 및 f510f9d와 동일. HTTP 변경은 기존 Provision/LabExecution/LabInstance route의 권한·CreationSnapshot·error 설명이며 Terminal route/wire를 바꾸지 않는다. DB migrations unchanged. Backend README는 durable Provision 접수/LabInstance projection의 owning 책임을 추가했다.

## C. 새 main producer/consumer 및 실패·보안·수명주기 영향

- #92 `labexecution/snapshot.go`는 immutable snapshot의 `vms[].vmKey/role/instanceIndex`와 `workspaceVmKey`를 생성한다. 기존 `postgres/terminal.go:74`의 projected read 및 `terminal/targets.go:123`의 target validation shape와 대조했다. 새 snapshot의 opaque vm-1 keys는 consumer가 role로 재구성하지 않고 저장된 key를 사용하는 기존 계약에 맞는다. 새 `TestSnapshotDecodesAsConnectorCreationSnapshot`은 실제 protocol type의 unknown-field-free decode/unique key/workspace/digest를 검증한다.
- Provision 접수의 application boundary는 Provider/Connector/Terminal/File/Preview 직접·전이 의존을 금지하는 test를 가진다. 이는 durable 접수이며 request 내 실제 SSH/VM lifecycle을 실행하는 변경이 아니다. Worker dispatch는 후속이다. 부모 새 full unit 결과에서 해당 consumer 연결 회귀를 확인한다.
- `control_stack.go`는 LabExecutions service option을 추가했으나 기존 Terminals/Files/Previews/Live/Registry bindings는 그대로다. HTTP 새 route와 기존 `/lab-instances/{id}/terminal-targets`/terminal-sessions route가 별도로 등록된다. 기존 Terminal authorization/target resolver/repository SQL/manager lifetime은 변경되지 않았다.
- #93는 TS Mock LabSpec owner list/detail403 및 page 노출 방어다. Go MockSaaS/TerminalRelay, terminal protocol, Workspace file, shared SSH 및 actual test source와 별개다. 현재 frontend가 바뀌었으므로 예전 Web163PASS를 현재 Web 결과로 재사용하지 않고 새 Web 검증을 요구한다.
- #91 GitOps image update step은 `github.event_name == 'push' && github.ref == 'refs/heads/main'` guard다. 이번 feature branch push/PR는 그 main update 경계를 열지 않는다. reviewer는 GitOps repository/deployment 전체 기능을 감사하거나 원격 작업을 실행하지 않았다.
- main 추가34파일에 Connector, VMAccess, OpenStack, Connector/Realtime/runtime, go.mod/go.sum, Web package/lock, DB migration 및 기존 Terminal/Workspace/WSS consumer 변경이 없음을 git path diff로 확인했다. 기존 취소/owner/final output/backpressure 수정의 보안·리소스 lifetime은 변화가 없다.
- tombstone 장기 누적/restart 후 durable 복구 없음, forced kill/rolling restart, Browser/SaaS C2, production SFTP Tree/Read/Save, KT D1의 제한은 이전 audit 그대로다. 새 main 미관련 feature 전체 완성이나 DB tagged integration을 이 검토로 인정하지 않는다.

## D. 실행 source 및 evidence 검증

Reviewer는 원본 로그를 읽으며 모든 실행 주체는 부모다. 현재 source283da48로 아래 결과를 run metadata 및 raw event에서 직접 확인했고 Go event를 별도로 재집계하여 metadata와 일치함을 확인했다. pass/skip 수에는 subtest가 포함되며 no-test package skip은 test skip 수에 넣지 않는다.

| 새 실행 | 원본 결과 | 범위/제한 |
| --- | --- | --- |
| Go fmt/vet/build | 408파일 LF-normalized format mismatch0, vet/build exit0 | 새로운 main의 code 포함 기본 검증 |
| Go unit `./...` | 3429PASS/12testSKIP/0FAIL/36packagePASS | OpenStack/KT opt-in 및 OS별 fixture 분기 skip을 아래 실제 원본 목록으로 구별 |
| Connector7pkg race | 692PASS/11SKIP/0FAIL | vmaccess/openstack/app/terminal/wss/mock/preview |
| 중요4pkg count20 race | 420PASS/0SKIP/0FAIL | setup/cancel/late channel/owner/frame/error/Preview/shutdown 회귀 |
| Web typecheck/lint/test/build | 모두 exit0,13file167testPASS | 새 main frontend의4개 추가 test 포함. canvas getContext 경고 원본 보존 |
| Contracts | OpenAPI/8JSON schema/runtime/migration metadata PASS,Reset11testPASS | 현재 workflow exact scripts/pinned PyYAML6.0.3/jsonschema4.26.0/openapi-spec-validator0.9.0 |
| source 안정성 | auto/web/contracts 모두 unchanged true, HEAD283da48 전후 동일 | Web temporary imported pnpm lock 제거, tracked npm lock SHA256 전후 동일 |

새 main 관련 `TestSnapshotDecodesAsConnectorCreationSnapshot`, application no-provider/no-HTTP boundary test2개 PASS를 raw Go event에서 확인했다. `TestSSHPTY_RealSSH_EchoAndResize`와 실제 loopback SSH 자동 회귀는 unit/race에서 PASS이며 실제 OpenStack VMAccess/PTY opt-in과 구별한다. unit12SKIP 목록은 OpenStack WSS/M1/Provision/M3/Internet/PTY/VMAccess7개, KT D1 read-only/network2개, KT fixture symlink/replacement2개, dev-auth POSIX permission1개다. race11SKIP은 dev-auth OS분기를 제외한 목록이다. 실제 VMAccess Reset/stale/idle, 추가 OpenStack mutation, KT D1, PostgreSQL tagged 및 Browser/SaaS C2는 이번 실행에 포함되지 않는다.

과거 actual VM evidence는 efc2258,133.81초 -race PASS 및 tracked7ABSENT/pre-post servers0networks4/fixture residual keypair0SG0이다. 과거 artifact/source manifest에서 검토한 실제 생산/actual harness와 현재 branch 사이를 별도 git diff로 확인했다. f510f9d→현재 전체 Connector/VMAccess/provider/app/Mock, Connector/Realtime/runtime/go.mod/go.sum diff0. efc2258→현재 그 경계의 유일한 차이는 actual cloud package에서 사용하지 않는 `managed_setup_cancel_test.go` fixture 승인 순서다. VMAccess opt-in actual test source도 unchanged다.

사용자가 code unchanged이면 재실행하지 말라고 지시했고 actual boundary source/dependency가 그대로이므로 VMAccess actual 및 PTY cloud를 이번에 실행하지 않는 것은 승인된 범위에 맞다. 이 결정은 새 current commit의 cloud PASS를 주장하거나 별도 VMAccess-specific actual Reset/gen/idle 테스트를 대체한다는 의미가 아니다. 새 main의 SaaS Provision 접수/TS Mock 권한 변경은 기존 VM acceptance harness의 실행 dependencies가 아니다.

### 최종 제출 artifact 검증

- 새9파일/8artifact SHA256,2026-10-10의6artifact SHA256,2026-10-14의13artifact SHA256을 직접 재계산하여 총27 checksum mismatch0을 확인했다.
- 새 manifest의34 git blob 및34 current byte SHA256을 재계산하여 mismatch0이다. previous/current Connector tree,go.mod,go.sum,runtime tree,Connector/Realtime contract tree의6pins를 각각 git rev-parse로 직접 검증하여 모두 동일했다. actual VMAccess test source unchanged 및 actual CC-02 boundary의 fixture-only 차이는 별도 git diff로 확인했다.
- 신규 rawzip281001바이트/50entry,CRC error없음. 전체50entry를 repo 밖 원본 logs와 byte 비교하여 mismatch0이다. archive 안 Go events도 별도 재집계하여3429/12,692/11,420/0와 일치한다. rebase-range-diff 원본의7commit 모두 `=`이며 source-manifest mapping과 일치한다.
- secret scan은 기존/신규 제출 text 및 모든 archive entry239개, actual credential 값1개/provider endpoint1개 및 private key/PEM의 specific scan을 수행했고 matches0을 보고한다. universal detector가 아님을 명시한다. reviewer는 실제 secret 값에 접근하거나 해당 specific-value scan을 직접 재실행하지 않았다. 새archive에 private-key PEM marker가 없는 것은 read-only로 별도 확인했다. 과거cloud log의 lifecycle-only review 및 payload 메모리assert 범위도 보존된다.
- 새 README와 source-manifest는 실제sourceefc2258/과거자동f510f9d/현재자동283da48을 구별한다. restore된 historical5text의 LF indexed bytes와 과거Windows CRLFsnapshot bytehash를 구별하므로 historical records를 새실행 결과로 바꾸지 않는다. 최신 외부 README copy 예정 내용도 직접 읽었다.
- 새자료는 이전 tracked runner/validatorpins/도구·local cloud inputs를 상대링크로 참조하며 새개인helper를 요구하지 않는다. source snapshots는 새evidence 복사 전에 고정되었다. 실제cloud 추가API/생성/Reset/VMAccess/PTY 실행0이다. 기본Go의 actualloopbackSSH PASS와 cloud opt-in SKIP를 구별한다.

## E. Findings 및 상태

이번 최신main integration 검토 범위에서 신규 production confirmed finding 없음. 이전 INHERITED/ASYNC/OWNER/Preview mock/SSH fixture/portable env findings는 해결한 source와 동일 blob이며 새 Go 회귀 결과를 확인했다. 초기 race/leak/panic 및 fixture failure는 과거 로그에 보존되어 있다. 자료 접근 제한 및 미실행은 confirmed finding과 구별했다.

### F-148-EVIDENCE-EOL-01 / P2 — 역사 evidence 체크섬이 Windows checkout에서 불일치

- 발견/보고: 부모가 제출 package의 historical checksum 전수검사에서 발견하여 사용자에게 수정 전에 보고했다. reviewer도 수정 전 상태를 독립 확인했다.
- 위치: `docs/connector/evidence/LBT-148/2026-10-10/artifact-checksums.json:3-7`, historical directory의 text attributes unspecified.
- 재현 조건: 저장소 `core.autocrlf=true`의 Windows checkout에서 README/reproduce.ps1/secret-scan.json/source-manifest.json/validation-summary.json의 byte SHA256을 checksum 값과 비교한다.
- 관찰/영향: 5개 모두 indexed Git blob의 LF byte SHA256은 expected 값과 동일하나 checkout CRLF byte SHA256은 불일치한다. raw archive zip은 byte checksum exact이고 `git diff HEAD`도0이므로 잘못된 실제 테스트 결과가 아니라 재현 artifact의 checkout 바이트 정책 누락이다. 다른 사람이 원본 integrity를 검증할 때 정상 자료가 실패해 AC4 evidence 재현성을 방해한다.
- 요구/근거: AC4 참조 가능한 evidence, PRE_MERGE_AUDIT의 source/hash/재현 추적성. 직접 byte-hash 및 `git check-attr text eol` 확인 근거다. cloud/test 실행 근거는 아니다.
- 필요한 수정: 원래 indexed LF 바이트로 5 text 및 checksum index를 복원하고 그 directory에 `.gitattributes`의 `* -text`를 추가하여 Git checkout 변환을 비활성화한다. original indexed blob/checksum 값/raw zip/생산 코드는 유지한다.
- 필요한 검증: 전6 indexed artifact의 current byte SHA256 일치, original Git blob와5 text/index/zip equality, attribute text unset, 의도한 diff가 새attributes뿐임을 확인한다. production/실환경 테스트 재실행은 필요하지 않다.
- source 표기: 기존2026-10-14 source manifest의 historical SHA256Bytes는 당시 Windows CRLF working byte 기록이다. 원래indexedLF로 복원 뒤 그 일부 checkout SHA와 달라질 수 있으므로 과거 기록을 고치지 않고 새 prepr에서 동일 Git blob/새 byte preservation 정책을 구별해야 한다.13 구현/test 실행source는 영향을 받지 않는다.
- 수정 후 확인: 5text/index/rawzip 모두 originalHEAD Git blob bytes와 exact이고 기존6artifact SHA256이 모두 일치한다. historical text attribute는unset이며 staged diff는 새`.gitattributes`의2줄1파일뿐이다. text6개/rawzip/hash값/원래code blob 변경0이다. 새README/source notes에 originalCRLFsnapshot과 indexedLFbyte 정책 보강을 구별했다.
- 상태: 부모의 수정 전 사용자보고 및 reviewer의 수정 전/후 독립 검증을 거쳐 **해결**. Production/full/cloud 실행source가 바뀌지 않았으므로 자동·실환경 재실행 없이 artifact byte/attribute 검증으로 마감했다. 전체 현재검토 범위의 미해결 confirmed finding 없음이다.

## F. 독립 두 번째 리뷰와 한계

구현과 분리된 동일 reviewer가 최초 전수diff를 검토한 뒤 최신main 재통합에 대해 별도 두 번째 영향 검토를 수행했다. 전체 PR source 동일성과 base delta34파일의 관련 producer/consumer/SSOT를 확인하고 신규Go/Web/Contracts 및 최종artifact/evidence EOL 보강까지 직접 대조했다. 새 main의 무관 구현 전체감사로 확대하지 않았고, 외부 원문은 부모 직접 조회 전달 범위다. 현재base949c1a6/검증source283da48 및검토한evidence 범위는 **PR 가능**이다.

문서commit이 추가되면 부모는 `git diff 283da48..HEAD`가 신규prepr directory 및historical `.gitattributes`에만 한정되고 finaltree/checksum이 유지되는지 마지막 확인한다. 이 최종 독립기록의 전달본 반영에 따른 체크섬/scan 재생성은 실행코드 변경이 아니다. 새runtime/source 변화가 발생하면 이 판정을 그대로 재사용하지 않고 해당diff와회귀를 다시 검토한다. 기존 감사 통과와 이번audit는 외부승인 자체가 아니며 사용자의 이번 명시적 branch push/PR 허용 및main merge 금지 범위를 그대로 따른다.
