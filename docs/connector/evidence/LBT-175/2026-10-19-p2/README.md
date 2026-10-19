# PR #121 · P2 후행 HTTP 데이터 검증 보완

실행일: 2026-10-19 KST. PR121-F01(P2)은 해결했고 회귀·관련 검증과
현재 수정 소스의 Linux 전체 기본 검증을 실행했다. Linux Go는
3979 PASS / 14 실환경 opt-in skip / 0 FAIL이며 fmt/vet/build도 PASS다.
기존 Windows POSIX CIDR 실패 기록은 보존한다. Linux 실행·정리 기록까지
독립 최종 검토를 완료했고 검토 범위에서 추가 확정 결함은 없었다.
제안된 PR 변경 범위는 PR 가능으로 판정한다. KT Cloud 신규 실환경 실행·SaaS/Browser C3와 구분한다.
commit/push/PR 수정 없음.

## A. 기준과 전체 변경 검토

- PR: https://github.com/ktcloud4-SL/labbit-app/pull/121
- base: `89a698dac50a8705ed305a5eccf94ee9505b033f`
- HEAD: `ef921c106f621d1282c14e7e8b429cfda107e9c5`
- 최신 main: `8369790226f6823c1531dacff7147923611973bb`
- branch: `codex/feat/LBT-175-ktcloud-preview-validation`
- base→HEAD 56파일과 미커밋 Go 2파일, 이 addendum 4파일을 검토했다.
  파일별 목적과 경계는 [reviewed-files.json](reviewed-files.json)에 기록했다.
  historical hash manifest는 전항목 재계산했고 수천 hash를 수동 비교했다고 주장하지 않는다.
- 적용 지침: 사용자 `PRE_MERGE_AUDIT.md` A–F, repo `AGENTS.md`, `TESTING.md`,
  `CONTRIBUTING.md`. 미검토 변경 파일 없음. ignored 원본·도구·key는 역할과
  재현 의존성을 확인했으며 credential·실제 주소는 이 기록에 복사하지 않았다.
- 새 실행 소스는 [source.json](source.json)의 530파일 canonical digest와
  historical compiled manifest 대비 Go 2파일 hash delta로 식별한다.
  historical [KT 기록](../2026-10-19/README.md)과 manifest/log는 변경하지 않았다.

## B. 최신 요구사항과 추적성

Jira [LBT-175](https://samsunglions.atlassian.net/browse/LBT-175)는
updated `2026-10-16T09:24:01.253+0900`, comments 0인 최신 원문을 직접 조회했다.
[D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) v5,
[검증 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637) v3도
직접 조회했다. Git owning SSOT는 `contracts/connector/README.md` §7b와
Preview Control/Data schema다. HTTP/Realtime/DB/runtime/Backend 책임 경계도
전체 diff에 대조했다. 이 PR은 해당 production 계약·schema·runtime을 변경하지 않는다.

| 요구사항·AC / 원문 | 구현·자동 회귀 | 실제 실행·Evidence / 공백·판정 |
| --- | --- | --- |
| 최신 main·기본 회귀 / LBT-175 범위1·6, AC4, TESTING | 전체 fmt/vet/test/build, Web4종, 계약; 임시 통합 소스 | 현재 소스 Linux fmt/vet/build PASS, 전체 Go 3979 PASS/14 cloud-gate skip/0 FAIL. Web/계약·main+로컬 수정 관련10pkgs race PASS. 기존 Windows CIDR 실패 보존 |
| Management Tier·Resolver·host pin / 범위2, D-18 #8·9·11 | `application`, 기존 VMAccess/Provider | 과거 KT 두 세대 pinned SSH·direct-tcpip 실행; 새 실환경 실행 아님. Connector binary 동일 |
| Gateway→Connector→KT HTTP / 범위3·4, AC1, §7b | `previewTransportHTTP`, `validatePreviewTransportHTTP`; 새 정확성·TLS WSS 회귀 | P2 수정 전 7부정 사례 재현, 후 20회 race PASS. 과거 KT body 일치 결과와 새 strict tail 검증은 구분 |
| ATTACH 인증·전체 correlation / §7b·Data schema | peer `serve`, exact binding/upgrade tests | 새 fixture 반복20회 PASS. 실제 테스트 TLS/인증 Data WSS이며 Connector 쪽은 synthetic |
| 순차 active≤1·duplicate·session 격리 / §7b | `normal`, peer active accounting | 과거 KT peak1/accepted3·6; 새 관련 회귀 PASS. 장시간 부하 미검증 |
| port·대상·앱 미실행 거절 / 범위5·AC2 | `open`, `normal` negative table | 과거 KT PORT_REJECTED/APP_NOT_RUNNING/VM_UNREACHABLE. 살아있는 VM route 단절 주입은 미실행 |
| CLOSE·Gateway 종료·retry / AC2·§7b | `closeSession`, `normal`, 기존 lifecycle tests | 과거 KT double CLOSE·1000/1011·TCP0·retry; 새 관련 race PASS. 모든 장애조합 보장 아님 |
| Reset/Cleanup·stale generation·drain / AC2, D-18 | `beforeMutation`, `afterMutation`, `finish`와 main lifecycle | 과거 KT active Reset/Cleanup close1000·UNAVAILABLE·TCP0·drain. 새 KT Cloud mutation 없음; Linux용 local fixture는 별도 생성·정리 |
| Control EOF/결과 전송 실패·4001/4002 / §7b | 기존 production WSS/Preview owner tests | 새 관련 race로 회귀 확인. production 무변경, KT 해당 fault 주입 아님 |
| recovery·불명 mutation·소유권 / 범위5, AGENTS | transport fresh-exclusive gate·bootstrap recovery isolation | 기존 unit 포함 새 fixture PASS. 새로운 blind retry·durable 정책 없음 |
| content·secret 비저장 / 범위8·§7b | generic validation errors, `finish`, allowlist export | 새 오류 비노출 회귀·변경 파일 secret 검사. 과거 Control secrecy stage 유지 |
| 자원정리·baseline 보존 / AC2, D-18 #12 | 기존 typed ledger/cleanup·export | 과거 Lab10/bootstrap8 삭제·owned0·7종 exact inventory 원본 재계산 일치. 이번 local Linux fixture 2회 각각 Lab7·Management SG·keypair GET absence 확인 |
| Terminal 교체 socket 회귀 / TESTING | `data_connection_backpressure_test.go` 기존 PR 변경 | 이전 old-unread/new-drain·join 검토, 새 관련 race 통과. 이번 수정 없음 |
| PR/CI·C3 인계·완료 경계 / 범위7·AC3·4 | historical README/AUDIT·PR121 | HEAD 과거 CI4/4 PASS는 새 미커밋 소스 검증 아님. 새 CI/approve/merge 미실행. 실제 Backend/SaaS/Browser/DNS/TLS/Ingress C3는 후속 |

## C. 실패·보안·수명주기

`http.ReadResponse`와 body 읽기가 같은 buffered reader를 공유하고, 정확한 body를
검사한 뒤 남은 stream의 EOF를 요구한다. Content-Length 뒤의 추가 bytes,
다른 HTTP 응답, reader buffer를 넘는 tail을 성공 처리하지 않는다. 오류는
응답 원문을 포함하지 않는다. 단일 `Connection: close` 요청을 보내는 fixture의
판정만 보완하며 production raw byte forwarding 동작은 변경하지 않는다.

실제 TLS Data WSS 회귀는 정상 분할 응답을 허용하고 같은/later frame의 tail 및
두 번째 응답을 거절한다. 정상 close1000과 기존 queue/frame/HTTP/body budget을
유지한다. synthetic Connector worker는 read/write deadline과 channel join을 사용한다.
독립 reviewer는 Content-Length·HTTP1.0 close-delimited·chunked·합법 chunk trailer,
tail/NUL/truncation/second response도 별도 overlay로 실행했다.

추가 state·retry·DB·영속 자원·식별자 재사용·재시작 정책은 만들지 않는다.
Control의 명시적 4001/4002 종료·소유권·generation 경계는 기존 코드와 관련 회귀로
확인했다. fixture Python 앱이 path/query/Host/Cookie를 따로 검증하지 않는 기존
공백은 유지하며 이 P2의 응답 stream 완전성 검사와 구분한다.

## D. 실행·실패·skip와 Evidence

명령·집계·원본 digest·초기 준비 실패·소스 안정성은
[validation.json](validation.json)에 기록한다. counts는 unique testcase 수가 아니라
반복·parent/subtest를 포함한 Go event다. 원본 로그는 workspace의
`tmp/pr121-p2-fix-20261019/`에 보존한다. 과거 KT 원본은 기존 보호된 `.local`에 있다.

Windows 전체 Go와 Provider 포함 race의 CIDR 실패/timeout을 보존했다.
독립 reviewer가 수정 전 통합 checkout에서도 같은 실패를 재현했으며
`ktcloud_d1_cidr.go`의 0700/0600 및 directory fsync 전제 때문임을 확인했다.
보안 조건 완화나 임의 test skip으로 덮지 않았다. 지원되는 관련10pkgs race의
Windows symlink skip 1건도 PASS와 구분했다.

보류됐던 Linux 전체 기본 검증은 현재 소스로 새 실행했다. source archive 654파일의
원본 hash, 530 Go·mod/sum 입력의 LF canonical, 실행 후 소스 안정성을 대조했다.
Ubuntu Linux/amd64·Go1.27.1·CGO_ENABLED=0에서 fmt/vet/test/build exit 0이며
전체 Go 3979 PASS/14 skip/0 FAIL, 테스트 package 43 PASS다.
반복·parent/subtest를 포함한 event 집계다. 10개 testless package는 테스트 PASS와 구분한다.
14개 실환경 opt-in skip의 이름과 사유, 명령·환경·원본 hash는 validation.json의
linuxRerun에 기록한다. 이 실행에서 Linux race는 수행하지 않았으며 기존 Windows
반복 race 및 독립 framing 검증과 구분한다. 과거 Linux 3970 PASS를 재표시하지 않았다.

Linux 실행은 local OpenStack의 일회성 VM 안에서 수행했으며 KT 인증정보를 전달하지
않았다. offline Go/module archive를 검증하고 LABBIT_/OS_ 환경을 제거했다.
초기 API 인증 timeout, 준비용 go.sum checksum 139줄의 원본 복원, 첫 archive의
과거 Go fixture 6파일 누락에 따른 실행 전 실패도 원본으로 보존했다. 첫 시도는
전체 Go가 시작되지 않았고, 보완한 별도 attempt02에서 위 실제 결과를 얻었다.
두 시도 모두 typed Lab7개와 별도 Management SG/keypair를 정확한 ID로 정리하고
GET absence를 확인했다. 사용자가 켠 OpenStack host VM은 종료하지 않았다.
원본은 `tmp/pr121-linux-rerun-20261019/`에 보존한다.

새 KT Cloud·PostgreSQL integration·GitHub CI·Browser/SaaS 공동 E2E는 미실행이다.
새 Linux 자동 검증 PASS를 해당 실환경 E2E 성공으로 확대하지 않는다.

## E. 수정 전 finding과 해결

`PR121-F01 / P2`: HEAD의 `tools/ktcloud-d1-acceptance/preview_transport.go:138`
(함수132–143). 정상 HTTP200·정확한 Content-Length/body 뒤의 추가 데이터나
HTTP503 응답을 붙이면 nil로 통과했다. §7b raw stream 검증에서 중복/추가 bytes를
놓치는 fixture 결함이며 실제 production 손상 발생을 관찰한 것은 아니다.
수정 전에 severity·위치·재현·영향·회귀 방향을 보고했고 사용자 요청으로 수정했다.
현재132–148의 shared reader/EOF 검사와 parser·TLS WSS 회귀로 해결했다.

## F. 독립 리뷰와 판정

구현하지 않은 `/root/pr121_p2_final_review`가 같은 전체56파일·최신 SSOT·로컬
수정을 독립 검토하고 fixture race3회, 추가 framing overlay race3회, 기존 Windows
baseline 실패 재현, 원본 Evidence/secret 재검사를 수행했다. 검토 범위에서 추가
확정 결함은 없었다. 신규 addendum 4파일도 최종 diff에 포함해 재검토했고,
530파일 소스·60파일 목록·9개 실행 원본 집계/hash와 민감정보 검사 일치를 확인했다.

이후 같은 reviewer가 새 Linux 실행·정리와 갱신된 addendum 4파일도 독립 검토했다.
3979 PASS/14 skip/0 FAIL과 명령 exit 0, 530 입력의 hash·실행 후 안정성,
33개 원본 size/hash·skip 사유를 재집계했다. Windows에서 실패한 20개 사례와
기존 timeout root가 Linux에서 각각 PASS이며 새 P2 parser/TLS WSS 14개
root/subtest event도 PASS임을 확인했다. 두 시도 exact cleanup과 마지막 local
서버0·네트워크4 복귀, 전체60파일 민감정보 검사도 확인했다. 보고서와 독립 checker
digest는 validation.json의 review.linuxFinalReview에 기록했다. 추가 확정 결함 없음.
최종 read-only 원격 ref 확인에서도 main과 PR branch HEAD는 위 기준과 같았다.

**PR 가능:** P2 수정, 현재 Linux 전체 기본 검증, 관련 회귀와 전체 변경의 독립
검토가 충족됐다. 14개 cloud-gate skip·Linux race 미실행·기존 Windows POSIX
실패를 무조건 ALL PASS로 바꾸지 않는다. 신규 KT Cloud/remote CI·실제 SaaS/Browser C3와
PR 승인·병합은 별도이며 LBT-175 전체 완료를 추가 선언하지 않는다.
commit/push/PR 변경은 수행하지 않았고 이 판정은 push 승인이 아니다.

## 재현

저장소 루트에서 Go 1.27.1·GCC·Node/pnpm·기존 계약 validator를 준비한다.
Windows에서는 POSIX 의존 전체 Go 실패가 발생하므로 전체 Go는 Linux에서 수행한다.
cloud integration 환경변수는 설정하지 않는다. 합성 TLS WSS 테스트에 실 Credential은 불필요하다.

```sh
go test -mod=readonly -race -json -count=20 -timeout=5m -run '^(TestPreviewTransportResultsAndHTTPAreExact|TestPreviewTransportHTTPRejectsTrailingDataFrames)$' ./tools/ktcloud-d1-acceptance
go test -mod=readonly -race -json -count=20 -timeout=5m ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap
go test -mod=readonly -race -json -count=1 -timeout=5m ./internal/connector/vmaccess ./internal/connector/app ./internal/connector/terminal ./internal/connector/wss ./internal/connector/workspacefs ./internal/connector/workspacefile ./internal/connector/filetransport ./internal/connector/preview ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap
go vet -mod=readonly ./...
go test -mod=readonly -json -count=1 -timeout=5m ./...
go build -mod=readonly ./...
```

fmt는 tracked/untracked Go 전체를 LF 정규화 후 gofmt 출력과 비교했다. Web/contract/
cross-build 재현은 기존 `docs/connector/evidence/LBT-151/2026-10-14/validate.py`의
`web`, `contracts`, `build` mode와 `LABBIT_EVIDENCE_REPO/OUT`을 사용한다. Web의
pnpm adapter·frozen install·원본 npm lock hash 보존, contract validator 버전은
기존 runbook 및 validation.json에 기록했다. 새 출력 디렉터리를 사용해 실패 원본을 보존한다.
