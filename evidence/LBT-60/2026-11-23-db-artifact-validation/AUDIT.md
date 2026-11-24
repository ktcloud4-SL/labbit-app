# Pre-merge audit — DB/원본/2초 연결 증분

사용자 PRE_MERGE_AUDIT.md를 읽고 수행했다. 완료 판단은 **이번 연결 보완의 범위**다.
PR196 전체 운영 인수·push·배포·merge를 허용하는 판정이 아니다.

## 기준과 전체 변경

- repo ktcloud4-SL/labbit-app, branch `codex/feat/LBT-60-stage3-live-prep-20261123`.
- base/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`.
  최종 GitHub 읽기에서도 동일 base/head, Draft/open/unmerged였다.
- PR 기존387파일은 `pr196-existing-diff-inventory.txt`로 식별했다. 그 source는 기존 #11265~11267
  코드 QA 범위와 별개로 이번 전체 Go/영향 PG 회귀를 실행했다. 과거 QA를 신규 로컬 source QA로 대체하지 않았다.
- 이번 HEAD 대비 tracked/unstaged/untracked 제품·테스트·CI·Runtime·문서·Evidence와 이전 미푸시 준비자료는
  `working-change-inventory.json`/`source-manifest.json`에 고정했다. 새 artifact manifest는 이 묶음만 봉인한다.
  이전4개 packet은 과거 시점 source를 가리키므로 현재 source manifest로 해석하지 않는다.
- independent reviewer는 새 연결 경계·실측 helper·운영 문서와 이전 변경의 호출자/consumer를 검토했다.
  기존387파일 전부의 원본 로그를 독립적으로 재실행/전수 재심사했다는 주장은 하지 않는다.

## SSOT 추적

| 요구·원문 | 구현·경계 | 자동 검증·실행 | Evidence·공백 |
| --- | --- | --- | --- |
| 사용자 요청: DB/승인원본/2초 순서 보완, 영향 사전공지·no merge | 이번 코드/문서 diff, 기존실행과 신규활성화 분리 | source inventory와 실제 변경 목록 대조 | README 실제 작업표. SSM세션/S3샘플 외 서비스설정변경0; 무변경이라는 포괄 주장 없음 |
| LBT-60 최신 #11265~11267: 운영 신원/서명/TTL, 기본모드 보존·P2해소범위 유지 | Execute/ExecuteRestriction의 auth→historical→preload→innerInspect/CAS; Resumer/Restrictor·SQL Store는 기존 owning 계약 유지 | signed replay/expiry/race/UNKNOWN 회귀와 disposablePG 실행 | priorP2를 재개우회라고 확대하지 않음. 최신11270의 별도 역사적G6 Bridge 계보 문제는 이번 해결 범위가 아님 |
| D24 v13: Warm Standby/운영 판단/결과불명 blind mutation 금지 | RDS운송만변경, control적용없음; publisher INTENT/UNKNOWN보존 | publisher12 Mock normal/-O, 기존 UNKNOWN PG회귀 | RDS/TLS/보관 샘플은 Writer Fencing·RPO가 아님 |
| Runtime0.1.15 / protected Policy/실제DB target·TLS | contract.DatabaseTunnelPort, database.DatabaseConfig+VerifySQL | exactloopback/foreignnetwork/host/cancel/TLS생략 회귀 | actual RDS193ms/TLS1.3/readonlyROLLBACK, SSM종료·Active부재·포트닫힘 |
| Runtime callback2초·현재 TTL/역사적조회 | prepared.go byte-only8초·fullReference·copy/clear, verified.go/restrict.go | 바이트변조·size·전체reference·partialfailure·취소·만료·동시receipt·2.1sdownload 회귀 | actual S33719ms+음성callback<2초/네트워크0. 정상LIVE 전체안전조건 latency 미실측 |
| 원본 raw·고정Version·sha/보존 | standalone publisher Plan/조건부PUT/fsync/Head/Get | 잘못된계정/경로/작업창/불명응답/더큰객체/멱등재조회 | expired/SIMULATION2개AES256. 독립Reader/운영승인/RequestId 미보존은 그대로 |
| Runtime/CI producer-consumer 정합 | contract.yaml/README/dr-control0.1.15, Makefile/Pythonfixture CI연결 | PyYAML6.0.3 파싱·migration중복0·Go/Web검증 | 현재서울/게시후보는이전source이며새Runtime후보미배포 |

HTTP OpenAPI·Connector/Realtime 계약·DB migrations는 이번 **증분**에서 변경하지 않는다.
기존 public API/Worker/provider consumer의 회귀는 전체 기본 검사 및 기존 owning tests로 대조했다.
Jira/Confluence/PR은 연결 도구로 읽었고 쓰지 않았다. `ssot-reviewed.json`에 원문·버전을 남겼다.

## 수정 전 findings와 결과

| ID·심각도 | 당시 위치/재현·근거 | 영향·수정/회귀 |
| --- | --- | --- |
| PREP-DB/P2 환경연결공백 | be4 database.go:22 직접DB만지원, 이전직접TCP2초timeout | 실제RDS신원유지 loopback운송추가. actualTLS/SQL읽기·종료검증; 직접기본동작보존 |
| PREP-TIME/P2 환경연결공백 | be4 verified.go document→reader.Head/Get, 이전실측2430ms>callback2초 | callback연장대신auth/historical후8초byte준비. TTLfresh/경쟁/역사원본0회·실제음성callback회귀 |
| DBART-01/P2 | 새publisher초안:152~161, 창검사후fsync동안만료→창밖PUT. 독립Mock재현, 외부호출0 | PUT직전재확인, 경계동일/초과/역행에서INTENT+NOT_SENT사유·PUT0. 독립재검증수락 |
| DBART-02/P2 | 새publisher초안rangeGet: 요청크기SHA만으로전체객체크기미확인. 소스분석 | exactVersionHead전체ContentLength/암호화/DeleteMarker확인. larger-object부정회귀·실제최종Head/Get2/2 |
| DBART-03/P3 | dr-control.md0.1.15 vs owningYAML/README0.1.14 | 세문서·기계판독계약정합화, parser·CI연결검사. 이전배포source버전은별도표시 |

모두 수정 전에 사용자에게 해당 내용을 보고했다. 제품 수정과 검토를 분리한 reviewer가 DBART-01을
직접 재현했고 수정 후 12 Mock normal/-O 및 Go3패키지 race를 독립 실행했다.
근거 없는 확인된 결함으로 바꾸지 않은 항목: 다운로드 후 IAM/삭제 재조회 부재는 명시적 snapshot 한계,
로컬 ACL/영속 디스크 관리와 승인 권한·개인키 운영은 별도 선행조건이다.

## 실패·보안·수명주기

- invalid ref를 전부 검사 후 I/O, fullReference key별 bytecopy, partialfailureclear/cancel/mismatch 거절.
  다른 명령·목적·서명·TTL 판정은 재사용하지 않는다. 실제 임의 fallback·전체network허용 없음.
- 신원 확인 전 journal0, 역사적 결과는 원본0, 준비후 innerInspect/CAS가 동시명령을 다시 확인한다.
  APPLIED/UNKNOWN receipt가 재개 권한으로 변하지 않는다. 8초 준비·15초CLI·2초SQL/콜백 경계가 별개다.
- 운송만loopback, 실제RDS ARN/ResourceId/host/DB/user/CA/TLS 유지. 공유Secret을 고치지 않는다.
  SSM 종료는 exactHistory/Active/프로세스/포트로 확인하며 APIACK만으로 주장하지 않는다.
- 공개원본만 업로드. 개인키/DSN/RAM Secret·credential cache·raw 민감 stderr는 packet에 없다.
  publisher shape검사는서명인수가아니고, ACL은운영준비조건이다. 이번receiptACL은실측했다.
- publisher INTENT후불명응답은UNKNOWN/HOLD. crash lock/receipt유실·rename/fsync디스크손실은수동조사,
  새key/receipt삭제로자동재시도하지않는다. 반복은알려진VersionId읽기만. 관리자삭제방지는제공하지않는다.
- 신규S3샘플2개만수동보존목록에남고12/24검토. 자동삭제/정리명령실행없음.
  서울서비스설정은원본유지. 임시SSM/로컬PG는종료했고공유자원정리는하지않았다.
- 실제 Worker/Provider/failover/failback/브라우저전체경로는 이번변경의실측이아니다.

## 실행·실패·미실행

`validation.json`, command별 원본/gzip·초기실패·SKIP를 README와 대조했다.
전체Go4684PASS/88SKIP, race4pkg310PASS 및최종3pkg210PASS, disposablePG703PASS,
Web493PASS, Python12×normal/-O, vet/build/typecheck/lint/runtime/CI검사PASS.
첫PG fixture role오류, 첫Web EPERM, Windows PG launcher pipe/후행psql실패와helper준비오류를 별도 기록했다.
처음실패를성공로그로덮지않았다. Web는같은source로재실행통과했으며관련없는cleanup제품코드를고치지않았다.
실제RDS/S3는위표의제한범위만확인. 실제운영서명·Pause/Resume·다중Pod·Provider·WriterFencing은미실행.
Windows기준검증이며 이번증분의Linux실환경/새GitHubCI는미실행이다(이전CI성공과분리).

## 최종 판정

이번 **DB 접속·공개 원본 보관·2초 검증 연결 보완은 scoped 검증을 마쳤다**.
독립검토에서 발견한P2/P3는수정·회귀확인했다. 최종문서·소스21개·원본로그·공개helper·이전packet은
별도 reviewer가 대조했으며 새 차단 finding은 없었다. `independent-review.md`와 상세 검토 기록을
보존했고, 최종 artifact manifest의 파일/해시 일치는 봉인 도구로 확인했다.
전체PR merge·실제운영Pause/Resume 준비완료로판정하지않는다. 실제안전조건과복귀/전체Pod계획등이남는다.
기존ReplicaLag FAIL·필수4 NOT_VERIFIED·DR HOLD·recovery_authorized=false·cloud_verified=false 유지.
