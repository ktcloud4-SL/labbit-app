# LBT-198: Backend Multi-Replica Owner Routing 한계 재현 및 HOLD 판정 근거

## 1. 개요 및 배경

- **TARGET Jira**: [LBT-198](https://samsunglions.atlassian.net/browse/LBT-198) — Backend multi-replica Connector/WSS owner routing
- **작업 브랜치**: `feat/LBT-198-backend-multi-replica` (시작 base SHA: `4d74a4a83307a9b55d7c1d644ee440da06b696b9`, origin/main)
- **원칙 및 제약**:
  - 단일 implementation writer 유지 (LBT-197 등 타 worktree 격리 보존)
  - Frozen AWS baseline, Seoul/Osaka workload, cloud DB, live traffic, secrets 변경 금지
  - 신규 인프라(Redis/Valkey/Kafka/SQS/HPA/KEDA/Karpenter) 임의 도입 금지
  - durable PostgreSQL SSOT 보존 및 raw Terminal/Live body streaming 비영속 원칙 유지
  - 독립 ChatGPT Review Gate 유지 (Agent의 임의 merge / approve 금지, Jira DONE 처리 금지)

---

## 2. 현행 아키텍처 및 계약 분석 (SSOT 대조)

### 2.1 저장소 계약 (SSOT)
- `runtime/contract.yaml` (v0.1.7):
  - `not_provided: - multi-replica Terminal owner routing과 session affinity` 명시.
  - `roles: [api, realtime, worker, preview]`는 단일 프로세스/단일 Pod co-location 전제.
  - `worker_requires_api`, `worker_without_api`: Worker는 동일 프로세스 내 Connector Router를 통해 직접 디스패치해야 함.
  - 현행 Runtime main current contract 준수 (N=1 main baseline 불변).
- `contracts/connector/` & `contracts/realtime/`:
  - Connector는 백엔드 인스턴스에 WebSocket 제어 채널(`GET /ws/connector/v1/control`)로 상시 연결.
  - Terminal Data(`GET /ws/connector/v1/terminal/data`), File Data(`GET /ws/connector/v1/files/data`), Preview Data(`GET /ws/connector/v1/preview/data`)는 요청 시점마다 동적으로 열리는 단기 ephemeral WSS 터널.
  - Raw PTY stream 및 파일 바이트 스트림은 DB나 persistent storage에 영속화할 수 없음 (보안 및 비영속 원칙).

### 2.2 Istio / LB Session Affinity 가설의 한계
1. **서로 다른 클라이언트 식별체계 (Disjoint Identities)**:
   - 브라우저는 사용자 세션 쿠키(`__Host-labbit-session`) 및 Terminal attach token으로 인입.
   - Connector는 KT Cloud VM에서 Connector Bearer 토큰으로 인입.
   - 외부 L4/L7 Ingress/Istio는 브라우저와 Connector가 동일한 `lab_instance_id`나 `connector_id`를 대상으로 하는지 연관지을 수 없음.
2. **In-Process Worker의 비인가 인입**:
   - 백그라운드 Worker(`provision.Executor`)는 PostgreSQL의 `FOR UPDATE SKIP LOCKED` 큐를 프로세스 내부에서 직접 폴링함.
   - Worker는 외부 Ingress/LB를 통과하지 않으므로 어떠한 L7 Affinity나 Istio 가상 서비스 라우팅도 적용되지 않음.
3. **WebSocket 페이로드 불투명성**:
   - 데이터 채널 연결 시 대상 `terminalSessionId`, `fileRequestId`, `previewSessionId`는 WebSocket 핸드셰이크 이후 애플리케이션 프레임 본문으로 전달됨. L7 로드밸런서는 이를 검사하여 특정 Pod로 라우팅할 수 없음.
4. **Pod 라이프사이클 및 동적 재연결 취약성**:
   - Pod 재시작, 배포, Connector 일시 단락 후 재연결 시 기존 바인딩이 파괴되며, 인플라이트 커맨드의 응답 유실 발생.

---

## 3. In-Platform 라우팅 평가 및 HOLD 판정 근거

| 영역 | 요구사항 | 현 플랫폼 제약 하의 구현 한계 및 운영 리스크 | 판정 |
| --- | --- | --- | --- |
| **Control Plane** (Operation/Reconcile) | 다른 Pod의 Connector로 명령 포워딩 및 응답 수신 | - `LISTEN/NOTIFY`: 8KB 페이로드 제한으로 `CreationSnapshot` 등 대형 제어 본문 직접 전송 불가. Key-reference(ID 통지 후 별도 테이블 조회) 패턴은 원리적으로 설계 가능하나, 추가 상태 저장/동기화/정리 오버헤드 발생.<br>- DB polling/queue: 지연 시간 증가, DB I/O 증폭, 타임아웃 경합 유발 | ⚠️ 복잡도/비용 미입증 |
| **Data Plane** (Terminal PTY) | 브라우저-Connector 간 양방향 raw PTY 스트리밍 | - DB에 PTY byte stream 저장 불가 (비영속 원칙 위반)<br>- Pod-to-Pod mTLS 터널링은 원리적으로 불가능하지 않으나, 현 플랫폼에서 프로세스 간 동적 연결 메시 구축, 상호 인증, 장애 전파 방지 비용/복잡도가 입증되지 않음 | ⚠️ 안전한 최소 설계 미확보 |
| **Data Plane** (Workspace File) | HTTP 요청 Pod와 Connector 연결 Pod 간 바이너리 중계 | - 임의 크기의 파일 스트림을 DB를 통해 버퍼링하는 것은 I/O 증폭 및 메모리 고갈 유발 | ⚠️ 비효율적 |
| **Data Plane** (Preview) | Preview Origin HTTP/WSS 트래픽을 Connector 터널로 프록시 | - 실시간 TCP/HTTP 터널링을 DB로 중계할 수 없음 | ⚠️ 비효율적 |
| **Worker Safety** | 비소유 Pod의 중복 실행 방지 및 안전한 deferral | - 현재 코드는 Connector 미연결 시 `ErrConnectorUnavailable`을 감지하여 안전하게 `DEFERRED`로 되돌림 확인 | ⚠️ 방어는 되나 폴링 낭비 |

### HOLD 판정 정의
- **판정: HOLD (구현 유보)**
- **사유**: PostgreSQL `LISTEN/NOTIFY`의 8KB 직송 제한이나 Pod-to-Pod 터널링이 수학적·원리적 불가능(logical impossibility)을 뜻하는 것은 아닙니다. 그러나 **승인된 현 플랫폼 및 범위 내에서 안전하고 운영 가능한 최소 multi-replica 설계를 확보하지 못했기 때문**에 HOLD로 판정합니다.
- **분산 캐시 관련 주의**: Redis/Valkey Pub/Sub을 단독 도입하더라도 그 자체만으로 스트리밍의 안전성(data-path 버퍼 수명주기, split-brain 방지를 위한 fencing 등)이 자동으로 입증되는 것은 아니며, 전용 분산 조정 아키텍처가 추가로 요구됩니다.

---

## 4. Multi-Replica Mismatch 재현 검증 결과

`internal/server/app/multi_replica_owner_mismatch_integration_test.go`를 통해 격리된 PostgreSQL 위에서 3개의 백엔드 인스턴스(Pod A, Pod B, Pod C로 명명)와 Mock Connector를 조립하여 cross-Pod 불일치를 재현 및 검증함.

> **환경 특성**: 본 테스트의 `newMultiReplicaCluster`는 단일 Go 프로세스 내에서 `controlStack` 및 `httptest.Server` 3개를 구동하는 **논리적 인스턴스 시뮬레이션(logical instance simulation)**입니다. Kubernetes/k3d 멀티 노드, 실제 Pod E2E, KT Cloud 인프라 및 N=1 회귀 검증은 본 스위트의 검증 범위에 포함되지 않습니다 (**NOT VERIFIED**).

### 4.1 테스트 케이스 및 관측 결과

| 테스트 케이스 | 목적 및 시나리오 | 관측된 동작 및 검증 범위 | 판정 |
| --- | --- | --- | --- |
| `TestMultiReplica_RegistryAndRouter_CrossPodMismatch` | Connector가 Pod A에 연결되었을 때 Pod B의 Registry/Router 상태 확인 | Pod A는 `WithReadyRoute` 성공. Pod B는 `ErrNotConnected`, `SendOperationCommand`/`SendReconcileRequest`/`SendTerminalOpen`/`SendTerminalClose` 모두 `ErrConnectorUnavailable` 반환 확인. | **PASS** (재현 완료) |
| `TestMultiReplica_Worker_CrossPodClaimAndDeferral` | Worker가 실행 중인 Pod B가 DB에서 PENDING 작업을 claim했으나 Connector가 Pod A에 있을 때 동작 | Pod B Worker가 아이템 claim 후 `ErrConnectorUnavailable`을 수신하고, 아이템을 상태 `PENDING`, 단계 `DEFERRED`, `dispatch_not_before` 설정 후 반환. Provider 호출 0회 확인. | **PASS** (재현 완료) |
| `TestMultiReplica_Terminal_CrossPodMismatch` | - 3a: Pod B로 HTTP Terminal create 인입<br>- 3b: Pod B로 Connector Data WSS 인입<br>- 3c: Pod A에서 생성된 세션에 대해 Pod B로 Browser WSS 인입 | - 3a: HTTP 503 (`connector_unavailable`) 반환<br>- 3b: Pod B Relay가 1008/4003으로 거절<br>- 3c: Pod B Relay가 ERROR 프레임 전송 후 close 4003 (`closeNotFound`)으로 거절 확인 | **PASS** (재현 완료) |
| `TestMultiReplica_FileTransport_CrossPodMismatch` | - Pod B로 Workspace File tree HTTP 요청<br>- Pod B로 알 수 없는 `fileRequestId`의 File Data WSS 연결 | - HTTP 503 반환 확인<br>- Data WSS가 close 1008/4003으로 즉시 거절 확인.<br>*(범위 한정: 로컬 pending이 없는 unknown ID에 대한 fail-closed 거절 검증이며, Pod A의 유효한 active pending이 Pod B로 교차 인입된 관측은 아님)* | **PASS** (재현 완료) |
| `TestMultiReplica_Preview_CrossPodMismatch` | - Pod B로 Preview session 생성 요청<br>- Pod B로 알 수 없는 `previewSessionId`의 Preview Data WSS 연결 | - HTTP 503 반환 확인<br>- Preview Data WSS가 close 1008/4003으로 즉시 거절 확인.<br>*(범위 한정: 로컬 pending이 없는 unknown ID에 대한 fail-closed 거절 검증이며, Pod A의 유효한 active pending이 Pod B로 교차 인입된 관측은 아님)* | **PASS** (재현 완료) |
| `TestMultiReplica_ConnectorReconnect_StaleOwnerAndOrphanResult` | Pod A에서 커맨드 전송 후 Connector가 끊기고 Pod B로 재연결된 경우의 응답 고아화 | - Pod B Router는 로컬 pending이 없어 `RouteOperationResult`가 false(unmatched 폐기) 반환 및 `PendingCount=0` 확인.<br>- Pod A Router는 `PendingCount=1`로 미완료 대기 상태가 지속됨을 직접 assert 확인.<br>*(범위 한정: unmatched 폐기와 Pod A PendingCount=1 잔류를 직접 assert한 것이며, 실제 운영 타이머 만료나 DB UNKNOWN/RECONCILING 전이를 직접 assert한 것은 아님)* | **PASS** (재현 완료) |
| `TestMultiReplica_TopologyN3_FailureRateAnalysis` | N=3 논리 클러스터에서 9회의 순차적 요청 분산 시 1단계(HTTP Create) 실패율 표본 분석 | - `i % len(pods)` 고정 라운드로빈 하네스에서 9회 중 6회 실패(66.7% 실패율 표본) 확인 (실제 운영 LB 통계가 아닌 하네스 표본).<br>- 3단계 전체 흐름(Create + Data WSS + Browser WSS) 성공 확률 $(1/3)^3 \approx 3.7\%$는 3개 홉이 독립·균등 무작위 분산된다는 이론적 가정에 기반한 계산 예시이며 3단계 E2E 실측치가 아님. | **PASS** (재현 완료) |

### 4.2 실행 증빙 (Local Integration & Race Detector)
```bash
LABBIT_TEST_DATABASE_DSN="postgres://labbit:labbit-local-dummy@127.0.0.1:5432/postgres?sslmode=disable" \
go test -tags integration -run '^TestMultiReplica' -race -v ./internal/server/app
```
- **결과**: `PASS ok github.com/ktcloud4-SL/labbit-app/internal/server/app 10.651s` (Data race 0건).

---

## 5. 최종 판정 (VERDICT) 및 권고

### 5.1 판정: HOLD (구현 유보)
- v0.1 릴리스 기준에서는 **Backend multi-replica Connector/WSS owner routing 기능 구현을 HOLD**함.
- **사유**:
  1. 외부 공유 상태 계층이나 Pod-to-Pod 안전 메시가 부재한 현 플랫폼 제약 하에서, WebSocket 데이터 채널 및 동기식 명령-응답 라우팅의 안전하고 운영 가능한 최소 설계를 확보하지 못함.
  2. PostgreSQL을 이용한 임의 프록시/큐잉은 복잡도와 DB 부하를 증가시키며 비영속 스트리밍 원칙을 훼손할 위험이 큼.
  3. L4/L7 Ingress 수준의 세션 어피니티로는 서로 다른 엔티티(Browser vs Connector vs In-process Worker) 간 일치성을 보장할 수 없음.

### 5.2 권장 운영 아키텍처 (대안)
1. **단기 (v0.1 / 현재)**:
   - Backend Pod replica를 **단일 인스턴스 (`replicas: 1`)**로 유지 (SSOT `runtime/contract.yaml` 준수).
   - **주의**: 단일 인스턴스(`replicas: 1`) 배포 환경에서는 무중단 롤링 업데이트가 보장되지 않습니다. Pod 교체 및 재시작 시 일시적인 다운타임과 연결 단락이 수반되며, Connector는 재연결 백오프를 통해 새 Pod로 재접속하게 됩니다.
2. **중장기 (v0.2 이후 아키텍처)**:
   - 분산 인메모리 스토어(Valkey/Redis Sentinel 또는 Cluster) 공식 승인 및 도입 검토 (단, Redis Pub/Sub 단독 도입만으로 안전성이 완성되지 않으며 데이터 경로 버퍼 및 fencing 메커니즘 필수).
   - 또는 Connector Control Gateway와 API/Worker 서비스를 물리적으로 분리하여 Connector 연결 상태를 별도 Gateway 계층에서 전담 관리하는 토폴로지 검토.

---

## 6. Acceptance 기준 충족 현황

| Jira LBT-198 Acceptance 항목 | 상태 | 설명 |
| --- | --- | --- |
| 1. 현행 main runtime/connector/realtime 계약 및 구조 파악 | **PASS** | `runtime/contract.yaml`, `contracts/connector/`, `contracts/realtime/` 정밀 대조 완료 |
| 2. process-local Registry/Router, Terminal, Live, File, Preview, Worker cross-Pod mismatch 재현 | **PASS** | `multi_replica_owner_mismatch_integration_test.go`를 통해 단일 프로세스 3개 logical instance simulation 환경에서 재현 완료 |
| 3. Istio/LB affinity 한계 확인 및 최소 설계 성립 판단 | **PASS** | 현 승인 플랫폼 범위 내에서 안전하고 운영 가능한 최소 설계 미확보로 HOLD 판정 |
| 4. N=1, N=2, N=3+ 토폴로지 분석 및 고아 결과/stale owner 검증 | **PASS** | 하네스 표본 66.7% 및 unmatched 폐기/PendingCount=1 잔류 검증 완료 |
| 5. durable PostgreSQL SSOT 및 raw Terminal 비영속 원칙 준수 | **PASS** | DB 스키마 무단 변경 없음, 비영속 스트림 보존 |
| 6. 안전 수칙 (동결 인프라/클라우드 자원/비밀 변경 금지, Jira DONE 처리 금지) | **PASS** | 로컬 격리 DB 및 테스트 하네스에서만 검증 수행, 외부 자원 0회 변경, Jira 상태 변경 금지 |
| 7. 실제 KT Cloud Provider 연동 Acceptance | **NOT VERIFIED** | Provider 비용/자원 변경 금지 제약 및 아키텍처 HOLD 판정에 따라 실제 클라우드 인프라 호출 미수행 |
| 8. Kubernetes / k3d multi-pod E2E 검증 | **NOT VERIFIED** | 단일 프로세스 logical instance simulation으로 검증 수행, 실제 다중 Pod k3d 배포는 미수행 |
| 9. N=1 production 회귀 검증 | **NOT VERIFIED** | 현행 main single-replica runtime 계약 유지 baseline |
