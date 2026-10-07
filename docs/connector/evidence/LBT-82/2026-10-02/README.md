# LBT-82 / PR #48 · 실행 Evidence

Tested **source commit**: `57990544fac291def5d7cf7c6d5b54da3cabc4c8`.
Base main: `bec3f75af7991e5235556f98cbc9bdeb418265bd`; previous PR head: `ab3abe26aee6ee495a8301f4ae046cd634e649ee`.
실행일 2026-10-02 KST. Windows / Go1.27.1 / CGO_ENABLED=1 + GCC / Node24.19.0.
이 디렉터리를 추가한 후속 commit은 **Evidence-only**다. source commit 이후 production/tests/contracts/runtime/dependencies 변경은 없으며, 실환경 결과를 Evidence commit 자체에서 새로 실행했다고 주장하지 않는다. `source-manifest.json`은 모든 main 대비 변경 파일의 Git blob OID를 제공한다.

## 실제 실행 결과

| Target | 결과 | 원본 |
| --- | --- | --- |
| Keystone live validation + Image/Flavor/Server/Network 조회 | PASS 2.11초 · servers0/networks4 | [real-m1.jsonl](real-m1.jsonl) |
| Mock SaaS Control → Handler → actual OpenStack full lifecycle | PASS 240.31초 | [real-wss-lifecycle.jsonl](real-wss-lifecycle.jsonl) |
| Internet policy 최초 실행 | **FAIL** 262.58초: ON reachability SSH/exec FAIL119.02초, OFF PASS141.84초 | [real-internet.jsonl](real-internet.jsonl) |
| 최초 Internet 실패/cleanup 뒤 조회 | PASS · servers0/networks4 | [real-after-failed-internet.jsonl](real-after-failed-internet.jsonl) |
| 같은 source SHA ON-only 1회 재확인 | **ON branch PASS114.72초**, 부모 선택 target116.28초. OFF는 이번 재검증에서 미실행. 첫 실패 원인 미확정 | [real-internet-on-recheck.jsonl](real-internet-on-recheck.jsonl) |
| 모든 mutation/재확인 종료 후 최종 조회 | PASS · servers0/networks4(시작 수치와 동일) | [real-post-cleanup.jsonl](real-post-cleanup.jsonl) |

WSS raw log에 `Provision(g1)` 및 `Reset(g2)` 각각 StartupScript/authenticated SSH/host-key 확인, Reconcile(g2)8개 PRESENT, Cleanup8개 삭제, final Reconcile(g1+g2)16개 ABSENT/residual0가 있다. 공유 Management Network/SG는 generation-owned 삭제 대상이 아니다. fixture가 만든 임시 keypair/management SG는 별도로 t.Cleanup한다.

Internet 검증은 VM 안에서 Python socket.connect_ex 결과를 기대값에 assert하고 SSH/exec 실패는 FAIL로 처리한다. 첫 ON 실패의 오류는 안전하게 일반화된 `VM initialization did not complete`이며 transport/exec/assertion 중 어느 원인인지 이 원본만으로 확정하지 않았다. 최초 실패를 숨기거나 환경 탓/일시 장애로 단정하지 않는다. ON 재실행 성공을 전체 최초 실행 PASS로 덮어쓰지 않는다.

## 자동 검사

원본 자동 검사 로그는 [automated-logs.zip](automated-logs.zip)에 있다. 압축 해제 시 all-unit.jsonl, connector-race.jsonl, regressions-20.jsonl, checks.json, contracts.log, format.log/format-corrected.log, Web 로그 및 결과 JSON, 실행 scripts를 확인할 수 있다.

- Provider 신규/기존 핵심 회귀 14개 top-level 각각20회 PASS. subcases 별도 포함.
- Connector 전체 + Server connector/connectorwss race PASS. 실제 cloud targets는 이 suite에서 SKIP.
- vet/build PASS. 전체 Go suite는 **기존 Windows POSIX0600 fixture1건 FAIL**. main과 동일한 blob이며 임의 수정하지 않음.
- format 최초 helper는 Windows PowerShell5 API 미지원으로 FAIL; PowerShell7 corrected 검사 LF-normalized 차이0/CRLF-only27. 다른 팀원 파일을 reformat하지 않음.
- Web typecheck/lint/9files102tests/build PASS.
- CI validator 버전과 동일 PyYAML6.0.3/openapi-spec-validator0.9.0/jsonschema4.26.0로 contracts PASS. 로컬 Python3.12(CI3.13). migration metadata만이며 실제 PostgreSQL 검증 아님.

최신 SSOT 추적성·전체 diff·findings 및 독립 두 번째 패스는 [AUDIT.md](AUDIT.md)를 참조한다. 사용자 제외 F1(P1)/F6(P2) 및 auth/API 최종 wire 상세 구분은 open. 이 증거가 전체 PR 승인을 의미하지 않는다.

## 재현

1. 이 Evidence-only commit에서 [reproduce.ps1](reproduce.ps1)을 내려받아 repository 밖에 저장한다.
2. 깨끗한 checkout에서 tested source SHA `57990544fac291def5d7cf7c6d5b54da3cabc4c8`를 checkout한다. source SHA에는 후속 Evidence 파일이 없는 것이 정상이다.
3. Go 및 접근 가능한 실제 OpenStack, customer-local clouds.yaml, matching Ed25519 private key를 준비한다. Secret은 출력/공유하지 않는다.
4. repository root에서 아래 실행한다. script는 target별 subprocess를 순차 실행하며 실패/skip을 성공으로 기록하지 않는다. 실패하면 cleanup 로그와 실제 리소스를 확인한 뒤 다음 행동을 판단한다.

```powershell
& '<repository 밖에 저장한 reproduce.ps1>' `
  -ProviderConfigFile '<customer-local clouds.yaml>' `
  -SSHPrivateKeyFile '<customer-local Ed25519 private key>'
```

사용 환경변수 **이름**:
`LABBIT_PROVIDER_CONFIG_FILE`, `OS_CLOUD`, `LABBIT_OPENSTACK_SSH_USERNAME`, `LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE`,
`LABBIT_OPENSTACK_INTEGRATION=1` / `LABBIT_OPENSTACK_M3_WSS_TEST=1` / `LABBIT_OPENSTACK_INTERNET_POLICY_TEST=1`(target별 분리).
선택 입력: `LABBIT_OPENSTACK_TEST_IMAGE`(ubuntu), `LABBIT_OPENSTACK_TEST_FLAVOR`(m1.small), `LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK`(sharednet1), `LABBIT_OPENSTACK_EXTERNAL_NETWORK_ID`, `OS_PROJECT_ID`, `LABBIT_OPENSTACK_TEST_SSH_CIDR`(Connector source CIDR), `LABBIT_OPENSTACK_TEST_INTERNET_TARGET`(default external TCP1.1.1.1:443). Environment-specific defaults는 테스트에 명시되어 있으며 secret 값은 사용자의 로컬 설정으로 공급한다.

Commands:

```text
go test -json -count=1 -timeout=2m -run '^TestOpenStackM1Integration$' ./internal/connector/provider/openstack
go test -json -count=1 -timeout=20m -run '^TestOpenStackM2ControlWSSIntegration$' ./internal/connector/provider/openstack
go test -json -count=1 -timeout=25m -run '^TestOpenStackInternetPolicyIntegration$' ./internal/connector/provider/openstack
go test -json -count=1 -timeout=15m -run '^TestOpenStackInternetPolicyIntegration$/^internet-on$' ./internal/connector/provider/openstack
```

검증 무결성: [artifact-checksums.json](artifact-checksums.json)에 모든 배포 파일(자기 자신 제외)의 SHA256/bytes가 있다. 폴더 한정 `.gitattributes`로 Windows/Linux의 자동 EOL 변환을 막고 staged Git blob까지 검증했다. 원본 로그의 UTF-16/UTF-8 인코딩은 변경하지 않았으므로 일부 파일은 GitHub에서 내려받아 BOM을 지원하는 editor/PowerShell로 읽는다. 실제 credential/key/endpoint known-value scan을 통과했지만 완전한 DLP 보장은 아니다.

미실행: 로컬 PostgreSQL integration(DSN/Docker 없음), actual central SaaS/TLS443/G1/Browser E2E, 실제 Worker/no-script Multi-VM, 실제 token revoke/API 장애/부분 실패/재시작/취소/ID·IP 강제 재사용 주입, standalone M2/M3/M1 mutation. Mock/localTCP tests와 정상 real lifecycle을 구분한다. 이전 날짜/SHA 실환경 결과는 이 source commit 성공의 대체 증거로 사용하지 않는다.
