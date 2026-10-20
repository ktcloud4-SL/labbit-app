# C2 KT Cloud Browser Terminal follow-up — 2026-10-20

준비된 강사·학생 Workspace의 C2 대표 기능 통합 검증은 통과했다. **학생 TerminalSession에서 `OPEN_FAILED`가 한 번 발생했으나, 이후 테스트에서는 재발하지 않았다.** 과거 한 번의 세부 원인은 확인되지 않았다. 이 기록은 재현되지 않은 오류의 원인을 해결했다고 주장하지 않는다. C3는 실행하지 않았다.

## 실제 실행과 결과

| 범위 | 결과 | 원본 Evidence |
| --- | --- | --- |
| 과거 학생 A2 실패 | `3fdc3246-0bae-40fe-a4f8-8d156bf6fe5b`, 2026-10-20 03:47:33.527401–03:47:34.155688 UTC, 0.628초에 `OPEN_FAILED`. Connector `ATTACH_FAILED` 이후 정리됨. 기존 B는 생존, 다음 A3는 성공 | `browser-c2-evidence.json`, `backend-historical-openfailed.json` |
| 강사 SSH | 관리 IP의 응답 경로가 Lab NIC default로 나가던 문제를 현재 강사 VM의 Connector 주소 `/32` 관리망 경로로 복구. 재부팅 후 strict host-key SSH·cloud-init·PTY 확인 | `teacher-route-after.json`, `teacher-after-reboot-probe2.json`, `both-readiness-after-route.json` |
| 강사·학생 Browser/SaaS → Connector → 실제 VM | 신규 30개 세션: 강사7·학생23, attach30·ENDED30·OPEN_FAILED0. 두 역할의 실제 VM 응답, 브라우저 resize와 guest kernel 크기 일치, 독립 A/B 상태, A 종료 후 B 생존 확인 | `browser-observations.json`, `final-session-summary.txt` |
| 대표 reconnect/close | 같은 session ID의 grace 재접속과 기존 shell 상태 유지(강사12.647265초, 학생18.130271초), 명시 close, 원격 exit7과 `PTY_EXITED` 확인 | `browser-observations.json`, `final-session-summary.txt` |
| 추가 반복 검증 | 05:00–05:15 UTC 관측 창: Backend 신규34개 created/attached/ended UUID 집합 동일, 오류0. 별도 Browser 기록은 cold 후보2회와 신규 Terminal 열기30회 모두 fresh VM 응답·실제 kernel 크기 확인, B 생존 | `backend-current-lifecycle.json`, `browser-cold-*.json`, `browser-reopens.json`, `browser-B-survives-final.json`, `final-safe-failures.json` |
| Provider 실제 읽기 probe | 50 resolve 모두 성공, Nova GET50 + NSM list50 모두 HTTP200, transport 오류0 | `provider-probe-summary.json` |
| 종료·임시 자원 | 양쪽 VM PTY shell0, 임시 SSH 정책과 원격 probe 삭제 확인. 기존 소유 자원10개는 사용자 후속 G3/G4용 유지 | `*-final-pty-zero.json`, `temporary-ssh-delete.json`, `probe-cleanup.json`, `final-postconditions.json` |

추가34개 window와 Browser case의 session ID를 직접 일대일 수집하지 않았으므로 두 자료를 직접 join했다고 주장하지 않는다. Provider probe는 production resolver 설정을 읽은 별도 client의 새 인증으로 수행했다. 원래 실패 당시 client/token 상태를 재현한 것은 아니다. idle 후 cold 두 건도 새 transport를 직접 계측한 것은 아니다. HTTP/WSS101은 실제 왕복 동작에서 추론되며 별도 wire capture는 없다. 초기 A의 잘못된 resume 관측은 `browser-c2-evidence.json`의 invalidObservation에 보존하고 통과 근거에서 제외했다.

## 실행 소스와 PR 소스

- 실환경 Connector: base `bfd3e9a93b302b1ed7327cd2bfdb21acaafaf157` + 미커밋13파일. 정확한 각 SHA는 `live-source-manifest.json`; 실행 binary SHA256 `defdf9aab33dc00d914dd8744dcc1e0e7405ceead2c69b9551f3533f4e539cc2`.
- 실환경 SaaS: web `ghcr.io/ktcloud4-sl/web:sha-9172e05`, backend `ghcr.io/ktcloud4-sl/backend:sha-e0caea4`의 api/realtime/preview/worker. Origin `https://jinwook.store`. 이번 PR 준비 중 재배포하지 않았다.
- PR: `codex/fix/LBT-22-c2-ktcloud-acceptance`, base `9172e050f5660c80b84ba70d52f0c549d8f032d9`. 13파일을 최신 main에 옮겼고, `app.go`에는 main의 version wiring도 보존됐다. 나머지12파일 텍스트는 실환경 소스와 동일하지만 줄바꿈 byte SHA는 다를 수 있다. 새 PR의 unit 검증에서 발견한 두 C2 fixture의 protocol-ready 대기 helper도 추가됐다. 따라서 PR 전체 binary를 실환경에서 실행했다고 표시하지 않는다. `pr-source-manifest.json`이 코드·테스트14파일의 git blob과 SHA를 고정한다.
- 보존한 `final-postconditions.json`의 `userRequestedC2FullClosure:false`와 과거 source manifest의 `wholeBaseToHeadBranchReviewed:false`는 당시 더 넓은 원인 규명 요청/구 브랜치의 판정이다. 이후 사용자가 “1회 오류, 이후 테스트 이상 없음”을 기록해 push/PR하도록 요청했다. 현재 PR 범위의 감사 결과는 `PRE_MERGE_AUDIT.md`에 별도로 기록한다. 과거 판정을 덮어쓰지 않았다.

## PR 변경과 검증 경계

KT Cloud의 비어 있지 않은 user_data는 gzip으로 전송하고, SDK base64 이후 최종2048 제한을 API 호출 전에 확인한다. Snapshot/startup script 원문 바이트는 유지한다. 일반 OpenStack 분기는 변경하지 않는다. HTTP200 HTML 또는 잘린 JSON을 성공으로 처리하거나 불명 mutation을 재시도하지 않는다.

실패 시 resolve/SSH/channel/PTY/Data attach 단계를 고정 enum·timeout·허용 SSH reason·elapsed와 UUID로만 내부 로깅한다. raw Provider/SSH 오류, Terminal 입출력, key/token을 기록하지 않는다. 공개 오류와 retry/host pin/공유 transport 수명주기 정책은 바꾸지 않는다. 이 진단 보완은 과거 A2의 세부 원인을 소급 복원하지 못한다.

강사 경로 복구는 현재 VM의 운영 변경이다. 앞으로 생성할 모든 dual-NIC VM에 대한 일반 Provider 경로 자동 수정이나 정상 Provision→READY writer는 이 PR의 구현이 아니다. 기존 ERROR execution/FAILED operation 이력은 유지했다. 준비된 fixture에서의 C2 검증과 전체 provisioning/Cleanup 완료를 구분한다. 소유 자원10개 유지 상태이며 owned0 완료가 아니다. 후속 G3/G4의 구체 테스트 기간과 사람 단위 Cleanup 담당은 미확정이며 별도 인계가 필요하다. C2 대표 기능 PASS가 해당 환경 인계 완료를 뜻하지 않는다. C3·G3·G4 통과 판정도 없다.

## 재검증 방법

`TESTING.md`에 따라 `make setup && make test`를 실행한다. Windows에서 Provider fsync 테스트가 제한되면 Linux에서 Provider package를 검증한다. 실제 환경 E2E는 opt-in이므로 설정 없이 unit suite에서 skip된다. 준비된 별도 Workspace/Connector, strict known_hosts·credential을 외부 Secret으로 준비하고 Terminal 권한을 가진 강사/학생으로 로그인한다. 다음 순서로 확인한다.

1. 실제 Browser에서 세션 A/B를 열고 각각 새 VM 출력 증거와 guest terminal 크기를 확인한다. 출력과 nonce 값은 저장하지 않고 일치 여부만 남긴다.
2. viewport 변경 후 browser cols/rows와 guest kernel 크기를 비교한다. A/B의 shell 상태가 섞이지 않는지, A 명시 종료 후 B 상태·I/O가 유지되는지 확인한다.
3. grace 내 B를 다시 연결해 같은 session ID·기존 PTY 상태를 확인한다. 원격 shell exit도 실행해 UI/Backend ENDED를 대조한다.
4. Backend 생성/attach/종료 UUID 집합과 실패 event를 대조하고 실제 VM PTY shell0을 확인한다. 임시 정책/probe를 제거하고 소유 자원 retention 또는 cleanup 범위를 명시한다.

30회 반복은 관측된 재발 부재의 근거이며 장기 무오류나 과거 원인 해결의 증명이 아니다. 이 폴더에는 Terminal transcript·입출력·nonce 값·토큰·개인키가 없다. 원본 metadata를 선별해 공개본은 LF와 줄끝 공백을 정규화했다. `live-evidence-sha256.json`에 원본 export SHA와 공개본 canonical LF SHA를 구분했다. checkout의 autocrlf에 따른 줄바꿈은 canonical LF로 환산해 검증한다. 로컬 원격 작업 helper와 실행 binary는 PR에 포함하지 않았다.
