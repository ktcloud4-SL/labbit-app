# LBT-174 최종 독립 검토

**검토한 로컬 구현·검증 범위의 PR 제안 가능. 미해결 confirmed product finding 없음.** 사용자 PRE_MERGE_AUDIT A–F를 구현과 분리된 reviewer로 수행했다. 전체 파일 목적·SSOT 추적·실패/보안/수명주기·findings 및 한계는 [상세 A–F 검토](AUDIT_DRAFT.md)에 있다. 이 결론은 push·PR 생성·merge·배포 승인이나 Jira 전체 완료 판정이 아니다.

Primary base/HEAD는 `0051d8d6803fd447f7b690dd895a0a928c3c7302`, branch는 `codex/feat/LBT-174-preview-local-integration`이다. 변경38파일(36Go+owning 계약2, 미추적Go19 포함)과 consumer 경계를 검토했다. 최종521파일 canonical LF SHA256는 `7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31`이며 검토/실제 실행/복원 hash 차이0, staging0이다. reviewer는 제품·기존 Evidence·서비스·클라우드를 수정하지 않았다.

독립적으로 실행 원본을 다시 집계했다. 아래 pass 수는 parent/subtest event 수이며 고유 요구사항 수가 아니다.

| 확인 범위 | 독립 결과·경계 |
| --- | --- |
| [기본 자동 검증](final-primary-automated-verification.json) | Go3827/42pkg/18skip/0fail, vet/build PASS. Windows race12pkg 합계1518PASS/16skip/0fail/0race. 기본 skip은 별도 실행 결과와 구분 |
| [Native Linux exact CI](native-ci-raw-verification.json) | CGO1 tagged vet/build/race, C1 parent12/event67/skip0/fail0/race0. 실제PG+MockProvider이며 원격CI·전체C3는 아님 |
| [ON actual lifecycle](final-on-lifecycle-verification.json) / [OFF actual lifecycle](final-off-lifecycle-verification.json) | fresh CREATE, 각 generation first12, 양쪽 DHCP renew 두 순서, 실제 boot-ID 변경·pinned SSH 회복, immutable Reset g2 PASS. ON sole Lab default/TCP Lab source, OFF zero IPv4 default/route unreachable/TCP denied. tracked ON16/OFF14 및 SG/keypair exact absence |
| [전체 OpenStack actual batch](final-cloud-batch-verification.json) | 정확 parent12/event14/skip0/fail0/child0. 실제 VM·SSH·HTTP와 Mock Control/Data Gateway. Preview 순차 HTTP200·격리·CLOSE·공유PTY/SFTP·오류·Reset/Cleanup·stale/fresh generation·Control secrecy 마커4개 PASS. Lab absence28 entries+SG/keypair GET 확인. Linux OS exact4경로/inner SKIP0, helper5개 final source·ELF·hash 일치 |
| [최신 main1e2 합성 검증](latest-main-reconcile-verification.json) | 별도535 source, Go3865/43pkg/18skip, vet/build/contracts PASS, 실제PG1433/5pkg/optional1skip/0fail·임시DB0/query0/stop0. branch36Go 동일. Web66/tree/lock/config가 앞선 실제 Web16file/217test/type/lint/build와 동일한 bridge. SQL9 별도 HEAD canonical/실행 중·종료 후 raw 동일성 확인; 실행 전 관측으로 과장하지 않음 |
| [패키지 전수 검증](package-independent-verification.json) | raw380/파일431 snapshot, 원본·변환 provenance/gzip161 복원, base+export 최종521 복원, changed38/미추적Go19, 이전 sealed646개 불변, Go discovery53 동일·Evidence Go0, 보호 입력7 exact/제한 heuristic 후보0, 링크오류0 |

[20:28 KST closing 원문·main·source 재조회](closing-context-verification.json)는 Jira174 본문/댓글과 Confluence7개 문서의 본문·버전, remote main `1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4`가 검증 기준과 같음을 확인했다. D18/D19/D20 및 Git §7b와 runtime 소유권을 대조했다. 관리망 지원은 직접 IPv4·단일 DHCP subnet·전용 Neutron DHCP·검증된 dnsmasq/Linux/RFC3442 조건으로 한정한다. API echo만으로 guest 라우팅을 수용하지 않았다.

독립 제품 finding RNET006은 수정 전에 보고하고 6case red→green으로 재검증했다. 단일 검증 port 객체만 주소·ledger에 사용하도록 해결됐다. PDOC001의 raw SSH 즉시 join 과장도 수정 전 보고 후 caller-facing 종료와 transport별 TCP max16 setup/pump/channel 잔존 경계를 구분했다. REPORT001의 OFF14 집계 정정, PACK001의 첫 검사 자기 링크 생성 순서 수정·원래 실패 보존도 확인했다. [findings와 검증 근거](AUDIT_DRAFT.md#e-수정-전-findings와-상태).

제약은 유지한다. 최초 삭제된 역사적 VM의 단일 물리 원인은 확정할 수 없다. 원격CI·LBT174 최종 KT Cloud·실제 SaaS/Browser 전체C3·다른 backend/guest·장시간 부하/hard crash는 미실행이다. 비협조 SSH peer의 raw TCP 작업은 peer CLOSE나 transport idle/invalidation/shutdown까지 남을 수 있다. live는 reviewer가 직접 재실행하지 않고 실행 원본·명령·source를 독립 대조했다. secret 검사는 가용 입력과 패턴에 한정되며 모든 비밀 부재의 증명은 아니다.

이 보고서와 갱신된 독립 결과는 검토한 raw380 snapshot **이후 추가분**이다. 구현자는 이 추가분과 최종 판정 문구를 재export한 뒤 기존380원본 불변·추가분 provenance·링크/secret·최종 checksum을 다시 검사하여 봉인해야 한다. 제품·tested source와 기존 봉인646개를 바꾸지 않아야 한다. [검토 snapshot hash](package-independent-snapshot.json)를 보존했다.
