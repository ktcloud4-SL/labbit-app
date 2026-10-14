# LBT-151 KT Cloud 최종 회귀

이 폴더는 LBT-151의 실행 기록과 pre-merge audit를 보관한다. Jira의 최종
Browser → SaaS → KT VM 검증은 LBT-22이며, 여기서는 synthetic SaaS peer와
production Connector를 실제 KT Cloud API·VM·SSH·PTY·SFTP에 연결한다.

2026-10-14 최종 두 세대 회귀는 PASS다. 앞선 실패까지 포함한 owned IDs48개가
모두 absent이고 원래 seven-kind baseline을 복원했다. 자동 검사와 독립 audit도
통과했다. [Audit](AUDIT.md), [독립 검토](independent-review.md),
[전체 단계](actual-final.log), [C2 인계](C2-HANDOFF.md)를 확인한다.

재현에 필요한 소스는 `tools/ktcloud-d1-acceptance/`,
`tools/ktcloud-op02-bootstrap/`와 이 폴더의 `validate.py`다.
[KT fixture 준비·복구·정리](../../../../../tools/ktcloud-op02-bootstrap/README.md)를
따른다. API 자격증명이 유효하면 웹 콘솔 로그인은 필요하지 않다. 개인 로컬
helper나 과거 Evidence의 `.go.txt`를 실행 코드로 가져올 필요가 없다.

Git, Go 1.27.1, Windows race용 GCC, Python 3, Node/pnpm이 필요하다. Contracts는
PyYAML 6.0.3, jsonschema 4.26.0, openapi-spec-validator 0.9.0으로 실행했다.
실제 KT 입력은 검증된 `clouds.yaml`, project 권한, 미사용 Lab/Management `/24`,
Ubuntu image/flavor, 전용 keypair와 protected known-hosts다. 비밀번호·토큰·키
내용을 명령행이나 이 폴더에 넣지 않는다.

```powershell
$env:LABBIT_EVIDENCE_REPO = (Get-Location).Path
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt151-auto-' + [guid]::NewGuid())
python docs/connector/evidence/LBT-151/2026-10-14/validate.py auto
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt151-web-' + [guid]::NewGuid())
# 필요하면 LABBIT_EVIDENCE_NODE_BIN / LABBIT_EVIDENCE_PNPM을 지정한다.
python docs/connector/evidence/LBT-151/2026-10-14/validate.py web
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt151-contracts-' + [guid]::NewGuid())
# 필요하면 LABBIT_EVIDENCE_VALIDATORS에 pinned Python module 경로를 지정한다.
python docs/connector/evidence/LBT-151/2026-10-14/validate.py contracts
```

`auto`는 repository 기본 Go 검증과 관련 race/20회 회귀를 실행한다. `web`은
tracked npm lock에서 일시 pnpm lock을 만들고 frozen install/typecheck/lint/test/build
후 원본 lock 불변을 확인한다. `contracts`는 OpenAPI 및 현재 workflow의 JSON
Schema/Runtime/migration 검사와 Reset schema 11개 검사를 그대로 실행한다.
`build`는 Linux production Connector, acceptance harness, File helper와 Windows
bootstrap을 만들고 target/flags/SHA256을 남긴다. 각 mode는 fresh output directory를
사용하며, 실행 전후 모든 compiled Go source의 LF hash와 HEAD를 비교한다.
소스가 바뀐 실행은 이전 검증 기록으로 보존하고 최신 소스의 결과와 구분한다.

PTY/File bytes는 제한된 메모리에서만 비교한다. SSH 공유 검증은 두 PTY와 SFTP의
실제 `$SSH_CONNECTION` tuple을 비교하며 tuple 값은 Evidence에 남기지 않는다.
idle은 그 연결의 실제 TCP tuple이 60초 기본값 이후 사라지는지 확인한다.
File cancel/disconnect/단일 Data 4001/timeout은 실제 delay helper의 존재와 종료를
관측한다. 동시 두 SFTP는 실제 helper count2→1→0, 한 channel 종료 뒤 다른
File Data와 PTY 유지를 확인한다. 공유 probe 파일은 최신 helper 한 개의 SSH
tuple이며 두 helper 각각의 tuple을 독립 기록한 것은 아니다.
이는 Connector 전체 goroutine census나 전체 credential revoke 검증을
뜻하지 않는다. Save 결과 유실 주입은 synthetic SaaS application boundary에서
이미 받은 SUCCEEDED를 버리고, 실제 저장 상태·old-ID no-redial·fresh Read를 확인한다.
transport-level partial write/결과 불명 E2E로 확대하지 않는다.

Reset/Cleanup 전 synthetic SaaS가 계약대로 active Terminal/File 작업을 종료하고
원격 helper 종료를 확인한다. 실제 Reset의 새 VM/구 세대 리소스 부재, ACTIVE g2
server에 g1 요청 거절, Cleanup 후 모든 generation ID 부재를 별도로 확인한다.
Browser credential/sessionStorage, 실제 Backend authority/DB/Relay/Live fan-out,
운영 배포·부하·네트워크 blackhole은 LBT-22/해당 owning 작업의 후속 범위다.

최초 actual 실행의 SSH identity parser 실패와 Cleanup UNKNOWN은 삭제하지 않는다.
원본은 protected local directory에 보존하고, 공개 가능한 원본/정제된 metadata의
SHA256·변환 내역을 함께 남긴다. ANSI/fragmentation 파서 수정은 실제 TLS Data frame
회귀와 fresh KT Lab 재실행으로 검증한다. 리소스 cleanup UNKNOWN은 blind retry하지
않고 actual Reconcile로 잔여 ID를 확인한 뒤 정리한다.

attempt2는 production SSH Ready3m timeout, attempt3는 잘못된 fixture duplicate ACK
기대로 실패했다. 종료된 FileRequestID에 새 ACK를 요구하지 않고 fresh-ID Read와
5s no-redial 관측으로 수정했다. production Handler+File Manager trusted TLS driver
회귀의 red/green 로그와 모든 실패 원본을 최종 bundle에 포함한다.

attempt4는 추가 fixture SSH 연결 실패(정확한 원인 미확인), attempt5는 빈 Cleanup
성공 후 접근이 종료된 g2 Terminal을 다시 열려던 fixture 순서로 실패했다. 활성
작업 종료를 첫 Cleanup 앞으로 옮겼다. 두 실패의 전체 원본과 실제 자원 정리도
보존한다. 후속 성공이 이전 실패의 원인이나 신뢰성을 증명하지 않는다.

정리와 최종 inventory 확인 후 protected fixture directories를 순서대로 지정해
공개 Evidence를 만들 수 있다. 실제 비밀 입력과 binary는 exporter에 포함하지 않는다.

```powershell
python docs/connector/evidence/LBT-151/2026-10-14/export_evidence.py `
  $env:LABBIT_EVIDENCE_REPO $validationRoot $attempt1 $attempt2 $attempt3 $attempt4 $attempt5 $finalAttempt
```

이 실행의 기본 Go 검사 폴더명은 `auto-run6-final`이다. source manifest와 최종 actual
build의 전체 LF source hashes가 일치해야 export를 허용한다. 원본 로그의 SHA256과
공개 로그의 SHA256/IPv4 제거 개수를 함께 보존하며 typed ledger는 허용된 lifecycle
필드만 추출한다. `AUDIT.md`와 `C2-HANDOFF.md`에 검토 범위와 인계 조건을 연결한다.
