# PR #48 · 독립 pre-merge audit (2026-10-02 KST)

검토 기준: source `57990544fac291def5d7cf7c6d5b54da3cabc4c8`, latest main `bec3f75af7991e5235556f98cbc9bdeb418265bd`, 기존 PR head `ab3abe26aee6ee495a8301f4ae046cd634e649ee`.
사용자가 승인한 Provider F2–F5 및 추가 연결 검증 수정만 포함한다. main merge와 Backend/WSS 추가 수정은 수행하지 않는다.

## Findings-first 기록

| Finding | 심각도·기존 위치 | 재현·영향 | 필요한 회귀 / 처리 |
| --- | --- | --- | --- |
| cached-token-only 연결 검증 | P2 · `client.go:219-229`(e3a9b94) | 초기 인증 후 토큰 폐기/Keystone 장애에도 nonempty token이면 성공. 검증 화면에서 잘못된 정상 판정 가능 | HEAD + 200/204/404/401/403/503/429, safe error, timeout/cancel, refresh/concurrency. 기존 구현에서 신규 HEAD tests 실패를 먼저 확인한 뒤 Provider에서 수정 |
| SSH 취소 후 socket 유지(F3) | P2 · `startup.go` | handshake/command 정지 시 취소 후에도 deadline까지 연결 유지 가능 | 실제 localhost TCP/SSH 정지 + 즉시 취소/peer close. 수정 |
| 실패한 Reset/Provision test cleanup ID 누락(F4) | P2 · `control_wss_integration_test.go`, `integration_test.go` | partial g2 또는 최초 partial g1 결과를 못 보존하면 test teardown에 잔여 리소스 발생 | FAILED/UNKNOWN partial refs, mutation 전 recorder 등록, g1/g2 exact-ID 누적, listener join. 수정. 제품 Reset의 자동 전체 rollback 추가 아님 |
| Worker/no-script SSH readiness 누락(F5) | P2 · `provision.go` | no-script/Worker를 ACTIVE만으로 성공 처리할 수 있음 | 모든 VM authenticated SSH, Worker 실패 FAILED/취소 UNKNOWN + resource IDs 보존. 수정. OP-02 PTY 완료 보장 아님 |
| 재검증 가능한 공개 증적 부족(F2) | P2 · PR Evidence | CI opt-in skip/과거 SHA 성공은 현재 lifecycle 증거 아님 | 현재 tested source SHA와 원본 로그/재현 script/checksum을 연결. 아래 Evidence 참조 |

## Open findings · 수정 범위 밖

| 항목 | 심각도·위치 | 재현 조건·영향 | 담당·필요 회귀 |
| --- | --- | --- | --- |
| F1 Reset producer/consumer 불일치 | P1 · `internal/server/connector/command.go:35-72` / `internal/connector/wss/handler.go:372-386` | Backend nil/empty/missing logicalName/wrong previous generation를 보내면 strict consumer가 거절 | Backend/Control owner; production producer → 실제 Handler negative regression. 사용자 제외, 미수정 |
| F6 schema validation drift | P2 · `internal/connector/wss/handler.go:181,299,367,618` | required sentAt 누락, nonhex startup.sha256가 Handler validation 통과 가능 | WSS/공용 계약 owner; schema-vs-handler negative tests. 사용자 제외, 미수정 |
| 인증/API 오류 최종 wire·UI 상세 구분 | adjacent P2 · `internal/connector/wss/handler.go:276-281` | Provider는 안전한 오류 sentinel을 구분하지만 Handler가 같은 ERR_INFRA_OPENSTACK으로 일반화 | WSS/Backend owner, owning wire/UI 계약 확인 후 필요 regression. F-ORG-01 전체 E2E 해결 주장 금지 |

위 open findings 때문에 **전체 PR 승인/문제없음 판정은 하지 않는다**. 이번 승인 범위에서 추가 확정 blocker 미발견과 별개다.

## SSOT → 구현 → 테스트 → 실제 Evidence

2026-10-02 재조회: [LBT-82](https://samsunglions.atlassian.net/browse/LBT-82) 진행 중, [LBT-15](https://samsunglions.atlassian.net/browse/LBT-15) 진행 중, [LBT-83](https://samsunglions.atlassian.net/browse/LBT-83) 아이디어.
[D-18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730), [D-19 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7176194), [D-20 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283), [D-24 v12](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749), [테스트 전략 v3](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637), [기능 명세 v23](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/4653066). D-19 본문 첫 조회는 tool 오류로 실패했으며 재조회해 immutable snapshot/generation 정책을 확인했다. D-24는 Provider 현실 대조·Reconcile 전 mutation 재실행 금지의 확정 범위만 적용하고 미확정 DR 방식/수치로 완료 판정을 만들지 않았다.
규칙은 repository AGENTS/CONTRIBUTING/TESTING, wire는 `contracts/connector/`, 실행은 `runtime/`을 적용한다. 정책을 새로 정의하는 문서가 아닌 해당 diff의 검토 기록이다.

| 요구 | 구현 | 자동 회귀 | 실제 검증 / 미검증 |
| --- | --- | --- | --- |
| F-ORG-01 연결 검증/조회·Secret 비반출 | `client.go`, `query.go`, `provider/query.go`, app lazy Provider, Handler ProviderRequest | connection_validation, query, app/runtime_provider, handler tests | `real-m1.jsonl`: actual Keystone HEAD + Image/Flavor/Server/Network. 인증/API 장애는 fake만; 최종 wire 구분은 open |
| LBT-82 Control → Provider → cloud | protocol/operation, app/Handler, `control_wss_integration_test.go` | Mock Handler/Provider, reconnect tests | `real-wss-lifecycle.jsonl`: localhost Mock SaaS(ws) → real Provider. 실제 SaaS/TLS/DB/G1 아님 |
| D-18 Lab 격리/공용 management SG/각 VM SSH | network/port/router/server/provision/startup/runtime | network, provision, multi_vm_readiness, ownership tests | real WSS는 Workspace 1VM. 실제 Worker/no-script Multi-VM 및 Lab간 통신은 미실행 |
| D-18 Internet ON/OFF 관리망 우회 방지 | management SG egress preflight, router policy, Internet test | no-router/unsafe management-SG reject tests | `real-internet.jsonl`: 최초 ON FAIL119.02초/OFF PASS141.84초/전체 FAIL262.58초. `real-internet-on-recheck.jsonl`: 같은SHA ON-only 단회 PASS114.72초, OFF 재실행 아님. 첫 실패 원인 미확정. 모든 외부 목적지/IP family 검증 아님 |
| D-19 immutable Reset/generation/ID·IP 재사용 | lifecycle, quota old Nova Flavor, ProviderConnectionID+ServerID host pin | lifecycle/reset quota, startup host identity/repin/persistence/concurrent first key tests | real WSS: StartupScript 포함 g1→g2. IP 강제 재사용/actual restart는 미실행 |
| D-20 부분 실패/UNKNOWN/no blind retry | operation, lifecycle/quota/reconciliation, exact-ID tracking | partial failure/408·429 UNKNOWN, cleanup uncertainty/404-only ABSENT, recorder FAILED/UNKNOWN tests | real WSS 정상 lifecycle. 실환경 응답 유실/부분 실패/재시작 주입 및 DB durable operation은 미실행 |
| SSH 작업 취소/보안 | startup context socket close, host-key pin, keypair fingerprint | actual local TCP/SSH cancellation, same identity key change reject, Ed25519 mismatch reject | real WSS SSH/script. actual cloud cancellation/host-key tamper는 미실행 |
| Cleanup 소유권/잔여 0 | cleanup exact IDs/dependency order, knownResources Reconcile | future/wrong identity generation reject, no candidate adoption, g1/g2 record tests | final WSS Reconcile: both generations exact IDs ABSENT. shared infra 제외. mutation response ID 자체 유실은 별도 Reconcile 필요 |
| contracts/runtime/development rules | Connector README/schema, runtime README/YAML, Makefile, dependencies | CI 버전의 OpenAPI/JSON Schema/runtime validator, vet/build/Web | PostgreSQL integration는 로컬 미실행; CI가 실제 실행한 결과만 별도 기록 |

## Full diff 및 두 독립 패스

`origin/main...5799054`의 변경 파일 53개를 대상으로 앞선 전체 52파일 승인자 패스와 이번 추가 Provider 3파일 패스를 결합해 최신 unchanged main과 재대조했다. `source-manifest.json`은 **53파일 모두**의 Git blob OID를 나열한다. 테스트만이 아니라 production 및 contracts/runtime/config/dependency 전체를 검토했다.
이전 PR head 이후 추가는 Provider source/test 10파일이며 Backend/WSS/contracts/runtime/go.mod/go.sum 추가 변경은 없다. 기존 PR의 공용 연결부 변경은 보존한다.

- reviewer 1: SDK token lock/reauth/HEAD 실제 구현과 신규 전체 diff; Provider vet/full test/full race, query 10회/race3회 직접 확인.
- reviewer 2: full diff와 fresh Jira/Confluence AC 재대조; 새로운 production blocker 미발견, 외부 Evidence/미실행 범위를 조건으로 freeze 가능. 원격 승인자 리뷰를 대신하지 않는다.
- 실패/보안/수명 분석: 입력 nil/empty, generation, same-ID/new-ID host key, socket 취소, partial IDs, no blind retry, shared-vs-owned cleanup을 확인. 생성 ID가 반환되지 않는 UNKNOWN은 recorder만으로 해결하지 않는다.
- 민감정보: actual credential/key 값 및 endpoint 원문의 known-value scan, log/source checksum 수행. 전체 DLP 보장 아님.

## 실행/skip 해석

자동 regression 14개 top-level 각각 20회 PASS(기존 ValidateConnection 1개 포함), Connector/Server connector 경계 race PASS, vet/build PASS, Web 9files/102tests 및 typecheck/lint/build PASS. 일반 Go suite의 opt-in 실제 cloud targets는 **SKIP**; 각 별도 real 로그에서 실제 pass인 분기만 실환경 PASS로 사용한다. 최초 Internet 전체 FAIL과 ON 단회 재검증 PASS를 구분한다.
전체 Go suite는 기존 Windows `tools/dev-auth-class-fixture/main_test.go:157` POSIX0600 대 mode0666 assertion 1건 FAIL. 이 파일의 main/source Git blob은 동일(`4a328b13edb95d4316b4d6a2357f5da96b881ef5`). 실패를 숨기거나 타 담당 코드를 고치지 않았다.
포맷 helper 첫 실행은 Windows PowerShell5의 StandardInputEncoding 미지원으로 FAIL; PowerShell7에서 같은 소스를 read-only 재검사하여 LF-normalized format 차이0/CRLF-only27. 원본 실패 로그와 corrected 로그를 둘 다 보존한다.
CI와 동일 validator 버전(PyYAML6.0.3/openapi-spec-validator0.9.0/jsonschema4.26.0), Python3.12 실행( CI3.13과 interpreter는 다름 ): contracts PASS. DB migration metadata 검사는 DB Integration 성공이 아니다.

미실행: 로컬 PostgreSQL Integration(DSN/Docker 없음), 실제 central SaaS/Browser E2E/G1, cloud token revoke/fault injection, actual Worker/no-script Multi-VM, cloud restart/cancellation/강제 ID·IP 재사용, standalone M2/M3 및 M1 mutation target. 필요한 실패 경로의 fake/localTCP 회귀와 정상 cloud lifecycle을 구분한다.
SDK 자동 refresh를 새로 활성화하지 않았으며 설정된 client에 대한 stale subject 방어만 제공한다. Keystone validation은 모든 OpenStack service health check가 아니다.
