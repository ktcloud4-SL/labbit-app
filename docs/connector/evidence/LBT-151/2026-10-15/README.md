# LBT-151 latest-main 재검증 — 2026-10-15

`origin/main` **c628031e9fb2b511f941504a29c91e1c59dbd734**를 fetch하고 기존
LBT-151 commit8971ad5를 충돌 없이 rebase했다. 검증 HEAD는
**62722d259a004e2a04d5f26a1af83e177ebc0e35**다. 이 폴더 추가는 Evidence-only다.
최종 commit에서 동일 Go/웹/계약 tree를 다시 확인한다.

이 기록은 [10월14일 실제 KT 검증](../2026-10-14/README.md)을 보존하면서
그 이후 main 변경의 영향을 검증한다. 오늘 실제 Cloud/VM 시험을 다시 실행하거나
새 자원을 생성하지 않았다. 최신 audit와 독립 검토는 제안 PR 범위에서 통과했다.
판정·범위·한계는 [AUDIT.md](AUDIT.md), [독립 보고서](independent-review.md)에 있다.
push/PR/merge/배포 또는 Jira 상태 변경은 이번 요청에 포함하지 않는다.

## 결과

| 검증 | 10월15일 결과 |
| --- | --- |
| 전체 Go | fmt472파일, vet/build PASS; unit3640 PASS events,15named SKIP,0FAIL,41PASS packages |
| Connector race | 7packages,708PASS/13SKIP |
| Terminal/PTY/Preview 반복 | race20회,420PASS |
| File/Workspace/parser race | 5packages,74PASS/1SKIP |
| 두 acceptance/bootstrap fixture | race20회,2500PASS |
| File owner/unknown Save 반복 | race20회,320PASS |
| 새 main Backend 기본 경로 | app/connector race338PASS; Session pin 교체 거절 race20회20PASS |
| 웹 | typecheck/lint/build PASS, Vitest14files188tests PASS, tracked npm lock 불변 |
| 계약 | 현재 Actions OpenAPI/JSON Schema/Runtime/Migration번호 검사, Reset schema11tests PASS |
| Linux tagged Backend Harness | GOOS=linux/amd64 CGO0 vet/build 및 integration-tag test **컴파일** PASS |
| Linux tagged Harness 실행·PostgreSQL integration | **NOT RUN**: Windows host, WSL distro/Docker/PostgreSQL/DSN 미구비. 새 POSIX gate/DB 동작의 실행 PASS로 해석하지 않음 |

PASS events에는 parent/subtest가 포함된다. 기본 no-test package11개는 named SKIP15개와
별도다. SKIP은 cloud opt-in11개와 Windows에서 불가능한 POSIX/filesystem4개다.
정확한 command·시간·exit·counts·stability는 [validation-summary.json](validation-summary.json),
이름·조건은 [default-suite-skips.json](default-suite-skips.json), 미실행 경계는
[not-executed.json](not-executed.json)에 있다. 재검증 stdout/stderr/metadata98개는
[validation-logs.zip](validation-logs.zip)에 있으며 original/published SHA 및 IPv4 제거는
[raw-log-manifest.json](raw-log-manifest.json)에 있다.

## 실제 KT Evidence를 유지하는 근거

main 추가2PR은 LBT-53 Mapping 후보 UI와 LBT-167 Backend Router Harness다.
21파일 중 Go는 기존3수정+7추가, 전체 source manifest는467→474파일이다.
Connector/Provider/VM access/PTY/SFTP/File, contracts/runtime/db, go.mod/sum과
LBT-151 도구의 LF source는 실제 실행 때와 같다. 원래 LBT-151 patch 및 원래
Evidence의 Git diff는0이며 range-diff가 동일 patch임을 확인했다.
[integration-impact.json](integration-impact.json)에 전체 main 변경과 source 차이가 있다.

일반 새 빌드의 **production Connector와 SFTP helper2개는 실제 KT 실행 바이너리와
byte SHA256이 동일**하다. acceptance harness와 bootstrap2개는 rebase checkout의
CRLF 때문에 일반 빌드 hash가 다르다. LBT-151 변경 Go21개의 CRLF만 LF로 바꾸는
빌드 overlay로 두 개 모두 원래 실제 실행 바이너리 SHA를 정확히 재현했다.
tracked 파일은 고치지 않았다. 일반4개 모두 동일하다고 주장하지 않는다.
[binary-comparison.json](binary-comparison.json)과 [compare_builds.py](compare_builds.py)가
원본·새 빌드·overlay의 차이를 구분한다. 독립 reviewer도 실제 파일 hash를 대조했다.

따라서 KT 실제 증거는 **10월14일 실행**의 것으로 유지하고, 이번 main이 그 실행
경로에 의미 변경을 주지 않았다는 근거를 연결한다. production Connector 실제 KT
회귀를 오늘 전체 Backend/Browser 경로에서 다시 실행했다고 표시하지 않는다.
원래 6attempt/48owned exact IDs 부재와 7종 baseline 복원은 그 날짜의 관측이다.
오늘 residual 재조회는 하지 않았고 새 Cloud 자원도 만들지 않았다.

## 재현

Go1.27.1, Windows/amd64 GCC race toolchain, Node 및 pnpm과 pinned Python validator를
준비한다. 경로는 환경에 맞게 바꾸며 자격증명은 필요하지 않다. 각 OUT은 새 폴더다.

```powershell
$env:LABBIT_EVIDENCE_REPO = (Get-Location).Path
$env:LABBIT_EVIDENCE_OUT = '<fresh-run-root>/auto'
python docs/connector/evidence/LBT-151/2026-10-14/validate.py auto
# OUT을 각각 web/contracts/build로 바꿔 같은 validator의 해당 mode 실행
# web: LABBIT_EVIDENCE_NODE_BIN, LABBIT_EVIDENCE_PNPM
# contracts: LABBIT_EVIDENCE_VALIDATORS (PyYAML6.0.3/jsonschema4.26.0/openapi-spec-validator0.9.0)
$env:LABBIT_EVIDENCE_OUT = '<fresh-run-root>/latest-main'
python docs/connector/evidence/LBT-151/2026-10-15/validate_main.py
$env:LABBIT_EVIDENCE_OUT = '<fresh-run-root>/binary-comparison'
python docs/connector/evidence/LBT-151/2026-10-15/compare_builds.py
```

`compare_builds.py`는 sibling `build/binary-manifest.json`과 10월14일 manifest를 읽는다.
`collect_evidence.py`는 RUN_ROOT의 결과와 별도로 직접 조회한 latest-ssot.json을 검증해
이 폴더에 공개 bundle을 만든다. 이미 보관한 bundle을 재실행으로 덮어쓰지 말고
별도 checkout/fresh Evidence 폴더에서 재현한다. 파일별 SHA는 artifact-manifest.json.
전체 product CI는 Linux/PostgreSQL16에서 `.github/workflows/go.yml`을 별도 실행해야 한다.
오늘 기록은 원격 CI 통과를 의미하지 않는다.

## 알려진 한계와 인계

[원래 C2-HANDOFF](../2026-10-14/C2-HANDOFF.md)의 실제 KT 접속 조건·Workspace helper·
Terminal/File 계약과 잔여 범위를 그대로 적용한다. 실제 Browser/Backend/DB/Relay/Live
전체→KT 통합은 LBT-22다. synthetic trusted TLS SaaS, application이 받은 Save 성공을
버리는 주입, 단일 File4001, latest helper 한 개 SSH tuple 관측과 socket census 한계를
유지한다. full credential revoke+401, blackhole/부분 Save, 전체 goroutine census,
강제kill 신규 주입, 운영부하는 수행하지 않았다. 실패 attempt2/4의 SSH 원인은 미확인이다.
10월14일 성공이 그 실패를 없애거나 신뢰성·C2 전체 완료를 증명하지 않는다.
상속 LBT-167 Evidence의 과거 다른 base Linux unread Data replacement 실패도 이번
Windows PASS로 해결됐다고 표시하지 않는다. 현재 revision에서 재현된 결함으로 단정하지 않는다.
