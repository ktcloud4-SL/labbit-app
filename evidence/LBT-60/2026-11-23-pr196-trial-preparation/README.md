# PR196 시험 전 읽기 전용 준비

2026-11-23 / source be4d0ba / Draft·미병합.

- `readonly-observations.json`: 실제 AWS·Kubernetes의 선택 필드. 항목별 observed_at을 확인한다. raw_sha256은 메모리에서 조회한 전체 bytes의 hash이며 원본 전체를 보관했다는 뜻이 아니다.
- `readonly-command-inventory.json`, `readonly-collector.py.txt`: 재현용 읽기 명령과 소스. kubeconfig/CLI 경로는 작업 PC의 기존 경로이며 kubeconfig/credential 원문은 포함하지 않는다. 복사·읽기 실행도 최신 신원/대상 대조가 필요하다.
- `.initial.json`: 실제 ECR repo와 Runtime env field 이름을 확인하기 전 첫 조회 기록. `labbit-backend` NotFound를 실제 `backend` image 부재 근거로 사용하지 않는다.
- `preparation-plan.draft.json`: 관측한 대상·baseline, 제안과 미확정 값. 런타임 Policy/서명/승인/실행 input으로 사용할 수 없다.
- `migration-source-manifest.json`: Git LF source 기대 checksum과 Windows working copy raw checksum을 분리. 1–11은 main과 불변. 이미지 내부 bytes/DB checksum은 아직 미검증.
- `schema-readonly.sql`: 신원·적용 이력·테이블 존재를 확인할 읽기 전용 SQL. 공유 DB에서 아직 실행하지 않았다. 실제 DSN/권한·TLS·작업 대상 확인 후 사용한다.
- `preview-fixture-*.json`, `preview-results.json`: 비활성 합성 fixture와 두 CLI의 preview/disabled 4개 검사. 실제 key 생성/서명·S3·DB·Pod 조회나 제어 실행이 아니다. Legacy inspect input의 companion 거부도 구분한다.
- `AUDIT.md`, source/artifact manifest, validation: 소스·검증·독립 리뷰·실환경 미검증 경계.
- `local-checks.json`: 실제 빌드·선택 Go 테스트의 요약이며 stdout 원본 파일이 아니다. `supplemental-readonly-summary.json`의 수동 S3 metadata 조회는 주 command inventory와 분리한다.
- `prepare-packet.py.txt`, `finalize-readonly.py.txt`: 작업 PC에서 사용한 재현 helper. 전자는 같은 HEAD의 기존 preview fixture와 `.local` 빌드가 필요하고, 후자는 현재 AWS/Kubernetes 읽기를 다시 수행하므로 새 관측으로 취급한다. 출력 경로의 기존 packet을 덮어쓸 수 있으므로 원본 복사본을 보존한 별도 checkout에서만 재현한다.

로컬 재현: 같은 HEAD에서 `go build -o .local/pr196-trial-preparation-20261123/labbit-dr-control.exe ./cmd/labbit-dr-control` 및 `go build -o .local/pr196-trial-preparation-20261123/labbit-dr-evidence.exe ./cmd/labbit-dr-evidence`로 빌드한다. `prepare-packet.py.txt`의 ROOT를 별도 checkout에 맞추고 Python으로 실행하면 합성 preview/disabled4개를 확인한다. 실제 운영 입력/키를 이 fixture에 넣지 않는다. 읽기 조회 없이 4개 검사만 재현하려면 `preview-fixture-policy.json`과 `preview-fixture-input.json`을 CLI의 `--policy`, `--input`에 주고, evidence는 `--mode approve --key-file <존재하지 않는 경로>`를 사용한다. 각 CLI에 `--execute`를 추가해도 fixture의 EnableExecution=false 때문에 exit2/EXECUTION_DISABLED이며, preview는 exit0이다.

원본 로그·Secret·DSN·개인키·토큰은 포함하지 않는다. `schema-readonly.sql`은 준비만 했으며 공유 DB SQL·migration은 실행하지 않았다.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·DR HOLD 유지. 설정·배포·merge·push·Slack 전송은 하지 않았다.
