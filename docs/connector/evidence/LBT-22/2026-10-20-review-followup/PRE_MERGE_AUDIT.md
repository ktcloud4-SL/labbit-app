# PR #137 P2 follow-up pre-merge audit

## 검토 기준·전체 범위

사용자 개인 PRE_MERGE_AUDIT.md 전체 및 저장소 AGENTS.md/CONTRIBUTING.md/TESTING.md를 적용했다. branch `codex/fix/LBT-22-c2-ktcloud-acceptance`, base `9172e050f5660c80b84ba70d52f0c549d8f032d9`, 수정 전 HEAD `d21ee48394eea59d65755a67be18f8bb5a21fbd9`. base→working 전체14개 Go 파일과 기존 Evidence39개, 이번 Go2개 변경 및 새 Evidence 전체를 검토했다. staged/unstaged/untracked inventory와 검증 소스 hash는 source-manifest.json에 기록한다. manifest 자체와 최종 보고서는 내용 고정 후 Git index로 확인한다. ignored node_modules는 기존 설치본 재사용이며 버전 차이를 공개한다. 실행 helper는 결과 수집·임시 binary 배치용이며 재현 명령·환경·cleanup은 README에 기록했다. 개인 helper나 비밀 파일이 제품 실행의 의존성은 아니다.

전체14개 코드·테스트의 파일별 목적과 관련 consumer/SSOT 경계는 이전 [전체 diff audit](../2026-10-20-browser-followup/PRE_MERGE_AUDIT.md)의 파일별 표를 대조하며 다시 검토했다. app 안전 진단3파일, vmaccess3파일, terminal SSH PTY1파일, WSS2파일, Provider5파일이다. 새 제품 diff는 `ktcloud_d1_topology.go`의 obsolete guard3줄 제거, 새 테스트 diff는 `ktcloud_userdata_test.go:78–137`의 positive 회귀1건이다. 기존 C2 Evidence는 수정 전 HEAD 대비 변경 없음이며 새 테스트 결과로 소급 덮어쓰지 않았다. 전체 변경 중 제외하거나 미검토한 파일은 없다.

## 수정 전 finding

| ID·심각도 | 위치·재현·영향 | 근거·해결·회귀 |
| --- | --- | --- |
| R137-P2 / P2 | d21ee48 `internal/connector/provider/openstack/ktcloud_d1_topology.go:56–58`. 원문이1536바이트 초과이고 gzip-base64가2048바이트 이하면 실제 전송 가능한데 preflight에서 ErrProvisionConfig로 거절 | 외부 리뷰5581268329 및 runtime/README.md의 전송 크기 제한. 수정 전 사용자에게 보고하고 RED로 재현. raw guard만 제거하고 final gzip-base64 cap 유지. 새 positive 테스트로 preflight·SDK POST·원문/SHA 확인, 기존 overflow 테스트로 API0회 확인 |

기존 독립 감사에서도 이 raw guard와 신규 gzip 전송의 충돌을 놓쳤다. 기존 '차단 finding 없음'을 이번 경계까지 이미 검증했다는 뜻으로 확대하지 않는다. 외부 리뷰가 발견한 보완을 이번 RED→GREEN과 독립 재검토로 확인했다.

## 최신 SSOT와 요구사항 추적성

Jira [LBT-22](https://samsunglions.atlassian.net/browse/LBT-22) 본문/AC/최신 관련 댓글을 직접 재조회했다(updated 2026-10-19T21:52:37.638+0900, 검토 중). [D-20 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283/D-20+Lab), 독립 reviewer의 D-18 v5 재조회와 기존 HTTP/Connector/realtime/runtime/DB 소비 경계를 대조했다. Jira·Confluence에 쓰지는 않았다.

| 요구사항·원문 | 구현·consumer | 자동 테스트·실행 | 실환경·tested source | 공백·판정 |
| --- | --- | --- | --- | --- |
| R137-P2 + runtime/README.md:236 최종 base64 2048 | topology preflight와 `server.go:97–106` gzip→SDK base64 | 신규 `TestKTCloudCompressibleStartupPassesPreflightAndServerCreate` RED→GREEN 및 Linux 전체 PASS; final overflow 기존 테스트 PASS/API0회 | 이번 소스 manifest로 식별; SDK HTTP는 Mock. 신규 real cloud 없음 | 전송 가능 입력 거절 결함 수정 확인 |
| D-18 startup/snapshot 불변, Connector schema StartupScriptSnapshot, contracts/connector README, runtime contract | `provision.go:211–219` raw64KiB·SHA, snapshot 원문, server UserData clone | 신규 원문/SHA 유지 및 기존 empty/UTF8/HTML roundtrip PASS | 이전 live startupSHA 증거는 이전 실행 source에 한정 | 계약 변경 없음. 새 소스 real VM 미배포 명시 |
| LBT-22 prepared Browser/SaaS→SSH/PTY·reconnect/close | WSS→terminal→vmaccess→Provider target, realtime 소비 | 기본 Go 및 Provider lifecycle/Fake HTTP·SSH·WSS, Web286 PASS | 기존 C2 강사/학생 Browser/VM attach·resize·독립 shell·resume·close 기록/당시 live-source manifest | 대표 C2 증거 보존. 최종 PR binary 재배포/장기 soak 미실행 |
| D-20 READY·ownership·이력·불명 결과 재조회 | target generation/pin, mutationFailure·reconcile·close, HTTP/runtime/DB 소비 | 기존 malformedHTTP200 unknown/no retry·ownership·close 회귀 전체 PASS | 기존 actual PTY0/임시 probe cleanup/소유10 유지. 과거 OPEN_FAILED 원인 미확정 | 이번 상태기계·DB schema·durable 소유권 변경 없음. 전체 신규 Provision→READY/C3 추가판정 없음 |
| 안전한 오류 및 보안 경계 | vmaccess safe diagnosis→WSS SafeError→app allowlist | raw cause/secret/token/invalidUUID/sentinel adversarial tests 전체 재실행 PASS | 기존 배포 후 재검증 오류0은 과거 source에 한정 | 실환경 실패 주입·과거 원인 확정 없음 |

HTTP/realtime/Connector wire 및 DB migration은 변경되지 않았다. 전체 PR의 오류 소비/로그 허용 필드와 immutable snapshot을 해당 계약과 다시 대조했다. DB migration 재실행은 해당 없음이며 integration-tag PostgreSQL suite 미실행을 분리한다.

## 실패·보안·수명주기

- 입력: raw64KiB/SHA 유효성은 공통 request 검증에 유지되며 초과·불일치 입력은 외부 mutation 전에 거절한다. Provision 호출 순서상 settings gzip 뒤 request 검증이므로 첫 압축 자체가 raw64KiB로 제한되는 것은 아니다(전체 WSS 메시지 한도1MiB). 이 순서는 이번에 변경하지 않는다. final gzip-base642048 한도는 preflight/직접 EnsureServer 양쪽에 남아 초과시 외부 mutation 전에 거절한다. empty와 HTML/UTF8도 기존 테스트로 재확인했다.
- 외부 응답/재시도: 기존 malformedHTTP200을 OutcomeUnknown으로 유지하며 재조회 없이 POST를 반복하지 않는다. 신규 정상 경로 POST1회 검증. lifecycle/reconcile/재시작 durable 데이터·timeout 동작 변경 없음. crash/모든 외부 mutation 실패 주입은 이번에 추가 수행하지 않았다.
- 동시성/연결/소유권: 기존 shared SSH reservation·users·독립 channel·route readiness 및 close 경계를 재검토했다. 이번 guard 제거는 공유 상태를 추가하지 않는다. generation/server/host pin 검증 우회 없음. 단일 스크립트 입력에 대한 크기검사와 immutable bytes만 변경 범위다.
- 보안: 스크립트 원문/secret/raw provider 오류를 로그나 Evidence에 추가하지 않는다. 테스트는 공개 고정 fixture만 사용한다. 진단 allowlist와 outer sentinel 보존을 전체 diff/테스트로 재확인했다. 별도 권한·네트워크·설정·dependency 추가 없음.
- 리소스/cleanup: Linux 임시 테스트 binary와 유일 디렉터리만 생성하고 제거 확인. KT Cloud VM/네트워크·Connector 설정은 수정하지 않았다. 기존 C2 자원10개 유지 기록을 owned0으로 바꾸지 않았다. 미래 VM route 자동화, 장기 soak는 기존 제한이다.

## 실행·Evidence 및 두 번째 검토

2026-10-20 최종 소스(14개 파일 manifest)로 Windows 일반 Go package 전체 test, 전체 vet/build, Linux Provider PASS562/FAIL0/SKIP14, Go546개 canonical-LF gofmt 출력 없음, Web typecheck/lint/build 및23파일286테스트 PASS. RED의 예상 실패는 최종 검사 실패와 구분해 보존했다. 원본 validation 로그와 test-results.json을 대조했다. Linux의 실제 실행은 외부 서버에서 수행했지만 HTTP/SSH fixture 검증이며 실제 클라우드 생성 성공을 뜻하지 않는다. opt-in actual cloud14건은 환경 미설정 skip, integration build-tag PostgreSQL은 미실행. 목록은 manifest에 있다. Web 기존 Vitest5.0.3/lock5.0.5 차이 공개, push 후 exact-lock CI 별도 확인 예정.

기존 실환경 Evidence의 source/binary와 이번 검토 소스를 혼동하지 않는다. 기존 live-source와 PR-source manifest는 역사적 증거로 보존하고 이번 실행 identity를 별도 추가했다. 새 제품 소스 전체와 Git index blob 동등성을 확인한다. 원본 로그에는 인증값·개인키·transcript를 추가하지 않았으며 고정 테스트 입력과 실패·skip만 포함한다. 기존 Evidence39개는 수정 전 HEAD와 byte/Git blob 변경 없음.

구현과 분리된 reviewer `/root/c2_c3_audit`에게 base→working 전체 diff·SSOT·consumer·새 실행 로그·최종 Evidence를 재검토하도록 요청했다. 최종 결과는 [independent-review.md](independent-review.md)에 별도 기록한다. 외부 R137-P2를 수용했고 나머지 신규 차단 finding 유무는 그 기록과 함께 판정한다. 이 문서가 CI/실환경 skip을 PASS로 바꾸지 않는다. merge·배포·Jira 완료는 요청 범위 밖이며 push/재리뷰 요청은 사용자의 이번 명시적 지시에 따른다.

## 최종 판정

**PR 보완 가능.** R137-P2 해결과 RED→GREEN, 전체 기본 검사 및 독립 두 번째 검토를 마쳤으며 이번 범위에 잔여 차단 finding은 없다. 독립 검토의 압축 검사 순서에 대한 문서 finding을 수용·정정했다. 위 실제/Mock/skip/미실행 및 역사적 source 차이와 과거 오류 원인 미확정은 유지한다. 이후 Git index 소스 hash·diff 검사와 push 대상 확인 결과를 아래에 고정한다. exact-lock CI는 원격 최신 commit에서 별도로 확인한다.

최종 index 검사: 이번 staged25개(제품/테스트2 + 새 Evidence23), unstaged0/untracked0. manifest의14개 소스 SHA256/Git blob 일치, 전체546 Go working canonical bytes와 index blob 동등, `git diff --cached --check` 통과. 새 공개 Evidence 수동 검토 및 private-key/GitHub token/Bearer 패턴 검사에서 원문 자격정보 발견 없음. web-build의 줄 끝 공백만 정규화하고 원본/정규화 hash를 공개했다. 원격 PR의 base/head가 위 기준과 같고 branch도 동일함을 push 전에 확인했다.
