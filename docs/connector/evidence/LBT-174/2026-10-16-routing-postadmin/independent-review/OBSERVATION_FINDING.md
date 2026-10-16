# F174-RNET006 — 검증한 관측과 사용하는 SSH 주소의 분리

- 심각도: **Medium / confirmed loopback fault reproduction**.
- 검토 기준: HEAD/base `0051d8d6803fd447f7b690dd895a0a928c3c7302` + 미커밋 source521 `8b3b7bf754beb3f0cd86b3f2979a4a45e02850c0420ed06eb5667cb9b0d51edc`.
- 위치: `internal/connector/provider/openstack/topology.go:155–167`의 `refreshProvisionNICs`.
- 계약: Provider의 관리 NIC / 원래 검증한 Management CIDR / SSH endpoint 일치, fail-closed 및 이미 생성한 자원 ID 보존. `runtime/contract.yaml` Management 지원 조건과 Connector Provision NIC 계약.

첫 post-Nova GET만 같은 subnet ID의 범위 밖 IP를 반환하고 다음 GET은 정상 port를 반환하도록 loopback API를 조작했다. `verifyManagementPort`는 두 번째 정상 응답을 검증하지만, `managementAddress`는 첫 응답의 IP를 반환한다. `Provision`이 `SUCCEEDED`를 반환하고 `sshProbe("172.16.9.200:22")`를 실제 호출했다. 이 주소는 합성 fixture 주소이며 클라우드 호출은 없었다.

재현 파일: [management_observation_probe_test.go](management_observation_probe_test.go.txt). [overlay](management-observation-overlay.json), [원본 실패 로그](management-observation-before.log.gz), [실행 metadata](management-observation-before.json). `go test -overlay <overlay> -run ^TestIndependentManagementReturnedObservationIsValidated$ -count=1 -timeout=30s ./internal/connector/provider/openstack`는 exit1이다.

영향은 검증되지 않은 관리 주소로 SSH를 시도할 수 있다는 것이다. 이는 정상 Neutron이 subnet CIDR 변경을 허용한다는 주장이 아니라, 요청 사이 API 응답 변경·불일치에 대한 fail-closed 경계 재현이다. 실제 검증할 단일 port 응답을 ID/network/subnet/fixed IP/prefix/SG/options 모두 확인하고 바로 그 응답의 주소를 사용해야 한다. 두 GET을 유지한다면 사용한 첫 응답 자체를 검증해야 하며, 별도 응답의 정상 여부로 이를 대체할 수 없다.

필요한 회귀는 첫 관측만 잘못된 IP이고 다음 관측은 정상인 경우의 SSH 금지/실패와 알려진 port ID 보존이다. 같은 경계에서 ID/network/policy 오류도 사용할 응답에 대해 검증해야 한다. 구현 담당에게 수정 전에 보고했다. 본 reviewer는 제품 소스·클라우드를 수정하지 않았다.

## 수정 후 독립 재검증

최종 source521 `7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31`은 `verifyManagementPort`가 검증한 `ports.Port`를 반환한다. `refreshProvisionNICs`는 그 객체를 검증한 뒤에만 ledger에 갱신하고 바로 그 IP를 사용한다. 중복 Management GET은 제거됐다. checked-in `TestManagementPortUsesValidatedObservation`의 outside-address / foreign-ID / foreign-network / empty-121 회귀와 원래 ID 보존·foreign ID 배제도 확인했다.

[run_observation_regression.py](run_observation_regression.py)는 6개 독립 경우(stable / 첫 관측만 outside address / wrong ID / wrong network / wrong SG / missing policy)를 실행한다. 저장한 수정 전 production 2파일만 overlay로 복원하면 stable PASS와 5개 invalid 경우 FAIL, 최종 소스에서는 6개 모두 PASS다. 소스521 전후 hash 차이는 0이며 cloud 호출은 없다. [실행·소스 metadata](management-observation-regression.json), [수정 전 복원 로그](management-observation-boundary-restored-before.log.gz), [수정 후 로그](management-observation-boundary-after.log.gz).

**이 finding의 코드·loopback 회귀 경계는 해결됐다.** 제품의 fresh CREATE / ON·OFF / 갱신 / 재부팅 / Reset 실제 수용은 별도 최종 검증 결과를 기다린다. 초기 `management-observation-boundary-initial-after.*`는 수정이 반영된 후 첫 PASS 실행이다. 정확한 전후 source identity가 있는 위 두 재실행이 판정 근거다.
