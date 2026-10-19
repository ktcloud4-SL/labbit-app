# LBT-175 · KT Cloud Preview transport 검증

실행일: 2026-10-19 KST. 이 기록은 Mock Gateway와 **실제 production Connector
프로세스, KT Cloud API·Workspace VM·SSH direct-tcpip·HTTP 앱**을 연결하는 검증이다.
실제 Backend/SaaS Gateway/Browser 공동 E2E는 LBT-25 C3의 별도 Gate다.

실제 KT 두 세대 HTTP·guard·Reset/Cleanup·종료 검증 PASS. 소유 자원18개 삭제,
잔여0,7종 inventory baseline 보존. Linux Go3,970 PASS/14skip, Web217 PASS,
관련 race·fmt/vet/build·contract 검증 PASS다. 초기 실패와 skip은 AUDIT에 구분했다.

기준 main·HEAD는 `89a698dac50a8705ed305a5eccf94ee9505b033f`, 로컬 브랜치는
`codex/feat/LBT-175-ktcloud-preview-validation`이다. 실행 소스는 미커밋 변경을
포함하며 `tested-source.json`과 `compiled-source.json`이 식별한다.
push·PR·신규 원격 CI는 수행하지 않는다. 따라서 Jira 전체 마감 조건인
PR 리뷰·승인·병합과 신규 CI는 후속 단계로 남는다.

## 재현 준비

- Windows operator: Go 1.27.1, Git, Python 3, Go race용 GCC.
- 접근 제한된 새 fixture 디렉터리, 보호된 `clouds.yaml`의 명시적 `ktcloud-d1`
  alias, 유효한 KT D1 인증·CA 신뢰. Credential/개인키를 Git에 넣지 않는다.
- 기존 [bootstrap runbook](../../../../../tools/ktcloud-op02-bootstrap/README.md)의
  빈 compute/volume baseline, 사용하지 않는 두 fixture CIDR, Ubuntu 24.04
  image `os-ubuntu24.04-image-20260929`, flavor `2x4.itl`을 확인한다.
  생성 대상은 bootstrap 8개와 두 Lab 세대의 총 10개 소유 리소스다.
- 정상 TLS 검증과 등록된 SSH host pin을 사용한다. 앱은 Workspace VM의
  loopback 3000에만 바인딩한다. 공개 앱 포트·추가 앱 방화벽은 만들지 않는다.

저장소 루트에서 bootstrap runbook의 build·환경 준비를 수행하고, deploy 전에
아래 두 설정을 추가한다. cloud mutation 단계는 exit code를 확인하면서 하나씩
진행하고 각 attempt의 시작·끝·exit code와 보호된 로그 위치를 보존한다.

```powershell
$env:LBT175_BOOTSTRAP_PREVIEW_TRANSPORT = '1'
$env:LBT173_BOOTSTRAP_PREVIEW_SMOKE = '0'
# 나머지 명시적 cloud/ledger/key/build 설정은 bootstrap runbook 참조.
# phase 순서: inventory, create, access, ssh, deploy, control, collect, residual,
#            cleanup, residual, final
$env:LBT145_BOOTSTRAP_PHASE = 'control'
& (Join-Path $fixtureDir 'bootstrap.exe')
if ($LASTEXITCODE -ne 0) { throw 'Preserve journal; observe before further mutation' }
```

`control`은 fresh journal에서 한 번만 실행한다. 실패하거나 결과가 불명확하면
동일 journal에 control을 재실행하지 않는다. `collect` 후
`recover-observe → recover-cleanup`으로 정확한 소유 ID를 조회·정리한다.
복구 성공을 acceptance 성공으로 바꾸지 않는다. 이름·tag로 자원을 입양하지 않는다.
첫 SSH 접근만 실패한 경우 create/access를 반복하지 않고, 같은 VM의 읽기 전용
`ssh` 확인을 재시도할 수 있다. 실패 attempt도 보존한다.

bootstrap을 삭제하기 **전에** 로그를 collect하고 필요한 Linux 검사를 끝낸다.
마지막 residual은 모든 기록된 Lab ID의 부재, final은 일곱 종류 전체 inventory가
처음과 정확히 같은 ID 집합임을 요구한다. 공유 Tier는 보존한다.

## 실행 경로와 판정

Control/Preview Data peer와 test CA는 synthetic이다. 생성한 Credential은
production Connector 자식 프로세스에 정상 설정으로 전달한다. Data peer는
Bearer·단일 subprotocol·query 부재·전체 ATTACH correlation을 검증한다.
HTTP POST는 세 binary frame으로 분할해 보내며 정확한 상태와 전체 응답 body를
메모리에서 대조한다. SSH exec는 앱 설치와 별도 TCP 관측용이며 HTTP payload를
우회 전달하지 않는다. HTTP·헤더·쿠키·Credential 원문을 Evidence에 내보내지 않는다.

각 세대에서 동일 logical session의 순차 3회, duplicate OPEN의 정확한 success와
기존 tunnel HTTP, 서로 다른 session의 CLOSE 격리, 1000/1011 Gateway 종료 후
재시도, 22 거절, 3001 앱 미실행, 잘못된 VM key/server를 검사한다.
Reset 전에는 활성 tunnel을 유지한 상태로 세대를 교체하고 기존 WSS 정상 종료와
이전 세대 UNAVAILABLE을 확인한다. 빈 Cleanup도 runtime generation을 retire하므로
첫 Cleanup **전** 활성 tunnel을 열고, 이후 TCP=0을 VM에서 확인한 다음 물리 자원을
정리한다. 최종 Data drain과 Control content secrecy, 자식 프로세스 graceful exit도
별도 판정한다. `PREVIEW_ENDED` 메시지는 계약에 없으므로 만들지 않는다.

실제 결과, 자동 테스트·skip·초기 실패, 전체 diff와 SSOT 추적성 및 독립 리뷰는
[AUDIT.md](AUDIT.md)에 기록한다. 기계 판독 metadata는 원본 journal/inventory의
ID를 SHA256으로 바꾸며, 원본은 접근 제한된 ignored `.local`에만 보존한다.

## 자동 검증 재현

```sh
go vet -mod=readonly ./...
go build -mod=readonly ./...
# POSIX filesystem semantics가 있는 Linux에서 저장소 전체 실행
go test -mod=readonly -json -count=1 -timeout=5m ./...
go test -mod=readonly -race -json -count=1 -timeout=5m ./internal/connector/preview ./internal/connector/wss ./internal/connector/app ./internal/connector/workspacefs ./internal/connector/workspacefile ./internal/connector/filetransport ./internal/connector/vmaccess ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap
go test -mod=readonly -race -json -count=20 -timeout=5m ./tools/ktcloud-d1-acceptance ./tools/ktcloud-op02-bootstrap
```

Go fmt는 tracked/untracked 전체 Go 파일을 LF 정규화 후 gofmt 출력과 비교한다.
Web·contract·cross-build는 기존
`docs/connector/evidence/LBT-151/2026-10-14/validate.py`의 `web`, `contracts`,
`build` entrypoint를 사용한다. `LABBIT_EVIDENCE_REPO/OUT`을 새 출력 디렉터리로
지정하고 Web은 `LABBIT_EVIDENCE_PNPM/NODE_BIN`, contract는 필요한 경우
`LABBIT_EVIDENCE_VALIDATORS`를 명시한다. pnpm adapter는 tracked npm lock을
import하고 frozen install 후 생성한 pnpm lock을 제거하며 원본 lock hash를 확인한다.

Linux 전체 검사는 현재 checkout의 tracked+untracked 소스(과거 Evidence 제외)를
새 비공개 디렉터리에 복사하여 파일 SHA256을 모두 대조한 뒤 실행한다. 이 실행은
기존 bootstrap VM을 재사용한다. Go toolchain은 공식 module proxy+sumdb로 받은
`golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64`이며 sum과 파일 목록은
`linux-source.metadata.json`에 남긴다. 새 SSH 접속도 기존 host pin을 엄격히 검증한다.
production Connector에 주입한 cloud Credential은 일반 Go 테스트 환경에 주입하지 않는다.

```sh
python docs/connector/evidence/LBT-175/2026-10-19/verify.py source OUTPUT pre-live
python docs/connector/evidence/LBT-175/2026-10-19/verify.py summarize RAW_GO_JSON OUTPUT_JSON
python docs/connector/evidence/LBT-175/2026-10-19/verify.py export PROTECTED_FIXTURE ALL_PHASE_ATTEMPTS_JSONL OUTPUT
```

## C3 인계와 남은 Gate

LBT-25에서는 실제 Backend PreviewSession 생성·인가·generation 경계, SaaS Gateway,
Browser, 운영 DNS/TLS/Ingress를 이 transport 위에 연결한다. 여기의 synthetic peer를
실제 SaaS PASS로 취급하지 않는다. 실제 KT fixture는 Internet OFF, 단일 Workspace VM,
Ubuntu 24.04의 두 세대다. 다른 이미지·멀티 VM·장시간 부하·네트워크 단절의 모든 조합은
이 실행이 보장하지 않는다. 일반 Control EOF·결과 전송 실패·4001/4002·취소 race는
현재 소스의 자동 회귀 범위이며 KT에서 해당 fault를 주입한 결과로 표시하지 않는다.
DB integration·실제 PostgreSQL은 이 tools-only 변경에서 실행하지 않는다.
