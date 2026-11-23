# PR196 Security CI follow-up — 2026-11-23

## 고정 기준·최신 SSOT·전체 변경

최신 main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, 제품 검증 commit `2dd4e591b19438d2a844e738fdb75a256b21481e`.
앞선 전체 감사와 72개 source/실행 trace는 `../2026-11-23-pr196-publication/AUDIT.md` 및 publication-source.json에 고정되어 있다.
이번 추가 diff는 `.gitleaks.toml:17~25`의 AND allowlist와 이 디렉터리의 Evidence뿐이다. 제품·계약·migration·workflow·Dockerfile·dependency·원본 로그 및 `.gitleaksignore`의 Git 내용은 불변이다.
앞서 확인한 최신 Jira LBT59/60/205·Confluence9666637v4 및 AGENTS/TESTING 기준을 유지한다. 새 제품 요구사항을 추가하지 않는다.

## 수정 전 finding F-02 / Medium — 보안 CI 공개 인증서 오탐

- 위치: `qemu-attempt01.log:291`, `qemu-attempt02.log:288`; 기존 security config에 해당 분류 없음.
- CI Security run45089369442의 Gitleaks8.28은 두 부팅 로그의 `Loaded X.509 cert` 공개 Alpine 커널 인증서 식별자(40hex)를 generic-api-key로 탐지했다. 비밀키·인증 토큰이 아니다. 공개 식별자 분류의 근거는 원본 부팅 로그다. publisher 서명을 검증했다는 주장은 하지 않는다.
- 재현: exact PR commit range origin/main..HEAD, Gitleaks8.28 detect/verbose/redact → 2 findings/exit1. CI/redacted JSON·로그와 로컬 baseline을 보존했다.
- 영향: 제품 실행 영향 없이 Security CI 차단. 원본 Evidence는 임의로 재작성하지 않고 무결성을 유지해야 한다.
- 수정 전 사용자에게 보고했다. 최초 exact commit fingerprint 2건 예외는 이력 검사에서 통과했으나 이후 main의 directory 검사까지 동일 보장을 주지 않으므로 최종 diff에서 제거했다. `.gitleaksignore`는 원래 Git 내용이다.
- 최종 수정: 기존 generic-api-key rule에 **고정 두 로그 경로 AND 정확한 공개 식별자의 전체 rule match**가 동시에 일치할 때만 예외를 추가했다. 다른 지문·다른 경로·같은 파일의 다른 키는 제외하지 않는다. 전체 경로/규칙 비활성화가 아니다.
- 중간 full-line anchor 후보 두 번은 실제 detector에서 일치하지 않아 탐지2/exit1을 보였고 원본을 line-anchor-initial/second로 보존했다. 최종은 regexTarget=match이며 이것들의 실패를 최종 PASS로 숨기지 않는다.

## 요구→코드→자동검증→실제 범위

| 요구·SSOT | 구현 | 실제 테스트 | 공백 |
| --- | --- | --- | --- |
| 원본 보존과 기존 보안 검사 유지; PRE_MERGE_AUDIT/TESTING | .gitleaks.toml의 기존 rule 아래 AND 예외 | history 최종0/exit0, clean archive directory 최종0/exit0 | 모든 비밀 형식 부재를 증명하지 않음 |
| 다른 자격증명 탐지 유지 | exact2path+fixed public match | 같은 allowed file의 다른 synthetic key, 다른 지문, 다른 경로의 같은 지문3건 모두 탐지/exit1(기대) | 실제 credential을 넣지 않았음 |
| 기존 제품72개·원본 Evidence110개 불변 | 기존 source index trace와 raw blob | 독립 reviewer의 canonical/raw/Git 비교 및 primary final index 확인 | 운영자 승인/Fencing/실Pod/실Provider는 기존 미검증 |
| PR/배포 분리 | workflow 변경 없음 | 이전 Backend CI에서 AWS OIDC/ECR/GitOps skip 확인 | 새 commit CI는 PR 본문에서 별도 추적 |

Gitleaks8.28의 match/line·AND/경로 판정은 공식 소스와 대조했다:
[allowlist 형식](https://github.com/gitleaks/gitleaks/blob/v8.28.0/config/allowlist.go),
[checkFindingAllowed](https://github.com/gitleaks/gitleaks/blob/v8.28.0/detect/detect.go).
최종 예외는 단순 문구 변경이 아니라 위 actual detector 실행과 부정 대조로 검증했다.

## 테스트·도구·보안·수명주기

실행 파일은 공식 upstream8.28 Windows ZIP과 upstream SHA256 checksum을 대조했다(provenance). detached publisher signature 검증은 하지 않았다. 앱 dependency/런타임 변경이 아니다.
제품72개는 기존 canonical LF SHA/Git blob과 동일함을 재확인한다. 원본 Evidence110개와 기존 artifact SHA를 보존하며 새 파일은 별도 manifest로 고정한다.
제품 동작 변경이 없으므로 보안 설정 예외 수정만을 위해 Go/Web/PG 전체를 반복하지 않는다. 2dd4e59의 제품 source/test identity를 유지하고 새 최종 commit의 GitHub CI를 별도로 확인한다.
Directory scan은 Git archive HEAD의 fresh export에 최종 config와 신규 Evidence를 덧붙여 수행했다. ignored node_modules/build output은 Actions fresh checkout처럼 포함하지 않았다. 이력 scan과 directory scan은 다른 명령으로 각각 보관한다.
세 부정 사례는 별도 로컬 scratch의 비실재 synthetic key/공개 식별자다. 실제 토큰·외부 서비스 호출이 아니며 결과 JSON/로그는 redact했다. Scratch와 도구는 재현용 로컬 보존, 외부 자원 Cleanup은 해당 없음이다.
AWS·공유 DB·서울·GitOps·Slack·실Provider 조작, merge·ready 전환은 없다.

## 독립 검토와 최종 판정

review.md에서 최종 AND scope·원본 불변·제품 불변·detector 실행을 독립 대조한다. 초기 fingerprint안 검토와 최종 config안을 구분해 기록한다.
검증과 독립 검토를 합쳐 이번 보안 설정 보완은 **Draft PR 반영 가능 범위**로 판단한다. 최종 commit CI는 PR 본문에 commit/run과 구분한다. 이전 CI 실패는 지우지 않는다.
운영 Pause·collector·키 운영·전체 Pod·Writer/Fencing·실제 DR 인수는 기존 미검증이다.
ReplicaLag FAIL·4개 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false 유지.
