# LBT-148 / PR #95 Request Changes — 독립 재검토

작성일: 2026-10-14. 구현과 분리된 reviewer가 PRE_MERGE_AUDIT.md의 A–F 절차에 따라 검토했다. 코드·테스트·원격 상태를 수정하지 않았고, 새 테스트나 실환경 호출도 실행하지 않았다. 구현 담당의 원본 실행 결과를 독립 집계하고 source pin과 대조했다. 이 파일만 저장소 밖에 작성했다.

검토한 수정과 로컬 검증 범위는 PR 업데이트 및 재리뷰에 적합하다. **현재 미해결 confirmed finding 없음.** 최신 main과 결합한 새 GitHub CI 및 CODEOWNER의 Request Changes 해제는 이 보고서 작성 시점의 확인 범위에 포함되지 않는다. 이는 main merge 판정이나 Jira 전체/MVP/C2 완료 판정이 아니다.

## A. 전체 diff와 버전

| 항목 | 검토값 |
| --- | --- |
| 저장소 | `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-pty-148` |
| 브랜치 | `feat/LBT-148-pty-cc02` |
| PR | [#95](https://github.com/ktcloud4-SL/labbit-app/pull/95) |
| Request Changes | [review 5512252475](https://github.com/ktcloud4-SL/labbit-app/pull/95#pullrequestreview-5512252475), 2026-10-14T02:09:09Z; 본문·inline thread 직접 읽음 |
| PR merge-base | `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6` |
| 수정 전 공개 HEAD | `d509aac6faa873697273ee9c9fc1e681d8d33426` |
| 수정 후 tested HEAD | `f4999aa080d4f026a8ee06019d12afa01fb8f3b0` |
| fetch된 최신 main | `6b2c6dbd1cb495538367143f427dcd04905c3886` |
| 검토 시 로컬 상태 | f4999aa 코드 commit 이후 clean. 추가 evidence 문서는 이 뒤 별도 패키징 예정 |
| 전체 PR 변경 | base949 → f4999aa: 46파일 = source/test15 + 기존 evidence31 |
| 이번 수정 | d509aac → f4999aa: 4파일, 181삽입/3삭제. 생산2 + 회귀2 |

이전 full-diff 독립 검토는 `2026-10-14/independent-review.md` 및 `2026-10-14-prepr/independent-review.md`에 보존돼 있다. 이번에 전체 목록·기존 commit 이후 변경·관련 consumer/SSOT를 다시 대조했다. d509 이후 변화는 아래 4파일뿐이며, 다른 구현·기존 evidence는 이전 검토의 blob과 같다. 이전 감사가 놓쳤던 in-flight attach 실패 분기는 이번 finding과 새 negative 기록으로 명시적으로 보완한다.

| source/test 파일 (`internal/connector/` 이하) | 목적·요구사항·검토 경계 |
| --- | --- |
| `mock/preview_gateway.go` | Mock text/binary/ATTACHED write 직렬화. 공유 fixture의 기존 race/panic 방지 |
| `mock/preview_gateway_test.go` | 32개 혼합 writer의 관찰 가능한 전송 결과 회귀 |
| `provider/openstack/pty_cc02_integration_test.go` | 실제 VM + 기존 Go MockSaaS/Relay Control/Data WSS lifecycle harness |
| `terminal/client.go` | current data owner, attach ACK/cancel, stale frame·늦은 I/O 실패 방어, reconnect |
| `terminal/data_frame_owner_test.go` | 교체 후 INPUT/RESIZE/CLOSE/fatal 및 교체 전 시작한 I/O의 늦은 실패; Write/Resize 정리·형제 유지 |
| `terminal/data_owner_test.go` | client/connection owner 교체와 폐기된 owner 정리 |
| `terminal/data_write_test.go` | blocked Data write와 shutdown/best-effort final ENDED 경계 |
| `terminal/lifecycle_test.go` | final bytes, remote exit 분류, ended tombstone·grace·중복 OPEN |
| `terminal/managed_setup_cancel_test.go` | 실제 loopback SSH setup cancel/late reply/stdout/Wait worker·형제 channel; gate fixture 수정 포함 |
| `terminal/pty.go` | 원격 종료를 구분하는 TerminationProvider 경계 |
| `terminal/session.go` | stdout drain·종료·owner fence·tombstone·bounded write; **이번 owner 실패 close 원자성 추가** |
| `terminal/ssh_pty.go` | shared managed SSH PTY setup 소유권·late result 정리, stdout/stderr drain·Wait |
| `wss/handler.go` | 기존 session 재사용과 safe lifecycle 결과; **이번 이전 attach 실패가 새 owner session을 종료하지 않도록 수정** |
| `wss/handler_terminal_owner_test.go` | **신규** 두 in-flight OPEN 경합 분기: ACK 대기 취소, B 성공 뒤 A credential 실패 |
| `wss/handler_terminal_test.go` | 기존 current-owner attach 실패 회귀에 **실제 MockPTY closed-pipe assertion 추가** |

기존 evidence31파일의 범위도 누락하지 않았다. 각 디렉터리의 기존 checksum/raw/source pin/재현 runner/scan 및 독립 감사는 이전에 전수 확인한 내용이고 이번 코드 수정으로 변경되지 않았다.

| `docs/connector/evidence/LBT-148/` 이하 | 모든 파일과 역할 |
| --- | --- |
| `2026-10-10/` (8) | `.gitattributes`, `README.md`, `artifact-checksums.json`, `automated-logs.zip`, `reproduce.ps1`, `secret-scan.json`, `source-manifest.json`, `validation-summary.json`: 과거 실행·source·checksum 및 checkout 바이트 보존 |
| `2026-10-14/` (14) | `.gitattributes`, `README.md`, `artifact-checksums.json`, `check_pre_fix.py`, `independent-review.md`, `pre-merge-audit.md`, `reproduce.ps1`, `residual_harness.go.txt`, `secret-scan.json`, `source-manifest.json`, `validate.py`, `validation-logs.zip`, `validation-summary.json`, `verify_runner_env.py`: 통합·실VM·negative·잔여 inventory·재현/환경 격리 기록 |
| `2026-10-14-prepr/` (9) | `.gitattributes`, `README.md`, `artifact-checksums.json`, `independent-review.md`, `pre-merge-audit.md`, `secret-scan.json`, `source-manifest.json`, `validation-logs.zip`, `validation-summary.json`: base949 재통합의 자동검증·과거 VM evidence 재사용 경계 |

관련 호출자/consumer는 `terminal/client.go`의 claim/ACK/attach/cancel, `session.go`의 closeDataConn/grace/Close/Manager callback, `wss/handler.go`의 OPEN/CLOSE·safe response, Mock TerminalRelay, app의 shared VMAccess wiring, SaaS Router/Terminal use case를 확인했다. Router `internal/server/connector/terminal.go:398–417`는 replyToMessageId·labInstance·generation을 대조한 뒤 pending을 소비하므로 이전 OPEN 실패를 새 요청에 fallback하지 않는다. `internal/server/terminal/service.go:649–686`의 unmatched event도 다른 session을 종료하지 않는다.

최신 main의 base949 이후 15파일은 HTTP Reset/Cleanup 접수·Application/Repository/PostgreSQL 및 관련 테스트·backend 문서다. owning OpenAPI와 Application 변경을 Terminal 경계에 대조했다. 접수만으로 generation·상태를 바꾸거나 Provider/Terminal/Live 종료를 실행하지 않으며, 실행은 Worker의 범위로 남아 있다. Connector/VMAccess/SSH·runtime·go.mod/go.sum·Connector/Realtime 계약에는 변화가 없다. 무관한 새 main 기능 전체 감사를 이 LBT-148 리뷰의 완료 조건으로 확대하지 않았다.

초기 merge tree `17a34d6d8700ccfc3ea3947b361a280c330cb66a`는 d509 기준이었다. 부모가 f4999aa 기준으로 다시 생성한 tree `f3bfe3d69207905fae33977fc94effd1dc168191`도 conflict check exit0이며, reviewer가 이 tree를 읽어 Connector/runtime/deps/Connector·Realtime 계약이 f4999aa와 동일하고 차이는 최신 main의 15파일임을 확인했다. **merge-tree는 실행 결과가 아니다.** 새 PR merge source의 CI 결과는 별도 확인해야 한다. 로컬 자동검증은 base949 위 f4999aa의 실행이다.

## B. 최신 SSOT 추적성

이번에는 Atlassian 자료도 reviewer가 직접 read-only 조회했다. [LBT-148](https://samsunglions.atlassian.net/browse/LBT-148)는 updated `2026-10-14T09:19:08.151+0900`, 진행 중·댓글0이며 이전 원문과 같다. D-18 v5, D-21 v10, Connector MVP v19는 확정값/후속 범위가 이전 전달 내용과 같다. Git 원문과 repository `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md`, 개인 PRE_MERGE_AUDIT.md를 적용했다. 이전 감사의 외부 자료는 부모 전달 범위였으며 이번 직접 조회를 이전 감사에 소급해 표시하지 않는다.

| 요구사항·AC | 원문·버전 | 구현 | 자동 테스트·실행 | 실제 검증·tested source | 공백·판정 |
| --- | --- | --- | --- | --- | --- |
| AC1 실제 VM PTY 생성/input/stdout/resize/종료 | Jira LBT-148 최신; [D-18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) | SSHPTY/TerminationProvider/Session pump | 기존 실제 loopback SSH·drain/termination 회귀가 f499 전체/race에서 PASS | 10/14 actual PTY CC02 `efc22587667a53e11dd87d75c025db37512bc221`, -race 133.81s PASS | SSH/VMAccess/실VM harness/deps/runtime unchanged. 새 HEAD 전체가 cloud exact source라는 주장은 하지 않음 |
| AC2 기존 Control/Data WSS 통한 실제 VM evidence | Jira; Connector README§6/7, terminal schemas | Handler/client/Session/actual harness | **추가 owner failure 경계** 새 handler 두 case + current-owner cleanup, count100 -race PASS | 이전 actual VM + Git Go MockSaaS/Relay의 왕복 PASS. 이번 failure guard는 새 Mock WSS 회귀로 검증 | 중앙 peer는 Mock, actual Browser/SaaS 전체 C2 미실행. 연결 경계 변경에 필요한 회귀 충족 |
| AC3 종료/실패 뒤 미정리 PTY 없음·형제 Channel 유지 | Jira; D-18 shared SSH/독립 channel | managed SSH Close/Session.Close/CloseIfDataClientOwner | 기존 async/frame/I/O/shutdown 회귀 + 새 current-owner PTY Write→ErrClosedPipe, B session identity/echo 유지 PASS | 이전 cloud Aclose 뒤 B PTY+SFTP 유지; 전체SSH단절은 모든 channel 종료 확인 | 새 owner 경합은 MockPTY+loopback WSS. shared SSH 자체 단절 시 모든 channel 종료가 올바름 |
| AC4 LBT-20/C2에서 참조 가능한 evidence | Jira; TESTING/기존 test 전략 | historical evidence + 새 원본 logs/source pin/이 보고서 | negative fail/positive PASS 원본·tested blob·skip 독립 확인 | 과거 actual source·cleanup 기록 보존 | 새 최종 문서 archive/checksum/scan은 코드 검증 후 별도 패키징 대상 |
| 중복 OPEN은 추가 PTY 없음, 새 유효 Data attach는 current connection | Connector README:161–167/208–212 | `session.go:333–357`, `handler.go:1121–1124` | overlapping handler A/B, one allocation/same session/ACTIVE/B byte bridge/ENDED 없음/old socket closed PASS | 실VM 순차 duplicate/reconnect는 이전 actual evidence | 이번 경합은 actual cloud에서 재실행하지 않았으며 새 자동 회귀로 구분 |
| 동일 PTY 재접속·60초 grace·no replay·본문 비저장 | [D-21 v10](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516); realtime/runtime | 기존 timer·bounded transport·current owner/본문 미기록 | 기존 owner/grace/no replay 및 새 old failure 방어 PASS | 이전 actual harness grace3초·동일 shell/SSH/history=false | production60초 설정 unchanged; actual60초 wall-clock/Browser credential/Live fanout C2 미실행 |
| actual VM 검증에 Mock 중앙 peer 사용 가능; 기능별 자체/연결 회귀 | Jira; [Connector MVP v19 M4](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071) | 기존 Go MockSaaS/Relay + production adapter | WSS lifecycle 경합은 외부 VM 생성 없이 재현·검증 | actual VM evidence + 중앙 Mock 범위를 계속 분리 | M4 SFTP 전체 read/write(LBT-149), MVP 전체 lifecycle, C2 완료로 확대하지 않음 |

HTTP owning 계약은 최신 main의 mutation 접수 경계만 관련된다. DB migration·provider mutation·runtime·dependency 변경은 이번 fix에 해당 없음이며 Git diff로 확인했다. Browser/Live 계약은 PTY ownership/no replay/수명 제한을 대조하는 데 적용하고, Browser 인증 구현의 새 변경은 없다.

## C. 실패·보안·자원 수명주기

1. `CloseIfDataClientOwner`는 owner nil, current-owner 불일치, 이미 CLOSED이면 종료하지 않는다. 현재 owner일 때는 `claimDataClient`와 동일한 `Session.mu` 아래에서 CLOSED를 선점한다. 따라서 A 실패 cleanup과 B claim 중 먼저 lock을 획득한 행동이 종료 또는 대체를 결정한다. owner 확인 뒤 별도 Manager.CloseSession을 호출하는 TOCTOU가 없다.
2. `attachDataConn`도 동일 lock에서 CLOSED·owner를 확인한다. 종료 선점 이후 B가 attach해 session을 부활시키지 않는다. 정상 owner 교체가 먼저 끝났다면 이전 A 실패는 B의 PTY/connection/status/timer/tombstone을 건드리지 않는다.
3. 실제 `Session.Close`는 lock 밖에서 수행하며 `closeOnce`·timer 정리·DataConn/dataClient/PTY snapshot 후 해제·Manager ended callback을 그대로 사용한다. blocked SSH/Data IO 동안 전역 session lock을 보유하는 방식으로 바꾸지 않았다. 기존 bounded final Data ENDED/Control ENDED/PTY 정리 경계도 유지된다.
4. 실패한 client 자체는 owner 여부와 무관하게 `dataClient.Close()`한다. 현재 owner 실패는 정상 Close가 client까지 정리하고 두 번째 Close는 idempotent다. superseded owner 실패는 자신만 정리한다. ACK 대기는 기존 ctx cancellation/conn.Close로 풀리고 owner 검증 이후 새 연결만 attach한다.
5. 새 handler 회귀의 credential gate는 B 성공 뒤에 A failure를 풀어 늦은 결과 순서를 보장한다. ACK gate는 취소된 A socket의 관찰 가능한 종료와 B bridge를 확인한다. parent/control handler 두 개가 Manager를 공유하므로 Control 재연결 경계도 모델링한다. current-owner failure의 실제 allocatedPTY closed-pipe 검증은 tombstone만 확인하고 자원을 남기는 회귀를 막는다.
6. 종료 tombstone, old generation/target 검증, host-key pin, Credential/TLS/subprotocol/ACK correlation, wire safe error에는 변경이 없다. 새 test의 문자열·credential 오류는 합성 fixture이며 실제 secret·terminal 본문을 영속하는 생산 경로를 추가하지 않는다. 실패 wire payload는 기존 literal ATTACH_FAILED를 유지한다.
7. 프로세스 재시작의 durable Provider reconciliation이나 Terminal recovery 정책을 새로 정의하지 않는다. 이 수정은 ephemeral client 소유권 범위이며 main의 durable mutation 접수와 섞이지 않는다. 연결 자체가 사라진 기존 Shell을 새 SSH로 복구한다고 주장하지 않는다.

검토한 범위에서 위 수정 외 새로운 confirmed defect는 없다. 모든 가능한 scheduler 순서나 actual cloud packet-loss가 자동 테스트로 증명됐다는 뜻은 아니다. 원자성은 동일 mutex/CLOSED fence의 코드 분석과 제어된 경쟁 회귀를 함께 근거로 삼는다.

## D. 테스트와 Evidence

reviewer는 실행자가 아니다. 다음은 구현 담당이 실행한 원본 `tmp/LBT-148-pr95-reviewfix-20261014/logs`를 직접 읽고 Go JSON events를 다시 집계한 결과다. pass 수는 subtest를 포함한 **event 수**다.

| 실행 | tested source·범위 | 독립 확인 결과 |
| --- | --- | --- |
| 수정 전 negative | d509aac production + 신규 owner test. source-negative localStatus는 신규 test 한 개뿐 | 예상 exit1, fail3 = 두 subtest + parent. ACK case: B attach가 취소됨; credential case: 새 current session이 닫힘 |
| 수정 후 신규 회귀 | d509aac + fix4파일 working source. source-targeted 46 Git blobs가 f4999aa와 일치 | `-race -count=100`, pass400/skip0/fail0, package1 PASS |
| 전체 Go unit | f4999aa | pass3432/skip12/fail0, 36package PASS |
| Connector 7package race | f4999aa | pass695/skip11/fail0, 7package PASS |
| 기존 4package 반복 회귀 | f4999aa, count20 | pass420/skip0/fail0, 4package PASS |
| format/vet/build | f4999aa | fmt409 files, LF-normalized unformatted0; vet/build exit0 |
| Web | f4999aa | 13files/167tests PASS; typecheck/lint/build exit0; 기존 canvas warning는 실제 Browser 검증이 아님 |
| Contracts | f4999aa | OpenAPI, Connector/Realtime 8schema, runtime/migration metadata PASS; Reset schema 11tests PASS |
| source stability | auto/web/contracts before/after | 모두 unchanged:true, HEAD f4999aa; source-auto before/after 및 web after의 46개 filtered Git blob을 현재 source와 직접 대조해 mismatch0 |
| actual VM | 과거 efc2258 | 기존 actual PTY -race 133.81s PASS·cleanup7 ABSENT·전후 server0/network4·별도 residual keypair0/managementSG0 기록 보존. 이번 실행0 |

unit skip12는 OpenStack actual opt-in7(PTYCC02/VMAccess/M1/M2Control/M2Provision/M3/Internet), KT actual opt-in2, Windows 미지원 symlink/replacement2, dev-auth POSIX permission1이다. race skip11은 dev-auth1을 제외한 동일 범위다. tag-gated PostgreSQL Integration은 이 로컬 기본 suite에 포함되지 않는다. 최신 main backend PostgreSQL mutation 구현의 전체 검증을 이 branch의 unit PASS로 대신하지 않으며 PR merge CI가 따로 확인할 대상이다.

VMAccess/actual VMAccess test/runtime/go.mod/go.sum을 efc2258과 직접 diff해 모두0을 확인했다. actual PTY harness 및 SSH production도 동일하다. 단, efc2258 → f4999aa에서 production `terminal/session.go`와 `wss/handler.go`가 이번 owner cleanup 수정으로 바뀌었다. 따라서 **이전 actual cloud는 f4999aa 전체 exact-tested-source가 아니다.** 변경된 in-flight failure 분기는 새 Mock WSS+MockPTY 회귀로 검증했고, VM 생성/SSH/SFTP/resize/정상 lifecycle의 historical evidence는 unchanged 하위 경계 근거로 재사용한다. VMAccess 실환경 테스트 코드가 같으면 재실행하지 말라는 사용자 지시도 준수했다. 새 cloud API/VM 생성·잔여 inventory 호출0이다.

기존 archive/checksum/secret-scan/portable runner는 수정되지 않았고 이전 독립 전수 확인 기록을 유지한다. 신규 원본 logs에는 실패 기록과 before/after source가 모두 존재하며 새로운 최종 evidence 패키징은 본 보고서 이후다. 최종 docs-only commit의 production 동일성, archive/checksum/scan과 CI 결과는 구현 담당의 후속 확인 기록에 연결해야 한다. 개인 인증 파일은 재현자의 로컬 입력이며 본 보고서에 포함하지 않는다.

## E. 수정 전 finding 및 해결

| ID·심각도 | 수정 전 파일·라인/revision | 재현 조건·관찰 | 영향·SSOT | 수정·회귀·상태 |
| --- | --- | --- | --- | --- |
| F-148-ATTACH-OWNER-01 / P2, Request Changes blocking | `internal/connector/wss/handler.go:1121`, d509aac | A DialAndAttach 대기 → 동일 ID B claim이 A cancel → A error 무조건 CloseSession. ACK gate는 B attach 실패, B 성공 후 credential gate는 B session 종료를 실제 negative로 재현 | 새 owner의 정상 PTY/DataConn까지 종료; Connector README§6 duplicate OPEN/§7 current connection·PTY 보존 위반 | f4999aa `session.go:333–341` 원자적 owner+CLOSED fence, `handler.go:1123–1124` guarded sessionClose+실패 clientClose. 신규 두 경합+current-owner cleanup -race count100 PASS, **해결** |
| Evidence 설명 범위 / P3 | 신규 `pre-merge-audit.md` D의 수정 전 skip 문구 | 전체 skip12를 cloud opt-in12로 설명했으나 원본은 cloud9/Windows2/POSIX1 | 실행 범위를 과대 해석할 수 있는 문서 오류; TESTING/PRE_MERGE_AUDIT D | 수정 전 부모에게 전달, 사용자 보고 후 cloud9+Windows2+POSIX1/race11로 정정. current-owner test는 endpoint validation 실패이고 TCP dial/ACK 실패 검증이 아님도 명시. 코드·실행 변화 없이 **해결** |

새 severity/file/재현/영향/회귀 요건을 수정 전에 부모에게 전달했다. GitHub 요청 자체도 수정 전 공개돼 있었다. 이전 ASYNC/frame-owner/Mock Preview/fixture gate/runner environment/Evidence EOL findings는 별도 historical 감사의 해결 상태를 유지하며 이번 수정이 이를 되돌리지 않았음을 diff와 관련 regression 결과로 확인했다.

## F. 독립 리뷰와 최종 판정

구현 담당과 별도 reviewer가 동일 PR base·HEAD·working fix·최신 외부 원문·Git SSOT·전체 변경 목록을 다시 확인했다. 이전 설명/PASS만으로 owner fix를 인정하지 않고 claim/attach/close lock 순서, partial attach, late failure, current-owner cleanup, SaaS response correlation을 코드로 대조했다. 수정 전 실제 fail 원본과 수정 후 test 의미/source pin/전체 PASS를 확인했다. 코드 수정·실행·원격 write는 reviewer가 수행하지 않았다.

**판정: 검토한 코드 및 로컬 evidence 범위에서 PR 업데이트·재리뷰 가능, 현재 미해결 confirmed finding 없음.** 최종 패키지는 별도 생성하며 tested f4999aa 이후 코드가 다시 변하면 재검토가 필요하다. 최신 main 결합 source의 새 CI 결과와 CODEOWNER Request Changes 해제는 아직 이 보고서에서 확인하지 않았다. 따라서 merge-ready, actual current-HEAD cloud PASS, Jira/MVP/C2 전체 완료를 뜻하지 않는다. 사용자 승인 범위는 feature branch push·재리뷰 요청이며 main merge/배포는 수행 범위 밖이다.

잔여 후속 범위: actual Browser/SaaS/Live C2, actual60초 wall-clock, actual VMAccess Reset-generation/idle suite의 별도 기존 근거, LBT-149 SFTP 전체기능, Preview production shared Manager 통합의 기존 LBT-24 경계. 이번 좁은 owner failure guard 때문에 무관한 cloud mutation suite를 반복해야 한다는 근거는 없으며, 변경 경계의 제어된 경쟁 회귀로 보완했다.

## 최종 evidence 추가 검토

위 코드 리뷰 저장 후 생성된 `docs/connector/evidence/LBT-148/2026-10-14-pr95-reviewfix/` 9파일도 독립 검토했다. `.gitattributes`, README, source manifest, validation summary, pre-merge audit, 이 독립 리뷰, 원본 ZIP, secret scan, artifact checksum이며 코드 추가는 없다. tested f4999aa의 46파일 source 목록과 최종 evidence9파일을 구분한다(최종 제출 범위는 합계55파일).

- 새 artifact checksum8개 및 기존 checksum27개를 현재 checkout bytes와 대조해 mismatch0. `.gitattributes`의 text:unset 직접 확인.
- 새 ZIP 299522bytes/58entries CRC 정상, 58entry 모두 저장소 밖 원본 파일과 byte-exact. negative log/source/meta가 포함돼 있고 archive에서 negative3fail 및 positive400/3432/695/420 PASS events를 다시 집계해 요약과 일치함을 확인.
- source manifest의 46개 Git blob을 f4999aa object와, SHA256을 현재 파일과 각각 대조해 mismatch0. latest main/base/merge-tree/current/historical source 구분도 정확하다.
- copied independent review의 원본 바이트 일치 확인. README의 새 targeted 명령과 tracked 기존 runner 재현 경로, MockPTY/실제 loopback WSS·historical actual VM 한계, 새 CI 후속을 확인.
- scan 원본은 historical/new 제출 text·ZIP entry303개에서 특정 실제 Credential/endpoint/private-key bytes 및 PEM 검사를 수행해 matches0이다. reviewer는 실제 secret 값을 열거나 출력하지 않았으며, 새 ZIP의 PEM private-key marker도 직접0을 확인했다. 특정값 scan을 universal detection으로 확대하지 않는다.
- audit의 skip 분류 및 current-owner endpoint failure 한계 정정도 확인. 최종 archive/본문은 새 VM exact-HEAD PASS나 최신 main 통합 실행 PASS를 주장하지 않는다.

이 addendum을 포함한 독립 문서를 다시 복사하면 artifact checksum/scan을 재생성해야 한다. 코드/source pin 및 raw logs는 바뀌지 않는다. 생성된 최종 evidence 범위까지 추가 confirmed finding 없으며, 최종 docs-only commit의 생산 코드 동일성과 원격 CI/리뷰 승인 상태는 이후 단계에서 별도 확인한다.
