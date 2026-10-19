# LBT-174 post-admin 독립 audit — 최종 로컬 검토

판정은 **검토한 로컬 구현·검증 범위의 PR 제안 가능**이다. 최종521/7e8 source의 fresh CREATE·ON/OFF·양쪽 DHCP 갱신·실제 재부팅·Reset, native CI, 전체 OpenStack fixture12개, 새 main 별도 합성 Go/실DB와 패키지를 독립 대조했다. 미해결 confirmed product finding은 없다. Jira 전체 완료·원격 CI·전체 SaaS/Browser C3·다른 backend/guest의 수용이나 push/PR 생성 승인을 뜻하지 않는다. 파일명은 기존 링크를 유지하기 위한 것이며 최종 판정은 [FINAL_AUDIT.md](FINAL_AUDIT.md)에도 기록했다.

## A. 범위와 source

사용자 AGENTS, 저장소 AGENTS/CONTRIBUTING/TESTING 및 외부 PRE_MERGE_AUDIT A–F를 직접 대조했다. 구현과 분리된 reviewer다. base/HEAD는 `0051d8d6803fd447f7b690dd895a0a928c3c7302`, branch는 `codex/feat/LBT-174-preview-local-integration`이며 미커밋 변경이다. 최종 source521의 canonical LF aggregate는 `7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31`이다. [파일별 상태·해시·테스트 index](source-scope-index.json)에 Evidence 외 전체 38개 변경 경로(36 Go / 2 owning 문서)를 기록했다.

파일별 목적과 경계는 다음과 같다. 아래 그룹에 index의 38개 경로가 모두 포함된다.

| 변경 파일 | 목적·영향 경계 |
| --- | --- |
| `preview/{client,manager,session}.go`, `preview/{lifecycle,upgrade_cancel}_test.go` | 수신 순서의 OPEN 예약, attempt별 TCP/WSS 소유권, pending Upgrade 취소, bounded Close, stale 결과와 새 attempt 분리, 정상 EOF의 logical session 유지 |
| `wss/{handler,handler_preview}.go`, `wss/{handler_preview,preview_owner,preview_replacement}_test.go` | Control sender·credential·owner epoch 고정, 64 worker admission, retirement와 OPEN/CLOSE의 authority lock, 알림 context 분리 및 replacement trust 종료 |
| `app/{app,vm_access}.go`, `app/vm_access_test.go` | 실제 App의 SSHForwarder 주입 유지, Reset/Cleanup 결과에 따른 generation invalidation, Shutdown fence |
| `provider/openstack/{management_network,port,topology,provision}.go`, `{management_network,management_subnet_affinity,management_classless,provision}_test.go` | standard Neutron 지원조건 preflight, exact canonical onlink121과 blank router 생성, 원래 subnet ID/CIDR 보존, 단일 검증 응답의 관리 주소 사용, known ID 보존과 드리프트 거절 |
| `provider/openstack/{integration,integration_cleanup,integration_diagnostic}_test.go` | opt-in actual lifecycle·보조 SG/keypair exact GET absence, 전용 SSH 진단 transport 취소·출력 join |
| `provider/openstack/{preview_transport_export,preview_transport_integration}_test.go` | test-only export로 실제 App/Provider/VMAccess 재사용, strict Mock Gateway와 실제 VM HTTP/PTY/SFTP/generation/cleanup 조합 |
| `provider/openstack/{workspace_file_integration,workspace_file_fixture,workspace_file_integration_gate}_test.go` | Setup admission과 긴 명령 Wait 분리, cancel/peer-close 회귀, 실제 Linux 바이너리의 exact PASS·skip gate |
| `vmaccess/tcp_test.go` | 정상 SSH 초기 설정과 의도적으로 stall한 caller deadline 구분, 외부 2초 bound로 timeout 무시 mutant 검출 |
| `server/realtime/{trace_propagation,trace_log}_test.go` | ACK 후 첫 log 지연을 허용하되 비어 있지 않은 malformed JSON은 거절하는 polling 회귀 |
| `server/preview/previewtest/{peer,refused_windows,refused_other,refused_test}.go` | Windows 실제 closed-loopback refusal의 test-support 분류. 제품 forwarding 정책·timeout을 바꾸지 않음 |
| `runtime/contract.yaml`, `contracts/connector/README.md` | 직접 연결 IPv4/단일 DHCP/host-route 없음/전용 Neutron DHCP/검증된 dnsmasq Linux 범위와 121 onlink 정책 명시 |

이전 Evidence 패키지는 수정하지 않았다. post-admin 실제 원본은 외부 root OUT에 있으며 새 패키지의 raw380/파일431 snapshot을 독립 전수 대조했다. 최종 보고서·검증 파일 추가와 최종 판정 문구 갱신 후의 재export/seal은 구현자가 별도 수행한다. 검토 snapshot과 그 후 추가되는 파일 수를 혼동하지 않는다. 별도 로컬 checkout에 최신 main과 변경을 합성한 검증은 primary branch 결과와 구분한다.

## B. 요구사항 → 구현 → 검증 추적

Jira와 Confluence 최신 본문·댓글·버전을 직접 refresh한 [SSOT snapshot](ssot-refresh.json) 및 [context 차이](context-refresh-check.json)를 사용했다. 19:51:44 KST의 재조회 이후 20:28:27 KST에 [closing 원문](ssot-closing-refresh.json)과 [직접 대조](closing-context-verification.json)를 추가했다. Jira LBT-174 updated `2026-10-16T14:59:23.423+0900` / comments0, D18v5·D19v7·D20v7·전략v3·플랫폼v27·결정v11·추가요구v3의 본문·버전과 remote main1e2는 모두 직전 검증과 같다. Git Preview wire의 owning SSOT는 `contracts/connector/README.md` §7b와 preview control/data schema다.

| 요구·AC / 원문 | 구현 | 자동 검증 | 실제 경계·공백 |
| --- | --- | --- | --- |
| CC-03: 실제 SSH TCP 선연결 후 outbound Data WSS, 승인한 정확 target / Jira174·Git §7b | 실제 App SSHForwarder와 manager Open·client attach | forwarder 기존 회귀, lifecycle/handler fixture | final521/7e8의 actual Preview fixture PASS. 실제 App/VMAccess·SSH·VM HTTP와 Mock Gateway 조합이며 Direct fallback을 추가하지 않음 |
| attach correlation·한 active tunnel·sequential / §7b schema | attempt 예약, exact attempt socket handoff, 중복은 같은 pending ready 대기 | stale attach·동일 attempt·active conflict·sequential 회귀 | Mock Gateway actual VM 조합과 전체 SaaS/Browser C3를 구분 |
| Byte stream만 Data WSS, 종료 시 남은 TCP bytes 전달·logical 유지 / §7b | 두 relay 방향, binary-only, TCP EOF 후 tunnel Close | arbitrary frame boundary HTTP·text violation·normal EOF 회귀 | 본문·Cookie를 Control/log/DB에 저장하는 새 경로 없음 |
| CLOSE/취소/timeout/Shutdown이 pending 작업을 회수 / Jira174·§7b | manager fence, exact tunnel ctx, raw Upgrade socket cancellation, bounded WSS close | pending dial/attach/Upgrade·blocked writer·Shutdown 회귀 | generic net.Conn의 임의 Close 구현까지 보장하는 테스트는 아니며 production VMAccess managed Conn의 소유권을 사용 |
| Credential revoke·Control replacement의 trust 종료 / §7b4001/4002 | captured sender+credential, owner epoch, registry retire를 authority lock 내부에서 확정하고 IO는 밖 | stale owner·blocked callback·64 worker·direct replacement·normal EOF 보존 회귀 | 서로 다른 Credential·session의 새 authority를 빌리지 않음 |
| Reset immutable snapshot·세대 invalidation / D19·§8a | accessLifecycleProvider, Provider preflight·nics 원래 ID/CIDR | preflight 실패 DELETE0·partial 결과 invalidation·stale generation | ON/OFF actual Reset g2와 Preview Control Reset/Cleanup 후 old WSS·logical session·stale target 거절, fresh HTTP PASS |
| Internet ON은 Lab egress, OFF는 외부 route 없음·관리 SSH 독립 / D18·runtime | Management에만 router blank+validated onlink121. Lab DHCP와 immutable UserData 유지 | ON/OFF payload·두 CIDR·invalid input·policy/address drift·same observation 회귀 | blank121 actual 실패와 candidate v3 별도 보존. 최종 fresh CREATE ON/OFF 각각 first12·renew양순서·reboot·Reset PASS |
| UNKNOWN·부분 자원은 known IDs로 추적, blind mutation 금지 / D20 | 생성 ID 먼저 보존, 검증한 관측만 ledger upsert, lookup/드리프트 시 중단 | create/read option 누락·Reset failure·same-ID outside prefix·foreign-ID 회귀 | 최종 ON tracked16/OFF tracked14 및 Preview Lab entries28+각 SG/keypair exact GET absence PASS. 전체 cloud 임의 사용자 자원0을 주장하지 않음 |
| 진단 timeout도 bounded return·remote 소유 route 정확 정리 / 사용자 후속 요청 | 전용 SSH client cancel+Run join, 외부 remote alarm·bounded children·owned route finally | 10회 cancel loopback + 외부 method 회귀 | initial timeout probe joined=true; session 종료만으로 remote route 부재를 추정하지 않음 |
| Test 의미 보존 / TESTING | realtime log wait, caller timeout, refusal OS 분류, Setup/Wait 분리 | 각 before-fail/after-pass·semantic mutant·latch·Linux exact-test gate | 기존 실환경·실DB·native 실행의 source 차이와 최종 재실행을 구분해야 함 |

원문 링크는 snapshot 내 각 원문 URL 및 저장소 계약 경로에 보존했다. 새 HTTP/WSS 필드·DB schema·module dependency 변경은 없다. latest main `ee05485bd9a1668f222e7a54fb5f880ea197e35a`는 SaaS worker/DB migration/프런트 후속 변경이 있다. 별도 latest-main 합성 checkout은 Go 3,865 pass / 43 package / 18 skip, vet/build 및 Web typecheck/lint/test/build exit0이며 Web 16개 파일·217개 테스트 PASS다. source531, branch의 변경 Go36개, tracked Web66개 hash 일치를 직접 확인했다. 실제 PostgreSQL migration8/9가 포함된 5 package 명령도 1,415 pass / optional `TestDumpPreviewWireSamples` 1 skip / fail0 / exit0이다. [별도 통합 source·검증 경계와 원본 해시](latest-main-integration-check.json). 이 별도 checkout을 primary branch rebase나 전체 SaaS/Browser C3 성공으로 표시하지 않는다.

최종 remote GET 중 main이 `1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4`로 진행됐다. SHA-only fetch는 refs·checkout을 갱신하지 않았다. [15개 경로 영향](latest-main-1e2-impact.json)과 production 원문을 직접 확인했다. 새 LBT179는 조회 전용 reconciliation worker/store 및 runtime 설명이며 provider/connector/Preview/realtime/HTTP·WSS wire/Web/module 변화0, SQL migration 추가0이다. 이후 새 별도 합성 checkout의 [1e2 통합 원본 대조](latest-main-reconcile-verification.json)는 source535/branchGo36/hash0차이, Go3865/43 package/18 skip, vet/build exit0, realPG1433/5 package/optional wire1skip/0fail, DB0/query0/stop0을 확인했다. Web66개와 lock/config/tree가 이전 실행과 동일하여 16file/217test/typecheck/lint/build 결과를 재사용한다. SQL9개는 source535의 Go/module/contracts/runtime 범위 밖이므로 별도 HEAD canonical 일치, root during-run/post-run raw·canonical 동일성을 확인했다. SQL 최초 관측이 DB 시작 전이었다고 주장하지 않는다.

## C. 실패·보안·수명주기

- preflight는 extra DHCP 확장, exact ACTIVE network와 단일 subnet, IPv4/DHCP/host routes, SSH prefix 완전 포함, Lab non-overlap를 읽기 검증한다. unsupported/401/5xx/ctx는 mutation 이전 거절한다. exclusivity와 실제 guest/backend 지원은 배포 전제·실제 수용 조건이며 API echo만으로 보장하지 않는다.
- 생성한 Management port에만 canonical 두 옵션을 넣는다. router empty는 JSON에서 누락/null로 바뀌지 않으며 121은 exact canonical CIDR+zero-next-hop다. numeric option 이름·249·default121·빈121·중복·null·다른 CIDR·IPv6는 거절한다.
- 알려진 새 port ID는 policy 검증 실패 전에도 남는다. 최종 post-Nova 응답은 original ID/network/subnet/prefix/SG/options 전체를 검증한 뒤 동일 객체를 사용한다. 독립 fault가 foreign cleanup ID나 밖의 SSH endpoint로 전환되지 않는지 확인했다.
- 신규 runtime 제한은 standard Neutron 분기에 적용된다. KT topology 분기를 표준 Neutron 정책으로 우회시키지 않는다. 공유 subnet/SG·snapshot UserData를 제품이 바꾸지 않는다.
- callback은 session lock 해제 후 manager callback으로 이어진다. authority→registry retirement와 새 OPEN admission이 직렬화되고 retirement IO는 authority lock 밖이다. 새 owner의 session은 old callback/4001/4002에 같이 제거되지 않는 회귀가 있다.
- 결과 알림은 취소된 tunnel ctx로 shared Control write를 중단시키지 않도록 captured sender와 bounded WithoutCancel ctx를 쓴다. old sender가 새 sender로 갈아타지는 않는다. owner가 이미 종료되면 결과를 보내지 않는다.
- caller-facing Preview socket/relay/handler 종료와 VMAccess의 raw SSH 수명은 다르다. 기존 `tcp.go`의 TCP setup/pump/channel은 transport당 최대16 budget으로 제한되며 비협조 peer의 EOF-only/late open은 SSH CLOSE 또는 idle·generation invalidation·Shutdown에 의한 transport 종료까지 남을 수 있다. 모든 raw worker의 즉시 join·전역 goroutine0을 주장하지 않는다. root 보고서 초안의 과장을 PDOC001로 수정 전 보고했고 최종 문구가 이 경계를 명시함을 확인했다.
- 현재 상태는 ephemeral session의 lifecycle fix다. durable operation persistence·schema·replay를 추가하거나 결과 불명 mutation을 재시도하는 변화가 없다. process 재시작은 기존 SaaS/Provider reconciliation 경계를 따른다.
- 실제 DHCP ACK의 router3 잔류와 option121 전달을 분리했다. “빈 router가 wire에서 제거됐다”거나 실제 미노출 daemon 버전이 확인됐다고 주장하지 않는다. 초기 역사적 VM의 단일 원인은 원본이 없어 확정 불가다.

## D. 실행 Evidence 경계

독립 실행: [같은 관측 전후 회귀](management-observation-regression.json)는 final521/7e8 전후 전수 hash0차이, 수정 전 2개 production 파일 overlay에서 expected exit1(정상1 PASS/invalid5 FAIL), final에서 exit0(6case PASS)다. [4case SDK HTTP status 회귀](request-status-probe.log.gz)는 loopback만 사용한다. [독립 GET allowlist](backend-configuration-run.json)는 이전 source520/86ec에서 actual DHCP agent Dnsmasq driver를 확인했고 installed version은 API 미노출이다. cloud mutation은 없었다.

실제 post-admin stage별 독립 원본 대조는 [postadmin-observations.json](postadmin-observations.json)와 [DHCP_BACKEND_REVIEW.md](DHCP_BACKEND_REVIEW.md)에 있다. strict initial FAIL, observer helper TypeError FAIL, original PUT helper FAIL, candidate-v2 fast renew FAIL, candidate-v3 external candidate PASS를 보존했다. v3는 wire121 decoded exact-prefix/zero-next-hop/no-default 및 sole Lab default, TCP Lab source, cleanup을 확인했지만 source520의 제품은 blank121였다. 현재 source521/7e8의 수용으로 표시하지 않는다.

final521/7e8의 실제 JSONL을 독립 집계했다. [최종 primary 자동 검증](final-primary-automated-verification.json)은 fullGo 3,827 test pass / 42 package pass / 18 skip / fail0, Windows race11 package 1,443 test pass / 16 skip / fail0 / race warning0와 별도 server/preview 75 pass / skip0 / fail0(합계12 package / 1,518 test pass / 16 skip), vet/build exit0다. source521 hash 차이는 0이다. pass 수는 parent/subtest 이벤트를 포함하며 고유 AC 수가 아니다.

[Native 배포 archive 검증](native-archive-identity.json)은 archive SHA와 metadata 일치, root embedded source manifest와 최종521개 파일 hash 일치, unsafe path/link/private .local·.git·clouds.yaml 포함0을 확인했다. 압축을 풀거나 실행하지 않고 member를 직접 읽었다. 최초 reviewer suffix 검색은 역사 Evidence의 동명 manifest를 잡아 실패했으므로 [방법 실패](native-archive-initial-reader-failure.json)도 남기고 exact root 경로로 수정했다.

최종521/7e8의 [native 원본 집계](native-ci-raw-verification.json)는 Linux/amd64 Go1.27.1·CGO1·gcc13.3.0, exact tagged vet/build/race marker 모두 1회, C1 12 parent / 67 pass event / skip0 / fail0 / race warning0을 확인했다. 이는 실제 PostgreSQL + Mock Provider C1 범위이며 실제 OpenStack opt-in 12 parent와 다른 검사다. [바깥 ON lifecycle 원본 집계](final-on-lifecycle-verification.json)는 child exit0 / 1002.157초, source521/7e8 전후0차이, fresh first12와 Reset g2 first12, 두 renewal 순서 각각 pre/post reboot/post Reset의 6회, sole Lab default·실제 TCP Lab source, 실제 boot-ID 변경 후 pinned SSH를 확인했다. 의도 remote timeout 후 route 부재와 fresh 명령 성공, A/B 원상복구, apt 임시 route 없음, tracked16+SG/keypair exact absence, PG 임시DB0/query0/stop0도 확인했다. KT Cloud·원격 CI·Browser/SaaS C3는 현재 범위에서 미실행이다.

Internet OFF 기본 경로 부재는 기존 reachability-only 테스트만으로 충족 처리하지 않았다. 별도 외부 OFF overlay가 false snapshot, generation1/2 각각 first12, 양쪽 DHCP 갱신 순서, 실제 boot-ID 변경 및 pinned SSH 회복, zero IPv4 default/target route 없음/TCP nonzero를 검사하고 known-resource cleanup을 유지함을 직접 읽었다. [OFF 방법 검토](off-method-review.json)는 로컬 overlay compile 및 생성한 3개 Python command AST만 검사했고 source521 전후 차이0이다. 이후 [actual OFF 원본 집계](final-off-lifecycle-verification.json)는 child0/375.406초, source521/7e8 전후0차이, first24·renew6순서·reboot/Reset·SSH·tracked14+SG/keypair exact absence PASS다. copied native CI 부속 파일은 OFF 테스트에서 실행되지 않고 PG도 사용·정지하지 않았다. 최초 내부 전달에서 OFF 정리 수가16으로 제시됐으나 reader가 원본14를 확인하고 바로 알려 최종 값은14로 기록했다. 제품 실행 실패가 아닌 집계 숫자 정정이다.

최종 [전체 OpenStack batch 원본 대조](final-cloud-batch-verification.json)는 정확한 TestOpenStack parent12/pass event14/skip0/fail0, test package elapsed1978.776초·바깥 명령1981.656초/child0, source521 및 Go/module507 전후 동일을 확인했다. 실제 Preview의 순차 HTTP200 두 번·동시 session 격리·Control CLOSE·공유 PTY/SFTP·대표 오류·Reset/Cleanup·stale/fresh generation·Control secrecy 마커4개를 모두 확인했다. Preview Lab resource absence28 entries와 SG/keypair GET absence를 확인했으며 각 fixture의 known-resource 검사와 전체 cloud 임의 자원0을 구분한다. Linux OS exact PASS4경로/inner SKIP0, helper5개 실제 ELF/hash/build-source 일치도 확인했다. 이 batch는 non-race 실제 OpenStack+Mock Control/Data Gateway이며 별도 native C1 CGO1/race와 합쳐 전체 SaaS/Browser C3로 표시하지 않는다.

[독립 패키지 검증](package-independent-verification.json)은 root checker를 실행하거나 패키지를 쓰지 않고 raw380/파일431 snapshot을 전수 읽었다. 원본380 SHA·길이, gzip161 복원 및 mtime0, Markdown suffix 변환, changed38/미추적Go19·base0051+export로 최종521 파일 복원, source aggregate, tracked patch, 이전 sealed646개, package Go0/unsafe경로0·Go discovery53 동일을 확인했다. 가용 보호 입력7개 exact 비교 및 제한된 secret heuristic 후보0, broken Markdown link0이다. 보호된 값은 산출물에 기록하지 않았다. 모든 가능한 비밀의 부재를 증명하는 검사는 아니다. [검토 snapshot hash](package-independent-snapshot.json)를 보존했다. 새 reviewer 보고·결과 추가와 판정 문구 수정 후 root의 재export/seal은 이 snapshot 이후 단계다.

## E. 수정 전 findings와 상태

| ID·심각도 | 근거·위치·재현 | 상태 |
| --- | --- | --- |
| RNET004 / Medium | source520/86ec `management_network.go:81–82`의 blank121 API echo와 actual Neutron-origin lease dual-default. 외부 valid onlink121 ACK/renew는 sole Lab default | 제품 onlink121 수정·단위 회귀·final ON/OFF fresh/reboot/renew/Reset actual PASS |
| RNET005 / Medium | source520/86ec `management_network.go:108`: 같은 subnet ID이나 밖의 fixed IP가 관리 endpoint로 사용될 수 있음 | 원래 CIDR 보존과 prefix 검증 구현·회귀 확인 |
| **RNET006 / Medium** | source521/8b3 `topology.go:155–167`: 첫 GET 밖의 IP, 둘째 GET 정상 → SUCCEEDED·outside SSH 시도 | [수정 전 보고·재현·최종 전후 회귀](OBSERVATION_FINDING.md). final521/7e8 단일 검증 객체 + ledger 순서로 코드 경계 해결 |
| PMETHOD001 / Low | 외부 `candidate-onlink/local_probe_test.go:176` PUT 기본 성공코드에 정상 200 제외 | 사용자 보고 후 explicit200/201+exactGET, 독립 4case 회귀 PASS. 원본 actual status는 미수집 |
| REPORT001 / Low | root가 `summarize_final.py:65`에 ON/OFF 공통16개 가정을 두었다고 수정 전 보고. 독립 raw reader는 OFF 실제14개와 초기 전달16 불일치를 확인 | root의 ON16/OFF14 분리 assertion과 최종 summary/AUDIT 값을 직접 확인. 실제 OFF PASS는 불변 |
| PDOC001 / Low | `write_audit.py:54` 초안이 비협조 SSH peer의 late channel까지 즉시 join하는 것으로 읽힘. 기존 TCP max16 raw worker 소유권과 다름 | 수정 전 보고 후 caller-facing Preview 종료와 transport 소유 TCP budget/잔존 수명을 분리한 최종 문구를 직접 확인. 제품 수정·새 테스트 없음 |
| PACK001 / Low | root 독립 checker `verify_package.py:95`가 자기 결과 링크를 해당 파일 생성 전에 검사해 첫 실행 거짓 broken link | root가 사용자 수정 전 보고. 원래 실패 JSON 보존, pending 결과 선생성 후 첫 조건 재실행 PASS. 별도 reviewer 전수 링크검사도0 |

이전 RNET/METHOD/Preview/fixture findings는 기존 역사 Evidence에 보존돼 있으며 해결 전 PASS로 바꾸지 않았다. 이번 pass에서 독립 재현한 제품 RNET006은 해결됐다. 보고서·집계·checker Low 항목도 위와 같이 구분했다. 검토한 범위에서 미해결 confirmed product finding은 없다.

## F. 독립 판정·잔여 범위

구현자와 분리된 두 번째 리뷰다. 38개 코드·test·owning 계약의 전체 범위와 소비 경계, 최신 SSOT, source identity, 실패·보안·소유권을 대조했고 새 관측 분리 결함을 독립 재현하여 수정 전 보고했다. 제품 소스·기존 Evidence·서비스·클라우드 자원은 수정하지 않았다.

**판정: 검토한 로컬 구현·검증 범위의 PR 제안 가능.** final ON/OFF fresh/renew/reboot/Reset/known cleanup·native C1·fullGo/Windows race/vet/build·실제 OpenStack fixture12개·별도 main1e2 합성 Go/실DB와 raw380 패키지 검토를 통과했다. 이 판정은 기존 역사적 최초 실패의 단일 원인 확정이나 Jira 전체 완료를 뜻하지 않는다. KT·전체 C3·원격 CI·미검증 backend/guest·장시간 부하/hard crash는 별도이며 final export/seal에서 보고서 추가분도 해시 검사해야 한다. primary는 로컬 branch만 작업하고 push/PR 금지를 유지한다. reviewer는 live를 재실행하거나 직접 관측하지 않았고 실행자가 남긴 실제 원본·명령·source와 owning 계약을 독립 대조했다.
