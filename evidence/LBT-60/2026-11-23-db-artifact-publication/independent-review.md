# PR196 DB/artifact 보완 — 독립 publication review

2026-11-23 KST, reviewer-only. PRE_MERGE_AUDIT.md 재확인. 실제 AWS/DB/SSM/Secret/Slack/원격 API 호출, 제품/테스트/packet 수정, push/merge를 수행하지 않았다.

## 범위

base753eecbff737409a4c9cc8c542f3c6baa6acf0da / HEADbe4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1 및 미커밋 보완. 기존 상세 감사 `pr196-db-artifact-final-review-20261123.md`와 앞선4개 packet 독립 감사를 재사용한다. 전체 과거 PR387파일을 신규 재감사한 것은 아니다. 이번은 공개 후보 경로/byte 보존, workflow 및 scanner 경계, 새 source와 제출 scope 일치가 대상이다.

## Findings — 수정 전 보고

### PUB-DBART-01 / P2 / Security CI false-positive 설정 불일치

- 근거: `.github/workflows/security.yml:35`는 기본 루트 설정으로 `gitleaks detect --log-opts="origin/main..HEAD"`를 실행한다. :38의 main/dispatch 검사도 기본 `gitleaks dir .`이다.
- 대상 새 파일: candidate-publication의 image-local-verification.json:23 / image-publication-readback.json:24, 각각 runtime-zlib14 하위 동일2개; go.sum.txt:5; independent-review.md:20.
- 공개 checksum5건과 도구 목록 문구1건이 기존 packet 전용 gitleaks-reviewed.toml에서는 허용되지만 루트 .gitleaks.toml에는 해당 예외가 없었다.
- 독립 재현: Gitleaks8.28.0 `dir <candidate-publication> --config <repo>/.gitleaks.toml --redact` → exit1/6건. 외부 보고서 `pr196-db-artifact-publication-default-scan-20261123.json` 보존. 실제 secret 유출 finding은 아니다.
- 영향: 이 Evidence를 새 커밋에 올리면 PR Security CI의 기본 검사에서 동일 false-positive로 실패할 가능성이 높다. packet 안의 별도 reviewed config 성공을 CI 성공 가능성으로 바꾸면 안 된다.
- 수정 권고: 고정 경로 AND 정확 literal의 좁은 rule allowlist, 알려진6건은 허용하고 경로 변경/실제값 변경은 탐지되는 negative regression. 원본 Evidence를 수정하거나 scanner 전체 규칙을 끄지 않는다.
- 상태: 구현자에게 수정 전 즉시 전달. 보완 후 재검토/최종 index 확인 대기.

## 이미 확인한 공개/배포 경계

- 새 go.yml 변경은 contents:read와 stdlib Python 오프라인 fixture 연결이다. 신규 AWS/registry/GitOps 설정 변경은 없다.
- 기존 backend delivery는 pull_request에서 build하지만 `push:false`; AWS OIDC/ECR 로그인과 GitOps image 갱신은 PR 조건에서 skip한다. GHCR login 단계 자체는 기존대로 PR에서도 실행되므로 외부 인증이 전혀 없다는 표현은 사용하지 않는다.
- Web delivery job은 pull_request에서 skip한다. Connector delivery는 connector 태그/수동 실행만이다. main/ref merge·tag·dispatch는 이번 사용자 승인의 대상이 아니며 이후 별도 영향 판단이 필요하다.
- PR 테스트 실행은 disposable CI Postgres/Valkey·Mock fixture 사용이다. 변경 브랜치 push를 서울 배포/실 DR 실행으로 연결하는 경로를 이번 workflow에서 찾지 못했다.
- Evidence 상위 `.gitattributes`의 `* -text`를 check-attr로 확인했다. 원본 byte/seal은 유지하고 소스의 core.autocrlf 정규화와 구분해야 한다.
- sealed 새packet80/80 exact size/SHA·safe unique path·coverage와 manifest SHA347a8929e39bd0785ece9bd804537396e895d1436a2353e156a9f2d60e777d48를 앞선 최종 패스에서 독립 확인했다.

## 이후 확인

원본 source21개/4prior packet/새80개 packet의 staged index blob 정합 및 PUB-DBART-01 해결 후 최종 공개 범위의 조건부 판정을 추가한다. 감사는 push/merge/실배포 승인과 별개다.


## 증분 확인 — PUB-DBART-01 해결

루트 `.gitleaks.toml`의 generic-api-key rule에 고정6경로 AND exact literal로 제한한3개 예외가 추가됐다. 실제 Git migration000010 원본의 SHA, 공개 module h1, 도구 목록 문구임을 독립 확인했다. 현재 설정으로 동일6원본을 다시 검사해 exit0/0finding을 확인했다. 제출14개 회귀 결과의 report SHA/조건 수가 맞고, reviewer도12개 literal/path 변조 fixture를 각각 실제 Gitleaks로 다시 검사해12/12 탐지가 유지됨을 확인했다. 전역 규칙 비활성화나 광역 path 예외는 없다. PUB-DBART-01은 해소됐다.

publication/AUDIT 및 ssot-refresh의 최신 LBT-60 #11271도 확인했다. 과거 Stage2C 네 NOT_VERIFIED를 이후 source/읽기 검증으로 소급 PASS하지 않으며, 새 Runtime source와 과거 v0.1.14 이미지·legacy rollout을 구분한다. GitHub 원격 최신성은 구현자가 직접 조회한 기록을 사용했고 reviewer는 네트워크 호출하지 않았다.

## PUB-DBART-02 / P2 — 공개 helper 재실행 시 own manifest/index의 stale self-reference

- 파일: ignored local `.local/publish_db_artifact.py:61–69`, entries 생성 루프 및 뒤의 index-verification/artifact-manifest 덮어쓰기 경계(보정 후 소스 위치). 제품 실행 소스가 아니라 공개 감사 도구다.
- 재현: 이전 publication 폴더가 이미 존재한 상태에서 helper를 재실행하면 allowed/staged에 own index-verification.json와 artifact-manifest.json가 포함된다. 두 파일의 기존 byte hash를 entries에 담은 뒤 두 파일을 새로 생성하므로 해당 행은 현재 파일/index와 달라진다. 출력 count도 실제384에2를 중복 가산했다.
- independent check: 전체 실제 staged raw Evidence/canonical LF source blob은 일치했으나, 제출 index-verification의 own artifact-manifest 행과 실제 Git blob 대조에서 실패했다. 제품/source21/원본80packet 손상은 없다.
- 영향: 제출 감사 기록이 자체 기준으로 검증되지 않아 공개 source→index 정합성의 근거로 그대로 쓸 수 없다. 구현자도 발견해 commit/push 전에 수정 전 보고했다.
- 수정·회귀: own2파일은 entry 목록에서 제외하고 별도 최종 actual index 전체384개와 바이트 비교. 재실행 후에도 entry self-reference가 없어야 하며 실제 file count를 보고한다. 보정 후 동일 독립 검증 재실행 대기.

## 최종 증분 — PUB-DBART-02 해결 및 staged 전체 독립 검증

보정 후 같은 독립 검증 스크립트를 다시 실행했다. 실제 staged384개에 대해 `git cat-file --batch`에서 직접 가져온 각 blob과 파일 원본을 대조했다. source22개(기존21개+root scanner config)는 검토한 canonical LF, Evidence362개는 exact raw bytes로 모두 일치했다. index-verification에는 self2개를 제외한382개만 들어 있으며 각 기록 raw SHA/Git SHA/blob ID가 실제 index와 같고, 최종2개도 별도 byte 대조에서 일치했다. PUB-DBART-02는 해소됐다.

- 허용 scope는 원본 source21개, root .gitleaks.toml, 기존4개 sealed packet 및 새 DB/artifact80개 원본 packet, 공개 감사 packet뿐이었다. unrelated 파일·.local·pycache·pyc·키/DSN 디렉터리를 stage하지 않았다.
- staged 그룹은 source22 / candidate69 / collector102 / db-validation81 / migration75 / trial-preparation25 / publication10개였다. 당시 manifest의 historical root source 참조와 packet 내부 파일 수를 구분했으며 합계를 임의로 모두 더하지 않았다.
- six packet manifest와 대응 원본/index가 일치했다. 이전 manifest의 비Evidence source 참조는 당시 historical source이며 현재 제품 byte와 같다고 주장하지 않는다. 이 historical 예외는 새 source21개 및 현재 packet 검증에는 적용하지 않았다.
- raw Evidence의 whitespace를 보존하기 때문에 전체 diff-check의 원본 출력·diff snapshot 경고를 실패 기록으로 남겼고, 제품/설정/문서의 개별 검사 통과와 구분했다. 사용자 원본을 lint 목적으로 고치지 않았다.
- 구현자의 staged 공개 snapshot+gzip 해제 본문을 대상으로 한 root config scanner 결과는0건이다. reviewer는 그 결과/스크립트 범위를 읽었고, root config의 알려진6원본 및12개 negative 조건은 별도로 직접 재실행했다. fresh remote Security CI 성공을 미리 주장하지 않는다.

**현재 source 및 staged 공개 범위에서 미해결 확정 차단 finding 없음. 승인된 기존 Draft PR 업데이트 범위의 공개 검토는 PASS_SCOPED다.** 실제 서울/AWS/공유 DB 변경·실제 제어·merge/DR GO를 허용하거나 인수하는 판단이 아니다. 제품 검증은 앞선 독립 코드 감사와 동일 source의 회귀 결과를 재사용했다. 새 코드 전체 테스트를 다시 실행하지 않았다.

이 보고서 복사 후 새로 생기거나 갱신되는 공개 감사 metadata/scan/index/manifest만 최종 seal·전체 index byte 대조가 추가로 필요하다. 기존 source21개/원본80개/과거 sealed packets는 그대로 유지해야 한다. 보고서에 공개 checksum의 고entropy literal을 중복 복사하지 않고 고정 원본 경로·근거 유형을 참조했다.

