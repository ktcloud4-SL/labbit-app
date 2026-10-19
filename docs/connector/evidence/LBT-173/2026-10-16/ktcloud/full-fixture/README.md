# LBT-173 KT fresh full fixture

2026-10-16 KST **11:37:43–11:52:04**, 새 실행의 11개 단계 전부 exit 0. Provision → 1세대 forwarding → Reset → 2세대 forwarding → Cleanup → Connector 정상 종료 → bootstrap 정리 → 최종 inventory 확인을 한 실행에서 통과했다. 원래 실패/복구 실행을 합쳐 만든 결과가 아니다. [최종 audit](AUDIT.md), [독립 검토](independent-review/README.md), [재현 절차](RUNBOOK.md)를 함께 확인한다.

| 실제 검증 | 결과와 Evidence |
| --- | --- |
| 전체 단계 | inventory/create/access/ssh/deploy/control/collect/residual/cleanup/residual/final 모두 exit0. [시각·결과](phase-results.json) |
| actual production Control | Provision SUCCEEDED, Reset SUCCEEDED, empty Cleanup SUCCEEDED, final Cleanup SUCCEEDED. Control 실행 10분30.6초. [lifecycle](live-lifecycle.log) |
| 양 세대 forwarding | 실제 Workspace loopback3000 HTTP200/exact body 각2회, 공유 SSH, forwarding Close 뒤 PTY/SFTP, wrong target/port22·범위/refused 차단, Reset 뒤 old generation/target 거절. [lifecycle](live-lifecycle.log) |
| 정확한 Lab 부재 | g1/g2 각5개, unique typed ID10개 모두 DELETED. [최종 journal metadata](control-ledger.metadata.json) |
| 정상 종료 | production Connector가5초 안에 성공 종료, forcedKill=false, journal passed=true/final stage 정상종료 PASS. [lifecycle](live-lifecycle.log) |
| 최종 자원 정리 | Lab10+bootstrap8개 부재, 잔여10종 counter 모두0. [잔여](final-residual.json) |
| baseline |7종 exact ID 집합이 초기와 동일. Server/Volume/Keypair/Policy/PublicIP/Forward0, 공유 Tier3만 유지. [최종 inventory](final-inventory.metadata.json) |
| 실행 바이너리/소스 | 배포3개 SHA 검증, bootstrap 포함4개 빌드 digest. [배포](deployment-digests.log), [binary manifest](binary-manifest.json), [source543개](source-manifest.json), [build/source 대조](identity-verification.json) |

실제 KT D1 Provider API/VM/관리망 SSH/HTTP/PTY/SFTP를 사용했다. Ubuntu24.04 `os-ubuntu24.04-image-20260929`, `2x4.itl`(2CPU/4GiB),50GiB boot volume, bootstrap1대와 차례로 교체되는 Lab1대다. app은 `127.0.0.1:3000`에만 bind하며 app port를 공개하지 않는다. TLS/SSH pin 검증을 유지했다.

SaaS Control peer는 synthetic TLS fixture이고 production Connector child가 실제 Provider lifecycle을 실행했다. Forwarding은 같은 Connector host의 별도 acceptance 프로세스에서 production SSHForwarder/VMAccess 라이브러리를 사용했다. 같은 fixture manager에서 TCP/PTY/SFTP의 SSH 재사용과 격리를 검사했다. **실 SaaS/Browser C3와 Preview Control/Data WSS 전체 E2E는 미실행인 후속 범위**다.

## 변경·회귀·감사

사용자에게 수정 전 보고한 F-KT002/003을 보완했다. 이미 deleting인 owned root는 DELETE를 다시 보내지 않고 실제 부재까지 기다린다. 첫 조회 직후 자동삭제가 시작되어 DELETE400/409를 받으면 같은ID를 다시 조회한다. absent/deleting만 기존 absence poll로 이어지며 stable rejection/403/조회불명은 FAILED/UNKNOWN을 유지한다. DELETE 자동 재전송이나 force-delete 조건 확대는 없다.

최초 실패 대상은 g2 root volume였지만 당시 HTTP/status 기록이 없어 **이 두 코드 경합이 최초 실환경 실패의 원인이라고 확정하지 않는다.** [과거 실패와 복구](../README.md), 원래 journal false/exit1, 당시source/binary/log는 그대로 보존했다. 새 실행은 별도 새 자원·journal·4개 바이너리를 사용했고 recovery/재실행 없이 전체를 통과했다.

전체 Go41packages/3707test·subtest PASS,16skip/0fail. test 없는12package skip은 별도다. 전체 vet/build 및 관련9package race PASS. [원본gzip](go-test.jsonl.gz), [집계·skip 목록](go-test-summary.json), [race](race.log), [실행 exit 기록](verification-results.json). Web/Runtime source가 이전 local 검증과 동일해 기존 Web188tests/typecheck/lint/build/YAML 결과를 재사용했다. DB integration/실Browser를 일반 Go PASS에 포함하지 않았다.

독립 reviewer가34개 회귀·18개 외부 probe·semantic mutant/source hash/원본 집계를 별도 검토했다. 과거 source 및 guard-only mutant는 예상FAIL, 현재 source는 PASS다. [독립 검토](independent-review/README.md)에 실제 cloud 실행과 독립 원본 대조 범위를 나눠 기록한다. 수정 전 재현 실패, 초기 compile 실패, metadata helper의 역사적 docs6개 범위 차이 실패도 보존했다.

브랜치 `codex/feat/LBT-173-ssh-tcp-forwarding`, base/HEAD `88097be5d9bb6996b718fd185515ca324629fda6`, 미커밋 로컬 작업이다. 최신 main `8f88833`의 C2 test/웹 header 변경은 별도 대조했고 branch를 rebase하지 않았다. push/PR/merge/Jira 변경/외부 메시지 전송은 하지 않았다. KT 임시 fixture 실행·정리는 사용자의 명시적 승인 범위다.

공개 Evidence는 hash/count/lifecycle metadata만 담는다. 원본 Provider IDs/주소·자격증명·키·pin 파일·원본로그는 보호된 로컬 폴더에 유지한다. HTTP/PTY 본문과 SSH tuple 원문은 저장하지 않았다.
