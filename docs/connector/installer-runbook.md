# Labbit Connector 설치 및 운영 Runbook (Installer · 수동 업그레이드 · 롤백)

이 문서는 고객사 Linux 환경에서 `labbit-connector`의 신규 설치(Enrollment), `systemd` 서비스 관리, 수동 바이너리 업그레이드, 롤백 절차 및 운영 안전 수칙을 정의합니다.

---

## 1. 사전 요구사항 (Prerequisites)

* **운영체제 및 아키텍처**: Ubuntu 24.04 LTS (또는 호환 Linux) / `linux/amd64`
* **권한**: `root` 또는 `sudo` 권한
* **서비스 관리자**: `systemd` 구동 환경
* **네트워크**: SaaS 엔드포인트(기본 포트 `443/TCP`)로의 아웃바운드 HTTPS/WSS 통신 허용 (인바운드 포트 불필요)
* **필수 도구**: `curl`, `sha256sum`, `ssh-keygen`, 대화형 `/dev/tty`
* **사전 발급값**: Labbit 웹 콘솔(조직 ADMIN)에서 발급받은 1회용 Enrollment Token

---

## 2. 신규 설치 및 등록 (Installer Execution)

### 2.1 한 줄 설치 (권장)

Labbit 웹 콘솔에서 안내된 설치 스크립트를 내려받아 실행합니다. 다운로드 실패 시 빈 입력으로 `sh`가 성공 종료(exit 0)되는 문제를 방지하기 위해 임시 파일 다운로드 후 `&&`로 연결해 실행합니다.

```bash
curl -fsSL https://install.labbit.example/install.sh -o install.sh && sudo env \
  LABBIT_CONNECTOR_RELEASE_TAG='<approved-release-tag>' \
  LABBIT_CONNECTOR_EXPECTED_SHA256='<approved-connector-sha256>' \
  LABBIT_WORKSPACE_SFTP_HELPER_EXPECTED_SHA256='<approved-helper-sha256>' \
  sh ./install.sh
```

> 예시의 release tag와 두 digest는 게시·승인된 릴리스 기록의 실제 값으로 바꾼다. 기존 기본 릴리스에는 helper가 없으므로 새 릴리스 게시 전에는 이 설치 경로가 안전 중단한다. 파이프라인으로 실행할 때는 `pipefail`과 동일한 승인 값 전달이 필요하다.

### 2.2 수동 다운로드 및 검토 후 설치 (보안 대안)

스크립트 원문을 검토한 후 실행하고자 하는 경우:

```bash
# 1. 설치 스크립트 다운로드 및 내용 검토
curl -fsSL https://install.labbit.example/install.sh -o install.sh
chmod +x install.sh
less install.sh

# 2. 실행
sudo env LABBIT_CONNECTOR_RELEASE_TAG='<approved-release-tag>' \
  LABBIT_CONNECTOR_EXPECTED_SHA256='<approved-connector-sha256>' \
  LABBIT_WORKSPACE_SFTP_HELPER_EXPECTED_SHA256='<approved-helper-sha256>' \
  sh ./install.sh
```

### 2.3 설치 과정 요약
1. **사전 검사**: root 권한, OS(Linux), 아키텍처(amd64), systemd, 필수 유틸리티(`curl`, `sha256sum`, `ssh-keygen`, `stty`), 대화형 `/dev/tty` 접근 및 `stty` 제어 가능 여부 확인.
2. **바이너리 다운로드 및 무결성 검증(Fail-Closed)**: GitHub Releases의 같은 tag에서 `labbit-connector`, `labbit-workspace-sftp`, `SHA256SUMS`를 다운로드하고 체크섬을 검증하여 일치할 때만 `/usr/local/bin/`에 배치. helper 누락·손상은 Enrollment Token 입력·소비 전에 중단한다. Connector 승인 digest는 기존 릴리스 정책에 따라 `LABBIT_CONNECTOR_EXPECTED_SHA256`으로 지정할 수 있으며, helper 승인 digest인 `LABBIT_WORKSPACE_SFTP_HELPER_EXPECTED_SHA256`은 필수다.
3. **토큰 입력 및 단회성 Enrollment**: `/dev/tty`에서 입력 마스킹(`stty -echo`) 상태로 1회용 토큰을 입력받아 SaaS Enrollment API (`POST /api/v1/connector-enrollments`, 요청 필드 `enrollmentToken`) 호출 및 `connectorId`·`connectorCredential` 수신.
4. **로컬 보안 저장소 구성**:
   * 시스템 계정 `labbit` (non-root) 생성
   * `/etc/labbit-connector/` (권한: `0750`, 소유자: `root:labbit`)
   * `/etc/labbit-connector/connector.id` (권한: `0640`, 소유자: `root:labbit`)
   * `/etc/labbit-connector/credential` (권한: `0600`, 소유자: `labbit:labbit`)
5. **D-18 관리용 SSH 키 및 known_hosts 초기화**:
   * `/etc/labbit-connector/ssh/id_ed25519` 관리 키 페어 생성 (권한: `0600`)
   * 빈 `/etc/labbit-connector/ssh/known_hosts` 생성 (권한: `0644`)
6. **systemd 서비스 등록 및 자동 시작**:
   * 서비스 유닛 `/etc/systemd/system/labbit-connector.service` 등록
   * 환경설정 `/etc/labbit-connector/connector.env` 생성
   * `LABBIT_WORKSPACE_ROOT=/var/lib/labbit-workspaces/workspace`, `LABBIT_WORKSPACE_SFTP_HELPER=/usr/local/libexec/labbit-workspace-sftp`, `LABBIT_WORKSPACE_SFTP_HELPER_SOURCE_FILE=/usr/local/bin/labbit-workspace-sftp`, 다운로드로 검증한 `LABBIT_WORKSPACE_SFTP_HELPER_SHA256` 저장
   * 서비스 기동 및 상태 확인

### 2.4 Workspace VM 자동 준비 및 출시 전제

위 source 파일은 Connector 호스트의 읽기 전용 artifact이며 helper 목적지/root는 VM 안의 경로다. Provider가 별도로 구성된 Connector에서 새 Provision/Reset 시 지정 Workspace VM의 cloud-init 완료 후 관리 SSH로 helper/root를 준비한다. VM의 외부 인터넷과 사용자 StartupScript 변경은 요구하지 않는다. 이미지에는 Linux amd64/Python3/runuser와 non-root 관리 계정의 `sudo -n python3` 권한이 필요하다. 실패시 실습 생성 성공으로 처리하지 않는다. 보안·재시도 경계는 [Workspace SFTP](workspace-sftp.md)를 따른다.

이 기능은 helper asset을 포함하는 새 Connector release를 먼저 게시해야 사용할 수 있다. 기존 기본 `connector-v0.1.0`의 승인 checksum을 임의로 바꾸거나 기존 release asset을 덮어쓰지 않는다. 새 tag의 두 artifact/checksum을 게시·승인한 뒤 Installer의 기본 tag/pin을 갱신하거나 `LABBIT_CONNECTOR_RELEASE_TAG`로 해당 release를 명시한다. helper가 없는 과거 release를 선택하면 Enrollment 전에 안전 중단한다. 코드 merge만으로 이미 설치된 Connector나 기존 READY VM이 자동 갱신되지는 않는다.

---

## 3. 서비스 운영 및 상태 확인

### 3.1 systemd 상태 확인

```bash
sudo systemctl status labbit-connector.service
```

### 3.2 실시간 로그 확인 (구조화 JSON)

```bash
sudo journalctl -u labbit-connector.service -f -o cat
```

> **비밀 정보 보호 원칙**: 로그에는 Connector Credential, Enrollment Token, Keystone Token 등 민감한 자격증명이 일체 기록되지 않습니다.

---

## 4. 수동 업그레이드 절차 (Manual Upgrade Runbook)

> **[CAUTION] 운영 중 Mutation 위험 고지**
> 학생이 실습 중(활성 터미널 세션, VM 프로비저닝, 리셋, 정리 진행 중)일 때 강제로 바이너리를 교체하거나 서비스를 재시작하지 마십시오.
> 미완료 비동기 작업(Operation)의 상태 불일치가 발생할 수 있으므로, 반드시 활성 세션이 없는 유지보수 시간대에 진행해야 합니다.

### 4.1 업그레이드 순서

1. **새 바이너리 다운로드 및 체크섬 검증**:
   ```bash
   TARGET_VERSION="connector-v0.2.0"
   UPGRADE_DIR="$(mktemp -d)"
   curl -fsSL "https://github.com/ktcloud4-SL/labbit-app/releases/download/${TARGET_VERSION}/labbit-connector" -o "${UPGRADE_DIR}/labbit-connector"
   curl -fsSL "https://github.com/ktcloud4-SL/labbit-app/releases/download/${TARGET_VERSION}/SHA256SUMS" -o "${UPGRADE_DIR}/SHA256SUMS"

   # 체크섬 검증 (SHA256SUMS 내 단일 바이너리 항목만 정확히 추출하여 다운로드한 파일과 대조)
   (
     cd "${UPGRADE_DIR}"
     awk '$2 == "labbit-connector" || $2 == "*labbit-connector"' SHA256SUMS | sha256sum --check --strict
   )
   ```

2. **기존 바이너리 백업**:
   ```bash
   sudo cp /usr/local/bin/labbit-connector /usr/local/bin/labbit-connector.bak
   ```

3. **서비스 정상 종료(Graceful Drain)**:
   ```bash
   sudo systemctl stop labbit-connector.service
   ```

4. **바이너리 교체 및 권한 설정**:
   ```bash
   sudo mv "${UPGRADE_DIR}/labbit-connector" /usr/local/bin/labbit-connector
   sudo chmod 0755 /usr/local/bin/labbit-connector
   sudo chown root:root /usr/local/bin/labbit-connector
   rm -rf "${UPGRADE_DIR}"
   ```

5. **서비스 재기동 및 관측**:
   ```bash
   sudo systemctl start labbit-connector.service
   sudo systemctl status labbit-connector.service
   ```
   Labbit 웹 콘솔에서 Connector가 정상적으로 `ONLINE` 상태로 복귀하고 새 버전 번호가 수신되는지 확인합니다.

---

## 5. 롤백 절차 (Rollback Runbook)

업그레이드 후 비정상 동작 또는 호환성 문제가 발생하는 경우 신속하게 이전 버전으로 롤백합니다.

```bash
# 1. 서비스 중지
sudo systemctl stop labbit-connector.service

# 2. 백업 바이너리 복원
sudo cp -f /usr/local/bin/labbit-connector.bak /usr/local/bin/labbit-connector
sudo chmod 0755 /usr/local/bin/labbit-connector
sudo chown root:root /usr/local/bin/labbit-connector

# 3. 서비스 재시작
sudo systemctl start labbit-connector.service

# 4. 서비스 로그 및 온라인 상태 확인
sudo journalctl -u labbit-connector.service -n 30 --no-pager
```

> **자격증명 보호 주의**: 롤백 시 `/etc/labbit-connector/credential` 또는 `connector.id` 파일을 임의로 삭제하거나 재생성하지 마십시오. 기존 자격증명 파일이 유지되어야 SaaS와의 WSS 신뢰 관계(ONLINE)가 즉시 복원됩니다.

---

## 6. 장애 진단 및 문제 해결 (Troubleshooting)

| 증상 | 원인 | 조치 방법 |
| :--- | :--- | :--- |
| `Interactive TTY (/dev/tty) is not accessible` | 비대화형 백그라운드 세션에서 실행하여 `/dev/tty` 제어 불가 | 반드시 대화형 터미널(`/dev/tty` 접근 및 `stty -echo` 가능 환경)에서 실행 (환경변수/CLI 인자 주입 금지) |
| `Connector enrollment was rejected by SaaS (HTTP 401: invalid_enrollment_token)` | 토큰 만료, 오타 또는 이미 소비된 일회용 토큰 | Labbit 웹 콘솔(조직 ADMIN)에서 새 Enrollment Token을 발급받아 재실행 |
| `Connector enrollment outcome is UNCERTAIN` | Enrollment 요청 중 네트워크 단절·타임아웃·응답 유실 | 동일 토큰으로 임의 재시도 금지. Labbit 웹 콘솔에서 Connector 목록을 조회하여 불완전 등록된 항목이 있으면 폐기(Revoke)한 뒤 새 Enrollment Token을 발급받아 재설치 |
| `Failed to download release SHA256SUMS` / `SHA256 mismatch` | 체크섬 파일 조회 실패 또는 바이너리 해시 불일치 | Fail-closed 정책에 의해 설치가 즉시 중단됨. 아웃바운드 네트워크 및 GitHub Release `SHA256SUMS` 확인 후 재시도 |
| `connector credential is required in production` | 자격증명 파일 누락 또는 경로 오류 | `/etc/labbit-connector/credential` 파일 존재 여부 및 `connector.env` 경로 확인 |
| WSS 재연결 반복 (`재연결 대기`) | 아웃바운드 443 차단 또는 SaaS 서버 도메인/TLS 오류 | `curl -vk https://<SAAS_URL>`로 아웃바운드 HTTPS/TLS 연결 진단 |
| `Existing Connector credentials or identity found` | 이미 `credential`, `connector.id` 또는 서비스 유닛이 존재하는 환경에서 재실행 | 기존 식별자 및 현재 서비스 상태(실행/중지) 보호를 위해 설치가 안전 중단됨. 바이너리 업그레이드나 서비스 재기동은 본 Runbook의 수동 절차 수행 |
| `Failed to securely save Connector credentials (Token was already consumed)` | 토큰 소비 성공 후 로컬 설정 디렉터리 권한·디스크 오류 등으로 파일 저장 실패 | 관리자 웹 콘솔의 커넥터 목록 확인 → 등록된 커넥터(ID) Revoke(폐기) → 신규 Token 발급 후 재설치 |

### Helper 승인 체크섬과 릴리스 적용

설치 실행 전에 승인된 릴리스 기록에서 `LABBIT_WORKSPACE_SFTP_HELPER_EXPECTED_SHA256`를 지정한다. helper와 SHA256SUMS를 함께 변조한 경우도 이 독립된 승인 값으로 거절한다. 값 누락은 Enrollment 전 안전 중단한다. 기존 기본 릴리스 `connector-v0.1.0`에는 helper가 없으므로 이 변경이 포함된 새 릴리스를 발행하고 승인된 두 바이너리 체크섬 및 기본 릴리스 pin을 갱신해야 운영 설치에 적용된다. 이 코드 변경 자체는 릴리스나 배포를 수행하지 않는다.
