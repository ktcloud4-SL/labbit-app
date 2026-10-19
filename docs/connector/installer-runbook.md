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

Labbit 웹 콘솔에서 제공하는 명령을 복사하여 고객사 Linux VM에서 실행합니다.

```bash
curl -fsSL https://install.labbit.example/install.sh | sudo sh
```

### 2.2 수동 다운로드 및 검토 후 설치 (보안 대안)

스크립트 원문을 검토한 후 실행하고자 하는 경우:

```bash
# 1. 설치 스크립트 다운로드 및 내용 검토
curl -fsSL https://install.labbit.example/install.sh -o install.sh
chmod +x install.sh
less install.sh

# 2. 실행
sudo ./install.sh
```

### 2.3 설치 과정 요약
1. **사전 검사**: root 권한, OS(Linux), 아키텍처(amd64), systemd, 필수 유틸리티, 대화형 TTY 유무 확인.
2. **바이너리 다운로드 및 무결성 검증**: GitHub Releases에서 고정 버전의 바이너리를 다운로드하고 SHA256 체크섬을 검증하여 `/usr/local/bin/labbit-connector`에 배치.
3. **토큰 입력 및 단회성 Enrollment**: `/dev/tty`에서 입력 마스킹(echo-off) 상태로 1회용 토큰을 입력받아 SaaS Enrollment API (`POST /api/v1/connector-enrollments`) 호출.
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
   * 서비스 기동 및 상태 확인

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
   curl -fsSL "https://github.com/ktcloud4-SL/labbit-app/releases/download/${TARGET_VERSION}/labbit-connector" -o /tmp/labbit-connector-new
   curl -fsSL "https://github.com/ktcloud4-SL/labbit-app/releases/download/${TARGET_VERSION}/SHA256SUMS" -o /tmp/SHA256SUMS

   # 체크섬 검증
   cd /tmp
   grep "labbit-connector" SHA256SUMS | sha256sum --check
   ```

2. **기존 바이너리 백업**:
   ```bash
   sudo cp /usr/local/bin/labbit-connector /usr/local/bin/labbit-connector.bak
   ```

3. **서비스 정상 종료(Graceful Drain)**:
   ```bash
   sudo systemctl stop labbit-connector.service
   ```

4. **바이너리 원자적 교체 및 권한 설정**:
   ```bash
   sudo mv /tmp/labbit-connector-new /usr/local/bin/labbit-connector
   sudo chmod 0755 /usr/local/bin/labbit-connector
   sudo chown root:root /usr/local/bin/labbit-connector
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
| `Interactive TTY not detected` | 파이프라인이나 백그라운드 스크립트에서 실행 시 TTY 없음 | 대화형 쉘에서 직접 `install.sh` 실행 또는 `LABBIT_ENROLLMENT_TOKEN` 환경변수 주입 |
| `Connector enrollment failed with HTTP status 401/403` | 토큰 만료 또는 이미 소비된 일회용 토큰 | Labbit 웹 콘솔(관리자)에서 새 Enrollment Token을 발급받아 재입력 |
| `SHA256 mismatch` | 다운로드 중 패킷 유실 또는 손상 | 네트워크 상태 확인 후 재실행. 지속될 경우 GitHub Release SHA256SUMS 확인 |
| `connector credential is required in production` | 자격증명 파일 누락 또는 경로 오류 | `/etc/labbit-connector/credential` 파일 존재 여부 및 `connector.env` 경로 확인 |
| WSS 재연결 반복 (`재연결 대기`) | 아웃바운드 443 차단 또는 SaaS 서버 도메인/TLS 오류 | `curl -vk https://<SAAS_URL>`로 아웃바운드 HTTPS/TLS 연결 진단 |
| `Existing Connector credentials found` | 이미 설치 및 등록된 환경에서 재실행 | 기존 식별자 보호 정책에 따라 무단 재등록이 방지됨. 업그레이드는 수동 바이너리 교체 수행 |
