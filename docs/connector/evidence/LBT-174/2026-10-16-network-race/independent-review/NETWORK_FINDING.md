# F174-NET001 / Medium — 두 DHCP default 경로에서 Internet ON 가용성 보장 공백

수정 전 독립 보고다. 기준 revision은 `0051d8d6803fd447f7b690dd895a0a928c3c7302` + a7d502이며 제품 수정은 하지 않았다. 구현자에게 2026-10-16 실험 중 전달했다.

| 항목 | 근거·판정 |
| --- | --- |
| 분류 | 확인된 기존 구성 의존 가용성 한계 및 배포 계약 공백. LBT-174가 도입한 회귀나 특정 코드 버그·수정 책임을 확정한 finding은 아니다. |
| 관련 정확한 위치 | `internal/connector/provider/openstack/provision.go:219–223` Management network/SG 수락, `topology.go:127` Lab+Management NIC 제공, `runtime/contract.yaml:592–598` 입력 및 Management SG egress0. 해당 라인의 신규 결함으로 단정하지 않는다. |
| 재현 조건 | 현재 수락하는 Ubuntu image·Management DHCP network로 Internet ON VM 생성. Lab/Management default가 모두 metric100. 원격 startup readiness/cloud-init 성공 뒤 고정 외부 HTTPS target에 단일 socket 연결. |
| 관측 | v1 첫12 probe 전부 원격 exec exit1·고정 Python assertion·connectCode11/약5초. default role 순서 Management→Lab. exact target /32를 Management에 고정하면 실패하고 Lab에 고정하면 성공. |
| 인과 확인의 범위 | v2는 socket source와 exact TCP tuple route를 role로만 수집했다. Management 고정 source/tuple Management 및 실패, Lab 고정 source/tuple Lab 및 성공. 전체 경로 선택을 바꾼 차이이며 실제 SG packet drop과 NAT/상위 경로의 개별 원인까지 분리 관측하지 않았다. |
| 원본 최초실패의 한계 | 과거 broad run의 VM은 삭제되어 원래 socket/route/stage 자료가 없다. v1 첫 snapshot이 Lab인데 실패한 경우도 실제 source/tuple 미수집이다. 따라서 역사적 최초실패 전부의 동일 단일 원인이라고 확정하지 않는다. |
| 사용자 영향 | Router가 생성되고 startup-ready인 Internet ON VM도 guest route 선택에 따라 일반 outbound 연결이 실패할 수 있다. 가용성 문제이며 Management 보안 우회나 타 Lab 유출을 관측한 것은 아니다. |
| owning SSOT | [D18 v5 결정7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730/D-18+VM)은 ON이면 Lab 전용 Router로 outbound 경로를 제공한다. Git은 Mgmt SG egress0을 강제하지만 DHCP default/guest route 우선순위·지원 image/배포 준비 책임을 정의하지 않는다. |
| 필요한 후속 | 지원 image·Management network 준비조건과 guest route/image/Platform 책임을 owning SSOT에서 결정한 뒤 그 경계에 맞춘 보완을 선택한다. fixture에서 Lab 경로를 임시 고정한 PASS로 일반 ON 가용성을 완료 처리하지 않는다. |
| 필요한 회귀 | 두 NIC DHCP default 설치 순서·동률/명시 metric에 대해 선택한 정책의 ON reachable/OFF blocked, 관리 SSH·PTY/SFTP/Preview 유지, Reset 후 동일성, Mgmt egress0·타 Lab 격리, 실패 관측/cleanup. 입력 제약을 새로 정한다면 생성 전 거절도 검증한다. |

현재 진단 요청에 제품 정책 변경·공유 Management subnet 수정·Management egress 허용·immutable StartupScript 변형을 포함시키지 않았다. 새 진단을 마치는 것과 Internet ON 정상 보장 또는 무조건적인 PR/rollout 승인은 구분한다. 앞선 Preview 코드·로컬 검증 판정의 범위를 넘어 이 네트워크 한계를 숨기거나 완료로 바꾸지 않는다.
