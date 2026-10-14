# Workspace File transport

`LABBIT_WORKSPACE_ROOT`와 `LABBIT_WORKSPACE_SFTP_HELPER`를 함께 설정하고 Workspace VM 검증을 지원하는 Provider를 사용하면 Connector는 HELLO에 `file-v1`을 선언한다. 구성 전에 VM에 접속하지 않는다. Helper 설치·root 보안 조건은 [SFTP 경계](workspace-sftp.md)를 따른다. 설정이 없으면 사용자가 capability 목록에 넣어도 `file-v1`을 제거한다.

Control의 `FILE_OPEN`마다 `/connector/v1/file-data`에 outbound 연결 하나를 연다. endpoint는 `LABBIT_SAAS_BASE_URL`에서 유도하며 production은 WSS와 서버 인증서 검증을 요구한다. credential은 해당 Control 연결이 실제 dial에 사용한 값이다. Credential 파일이 바뀌어도 진행 중 요청의 인증을 바꾸지 않는다. 새 Control 연결은 새 credential을 사용한다.

Attach reply와 모든 frame의 request/Lab/generation/operation을 검증한 다음 rooted SFTP Tree·Read·Save를 수행한다. 경로·목록·본문은 Data WSS에만 있으며 Control에는 lifecycle/correlation만 있다. 파일 본문은 JSON 다음 Binary frame 하나이고 0 byte도 frame을 보낸다. Text는 decode 전 1 MiB, Tree 결과는 최종 envelope 전체 1 MiB를 제한한다. SafeError에는 고정 code만 보낸다.

기본 요청 timeout은 30초, attach/write timeout은 각각 5초, 본문 한도는 1 MiB, 동시 요청 한도는 16이다. Read는 SaaS가 요청한 ceiling과 Connector의 유한한 본문 한도 중 작은 값으로 실제 파일을 읽으며, 요청 ceiling 자체가 크다는 이유로 작은 파일을 거절하지 않는다. Save는 완전한 Binary가 도착하고 revision이 일치할 때만 쓴다. 한도 초과 body는 저장하지 않는다. transport read limit까지 초과하면 1009로 연결을 종료하므로 이미 전송한 Save의 결과가 불명일 수 있다.

Control 연결별 취소 context를 사용한다. 단절·교체·revoke·shutdown 시 해당 연결의 File 요청을 정리하며, 늦은 이전 reader가 새 Control의 File 요청을 취소하지 않는다. File close/timeout/peer 종료는 SFTP channel과 해당 Data WSS만 끝낸다. 공유 SSH의 다른 PTY는 유지한다. Control EOF에서 Terminal grace 동작은 기존 계약을 따른다.

중복 `fileRequestId`는 새 Data WSS를 만들지 않는다. 활성 요청은 상관관계를 확인하고, 완료·실패·취소된 ID는 프로세스 수명 동안 hash로 기억한다. 기본 16,384개의 서로 다른 ID가 기억되면 이후 새 요청은 `UNAVAILABLE`로 거절한다. 기억한 ID를 제거해 Save를 재실행하는 동작은 없다. 운영 시 이 유한한 수명 제한을 고려해야 한다. 프로세스 재시작 후에는 이 ephemeral 기억을 복구하지 않으며 SaaS는 결과 불명 Save를 같은 ID로 재전송하지 않는다. 결과 불명 Save는 새 Read로 파일 상태를 확인한다.

계약은 [Connector SSOT](../../contracts/connector/README.md#7a-workspace-file-요청tree-read-save), 실행·Mock·실환경 범위와 재현은 [LBT-150 Evidence](evidence/LBT-150/2026-10-14/AUDIT.md)에 기록한다. 이 검증은 Browser/실제 SaaS DB 전체 E2E 또는 KT Cloud 유료 환경 검증을 포함하지 않는다.
