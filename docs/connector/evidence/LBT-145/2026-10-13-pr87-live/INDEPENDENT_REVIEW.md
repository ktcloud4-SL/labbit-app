# PR #87 실제 KT 네트워크 재검증 독립 검토

리뷰어: 구현과 분리된 `/root/kt_naming_audit`. 준비 기준일 2026-10-13 KST. 이 문서는 신규 live 재검증 기록이며 과거 공개 Evidence를 수정하지 않습니다.

## 범위와 판정 경계

- 기준 PR HEAD `905ed25d1f2e04022bb814b83b61fb8eaa490366`, production/test/contract source350은 수정 검증 commit `943d0bdd5e60221638886b03aa37038976cb8a5f`와 동일합니다. 최신 main은 `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`입니다.
- 추가 승인 범위는 actual `New`/production `prepareKTCloudNICs`→신규 Management Tier1+Firewall1→public Reconcile/Cleanup→complete inventory의 exact absence 및 baseline 복원→이미 부재가 확인된 동일 refs의 별도 Cleanup입니다. VM/Connector 설치/SSH/Reset은 실행하지 않습니다.
- 기본 Private Tier의 physical refId는 읽기 전용 topology 입력입니다. baseline 전체 Tier/Policy는 삭제 대상이 아닙니다.
- 본 리뷰어는 소스·원장·시험 로그만 읽고 자격증명·보호 설정 내용을 읽거나 클라우드 API·SSH·원격 mutation을 수행하지 않습니다. actual 실행 및 테스트 실행은 parent가 수행하며 아래 기록에서 직접 검토와 구현자 실행을 구분합니다.
- 개인 AGENTS/PRE_MERGE_AUDIT와 앞선 PR 전체 diff/SSOT 독립 audit를 적용합니다. Jira145/158, D18(v5)/D20(v7)/검증전략(v3)의 actual typed ID 소유권, uncertain mutation 관측, 부분 성공 보존, Mock/actual 구분 요구가 이 시험에 적용됩니다. 새 시험 범위가 C1/PTY/SFTP/multi-VM 완료조건을 추가하지 않습니다.

## 수정 전 findings

첫 helper `.local/pr87-live-20261013/source/pr87_live_test.go`(501행) 및 `run.py`/`overlay.json`을 직접 읽었습니다. parent가 제시한 초기 helper SHA256은 `ff562f77cd05444b62cd715dadb27a17b5f765ceaef4ccee1bfa2153d90ac59a`입니다. actual mutation 전에 아래 두 findings를 parent에게 전달했고 parent는 사용자에게 수정 전 보고했다고 회신했습니다.

| ID | 심각도·초기 위치 | 재현 조건·영향 | 필요한 수정·회귀 |
| --- | --- | --- | --- |
| KT-L1 | P2, run.py:43 / helper:267,378 | Go test 기본 timeout10분보다 작업8분+independent fallback cleanup5분의 합계가 큼. 작업 timeout 부근 실패 시 정리 중 Go 전체 timeout이 프로세스를 강제 종료할 수 있음. | 전체 timeout을 명시적으로15분 이상 설정하고 실제 runner/context 경계를 대조. |
| KT-L2 | P2, helper:419–420,425,435–447,374–403 | 반환 resources를 삭제 허용 목록/fallback refs에 먼저 등록한 뒤 기본 Tier·매핑 모순을 검출. 기본 Private Tier ref+Firewall ref를 반환한 상황에서 t.Fatal 후 fallback이 동일 refs를 삭제할 수 있음. production Cleanup 보호는 Connector DMZ에 한정되므로 Private 보호는 helper가 수행해야 함. 현재 production 정상 코드에서 해당 오작동이 실제 관측됐다는 뜻은 아님. | raw 반환 기록과 삭제 권한 분리. baseline 모든 Tier/Policy ID, typed/logical/generation/중복 및 exact mapping 확인 전에 권한 금지. 정상 confirmed partial은 보존하되 모순 refs는 mutation0. 기본 Private/기존 정책/잘못된 identity/매핑 불일치의 실제 local transport DELETE0 회귀. |

## 수정 후 실행 전 재검토

수정 helper SHA256 `fa22e3518cecb9a2c5ce2d0370844537f4b940f3f14a259df5b1abffea1f386f`, runner SHA256 `0e2354a62dde91e020ec8ace811582a43e4b857c75302b49c859b99f78074cbb`를 리뷰어가 실제 파일에서 계산했고 parent와 receipts 값이 일치했습니다. 현재 HEAD905 및 tracked diff0도 직접 확인했습니다.

- KT-L1 **해결**: runner:48의 명시적 `-timeout=20m`가 작업8분+independent inventory confirmation2분+fallback cleanup5분의 합계보다 길고 여유가 있습니다. 모든 `t.Cleanup`은 완료를 보장하는 외부 재해 복구가 아니며 process hard-kill/호스트 종료는 이 로컬 테스트가 보장하지 않습니다.
- KT-L2 **해결**: helper:603은 `ReturnedResources`에 관측용 값만 남깁니다. `pr87ConfirmRefs`(:241–316)는 baseline 전체 Tier/Policy ID, resource type/logical/generation/중복, exact-ID Tier Name/CIDR/shared explicit false, exact-ID Firewall의 유일 comment/완전 SSH 매핑을 확인합니다. 성공 반환의 NIC identity/mapping도 :616–624에서 검사한 후 :627–628에서만 fallback refs/transport 삭제 권한을 부여합니다. 모순은 authority0이며 partial confirmed Tier는 유지합니다. policy attempted+ID 미확정에서는 기존 cleanup guard가 Tier 삭제도 차단합니다.
- `guard.jsonl`을 리뷰어가 직접 파싱했습니다: **21 leaf PASS**, FAIL0/SKIP0. 19 invalid 상황에서 pure guard가 권한을 주지 않고 실제 local RoundTripper의 underlying HTTP 호출0을 확인하며 valid partial/full2개를 보존합니다. 이 테스트는 fixture guard 회귀이며 actual cloud fault injection이 아닙니다.
- `compile-guard-fixed.jsonl`/receipt: gate false, live test SKIP, exit0. `vet-guard-fixed` receipt: exit0, raw 로그는 출력 없는 정상 로그입니다. 위 검증은 parent 실행이며 reviewer는 파일/receipt/실제 소스를 대조했습니다.
- `readonly.jsonl`/receipt: source905 actual KT 조회를 parent가 실행하여 PASS/skipfalse, servers0/flavors235/images12를 보고했습니다. reviewer는 receipt의 exit0/read-only 범위를 확인했고 직접 KT API를 실행하지 않았습니다.

**실행 전 판정**: 두 확인된 P2가 해결됐고 추가 확인된 차단 결함은 없습니다. 기존 사용자 승인 범위의 신규 Tier1+Firewall1, VM0, 기본 자원 보존 및 검증된 두 refs의 정리 시험을 진행할 수 있습니다. actual 결과와 full baseline/정확 부재/source 동일성을 확인하기 전 성공 판정은 보류합니다.

앞선 source943d의 80개 httptest 회귀와 역사적 KT source6a6 actual lifecycle은 각각 별도 증거입니다. 새 live의 실제 응답이 HTTP404 또는 body404 중 한 형태만 보이면 관측한 형태만 actual 검증으로 인정합니다. 불확실한 ID/metadata/lookup에서는 자동 채택·blind DELETE를 하지 않아 manual 원장 조사와 수동 확인이 남을 수 있습니다.

## Actual raw 결과 대조 및 추가 finding

parent가 실행한 `live.jsonl`, `live-receipt.json`, `ownership-ledger.json`을 reviewer가 직접 읽고 source/hash와 대조했습니다. receipt exit0/gate true, test PASS/skip0/fail0입니다. ledger 실행 구간은 `2026-10-13T01:48:31.6938267Z`→`01:49:24.7492843Z`이고 runner 구간은 `01:48:27.953714Z`→`01:49:24.819661Z`입니다. 두 구간을 VM/SSH 시간으로 사용하지 않습니다.

### KT-E1 — P2: Reconcile/Cleanup 원장 스냅샷 포인터 덮어쓰기

- 실행 source의 helper:648–651에서 `BeforeCleanup=&observed`를 저장하고 :661–662 및 :678–679에서 같은 `observed` 지역변수에 후속 Reconcile 결과를 재대입합니다. 저장한 여러 포인터가 같은 지역변수를 참조하므로 과거 값이 최종 absent 결과로 바뀝니다. result도 :656–657/673–674에서 같은 방식으로 첫 Cleanup 결과 포인터를 덮어씁니다(이번 두 결과는 내용이 동일).
- 실제 raw ledger의 `beforeCleanup.Observations`는 두 ref 모두 `Exists=false`, `ObservedState=ABSENT`입니다. 이 값으로 create 직후 두 자원 존재를 직접 증명할 수 없습니다. 코드의 present 검사와 `CREATE_AND_PUBLIC_RECONCILE_PASSED` raw 출력(`01:48:59.8246212Z`)은 해당 단계가 통과했다는 별도 증거입니다.
- parent에게 보고서/원장 수정 전에 finding을 전달했습니다. executed source 및 raw 원장을 사후 변경하거나 유실된 첫 관측을 새 실측처럼 복원해서는 안 됩니다. 실제 mutation 실패나 현재 production 코드의 결함으로 분류하지 않으며, 이 기록 결함만을 이유로 새로운 cloud 자원을 만들도록 요구하지 않습니다.
- 향후 helper에는 깊은 immutable snapshot 또는 각 phase별 별도 지역변수를 사용하고, 후속 absent 결과를 대입해도 첫 present snapshot이 유지되는 회귀가 필요합니다. 게시 문서는 stage+executed guard에 근거한 present 검사 통과와 raw ledger snapshot 유실을 구분해야 합니다.

### 독립 확인한 실제 실행 범위

- Management Tier `b10cb63d-9e5d-49c4-82b2-d7adb125c258`, physical refId `d299f160-b30f-4c6d-b9de-1910c64a5c9a`는 서로 다릅니다. 이름46자, CIDR `172.25.242.0/24`, `sharedExplicit=true`, `shared=false`, ACTIVE가 createdTier에 기록됐습니다.
- Firewall `e5616932-61c0-4016-afa9-66655c4cf260`는 configured DMZ→새 Tier, source /32, TCP22, accept/enable/NAT disable/all destination의 완전 매핑입니다. create POST job `89a7b1c35a02dfe9e7efa8d3ba3f98ac`의 RUNNING→SUCCESS를 HTTP GET events에서 직접 확인했습니다.
- mutation event는 정확히 create POST2개와 DELETE4개입니다. 정상 Cleanup은 정책 HTTP204 뒤 Tier HTTP202/body202이며, exact absence/full baseline 확인 후 별도 already-absent Cleanup은 정책·Tier 모두 **HTTP404 + numeric body404**입니다. uncertainty 이후 blind DELETE를 반복한 흐름이 아닙니다.
- initial present 검사의 raw 단계 통과 뒤 정상 public Cleanup과 already-absent Cleanup 모두 `SUCCEEDED`, 두 refs `DELETED`를 기록했습니다. 최종 Reconcile 두 refs는 `KNOWN_RESOURCE`, 정확 typed/logical/generation 일치, Exists=false/ABSENT입니다. 최종 absent 값은 포인터 alias에도 손실되지 않았습니다.
- baseline/final 전체 decoded Tier 배열은 정확히 같고3개, policies0→0, servers0→0도 배열 전체 일치입니다. 원본 기본 DMZ/Private와 external entry가 보존됐습니다. production transport guard는 non-NSM mutation을 금지하며 VM 생성 호출이 없습니다.
- executed helper `fa22…386f`/runner `0e235…cbb`의 현재 raw hash가 live receipt와 일치했습니다. parent는 production source350 동일성을 보고했으며 최종 published exports/manifest 검토는 아직 별도 단계입니다.

Actual **HTTP200/body404**, string body404, malformed pagination/shared/error/ownership mismatch는 이번 실행에서 관측되지 않았습니다. 그것은 기존 fault-only httptest80개 범위이며 actual PASS로 확대하지 않습니다. 새 VM/SSH/Reset은 실행하지 않았으므로 앞선 전체 lifecycle의 재실행 증거가 아닙니다.

## KT-E1 보완과 최종 공개 bundle 대조

원래 실행 helper/runner/원장을 유지한 상태에서 별도의 `pr87_live_snapshot_fixed_test.go`를 검토했습니다. 수정 copy SHA256 `035d7bc297829092ed7ac08f8b01ac0abafb4f6f38326e17a5232402205f1f83`를 reviewer가 직접 계산했고 receipt/export와 일치했습니다. `snapshot-fix.patch.txt` 전체는 저장 지점의 immutable snapshot 변경과 테스트 추가에 한정됩니다.

- `pr87SnapshotReconcile`(:703–711), `pr87SnapshotCleanup`(:712–720)은 struct와 해당 observations/resources slice 및 SafeError를 각각 복사합니다. 정상/fallback/첫·두 번째 Cleanup/Reconcile 저장 지점 모두 이를 사용합니다.
- parent 실행 `snapshot-fixed.jsonl`을 직접 파싱했습니다: **24 leaf PASS/FAIL0/SKIP0**, 기존 guard21+새 snapshot3. 새 tests는 present→absent에서 같은 slice 변경과 지역변수 재대입, 첫 Cleanup→UNKNOWN 변경, SafeError 재사용을 실제 local 코드로 검증합니다. fixed overlay vet exit0는 parent가 tool 결과로 보고했으며 별도 raw vet 파일을 reviewer가 직접 확인한 결과와 혼동하지 않습니다.
- KT-E1은 **향후 도구에서 local 수정·회귀 해결**, 기존 actual Evidence의 첫 관측 snapshot 유실은 **그대로 남는 기록 한계**입니다. fixed helper를 실제 KT에 실행하거나 유실 관측을 복원한 것으로 분류하지 않습니다. production 변경0, 추가 cloud mutation0입니다.
- `README.md`와 `PRE_MERGE_AUDIT_RESULT.md` 전체를 actual source/raw events와 대조했습니다. 원장 첫 값 유실, executed guard+raw stage의 present 검사 근거, 실제 HTTP404/numeric body404 한정, 기존80 httptest 및 historical6a6 VM/SSH와 별도 범위가 명시됐습니다. 재현 안내는 별도 fixed copy를 사용하도록 하며 실제 원본 fa22는 보존합니다.
- `result.json`에도 `initialReconcileLedgerSnapshotLost=true`, 정확 raw stage와 executed check를 적은 `initialReconcileProof`, `firstCleanupSnapshotAliased=true`, `fixtureSnapshotFixLiveTested=false`, local leaf24 및 사후 원본 복원 아님을 명시했습니다. 기계 판독 결과가 첫 raw snapshot 보존을 주장하지 않습니다.
- reviewer가 `tested-source.json`에 있는 **현재350개 파일을 LF정규화 SHA256으로 직접 대조: mismatch0**. 기존1183개 파일 raw 변경/유실0은 parent의 `preservation-check.json` 결과를 읽어 확인했습니다.
- 실제 raw/export 동일 이름 **18개 사본 byte 일치**, actual overlay/runner의 `.txt` 사본 byte 일치, fixed overlay 및 fixed test log의 export byte 일치도 직접 확인했습니다. raw 원장의 `beforeCleanup`을 고쳐 쓰지 않았습니다.
- `candidate-files.json`: basef7a9/tested905, full191/non-Evidence40. 새 Evidence 외 production delta가 없으므로 앞선 source943d full audit 판정 범위를 유지합니다.
- parent 실행 `publication-scan.json`(`2026-10-13T01:59:07.002555+00:00`)은 candidate191/15commit snapshots/383blobs에 대해 protected credential exact-match와 private-key/common pattern 검사, findings0/credentialValuesPrinted=false를 기록합니다. reviewer는 이 receipt와 범위를 확인했고 credential 원문을 직접 읽거나 scanner를 재실행하지 않았습니다.
- 최종 artifact manifest는 이 독립 기록 복사 후 parent가 파일 집합·해시를 고정하여 별도 대조하는 출판 metadata입니다. 자기참조 해시나 기존 공개 Evidence 변경을 이번 actual 실행 증거로 사용하지 않습니다.

## 최종 독립 판정

**승인된 좁은 actual KT 생성·조회·삭제 재검증은 PASS입니다.** 수정된 production Tier/Firewall 메서드 실행, typed exact-ID 확인, public Cleanup, 실제 HTTP404/numeric body404의 already-absent Cleanup, 최종 owned2 absent 및 complete baseline3/0/0 복원을 raw 로그·HTTP events·executed guards·현재 source 해시로 대조했습니다. 두 pre-live P2는 실행 전에 해결됐습니다. 추가된 KT-E1은 원래 실제 관측 기록의 한계를 밝히고 별도 local 도구 수정·회귀를 검증했으며, 원본 snapshot이 보존됐다는 판정을 하지 않습니다.

이 판정은 신규 VM/SSH/Reset/전체145 재실행, 모든 실환경 fault branch, multi-page eventual consistency, crash/hard-kill 자동복구, Backend/Browser C1 및 PTY/SFTP 완료를 뜻하지 않습니다. 기존 unit/httptest/전체 체크와 역사적 actual 증거는 버전을 구분하여 유지합니다. source 및 raw 범위에 대해 추가 확인된 필수 결함은 없으며 GitHub 인간 reviewer 승인·Jira 상태 변경·push/merge·외부 메시지를 이 독립 판정의 권한으로 해석하지 않습니다.
