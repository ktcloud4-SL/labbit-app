# PR #95 P2 중복 OPEN 회귀 보완 — 2026-10-14

동일 TerminalSession의 이전 Data attach 실패가 새 owner의 PTY를 닫는 [리뷰 요청](https://github.com/ktcloud4-SL/labbit-app/pull/95#pullrequestreview-5512252475)을 보완했다. `Session.mu` 안에서 owner 확인과 CLOSED 상태 전이를 함께 처리하고, 실패한 client 자체는 정리한다. wire/schema/Manager 구조 변경은 없다.

검증 소스 `f4999aa080d4f026a8ee06019d12afa01fb8f3b0`. fetched main `6b2c6dbd1cb495538367143f427dcd04905c3886`, PR diff merge-base `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6`. merge-tree 충돌 0이며 게시된 feature history를 다시 쓰지 않았다. 이후 evidence commit은 이 디렉터리만 추가한다.

새 테스트는 수정 전 두 케이스 모두 FAIL, 수정 후 `-race -count=100`과 기존 현재 owner 실패 cleanup 테스트까지 PASS(400 test events, skip0). Go unit3432 PASS/12SKIP/36pkg, Connector race695 PASS/11SKIP/7pkg, 기존 회귀20회420PASS, fmt409/vet/build PASS. Web 13files/167tests와 typecheck/lint/build, Contracts PASS. Go 숫자는 subtest event 포함이다.

이번 테스트의 네트워크는 실제 loopback WebSocket, PTY는 Mock echo다. VMAccess 구현/실환경 test 변경이 없어 사용자 지시대로 live test는 재실행하지 않았다. 앞선 실제 VM 133.81초 결과는 [원본 기록](../2026-10-14/README.md)에 보존하며 **변경된 현재 handler/session의 exact-HEAD VM PASS로 표시하지 않는다**. 이 동시성 수정에 필요한 두 실행 순서는 WSS fixture에서 제어해 검증한다. 실제 Browser/SaaS C2·Live·운영 SFTP는 후속 범위다.

[감사](pre-merge-audit.md), [독립 리뷰](independent-review.md), [실행 요약](validation-summary.json), [전체 source 목록/해시](source-manifest.json), [원본 로그](validation-logs.zip), [민감정보 검사](secret-scan.json), [체크섬](artifact-checksums.json).

## 재현

현재 source checkout에서 Go/GCC가 PATH에 있어야 한다. Mock 데이터만 쓰는 새 회귀는 외부 credential 없이 실행한다.

```powershell
go test -mod=readonly -race -count=100 -timeout=5m -run '^TestHandler_TerminalOpen_(OverlappingAttachFailurePreservesReplacement|AttachFailure_CleansUpSession)$' ./internal/connector/wss
```

전체 기본 검증은 version-controlled `../2026-10-14/reproduce.ps1`의 auto/web/contracts stage를 사용한다. Python/Node/pnpm/validator 준비와 정확한 option은 [이전 재현 안내](../2026-10-14/README.md#재현) 및 runner에 있다. 같은 runner를 실행했고 원본 command/시간/exitCode/source snapshot을 archive에 담았다. 개인 미추적 helper는 필수 재현 의존성이 아니다. 새 negative control은 d509aac의 runtime에 현재 `handler_terminal_owner_test.go`만 추가한 실행이며 해당 git blob을 manifest에서 고정한다.

CI는 push 이후 현재 PR head에서 확인하고 PR 리뷰 thread에 결과를 남긴다. 이 archive는 로컬 실행만 기록한다. main merge/auto-merge는 하지 않는다.
