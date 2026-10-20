# PR #137 P2 review follow-up — 2026-10-20

[외부 리뷰](https://github.com/ktcloud4-SL/labbit-app/pull/137#pullrequestreview-5581268329)의 압축 전 base64 제한 지적을 수용했다. KT Cloud preflight의 raw-base64 2048바이트 검사를 제거하고 gzip 후 최종 base64 2048바이트 검사를 유지했다. 일반 OpenStack 분기, 원본 64KiB/SHA 검증, snapshot 내용, 생성 결과 불명 시 재조회·중복 mutation 방지 동작은 변경하지 않았다.

`TestKTCloudCompressibleStartupPassesPreflightAndServerCreate` 1건을 추가했다. 약4KiB 스크립트가 request validation → KT preflight를 통과하고 실제 SDK가 Fake HTTP server에 보낸 POST의 `user_data`가 2048바이트 이하이며 base64 decode/gunzip 후 원문·SHA가 일치하는지 확인한다. POST1회 및 입력 불변도 확인한다. **이 테스트는 실제 KT Cloud VM 생성 테스트가 아니다.**

## 실행 결과와 재현

실행일은 2026-10-20 KST. base `9172e050f5660c80b84ba70d52f0c549d8f032d9`, 수정 전 PR HEAD `d21ee48394eea59d65755a67be18f8bb5a21fbd9`. 최종 실행 소스는 [source-manifest.json](source-manifest.json)의 14개 전체 PR Go 파일 SHA256/Git blob으로 고정한다. RED는 새 테스트만 추가하고 제품 수정 전, GREEN과 전체 검사는 제품 수정 후 실행했다.

| 검사 | 결과·범위 | 원본 |
| --- | --- | --- |
| 신규 테스트 RED | 기존 preflight가 ErrProvisionConfig로 거절하여 예상 실패 | [regression-red.txt](validation/regression-red.txt) |
| KT startup/user_data GREEN | 신규 positive + 기존 최종 용량 초과 거절·빈값·UTF8/HTML roundtrip 통과 | [regression-green.txt](validation/regression-green.txt) |
| 기본 Go | Windows 일반 package 전체, vet/build 통과 | [test-results.json](validation/test-results.json), validation/go-*.txt |
| Provider 전체 | Linux amd64에서 PASS562(하위 테스트 포함), FAIL0, SKIP14 | [linux-provider-test.txt](validation/linux-provider-test.txt) |
| 포맷 | 추적 Go546개 CRLF→LF 정규화 후 gofmt exit0, 출력 없음 | [gofmt-summary.json](validation/gofmt-summary.json) |
| Web | typecheck/lint/build 통과, 23파일·286테스트 통과 | validation/web-*.txt |

Go1.27.1을 사용했다. Linux 기본 환경에서는 전체 `go test -count=1 -timeout=180s ./...`, `go vet ./...`, `go build ./...`로 재현할 수 있다. Windows에서는 Provider의 shell/SSH fixture가 Linux를 요구하므로 `go list ./...`의 `/provider/openstack`을 제외한 package 전체를 `go test -count=1 -timeout=120s`로 실행하고, `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/connector/provider/openstack`으로 만든 binary를 Linux에서 `-test.v -test.timeout=180s`로 실행했다. 원격 고유 임시 디렉터리에서 `PATH=/usr/bin:/bin`, 해당 디렉터리 HOME만 제공했고 LABBIT_/OS_ 외부 클라우드 opt-in 설정은 전달하지 않았다. binary·임시 디렉터리 제거 확인은 [cleanup 로그](validation/linux-provider-cleanup.txt)에 있다. 새 VM·네트워크·배포·자격정보 변경은 없었다.

RED 재현: 수정 전 HEAD에 신규 테스트를 추가하고 `go test ./internal/connector/provider/openstack -run '^TestKTCloudCompressibleStartupPassesPreflightAndServerCreate$' -count=1 -v`. GREEN: 현재 소스로 `go test ./internal/connector/provider/openstack -run '^TestKTCloud.*(UserData|Startup)' -count=1 -v`.

Web은 기존 의존성을 재사용해 `tsc --noEmit`, `eslint . --max-warnings=0`, `vitest run`, `vite build`를 실행했다. 설치 버전은 [web-installed-versions.json](validation/web-installed-versions.json); Vitest5.0.3은 lock5.0.5와 다르므로 exact-lock CI 결과를 별도로 확인한다. canvas 미설치 진단은 기존 테스트의 비실패 출력이다. opt-in 실제 cloud 테스트14건은 skip이며 이름은 manifest에 기록했다. integration build-tag PostgreSQL tests는 미실행이다.

Git diff 공백 검사를 위해 web-build 로그의 줄바꿈과 줄 끝 공백만 정규화했다. 원본/정규화 SHA256은 [log-normalization.json](validation/log-normalization.json)에 기록하며 실행 결과·진단 내용은 유지했다.

## 실환경 증거와 판정 경계

이전 [C2 실환경 Evidence](../2026-10-20-browser-followup/README.md)는 수정 전 PR의 역사적 기록으로 그대로 보존했다. 당시 배포 source/binary 식별과 최종 PR source 차이는 기존 manifest와 README가 설명한다. 이번 수정 소스 또는 최종 PR binary로 Browser→SaaS→KT Cloud 전체를 다시 실행하거나 배포하지 않았다. 이번 결과는 긴 압축 가능 스크립트의 preflight/SDK 전송 경계 보완이다.

과거 학생 `OPEN_FAILED`는 1회 발생했고 이후 C2 재검증에서 재발하지 않았으며 **원인은 미확정**이다. 이번 P2 수정으로 과거 오류 원인을 해결했다고 주장하지 않는다. 강사 route 운영 수정 범위, 소유 자원10개 유지, 최종 PR binary 실환경 미배포, 장기 soak/전체 신규 Provision→READY 미검증도 기존 기록을 유지한다. C3/G3/G4는 본 PR의 추가 검증 범위가 아니다.

감사 상세와 최종 독립 검토는 [PRE_MERGE_AUDIT.md](PRE_MERGE_AUDIT.md), [independent-review.md](independent-review.md)를 따른다.
