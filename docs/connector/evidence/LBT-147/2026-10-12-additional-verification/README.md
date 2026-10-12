# LBT-147 추가 회귀·로컬 OpenStack 검증

2026-10-12 KST. 이전 리뷰 R1/R2/R3에 더해 Data WSS backpressure와 SSH Close/Resize 종료 정체를 재현하여 수정했다. 별도 승인받은 tracing 변경은 테스트의 잘못된 elapsed 하한만 수정한다. tracing production timeout/budget은 변경하지 않았다.

이 패키지는 LBT-147 전체 PR 범위의 추가 감사다. 이전 날짜 Evidence는 그대로 보존한다. 현재 감사·원문/AC 추적·실패/보안/수명주기·잔여 범위는 [AUDIT.md](AUDIT.md), 파일별 범위는 [file-review.md](file-review.md), 독립 검토는 [independent-review.md](independent-review.md)에 있다. 실행별 소스와 결과는 [validation-summary.json](validation-summary.json), [source-manifest.json](source-manifest.json) 및 [원본 ZIP](audit-logs.zip)에 기록한다.

검증 단계는 구분한다.

- 누락됐던 M2 Provision, M3 Lifecycle, Control WSS full lifecycle, Internet ON/OFF 네 개 suite를 **로컬 OpenStack labbit-test**에서 실행했다. KTCloud 실행이 아니다. 실행 소스는 `da6b670`이다.
- 최신 main `7aaa5ca`를 feature branch에 통합한 뒤, 최종 Terminal 수정 소스로 실제 VM 2개의 R1 exec cancel/deadline, 실제 target에 대한 scoped Nova/Neutron 503, affinity/404, host pin mismatch, production runControl healthy/unread Data 종료, VM별 30회 SSH open/exec/close와 idle 대기 후 인증 성공을 다시 실행했다. 이 부하 시험은 접속 identity를 비교하지 않는다.
- 코드 커밋 `bd8071d`에서 정상 VM integration도 다시 실행했다. 실제 PTY input/output/resize, **PTY 종료 후 같은 공유 SSH의 SFTP INIT/VERSION/REALPATH**, idle 전후 SSH_CONNECTION identity 변화, gen2 Reset/new target/stale gen1 거절을 검증했다. tracked14 exact-ID 부재와 최종 matching residual0을 확인했다.
- Data/SSH TCP backpressure 회귀는 실제 loopback TCP/SSH/WSS와 생성한 테스트 키를 사용한다. 배포된 SaaS/Browser나 실제 cloud의 packet blackhole 검증은 아니다.
- 첫 Evidence 보완 push의 Linux Go CI는 format 누락을 검출했고, 다음 push는 manager 회귀가 특정 `internal/poll.(*FD).Write` stack을 요구한 OS 의존 assertion을 검출했다. 두 원격 실패와 수정 전 source를 ZIP에 보존했다. 최종 manager 회귀는 unread TCP에서 `SSHPTY.Write` 지속 대기와 종료·worker 해제·server EOF를 검증하며, 실제 kernel pending resize는 client write buffer를 줄이는 별도 회귀가 담당한다.
- 전체 Go/race/20회 회귀와 Web typecheck/lint/test/build, 계약 validator 결과는 validation-summary에 있다. 일반 Go의 opt-in SKIP는 별도 cloud 실행 PASS와 구분한다.

각 cloud suite의 exact-ID cleanup assertion과 사후 read-only inventory를 확인한다. `cleanup verify GET/404` 로그는 assertion **직전** 출력이므로 그 문구만 cleanup 성공으로 취급하지 않는다. 해당 testcase PASS와 뒤의 조회 결과를 함께 확인한다. 실패한 초기 fixture 실행도 ZIP에 보존하며 최종 PASS로 덮어쓰지 않는다.

남은 범위: KTCloud gen2 Reset/소유 Tier2 실제 부재, 실제 Browser/배포 SaaS CC02·routing/reconnect/Live, production SFTP/File WSS, 실제 SSH pty-req/close-ACK packet fault·자연 outage/key rotation/packet loss·hard process restart·장시간 soak/대규모 load. 2VM ×30회는 짧은 동시 부하다. 실제 VM child runControl의 서버 측 SSH EOF는 별도로 관측하지 않았다. 별도 loopback 실제 manager 회귀는 EOF를 관측한다. 연결이 이미 단절된 상대에게 ENDED 전달을 보장하지 않는다.

## 재현

Go1.27.1, race를 지원하는 C compiler/CGO, Node24.19.0/npm11, Python3.11+와 PyYAML/openapi-spec-validator/jsonschema가 필요하다. 저장소 밖 Python 환경에 validator를 설치한다. 코드 커밋은 source-manifest의 `codeCommit`을 사용한다. manifest는 실행 checkout의 byte SHA와 Git clean-filter blob을 따로 기록하므로 Windows EOL 변환을 숨기지 않는다. 과거 cloud 네 개 결과 재현은 `da6b670` 별도 checkout을 사용한다.

1. 일반 검증은 repo의 `make setup && make test`와 동일한 target을 실행한다. Windows에서 make가 없으면 `$needsFormat = @(Get-ChildItem -Recurse -File -Filter *.go | Where-Object { $_.FullName -notmatch '[\\/]vendor[\\/]' } | ForEach-Object { gofmt -l $_.FullName }); $needsFormat`이 **게시 `_fixtures`를 포함해 출력0**인지 확인하고, `go mod download`, `go vet ./...`, `go test -count=1 ./...`, `go build ./...`, `web`의 `npm ci`, `npm run typecheck/lint/test/build`를 실행한다. 실행 전 `LABBIT_OPENSTACK_*` opt-in 및 `LABBIT_LBT147_ADDITIONAL_REAL_TEST`를 모두 0으로 둔다.
2. 관련 race: `go test -race -count=1 ./internal/connector/{vmaccess,provider/openstack,app,terminal,wss} ./internal/observability/tracing`(PowerShell에서는 package별 경로를 풀어서 전달). 새 backpressure/tracing 테스트와 기존 R1/R2/R3 테스트의 20회 선택 regex는 ZIP의 final-checks/regression-repeat-run.json에 있다.
3. Cloud fixture 준비만 수행: `python prepare_overlays.py --repo <absolute checkout> --output <새 외부 디렉터리>`. `_fixtures`의 공개 테스트 소스를 복사하고 절대 경로 overlay 세 개를 생성한다. 이 명령은 테스트나 cloud mutation을 실행하지 않는다. `--verify-source`는 최종 source-manifest Git blobs를 대조한다(과거 da6 cloud 재현에는 적용하지 않는다).
4. 실제 cloud 실행은 별도 명시적 opt-in이다. `LABBIT_PROVIDER_CONFIG_FILE=<외부 clouds.yaml>`, `OS_CLOUD=labbit-test`, `LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE=<외부 key>`, `LABBIT_OPENSTACK_SSH_USERNAME=ubuntu`를 설정한다. fixture는 ubuntu/m1.small/sharednet1, SSH 접근 CIDR172.16.8.1/32를 요구한다. credential/private key/known_hosts 원문은 제공하지 않는다. 다른 cloud에서 동일 acceptance라고 가정하지 않는다.
5. 모든 opt-in을 먼저 0으로 설정한 뒤 아래 선택 플래그만 1로 바꾸고 `go test -race -json -count=1 -timeout=30m -overlay <output/cloud-overlay.json> -run '^선택테스트$' ./internal/connector/provider/openstack`를 **순차** 실행한다. 테스트는 실제 VM/network 생성·삭제를 수행한다. 시작 전과 각 실행 후 `LABBIT_OPENSTACK_RESIDUAL_AUDIT=1`로 `TestLBT147ReadOnlyResiduals`를 같은 overlay에서 실행한다. 각 testcase의 exact-ID cleanup과 inventory matching0을 함께 확인한다. 실패 후에도 inventory를 실행하되 타인의 자원을 이름 접두사로 일괄 삭제하지 않는다.

| 테스트 | 선택 플래그 |
| --- | --- |
| TestOpenStackM2ProvisionIntegration | LABBIT_OPENSTACK_M2_TEST |
| TestOpenStackM3LifecycleIntegration | LABBIT_OPENSTACK_M3_TEST |
| TestOpenStackM2ControlWSSIntegration | LABBIT_OPENSTACK_M2_WSS_TEST 및 LABBIT_OPENSTACK_M3_WSS_TEST 둘 다 |
| TestOpenStackInternetPolicyIntegration | LABBIT_OPENSTACK_INTERNET_POLICY_TEST |
| TestLBT147AdditionalRealFailures | LABBIT_LBT147_ADDITIONAL_REAL_TEST; overlay는 real-failure-overlay.json |

마지막 실제 VM suite에는 추가 환경 `LABBIT_ADDITIONAL_REPO_DIR=<repo>`, `LABBIT_ADDITIONAL_OUTPUT_DIR=<output>`, `LABBIT_ADDITIONAL_APP_OVERLAY=<output/actual-runtime-overlay.json>`가 필요하다. parent가 소유 VM과 temporary metadata를 생성하고 child runControl 시험 후 정리한다. child metadata file은 parent가 주입하므로 사람이 만들거나 credential을 복사할 필요가 없다. 로그는 output에 보존한다. 내부 원본 runner의 당시 개인 경로는 역사적 기록이며 위 입력 기반 재현을 따른다.

ZIP은 원본 실행 결과, 초기 실패, 실행 source pin, fixture/runner와 독립 reviewer red/green을 포함한다. ZIP entry CRC/SHA256은 archive-manifest, 게시 artifact는 artifact-checksums, scoped secret 검사는 secret-scan에 있다. secret 검사는 확인 가능한 로컬 secret/key 값 및 알려진 패턴 범위이며 모든 unknown secret/Git history를 증명하지 않는다.
