# LBT-158 / LBT-145 독립 pre-merge audit — 2026-10-13

## 최종 독립 판정

**구현·회귀·이번 KT 실측 검토 PASS. 현재 검토한 변경에서 해결되지 않은 PR 차단 결함은 확인하지 못했다.** 이는 사용자 승인된 작업 브랜치 push 및 새 PR 준비에 대한 독립 검토 판정이다. 구현자는 /root, reviewer는 /root/kt_naming_audit다. 마지막 독립 기록 복사·문서 판정 갱신 후 parent가 candidate 목록/publication scan/artifact manifest를 다시 고정해야 한다. 소스 변경이 발생하면 이 판정을 그대로 재사용하지 않는다. main push·merge·배포·Jira/지원팀 메시지 승인으로 확대하지 않는다.

최종 reviewed production HEAD `6a6fb4f97e676a80cfce27f85269f0627970fc9c`, main base `4501f447d7a8c527b17ff2e08ee1d2d2fd88a8aa`, branch `fix/LBT-158-ktcloud-d1-verification`. 당시 dirty/untracked까지 조사했다. 구현 source/contract/runtime/tool full diff 38개, 역사적 Oct7 Evidence13개, 새 proof 및 Oct7 README의 최신 index doc-only delta를 대조했다. 최초 cad79b0/base6d675c에서 기존 작업을 보존한 채 최신 main을 통합한 경위도 검사했다. Contributing의 Jira branch 규칙 적용이 적절하다.

Reviewer는 소스/테스트/기존 Evidence를 수정하지 않았고 credential값 열람, cloud API/SSH/mutation, remote writes 또는 테스트 중복 실행을 하지 않았다. parent 실행의 raw logs를 독립적으로 읽고 source/binary/export 해시 및 JSON 집합을 별도 계산했다. 이 문서만 작성했다.

## 직접 읽은 요구와 SSOT

개인 `C:/Users/user/Documents/ChatGPT/3차 2/PRE_MERGE_AUDIT.md` 및 repo AGENTS/CONTRIBUTING/TESTING를 적용했다. Jira145/158 원문·AC·상태·댓글·링크를 이번 audit에서 직접 조회했다. 145 updated2026-10-07T21:04:00.690+0900,158 updated2026-10-07T21:01:01.657+0900이며 둘 다 진행 중이다. 145 blocked-by158 및 Oct7 실패 댓글은 당시 기록으로 보존하며 이번 실측과 구분한다.

Confluence D18 page7241730 v5 / D20 page6881283 v7 / D19 page7176194 v7 / D26 page28672005 v1 / 검증전략9666637 v3를 직접 다시 읽었다. actual typed ID ownership, 이름·metadata 검색보조, immutable snapshot/old-new generation, UNKNOWN observe-before-mutate, host pin/관리망, Mock·실제 구분을 적용했다. D17/Dependency Map은 parent가 추가 조회한 원문·revision을 parent 감사에 기록했으며 reviewer가 직접 읽었다고 확대하지 않는다.

145의 실제 Provision/Reset/Reconcile/Cleanup·Connector→Management SSH·소유잔여0·SDK/KT경계 및 제약 Evidence와 158 최소호환 범위5/검증4를 기준으로 했다. C1/Backend-Browser 전체, PTY/SFTP, multiVM/전체 crash fault 조합을 새 AC로 추가하지 않았다. Audit 절차는 제품 계약을 새로 정하지 않는다.

## Findings-first와 처분

아래 findings는 코드·문서 수정 전에 parent에게 severity/위치/조건/영향/필요 회귀를 전달했고 parent가 사용자에게 보고한 뒤 수정했다. 실제 외부 자원 오삭제 사고를 관측한 주장이 아니다.

| ID·심각도 | 수정 전 조건·영향 | 해결 및 확인한 회귀 |
| --- | --- | --- |
| LC-R2/P2 | acceptance main recovery 약294–334, count-only foreign/duplicate/UNKNOWN/missing 관측을 기록·Cleanup하고 불완전 final에 false PASS. D18/D20 위반 | recovery.go18–47/scope.go41–114에서 whole typed ID+gen+logical+KNOWN source+Exists/state를 record/save/Cleanup 전에 검사. final 모든 exactID ABSENT 이후만 성공. before/final24negative, 정상partial/absent/read-only 및 observe/save/cleanup실패 raw PASS. resolved |
| F3/P2 | gated network fixture old85 WriteFile로 기존 ledger ID history 덮어씀 | cloud 접근 전 O_EXCL, heldfd Lstat/SameFile/regular 및 owned update. 실제 subprocess prior ledger complete/incomplete sentinel·동시claim1winner PASS. Windows symlink/replacement2subcase SKIP를 숨기지 않음. 코드resolved, crash atomicity 보장 아님 |
| PR-R1/P2 | fixture old100–155, ID/job불명인데 name/CIDR/comment 후보를 채택·DELETE | cleanupKTFixtureKnownResources219 이후 confirmed ID 없으면 manualledger 보존·후보lookup/adopt/delete0. unknownTier/FAILED·unknownjob/성공job mapping불명 negative 및 knownID 정상삭제+absence PASS. resolved |
| PR-R2/P2 | topology old295–305에서 attachedVolumeIDs를 single/image/size/owner 검사 전에 knownrefs로 upsert | current refreshKTCloudNICs290–304에서 exact single root50GiB/image/단일실제serverattachment 검사 후 기록. foreign/ambiguous/empty/wrongimage/size/server/multi/ID/503 및 후속Management lookup실패11case PASS. 정상확정root 보존. resolved |
| PR-R3/P2 | production old229/fixture old189에서 policy fullmapping 전에 소유ID 저장, !disable matcher가 empty/UNKNOWN/ERROR 허용 | current topology229–237/full fixture mapping253–266에서 successjob+단일ID+exact spec+explicit enable 이후만 채택. production14/fixture11case raw PASS. preflight의 unknown-policy 보수차단은 별도 유지. resolved |
| PR-E1/P3 | source-after-live verifiedAt17:05:55Z가 Control 종료17:08 및 final17:12보다 앞인데 post-execution으로 표기 | source 변경 없이17:15:04Z에 source348+binary3 새재검증mismatch0. reviewer도17:13:58Z 독립재계산0. 기록 정확성resolved |
| PR-E2/P3 | 최초 README reproduction6이 helper externalmodule+replace를 안내하지만 Provider/Protocol internal import 때문에 빌드불가 | parent가 수정 전 보고. checkout/.local에 .go.txt를 복원하고 existing go.mod로 빌드하도록 정정. helper-export-build.log 실제PASS 및 복원10Go와 frozenexports exactbyte일치를 reviewer 확인. cloud/API 실행 없이 doc-only resolved |
| PR-E3/P3 | 감사 Oct7 Evidence14개 표기가 실제13개와 다름 | 실제 fullHEAD51=source38+Oct7Evidence13, 디렉터리13, 감사119의13개 정정 직접확인. doc-only resolved |

실행 helper도 별도 검토했다. recordServer335–350 단일root/image/50GiB/exactserver 및 기존knownidentity 검증, inspect 임의volume추가 제거, exactserver/project/name/key/fixture 검증 전 access Cleanup금지, unknown 후보 미채택을 확인했다. outer50m/remote45m cap은 harness30m+teardown8m보다 길다. 이는 fixture 안전성이고 제품 SSH wait 변경이 아니다.

## Full diff 및 영향 검토

main→HEAD의38개 파일 전 범위와 변경 밖 caller를 대조했다. auth/client/compute/network/topology/delete/lifecycle/reconcile/terminal, boundednames와 모든 신규KT tests, 공통Provision/topology/quota/server/startup/query, Providerkind 상수, Connector/runtime owning docs, trackedacceptance main/recovery/scope/reset/SSH 및 tests를 읽었다. parent 감사의 full38-file table과 실제 파일 집합이 맞는다.

producer internal/server/connector/command.go60–109, inbound consumer106–140, Connector WSS handler374–405 및 schemas를 대조했다. Reset previous generation/nonempty/logicalName 검증은 latestmain에 존재한다. generic resourceType strings/actualID/gen 전달 구조라 KT kinds 추가는 wire enum drift 또는 fakeNeutronID가 아니다. HTTP/Realtime/DB schema/migration 변경은 없으며 Backend durable policy/Browser flow를 추가하지 않았다. 기존 표준 OpenStack 공통 경로의 extraction 및 KT 선택경계도 읽었다.

현재 server/startup에는 reverted SSH Ready classifier/permanenterror/logging feature가 없다. user 지시대로 초기 TCP/EOF후 성공은 boot/PAM readiness 맥락으로 다루며 새 defect로 분류하지 않는다. retry/timeout/fixed60s/PAM 정책 변경을 추가하지 않았다.

## 요구 → 구현 → 자동 검증 → 실제 Evidence

| 요구 | 구현·자동 검증 | 실제 이번 proof / 한계 |
| --- | --- | --- |
|158 최소 auth/endpoint 및 SDK재사용 | client135/KTclient25–103; exactendpoint/TLS/safeerrors/token-only/401once/non401 no retry tests | actual readonly New/Validate,server0/image12/flavor235; production query3PASS. tokenexpiry 동시401은 httptest |
|158 NSM·networkId/refId·root/NIC | network17–296/topology126–349; method/path/body/job/paging/malformed/mapping 및 root/policy ownership regressions | 46자Tier+FWcreate/job/deletePASS,UUID≠refID; VM 실제2NIC/root50GiB/Management IP. nofakePort/SG |
|145 immutable Reset/old-new generation | lifecycle37–79/KTlifecycle 및 scope resetReality; unsupportedprofile preflight beforedelete/exacttyped oldset tests | actualg1→old5absent/new5present, newSERVER ID다름, snapshot 동일; g1/g2 authenticated execPASS |
|D18/D20 failure·ownership·cleanup | known observation/candidate advisory, LC2/F3/PR1–3, 200fault≠empty/400≠absence/404listcontradiction/no blind retry | 실제 정상 known-onlyCleanup/all10absent. foreign/malformed/jobunknown destructive fault는 Fake이며 고객 프로젝트에 주입하지 않음 |
|145 Connector→Management SSH·소유0 | terminal actualserver/id/gen/VMKey/pin+Management noLabfallback; inherited credential/pin suite | production LinuxConnector+TLSMockSaaS CC01/OP01 실제API, g1/g2 pinned auth+exec 및 TCP22/443deny. 실제BackendBrowser C1/모든프로토콜 인터넷deny 아님 |
|158 명시적 gate·secret·재현 | ordinaryGo gateoff, safeerrors/보호localconfig/manifest/snapshots | KT2별도actualPASS, source348/bin3 freeze·remote2sha·최종0·publication scan. actualsecrets 미게재 |

공식 KT 공개 [Tier](https://cloud.kt.com/docs/open-api-guide/d/computing/tier/) 및 [NSM 안내](https://cloud.kt.com/docs/open-api-guide/d/nsm/how-to-use/), Networking 문서를 credential없이 읽었다. Tier guide는 실제 networkId/refId/기본Tier를 설명하지만 공개 field에50자삭제한도를 적지 않는다. 49자보수경계는 사용자전달 지원답변+실제46자삭제 성공에 근거하며 내부 KT 원인을 확정하지 않는다. Jira how-to-use 단일링크 web fetch는 internalerror여서 그 문서전체를 검증했다고 주장하지 않는다.

## 실행 결과와 원본 대조

모든 실행은 parent 수행이다. reviewer는 raw JSONL·phase logs 및 결과파일을 읽었다.

- Go canonicalLFformat329tracked파일, vet/test/build0; full991 top-level/2966PASS events/fail0. provider+harness race124top/317PASS events/fail0. PASS events에는 subtest/package가 포함된다.
- Web npmci239packages/audited240/0vulnerability, typecheck/lint/test/build0,13files163testsPASS. 현재22direct dependencyversion과mainlockfile도 직접대조0mismatch. Web warning은 PASS나 새실환경 proof로 확대하지 않음.
- OpenAPI, eightDraft202012JSONschemas/localrefs, Runtime YAML/migrationprefix 원본PASS. PostgreSQL -tagsintegration은 로컬미실행, PR CI PG16에서 확인예정이다.
- 일반 Go7live SKIP는 standardOpenStack5+KT2. 이후KTreadonly1.72s 및NSM46.33s explicitactualPASS로KT2를 채웠다. standard5는 사용자 실제실행금지. 별도 Windows ledger공격2subcase/POSIXcredentialmode1subcase도SKIP, LinuxCI실행예정이며 이미PASS라고 확대하지 않는다.

freshlive g1ProvisionSUCCEEDED5refs→pinnedManagementkey-authenticated exec/egressdeny→ReconcilePASS, g2ResetSUCCEEDED10refs→Managementexec→oldabsent/newpresent→emptyCleanup/candidate미채택→CleanupSUCCEEDED5refs raw를 확인했다. Controlfinal passed=true/plannedGenerations2/stageREAL_PRODUCTION_CONNECTOR_WSS_LIFECYCLE_PASS_OWNED_RESIDUAL_0, 총10typed refsDELETED. 두LabSERVER ID가 다르고 bootstrapSERVER와도 다르다.

bootstrap8=server1/root1/keypair1/public1/forward1/policy3, BOOTSTRAP_CLEANUP_CONFIRMED/deletedtrue. finalresidual2026-10-12T17:12:01Z exact10kinds각int0/ownedResidualTotal0/basicTier2/external1/network3/ALL_TRACKED_IDS_CONFIRMED_ABSENT 직접확인했다. final17:12:03.516Z dictionary는 baseline7kindexactIDs와 reviewer equalityTrue, SERVER/VOLUME/KEYPAIR/POLICY/PUBLIC_IP/FORWARD0·TIER3다. intermediatebootstrap8 residual은 최종0과분리보존했다.

prelive source348LFhash 및 binary3, helper10Go+wrapper11exports exactbytes를 직접재계산0mismatch했다. remoteConnector6e86adbbe49e1e39ac9c8eea4d76dfbbfff52b11940fb5ae8728ce1694ef090d/harness2eaf38ce1de1fb6d450e334316f5c848b28b02caf238c5adfde96fbfcc81cdb6 검증raw를 확인했다. buildinfo HEAD6a6/Gophercloud2.15.0/latestmain deps/vcs.modifiedtrue라 cleancheckout 또는최종Evidence commit에서bit-identicalbuild를 주장하지 않는다. 마지막source-after17:15:04Z의348/3 mismatch0은 실제종료후 검증이다.

newEvidence log16copies exactbyte일치, parent의 bootstrapexport에서projectId/sourceCIDR/publicIP만제외하고 나머지knownIDs그대로임을 대조했다. currentartifactmanifest63entryallhashmatch, candidate115paths는actualmain→HEAD51+newEvidence64exactset과일치했다. 이 기록복사후 수치는새로고정한다. publication scan17:13:20Z candidate110/11commits/189blobs·protectedexactvalues/commonsecretpatterns/findings0를 읽었다. reviewer는credential exact값을 직접열람하지않으므로 그 비교 실행은parentEvidence로명시한다. oldOct7 portaljpg를 로컬view_image로직접보아 password/token/contact노출없음을 확인했다.

보존검증latest1046byte-identical+7LFonly+12expectedcontentchanges+mainworkflowrename1=1066이며 unexpected0이다. oldEvidence3파일EOL/Oct7README index 변경을 bytes불변으로 과장하지 않는다. 원본byte는before백업에남으며 oldfailure문맥은유지한다. 기존실험590untracked보존local경로는이번PR게재대상과분리된다. audit/sourceafter/README/count정정외추가생산코드변경없다.

## 잔여 위험과 승인 경계

지원 profile은 단일UbuntuLinux/50GiB/InternetOutboundfalse/D1측정범위다. 이름hash는검색보조이며 actualtypedID가소유권이다. KTNSM/Cinderquota완전검증, tokenexpiry/concurrent401 및 모든APIfault 실제주입, multiVM/Windows/Internettrue, BackendBrowserC1/PTY/SFTP의실측을주장하지않는다.

trackedControl ledger 및 fixture WriteAt/Truncate/Sync는crashatomictransaction이 아니다. aggregate deadline child종료가defercleanup완료를보장하지않으며 ID없는mutation/원장손상/미기록g1candidate는수동조사·exactIDrecovery가필요하다. LC2 guard의FakePASS는프로세스중단의실클라우드복구PASS가아니다. 이것이정상liveAC proof를무효로하거나새전체faultAC를추가하는것은아니다.

reproduction6의 Go internal module경계는 parent secondpass가 발견했다. 독립reviewer가 최초 draft에서 놓친 안내오류였으며 최종에는 수정된문구·실제exportcompilePASS·10sourcecopy일치를 직접대조했다. 구현/실행source348/bin3는변경없다.

현재범위의필수findings는해소됐고full38source+관련contracts/callers+rawactualproof를독립대조했다. 마지막복사후finalscan/목록/hash갱신을parent가확인하면사용자승인범위의branchpush+새PR로진행할수있다. PR CI의Go/Web/Contracts/PG/OS공격branch검사는merge전별도게이트이며이번로컬PASS로대체하지않는다. Jira상태Done·mainmerge·배포·외부연락은이검토가실행하거나승인하지않는다.


## 公개 metadata·Git index 한정 최종 두 번째 확인

PR-E4/P3는 parent가 수정 전 보고한 doc/report-only hash 정확성 문제다. Windows CRLF raw SHA와 Git LF blob을 같은 해시로 간주하지 않도록 candidate-manifest.py는 `sha256/bytes`=publishedLF, `originalWorktreeSHA256/Bytes`=pre-Gitraw로 구분한다. 새Evidence 범위가 text-only이며 normalization은 CRLF→LF만 수행하여 Vite 출력 trailing space와 Vitest EOFblank를 제거하지 않는다. source/contract whitespace 검사와 원본 도구출력 보존을 구분한 README32/Audit51 문구를 확인했다. 생산348source/실행3binary/cloudstate 변경이 아닌 수정이다.

parent final순서 scan2026-10-12T17:26:05Z→manifest→restage를 수행한 뒤 reviewer가 Git `cat-file --batch`로 staged blob을 독립 읽었다. Evidence66entry의 publishedSHA/bytes 및 source348의 LFhash 모두mismatch0, candidate118paths와 실제main대비indexset equalityTrue였다. finalpublication scan197blobs/11commitSnapshots/protectedexactcredentialcomparison/commonpatterns/findings0 원문도 읽었다. reviewer는 credential을 직접열람하지 않았다.

검사 직후 WT Audit 문서에는 count PR-E3행과 최종판정문구 doc-only추가가 있어 index와 달랐다(AM). 논리내용은 적절하나 현재WT rawhash가 manifest와 같다고 주장하지 않는다. 이 append를 복사한 뒤 parent가 그 Audit 변경까지 포함해 마지막 scan→manifest→restage→artifact-onlyindexSHA 재검증하는 단계가 남는다. 실행 로그 원본을 trim/변조하는 해결은 필요없으며 하지 않았다. 새생산코드/전체API/live재검토 없이 metadata만 대조한 두 번째 패스다. 구현·실측 독립PASS판정은 유지하며, 최종공개metadata검증0mismatch를 확인한 뒤 승인된push/새PR로 진행한다.
