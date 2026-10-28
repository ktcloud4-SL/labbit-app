# Connector 연결 복구 검증 기록

2026년 10월 28일의 Connector 수정과 운영 설정 보완을 검증했다. 운영 실측 제품 소스는 당시 base `aaa99d80c434910357007882bdbc614de8f1109d`와 [source-manifest.json](source-manifest.json)의 21개 파일로 고정한다. 최종 PR은 작업 중 병합된 PR163·166을 포함한 main `019ff7ece1dccec551a9247d954020d9dc958855`에 통합했고 [integration-source-manifest.json](integration-source-manifest.json)으로 별도 식별한다. 최종 통합본의 실제 VM 시험·운영 배포는 실행하지 않았다. canonical LF 파일 해시 집합의 SHA256은 `21f07ea74db5e436aa644210aaa4bb1814a7bd11459b63abaca1e25c6eda8435`다. 최종 PR commit의 제품 해시는 운영 실측 해시와 다르다. 과거 실제 Browser PASS를 새 통합 코드의 실제 VM PASS로 옮기지 않는다.

[운영 변경 이력](../../../runtime-recovery-20261028.md), [pre-merge audit](PRE_MERGE_AUDIT.md), [자동 검증 결과](validation.json), [실제 Browser와 Cleanup 검증](browser-cleanup.json), [운영 설정 비교](operations.json)를 함께 본다. 공유 파일은 원본 결과에서 안전한 metadata와 관찰 결과를 선별했다. 원본 로그·보호된 env·credential·SSH 키·PTY transcript·파일 경로와 본문·Provider raw payload는 포함하지 않는다. 원본 파일의 SHA256과 실행 시점을 provenance에 기록하되 저장소 밖의 개인 helper를 재현 필수 조건으로 삼지 않는다.

## 자동 검증 재현

Go·Node 버전은 저장소의 `go.mod`, `web/package.json`, lockfile과 CI 설정을 따른다. POSIX 권한·shell fixture가 있는 전체 기본 검증은 Linux에서 실행한다. 일반 테스트는 실제 Cloud gate를 켜지 않는다. 실제 자격증명을 주입하지 않고 다음을 실행할 수 있다.

```sh
make setup
make test
go test -race -count=1 ./internal/connector/vmaccess ./internal/connector/app ./internal/connector/terminal ./internal/connector/wss
go test -count=1 ./internal/connector/provider/openstack -run 'TestKTCloudCIDR' -v
```

계약 검증은 `.github/workflows/contracts.yml`의 OpenAPI·Draft202012 schema·local ref·Runtime YAML·migration prefix 검증과 동일하다. 실제 PostgreSQL은 `make go-integration-test`의 별도 환경이 필요하다. PR CI 결과와 로컬 unit을 실제 VM 검증으로 취급하지 않는다.

| 경계 | 현재 저장소의 재현 테스트 | 테스트 환경 |
| --- | --- | --- |
| 첫 요청 취소와 생존 대기자 | `TestCancelFirstDialCallerKeepsSurvivingWaiter`, `TestLastCancelReturnsWhileHostKeyCheckBlocked`, `TestLastCallerDeadlineCauseReachesFinalDialDiagnostic` | loopback SSH 및 통제된 callback. 테스트 소스는 `internal/connector/vmaccess/shared_dial_test.go`. |
| handshake 단계·취소·안전한 로그 | `TestHandshakeDiagnosticsSeparateVersionAndKeyExchange`, `TestHandshakeDiagnosticsKeepRequestCancelSeparate`, `TestIdentificationObserverDoesNotConsumeOrRetainPeerText`, `TestVMAccessFailureLogValidatesTargetAndKeepsSafeCheckpoint` | loopback socket·unit. 실제 peer text·개인키·파일 본문 저장 여부도 확인. |
| Provider 처리 중 접속 | `TestListenOpensReadyTerminalWhileAnotherVMOperationIsBlocked` | mock Provider와 echo PTY, in-process WSS. |
| reconnect·종료 | 최신 main의 `TestReconnectPreservesAdmittedWaitingItem`, `TestOperationAdmissionBoundAndShutdownWaiters`, `TestActualConnectorAdmitsFourWorkerItemsWhileProviderBlocked` 및 `TestSessionCancelPreservesAcceptedOperationButStopsBufferedCommands`, `TestSupervisorShutdownCancelsAcceptedOperationContext` | in-process WSS·context. accepted 대기 작업·미수락 buffered frame과 read/write 수명을 구분. 운영 FIFO64 특화 회귀 두 개는 정책이 대체되어 제거하고 최신 main 회귀를 보존했다. |
| CIDR 반환·보존·재사용 | `TestKTCloudCIDRCleanup*`, `TestKTCloudCIDRPublicLifecycleReturnsCompleteTopology`, 기존 `TestKTCloudCIDRPool*` | loopback Provider HTTP·SSH readiness fake·임시 원장. public Provision→5개 refs→Cleanup→같은 pair의 새 owner Provision 경로 포함. 실제 OpenStack 아님. |

과거 독립 리뷰의 overlay red/green은 공유 dial 취소 결함을 별도로 재현했다. 위 회귀가 최종 저장소에 포함되어 있으므로 개인 로컬 overlay 경로는 필요 없다. Linux Provider 전체 재사용 결과는 같은 Provider·계약 파일 해시에서 180 top-level PASS, 실제 OpenStack opt-in 14개 skip이다. Installer는 Linux 격리 fixture 12개 PASS를 재사용한다. PR의 첫 commit `4f0c3bc`에서도 Linux 전체 Go·PostgreSQL 통합·Web 443개·Contracts 검증을 통과했다. [CI 기록](ci.json)은 이 실행과 보안 검사의 오탐을 구분하며, 최종 HEAD의 전체 결과는 PR 본문과 GitHub Checks를 기준으로 확인한다.

## 실제 Browser와 VM 검증

실제 SaaS HTTP·Control WSS·Data WSS·KT VM·guest helper를 사용하는 native Browser에서 두 차례 새 강사·학생 VM을 검증했다. 첫 번째는 Terminal과 Cleanup 종료 알림, 두 번째는 helper 설정 보완 후 Terminal과 파일 기능을 확인했다. 시험마다 정상 UI Provision·Cleanup을 사용했고 수동 SSH 선접속·refresh·접속 retry로 성공을 만들지 않았다.

최종 시험의 첫 attach는 강사 0.771810초, 학생 0.721365초였다. 각 역할에서 최초 Tree가 빈 디렉터리로 성공했고, Terminal에서 준비한 통제된 fixture를 별도 정상 Workspace 탭에서 조회·읽기·저장했다. 원래 Terminal에서 저장된 결과와 명령 수신을 확인해 File 작업이 기존 PTY를 끊지 않음을 확인했다. 자동 Tree가 먼저 실행되므로 이는 첫 웹터미널 성공이며 최초 SSH transport의 시작 주체를 단정하지 않는다.

직전 시험에서는 READY 학생의 Terminal을 다른 VM Provision 중 열었고, 강사 Terminal을 연결한 채 공식 Cleanup하여 UI `LAB_CLEANUP` 종료 알림과 DB ENDED를 확인했다. 최종 시험은 두 세션 명시 종료 후 Cleanup했다. 두 시험의 종료 경로는 서로 구분한다.

최종 시험은 06:10:17–06:21:55 UTC, 약 698초로 승인된 15분 이내에 끝났다. 검증용 10개 자원이 DB DELETED이고 exact ID 관찰 및 기존 Cloud baseline 비교가 일치했다. 원래 별도 서버 3개·volume 3개·Tier 7개·policy 6개는 보존됐으며 active CIDR 예약은 비고 4쌍이 사용 가능했다. metadata 검증기는 Cloud mutation이나 DB write를 수행하지 않았다. 생성·삭제는 정상 제품 Operation 경로였다.

## 실패 기록과 검증 한계

현재 제품 소스의 fresh Windows Go vet·unit·build와 관련 race는 PASS다. fresh Web typecheck·lint·build는 PASS, test는 441/443으로 기존 installer env fixture 두 건의 `rmSync` EPERM이 실패했다. 이전 Windows 실행의 442/443 실패도 같은 임시 디렉터리 정리 경계였다. Web·workflow 입력은 base와 동일하며 Linux PR CI에서는 443/443 PASS였다. Windows 실패를 전체 로컬 PASS로 바꾸어 표시하지 않는다.

첫 PR commit의 Gitleaks는 manifest의 `vmaccess/access.go` 파일 SHA256을 generic API key로 오인했다. 실제 파일에서 같은 해시를 다시 계산해 확인했다. 공개 manifest의 파일 목록을 path·sha256 배열로 표현해 directory scan에서도 credential key 문맥으로 오인되지 않도록 했으며, 21개 원래 경로·해시와 changeSHA256은 보존했다. 이미 게시된 첫 commit을 검사하는 history scan에는 `.gitleaksignore`의 해당 commit·file·rule·line fingerprint 한 건만 예외로 둔다. 파일 전체·규칙 전체를 제외하지 않는다.

같은 v8.28.0의 로컬 history 검사는 0건으로 통과했다. tracked tree 전체 directory 검사는 기존 파일 48개의 807건 탐지로 실패했다. 이 48개 파일은 base와 canonical 내용이 전부 같고 이번 신규·변경 파일의 탐지는 0건이었다. 기존 807건을 모두 오탐이나 안전한 값으로 판정한 것은 아니며, 저장소 전체 directory 검사를 통과했다고 주장하지 않는다. 이 PR은 해당 기존 범위를 숨기는 전역 예외나 workflow 변경을 추가하지 않는다.

이전 준비 과정에서는 Windows Provider POSIX permission, Installer Git Bash 시나리오 7, gofmt의 CRLF·인자 길이, public lifecycle fixture flavor 불일치가 실패했다. Linux permission·installer 검증과 canonical LF 비교, 올바른 immutable fixture flavor로 보완한 뒤 최종 소스를 고정했다. 초기 metadata 검증기에는 기준 SHA·UUID 표현 오류가 있었고 read-only 검증기만 수정해 재검증했다. Cleanup 직후 한 snapshot의 connection count assertion도 실패했으며 후속 관찰과 동일 검증기 재실행에서 정상화됐다. 원인을 확인하지 않은 snapshot 실패를 제품 결함 해결로 설명하지 않는다.

과거 약 10초 장애의 정확한 SSH 진행 단계와 취소 주체는 당시 로그로 소급 확정할 수 없다. 현재의 진단·취소 결함 회귀·첫 웹접속 성공이 과거 단일 원인의 증명은 아니다. 과거 삭제된 VM의 파일 오류 세부 단계도 확정하지 않았다. 누락된 자동 bootstrap 설정과 새 VM의 정상 파일 기능을 각각 확인했다.

power-loss·실제 fsync 장애, 강제 프로세스 kill 중 VM mutation, 실제 네트워크 장애·UNKNOWN·lease 만료의 전체 장애 매트릭스, 대규모 부하, 모든 Preview·Live 여정은 이번 실환경 시험에 포함하지 않았다. crash 원장 중간 상태·UNKNOWN·stale generation·공유 채널 독립성은 자동 fixture로 검증했다. 실제 VM 시험은 성공 경로와 위 명시한 종료·동시 Provision 경계에 한정한다.
