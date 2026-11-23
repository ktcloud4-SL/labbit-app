# PR196 DB·원본 보관·제한 시간 연결

Owning contract: [Runtime 0.1.15](../../runtime/dr-control.md). LBT-60/PR196 후보의 운영 재현 절차다.

## DB 운송

직접 연결은 그대로다. Windows에서 승인된 SSM 원격 포트 포워딩을 사용할 때만 보호된 Policy에
`DatabaseTunnelPort=35432`를 지정한다. DSN은 localhost로 바꾸지 않고 실제 RDS host/5432,
DB/User와 `sslmode=verify-full`·CA를 유지한다. 제품 DatabaseConfig는 실제 대상의 TCP만
`127.0.0.1:35432`로 전달한다. 로컬 포트 연결을 RDS 신원 증명으로 인정하지 않는다.
운영자는 exact 계정/RDS ARN·ResourceId/EC2/document/remote host·5432와 로컬 포트 독점을 확인한다.
끝나면 exact SSM SessionId의 History=Terminated/Active 부재, 자식 프로세스와 포트 종료를 확인한다.
Secret 원문은 RAM에서만 읽고 공유 Secret·SG·NetworkPolicy·DB 설정을 변경하지 않는다.
11/23 실측은 제품 VerifySQL·schema/inventory 읽기 후 ROLLBACK이며 실제 제어 변경은 없다.

## 공개 승인·관측 원본 보관

`scripts/dr_control_artifact_store.py`는 companion이 만든 공개 Signed 원본을 취급한다.
개인키·DSN·Secret 파일을 주지 않는다. shape와 보관 성공은 서명/승인 인수가 아니며,
제어 consumer가 공개키·purpose·최종 요청·대상·현재 TTL을 다시 검증한다.

Plan 필수 필드: version=`dr-control-artifact-store/v1`, account, actor(exact IAM user ARN), profile,
aws(절대경로), region, bucket, prefix, key, sha256, size, not_before, valid_until, retain_until, owner,
enable_upload. 날짜는 UTC `Z`, 작업창 최대1시간, 보존 검토는 창 종료보다 최소30일 이후다.
Plan/source/receipt 부모와 실행 파일은 trusted operator만 수정할 수 있어야 한다.
**도구는 OS ACL을 자동 설정/검증하지 않는다.** Windows receipt 디렉터리는 상속을 검토하고
owner/SYSTEM/Administrators 이외 쓰기 권한을 제거한 로컬 영속 경로로 별도 준비한다.

```powershell
# 세 변수 모두 검토된 절대경로. 기본 preview는 STS/S3/journal 쓰기가 없다.
python scripts/dr_control_artifact_store.py --plan $planPath --source $publicSignedPath --receipt $receiptPath
# 승인된 창, enable_upload=true에서만 1회 생성한다. 제어 실행을 호출하지 않는다.
python scripts/dr_control_artifact_store.py --plan $planPath --source $publicSignedPath --receipt $receiptPath --execute
```

exclusive lock→STS→versioning→durable INTENT→정확 바이트 임시 사본 fsync→작업창 재검사→
If-None-Match:*·AES256·checksum PUT1회→VersionId 영속 기록→고정 버전 Head 전체 크기/암호화→
같은 버전 Get와 원본 바이트 비교 순서다. 공개 원본을 재직렬화하지 않는다.
receipt의 Bucket/Key/VersionID/SHA256/Size를 Invocation에 고정하고 Policy ArtifactPrefix도
해당 하위 경로로 제한한다. ArtifactRegion을 실제 보관 리전으로 명시한다.

PUT 응답 불명/VersionId 불명은 UNKNOWN/HOLD다. INTENT/UNKNOWN·crash 잔존 lock은 수동 조사하며
receipt 삭제나 새 Key 생성으로 재시도하지 않는다. STORED/READBACK_VERIFIED의 동일 실행은
기록된 VersionId Head/Get만 한다. 로컬 기록 유실·전원 손실 durability는 운영 위험이며 분산 transaction은 아니다.
보존 기간 입력은 lifecycle/Object Lock/자동 삭제 설정이 아니다. 담당·비용·독립 Reader 인수는 별도다.
버전관리·조건부 생성만으로 관리자 삭제를 막지는 않는다.

이번 샘플2개는 기존 2B 조건부 생성 정책의 `storage-trial/.../control-originals` 하위에 별도로 저장했다.
기존 Synthetic/Archive key·IAM·bucket 정책·Slack 권한은 변경하지 않았다.
샘플은 만료+SIMULATION이고, 무작위 샘플 개인키는 RAM에서 폐기했다. 운영 승인으로 사용하지 않는다.
수동 보존 검토 `2026-12-24T06:17:35.032904Z`, 담당 서빈/LBT-60, 자동 삭제 없음.
추가 객체는 총1545B(471+1074B)이며 기존 운영비 목표와 구분하여 실비용을 재검토한다.
새 샘플의 독립 Reader 인수는 수행하지 않았다.

## 제한 시간과 한계

인증·역사적 receipt 조회 후 바이트만 최대8초 준비한다. callback2초, 전체 CLI15초와 현재 TTL은
유지한다. callback 안에서 서명·대상·request·**현재 시각**을 다시 확인하고 추가 네트워크 호출은 없다.
준비 중 만료는 신규 INTENT0, 역사적 결과는 원본0회, 준비 중 경쟁 완료는 내부 Inspect/CAS로 차단한다.
다운로드 뒤 IAM 회수/객체 삭제·이미 읽은 Policy 변경은 invocation 안에서 즉시 재조회되지 않는다.
이 snapshot 한계를 수용하지 않는 작업은 실행하지 않는다. 긴급 중단은 별도 프로세스/DB 접근 통제와
INTENT 확인이 필요하다. 준비한 바이트는 다른 invocation이나 전역 캐시로 재사용하지 않는다.

실측 원본2개 준비3719ms, 승인 callback523µs, 관측 callback은 타이머 분해능 내, 추가 외부 호출0.
만료 승인/SIMULATION 관측 거부를 확인했으며 Store를 만들지 않았다. 실제 승인 기반 init/Pause/Resume,
다중 Pod·Provider·Single Writer 성공 증거가 아니다.
[Evidence](../../evidence/LBT-60/2026-11-23-db-artifact-validation/README.md)에 재현 소스와 한계를 둔다.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·DR HOLD를 유지한다. push/merge는 별도다.
