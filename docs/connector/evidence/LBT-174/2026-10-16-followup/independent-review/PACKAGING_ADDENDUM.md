# 최종 패키징 독립 검토 보충

2026-10-16 16:30 KST. base/HEAD0051, source502개 a7d, 전체27 Go diff를 검토했다. 아래 검증은 별도 Python script와 `git archive`/`git init`을 사용했고 제품 source·기존 Evidence·cloud를 수정하지 않았다.

- base archive에 `full-code-diff.patch`를 check/apply한 별도 저장소의502개 LF 해시/aggregate가 최종manifest와 모두 일치했다. 미추적 파일을 포함한27개 경로와 inventory도 같았다.
- freeze된243개 raw-provenance artifact를 모두 읽고 gzip복원 bytes/SHA를 검증했다. 외부233이전/추가post-package 원본을 포함한243개 원본도 대조했다. `intermediate`는 외부 `final` 디렉터리로 매핑된다.
- 17개 `*-events.json`을 raw JSONL에서 독립 집계했다. test와package, parent와child, FAIL/skip을 구별한다. 원래 broad batch9top-level PASS/3FAIL과 초기PG실패·sourceguardfalse를 보존한다.
- cleanup-observations의5개 live log,17 top-level 결과와87 marker가 실제 raw JSON output에 존재했다. Lab exactref부재와 보조 SG/keypair GET404를 확인한 범위이며 global cloud 모든자원0을 뜻하지 않는다.
- first/intermediate/verified/sealed/closing-c3a/closing-live5ea의6개 sourcebridge는 파일수·aggregate·최종과의차이목록이 맞다. 5ea는 sealed67cd map에서 Workspacehelper 한 파일만 바꿔 정확히 재구성된다. 당시 command metadata aggregate와 같은 재구성이며 원시manifest를 당시에 저장했다고 주장하지 않는다.
- `.go.txt` 보관 후 Evidence의 `.go`파일0, `go list ./docs/connector/evidence/LBT-174/...` exit0/검색package0, 깨진 local Markdown link0이다. post-package 전체Go3753PASS/42pkg/18skip/0FAIL,vet/build exit0·source동일 원본도 추가 대조했다.
- 압축해제한 로그까지 포함한272개 텍스트파일에서 privatekey/JWT/Bearer/AWS key형식 후보0이다. 휴리스틱이므로 모든secret부재의 수학적 증명이 아니다. reviewer는 보호된 cloud/key값을 읽지 않았으며 root의7보호입력 exact값 대조와 구별한다.

검증 중 checker 자체의 추가 원본/cleanup 대조 기능을 확장해, freeze한243개 중 `independent-review/check_packaging.py`만 외부 현재판과 달랐다. package에 저장된 당시판의 rawSHA/bytes는 유효하며 결과 JSON `postSnapshotReviewerScriptUpdates`에 명시했다. 이는 제품 source나 원본 실행로그 drift가 아니다. 최종 출판에서는 이 최신 checker/보고서/JSON을 재복사하고 provenance/checksum을 갱신한다. checker가 생성하는 자신 결과를 포함한 후속 문서복사 무한순환을 새로운 제품 실행으로 취급하지 않는다.

작성자 발견 F174-DOC002는 test/evidence Low이며, `.go` 복사로 Go package 검색이 오염되는 문제다. reviewer는 사전 실패를 직접 실행하지 않았고 작성자 보고와 수정후 실제검색0·post-package fullGo를 확인했다. 열린 confirmed 제품 finding은 없다. 재현용 runner의 역사적 절대경로는 별도 작업폴더로 복사해 수정하고 overlay Replace key/value를 현재 절대경로로 재생성해야 한다. `.go.txt` 내용을 overlay value로 지정할 수 있다.

최종 판정은 [FINAL_AUDIT](FINAL_AUDIT.md)와 같으며 제안 코드·로컬 검증 범위 PR 가능이다. 원격CI/특수tagged-race·실제 SaaS/Browser C3·KT·운영TLS·장기부하와 최초Internet실패 원인미확정은 남는다. Jira전체완료·원격쓰기 승인이 아니다.
