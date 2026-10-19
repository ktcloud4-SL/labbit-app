# LBT-174 추가 독립 검토 — 초기 분석 및 timing probe

검토자 `/root/review_lbt173`는 제품 소스와 기존 Evidence를 수정하지 않았다. 이 폴더는 저장소 밖 독립 fixture/overlay/결과만 담는다. 클라우드 API, 원격 쓰기, push/PR은 실행하지 않았다. `PRE_MERGE_AUDIT.md`를 다시 읽고 요구사항·실패 경계에 따라 검토했다. 이 보고서는 후속 최종 source 전체 audit 이전의 분석 기록이다.

## 소스 구분

- 기준 HEAD: `0051d8d6803fd447f7b690dd895a0a928c3c7302`.
- 수정 전: 이전 독립 검토에서 495개 Go/module LF 해시가 최종 source aggregate `956c7d6f3b87f6d49d1b842571124da4591db40f5bc15de5c6efce9d65fe98b9`와 모두 일치했던 `../lbt174-final-independent-review-0051d8d/reconstruction-isolated`.
- 첫 수정 후: 위 복원 소스의 별도 복사 `after-source`에 실제 작업 트리의 `trace_propagation_test.go`, 새 `trace_log_test.go`, `tcp_test.go`만 복사했다. 세 파일의 LF 해시는 `after-source-manifest.json`에 보존한다. 이후 GET absence helper 검토를 위해 `integration_test.go`와 `integration_cleanup_test.go` 두 파일을 추가 복사했다. 최종 source 전체와의 일치는 이후 별도 확인 대상이다.
- Probe는 외부 Go overlay로만 적용한다. 지연은 정상 경로의 실제 순서 가정을 흔들기 위한 의도적 75ms scheduling fault다. 원래 병렬 실패 때 발생한 구체적인 OS/CPU 지연을 입증한 것으로 해석하지 않는다.

## 수정 전 findings

| ID / 심각도 | 위치와 재현 | 영향 및 필요한 회귀 |
|---|---|---|
| F174-T001 / Low, 기존 테스트 결함 | `internal/server/realtime/trace_propagation_test.go:265–268`. 빈 로그 snapshot의 `strings.Split(TrimSpace(""), "\n")`가 빈 문자열 한 줄이 되어 첫 poll에서 JSON EOF로 Fatal. `data.go:152`의 bindData는 ATTACHED를 먼저 queue에 넣고 `:172`에서 이후 InfoLog를 남기므로 ACK 수신이 로그 존재를 보장하지 않는다. | 5초 eventually 대기를 우회하는 false FAIL. 빈 첫 snapshot 뒤 실제 matching log를 기다리는 회귀, 빈 줄/무관 event 처리, 완성된 malformed JSON 거부를 유지해야 한다. 제품 로그 순서를 테스트에 맞춰 바꿀 이유는 없다. |
| F174-T002 / Low, 기존 테스트 결함 | `internal/connector/vmaccess/tcp_test.go:229–230`. SetupTimeout 40ms가 의도적으로 stall시키는 direct-tcpip 이전의 정상 SSH handshake/PTY 준비에도 적용된다. 정상 server handshake에 75ms 지연을 넣으면 :230에서 DeadlineExceeded. | timeout/late channel/sibling 검증에 도달하기 전 false FAIL. 정상 준비는 정상 budget으로 하고 이미 연결된 transport의 의도적 stalled TCP call만 명시 caller deadline으로 제한한다. 기존 `TestHandshakeTimeoutAndCancellationCloseSocket`는 설정 SetupTimeout 자체의 검증을 별도 유지한다. |
| F174-T003 / Low, 첫 수정 후 회귀 강도 부족 | 첫 수정본 `tcp_test.go:236–241,252–258`. `tcp.go:28`에서 caller ctx를 `context.WithoutCancel`로 대체한 단일 mutant에도 새 전용 timeout test가 **50.08초 뒤 PASS**했다. 200ms와 10ms caller deadline 대신 각 10초 설정 deadline으로 반환해도 errors.Is만 검사한다. | 이 테스트만으로 caller deadline의 신속한 전달을 검증할 수 없다. 실제 호출 결과를 goroutine + 충분히 넉넉한 1–2초 독립 select로 기다려 설정 10초 fallback과 구별해야 한다. 이 finding은 제품 결함이 아닌 테스트 검출력 문제다. 다른 취소 테스트가 이 mutant 일부를 검출할 수 있다는 범위와 구분한다. |

각 finding을 root에 수정 전에 보고했다. T001/T002는 root가 사용자에게도 수정 전에 보고했음을 통지했다. T003 최종 수정 여부 및 최종 재검증은 이 초기 문서 이후의 별도 기록 대상이다.

## 독립 실행 결과

| Source / 검증 | 결과 및 파일 |
|---|---|
| 수정 전, ACK 후 attach log 전 75ms 지연 | 원본 Realtime test 3/3 FAIL, 원래와 같은 `trace_propagation_test.go:268`. `before-realtime.log` |
| 수정 전, 정상 SSH server handshake 전 75ms 지연 | 원본 VMAccess test 3/3 FAIL, 원래와 같은 `tcp_test.go:230`. `before-vmaccess.log` |
| 첫 수정 후, 같은 Realtime 75ms 지연 | 원본 trace test 및 delayed-first-event/malformed 회귀 각각 10/10 PASS. `after-realtime.log` |
| 첫 수정 후, 같은 SSH 75ms 지연 | late open/slot/sibling test 10/10 PASS, 기존 configured handshake timeout/cancel test도 각 10/10 PASS. `after-vmaccess.log` |
| 첫 수정 후 caller deadline 무시 mutant | 해당 TCP test 50.08초 뒤 PASS. `mutant-caller-deadline.log`, `tcp-caller-deadline-ignored-overlay.json`, `context-mutant-manifest.json` |

`create_timing_overlays.py`, `create_after_source.py`, `create_context_mutant.py`에 각 overlay와 복사 소스 생성 절차가 있다. 이 재현은 실제 loopback SSH/WebSocket 및 테스트 로그 버퍼를 사용한다. 실제 OpenStack/VM E2E를 대체하지 않는다.

## 잔여 검증 경계

- 기존 일반 suite 18skip은 cloud opt-in 14개와 Windows 파일시스템/권한 제한 4개다. 실제 Preview opt-in의 별도 PASS는 일반 suite skip이 사라졌다는 뜻이 아니다.
- `TESTING.md:79–84`, Makefile은 PostgreSQL 16 및 CREATE DATABASE 권한을 가진 실제 server에서 5 package의 `integration` tag 검증을 요구한다. 이 경로는 일반 suite 18skip에 포함되지 않는다. Mock DB로 대체할 수 없다. `postgrestest`는 무작위 `labbit_it_` DB만 만들고 DROP WITH FORCE로 정리한다.
- 기존 보조 management SG/keypair helper는 DELETE ACK만 확인했다. 새 helper는 직접 만든 ID/이름에 DELETE 한 번 후 exact GET 404를 기다리고 401/403/5xx/network/context 실패를 부재로 취급하지 않아야 한다. 설정으로 재사용하는 SG/keypair에 cleanup을 등록하지 않는 기존 소유권 경계를 유지해야 한다.
- Linux에서 네 개 OS 경로를 별도 실행하면 Linux symlink/replacement/POSIX mode 검증을 보완할 수 있다. Windows ACL 기밀성 확인의 대체로 표시하면 안 된다.
- 원격 CI, KT Cloud, 실제 Browser C3, 장시간 부하와 rollout은 이번 loopback probe의 검증 범위 밖이다. 아직 최종 전체 diff/source/Evidence audit 판정을 내리지 않는다.
