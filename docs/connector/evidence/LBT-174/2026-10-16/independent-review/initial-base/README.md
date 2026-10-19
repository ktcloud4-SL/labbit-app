# LBT-174 구현 전 독립 검토

2026-10-16 KST. 사용자 PRE_MERGE_AUDIT.md를 다시 읽고 구현과 분리된 reviewer가 초기 base의 Preview manager/session/client → WSS Handler → app lifecycle을 검토했다. **초기 판정은 보완 필요**이며 현재 수정 중인 코드의 최종 판정이 아니다. 변경 전 findings를 parent에게 먼저 전달했고 제품 소스·기존 Evidence·cloud·원격 Git은 수정하지 않았다.

검토 base는 `4691c0ae8a43955549ae2b9e8bc4f5a464c30001`(origin/main, LBT-173 merge 포함), branch `codex/feat/LBT-174-preview-local-integration`이다. 진행 중인 작성자 수정과 섞이지 않도록 이 폴더 `base/`에 git archive한 고정 소스에서만 probe를 실행했다. `zz_independent*.go` 두 파일은 외부 archive에만 추가한 fixture다. 아래 모든 제품 줄번호는 **이 pinned base**다.

## 최신 요구사항 직접 확인

[LBT-174](https://samsunglions.atlassian.net/browse/LBT-174) updated `2026-10-16T09:23:57.168+0900`, 댓글0, parent LBT-24. [조회 메타데이터](ssot-read.json). D18v5/결정완료v11/테스트전략v3도 다시 직접 읽었다. AGENTS/TESTING/CONTRIBUTING과 owning Git Connector §7b 및 preview-control/data schema를 기준으로 했다.

범위는 기존 CC-03 transport에 실제 SSHForwarder를 연결한 **Mock Preview Gateway → Connector Preview WSS → 실제 local OpenStack Workspace HTTP 왕복**, full attach correlation,1WSS=1TCP,세션당 active tunnel≤1,sequential 재개,Close/오류/Reset/Cleanup/stale/cancel/backpressure/shutdown/회귀다. KT최종 검증은 OP03다음 작업, 실제SaaS/Browser E2E는 LBT-25로 제외돼 있다. 과거 Confluence의 Preview 필드·순서보다 현재 Git wire를 우선한다.

## 수정 전 confirmed findings

| ID·심각도 | pinned base 파일·줄 | 재현·관찰·영향 | SSOT·필요한 수정 및 회귀 |
| --- | --- | --- | --- |
| F174-R001 / High | `internal/connector/preview/client.go:252`, `session.go:309–336`; 관련 `wss/handler.go:1528–1530` | 실제 로컬WSS peer가 M1 ATTACH를 받은 뒤 ACK를 지연 → session에 M2 tunnel을 bind → M1의 올바른 correlation ACK 반환. M1 DialAndAttach가 err=nil이고 M2 active tunnel에 old WSS를 붙임. client는 ACK의 M1 corr만 확인한 뒤 현재 tunnel에 무조건 attach한다. Handler의 M1 attach 실패 처리도 현재 tunnel을 닫으므로 새 M2에 작용할 수 있다(이 후자는 코드 분석). | §7b stale/late attach는 현재 attempt·새 tunnel을 건드리면 안 됨. Dial/attach/실패cleanup을 동일 attempt/tunnel identity에 묶고 closed/replaced tunnel은 거절. 지연 M1 success 및failure가 M2의WSS/TCP/상태를 바꾸지 않는 회귀 필요 |
| F174-R002 / High | `preview/manager.go:87,154,167–194`; app `app.go:371–377`/WSS `handler.go:1024` 호출 경계 | Fake forwarder를 dial 중 정지 → CloseSession(S) 또는 CloseAll(CREDENTIAL_REVOKED) 실행 → dial 해제. 두 경우 모두 open이 err=nil로 반환되고 manager Count=1. pending open이 등록돼 있지 않고 close barrier/epoch/tombstone이 없어 닫힌 logical session이 부활한다. | §7b 명시Close·4001/4002·shutdown에서 pending 포함 정리. pending을 manager가 먼저 소유하고 Close에서 취소·retire하며 결과 등록 때 동일 entry/epoch 검사 필요. 일반 CloseAll후새정당Control 재사용과 영구Shutdown을 구분. blocked dial의 late success는 즉시 Close, Count0, 재attach0 회귀 필요 |
| F174-R003 / High | `preview/session.go:60–66,88–91`; app shutdown `app.go:372` | 실제 WSS의 underlying net.Conn.Write가 peer backpressure처럼 정지하도록 gated wrapper 사용 → TCP output이 WSS WriteMessage/writeMu를 잡고 정지 → session.Close 호출. Close가 같은writeMu를 기다려서 underlying socket.Close에 도달하지 못한다.100ms 경계 내 안끝남. fixture가 underlying Close를 직접 호출하면 즉시 writer와 Close가 풀린다. 코드상 gate가 해제되지 않으면 무기한이다. | Jira backpressure/cancel/shutdown·§7bTCP/Data정리 위반. 일반 data writer mutex를 기다리지 않고 socket Close로 blocked read/write를 해제할 수 있어야 함. 정책Close frame은 bounded best effort로 처리. slow consumer에서 Close/CloseAll/Shutdown이 예산 내 끝나며 다른session/PTY가 생존하는 회귀 필요 |
| F174-R004 / Medium | `preview/client.go:194,204–207` | Gateway가 ATTACH를 받고 ATTACHED를 보내지 않는 상태에서 ctx cancel. 현재 read는 AttachTimeout(기본5초)에만 연결되어100ms내 반환하지 않는다. write에도 ctx cancellation/독립deadline이 연결되어 있지 않음(Write부분은 코드분석). fixture가 gateway socket을 직접 닫은 뒤에야 반환했다. | Jira cancel/shutdown/timeout 정리. ctx/attempt 종료가 setup WSS read/write를 깨우고 성공handoff 뒤 setup cancel이 active stream을 죽이지 않아야 함. cancellation before/after dial,ATTACH blockedwrite,ACK wait,lateACK후정당stream 보존 회귀 필요 |
| F174-R005 / High | `wss/handler.go:160–199,1344–1351`; `reconnect.go:255–260` | old Control ownerEpoch를 캡처 → 새SetSender로 credential/sender 교체 → old epoch의 PREVIEW_OPEN 처리. handler가 거절하지 않고 TCPdial1회와 **새Control의 synthetic Bearer**로 실제 로컬WSS upgrade/attach 성공(err=nil). File만 epoch를 확인하고 Preview는 mutable current credential/sender를 다시 조회한다. | §7b OPEN을전달한동일ControlCredential, oldControltrust폐기 요구. 수락 당시 Control owner epoch/credential/sender를 불변 snapshot으로 묶고 owner retired 후 오래된 OPEN/CLOSE가 다른owner자원에 작용하지 못하게 해야 함. pending취소도 owner수명에 연결. durableProviderOperation의 reconnect별도수명 정책은 유지. oldepoch/newcredential·staleclose·4002lateattach 회귀 필요 |

## 실제 실행 결과

이 probe는 예상하는 안전 동작을 assertion하므로 **현재 base에서 FAIL이 올바른 결함 재현 결과**다. 테스트 자체가 제품을 고쳤다는 의미가 아니다.

- [Preview 원본 로그](initial-probes.log): `TestReviewPendingOpenRetiredByClose`(CloseSession/CloseAll 각각), `TestReviewStaleAttachCannotBindNewAttempt`, `TestReviewAttachReadCancellation`, `TestReviewCloseUnblocksBlockedWSSWriter` 총5시나리오 FAIL, 명령exit1.
- [Control owner 원본 로그](owner-probe.log): `TestReviewOldControlEpochCannotUseNewCredential` FAIL, 명령exit1. 토큰은 명시적 합성fixture값이고 실제비밀/Provider응답/HTTP본문을 사용하지 않았다.
- 둘 다 pinned base 전체 cmd/internal/go.mod/go.sum archive에서 실행했다. external fixture는 `base/internal/connector/preview/zz_independent_test.go`, `base/internal/connector/wss/zz_independent_preview_owner_test.go`다. stdlib net.Pipe/httptest + 실제 gorilla WebSocket을 사용했다. 실제 SSH/Provider/cloud/Browser 검증은 이 초기 검토에서 실행하지 않았다.

재현 명령(이 폴더의 `base`에서):

```powershell
go test ./internal/connector/preview -run '^TestReview' -count=1 -v -timeout=20s
go test ./internal/connector/wss -run '^TestReviewOldControlEpoch' -count=1 -v -timeout=15s
```

초기 fixture compile에서 `PreviewAttachedPayload` 대신 map을 쓴 작성오류1회가 있었고 외부 fixture만 정정한 뒤 위 runtime 재현을 수행했다. 제품 compile실패로 집계하지 않는다. archive 추출 명령의 최초 PowerShell output 인자 오류도 제품결함과 무관하다.

## 추가 분석과 최종 검토 조건

기존 `BindTunnel`은 old.Close를 goroutine으로 예약한 즉시 새 tunnel을 등록하므로 둘의 실제close/attach 순서도 최종 diff에서 확인해야 한다. t.dataConn은 attach와close/pump 사이의 동기화가 불완전해 closed tunnel attach나 pointer race가 없는지 확인해야 한다. 현재 이 두 항목은 별도 defect로 확정하지 않았고 lifecycle 수정과 `-race`로 재검토할 경계다.

Handler Listen은 Preview open/attach를 동기 호출하므로 느린setup 중 queued PREVIEW_CLOSE/Control Close 읽기가 지연된다. Preview의 임시 attempt를 비동기로 분리하더라도 provider durable operation dispatch 및 File/Terminal 수명 정책을 함께 바꾸지 않는 좁은 변경인지 확인해야 한다. attach ACK 전 binary invalid frame 처리,1MiB JSON상한,raw error safe매핑,1WSS=1TCP,Body/credential 비저장도 최종 회귀 대상이다.

이 문서는 구현 전 findings 보고다. 이후 변경 전체 diff/자동race/실제localOpenStack+MockGateway Evidence/실행소스 identity/cleanup을 다시 확인하기 전 완료·PR가능 판정을 하지 않는다. 사용자 승인 없이 cloud mutation/remote write/push를 하지 않는다.
