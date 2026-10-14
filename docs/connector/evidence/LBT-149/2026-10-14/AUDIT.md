# LBT-149 pre-merge audit — 2026-10-14 KST

판정: **LBT-149 구현·검증 범위 완료 / PR 가능**. 최신 SSOT 추적성, 실제 VM acceptance, 전체 기본 검증과 독립 리뷰가 충족됐다. 이 문서는 사용자의 `PRE_MERGE_AUDIT.md` A–F 절차를 수행한 기록이며 더 큰 OP02 전체 완료를 뜻하지 않는다. push·PR·merge·배포·Jira 변경·외부 메시지는 실행하지 않았다.

## A. 목적·버전·전체 diff

- Repository: `https://github.com/ktcloud4-SL/labbit-app.git`, 작업 브랜치 `feat/LBT-149-sftp-workspace`.
- 기준 main/base: `0599947407e9edd4539690e2f56eecf0622909ce` (LBT-148 PR95 통합). 최종 fetch에서도 같은 main을 확인했다.
- 최초 구현 `c753ce9c72c7f13773d3bcfbdb53bddc331343b2`, 저장장치 오류 수정 및 최종 tested source `a495184e13a159c0cca25256dd8d0766da83283e`.
- base부터 최종 tested source까지 22파일, 2개 local commit. 검토 후보는 a495 + runtime EOF의 불필요한 빈 줄1개 제거 + 이 Evidence 디렉터리의 전체 추가 diff다. staged/unstaged/untracked 전체를 포함하여 확인했다. Evidence는 감사·원본·재현 artifact이며 production 기능 변경이 아니다. 정확한 각 파일의 delivery byte SHA256/tested·delivery Git blob은 `source-manifest.json`에 기록한다.
- 실행 중 source snapshot 전후를 별도로 보존했다. Go 최종 검증은 a495, Web 및 최초 Contracts는 c753에서 실행했다. a495 수정4파일은 helper 오류 분류·관련 Go 회귀·실 VM fixture에 한정되며 Web/HTTP/Connector wire/runtime/dependency의 blob은 c753와 동일하다. Web은 a495에서 다시 실행하지 않았다. 최종 Contracts는 아래 whitespace 정리 후 current dirty-source snapshot의 후보에서 다시 실행했다.
- 최초 live-second는 commit 전 실행이며, 후속 live-final은 c753를 실행 전 pin하여 다시 빌드·실행했다. 저장장치 수정 후 live-after-storage는 a495 snapshot을 실행 전에 기록하고 Linux helper/test binary를 새로 빌드한다. 앞 실행의 snapshot을 소급 적용하지 않는다.
- 최종 diff 검사에서 runtime EOF blank1줄만 제거했다. LF 정규화 후 EOF newline을 제외한 내용은 a495와 현재 파일이 동일하며 reviewer의 PyYAML parse 비교도 동일했다. 이후 current dirty-source snapshot을 찍어 Contracts 전체/Reset schema를 다시 PASS했다(`contracts-final`). helper/Go/application/env 설정값은 미변경이므로 실제 VM/Go 검증을 반복하지 않았다. 최종 runtime bytes를 a495의 runtime bytes와 동일하다고 주장하지 않으며 manifest에 두 hash와 차이를 기록한다. 이 외 tested26파일의 source bytes는 동일하다.
- 기존 다른 worktree의 변경은 수정하지 않았다. 검증 runner가 만든 `__pycache__`는 원본 manifest에 남기고 종료 후 생성된 단일 bytecode/빈 directory를 제거했다. 재현 runner는 bytecode 생성을 비활성화한다. Web pnpm lock은 tracked npm lock에서 임시 import한 후 제거하며 npm lock hash 불변을 확인했다. ignored node_modules/dist 및 외부 local credential/key는 재현 입력이며 commit하지 않는다.

| 파일 | 변경 목적·적용 요구사항·영향 경계 |
| --- | --- |
| `cmd/labbit-workspace-sftp/main.go` | Linux SSH exec stdio helper, administrator installation 검증, listener 없음, 종료 시 stream/handle 수명 |
| `docs/connector/workspace-sftp.md` | SSOT 링크 및 operator build/install/config, Linux 지원·이전 VM failclosed·부분 Save·후속 transport 한계 |
| `go.mod` | direct pkg/sftp v1.13.11, indirect kr/fs v0.1.0; 기존 dependency 버전 유지 |
| `go.sum` | 해당 두 dependency checksum4줄; dependency 다운로드와 readonly build 검증 |
| `app/app.go` | optional WorkspaceFiles 구성·기존 공유 Manager 재사용, 두 env 설정·Provider validation failclosed |
| `app/runtime_provider.go` | lazy WorkspaceValidator 위임, 인증 lazy/cache/context 경계 유지 |
| `app/runtime_provider_test.go` | lazy 위임/취소/missing capability, assembly factory 미호출·partial config 실패 회귀 |
| `openstack/provision.go` | immutable snapshot WorkspaceVMKey를 각 Nova Server metadata에 기록; 기존 Operation/Reset 경로 유지 |
| `openstack/provision_test.go` | 실제 Nova 생성 request payload의 Workspace designation 검증; HTTP fake |
| `openstack/terminal_target.go` | File 전용 Workspace 지정 검증, Provider ownership·generation/NIC/pin은 OpenSession에서 재확인 |
| `openstack/terminal_target_test.go` | designated/worker/legacy metadata missing/VM 불일치 거절; HTTP fake |
| `openstack/workspace_file_integration_test.go` | 폐기용 local cloud 실제 VM/SSH/helper/SFTP/PTY 및 Linux kernel EFBIG fixture, tracked cleanup |
| `workspacefile/service.go` | canonical Tree/Read/Save·revision·요청 size·stable error mapping, per-path lock/16slots/context/독립 SSH 채널 |
| `workspacefile/service_test.go` | in-process 실제 SSH protocol fixture에서 contract/concurrency/target/cancel/timeout/disconnect/sibling 보존 검증 |
| `workspacefs/path.go` | 기존 HTTP canonical 상대 POSIX 규칙, UTF8/byte limits/dot/percent escape/control 거절 |
| `workspacefs/root_linux.go` | O_NOFOLLOW openat root fd pin, administrator anchors/helper 검사, inode flock |
| `workspacefs/root_other.go` | Linux/Windows 외 filesystem 실행 unsupported failclosed |
| `workspacefs/root_windows.go` | Windows in-process 자동 테스트 지원, 실제 helper 설치 unsupported |
| `workspacefs/security_linux_test.go` | 실제 Linux root/symlink swap/atomic escape/flock 회귀; Linux VM에서 실행 |
| `workspacefs/server.go` | rooted SFTP requests, regular existing handle, first-write digest/SameFile guard, 같은 handle truncate, listing 실제 JSON budget, safe I/O 오류 |
| `workspacefs/server_test.go` | rename/in-place edit/후속 mutation·close 충돌, Tree budget/invalid 이름 filtering, storage errno 회귀 |
| `runtime/contract.yaml` | Workspace root/helper owning 설정 추가, both configured/secure Linux 설치 및 후속 File WSS 경계 |

위 `app`, `openstack`, `workspacefile`, `workspacefs` 경로는 `internal/connector/` 아래다. 영향을 받는 기존 VMAccess OpenSession/Setup/retire/idle/Close, Terminal PTY factory, runtime Run의 lazyProvider, File SaaS consumer 오류 매핑, HTTP canonical path 구현을 함께 읽었다. 미검토 production 변경 파일 없음. Evidence runner/fixture/source manifest/raw logs/archive/scan/checksum도 추가 diff 검토 대상이다.

## B. 최신 원문·SSOT 추적성

Atlassian Rovo에서 직접 재조회: [LBT-149](https://samsunglions.atlassian.net/browse/LBT-149) updated `2026-10-14T12:20:40.288+0900`, comment0, 진행 중. [LBT-21](https://samsunglions.atlassian.net/browse/LBT-21) OP02 stage4/6, 선행145/147/148 이후, [LBT-150](https://samsunglions.atlassian.net/browse/LBT-150) File Control/Data WSS 조립, LBT151 OP02 최종 audit/PR, LBT22 Browser/SaaS C2 후속. [D-18](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/7241730) 확정 v5, [Connector MVP](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071) v19를 재조회했다. 원문 버전·관련 AC 요약은 `requirements.json`에 있다.

Git AGENTS/CONTRIBUTING/TESTING 및 Backend 책임 문서, [Connector §7a](../../../../../contracts/connector/README.md), File Control/Data schemas, [HTTP File API](../../../../../contracts/http/openapi.yaml) 및 `internal/server/workspacefile/path.go`, runtime SSH/File 설정을 직접 대조했다. Browser realtime/DB migration은 변경 및 새 consumer 연결이 없어 이번 구현 적용은 없음이다. 구조 계약 validator에서는 해당 파일도 전수 검사했다. 실제 SaaS/Browser/DB 전체 경로를 검증했다고 주장하지 않는다. Git/File 계약과 Jira 사이의 미해결 충돌 없음. SSH exec를 통한 rooted SFTP는 D18의 SFTP over shared SSH를 구현하며 외부 wire의 메시지/field를 추가하지 않는다.

| 요구사항·AC/Decision | 원문·버전 | 구현 파일·라인(a495) | 자동 테스트·실행 | 실제 실행·Evidence | 공백·판정 |
| --- | --- | --- | --- | --- | --- |
| 현재 generation 지정 Workspace의 공용 SSH/독립 SFTP | Jira 작업1·AC2, D18v5 | service.go:320, openstack/terminal_target.go:23, provision.go:80; 기존 vmaccess | BoundaryAndSharedPTY, WorkspaceDesignationFailsClosed, 기존 host key/ownership/generation 단위+race PASS | actual target3종 거절, 실제 공유 SSH identity 동일, PTY 보존; live-after-storage(a495) | 충족. metadata 없는 이전 VM은 failclosed, 새 Provision/Reset 필요 |
| Workspace root와 canonical path·absolute/traversal/symlink escape 거절 | Jira 작업2–3·AC2, HTTP/Connector§7a | service.go:100/252, path.go, root_linux.go:68, server.go checked/open | CanonicalPaths PASS, Linux RootRejects/SymlinkSwap 실제 VM PASS | outside file 불변, final/ancestor symlink·absolute/traversal Read/Save 거절 | 충족. root privileged mount/ACL 관리자는 운영 경계 |
| Tree는 허용한 직계 regular 파일/디렉터리 | Jira 작업4, Connector§7a entries/1MiB | service.go:119, server.go Filelist | TreeReadSaveContract, TreeBudgetsAllowedJSONEntries3분기 PASS | 실제 Tree4entry, symlink/FIFO 제외; Linux 3000valid/3000invalid/5000overbudget | 충족. 최종 Data WSS envelope budget은150 |
| Read 정확한 body/revision·요청 size | Jira 작업5·AC3, File read schema | service.go:159/242/106 | empty/read/revision/64+1/request cap PASS | 실제 read-save-read exact bytes/revision, empty, 65byte TooLarge | 충족. wire UTF8/encoding/meta validation은150 |
| 기존 파일만 Save, 첫 mutation 직전 revision 비교·충돌/없음/permission | Jira 작업6·AC3, File save schema§7a | service.go:186, server.go OpenFile/writeHandle, root_linux.go:18 | ConcurrentSaveRejectsOneStaleWriter, regular rename/in-place/afterwrite replacement, errno test PASS | 실제 stale conflict·missing미생성·permission, truncate/empty, EFBIG→INTERNAL_ERROR 후 불변 reRead | 충족. kernel EFBIG 실제; ENOSPC/EIO/EDQUOT는 합성 errno 자동 검증 |
| cancel/timeout/SSH·SFTP 단절 정리·다른 채널 보존 | Jira 작업7·AC3, D18v5 | service.go:320–397, 기존 Manager lifecycle | activecancel/hunginit/timeout/disconnect 재Read safe errors + race PASS, existing cancel/isolation 반복 PASS | 실제 pre-cancel·active init timeout, 같은 SSH에서 PTY marker, PTY close 후 Read | 충족 범위 명시. 실제 packet blackhole/Save중강제단절 미실행; loopback 단절과 실제 timeout 분리 |
| 실제 VM 정상 Tree/Read/Save·path/size/revision/permission 실패 | Jira 작업8·AC1/2/3, MVPv19M4 | workspace_file_integration_test.go | opt-in fixture, 정상 auto에서는 skip | 실제 local OpenStack Linux VM + administrator helper + production Service, tracked7absence·추가 read-only inventory0 | 충족. KT 실cloud/운영배포/Browsers는 이번 실행 아님 |
| 후속 transport가 사용할 기능 경계 | Jira AC4·150 경계 | app.go:270, runtime_provider.go:154, Service public methods | FileLazyAssembly factory0·validation delegation·partialconfig/missing validator failclosed PASS | 운영 설치 후 실제 Service acceptance; VM API 아닌 Connector 경계 | 충족. FileControl/Data 연결·cap 광고는 후속150에만 포함 |
| Runtime mandatory pairing·관리자 설치 | runtime/contract.yaml:552, D18 | New:79, root_linux.go:24, helper main:19 | invalidconfig/lazy assembly·Linuxroot tests PASS | root-owned helper/ancestors, ubuntu root/file permissions, actual helper 실행 | 충족. admin mount/ACL·Linux/proc prerequisites 문서화 |
| 내용·raw errors 비저장, control/data 분리·부분 mutation 재시도 금지 | AGENTS/Connector§7a | stable Error/classify, no logs, no retry in Save | stable error tests·source inspection·secret scan | fixtures는 메모리에서 본문 assert, 원본은 고정 case/lifecycle metadata만 | 충족. 실패 후 새 Read 필요; Save rollback/wholefilesystem transaction 아님 |

## C. 실패·보안·수명주기 분석

- 입력: UTF8/4096bytes/segment255/canonical slash/dot/control/backslash/percent rejection; root/helper는 quoting된 canonical absolute config. SaaS 입력은 decode/normalize하지 않는다. Config 하나누락/Provider capability부재는 failclosed. Read limit+1 및 Save length 한도로 allocation/compare bound, Tree entries 실제 JSON budget을 적용한다.
- Workspace/ownership: client selected VM만 믿지 않고 immutable snapshot metadata를 조회한다. 이후 Manager가 LabInstance/current generation/project/management NIC/status/pin/SSH key를 검증하므로 cached 연결도 이전 generation을 활성화하지 않는다. 새 metadata가 없는 이전 VM은 명시적으로 거부하며 임의 retrofit하지 않는다. 기존 Terminal 접근에는 Workspace 지정조건을 추가하지 않는다.
- symlink/root: static 구성요소 rejection과 os.Root 원자적 confinement은 별개다. Linux openat NOFOLLOW root fd를 pin하여 root 이름 교체를 따라가지 않는다. regular open의 타입을 다시 확인하며 FIFO/device는 읽지 않는다. root administrator ancestors/helper 소유권 검사를 실제 helper startup에서 수행한다. privileged mount/ACL 악의적 변경을 같은 VM의 root administrator로부터 격리하지 않는다.
- Save concurrency: canonical target/path mutex와 Linux inode flock으로 동일 서비스·별도 helper·hardlink alias의 writer를 보호한다. 파일 create/trunc flags를 쓰지 않는다. body revision 비교 및 helper의 first-mutation digest/현재path SameFile 확인, 후속 truncate/Close namespace 검증으로 stale handle 성공을 막는다. 외부 Terminal writer의 flock은 advisory다. 검증 후 syscall 사이 또는 여러 chunk의 전체 원자 transaction, rollback, durable filesystem save를 보장하지 않는다. 시작한 Save의 cancel/disconnect는 일부 변경이 남을 수 있고 새 Read가 필요하다. 자동 Save 재전송 없음.
- 채널: caller는 context에 반환하며 pipes·SFTP client·독립 session을 닫고 worker가 bridges를 회수할 때 slot/path lock을 해제한다. SSH transport/idle/retirement/Reset/shutdown은 기존 Manager 소유이며 다른 PTY를 닫지 않는다. bounded16workers, perpath locks refcountcleanup. 정상 sshd timeout/sibling보존은 실 VM, TCP단절/activecancel은 loopback에서 검증했다.
- 잔여 가설: 비협조 peer/TCP blackhole에서는 SSH CLOSE를 안 받아 bridge가 retirement까지 남을 수 있다. 16slot과 해당 path lock은 그동안 보존되어 capacity를 제한할 수 있다. 실제 blackhole/장기부하/Save중패킷유실 검증은 미실행이며 확인된 leak 결함으로 보고하지 않는다. 공유 PTY 보존 때문에 전체 연결을 임의 forceclose하지 않는다.
- storage 오류: 일반 OS I/O failure는 SFTP FxFailure→INTERNAL_ERROR, permission/notfound/pathescape는 의미를 구분한다. Go1.27의 unexported os.Root escape sentinel은 PathError.Err의 정확한 고정문자열만 검사한다. 전체 path-bearing error를 출력하지 않는다. 향후 Go 버전 변경은 실제 escape regression의 검토 대상이다.
- 재시작/외부시스템: 별도 durable File 작업·새 infra mutation/idempotency 모델을 만들지 않는다. helper는 단일 exec 수명, 프로세스/SSH 단절 때 openhandles를 RequestServer가 닫는다. 이후 요청은 새 helper에서 현재 VM filesystem을 원본으로 Read한다. Provision metadata는 기존 immutable snapshot 경로를 사용하며 operation unknown/reconciliation을 바꾸지 않는다. 실 VM 생성 fixture는 모든 반환 resource를 즉시 추적하고 실패·성공 양쪽 Cleanup을 수행한다.
- transport 적용 제외: WSS session/admission/revoke/correlation/full envelope/UTF8/encoding·HTTP session response mapping은 새 Service consumer가150에서 연결되기 전까지 동작하지 않는다. file-v1은 광고하지 않는다. Browser/SaaS/C2·DB/migration 변경과 실제 검증은 후속 범위다.

## D. 테스트·Evidence

`validation-summary.json`은 원본 run metadata로부터 생성하며 raw archive와 대조한다. Windows에는 make가 없어 같은 fmt/vet/unit/build 및 Web typecheck/lint/test/build entrypoint를 portable runner로 실행했다. Go mod download 및 locked Web dependency install이 setup equivalent다. pnpm adapter는 tracked npm lock을 import하고 frozen 설치하며 원 npm lock을 변경하지 않았다.

- c753: Go 전체 unit3495 PASS/14skip/38packages, 기존 Connector7package race702 PASS/12skip, 회귀20반복420 PASS, Workspace race12 PASS/1skip. Web13파일/167테스트 PASS + typecheck/lint/build. OpenAPI/JSONSchema/Draft2020-12 ref/runtime/migration workflow와 Reset schema PASS, validator pins PyYAML6.0.3/jsonschema4.26.0/openapi-spec-validator0.9.0.
- a495: 최신 Go fmt425파일/vet/unit3496 PASS/14skip/build, 기존 Connector race 및 Workspace race/20반복 결과는 최종 summary 참조. 최종 실제 VM 결과는 live-after-storage 원본 참조.
- 자동 unit/race는 in-process TCP SSH + local filesystem/HTTPfake/MockSaaS를 사용한다. Windows symlink 권한 없는 `TestSymlinkRejectedAndExcludedFromTree`는 skip이며 실제 Linux VM fixture에서 final/ancestor symlink 거절을 검증한다. Linux-only3test는 Windows build에서 미포함이며 실제 VM regression binary에서 실행한다. live OpenStack/KT opt-in tests는 기본 auto에서 skip된다. PostgreSQL tagged integration은 실행하지 않았으며 변경에 DB 모델/transaction이 없어 적용 제외한다.
- 실제 VM 경계는 local OpenStack API·Provision·Linux sshd·admin helper installation·rooted SFTP Service·PTY다. SFTP 테스트의 standard unrestricted client는 fixture/helper 설치에만 사용하며 production acceptance는 전용 rooted Service다. 실제 File WSS/SaaS/Browser는 실행하지 않았다.
- RLIMIT_FSIZE=0은 kernel EFBIG를 발생시키는 실제 write 실패이며 저장장치 full을 흉내 내는 Mock이 아니다. 실제 ENOSPC/EDQUOT/EIO/갑작스러운 VM reset·관리자 mount 변경은 실행하지 않았다. errno-wrapped PathError 자동 검증과 구별한다.
- Cleanup: 각 live run의 Provision7개를 exact tracked IDs로 삭제하고 Provider 조회 부재를 확인한다. fixture keypair/Management SG는 별도 t.Cleanup에서 삭제하고 후속 read-only inventory로 keypair/SG 및 lbt149 Server/Network/Subnet/Port0을 확인한다. inventory는 자원을 변경하거나 ID/raw 응답을 기록하지 않는다. 전체 사용자 cloud 모든자원이0임을 뜻하지 않는다.

초기 실패를 보존한다:

1. 첫 loopback `TestTimeoutDisconnectAndSafeErrors`가 TCP close 직후 watcher retirement 이전의 immediate Read 성공을 기대해 FAIL했다. fixture를 bounded 독립 Read 재시도로 고쳤으며 Save 재시도는 추가하지 않았다. 원본 도구출력은 이 chat에 있고 archive에는 초기 raw가 없다. 후속 unit/race 정상 로그는 archive에 있다. production retry/known-safe bypass를 추가한 수정이 아니다.
2. 최초 live helper 설치 exec 단계 FAIL124.84초, tracked7absence 확인. `live-initial-failure.log`를 그대로 보존했다. 당시 stderr가 수집되지 않아 원인은 확정하지 않는다. 다음 fixture에 noninteractive sudo/단계&&/고정경로를 redacted한 진단을 추가했고 live-second PASS122초였으나 이를 입증된 rootcause fix로 주장하지 않는다.
3. F-IO-004 수정 전 `TestStorageErrorsAreSafeFailures` expected FAIL은 `storage-classification-red.log`, production c753 classifier + 현재 동일 regression test source로 재현했다. 수정 후 pass와 실제 EFBIG를 별도로 보존한다.

환경: Windows amd64 Go1.27.1/GCC16.2.0 UCRT/Python3.12/Node24.19.0, Linux amd64 CGO0 helper + actual ubuntu/m1.small/sharednet1 local cloud. 실제 fixture opt-in/Cloud credential·SSH private key는 외부 local 파일로 주입하며 repository에 복사하지 않는다. 재현 source/명령/도구 pins/비민감 fixture 입력은 README/reproduce.ps1/validate.py/residual149.go.txt에 있다. archive는 원본 bytes를 보존하고 SHA256/secret-scan으로 검사한다.

## E. 수정 전 findings와 처리

| ID·심각도 | 당시 revision·파일/라인 | 구체적 재현·영향·근거 | 수정·필요 회귀·최종 상태 |
| --- | --- | --- | --- |
| F-REV-001 / P2 | 최초 uncommitted service.go:223–238 | Open/read A 후 경로를 다른 regular B로 rename교체; stale A write 후 성공. Jira 쓰기직전revision/현재path 정확성 위반. 독립 code review confirmed | firstmutation SameFile/digest+samehandle truncate/Close, inodeflock; rename/inplace/postwrite 회귀 및 실제 Linux tests PASS, 해소 |
| F-TREE-002 / P2 | 최초 uncommitted server.go:230–232 | 3000×50valid 이름 실제 약231KiB를 과대budget으로거절, invalid percent 이름도 filtering전budget차지. File entries 허용계약 위반 | canonical 먼저filter + actualJSONbytes budget; 3000valid/invalid·실제over1MiB3분기 Linux/Windows PASS, 해소 |
| F-APP-003 / P1 | 최초 uncommitted app.go:273; runtime_provider.go 위임누락 | actual Run이 lazyProvider로 Build; File env를 켜면 WorkspaceValidator type assertion 실패하여 Connector 시작불가. 코드분석 confirmed | lazy ValidateWorkspace delegation/failclosed/context, assembly factory0/partial config regression PASS; 해소 |
| F-IO-004 / P2 | c753 server.go:58–68/248–249 | canonical Save의 ENOSPC/EIO/EFBIG/EDQUOT를 INVALID_PATH로 반환; SaaS invalid_path/400오보고. reviewer codeanalysis + expectedred confirmed | generic FxFailure→INTERNAL_ERROR, root escape/permission/notfound유지; errno red→green 및 실제 kernel EFBIG wholeboundary+readback PASS, 해소 |

모든 confirmed finding은 수정 전에 사용자 commentary에 보고했고 해당 방향으로만 수정했다. 초기 fixture 실패/자료 한계/TCP blackhole 미재현 가설은 제품 confirmed finding과 구분했다. 검사 범위에서 미해결 confirmed finding 없음이라는 최종 판정은 F의 마지막 리뷰 이후에만 기록한다.

## F. 독립 리뷰·최종 판정

구현하지 않은 `/root/review_lbt149`가 같은 base와 22파일 전체 diff, 영향받는 VMAccess/app/PTY 및 최신 Jira/D18/MVP/owning Git 계약을 직접 읽었다. 처음 F-REV-001/F-TREE-002, 마지막 F-IO-004를 독립적으로 발견했다. 구현자는 각 근거를 검증하고 사전보고 후 수용·수정했다. reviewer는 F-APP-003 수정과 나머지수정을 다시 검토했고 별도 관련 package race를 직접 실행했다. `independent-review.md`는 reviewer 메시지의 검토범위/한계/실행을 기록하며 전체 Browser/SaaS가 검증됐다고 확대하지 않는다.

독립 reviewer가 source/Evidence 추적성과 hash를 직접 대조했고 미해결 confirmed finding은 없다. 최종 a495 실제 VM123.27초 PASS, Go3496pass/14skip/38package·관련 race·420반복 PASS, 마지막13:08KST inventory6항목0을 확인했다. 원본109entries와 당시 checksum을 독립 확인했으며 이후 runtime EOF 검증 원본을 추가하고 판정문/독립 리뷰 갱신 후 checksum을 재생성했다. 최종 artifact 개수·현재 archive entries/hash는 manifest/checksum을 기준으로 한다. 4confirmed findings는 수정 및 회귀/실VM 재검증으로 해소됐다.

따라서 LBT149의 완료조건(Tree/Read/Save actualVM, Workspace/root 경계, revision/size/error/취소 정리 검증, 후속 transport용 Service)이 이 범위에서 충족됐다. TCP blackhole 등 위 잔여 검증 경계는 명시한 한계이며 actual Mock/VM/Browsers 범위를 확대하지 않는다. LBT150 File WSS·LBT151 OP02최종감사·LBT22 Browser/SaaS C2는 후속으로 남는다. Audit 통과는 원격 push·PR 생성·merge·배포·Jira 상태변경·외부 메시지 승인이 아니다.
