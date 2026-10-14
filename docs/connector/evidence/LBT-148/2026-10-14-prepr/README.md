# LBT-148 PR 제출 전 최신 main 통합 검증

2026-10-14 KST. 최신 main `949c1a6dcc0e9fac2b1b72f9afca19c3ab998ed6` 위에 LBT-148 작업 7개 commit을 충돌 없이 rebase했다. 이번 자동 검증 source는 `283da48f98441395249a6ad897f62c3db18f52eb`다. 이후 추가 내용은 감사·검증 자료 및 historical폴더의 `.gitattributes` 바이트보존 보강뿐이다.

이번 fetch로 반영된 main 작업은 #93 LabSpec Mock 소유권, #92 Provision HTTP/CreationSnapshot, #91 main GitOps image tag update다. Connector 전체, VMAccess 실환경 test, PTY/CC-02 harness, VMAccess/SSH runtime, Connector/Realtime 계약 및 Go dependency는 이전 최종 검증과 동일하다. source manifest의 경계 비교와 range-diff를 참조한다.

사용자 지시에 따라 코드 변경 없는 `TestOpenStackVMAccessIntegration`은 재실행하지 않았다. 이번에는 OpenStack API·VM 생성·Reset을 실행하지 않았다. 기존 실제 PTY/CC-02 실행 source와 production/harness blob의 동일성을 확인하고, 133.81초 PASS 및 exact-ID cleanup7개·임시 keypair/Management SG0/0의 기존 원본 기록을 보존했다. Reset generation 교체·stale generation 거부·idle SSH 회수의 별도 VMAccess 실환경 테스트를 그 CC-02 결과로 대체 충족 처리하지 않는다.

[최종 감사](pre-merge-audit.md), [독립 리뷰](independent-review.md), [자동 검증 요약](validation-summary.json), [소스 비교](source-manifest.json), [이번 원본 로그](validation-logs.zip), [민감정보 검사](secret-scan.json), [SHA256](artifact-checksums.json).

이전 실제 VM·실패/수정 회귀 원본은 [2026-10-14 기록](../2026-10-14/README.md)에 있다. 거기에 적힌 rebase 이전 SHA는 원본 실행 당시 commit이며 이번 PR history의 SHA로 변경하지 않았다. manifest의 rebase commit mapping 및 동일 blob 기록이 현재 PR와 연결한다. 실제 VM 검증은 현재 PR source의 같은 Connector/Provider/harness를 사용해 재현할 수 있다.

과거2026-10-10의 텍스트5개는 Windows checkout의 CRLF 변환으로 checksum이 달라져 indexed Git blob의 원래LF바이트로 복원하고 하위 `.gitattributes`로 보존했다. 기존 checksum값·Git blob·rawzip은 변경하지 않았다. 옛 source snapshots/2026-10-14 manifest의 당시 CRLF byte hash는 실행 당시 기록으로 유지한다. 코드 blob 동일성과 당시 checkout bytes를 구별한다.

현재 PR source를 checkout한 뒤 기존 version-controlled runner를 사용한다. 도구·validator pins·환경 입력은 [기존 재현 안내](../2026-10-14/README.md#재현)를 따른다.

```powershell
../2026-10-14/reproduce.ps1 -Stage auto
../2026-10-14/reproduce.ps1 -Stage web -NodeBin '<Node bin>' -PnpmExecutable '<pnpm.cmd>'
../2026-10-14/reproduce.ps1 -Stage contracts
```

자동 stage는 inherited OpenStack/KT test·audit·integration opt-in을 모두 비활성화한다. SSH loopback 자동 테스트는 실제 SSH protocol로 수행했다. source snapshot 전후는 새 감사 artifact를 복사하기 전에 고정했다. archive는 이번 `logs` 디렉터리의 모든 원본 파일 및 rebase range-diff를 포함한다.

실제 Browser/SaaS C2, 운영 SFTP Tree/Read/Save, KT D1, PostgreSQL tagged integration 및 강제 재시작/장기 부하는 이번에 실행하지 않았다. 알려진 공백과 적용 이유는 최종 감사에 명시한다. 사용자는 작업 branch push와 PR 생성만 승인했으며 main merge는 금지했다.
