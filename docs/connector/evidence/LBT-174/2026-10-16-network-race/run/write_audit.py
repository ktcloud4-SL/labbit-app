from pathlib import Path
import json,datetime,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
DEST.mkdir(parents=True,exist_ok=True)
audit='''# LBT-174 Internet 원인 진단·native Linux tagged race 감사

2026-10-16. **진행 중 기록 — 최종 판정은 실행·독립검토 후 갱신한다.** 사용자 요청은 Internet ON 최초 실패 원인 확인과 특수 Linux race 검증이다. 새 제품 정책·공유망 변경은 하지 않는다. commit/push/PR/merge/배포/외부 메시지는 하지 않는다.

## A. 전체 diff·버전

저장소 labbit-app, branch `codex/feat/LBT-174-preview-local-integration`, base/HEAD `0051d8d6803fd447f7b690dd895a0a928c3c7302`. 모든 제품 변경은 여전히 미커밋 상태다. Go/module502파일 aggregate는 `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`. 이번 요청으로 repo Go·module·contract·runtime·DB·web·CI 소스 변경은 없다.

base부터 전체27 Go파일 변경은 이전 [전체 inventory](../2026-10-16-followup/code-inventory.json), [복원 patch](../2026-10-16-followup/full-code-diff.patch), [전체 A–F 감사](../2026-10-16-followup/AUDIT.md)를 그대로 적용한다. 파일별 목적·요구·상태/동시성/취소/보안/수명주기와 이전 findings 수정·자동테스트 결과도 동일하다. 새 Evidence가 이전 판정을 바꾸는 부분은 아래 Internet 구성 한계와 Linux 실제 race의 결과다. 기존 frozen followup 패키지는 수정하지 않고 checksum 전체를 다시 대조한다.

실제 host cloud runner는 **외부 관측 overlay**를 쓴다. `startup.go`의 오류 매핑·프로브 의미를 유지하며 안전한 stage·exit·검사 JSON만 관측한다. 이 host 실행을 overlay 없는 소스 실행으로 표시하지 않는다. native Linux에 보낸 아카이브에는 overlay가 없으며 a7d 실제 소스를 직접 검증·컴파일한다. [source](run/source-manifest.json), 각 `run-state.json`/`run-result.json`과 [아카이브 검토](independent-review/method-source-verification.json)를 따른다. 추적·미추적 전체 Go502 및 모듈이 기준이며 보호된 개인키/clouds.yaml은 포함하지 않는다.

## B. 최신 SSOT 추적성

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) updated `2026-10-16T14:59:23.423+0900`, 댓글0. 독립 reviewer가 Jira 원문과 D18v5/결정완료v11/테스트전략v3/D20v7/추가결정v3를 다시 직접 읽고 버전·전체 본문 동일성을 확인했다([refresh](independent-review/ssot-refresh.json)). repo AGENTS/CONTRIBUTING/TESTING, 관련 Connector/Realtime/HTTP/Runtime/DB/backend와 CI 적용범위는 이전 감사 전체표와 같다. 2026-10-16 16:57 KST git ls-remote의 read-only main 확인도0051d8d이며 로컬stage변경은0이다.

| 요구·원문 | 구현·자동 검증 | 실제 실행·증거 | 공백·판정 |
| --- | --- | --- | --- |
| LBT174 AC1–5 Preview forwarding/lifecycle/오류/Terminal·File 회귀/Evidence, D18/Connector§7b | 전체27diff와 기존 회귀, 이전 fullGo3753PASS/42pkg/18skip, race11pkg, web188, realPG1305, actual VM fixture | a7d와 동일 소스. 이전 감사 B/D와 각 raw·stagebridge를 상속 | 이전 PASS를 변경된 소스 PASS로 바꾸지 않음; 이번 새 제품 소스변경0 |
| D18 결정7·Connector README512–514: ON LabRouter outbound, OFF no external GW | `provision.go:119–137,219–223`; `topology.go:127`; `integration_test.go:387–424` | 같은 safeerror 형태 재현; 새진단에서 remote assert/code11 실패12회 확인, 새 VM Lab source12회 성공; target `/32` role A/B | **확인된 구성 의존 가용성 한계**, 최초 역사적 원인 단일 확정 불가 |
| runtime/contract.yaml595–598: Management SG ingress22 only/egress0 | `provision.go:265–290`, `integration_test.go:488–502` | 보안규칙을 유지한 상태에서 Management socket timeout/Lab socket 성공 | SG를 열어 우회하지 않음. guest route 정책·배포 지원조건 소유권 미정 |
| TESTING/CI 특수 Linux harness | `.github/workflows/go.yml:60–66`; app C1 12 parents + real PG/MockProvider | native Linux Go1.27.1/CGO1/GCC, 실제PG16, a7d502 source archive 검증. 최종 결과는 D표 갱신 | 원격 GitHub CI와 actual KT/SaaS/Browser 성공으로 확대 안 함 |
| Evidence/재현·정리·보안 | exacttracked cleanup + SG/keypair GET404; source/overlay/archive hashes | raw·실행metadata·부재marker·PG정리·독립review | 전역 cloud자원0·원래삭제VM 상세상태 복원 주장은 아님 |

## C. 실패·보안·수명주기

- 최초 ON은 Provision/cloud-init 성공 뒤 새 SSH exec 단계다. `startup.go:59–105`가 key/TCP/handshake/channel/remote exit 실패를 같은 readiness sentinel로 평탄화하므로 과거 `VM initialization did not complete`가 SSH 장애였다고 읽을 수 없다. 원래 VM은 삭제돼 errno·실제 socket source·route가 남지 않았다. reviewer 독립 loopback7모드×10(70child/10parent, race0)은 이 관측 한계를 재현한다.
- 새 진단은 target warm-up 없이 첫 socket을 관측한다. v1 첫12회 모두 `remote_exec`, exit1, exact assertion, errno11 약5초였다. route snapshot 중2회는 Lab을 가리켰지만 실제 socket source 미수집이라 그 두 실패까지 Management였다고 단정하지 않는다. v2/v3는 socket getsockname을 MAC→role로 치환하고 TCP5tuple route와 rp_filter를 관측한다. IP·MAC·key·raw SSH/provider error는 JSON에 출력하지 않는다.
- 같은 우선순위100의 Lab/Management default가 guest에 생성된다. v2에서는 12회 첫 socket source/tuple 모두 Lab·success였고, Management target hostroute를 강제하면 Management source/tuple·errno11, Lab 강제 시 source/tupleLab·code0이다. rp_filter는 두 NIC 모두2였다. **공존 route와 경로 의존 실패의 메커니즘을 확인한 것**이지 과거 최초 VM의 동일 원인을 증명한 것은 아니다.
- A/B는 target1.1.1.1/32 사전부재→exactadd→singleprobe→finallyexactdelete이며 replace/flush나 공유망 변경이 없다. v1/v2 restored/ownedRouteAbsent까지 확인한다. v3는 별도45초 deadline 초과로 parent가 실패했다. restored marker가 없으면 복원 완료로 계산하지 않는다. 최종 VM 폐기가 해당 disposable 경로의 회수 경계다.
- native 준비 중 apt는 ownedVM에서만 일시 Labdefaultmetric40을 사용한다. v1 raw에는 libc 업데이트의 needrestart가 networkd를 포함해 재시작한 기록이 있고, 이후 helper finallydelete 시점에는 경로가 이미 없어 NoSuchProcess로 exit1이 됐다. 재시작에 따른 경로 소실은 강한 추론이며 소실 주체·시각의 직접 관측은 없다. Linux CI 미진입 실패 원본을 보존한다. v3는 `NEEDRESTART_MODE=l`, exactroute 조회→있으면delete→absence assert를 쓰고 이 VM의 메모리961MiB를 보조하는 소유1GiBswap을 만든다. swap은 VM폐기로 회수한다. guest·공유망·제품의 영구 정책 변경이 아니다.
- 외부 phase timeout은 SSH session.Close 후 Run worker join을 보장하지 않는 helper 한계가 있다. timeout branch를 제품 SSH 생명주기 회귀검증으로 쓰지 않는다. nativeCI 정상 exec의 완료는 session.Run join/exit 후 로그를 읽어 판정하고, 최종 VM폐기/PGstop으로 회수 범위를 확인한다.
- Linux C1은 실제PG/WSS/Handler/Dispatcher지만 Provider는Mock이다. SSH reverseforward는 VM127.0.0.1 임시port→호스트127.0.0.1:15432뿐이다. 인증/hostpin을 유지하고 DB를 public interface에 열지 않는다. actual KT API·credential·SaaS/Browser를 사용하지 않는다.
- lifecycle/입력/ID재사용/epoch/cancel/백프레셔/동시성/재시도·reconcile/secret 분석은 이전 전체27diff C와 같다. 새 제품 상태·프로토콜·DB·영속 설정이 없으므로 별도 API/migration/재시작 정책 도입은 해당 없음이다.

## D. 실행·미실행·skip·Evidence

| stage | 실제 결과·구분 |
| --- | --- |
| 독립 alias probe | loopback SSH 7모드×10, 70child+10parentPASS/race0/exit0; safe error alias 증명. 실제 Internet 원인 아님 |
| v1 local-network-race | host goexit1,282.578초; Internet 첫12전부assertFAIL. A/B expectedPASS/restoredTrue. apt cleanup helper exit1로 Linux vet/build/race 미실행. tracked8+SG/keypair GET404, PGDB0/stop0. repo소스전후a7d |
| v2 probe-only | host goexit0,134.062초; Internet first12PASS sourceLab; forcedMgmt errno11, forcedLab code0, restoredTrue. Linux/PG는 해당stage에서 실행 안 함. tracked8+보조SG/keypair GET404, 소스전후a7d |
| v3 nativeLinux | 진행 중. A/B45초 timeout 때문에 hostparent는 실패 상태다. nativeCI의 실제 자식 결과·exact12 parents/skip/race는 별도 집계한다 |
| 기존 기본·race·web·realPG·실VM | 소스불변으로 이전 frozen followup 결과를 적용한다. 이번 추가실행으로 가장하지 않는다. Linux직접검증은 기존 crosscompile/nonrace 제한을 그 실행범위에서 갱신한다 |
| 미실행 | 원격GitHub CI, actual KT 최종/actual SaaS·BrowserC3, 규모·장기soak·hardcrash. 최초삭제VM 실제5tuple/원래실패stage 직접확인 불가 |

각 요약은 원본 JSONL에서 재계산한다. wrapper exit와 실제go child exit를 구별하고 초기실패를 숨기지 않는다. archive에는 tracked+Go1,237파일과 vendor1,923파일, migrationSQL7이 있으며 reviewer가 현 checkout/외부vendor 전체bytes와 독립대조했다. officialLinuxGo metadata/SHA도 대조했다. 공식도구다운로드는 [Go downloads](https://go.dev/dl/)의 고정1.27.1 artifact다. [재현절차](REPRODUCE.md)를 따른다.

## E. findings·한계·필요 회귀

| ID·심각도 | 파일/줄·재현·영향·근거 | 처리·회귀·현재 상태 |
| --- | --- | --- |
| F174-N001 Medium, 기존 구성 의존 가용성 한계/배포 계약 공백 | a7d `provision.go:219–223`, `topology.go:127`, runtime595–598. accepted Ubuntu+MgmtDHCP가 default2개 metric100을 생성. v1 first12remoteassertFAIL와 v2 실제source/tuple별A/B로 Management선택시차단/Lab선택시성공 확인. D18 ON outbound 기대에 대한 가용성 제한 | 이번 진단에서 제품 버그의 특정 수정 책임이나 LBT174 신규회귀로 단정하지 않음. SGegress0 유지. image/guest/Platform 소유권·Management DHCP gateway 지원조건·Lab route 우선정책 확정 및 firstsocket 두 DHCP순서/ONOFF/SSH보존 회귀가 후속 필요. **미해결 한계** |
| OBS001, 원래 실패 관측 제한 | `startup.go:59–105`, original integration406. SSH/remoteexit 모두 같은 safe error, 삭제VM firsterrno/route없음 | 외부진단에서 실패stage/exit/errno/socketrole 추가. 과거 원인을 소급 확정했다고 표시하지 않음; 별도 제품 오류 공개계약 변경 없음 |
| METHOD001, 외부 준비 helper 실패 | v1 `local_probe_test.go` aptfinallydelete. networkd재시작기록 후 exactroute이미부재→deleteexit1, Linux미진입. 재시작이 route를 지운 주체라는 부분은 추론 | v1raw·실패보존; v3 list-onlyrestart/exactabsencecheck로 준비 재시도. 제품 수정/원래Internet원인으로 혼동 안 함 |
| F174-NDOC001 Low, Evidence 표현 | 감사초안31/55줄이 networkd재시작→route소실 단일인과를 확정해 원본 관측보다 강하게 표현. 독립reviewer가 raw대조 재현 | 사용자에게 수정 전 보고 후 관측과 추론을 구분하도록 두 위치 수정. 제품 finding 아님 |
| METHOD002, 외부 A/B timeout | v3 `local_probe_test.go:131–140`,45초초과·Runjoin없음·restoredmarker부재 | 실패보존·hostfixture전체PASS금지, ownedVM폐기경계. 두번째독립정상A/B(v2)와 nativeCI 실제성공을 구분 |

새 제품 코드는 수정하지 않았다. 확인된 구성 한계와 수정 방향을 사용자에게 먼저 보고했으며 보안 완화·공유망 mutation·사용자 immutable StartupScript 변형을 하지 않는다. 위 METHOD001은 진단helper 재시도 과정의 오류이고, helper 수정의 구체적 코드 finding 사전보고는 진행 update 뒤에 자세히 정리됐다는 절차상 한계도 남긴다. 기존27diff의 이전 findings 해결상태는 이전 E표와 같다.

## F. 독립 검토·판정

구현과 분리된 `/root/review_lbt173`가 최신SSOT와 전체27diff 유지여부, 초기rawtimeline, safeerroralias 실제race, tar1,237+vendor1,923/Go502, overlay·officialGo identity, A/B 해석과 한계, cleanup·보안을 직접검토한다. reviewer는 이 cloud를 조작하지 않았다. 최신 상세는 [초기분석](independent-review/README.md), [방법검토](independent-review/METHOD_REVIEW.md), 최종 addendum을 따른다. 동일 에이전트의 두번째패스로 가장하지 않는다.

최종 판정은 pending이다. nativeLinux 성공을 원격 CI나 전체 LBT174 완료로 바꾸지 않는다. 확인된Internet구성한계와 역사적원인미확정은 잔여위험이며 무조건 PR가능 판정을 하지 않는다. 이번 요청은 push/PR 승인도 아니다.
'''
(DEST/'AUDIT.md').write_text(audit,encoding='utf8')
print('draft audit A-F written; pending live results and independent final review',flush=True)
