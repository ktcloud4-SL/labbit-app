# LBT-61 · Connector Customer Installation & Enrollment MVP 실환경 검증 증적

- **실행일**: 2026-10-21 KST
- **대상 티켓**: [LBT-61](https://samsunglions.atlassian.net/browse/LBT-61) (`[CC-04] Connector Customer Installation & Enrollment MVP`)
- **담당자(DRI)**: 이주희
- **연계 서브태스크**:
  - [LBT-181](https://samsunglions.atlassian.net/browse/LBT-181): `[CC-04-1] Connector Runtime Hardening · Binary Release · Installer` (완료)
  - [LBT-182](https://samsunglions.atlassian.net/browse/LBT-182): `[CC-04-2] SaaS Enrollment API · Credential · Connector 상태` (완료)
  - [LBT-183](https://samsunglions.atlassian.net/browse/LBT-183): `[CC-04-3] Connector 관리자 UX · Installer 설치 안내` (완료)

---

## 1. 검증 환경 및 대상

| 구분 | 정보 | 비고 |
| :--- | :--- | :--- |
| **대상 호스트** | KT Cloud D1 `labbit-connector-vm` (공인 IP: `211.184.227.97`) | 신규 생성된 실제 클라우드 Linux VM |
| **운영체제** | Ubuntu 24.04 LTS (Noble Numbat, Kernel `6.8.0-142-generic`) | 개발도구(Go) 미설치 순수 환경 |
| **바이너리 버전** | `connector-v0.1.0` (commit `e0caea4f3191e9109171c02581d498f48e4c9887`) | GitHub Releases 공식 배포 바이너리 |
| **승인 고정 SHA256** | `742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346` | 인스톨러 스크립트 내 암호화 앵커 핀 |
| **연동 SaaS 주소** | `https://jinwook.store` | 운영 SaaS API / WSS / Web Console |
| **등록된 Connector** | ID: `113369f3-aa16-46e3-8173-068d85e2d5e3` / 표시 이름: `test123` | 단회성 Enrollment Token 정상 소비 완료 |

---

## 2. 실환경 검증 절차 및 실측 기록

### 1) 사전 준비 및 클린 환경 확인
VM 내부에서 Go 개발 환경이 존재하지 않는 순수 고객사 환경임을 확인했습니다:
```bash
$ which go || echo 'No Go installed'
No Go installed
$ cat /etc/os-release
NAME="Ubuntu"
VERSION="24.04 LTS (Noble Numbat)"
ID=ubuntu
```

### 2) 공식 인스톨러 원라이너 실행 및 릴리즈 무결성 이중 검증
웹 콘솔(`/connectors`)에서 발급받은 일회용 Enrollment Token을 기반으로 공식 인스톨러를 실행했습니다:
```bash
curl -fsSL https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/main/scripts/installer/install.sh | \
  sudo LABBIT_SAAS_BASE_URL="https://jinwook.store" sh
```

실행 결과 실측 로그:
```text
==> [1/6] Running pre-flight system checks...
==> [2/6] Downloading Labbit Connector binary (connector-v0.1.0)...
==> [2/6] Verifying binary integrity (SHA256)...
    SHA256 verified successfully against release SHA256SUMS: 742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346
    SHA256 verified successfully against approved pinned checksum: 742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346
    Installed binary to /usr/local/bin/labbit-connector
==> [3/6] Requesting Enrollment Token...
Enter Labbit Connector Enrollment Token: 
==> [3/6] Enrolling connector with SaaS (https://jinwook.store)...
    Successfully enrolled with SaaS!
    Assigned Connector ID: 113369f3-aa16-46e3-8173-068d85e2d5e3
==> [4/6] Creating labbit system user and configuring permissions...
==> [5/6] Creating initial Ed25519 management key pair and known_hosts...
==> [6/6] Installing and starting systemd service (labbit-connector.service)...
Synchronizing state of labbit-connector.service with SysV service script with /usr/lib/systemd/systemd-sysv-install.
Executing: /usr/lib/systemd/systemd-sysv-install enable labbit-connector
Created symlink /etc/systemd/system/multi-user.target.wants/labbit-connector.service → /etc/systemd/system/labbit-connector.service.
● labbit-connector.service - Labbit Connector Service
     Loaded: loaded (/etc/systemd/system/labbit-connector.service; enabled; preset: enabled)
     Active: active (running) since Wed 2026-10-21 17:49:24 KST; 18ms ago
...
================================================================================
  Labbit Connector installation and enrollment completed successfully!
  Connector ID : 113369f3-aa16-46e3-8173-068d85e2d5e3
  Service      : labbit-connector (active/running)
  Config Dir   : /etc/labbit-connector
================================================================================
```

### 3) 로컬 파일 레이아웃 및 권한 보안 검증
설치 완료 후 생성된 설정 및 크레덴셜 디렉터리의 접근 권한을 확인했습니다:
```bash
$ sudo ls -la /etc/labbit-connector/
total 32
drwxr-x---   3 root   labbit  4096 Oct 21 17:49 .
drwxr-xr-x 117 root   root   12288 Oct 21 17:49 ..
-rw-r-----   1 root   labbit   369 Oct 21 17:49 connector.env
-rw-r-----   1 root   labbit    37 Oct 21 17:49 connector.id
-rw-------   1 labbit labbit    44 Oct 21 17:49 credential
drwx------   2 labbit labbit  4096 Oct 21 17:49 ssh
```
- `/etc/labbit-connector/credential`: `0600` 권한으로 `labbit:labbit`만 읽기 가능 (일반 사용자 및 타 프로세스 격리)
- `/etc/labbit-connector/ssh`: `0700` 권한으로 안전한 Ed25519 관리 키 보관
- 설치된 바이너리 버전 및 해시 일치 확인:
```bash
$ /usr/local/bin/labbit-connector -version
labbit-connector connector-v0.1.0 (commit: e0caea4f3191e9109171c02581d498f48e4c9887)

$ sha256sum /usr/local/bin/labbit-connector
742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346  /usr/local/bin/labbit-connector
```

### 4) systemd 데몬 동작 및 SaaS WSS 연결 수립
커넥터 백그라운드 서비스 로그를 통해 SaaS Control WSS 연결 및 HELLO 핸드셰이크가 정상 완료됨을 확인했습니다:
```bash
$ sudo journalctl -u labbit-connector.service --no-pager
Oct 21 17:49:24 labbit-connector-vm systemd[1]: Started labbit-connector.service - Labbit Connector Service.
Oct 21 17:49:24 labbit-connector-vm labbit-connector[13467]: {"timestamp":"2026-10-21T17:49:24.197350037+09:00","level":"INFO","message":"Labbit Connector 시작","service":"labbit-connector","component":"bootstrap","version":"dev","environment":"production","connector_id":"113369f3-aa16-46e3-8173-068d85e2d5e3","version":"connector-v0.1.0"}
Oct 21 17:49:25 labbit-connector-vm labbit-connector[13467]: {"timestamp":"2026-10-21T17:49:25.341229687+09:00","level":"INFO","message":"Connector Control WSS 연결 완료","service":"labbit-connector","component":"bootstrap","version":"dev","environment":"production","connector_id":"113369f3-aa16-46e3-8173-068d85e2d5e3","heartbeat_interval_seconds":15,"offline_timeout_seconds":45}
```

### 5) SaaS Web Console 실시간 관측 확인
관리자 웹 콘솔(`https://jinwook.store/connectors`)에서 등록된 커넥터의 상태를 실시간 확인했습니다:
- **이름**: `test123`
- **ID**: `113369f3-aa16-46e3-8173-068d85e2d5e3`
- **제어 평면 상태**: **`Control ONLINE`** (녹색 뱃지)
- **등록 상태**: **`REGISTERED`**
- **버전**: `connector-v0.1.0`
- **최신 수신 Heartbeat**: `2026. 10. 21. 오후 5:49:25` (15초 간격 주기적 수신 정상 관측)
- **제공자 상태**: **`미확인 (별도 검증 필요)`** (D-18 원칙에 따라 OpenStack 온보딩 전 제어 평면 ONLINE과 제공자 READY 분리 검증 완료)

---

## 3. 실환경 검증 스크린샷 증적

### 1) 인스톨러 실행 및 등록 완료 (KT Cloud VM 터미널)
![인스톨러 실행 및 등록 완료](./installer-execution.png)

### 2) SaaS 관리자 웹 콘솔 Control ONLINE 확인
![웹 콘솔 Control ONLINE 확인](./web-console-online.png)

---

## 4. Parent Acceptance 충족 대조

| LBT-61 인수 기준 | 검증 결과 | 확인 근거 |
| :--- | :---: | :--- |
| **Go 미설치 신규 지원 Linux VM에서 원라이너 설치** | **PASS** | KT Cloud D1 Ubuntu 24.04 LTS에서 `curl -fsSL ... \| sudo sh`로 전 과정 단일 실행 완료 |
| **Release Binary 무결성 검증 (SHA256 앵커 핀)** | **PASS** | 릴리즈 SHA256SUMS 및 인스톨러 하드코딩 앵커 핀(`742ac899...`) 이중 대조 통과 |
| **비노출 대화형 일회용 토큰 소비 및 크레덴셜 저장** | **PASS** | `/dev/tty` 에코 없이 입력받아 1회 소비 및 `/etc/labbit-connector/credential`에 `0600` 저장 |
| **systemd 서비스 자동 등록 및 WSS 연결 수립** | **PASS** | `labbit-connector.service` 활성화, WSS 연결 수립 및 15초 주기 Heartbeat 발송 |
| **Web Console 실시간 상태 반영** | **PASS** | `Control ONLINE`, `connector-v0.1.0`, Heartbeat 타임스탬프 갱신 확인 |
| **Provider READY와 제어 평면 ONLINE 분리 (D-18)** | **PASS** | Provider 온보딩 전 임의 READY 표기 없이 `미확인 (별도 검증 필요)` 상태 유지 확인 |
| **민감정보 비노출** | **PASS** | 로그, 터미널 화면, Git 이력 및 PR 본문에 토큰 원문 및 크레덴셜 비노출 확인 |

---

## 5. 결론

LBT-61 (`[CC-04] Connector Customer Installation & Enrollment MVP`)의 모든 서브태스크(LBT-181, LBT-182, LBT-183) 및 상위 인수 기준인 **실제 Linux VM 기반 End-to-End 고객사 설치·등록 Smoke Test**를 완벽하게 검증 완료했습니다.
이에 따라 LBT-61 티켓의 상태를 완료(Done)로 종료 처리합니다.
