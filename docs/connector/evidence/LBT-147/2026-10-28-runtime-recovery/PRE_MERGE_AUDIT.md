# Connector 변경 전체 사전 감사

검토일은 2026년 10월 28일이다. 사용자 요청은 마지막 병합 이후 운영 Connector의 모든 변경, CIDR 계약 변경과 운영 설정 이력을 PR로 제출하는 것이다. 대상은 `ktcloud4-SL/labbit-app`, 브랜치 `fix/LBT-147-terminal-timeout`, base와 검토 시작 HEAD는 `aaa99d80c434910357007882bdbc614de8f1109d`다. 최신 main을 fetch해 같은 SHA임을 확인했다. 마지막 Connector 병합 `ff3d0cf8`부터 base까지 관련 코드 차이는 없다.

## 전체 변경 검토

실행된 미커밋 제품 소스는 [21개 파일 manifest](source-manifest.json)의 canonical LF 해시로 고정했다. staged·unstaged·untracked를 모두 확인했다. 아래 21개에 운영 설명 문서와 이 Evidence 패키지를 추가한다. raw status의 일부 WSS 파일은 line ending·stat 차이만 있고 normalized 내용은 base와 같아 PR에 포함하지 않는다. ignored Node 의존성·실행 도구는 setup 결과이며 제품 변경이 아니다. 개인 출력 디렉터리에는 운영 접근 helper와 민감한 참조가 있어 그대로 제출하지 않는다.

| 파일 | 검토 목적과 영향 경계 |
| --- | --- |
| `internal/connector/app/app.go` | VMAccess failure observer 연결. 기존 app 수명과 안전한 logger 경계. |
| `internal/connector/app/control_error.go` | stage·kind·context·SSH checkpoint allowlist만 기록. raw 오류 금지. |
| `internal/connector/app/control_error_test.go` | 유효 target·checkpoint와 민감값 제외 검증. |
| `internal/connector/vmaccess/access.go` | Manager 소유 pending dial·유한 budget·대기자 reference·마지막 취소·generation 검증·연결 정리. |
| `internal/connector/vmaccess/diagnostic.go` | 원래 stage·context cause·SSH 단계 보존, 안전한 외부 오류 유지. |
| `internal/connector/vmaccess/tcp.go` | Preview TCP 호출자와 같은 acquire 진단 사용. target 검사 유지. |
| `internal/connector/vmaccess/cancellation_test.go` | socket deadline과 context timer 순서의 안전한 실패·정리 검증. |
| `internal/connector/vmaccess/handshake_diagnostic_test.go` | identification·key exchange·cancel 구분 및 peer text 비보존. |
| `internal/connector/vmaccess/shared_dial_test.go` | 첫/마지막 요청 취소·host-key blocking·최종 진단 cause 회귀. |
| `internal/connector/wss/handler.go` | bounded FIFO·Provider slot 직렬화·accepted context·reader retirement·종료 write 보존. |
| `internal/connector/wss/operation_responsiveness_test.go` | READY Terminal 응답성과 reconnect 시 미수락 작업 폐기 회귀. |
| `internal/connector/wss/reconnect_operation_test.go` | canceled reader가 종료 알림 write를 먼저 막지 않도록 검증 보완. |
| `internal/connector/provider/openstack/ktcloud_d1_cidr.go` | completion 저장·retired fencing·원장 파일 인식·할당 전 안전성. |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_cleanup.go` | exact-ID·모든 generation·root ref·Tier 부재 확인 후 원장 반환. |
| `internal/connector/provider/openstack/lifecycle.go` | public Cleanup만 반환 wrapper 사용, Reset 내부 삭제는 예약 유지. |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_test.go` | 기존 예약·Tier 격리·재사용 정책 기대값 보완. |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_cleanup_test.go` | UNKNOWN·legacy·stale·partial delete·restart·공유 LOCK 검증. |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_lifecycle_test.go` | public Provision→complete 5refs→Cleanup→새 owner 재사용. |
| `contracts/connector/README.md` | Provider 안전성 설명과 CIDR 조건부 반환·원장 보존 정합. wire schema 변경 없음. |
| `runtime/README.md` | Runtime pool 반환 조건·복귀 제약 설명. |
| `runtime/contract.yaml` | 자동 반환 금지에서 엄격한 조건부 반환으로 owning 계약 변경. |

변경 주변의 Reset·Cleanup 호출자, generation invalidation/retire/release, File 오류 변환, Terminal 종료·Supervisor 순서, SaaS mutation service·migration의 충돌 거절을 함께 확인했다. HTTP·JSON schema·Realtime 메시지·DB migration·helper 소스·의존성은 변경하지 않는다. 다른 담당자의 미병합 FE PR은 대상에 포함하지 않는다.

## 최신 요구사항 추적

Jira 본문·AC·최신 관련 댓글 및 아래 Confluence 원문을 10월 28일 직접 조회했다. 정확한 조회 초 단위 시각은 저장하지 않아 추정하지 않는다. [LBT-147](https://samsunglions.atlassian.net/browse/LBT-147)의 기존 완료 범위를 재개방하는 판정이 아니라 관련 장애의 후속 수정이다. 관련 [LBT-149](https://samsunglions.atlassian.net/browse/LBT-149), [150](https://samsunglions.atlassian.net/browse/LBT-150), [158](https://samsunglions.atlassian.net/browse/LBT-158), [179](https://samsunglions.atlassian.net/browse/LBT-179), [26](https://samsunglions.atlassian.net/browse/LBT-26)의 기존 완료 경계와 별도 미검증 범위도 확인했다. AGENTS·CONTRIBUTING·TESTING, owning Runtime·Connector, 관련 Realtime·HTTP·migration을 대조했다. Backend auth 책임·의존 방향은 바꾸지 않는다.

| 요구사항과 원문 버전 | 구현과 경계 | 자동 실행 근거 | 실제 실행과 공백 |
| --- | --- | --- | --- |
| LBT-147, [D-18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730): VM별 lazy SSH 공유, 독립 channel, 현재 target·generation, cancel·idle 정리 | `vmaccess/access.go:93,218,288,300,319,343`, `tcp.go:24` | shared_dial·cancellation 및 기존 manager/channel 회귀. Windows fresh unit·race PASS. | 같은 제품 해시의 두 역할 첫 Terminal 및 File와 원래 PTY 동시 유지 PASS. idle·stale·cancel 극단 조합은 fixture. |
| LBT-147·150: 안전한 handshake/cancel 진단, credential·raw 오류·파일 본문 비보존 | `diagnostic.go:127,145`, `access.go:556,567,577,647`, `app/control_error.go:27,34`, `app.go:379` | checkpoint·request cancel·peer text 비보존·safe log 회귀 PASS. | 과거 10초 원인은 불확정. 새 상세 단계 관측 준비를 확인하며 과거 원인 확정으로 확대하지 않음. |
| [D-20 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6881283): durable async·충돌 거절·엄격 READY, [D-24 v12](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749): reconciliation 전 blind mutation 금지 | `wss/handler.go:972`; 기존 `server/labexecution/mutation.go`와 `000004` DB unique 제약 유지 | responsiveness·EOF·reconnect·shutdown 회귀 PASS. FIFO는 미수락 frame에 한정하고 기존 backend409를 바꾸지 않음. | 실제 다른 VM Provision 중 READY 학생 Terminal 성공. 실제 UNKNOWN·lease 회복은 이번에 재주입하지 않음. |
| [D-21 v11](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6848516): Terminal/Live 수명·종료 metadata·본문 비저장 | `wss/handler.go:987`, 기존 app→Terminal→Supervisor 종료 순서 | SessionCancel·SupervisorShutdown 및 기존 Terminal 회귀 PASS. | 이전 최종 소스 시험의 연결 중 Cleanup UI `LAB_CLEANUP`·DB ENDED 확인. 최종 File 시험은 명시 종료 후 Cleanup. Live 정책 불변, 전체 Live 여정 재검증 제외. |
| LBT-158·179, D-20·24, 변경된 Runtime 계약: 정확한 소유권·불확실성 보존·조건부 CIDR 반환 | `ktcloud_d1_cidr.go:209,301`, `ktcloud_d1_cidr_cleanup.go:44,106`, `lifecycle.go:104` | Linux 동일 Provider 해시 180 PASS/14 gated skip. public lifecycle·stale/reset·root 누락·불완전 inventory·LOCK·partial release fixture PASS. | 실제 최종 두 VM Cleanup 10개 DELETED·exact baseline·4쌍 반환 확인. 실제 power loss/fsync fault 미실행. |
| LBT-149·150·26: 현재 Workspace VM Tree/Read/Save·shared PTY 독립성 | 이미 병합된 PR155 bootstrap와 기존 Runtime source·SHA key를 운영에서 활성화 | 기존 File·bootstrap·path/revision/security 회귀 unit. helper source는 이번 PR 변경 없음. | 최종 새 강사·학생 Tree·Read·Save 및 원래 PTY 유지 PASS. 과거 삭제 VM의 오류 세부 원인·모든 권한/크기 오류의 실제 Browser 재시험은 미포함. |

실제 검증은 [browser-cleanup.json](browser-cleanup.json)의 고정 제품 해시와 binary/helper/runtime 해시에 연결된다. 자동 실행·재사용·skip의 정확한 결과는 [validation.json](validation.json)에 분리했다. 현 시점 PR commit CI의 판정은 GitHub의 해당 HEAD에서 추가 확인한다.

## 수정 전 findings와 해결

아래는 앞선 구현 전에 보고·분석한 결함과 정책 변경이다. 이번 PR 포장 단계에서 새로 확인된 제품 결함은 없다.

| ID·심각도 | 기준 파일과 재현 조건 | 영향과 근거 | 수정과 회귀 |
| --- | --- | --- | --- |
| F1 P2 | base `vmaccess/access.go:133`부터 135행. typed dialErr를 `c.err=safeError(...)`로 대체 | 원래 typed diagnostic의 stage·elapsed가 유실됨. LBT147 safe diagnostic 경계. | 원래 typed diagnostic 보존·safe log 회귀. |
| F2 P1 | base `vmaccess/access.go:88` acquire. 첫 File/Terminal 요청 취소 중 같은 pending dial의 다른 요청이 기다림 | 첫 요청 context가 공용 협상을 취소해 생존 요청까지 실패. D18 channel/connection 수명 독립성. red/green으로 확인. | Manager budget·waiter/user 관리, first/last cancel 회귀와 race PASS. 과거 장애의 단일 원인이라는 판정은 유보. |
| F3 P2 | base `wss/handler.go:963` Listen. 한 VM Provider 작업이 block된 동안 다른 READY VM Terminal 요청 | 동기 수신 경로가 후속 Control 요청을 지연시킴. D20 durable 작업과 READY 접근 경계. | bounded FIFO·operationSlot·retirement 회귀. 실제 다른 VM Provision 중 접속 PASS. |
| F4 P2 | 변경 `wss/handler.go:987`. read cancellation에 socket 전체 close가 앞서면 종료 sender도 끊김 | shutdown의 Terminal 종료 알림 누락 가능. D21 lifecycle metadata. | read deadline만 해제, reconnect·shutdown 회귀 및 실제 Cleanup 알림. |
| F5 P2 정책 변경 | base `ktcloud_d1_cidr.go:345` 부근·Runtime 계약. 4쌍 사용 후 Cleanup해도 예약이 영구 보존됨 | 기존 계약에는 맞지만 새 시험 반복·반환 요구와 충돌. owning 계약 변경 필요. | 엄격한 반환 계약·implementation 동시 변경. public lifecycle·재시작·새 owner 회귀. |
| F6 P1 안전성 경계 | base Provision의 server 확인 후 root volume 미확정 실패. FAILED를 곧바로 완전한 completion으로 간주하는 설계 검토 | root orphan 가능성을 배제하지 못한 채 pair 반환 위험. 구현 전에 독립 검토에서 식별. | 동일 generation의 root ref 필수, missing-root 회귀. 실제 boot fault 주입 아님. |
| F7 P2 운영 설정 | 마지막 main 배포 runtime에 root·helper는 있으나 source·SHA 누락 | 이미지 수동 준비 모드로 실행되어 새 VM helper 준비를 보장하지 않음. 기존 PR155 Runtime 계약. | 두 key 추가·고정 artifact 배치. 새 두 VM Browser File·Terminal·공식 Cleanup PASS. 과거 VM의 정확한 File 오류를 소급 확정하지 않음. |
| D1 P3 문서 | 운영 변경 이력 초안 13·18행. LabExecution 단위 거절·기존 slot이라고 기술 | 실제 LabInstance 충돌 거절과 이번 slot 추가를 오해할 수 있음. 독립 리뷰에서 수정 전 보고. | LabInstance와 기존 직렬 실행 보존으로 보정. 제품 동작 변경이 없어 새 자동 테스트 불필요. |
| E1 P2 검증 기록 | `validation.json` 초안 337행, concurrency 실행의 sameReviewedSource·최종 test hash | 신규 public lifecycle 테스트 추가 전 179 PASS를 최종 전체 Provider 테스트 실행처럼 표시. 실제 final180 PASS 근거는 별도 존재. | 해당 실행은 제품 구현 동일·신규 테스트 미포함으로 범위를 보정하고 최종 test hash 제거. 원본 binary·결과 대조, 추가 제품 테스트 불필요. |
| D2 P3 감사 기록 | 이 문서 초안 56행, F1 위치를 Diagnose 함수로 기술 | 원래 typed diagnostic을 잃은 실제 위치가 아닌 함수로 잘못 안내. | base `access.go:133`부터 135행으로 보정하고 원래 safeError 대체를 설명. |

위 위치는 함수 시작 또는 문제 경계의 대표 위치다. F1·F4는 수정 진행 중 중간 형태까지 포함하고 base와 최종 변경 줄이 일대일 대응하지 않는다. 최종 고정 소스와 전체 diff의 검토 위치는 앞 표에 따로 적었다.

## 실패 보안 수명주기 분석

| 시나리오 | 검토 결과와 한계 |
| --- | --- |
| 취소·timeout·host-key 지연 | Caller와 shared dial 수명 분리, 마지막 취소 시 지연 성공 폐기, budget 유한. 실제 네트워크 장애 주입 대신 loopback/callback 회귀·race. |
| target·generation 교체 | lease 반환 전 재검증·invalidate·late channel close 유지. stale owner의 CIDR allocation/cleanup은 retired와 generation 검증으로 HOLD. |
| Provider 작업·Control 재접속 | 전역 slot 직렬화, 새 reader 소유 epoch 확인, 미수락 FIFO 폐기, 수락 mutation은 재연결만으로 취소하지 않음. FIFO overflow는 disconnect/reconcile 경계. 강제 packet loss 실환경 미실행. |
| 종료와 다른 channel | read만 해제해 ENDED write 경로 유지. shared SSH는 남은 users/waiters 존중. actual File Save 중 원래 PTY 지속 확인. |
| CIDR 부분실패·원장 corruption | incomplete completion/legacy/UNKNOWN/잘못된 파일/LOCK은 fail closed. deletion 결과와 예약 보존은 구분하며 deletion 성공만으로 부재를 확대하지 않음. |
| 리소스 소유권·식별자 재사용 | 삭제는 SaaS exact refs. completion의 추가 ID는 read-only 확인. tombstone 선행 fsync와 원장 삭제 후 directory sync. Reset 예약 유지. 실제 fsync 오류·전원 손실은 synthetic 중간 상태 fixture 이상으로 주장하지 않음. |
| 입력 크기·영속 이력 | 기존 envelope 제한, FIFO64·원장 크기 한도 유지. 누적 retired 이력의 운영 보관·구버전 복귀 시 reconciliation 필요. 자동 pruning·stale LOCK takeover 없음. |
| 보안·로그 | host-key·Workspace·generation 검증·권한 우회 없음. allowlisted 단계·시간·context만 추가. token/key/raw payload/PTY·파일 내용은 제출하지 않음. |
| schema·consumer | Runtime의 실제 CIDR 반환 계약을 함께 수정. HTTP·DB·WSS wire는 그대로이며 충돌409·durable operation·reconciliation 정책 유지. |

## 실행과 독립 검토

최종 제품 해시에서 Windows Go vet·unit·build 및 VMAccess/app/Terminal/WSS race를 fresh 실행했다. POSIX Provider·installer fixture는 해시가 같은 기존 Linux 결과를 재사용하고 PR Linux CI에서 전체 기본 검증을 추가 확인한다. Windows Web test 실패와 skip, 이전 fixture 실패·수정은 README 및 validation에 공개했다. DB integration은 로컬에서 새로 실행하지 않았고 PR CI가 해당 HEAD의 PostgreSQL 경로를 검증한다. 실제 VM 결과는 unit이나 Mock으로 대체하지 않았다.

구현과 분리된 reviewer `terminal_reviewer`가 현재 21개 전체 diff, 신규 테스트, 호출자·consumer, 최신 D18·20·21·24 원문, source hash를 다시 확인했다. 새 confirmed blocker는 없었다. 공개 문서·Evidence의 재현성·privacy·범위에 대한 최종 검토는 문서 고정 후 별도로 기록한다.

현 단계 판정은 **제품 코드 검토 통과, 문서·최종 CI 확인 중**이다. 최종 PR 검토 가능 판정은 문서 독립 검토와 해당 HEAD CI 후 갱신한다. 과거 timeout의 단일 원인 확정, 전체 장애 매트릭스 완료, merge·추가 배포 승인으로 확대하지 않는다.
