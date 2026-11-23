# PR196 공개 인계 — DB·원본·2초 제한

이 묶음은 로컬 검증 소스와 기존 Draft PR196의 Git commit을 연결한다. AWS 시험을 다시 실행하지 않는다.

| 자료 | 범위 |
| --- | --- |
| [최신 보완·검증 원본](../2026-11-23-db-artifact-validation/README.md) | DB SSM/TLS 읽기, 만료된 모의 원본2건 보관, callback 음성 검증, 자동/PG/Web 테스트와 미검증 |
| [공개 감사](AUDIT.md) | 사용자 승인, 최신 SSOT, stage/source 추적, 기본 Gitleaks 오탐6건 보정 및 남은 Gate |
| [실제 migration·후보 기동/복귀](../2026-11-23-migration-candidate-rollout/README.md) | 앞선 회차 schema15/후보N2 호환·원상복귀. durable Pause/Resume 미실행 |
| [Collector 경로 준비](../2026-11-23-collector-path-preparation/README.md) | 앞선 회차 임시 네트워크 조사·복귀 및 제어/S3 리전 분리 |
| [이전 후보 게시](../2026-11-23-candidate-publication/README.md) | be4d0ba 시험 이미지·스캔·거부된 초기 후보 이력 |
| [최초 사전 준비](../2026-11-23-pr196-trial-preparation/README.md) | 당시 읽기 조회·비활성 preview. 이후 회차 결과와 구분 |

원본 packet은 당시 미푸시·미커밋/미검증 상태 문구를 그대로 둔다. `publication-source.json`과
`index-verification.json`이 이번 Git blob으로 연결한다. source LF 변환과 Evidence raw bytes를 분리하며
`artifact-manifest.json`으로 이 공개 묶음도 식별한다. 개인 키/DSN/인증 캐시·생성 pyc는 포함하지 않는다.

GitHub CI 성공은 CI fixture 검증이며 실제 운영 승인·다중 Pod·Provider·Writer Fencing을 대신하지 않는다.
기존 FAIL·네 가지 NOT_VERIFIED·DR HOLD를 유지하며 Draft·미병합 상태로 인계한다.
