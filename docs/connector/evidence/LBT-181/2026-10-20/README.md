# LBT-181 · Connector 공식 인스톨러 승인 SHA256 고정 검증 및 동시 변조 방어 증적

- **실행일**: 2026-10-20 KST
- **대상 티켓**: [LBT-181](https://samsunglions.atlassian.net/browse/LBT-181) / 연계 [LBT-183](https://samsunglions.atlassian.net/browse/LBT-183) (PR #133 리뷰 후속 보완)
- **릴리즈 버전**: `connector-v0.1.0`
- **대상 바이너리**: `labbit-connector` (Linux/amd64)
- **승인된 기대 SHA256**: `742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346`
- **검증 환경**: KT Cloud D1 `labbit-connector-vm` (Ubuntu 24.04 LTS, Go 미설치 순수 환경, IP: 211.184.227.97)

---

## 1. 보완 배경 및 구현 내용

GitHub Releases는 기본적으로 자산 수정이 가능(`immutable=false`)하므로, 릴리즈 서버의 `SHA256SUMS` 파일만 신뢰할 경우 공격자가 바이너리와 체크섬 파일을 동시에 변조하는 공급망 공격(Supply Chain Attack)에 노출될 수 있습니다.

이에 따라 공식 인스톨러(`scripts/installer/install.sh`)에 다음 2단계 방어 체계를 적용했습니다:

1. **1단계 (릴리즈 원장 무결성 검증)**: GitHub Releases의 `SHA256SUMS`를 다운로드하여 다운로드된 바이너리와 대조 (누락/단독 변조 차단).
2. **2단계 (승인된 암호화 앵커 고정 검증)**: 인스톨러 스크립트 내부에 하드코딩된 불변 승인 해시(`DEFAULT_EXPECTED_SHA256="742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346"`)를 대조하여, **릴리즈 서버에서 바이너리와 `SHA256SUMS`가 함께 변조되더라도 승인 해시와 다르면 즉시 설치를 중단(Fail-Closed)**.

---

## 2. 검증 결과 요약

### 1) 회귀 및 보안 시나리오 테스트 (`scripts/installer/install_test.sh`)

KT Cloud Ubuntu 24.04 VM에서 12개 테스트 스위트 전체 실행 결과: **12/12 PASSED**

```text
==> Running install.sh behavioral test suite...
--- Test 1: Non-root execution check ---
PASS: Non-root execution correctly rejected.
--- Test 2: Unsupported OS & Architecture check ---
PASS: Unsupported OS and Architecture correctly rejected.
--- Test 3: Concurrent lock ownership regression check ---
PASS: Lock directory preserved when second concurrent instance exits.
--- Test 4: SHA256SUMS 404 fail-closed check ---
PASS: SHA256SUMS 404 correctly aborted installation (fail-closed).
--- Test 5: TTY/stty validation & environment token prohibition ---
PASS: Environment token rejected, inaccessible TTY rejected, and stty -echo failure safely aborted with TTY state restored.
--- Test 6: Existing credential, connector.id & stopped service protection ---
PASS: Existing credential, connector.id, and stopped service state preserved without automatic restart.
--- Test 7: Full 201 Enrollment execution, file layout & uncertain outcome handling ---
PASS: Full 201 Enrollment happy path, SHA256SUMS verification, file layout, and uncertain outcome handling verified.
--- Test 8: Runbook upgrade SHA256SUMS verification command check ---
PASS: Runbook upgrade SHA256SUMS verification command succeeded.
--- Test 9: SIGTERM signal trap termination and lock cleanup ---
PASS: SIGTERM termination and lock cleanup verified.
--- Test 10: Credential save failure recovery guidance check ---
PASS: Credential save failure recovery guidance and secret scrubbing verified.
--- Test 11: Insecure HTTP SaaS URL rejection (HTTPS enforcement) ---
PASS: Insecure HTTP protocol and non-https schemes correctly rejected.
--- Test 12: Approved Release SHA256 Pinned Verification & Attack Defense ---
PASS: Case 12A - Approved pinned checksum successfully verified and binary installed in default execution.
PASS: Case 12B - Tampered binary aborted on release SHA256SUMS check.
PASS: Case 12C - Dual-tampering attack defense verified (aborted on approved pinned checksum mismatch).
==> All install.sh behavioral & regression tests passed successfully!
```

### 2) 기대 해시 일치 / 불일치 세부 동작 실측

| 시나리오 | 조건 | 동작 결과 | 판정 |
| :--- | :--- | :--- | :---: |
| **Case 12A (기대 해시 일치)** | 기본 실행 시 실제 승인 바이너리(`742ac899...`) 다운로드 | `SHA256 verified successfully against release SHA256SUMS`<br>`SHA256 verified successfully against approved pinned checksum`<br>바이너리 정상 설치 (`/usr/local/bin/labbit-connector`) | **PASS** |
| **Case 12B (단독 불일치)** | 바이너리 변조, 릴리즈 SHA256SUMS는 정품 해시 유지 | `::error::SHA256 mismatch against release SHA256SUMS!` 출력 후 즉시 종료, 바이너리 미설치 | **PASS** |
| **Case 12C (동시 변조 공격 방어)** | **공격자가 릴리즈 서버의 바이너리와 SHA256SUMS를 모두 변조** (`ffffffff...`) | 1단계 통과 후 2단계에서 `::error::SHA256 mismatch against approved release checksum! Expected: 742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346, Got: ffffffff...` 출력 및 즉시 중단, 바이너리 미설치 | **PASS** |

### 3) 실제 GitHub Releases 라이브 다운로드 실측

KT Cloud VM에서 실제 GitHub Releases 자산(`connector-v0.1.0`)을 대상으로 인스톨러 직접 구동 실측:

```text
==> [1/6] Running pre-flight system checks...
==> [2/6] Downloading Labbit Connector binary (connector-v0.1.0)...
==> [2/6] Verifying binary integrity (SHA256)...
    SHA256 verified successfully against release SHA256SUMS: 742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346
    SHA256 verified successfully against approved pinned checksum: 742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346
    Installed binary to /usr/local/bin/labbit-connector
==> [3/6] Requesting Enrollment Token...
Enter Labbit Connector Enrollment Token:
```
실제 배포된 GitHub Release 자산의 해시와 인스톨러의 고정 승인 해시가 완벽히 일치하여 성공적으로 다운로드 및 배치됨을 확인했습니다.
