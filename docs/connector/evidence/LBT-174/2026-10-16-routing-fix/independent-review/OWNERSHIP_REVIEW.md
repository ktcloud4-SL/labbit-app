# LBT-174 라우팅 수정 전 독립 설계 검토

> 역사적 1차 설계 메모다. 아래 초기 "세 옵션 빈값" 권고는 실제 Neutron API 원문과 맞지 않아 철회한다. API의 정확한 blankable 이름은 `router`, `classless-static-route` 두 개뿐이며 249 빈값은 미지원이다. [F174-RNET003](F174-RNET003.md)과 실제 wire 회귀 결과가 이를 정정한다. canonical 두 빈값만 적용한 실제 guest에서도 관리 default가 남아 renew 검증이 실패했으므로 이 문서는 최종 정책 수용 근거가 아니다. 현재는 실제 lease 원인 관측과 nonempty on-link 121 후보를 별도 검토 중이다.

검토일: 2026-10-16 KST. 구현과 분리된 reviewer. 저장소 HEAD/base `0051d8d6803fd447f7b690dd895a0a928c3c7302`, 수정 전 Go/module 502개 LF aggregate `a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51`. 이 메모는 소유권·지원 조건 검토이며 최종 전체 A–F 판정이 아니다. 제품 코드·공유 Evidence·클라우드·원격 상태를 변경하지 않았다.

## 최신 원문 직접 확인

[ssot-refresh.json](ssot-refresh.json)은 read-only Atlassian 응답 본문과 버전을 보존한다. Jira LBT-174 updated `2026-10-16T14:59:23.423+0900`, comments 0, 진행 중. D18 v5, D19 v7, D20 v7, 검증전략 v3, 플랫폼 v27, 결정완료 v11, 추가결정 v3을 직접 읽었다.

| 소유 원문 | 확인 내용 | 이번 구현에 주는 경계 |
| --- | --- | --- |
| [D18 v5](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) 결정 7·8, 네트워크 구조 | Internet ON/OFF는 LabInstance 전용 Router의 External Gateway로 제어. 관리망은 Connector의 각 VM SSH 전용이며 Internet OFF와 독립 | 공용 관리망을 Internet 우회 경로로 쓰거나 관리 SG egress를 여는 수정은 맞지 않는다. Lab NIC 경로와 관리 SSH 반환 경로를 함께 보존해야 한다. |
| [D19 v7](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7176194) 결정 2·5·6·7 | CreationSnapshot과 최종 StartupScript 본문·SHA 및 Internet 의도 불변. Reset preflight 실패 시 기존 환경 유지 | 임의 netplan/cloud-init 래퍼를 StartupScript에 덧붙이는 방식은 현재 계약과 맞지 않는다. 새 네트워크 지원 조건도 Reset 삭제 전에 검사해야 한다. |
| [플랫폼 v27](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6520866) P5 | application 설정 의미는 runtime 계약, 실제 배포·라우팅은 인프라 Git/Runbook 소유 | 배포 전제와 Provider가 생성하는 owned Port 정책을 구분해 runtime/README에 명시해야 한다. 실제 주소·비밀값은 복사하지 않는다. |
| [Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) | 실제 로컬 OpenStack Preview·세대/종료/보안 회귀, 자동 회귀와 관련 CI. 전체 실제 SaaS/Browser C3 및 KT 최종은 별도 | 이번 라우팅 수정은 사용자의 추가 요청에 따른 Provider/검증 범위 확장. Preview PASS만으로 확대된 라우팅·timeout·CI 전체를 충족 처리하면 안 된다. |

현재 확정 문서에는 “Management subnet gateway 금지”, 특정 guest route metric, DHCP 옵션 번호가 정해져 있지 않다. 따라서 이 중 어느 기술도 이미 확정된 요구사항이라고 주장해서는 안 된다. D18 목표를 실현하는 지원 조건과 구현을 Git owning SSOT에 함께 추가하는 것이 필요하다.

## 수정 전 확인된 결함과 판단

기존 **F174-NET001 = F174-N001 / Medium**을 유지한다. 수정 전 `provision.go:219–223`은 관리 Network ACTIVE/비어 있지 않은 Subnets만 검사하고, `topology.go:115–127`은 DHCP route 제어 없이 Management port를 생성한다. runtime `contract.yaml:592–598`도 route 지원 조건을 정하지 않는다. accepted Ubuntu 이미지에서 두 NIC의 default metric이 모두 100인 실제 배포는 관리 경로 선택 시 Internet ON의 TCP를 실패시킬 수 있었다. 새 VM role/source/tuple 관측과 exact host-route A/B에서 Mgmt 선택 실패, Lab 선택 성공을 확인했다. 이는 inherited 구성 의존 가용성 문제이며 최초 삭제된 VM의 단일 물리 원인이 소급 확정된 것은 아니다.

1. **gatewayless Mgmt를 새 지원 조건으로 검증**하는 방식은 dedicated 관리망을 준비할 수 있으면 가장 좁다. 다만 지금 로컬 Connector가 직접 연결된 provider-flat shared 관리망을 변경할 수 없고, 별도 test-owned overlay 관리망은 Connector에서 L2 도달하지 않는다. 이 방식만 적용하면 이번 현환경 해결이 되지 않는다. 공유 subnet을 수정하거나 중복 CIDR을 만드는 우회는 권하지 않는다.
2. **owned Management port의 IPv4 DHCP router / classless route 억제**는 현환경에서 소유권을 좁게 유지하는 합리적인 대안이다. Provider가 생성한 Port에만 적용하고 Lab Port·shared subnet·guest StartupScript는 건드리지 않는다. 별도 guest 파일/SSH 명령으로 임시 route를 고치는 구현보다 Reset·부팅·DHCP 갱신에 대한 의도를 선언적으로 보존할 수 있다. 최종 적정성은 아래 지원 조건과 실제 결과에 달려 있다.
3. **임의 guest route metric 변경**은 이미지별 네트워크 관리자·cloud-init 단계·DHCP 갱신·Connector return path 영향을 새로 소유하게 된다. 현재 범위에서는 권하지 않는다.

이 판단의 기술 근거는 공식 [Neutron 관리자 문서](https://docs.openstack.org/neutron/ussuri/admin/archives/use.html#advanced-networking-operations)의 port별 빈 DHCP 옵션 지원과 [Networking API extra_dhcp_opt 정의](https://docs.openstack.org/api-ref/network/v2/index.html#extra-dhcp-option-extra-dhcp-opt-extension)다. 공식 문서의 지원 설명이 특정 배포의 실제 적용을 증명하는 것은 아니다.

## per-port 방식의 필수 경계

- preflight에서 exact `extra_dhcp_opt` extension을 확인하고 조회 오류·미지원은 mutation 전에 실패시킨다. standard-Neutron 경로만 적용하며 KT D1 별도 구현/계약은 유지한다.
- 선택 가능한 Management Network/subnet의 ID·관계·ACTIVE, 단일 IPv4 DHCP 지원 조건을 검사한다. 한 subnet만 지원하거나 선택한 subnet ID를 Management Port `fixed_ips`에 명시해 선택을 일관되게 고정한다. 조회 결과를 Adapter의 공유 mutable 설정에 저장하면 동시 Provision/Reset 간 섞일 수 있으므로 operation-local 값으로 전달한다.
- 관리 CIDR은 유효하고 Lab CIDR과 겹치지 않아야 한다. `SSHAllowedCIDR`은 전체 prefix가 관리 subnet 안에 있어야 직접 SSH 반환 경로를 설명할 수 있다. 첫 주소만 Contains하는 검사로 더 넓은 CIDR을 허용하면 안 된다. 이 값은 실제 Connector 네트워크 인터페이스의 증명은 아니므로 설치 시 직접 도달 가능성도 확인한다.
- 이 방식은 routed Connector·Management host routes·IPv6 RA/SLAAC/default·guest static route 의존 구성을 지원한다고 주장하지 않는다. route 목록을 모두 비우므로 off-subnet SSH나 기존 specific route가 필요한 구성을 조용히 받아들이지 말고 지원 조건으로 거절한다.
- Gophercloud v2.15.0 `CreateExtraDHCPOpt.OptValue`는 `required:true` string이므로 empty 문자열을 직렬화하는 좁은 create builder가 필요하다. 옵션을 null로 바꾸거나 생략하면 의도가 같지 않다.
- 관리 Port에만 IPv4 세 옵션을 **exact 빈 문자열**로 전송하고 생성 응답/후속 GET에서 확인한다. 누락·null·중복·잘못된 IP version·비어 있지 않은 값은 성공으로 처리하지 않는다. 이름/번호를 provider가 변환한다면 허용하는 등가 표현을 명시적으로 비교한다.
- 생성 후 정책 검증 실패에도 confirmed Port ID는 caller/ProviderResource 목록에 남겨야 한다. 현재 `prepareProvisionNICs`는 error 확인 후 upsert하므로 새 post-create error를 추가하면 이 경계를 함께 고쳐야 한다. 외부 API 응답 유실은 기존 UNKNOWN/reconciliation 의미를 보존한다. 이름으로 발견한 기존 candidate Port를 owned로 채택하거나 임의 수정하지 않는다.
- shared Network/subnet/Management SG/keypair는 Lab lifecycle의 삭제/수정 대상이 아니다. 기존 직접 SSH ingress와 egress 0 보안을 유지한다.
- Neutron agent는 metadata 및 subnet host route도 classless 옵션에 넣을 수 있다. Management의 121/249 억제는 default 하나만 지우는 행위가 아니다. Lab NIC의 metadata 경로와 실제 cloud-init/키 주입 성공을 ON/OFF 각각 확인해야 한다. [공식 dnsmasq 구현](https://github.com/openstack/neutron/blob/master/neutron/agent/linux/dhcp.py#L1156-L1240) 참조.
- extension 존재나 API echo만으로 모든 driver에서 DHCP 적용이 보장되지는 않는다. [OVN 구현](https://github.com/openstack/neutron/blob/master/neutron/common/ovn/utils.py#L351-L428)에도 지원 옵션 mapping/무시 경계가 있다. runtime/운영 문서에는 실제 검증한 DHCP backend·이미지 조건과 지원 범위를 적어야 한다.

## 의미 있는 자동·실제 회귀

| 경계 | 필요한 검증 |
| --- | --- |
| preflight | extension 없음/조회 실패/취소, DHCP off/IPv6·복수 subnet/관계 불일치/잘못된 CIDR/overlap/off-subnet SSH/host-route 의존 입력을 mutation 0회로 거절. Reset에서는 기존 resource DELETE 0회 확인 |
| Provider Port | HTTP request 실제 JSON의 빈 문자열 3개와 Lab Port 무변경. 생성 응답 정책 축약/변조 시 ID 보존·실패, 후속 GET drift, duplicate candidate 소유권 거절 |
| routing lifecycle | accepted 이미지의 fresh ON/OFF 부팅, DHCP renew 및 Reset 신세대에서 Mgmt default 없음; ON Internet exact source/tuple Lab 성공, OFF 외부 실패; cloud-init·SSH·PTY·SFTP·Preview 유지 |
| 안전/정리 | shared 관리망/SG/keypair 전후 원문 설정 동일, 자기 Port만 정책 보유. 실패/timeout 포함 tracked+보조 자원 exact absence, child exit0와 단계 전체 PASS |
| 미지원 배포 | IPv6·static guest routes·routed Connector·다른 DHCP backend는 단위 입력 거절 또는 명확한 미검증/미지원 조건. 로컬 PASS를 KT/SaaS/Browser/remote CI로 확대하지 않음 |

이 메모는 구현 전 설계 검토다. 새 diff, timeout helper, 실제 실행 소스와 Evidence를 받은 후 전체 A–F 및 최종 판정을 다시 수행한다.

