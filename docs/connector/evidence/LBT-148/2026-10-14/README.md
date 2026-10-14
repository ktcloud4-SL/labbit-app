# LBT-148 최신 main 통합 및 최종 검증

2026-10-14 KST 실행. Base `45dfc486f2dab72ac705310e3f4f3acead1128f0`, 최종 코드/test `f510f9d9d24ebbde261614fdc9fabb3bc64146c6`.

실제 local OpenStack PTY/CC02 `-race` **133.81초 PASS**. 기존 GitHub main MockSaaS/TerminalRelay에 production Connector Control/Data handler를 연결하고 VM SSH/PTY를 실제 실행했다. IO/size/resize/close, 다른 PTY와 SFTP 보존, reconnect/shell state, harness grace3초, final output/exit7, SSH 단절·종료, tracked7개 ABSENT 및 사후 서버0 확인. 임시 keypair/Management SG 잔여도0/0이다. production grace60초는 유지했다.

최종 Go unit3361PASS/12SKIP, Connector race692PASS/11SKIP, 반복420PASS/0SKIP; fmt/vet/build, Web13file/163tests·typecheck/lint/build, contracts8schemas·OpenAPI·Reset11tests 모두 PASS. PASS 숫자는 Go subtest event를 포함한다.

실환경/Web/Contracts는 `efc2258`에서 실행했고 최종 source와 production/해당 boundary blob이 동일하다. 이후 변경은 loopback SSH 회귀 fixture 한 파일뿐이며 최종 Go 검사를 재실행했다. 소스 관계와 transient source stability false를 manifest에 명시했다. 2026-10-10 기록은 historical source 기록으로 보존했다.

참조: [최종 감사](pre-merge-audit.md), [독립 리뷰](independent-review.md), [실행 요약](validation-summary.json), [source pin](source-manifest.json), [원본 로그](validation-logs.zip), [checksums](artifact-checksums.json), [민감정보 검사](secret-scan.json).

Archive의 `first-pass`는 요약의 `logs`, `premerge-attempt`는 `final-logs`, `final-auto`는 `verified-logs`에 대응한다. `negative-logs`와 `runner-env-logs`는 같은 이름을 사용한다.

이 디렉터리의 `.gitattributes`는 evidence 원본 바이트를 유지하여 운영체제별 Git 줄바꿈 변환으로 SHA256이 바뀌지 않게 한다.

## 재현

이 code source 또는 동일 production/test blob의 evidence commit을 checkout한다. Go/GCC/git, 지원 Node와 pnpm, Python을 준비한다. Windows에서 Makefile의 개별 target을 `validate.py`가 동일 command로 실행하며 fmt는 CRLF를 LF-normalize하여 비교한다. Web은 tracked npm package-lock을 `pnpm import` 후 frozen install하고 생성된 pnpm lock을 제거한다. npm lock 자체는 변경하지 않는다.

Python validator는 현재 Contracts workflow와 같은 버전을 준비한다:

```powershell
python -m pip install PyYAML==6.0.3 jsonschema==4.26.0 openapi-spec-validator==0.9.0
./reproduce.ps1 -Stage auto
./reproduce.ps1 -Stage web -NodeBin '<Node bin>' -PnpmExecutable '<pnpm.cmd>'
./reproduce.ps1 -Stage contracts
./reproduce.ps1 -Stage negative
./reproduce.ps1 -Stage env-check
```

`-PythonExecutable` 또는 `-ValidatorDir`로 별도 Python/설치 directory를 지정할 수 있다. negative는 검증된 수정 전 두 production 파일을 overlay하여 race/leak/panic의 **expected FAIL**을 확인한다. 옛 Git object가 없으면 archive에 포함된 같은 파일을 사용한다. repository 코드를 변경하지 않는다.

실환경 stage는 자원 생성·삭제를 포함하므로 명시적으로 준비된 폐기용 local cloud에서만 실행한다. 인증 설정·개인키는 로컬 파일로 준비하고 repository/evidence에 복사하지 않는다. 아래 값은 실행한 fixture 입력이며 private endpoint/password는 기록하지 않는다.

```powershell
$env:OS_CLOUD = '<configured test cloud name>'
$env:LABBIT_PROVIDER_CONFIG_FILE = '<local clouds.yaml>'
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = '<local private key>'
$env:LABBIT_OPENSTACK_SSH_USERNAME = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_IMAGE = 'ubuntu'
$env:LABBIT_OPENSTACK_TEST_FLAVOR = 'm1.small'
$env:LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK = 'sharednet1'
$env:LABBIT_OPENSTACK_TEST_SSH_CIDR = '172.16.8.1/32'
$env:LABBIT_OPENSTACK_TEST_LAB_CIDR = '198.20.0.0/24'
./reproduce.ps1 -Stage cloud
./reproduce.ps1 -Stage residual
```

SSH_CIDR은 실행 환경의 Connector source에 맞춘다. cloud stage의 전후 discovery와 tracked resource absence, residual stage의 임시 fixture inventory를 확인한다. 자동 stage는 상속된 OpenStack과 KT Cloud의 test/audit/integration opt-in을 모두 비활성화한다. cloud stage에서는 PTYCC02와 읽기 전용 OpenStack discovery만 다시 활성화한다. env-check는 합성 환경과 command stub으로 이 경계를 검사하고 실제 클라우드를 호출하지 않는다.

실행한 비민감 입력은 cloud명 `labbit-test`, image `ubuntu`, flavor `m1.small`, management network `sharednet1`, SSH CIDR `172.16.8.1/32`, lab CIDR `198.20.0.0/24`다. VM command/output은 메모리에서 assert하고 로그에 저장하지 않는다.

실제 Browser/SaaS C2, KT D1, production SFTP Tree/Read/Save, PostgreSQL tagged integration, 강제 재기동·장시간 부하는 미실행이다. 적용/제외 이유와 잔여 위험은 감사 기록을 참조한다. 최종 감사 통과는 push·PR 생성·merge·배포의 승인이 아니다.
