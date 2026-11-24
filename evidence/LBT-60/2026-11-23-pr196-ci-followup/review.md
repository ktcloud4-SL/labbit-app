> **최종 정정:** 아래 최초 exact-fingerprint `.gitleaksignore` 검토는 중간 시도에 대한 역사적 기록이다. 그 안은 최종 diff에서 제거됐고 `.gitleaksignore`는 HEAD 원본으로 복원됐다. 최종 인수 범위는 문서 끝의 **최종 AND allowlist 증분 재검토**를 따른다. 제품/원본 로그 검토는 유효하되 최초 예외 방식의 최종 적용 주장은 승계하지 않는다.
# PR196 CI 후속 독립 검토 — 2026-11-23

Reviewer `/root/audit_pr196_regression`, read-only. PRE_MERGE_AUDIT 지침과 TESTING의 비밀/원본 Evidence 보존 경계를 적용했다.

## 기준과 수정 전 finding

- 코드 기준 `2dd4e591b19438d2a844e738fdb75a256b21481e`, main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- Parent가 사용자에게 먼저 보고한 CI 차단 사유: Gitleaks8.28.0이 Alpine Linux kernel X.509 공개 인증서 identifier를 generic-api-key로 오탐.
- 실제 줄: `qemu-attempt01.log:291`, `qemu-attempt02.log:288`의 `Loaded X.509 cert 'alpinelinux.org: Alpine Linux kernel key: ...'`.
- 이 값은 같은 공개 kernel cert의 40hex 식별자다. AWS/Slack/사용자 비밀이나 private key가 아니다.
- Parent의 교정은 `.gitleaksignore`에 commit/file/rule/line 정확 fingerprint 2건만 추가하고 원본 로그를 그대로 보존한 것이다.

## 독립 확인

1. `.gitleaksignore`의 기존1건은 불변, 새2건은 baseline JSON의 두 fingerprint와 정확히 일치한다. 총3개이며 wildcard/path-wide/regex/rule disable이나 broad allowlist 없음.
2. CI후속 `source.json`의 ignore rawSHA `4c80c1d0ed6e34c2a1ad1568e895c0ecdb4478a3b5c5873f4c874a97741fa562` 현재 일치.
3. 두 원본 로그의 현재 raw Git blob은 각각 `bc40f8c9214f492c1b83400fbefd551faa2db69e`, `97402783c3f48ceda4b0d8a24018a5995d9571bd`이고, `2dd4e59:<path>`의 committed blob과 일치한다. 로그를 redact/재작성하지 않았다.
4. `publication-source.json` 72개 모두 current raw size/SHA256 및 HEAD expected Git blob 일치. 제품72source 변경0.
5. 현재 tracked diff는 `.gitleaksignore`뿐이고 새 untracked는 CI후속 Evidence 디렉터리다. security workflow/제품/DB/runtime/dependency 설정 변경 없음.
6. baseline JSON은 exactly2 findings, commit/path/rule/line이 예외2건과 일치. commands.json은 같은 범위 `origin/main..HEAD`에 baseline exit1→final exit0을 기록하고 finalJSON은 `[]`다.
7. reviewer가 독립 실행: `gitleaks.exe detect --log-opts=origin/main..HEAD --verbose --redact --report-format=json --report-path=C:/Users/user/.codex/tmp/pr196-independent-gitleaks-20261123.json`. exit0, 5 commits/~28.50MB, no leaks found, report `[]` 확인. parent Evidence나 Git 상태를 변경하지 않았다.
8. 설치 archive의 실제SHA256 `da6458e8864af553807de1c46a7a8eac0880bd6b99ba56288e87e86a45af884f`가 보관한 upstream v8.28.0 checksums.txt의 Windows x64 항목 및 provenanceJSON과 일치한다. HTTPS release checksum 검증이며 publisher detached signature 검증은 아니다.

## 판정·한계

검토 범위에서 새 confirmed blocking finding 없음. 정확 fingerprint 예외는 확인된 공개 인증서 identifier 두 건에 한정되어 있고 원본 Evidence와 제품소스를 보존한다. PR의 기존 Gitleaks git-history 검사와 같은 도구/범위에서 독립 재실행도 PASS다.

- 이 실행은 working copy의 최신 ignore를 적용한 history scan이다. 새 예외/Evidence commit을 포함한 원격 exact-head CI는 parent가 후속 확인해야 한다.
- security.yml의 main push는 `gitleaks dir .`로 history scan과 다르다. 이번 실행은 main merge·directory scan·전체 Security Scan 성공을 주장하지 않는다. merge는 사용자 금지 상태다.
- Gitleaks 미탐 부재는 보장하지 않는다. broad disable을 하지 않았고 이미 탐지된 두 문자열의 공개 문맥을 검증했다.
- CI후속 새 JSON/log/provenance는 기존2dd 제품 전체 audit와 별도 incremental 범위다. 전체Go/PG/Linux fixture를 다시 실행하지 않았다.
- AWS·서울 서비스·공유DB·Provider·Slack·push·merge 실행0. 기존 ReplicaLagFAIL/필수4NOT_VERIFIED/DR HOLD 유지.
## 최종 AND allowlist 증분 재검토 — 2026-11-23

### 최종 diff와 원본

Parent의 후속 검토에서 exact commit fingerprint 예외는 PR history scan에는 적용되지만 main의 directory scan에 같은 보장을 주지 않는 한계가 확인됐다. Parent는 이 안을 제거하고 `.gitleaksignore`를 원본 복원했다. full-line target 후보 두 번의 미일치(2findings/exit1)도 중간 실패 Evidence로 보존했다.

최종 `.gitleaks.toml:17~25`는 기존 generic-api-key rule 아래 새 allowlist 한 개이며 다음 **AND** 조건을 원문에서 확인했다.

- path regex는 `evidence/LBT-60/2026-11-21-linux-offline/qemu-attempt01.log` 또는 `qemu-attempt02.log`의 정확한 suffix 두 개.
- `regexTarget = "match"`, regex 시작/끝 anchor로 detector의 전체 match가 고정 공개 Alpine kernel identifier와 정확히 일치해야 함.
- 기존 default rules와 기존 manifest 예외는 유지. wildcard directory 제외, detector/rule off, 임의40hex 허용 없음.
- 현재 ignore Git blob은 HEAD:.gitleaksignore와 동일.
- 최종 TOML raw SHA256 `4ba6e4465d0672b0e6a1762d474901f7bee34048e9f6967dfff189350904d2bf`, clean-export config SHA도 동일.
- publication-source 제품72개 current rawsize/SHA와 HEAD Gitblob 72/72 일치. 두 QEMU 원본 rawblob과 2dd committedblob도 각각 일치. 제품·원본 로그 변경0.

### 독립 직접 실행

동일 Gitleaks8.28.0으로 reviewer가 최종 config 상태에서 실행했다. 모든 결과 출력은 repo밖 scratch에 보관했다.

| 실행 | 실제 결과 | 판정 범위 |
|---|---|---|
| Repo `detect --log-opts=origin/main..HEAD --verbose --redact` | exit0, 5commits/~28.50MB, JSON[] | PR history scan |
| clean-export `dir . --verbose --redact` | exit0, ~92.27MB, JSON[] | Git archive 2dd+최종config+당시새Evidence fresh export |
| scope-control `dir . --config=<최종TOML> --gitleaks-ignore-path=<원본ignore>` | exit1(기대), 정확3findings | 다른synthetic key/잘못된fingerprint/다른경로동일fingerprint 모두 여전히 탐지 |

부정대조 JSON에서 qemu-attempt01.log:2의 다른 generic API key, qemu-attempt02.log:1의 다른 kernel identifier, unrelated.log:1의 동일 public identifier가 각각 탐지됨을 확인했다. 실제 운영 비밀을 사용하지 않았고 redact 출력이다.

### 문서 추적성 finding과 해결

초기 재검토 시 `AUDIT.md:8` 및 `source.json`이 제거된 exact-fingerprint 변경을 최종안처럼 설명한 추적성 불일치를 P3 문서 finding으로 parent에게 수정 전 보고했다. Parent 수정 후 최신 원문을 다시 확인했다.

- AUDIT는 최종 변경을 TOML17~25로 특정하고, fingerprint 중간안 제거/line-target 실패2회/최종match로 수정의 순서를 구분한다.
- source.json은 modified_file `.gitleaks.toml`, 위 rawSHA, canonical LF SHA/expectedGitblob, ignore unchanged, source72 불변을 기록한다.
- `commands.json`의 최초 baseline/final은 역사적 시도이며 `gitleaks-history-final`/`gitleaks-directory-final`/`scope-negative-final`이 최종 검증이라는 문서 구분이 있다.

따라서 해당 문서 finding은 해결됐다. 새 confirmed blocking finding 없음.

### 최종 판정 및 한계

검토한 최종 보안 설정 delta는 정확한 두 경로 AND 고정 공개 identifier match에 한정되고, 다른 키/다른값/다른경로 탐지는 유지된다. 제품72개·원본 로그 불변과 실제 history/directory/negative검사를 독립 확인했다. **최종 AND 예외 방식의 Draft PR 반영 범위에서 추가 차단 결함 없음.**

clean-export의 이후 추가되는 reviewer 보고서/최종metadata까지 포함한 exact final commit 원격 CI는 parent가 별도 확인해야 한다. detector가 모든 비밀을 찾는다는 의미가 아니며, allowlist는 공개 식별자가 두 고정 경로에 등장하는 경우를 허용하는 정책이다. 새 제품 요구/운영허가/실DR검증을 추가한 것이 아니다.

이 reviewer는 repo 파일·index·commit·AWS·서울·공유DB·Slack·Provider를 변경하지 않았다. scratch 보고서와 detector 결과만 생성했다. 기존 full audit의 제품 범위는2dd에 유효하며 이 delta 때문에Go/PG/Web 전체 재실행을 요구하지 않는다. 원격CI·미병합Draft·기존FAIL/4NOT_VERIFIED/HOLD는 구분해 유지한다.