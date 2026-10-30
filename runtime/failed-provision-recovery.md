# FAILED Provision 운영자 복구

의미와 권한의 원본은 `contract.yaml`의 `artifacts.failed_provision_recovery`입니다.
이 절차는 단일 VM KT D1, generation 1, 원래 결과가 전부 유실되어 자원 기록이 0개인
PROVISION HOLD만 지원합니다. READY 복원, Reset, UNKNOWN/성공 receipt, 부분 목록은 지원하지 않습니다.

운영자는 기존 DB 접근 권한과 검증된 Connector 관리 SSH 접근 권한을 모두 가져야 합니다.
SaaS 관리자·강사 역할이나 GitHub 권한은 이 권한을 부여하지 않습니다.
DB는 Project ID를 저장하지 않으므로 운영자가 환경의 Project와 Connector/ProviderConnection 관계를
직접 대조하고 `--project`와 dry-run의 `Target.ConnectorID`를 확인해야 합니다.
사용자 JSON 파일을 검증된 커넥터 원장 대신 입력하는 경로는 없습니다.

1. `go build ./cmd/labbit-recover-failed-provision`과 `go build ./cmd/labbit-recovery-evidence`로 같은 검토 소스에서 빌드합니다.
2. 복구 전에 기존 `labbit-migrate up`으로 additive migration 000011을 적용합니다.
   기존 Backend query와 호환되며 Backend·Connector 서비스 교체/재시작은 필요하지 않습니다.
3. evidence binary와 고정 executable wrapper를 Connector에 설치합니다. wrapper는 원래 서비스의 보호된
   Provider 환경을 공급하고 원장 소유자로 binary를 exec해야 합니다. wrapper·상위 디렉터리·환경·원장은
   trusted operator만 수정할 수 있어야 합니다. SaaS credential이나 DB DSN은 evidence process에 필요하지 않습니다.
   본문·키·환경 파일을 로그에 출력하거나 셸에서 임의 문자열로 평가하지 않습니다.
4. 다른 mutation을 시작하지 않는 작업 시간에 dry-run을 실행합니다. evidence는 기존 CIDR `LOCK`으로
   원장과 Provider 조회를 직렬화합니다. LOCK 획득 실패는 HOLD 보존이며 stale LOCK을 자동 제거하지 않습니다.

```sh
labbit-recover-failed-provision \
  --item ORIGINAL_OPERATION_ITEM_UUID --project VERIFIED_PROJECT_ID \
  --operator INFRA_OPERATOR_LABEL --reason LBT-197 \
  --ssh-address CONNECTOR_HOST:22 --ssh-user OPERATOR_ACCOUNT \
  --ssh-host-alias EXISTING_PINNED_ALIAS \
  --ssh-known-hosts /protected/known_hosts --ssh-private-key /protected/operator_key \
  --ssh-command /usr/local/libexec/labbit-recovery-evidence
```

DB 설정은 기존 `LABBIT_ENVIRONMENT`·`LABBIT_DATABASE_DSN_FILE` 계약을 사용합니다.
production에서 DSN 파일은 필수입니다.
복구 DB 연결은 CA와 hostname 검증을 모두 요구합니다(`sslmode=verify-full`). SSM 등의 기존 터널을 쓰면
`--database-tunnel 127.0.0.1:LOCAL_PORT`를 지정할 수 있으며 DSN의 원래 DB host·CA·TLS ServerName은 유지합니다.
터널 주소에는 literal loopback IP만 허용하고 SSL 검증을 완화하지 않습니다.
SSH host alias는 known_hosts의 기존 기본 22번 포트 alias입니다.
기존 pin을 자동 등록/교체하거나 SSH agent·비밀번호 인증으로 fallback하지 않습니다.
SSH command에는 absolute executable 경로만 허용하며 요청은 JSON stdin으로 전달합니다.

dry-run은 DB를 쓰지 않습니다. 출력에는 작업/소유권 ID·Snapshot/원장/receipt hash·host key·5개 자원·계획 hash가
들어가며 credential, 원문 Snapshot/StartupScript, raw Provider 응답은 포함하지 않습니다.
내용이 맞으면 같은 인자에 `--apply-plan DRY_RUN_PLAN_SHA256`을 추가합니다.
새 nonce로 증거를 다시 읽고 계획이 일치해야 합니다. 서버 상태 등 계획에 포함된 사실이 바뀌면 새 dry-run을 검토합니다.

반영은 하나의 transaction에서 현재 HOLD·세대·소유관계·idle lease를 재검증하고 독점 lease를 잡아
exact 자원 5개와 원래 item의 FAILED, Instance ERROR, parent/Execution 집계와 감사 metadata를 저장합니다.
동시 실행·다른 소유자 ID·만료 lease·계획 변경은 전체 rollback이며 PENDING/RUNNING 재실행으로 바꾸지 않습니다.
완료된 복구의 반복 실행은 거절됩니다. 결과 출력 전에 연결이 끊기면 DB의 `failed_provision_recoveries`와
원래 item 상태를 읽어 결과를 확인하며 blind apply/Provider Create/Delete를 반복하지 않습니다.

복구 후 기존 강사 권한 HTTP Cleanup을 실행하고, 모든 exact-ID 부재·CIDR 원장 retirement와 baseline 자원 보존을 확인합니다.
복구 명령 자체는 VM을 삭제하지 않습니다. 원장 `LOCK`이 crash로 남았거나 조회가 불완전하면 원래 HOLD를 유지하고
독점 실행·정확한 자원 상태를 검토한 후 운영자가 후속 조치를 결정합니다.
