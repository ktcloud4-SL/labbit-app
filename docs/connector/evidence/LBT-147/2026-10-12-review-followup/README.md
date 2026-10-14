# LBT-147 리뷰 후속 수정·검증 Evidence

2026-10-12 KST. CODEOWNER 리뷰의 **R1 공유 SSH 취소 격리, R2 오류 분류, R3 종료 순서**를 수정했다. Control 변경은 `SERVICE_RESTARTING` 알림 전에 sender가 닫히는 경계와 종료 중 신규 PTY 차단에 필요한 lifecycle 변경이다. wire/enum/schema를 변경하지 않았다.

검토는 **LBT-147 전체54파일**에 대해 최신 main/SSOT·요구사항 추적·실패/보안/수명주기·원본 Evidence 및 구현과 분리된 reviewer를 포함한다. 실행 소스는 `ed7843e25be99f414bfb0087d2f07b8121fe08a0` + 미커밋11파일이다. [source-manifest.json](source-manifest.json)의 byte SHA256/Git blob 및 ZIP의 source11 snapshot으로 식별한다. 이전 Evidence는 그대로 보존하고 과거 source의 PASS와 구분한다.

- Go 전체2856 PASS/7 SKIP, 관련5패키지 race392 PASS/6 SKIP, 새 회귀 race20회600 PASS. 숫자는 subtest 포함이며 skip 사유는 [실행 요약](validation-summary.json).
- Web typecheck/lint/build 및13파일163 tests PASS. OpenAPI·8JSONSchema·runtime/migration metadata·Reset schema11 PASS.
- **실제 로컬 OpenStack**: pinned/authenticated/shared SSH, 실제 PTY IO/resize·SFTP INIT/VERSION/REALPATH·idle, gen2 Reset/stale1 rejection PASS. 정상14+app16 tracked resources의 exact-ID ABSENT, 보조 SG/keypair ABSENT, 마지막 matching test residual0.
- 새 R1/R2/R3 fault 경계는 authenticated SSH/local HTTP/Mock Control·Data WSS fixture 및 독립 reviewer의 별도 fixture에서 PASS. 정상 실제 VM PASS를 이 fault들이 실제 배포 환경에서 검증됐다는 뜻으로 확대하지 않는다.

[전체 감사·AC 추적·미검증 범위](AUDIT.md), [54파일별 검토](file-review.md), [독립 리뷰](independent-review.md), [원본 로그·fixture·snapshot](audit-logs.zip), [archive entry hashes](archive-manifest.json), [최신 원문 버전](ssot-summary.json).

남은 범위는 KTCloud gen2 Reset/Tier2 Cleanup, 실제 Browser/배포 SaaS CC02·routing/reconnect, production SFTP/File WSS, 신규 fault의 실제 VM/Control 장애 주입, host-key 공격/키 회전/자연 outage/packet loss/hard restart/soak/load, blocked WSS 종료/전달, 별도 cloud4 suites 및 PostgreSQL integration이다. 자세한 담당 후속과 검증 구분은 AUDIT에 적었다. 응답하지 않는 SSH open/setup은 transport당1개로 제한되며 기존 channel은 유지된다. pending이 풀릴 때까지 같은 VM의 새 open/setup이 대기/timeout할 수 있다.

## 재현

Go1.27.1/CGO와 race 지원 C compiler, Node24.19.0, pnpm, Python3.11+ 및 `PyYAML==6.0.3 openapi-spec-validator==0.9.0 jsonschema==4.26.0`가 필요하다. Python validators는 repo 밖 환경에 설치한다. `pnpm install --frozen-lockfile` 등 dependency 준비는 사용자 환경에서 수행한다.

1. 별도 checkout에서 위 HEAD를 준비한다. 미커밋 소스가 없으면 ZIP의 `source/` 11파일을 저장소의 같은 상대 경로에 복사한다. 다른 producer/consumer 변경을 섞지 않는다. 실행 출력은 저장소 밖 새 디렉터리에 둔다.
2. 이 폴더의 `reproduce.py --repo <absolute repository> --verify-only`로 모든 pinned Git blobs/artifacts를 확인한다. Git blob 검증은 Windows checkout CRLF 변환을 허용한다. 실제 실행 byte SHA는 원본 manifest에 별도로 보존된다.
3. 자동 검증: `python reproduce.py --repo <repository> --output <new external directory>`. Go 전체/race/20회 회귀, Web4항목, 현재 workflow와 같은 contract 검사를 실행한다. cloud 실행 없음.
4. 실제 로컬 cloud 재실행은 명시적 opt-in: `python reproduce.py --repo <repository> --output <new external directory> --cloud --cloud-config <clouds.yaml> --private-key <SSH private key> --cloud-name labbit-test`. **실제 VM/네트워크 등 생성·삭제를 수행한다.** Ubuntu/m1.small/sharednet1, Management 접근 CIDR172.16.8.1/32, Lab CIDR198.20.0.0/24인 기존 fixture가 필요하다. 다른 cloud 환경은 동일 acceptance라는 보장이 없다. credential/key는 포함하지 않는다. child process에만 설정을 주입한다. 사전 discovery 실패 시 mutation을 시작하지 않고, mutation 종료 후 finally에서 read-only 잔여 조회를 수행한다. fixture 자체가 소유 exact-ID 정리를 담당한다.
5. 독립 close-ACK fault는 `_fixtures/reproduce-independent-unacked.ps1 -RepoPath <repository> -OutputDirectory <new external directory>`로 재현한다. 필요한 fixture·manifest가 같은 `_fixtures` 폴더에 있다. x/crypto v0.57.0의 immutable module cache를 외부 output에 복사하여 server-side B channel close ACK만 test overlay로 무시한다. production dependency/cache를 변경하지 않으며 cloud mutation 없이 race5회 실행한다. `-PrepareOnly`는 fixture 생성만 한다. byte guard 때문에 현재 LF byte snapshot 그대로 복사한 source가 필요할 수 있다.

raw before 실패, 초기 missing-import build 실패, validator 준비 및 최초 dependency overlay 거절 기록도 ZIP에 보존했다. raw 실행용 `run_validation.py`의 당시 로컬 경로는 역사적 정보다. 재현용 `portable_run.py`는 repo/config/key/도구 경로를 caller 입력으로 받으며 과거 개인 디렉터리에 의존하지 않는다.

재현 guard는 별도 isolated clone/외부 artifact fixture에서21조건(소스14·artifact7)을 검증했다. 정상 source·문서·기존 hidden `_fixtures` 허용, local source/무관 consumer/untracked·ignored Go/missing pin 및 Evidence의 visible Go/go.mod/go.sum 거절을 확인했다. artifact exactset/bytes 검사는 추가·누락·변조 파일을 거절하고 generated ignored Python cache만 제외한다. Go/cloud 호출0. [결과](reproduce-guard-regression.json). source 변경을 감지한 뒤 자동으로 원본을 덮어쓰지 않는다.

secret scan은 로컬 cloud secret 값·실제 개인키 body와 알려진 token/PEM pattern을 archive 내부까지 검사하는 scoped 검사다. unknown secret과 모든 Git history를 증명하는 검사는 아니다. 최종 archive CRC/hash/source identity·재현 syntax/overlay compile/독립 게시 검토는 publication-checks.json과 publication-review.md에 기록한다.

최종 판정: 새 리뷰3건 해결 및 전체 LBT-147 범위의 PR 리뷰 재요청 가능. publication-review.md에 게시 검토·제한을 기록한다. 자동·실환경 실행은 로컬 미커밋 상태에서 수행했고 동일 Git blob을 코드 커밋 `b191ecdbe87f28c2c2e34d28877f5de84b18f082`로 고정했다. 이후 사용자가 최신화·재검증·수정 업로드를 승인했다. Evidence 커밋과 feature branch push/PR 본문 최신화로 이어지며 main push/PR merge는 하지 않는다.

게시 전 보조 도구 findings: root `.go` fixture가 Go package로 인식된 문제는 `_fixtures` 이동으로 해결했고 latest 전체 Go PASS로 확인했다. Python validator 경로는 기존 PYTHONPATH를 보존한다. 기본 재현모드가 inherited cloud opt-in을 상속한 P2는 양쪽 helper에서10플래그를 비활성화해 해결했다. stub5케이스에서 실제Go/cloud호출0으로 default/direct-auto/no-input/cloud선택 경계를 확인했다. 원본 red/green은 ZIP에 보존했다. Ignored Python bytecode/cache는 게시 artifact에서 제외한다.

최종 ignored-Go 예외는 `web/node_modules/.pnpm/` 한정이다. 일반 `web/node_modules/visible` 경로는 Go가 탐색할 수 있음을 독립 fixture로 확인하여 거절하도록 보완했다. Evidence의 generated Python cache/bytecode는 폴더 내부 `.gitignore`로 Git 게시 대상에서 제외한다.
