# Connector 운영 복구와 설정 변경 이력

2026년 10월 28일 운영 Connector에는 마지막 병합본 이후의 SSH 연결 관리·Control 수신·종료 알림·CIDR 반환 수정이 실행 중이다. 강사와 학생의 새 VM에서 첫 웹터미널 접속과 파일 Tree·Read·Save를 확인하고 공식 Cleanup까지 마쳤다. 이 기록은 해당 운영 변경을 재현하고 검토하기 위한 것이다. Runtime 설정의 정의와 필수 조건은 [Runtime Contract](../../runtime/contract.yaml), 프로토콜은 [Connector 계약](../../contracts/connector/README.md)을 따른다.

## 병합본과 운영본의 차이

비교 기준 main은 `aaa99d80c434910357007882bdbc614de8f1109d`이다. 마지막 Connector 변경 병합은 [PR 155](https://github.com/ktcloud4-SL/labbit-app/pull/155)의 `ff3d0cf8022c6c242d019dfe6bae10d95446db06`이며, 그 사이 Connector·설치기·Runtime 파일에는 추가 변경이 없다. 이번 PR의 기존 변경은 제품 코드 9개, 테스트 9개, 계약·설명 3개다. 전체 파일과 canonical LF 해시는 [source manifest](evidence/LBT-147/2026-10-28-runtime-recovery/source-manifest.json)에 있다.

| 변경 | 운영에서 달라지는 행동 |
| --- | --- |
| 공유 SSH 연결 생성 | 첫 요청이 취소돼도 같은 VM에 남아 있는 요청의 SSH 협상을 취소하지 않는다. Manager의 유한한 연결 시간 한도를 유지하며 마지막 대기자 취소·generation 변경은 연결을 정리한다. |
| 안전한 오류 분류 | 취소·timeout 변환 뒤에도 연결 단계와 원인을 보존한다. SSH identification·key exchange·host verification·authentication 진행 지점, 소요시간, context 종료를 구분한다. 파일 요청의 오류 변환 전에도 같은 관측 경계를 사용한다. |
| Control 수신 | 오래 걸리는 Provider 작업 중에도 READY VM의 Terminal·File 요청을 수신한다. 내부 FIFO는 64개로 제한하고 기존 Provider 직렬 실행을 전역 slot으로 보존한다. |
| Terminal 종료 알림 | reader 종료는 read deadline으로 해제해 write 경로를 보존한다. Connector 종료 시 `TERMINAL_ENDED` 전송을 socket 전체 종료가 먼저 막지 않도록 한다. |
| CIDR 자동 반환 | Cleanup과 엄격한 부재 확인을 마친 예약만 반환한다. completion·retired 기록으로 이전 소유자의 지연 명령을 차단한다. |
| 파일 기능 설정 | main에 이미 있는 PR 155의 helper 자동 설치를 활성화하도록 기존 Runtime key 두 개를 설정했다. 이번 PR에서 파일 프로토콜이나 helper 소스를 새로 구현한 것은 아니다. |

Control FIFO는 SaaS의 충돌 요청을 기다리게 하는 제품 기능이 아니다. 같은 LabInstance를 대상으로 하는 충돌 mutation은 기존 HTTP `409 operation_in_progress`와 DB 제약으로 거절한다. 끊긴 reader의 미수락 명령은 폐기하고 이미 수락한 작업만 application context에서 계속한다. 종료 시에는 그 context도 취소한다. 명령이나 mutation을 자동 재전송하지 않는다.

## CIDR 계약 변경과 복귀 조건

기존 계약의 “Cleanup도 예약을 자동 반환하지 않음”을 조건부 반환으로 변경했다. 반환에는 해당 소유자의 모든 generation에 대한 확정 completion, 각 SERVER의 같은 generation root-volume 참조, 기록된 exact resource ID의 실제 부재, 완전한 Tier inventory에서 두 CIDR의 부재가 모두 필요하다. 삭제 후보는 SaaS가 전달한 소유 exact ID 그대로다. completion에만 있는 ID는 부재 확인에 사용하며 추가 삭제 대상으로 채택하지 않는다. DB의 ProviderResource 소유권을 로컬 원장이 대체하지 않는다.

할당과 반환은 같은 독점 LOCK을 사용한다. immutable retired 기록을 fsync한 뒤 active 원장을 제거하며, 중간 상태·오래된 LOCK·UNKNOWN·legacy intent·root-volume 누락·불완전 inventory는 예약을 보존하거나 reconciliation이 필요한 HOLD로 처리한다. Reset의 이전 자원 삭제는 예약을 반환하지 않는다. 파일·이력 크기 제한을 초과해도 임의 잘라내기 대신 HOLD한다.

구버전은 새 completion·retired 형식을 만나면 안전하게 HOLD할 수 있다. **운영 이력이 쌓인 상태의 binary 교체만으로 downgrade가 완결되지 않는다.** 복귀가 필요하면 writer를 중지하고 원장을 보존한 채 DB·Provider exact ID·Tier inventory를 대조한다. 원장을 삭제해 구버전을 기동하거나 stale LOCK을 자동 인수하는 절차는 사용하지 않는다. legacy 예약은 자동 migration 대상이 아니다.

## 운영 설정 변경 이력

다음 값은 해당 배포의 이력이다. 새 환경의 기본값이나 공용 재사용 설정이 아니다. Connector·Provider ID, 프로젝트와 Tier, credential·SSH key·known_hosts는 환경별로 준비해야 한다.

| 날짜 | 변경과 이유 | 적용 범위 |
| --- | --- | --- |
| 10월 21일 | SaaS endpoint를 `https://jinwook.store`에서 `https://labbit.jinnythecloud.cloud`로 전환. AWS 환경의 credential·known_hosts·state를 분리하고 systemd EnvironmentFile을 선택. 초기 CIDR pool은 `172.25.232.0/24`부터 `172.25.239.0/24`까지 4쌍. | 기존 sandbox 설정을 별도로 보존한 운영 전환. |
| 10월 21일 | 기존 시험 VM의 SSH·cloud-init·PTY를 확인한 뒤 host pin을 보강하고 DB의 상태 3행을 수동 복구. | 당시 시험 데이터의 일회성 복구. 제품 READY 판정 로직이나 권장 운영 경로가 아니다. 아래 새 VM 검증은 정상 Provision readiness를 사용했다. |
| 10월 22일 | `LABBIT_WORKSPACE_ROOT`, `LABBIT_WORKSPACE_SFTP_HELPER` 추가 후 기존 VM에 helper·workspace 수동 준비. | 당시 이미지 수동 준비 방식. 이후 10월 23일 PR 155에서 자동 설치 코드가 병합됐다. |
| 10월 27일 | 기존 Connector identity 삭제에 따른 인증 실패를 공식 재등록으로 복구. 별도 credential 파일을 사용하고 기존 상태는 보존. | SaaS 등록·배포 설정 변경. 인증 우회나 자동 재등록 기능 추가가 아니다. |
| 10월 27일 | 새 Provider 연결에 Ubuntu 24.04·최소 boot 50 GiB·2 vCPU/4 GiB flavor를 매핑. state 디렉터리와 pool을 `172.25.224.0/24`부터 `172.25.231.0/24`의 4쌍으로 분리. | 이전 예약 `232–239`, 검토 후보 `240–247`과 겹치지 않는 전용 범위 선택. |
| 10월 27일 | main `aaa99d8` binary 배포. | 설정·상태 55개 파일을 유지했고 이 배포 자체는 env를 변경하지 않았다. |
| 10월 28일 | CIDR 반환 수정 배포. DB·Cloud 부재를 확인하고 writer를 멈춘 상태에서 과거 legacy 예약을 별도 보관. | legacy 정리는 일회성 유지보수였다. 그 정리 때문에 Cloud DELETE나 DB UPDATE를 실행한 것은 아니다. |
| 10월 28일 | Terminal 수정 소스를 순차 검증한 후 아래 고정된 binary 적용. | SSH 시간 한도 연장이나 자동 retry가 아닌 연결 소유권·수신·알림·진단 수정. |
| 10월 28일 | helper source·SHA 설정 누락을 발견해 두 key 추가. 새 VM 자동 bootstrap 후 파일 기능을 실측. | 기존 VM에 소급 설치하지 않았다. root·helper 경로만 있으면 자동 설치가 켜지는 것이 아니다. |

마지막 main 배포 직후 env와 최종 env의 byte 비교에서는 아래 두 줄 추가만 확인됐다. 제거·다른 값 변경은 없다. 전체 env나 credential은 저장소에 넣지 않는다.

```dotenv
LABBIT_WORKSPACE_SFTP_HELPER_SOURCE_FILE=/opt/labbit/releases/workspace-helper-5a31b4536940/labbit-workspace-sftp
LABBIT_WORKSPACE_SFTP_HELPER_SHA256=5a31b45369409b0420f4a3c3611b7e06eb65b315e3e52b5345c6607ad7f65959
```

## 현재 배포 확인과 재현

2026년 10월 28일 검증 당시 binary SHA256은 `52ec09329b837c27618fc579aba5c7e87c40b12618f08d94cabc4e0c39c51c32`, 버전은 `terminal-diagnose-21f07ea74db5`다. 표기된 `aaa99d8…+diagnostic-21f07ea74db5`는 base와 미커밋 소스 해시를 조합한 운영 빌드 식별자이며 Git commit SHA가 아니다. 이 PR의 제품 소스와 해시가 일치한다.

적용된 env는 `/etc/labbit/connector/aws-seoul-20261027/runtime.env`, systemd 선택은 `/etc/systemd/system/labbit-connector.service.d/10-aws-seoul.conf`다. CIDR state는 `/var/lib/labbit-connector/aws-seoul-20261027-pool`, Workspace root는 `/var/lib/labbit-workspaces/workspace`, guest helper는 `/usr/local/libexec/labbit-workspace-sftp`다. credential·cloud config·개인키 파일은 기존 보호된 로컬 참조를 유지한다. known_hosts는 기존 검증된 전용 파일을 보존한다.

새 환경에서는 [Workspace 자동 준비 지침](../../runtime/README.md)을 따라 같은 배포 소스로 Linux helper를 빌드하고 SHA256으로 고정한다. 보호된 Connector 로컬 source 경로에 설치한 다음 root·guest helper 경로와 source·SHA를 함께 설정한다. source 파일 불가·해시 불일치는 Provision 전에 실패해야 한다. 해당 운영 helper SHA를 다른 소스 빌드에 그대로 복사하지 않는다. 변경 전 보호된 env·binary·원장을 백업하고, restart 후 실제 선택된 EnvironmentFile·버전·WSS 연결을 확인한다. 기존 VM은 자동으로 보정되지 않으므로 정상 Provision/Reset 또는 별도 승인된 유지보수가 필요하다.

실환경 재현은 정상 UI에서 강사·학생 1명씩 Provision하고 READY 후 각 Workspace를 처음 열어 Tree 성공과 첫 Terminal 명령 실행을 확인한다. 원래 Terminal을 유지한 채 두 번째 정상 Workspace 탭에서 통제된 시험 파일을 Tree·Read·Save하고, 원래 Terminal에서 저장 결과와 채널 생존을 확인한다. 공식 Cleanup으로 종료 알림·Operation·exact-ID 부재·공유 baseline·예약 반환을 대조한다. 유료 VM 생성·재시작은 해당 환경의 승인 범위를 따른다.

## 확인 결과와 남은 범위

최종 새 VM 두 대의 첫 웹터미널 attach는 강사 약 0.77초, 학생 약 0.72초였으며 refresh·수동 SSH 사전 접속·재시도 없이 명령 실행에 성공했다. 파일 Tree·Read·Save와 원래 PTY 유지도 두 역할 모두 통과했다. 정상 UI의 File Tree가 Terminal보다 먼저 실행되므로 “Terminal이 최초의 underlying SSH handshake를 시작했다”는 주장까지 포함하지 않는다. 별도 직전 시험에서는 다른 VM Provision 중 READY 학생 Terminal 접속과 Cleanup의 `LAB_CLEANUP` 알림도 확인했다.

각 시험은 공식 Cleanup을 완료했고 마지막 시험은 생성부터 정리 완료까지 약 11분 38초였다. 소유 자원 10개 DELETED, 원래 Cloud baseline 일치, CIDR 4쌍 사용 가능을 확인했다. [검증 기록과 한계](evidence/LBT-147/2026-10-28-runtime-recovery/README.md)에 자동·fixture·실환경 범위를 구분했다.

과거 강사 접속의 약 10초 timeout은 당시 상세 단계가 남지 않아 SSH 협상 지연과 요청 취소 중 정확한 유발 원인을 소급 확정하지 못했다. 공유 dial 취소 결함은 회귀로 재현·수정했고 현재 첫 접속은 실측했지만, 그 결함이 과거 장애의 유일한 원인이었다고 단정하지 않는다. 새 진단 로그는 동일 증상 재발 시 단계와 context 종료를 구분하기 위한 것이다.
