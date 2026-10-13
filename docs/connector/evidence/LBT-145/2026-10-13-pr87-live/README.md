# PR #87 수정 소스 KT Cloud 실환경 재검증

## 결과와 실행 소스

**수정 소스 `905ed25d1f2e04022bb814b83b61fb8eaa490366`에서 KT D1 실제 Tier/Firewall 생성·조회·삭제 PASS. 이번 시험 소유 자원 잔여 0.** 제품 코드와 기존 테스트를 수정하지 않고 외부 Go test overlay로 현재 production 메서드를 호출했다. production 수정 commit은 `943d0bdd5e60221638886b03aa37038976cb8a5f`이고 source350개 Git LF/실행 working bytes 정규화 해시가 같다. 이번 공개 변경은 Evidence 추가다.

- 실제 실행: 2026-10-13 10:48:27–10:49:24 KST, Go 명령 전체 56.87초. 상세 API 시각은 UTC로 원장에 기록한다.
- Windows, Go1.27.1, 기존 보호된 `clouds.yaml`의 `ktcloud-d1`, verified TLS. 인증정보·개인키·토큰·HTTP headers·raw provider error/body는 저장하지 않았다.
- public `New`/`ValidateConnection`, SDK `ListServers`/`ListFlavors`/`ListImages`: 실제 PASS, VM0/사양235/이미지12.
- `prepareKTCloudNICs` → **수정된 `createKTCloudTier`** → NSM Tier 생성/완전 inventory 관측 → Firewall job SUCCESS/전체 매핑 확인 → actual typed refs2 → public `Reconcile`/`Cleanup`.
- 기존 standalone network fixture의 low-level `createTier`만 실행하여 production Tier 소유권 확인을 우회하는 방식은 사용하지 않았다.

## 생성·관측·Cleanup

| 항목 | 실제 관측 |
| --- | --- |
| 임시 Management Tier | `b10cb63d-9e5d-49c4-82b2-d7adb125c258` |
| 물리 NIC refId | `d299f160-b30f-4c6d-b9de-1910c64a5c9a`; networkId와 다름 |
| production 이름 | `labbit-683d3cb3ea6eec280de5-m-21a3230e03772a58`, 46자 |
| CIDR·공유 여부 | `172.25.242.0/24`, `shared:false` 명시적 존재 확인 |
| 임시 Firewall | `e5616932-61c0-4016-afa9-66655c4cf260` |
| 정책 | 기존 DMZ의 `172.25.0.10/32` → 새 Tier, TCP22 accept/enable, NAT disable, destination all; job RUNNING→SUCCESS |
| 생성/조회 | 종류별 POST1회, confirmed Tier/Firewall2개를 KNOWN_RESOURCE·exact type/id/generation/logical identity로 public Reconcile |
| 정상 삭제 | Firewall DELETE HTTP204 → 완전 inventory 부재 → Tier DELETE HTTP202/body202 → exact inventory 부재; public Cleanup SUCCEEDED/DELETED2 |
| 부재 후 별도 Cleanup | 위 exact 부재와 baseline 복원을 확인한 **뒤에** 같은 두 owned refs에 별도 호출. 실제 HTTP404/body 숫자404를 받아 full inventory 재관측 후 SUCCEEDED/DELETED2 |
| 최종 | public Reconcile exact ABSENT2, 이번 소유 잔여0; NSM 전체 목록과 정책·서버 목록이 baseline과 동일 |

기본 Tier는 **DMZ·Private 두 개**다. NSM inventory에는 별도의 `external` 시스템 네트워크 entry도 조회되며, 이를 추가 기본 Tier나 시험 자원으로 세지 않는다. 기존 Private의 refId는 `prepareKTCloudNICs`의 사용하지 않는 두 번째 NIC 반환값에만 read-only 입력했다. VM을 붙이거나 기존 Tier를 수정·삭제하지 않았다. 최종 정책0·VM0이다.

## 실행·Mock·미실행 구분

- 이번 actual live: 실제 인증/SDK queries, 새 Tier·Firewall의 production 생성/매핑, public Reconcile/Cleanup, 부재 확인 뒤 실제 HTTP404 처리.
- 이번 local fixture: external helper의 deletion authority guard21개 leaf PASS. 19개 invalid profile에서 underlying HTTP DELETE0, 정상 partial/full2개 보존. gate-off compile은 SKIP, overlay vet PASS. 클라우드 fault 주입이 아니다.
- 기존 자동 검증: source943d와 현재 source350개 동일. [이전 회귀/전체 테스트·독립 audit](../2026-10-13-pr87-review/README.md)을 그대로 보존한다. 신규 소유권20/삭제60 오류 회귀는 httptest다.
- **HTTP200 본문404, 문자열404, incomplete pagination, foreign/reused ID, timeout·응답 유실·모순 mapping은 이번 actual API에서 유발하지 않았다.** 이전 회귀 검증 범위다. 실제 API는 부재 DELETE 두 종류 모두 HTTP404/body 숫자404를 반환했다.
- 새 VM/SSH/Reset/Control WSS는 이번에 실행하지 않았다. [기존 두 generation 실제 VM/Connector SSH Evidence](../2026-10-13-premerge/README.md)의 tested source는 `6a6fb4f97`이다. 수정본에서 모든 VM lifecycle을 새로 실행했다고 표현하지 않는다. SFTP/PTY 및 Backend/Browser C1도 이번 범위 밖이다.
- 표준 로컬 OpenStack 실환경 테스트는 사용자 지시대로 미실행했다. 이번 네트워크 시험은 전체 VM Provision 설정/쿼터/부팅/SSH 성공의 대체물이 아니다.

**실행 원장 한계(KT-E1/P2):** initial helper가 같은 지역 Reconcile 변수를 포인터로 저장하고 후속 결과로 재대입하여 `beforeCleanup`이 최종 ABSENT로 덮어써졌다. create 직후 present 검사는 실행 소스의 `pr87ReconcileCheck(...,true)`와 raw `CREATE_AND_PUBLIC_RECONCILE_PASSED` 단계(`01:48:59.8246212Z`)의 통과로 확인한다. 원장 `beforeCleanup` 자체를 present 스냅샷 증거로 쓰지 않는다. 첫 Cleanup 포인터도 후속 결과와 alias됐지만 이번에는 두 내용이 동일했다. 최종 exact ABSENT와 baseline 자료는 보존됐다. 원본 원장과 실제 실행 helper hash는 변경하지 않았다.

별도 `snapshot-fixed-overlay-test.go.txt`/`snapshot-fix.patch.txt`로 phase별 deep snapshot을 보완했고 local regression24개(기존guard21+present→absent/cleanup→UNKNOWN/SafeError isolation3)가 PASS했다. 이 고정 도구는 실제 KT 재실행하지 않았으며 유실된 원본 관측을 사후 복원한 기록도 아니다. 제품 코드의 결함·수정으로 분류하지 않는다.

## 같은 소스 재현

`live-overlay-test.go.txt`와 `live-runner.py.txt`는 실행 원본이다. 개인 protected config는 제공하지 않는다. 별도 안전한 checkout에서 tested source `905ed25d1f2e04022bb814b83b61fb8eaa490366`을 준비하고 다음을 수행한다.

1. 새 시험의 기록에는 snapshot이 보완된 `snapshot-fixed-overlay-test.go.txt`를 `.local/pr87-live-20261013/source/pr87_live_test.go`, runner를 `.local/pr87-live-20261013/run.py`로 복사한다. 기존 실제 실행본을 확인하려면 `live-overlay-test.go.txt`(hash fa22…)를 읽되 위 KT-E1 한계를 적용한다. 두 도구를 같은 tested source로 혼동하지 않는다. 제품 package에는 추가하지 않는다.
2. runner의 본인 Go 실행 경로 및 protected KT D1 `clouds.yaml` 경로를 설정한다. 기존 `ktcloud-d1` 설정에는 verified TLS와 실제 Project 인증이 필요하다. credentials를 Evidence나 콘솔에 출력하지 않는다.
3. runner의 사용하지 않는 `/24`와 기존 DMZ 내 source `/32`를 확인한다. 현재 계정 baseline에 같은 CIDR이 있거나 기존 원장이 있으면 시험은 변이 전에 중단된다. 새 시험마다 원장이 없는 새 output directory를 준비한다. 이전 원장을 덮어쓰지 않는다.
4. `python .local/pr87-live-20261013/run.py guard`, `compile`, `vet`를 실행한다. guard21개 PASS, compile은 의도한 live gate SKIP, vet exit0 확인 후 좁은 생성·정리 승인을 확보한다.
5. `python .local/pr87-live-20261013/run.py live`는 정확 HEAD·tracked diff0 확인, 환경변수 제거, independent mutation gate만 주입한다. Go test `-timeout=20m -overlay ... -count=1 -json -run '^TestPR87KTCloudProductionNetworkLifecycle$' ./internal/connector/provider/openstack`로 실행한다.
6. 작업8분 + 별도 ownership 확인2분 + fallback Cleanup5분보다 전체 제한20분이 길다. 이것은 **시험 도구** 제한이며 제품 timeout/retry를 바꾸지 않는다.
7. `live.jsonl` PASS/exit0뿐 아니라 원장의 exact ABSENT2·full baseline equality·confirmed refs와 실제 delete receipt를 확인한다. 불명확한 ID/정책 매핑/조회 실패에서는 이름으로 후보를 채택하거나 blind DELETE를 반복하지 않는다. 이런 경우 원장을 보존하고 수동 확인한다.

Test overlay JSON은 source와 가상 package 파일의 절대 경로 매핑이며 runner가 실행 host에서 생성한다. 실제 서비스 HTTP transport/TLS와 응답은 그대로 유지하고, NSM 허용 POST2개 및 검증된 ID의 DELETE만 추가 guard로 제한한다. 이 guard는 제품 기능으로 포함되지 않는다. 실행 도구의 crash-atomic recovery나 모든 provider eventual-consistency 상황을 보장하지 않는다.

## 파일·감사

- `ownership-ledger.json`: sanitized 실제 NSM receipt/pagination/job 상태, 최종 typed observations, Cleanup과 baseline. 초기 Reconcile snapshot은 KT-E1로 유실됐고 원본 그대로 보존했다.
- `snapshot-fixed.jsonl/receipt`, `snapshot-fixed-overlay-test.go.txt`, `snapshot-fix.patch.txt`: 기록 도구 수정과 local24 회귀; 실제 KT에 재실행한 source가 아님.
- `live.jsonl`, `live-receipt.json`, `readonly.jsonl`: 실제 Go 실행 원본과 source/overlay/runner 식별.
- `guard.jsonl` 및 compile/vet 기록: 시험 도구 검증. 초기 compile/timeout-fixed 기록도 덮어쓰지 않았다.
- `tested-source.json`: execution source350개, `preservation-check.json`: 기존 tracked/nonignored1183개 변경·유실0.
- `PRE_MERGE_AUDIT_RESULT.md`, `INDEPENDENT_REVIEW.md`: 수정 전 두 도구 P2와 해결·독립 실물/원본 검토·scope 한계.
- `artifact-manifest.json`: 공개 bundle 해시. `publication-scan.json`: credential/private-key exact-match와 일반 secret 패턴 검사.

최신 main fetch는 `f7a9a953`으로 동일했다. main push/merge, Jira 상태 변경, 지원팀 메시지 전송은 수행하지 않는다.
