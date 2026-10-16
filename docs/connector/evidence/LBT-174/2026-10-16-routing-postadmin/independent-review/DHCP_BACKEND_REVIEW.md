# Post-admin DHCP 경계 독립 검토

현재 source 520 / `86ec2c865b611a512eab3baabed7d238a102765d5095fd0c92a36372aba5b791`의 actual 라우팅 판정은 **보완 필요**다. 서비스가 Stopped/Disabled인 새 실행에서도 TCP는 Lab 경로로 성공했지만 Management 기본 경로가 남아 strict gate가 실패했다. VMware DHCP 중지는 확인된 경쟁 DHCP 조건을 제거한 조치이며, 현재 제품 정책의 충분조건이 입증된 것은 아니다.

## 실제 관측과 제한

- 첫 12회와 timeout 후 fresh 진단 모두 `connectCode=0`, Lab source/tuple path, Lab + Management metric 100 기본 경로 두 개다. test 출력의 `first socket failed`라는 문구로 TCP 자체가 실패했다고 해석하면 안 된다.
- intentional remote alarm 단계는 exit 0 / 1,442 ms / joined=true다. 뒤 단계의 실패는 같은 dual-default assertion이다. 이 실행에서 진단 goroutine이 멈췄다는 근거는 없다.
- 별도 lease 관측에서 Management SERVER_ADDRESS는 조회한 Neutron DHCP port 주소와 일치했다. 그 lease에 ROUTER가 있고 ROUTES 필드는 없었다. API에는 canonical router/classless-static-route의 명시적 빈 문자열 두 개가 보존돼 있었다.
- 이 관측의 netplan 출력은 bytes를 `json.dumps`에 넣은 TypeError 때문에 실행되지 않았다. 예외 전에 출력된 lease 사실은 사용할 수 있으나 observer 전체 PASS나 netplan 확인으로 표기할 수 없다.
- DHCP/Open vSwitch/L3/Metadata agent alive 관측은 dnsmasq 기반 DHCP agent 경로의 증거다. 실제 설치된 Neutron/dnsmasq 버전, 생성 host/opts 파일, wire ACK는 아직 직접 확인하지 않았다. agent 목록만으로 모든 network의 backend를 단정하지 않는다.
- strict probe와 lease observer 모두 exact tracked 8개 및 보조 SG/keypair GET absence 확인 로그가 있다. 첫 strict probe는 A/B·renew·reboot·Reset 단계 이전에 종료됐다.

## 공식 코드 대조

Neutron upstream commit `4538334273e79b520e193dfece7261e13183b751`를 고정해 함수 AST를 분리 실행했다. 실제 daemon 실행이 아니라 최소 dependency stub을 둔 원문 함수 검증이다. 4개 소스 SHA·URL·함수 행 범위와 결과는 [upstream-dhcp-observations.json](upstream-dhcp-observations.json), 재현은 [review_dhcp_upstream.py](review_dhcp_upstream.py)에 있다.

**OVN:** 빈 classless 값은 `{}`로 변환되고 port/subnet map 병합 경로는 subnet metadata/default 항목을 유지한다. onlink-only port map을 추가해도 그 병합 경로의 subnet default는 사라지지 않는다. 이는 최신 upstream 경로의 확인이며, 현재 로컬 실패 원인을 OVN으로 확정하는 근거는 아니다. [공식 OVN client](https://github.com/openstack/neutron/blob/4538334273e79b520e193dfece7261e13183b751/neutron/plugins/ml2/drivers/ovn/mech_driver/ovsdb/ovn_client.py), [공식 OVN 변환](https://github.com/openstack/neutron/blob/4538334273e79b520e193dfece7261e13183b751/neutron/common/ovn/utils.py).

**DHCP agent/dnsmasq:** upstream은 host tag가 붙은 router/classless 옵션을 원값으로 formatter에 전달한다. 빈 문자열은 해당 option 뒤에 comma가 있는 행으로 출력된다. dnsmasq 문서는 no-data router suppression 및 host tag가 range tag보다 우선하는 규칙을 설명한다. 현재 API echo와 guest lease의 차이를 설명하려면 설치 버전·실제 host tag/opts·ACK 중 최소 하나가 더 필요하다. empty API echo만으로 guest suppression을 주장할 수 없다. [공식 DHCP agent](https://github.com/openstack/neutron/blob/4538334273e79b520e193dfece7261e13183b751/neutron/agent/linux/dhcp.py), [dnsmasq 공식 문서](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html).

## 다음 실험과 판정

known disposable Management port 하나에만 `router=""` + `classless-static-route="<exact Management CIDR>,0.0.0.0"`을 UPDATE하고, exact GET·같은 guest의 실제 renew·lease origin·route/source·SSH를 대조하는 counterfactual은 적절하다. 공유 subnet, Lab port, StartupScript 변경은 필요 없다. 성공 시에도 fresh CREATE·Internet ON/OFF·양쪽 renew 순서·reboot·Reset 후 generation 2가 같은 정책을 유지해야 제품 수정을 검증한 것이다. 249를 요구하는 guest와 OVN map 병합 경로는 별도 미지원/미검증 경계로 남긴다.

새 단일 근본 원인은 아직 확정하지 않는다. 기존 F174-NET001의 라우팅 불완전 상태는 지속된다. 구현의 `management_network.go:79–83`은 빈 옵션을 쓰고 `:87–99`는 API echo만 확인하므로, actual guest 기본 경로 제거가 확인될 때까지 해당 이름을 실제 suppression의 증거로 사용할 수 없다. 회귀는 canonical API payload·unknown/드리프트 거절뿐 아니라 Neutron origin lease와 sole Lab default의 actual lifecycle 검증이어야 한다.

최신 SSOT/main 확인은 [context-refresh-check.json](context-refresh-check.json)에 있다. Jira/D18/D19/D20/전략 본문은 직전 검토와 같고, 최신 main은 `ee05485bd9a1668f222e7a54fb5f880ea197e35a`다. HTTP/WSS/schema/module 원문 차이는 없지만 현재 checkout은 `0051d8d`이며 새 SaaS RESET/CLEANUP worker/migration 9 및 frontend 통합 실행은 검증하지 않았다.

Reviewer는 신규 외부 폴더에만 산출물을 썼다. 제품/기존 Evidence/클라우드/서비스/원격 write/push/PR 변경은 하지 않았다.

## 수정 전 추가 finding

**F174-PMETHOD001 / Low / 외부 helper:** `candidate-onlink/local_probe_test.go:176–177`의 `a.network.Put(..., nil, nil)`은 Gophercloud v2.15.0의 PUT 기본 성공코드 `{201,202}`를 사용한다. Neutron port UPDATE의 HTTP 200도 오류가 되어 renew 이전에 종료될 수 있다. 같은 SDK `ports.Update`는 `{200,201}`을 명시한다. 영향은 후보가 이미 적용돼도 API 거절로 오분류하여 후보 실험을 평가할 수 없다는 것이다. 실제 실행의 status/body가 기록되지 않았으므로 그 실행이 HTTP 200이었는지는 확정하지 않는다.

19:18 KST 구현자에게 수정 전에 보고했다. [request_status_probe_test.go](request_status_probe_test.go.txt)의 실제 loopback HTTP 재현은 nil opts+200에서 applied=true/GET=true인데 오류가 발생함을 확인했고, 명시적 200 허용·201·400 구분을 포함한 4개 subtest가 통과했다. 원본은 [request-status-probe.log](request-status-probe.log.gz)다. 명시적 port 성공코드를 쓰고 exact GET으로 옵션을 확인한 뒤 renew해야 한다. cloud 호출 및 제품 수정은 없는 재현이다.

## 후보 v2 관측 추가

외부 helper가 명시적 `{200,201}`과 GET readback을 적용한 `candidate-onlink-v2`는 해당 API 경계를 통과했다. Management/Lab 순서의 실제 renew에서 양쪽 leaseChanged=true가 나왔지만 strict sole-Lab-default assertion은 실패했고 TCP/source는 계속 Lab 성공이었다. 새 Management lease도 Neutron DHCP port 서버와 일치하며 ROUTER가 있고 ROUTES 필드는 없었다. netplan 관측은 두 NIC의 `dhcp4: true`와 static routes/gateway 선언 부재를 확인했다. child exit 1 및 exact 8개 + SG/keypair GET absence를 유지한다. 원본 22개 SHA 및 안전 요약은 [postadmin-observations.json](postadmin-observations.json), 재집계는 [capture_observations.py](capture_observations.py)에 있다.

첫 UPDATE/GET에서 첫 renew까지는 짧은 간격이다. Neutron API와 DHCP agent reload의 비동기 시차가 있으므로 이 실행만으로 onlink 후보가 지원 불가능하다고 단정하지 않는다. 반대로 새 옵션 ACK나 수렴 확인 없이 후보 성공이라고 할 수도 없다. 동일 known port에 UPDATE를 반복하지 않고 bounded renew/allowlist ACK 관측으로 구분하는 것이 다음 단계다. [RFC 3442](https://www.rfc-editor.org/rfc/rfc3442.html)는 option 121의 최소 길이, zero next-hop local-subnet 의미 및 유효 classless option을 받은 client의 Router option 처리 규칙을 정의한다. 실제 backend/guest 적용은 별도 검증해야 한다.

## 독립 read-only backend 관측

구현자의 명시적 요청에 따라 외부 overlay만 사용해 `/agents` GET을 실행했다. 2026-10-16 19:23:09 KST DHCP agent의 allowlist 항목 `dhcp_driver=neutron.agent.linux.dhcp.Dnsmasq`와 alive/adminStateUp=true를 직접 확인했다. `dnsmasq_version`, `neutron_version`, `agent_version`, `version`, `revision`, `revision_number`, top-level revision은 모두 API에 노출되지 않았다. 설치 버전은 미확인으로 남긴다.

실제 test 0.68초 PASS, 전체 command exit 0 / 7.719초, source520/86ec 전후 차이 0이다. [backend-configuration-observer.log](backend-configuration-observer.log.gz), [backend-configuration-run.json](backend-configuration-run.json), [외부 test](backend_configuration_observer_test.go.txt), [runner](run_backend_observer.py)에 기록했다. 인증 초기화와 agent GET 외 cloud 자원 변경은 없고 host·ID·전체 configurations·HTTP error 원문은 출력하지 않았다.

## 후보 v3: wire 반영과 strict gate 수렴 확인

`candidate-onlink-v3`의 1회 owned-port UPDATE, 대기, owned Management MAC ACK allowlist 관측 뒤 첫 bounded renew 관측은 통과했다. child exit 0 / 164.766초, 실제 test 158.73초이며 원본 production520/86ec은 불변이다. exact tracked 8개와 SG/keypair GET absence도 확인했다. 실제 cloud mutation은 구현자가 실행한 외부 후보 실험이며 reviewer는 원본만 대조했다.

관측 ACK의 option121을 독립적으로 RFC3442 디코딩하여 exact Management prefix 한 개 / zero next-hop / default 0/0 없음으로 확인했다. ACK 서버는 lease 및 조회한 Neutron DHCP port 서버와 일치한다. 이 ACK에는 **option3 router가 여전히 있다**. 이후 sole Lab default와 Lab source/TCP 성공은 유효121을 받는 검증 Linux client의 Router option 무시 규칙과 일치한다. 빈 router가 wire에서 제거됐다고 보고하면 안 된다.

lease 파일에는 이때도 `ROUTES` 필드가 없으므로 그 필드 부재만으로 option121 전달 실패를 주장할 수 없다. 위 JSON의 raw 입력은 27개이며 주소·MAC·option hex 원문을 요약에 재출력하지 않고 일치 여부와 decoded route 성질만 저장했다. 후보 v2의 실패와 v3의 수렴 성공은 모두 유지한다. 실제 설치 버전, 빈 router override가 전달/생성되지 않은 세부 원인은 아직 미확인이다.

이 결과는 해당 dnsmasq/Linux 조합에서 nonempty onlink121 정책의 구현 근거가 된다. 제품 최종 source의 fresh CREATE, Internet ON/OFF, 양쪽 renew 순서, reboot, immutable Reset 후 generation 2 및 최종 cleanup 검증은 아직 이 후보 probe가 대신하지 않는다. 기존 source520/86ec의 blank121 제품 정책은 여전히 보완 필요다.
