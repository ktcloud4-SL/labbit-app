# PR196 collector WorkReconciled P2 — independent incremental audit

## Decision and fixed revision

현재 검토한 로컬 수정에서 P2 분류 오류는 해결됐으며 새로 확인된 P0/P1/P2 결함은 없습니다. 운영 검증 완료나 전체 DR 승인을 의미하지 않습니다. 사용자 NO PUSH·실제 AWS/서울 서비스/공유 DB/Slack 금지는 유지됩니다.

- Worktree: C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-dr-stage3-live-prep-20261123
- Branch: codex/feat/LBT-60-stage3-live-prep-20261123
- HEAD: f5413c91282888115ad2b497d5c2183ab58f2e70
- Full PR base:753eecbff737409a4c9cc8c542f3c6baa6acf0da
- Reviewed increment:uncommitted internal/drcontrol/collector.go and collector_adapters_test.go. No product/test edits by reviewer.
- PRE_MERGE_AUDIT.md, App AGENTS.md, CONTRIBUTING.md and TESTING.md were read. Prior unrelated Infra/GitOps task was stopped and was not continued.

## Finding before correction

P2-COLLECTOR-01 / P2 — original f5413c9 internal/drcontrol/collector.go:209–221,254–274. A trusted, correctly targeted/fresh SQL WorkSnapshot can contain positive Operations/Items/OpenBoundaries/PendingResumes/PendingRestrictions/ProviderUnresolved. With missing/expired/bad-signature/wrong-target SafetyAttestation, the original code never reaches its positive-count FAIL branch inside ok&&validWork and leaves WorkReconciled=NOT_VERIFIED.

That contradicts runtime/dr-control.md's confirmed-positive-count FAIL and independent-FAIL-preservation semantics. Both FAIL and NOT_VERIFIED currently block ACTIVE, so this is evidence classification and loss of confirmed negative information, not a demonstrated ACTIVE authorization bypass. Parent reported the finding to the user before implementation; reviewer independently confirmed the original branch structure before reading the correction.

Earlier independent report pr196-linkage-review-20261123.md stated that verified nonzero inventory remains FAIL, but did not test nonzero inventory crossed with invalid/missing external safety. This incremental review corrects that coverage limitation explicitly; prior broad PASS is not used to dismiss the missing combination.

## Implementation and traceability

| Requirement / owning source | Producer / implementation | Consumer and tests | Result / limit |
| --- | --- | --- | --- |
| Confirmed positive work count is FAIL independently of missing Provider/Fencing evidence; runtime/dr-control.md collector section | SQLWorkReader read-only repeatable-read inventory; Collect validWork and dbUnreconciled before SafetyAttestation branch | New6 counter x6 safety matrix, TestCollectorKnownDBBacklogDoesNotRequireExternalSafetyToFail | Valid/fresh exact SQL inventory sets WorkReconciled FAIL and WorkInventory PASS; unrelated SingleWriter/Fencing remain NV for invalid safety |
| Missing/untrusted inventory is NV, zero alone cannot prove Provider completion | Existing exact DB/user/writer/scope/revision/epoch/PAUSE_NEW/time and nonnegative checks retained | TestCollectorUnverifiedDBCannotEstablishReconciledFailOrPass:zero/query failure/wrong DB/user/writer/scope/revision/epoch/mode/time/negative | Zero valid counts still need external Provider PASS; unverified positive snapshot does not manufacture FAIL/PASS |
| Confirmed FAIL survives later collection expiry | Existing expiry logic clears only non-FAIL fields | TestCollectorKnownDBBacklogFailSurvivesCollectionExpiryAndActiveVerifier | FAIL retained; stale positive observation is historical negative evidence, not fresh work completion |
| ACTIVE requires fresh authenticated safe observation | drcontrol.Verifier.RecheckResume→workercontrol.ResumeProof.Validate | Signed-observation verifier check, existing verifier rejection tests, race regression | Owning proof checks five safety results PASS and Active WorkReconciled PASS. No approval, signature, target/TTL, DB mutation, key or global gate path changed |
| Independent external FAIL survives missing SQL evidence | Existing SafetyAttestation branch | Existing TestExternalSafetyRequiresBoundImmutableSources confirmed-fail-with-sql-error and Provider missing/NV/invalid cases | Existing behavior retained, no promotion of SQL backlog into external Fencing/SingleWriter evidence |

The change defines dbUnreconciled once, applies FAIL only within validWork before parsing external evidence, and reuses the predicate in the existing combined branch. It never creates PASS from counts alone. Exact validation of all six count fields, SQL identity, request binding and observation freshness is unchanged. No storage/schema/API/Runtime field, network request, command execution, mutation permission or resource lifecycle change was introduced.

## Full-diff reuse and limits

The full base→f5413c9 PR was previously reviewed through the independent operator/linkage/publication reports, including producer→collector→signer/verifier→restriction/ResumeStore consumers, auth/purpose, TTL, UNKNOWN, migration and deployment boundaries. That previous full review is reused only for unchanged baseline. This pass directly inspected the complete new local diff plus relevant SQL WorkReader, Observation DTO, runtime/dr-control.md, Runtime contract, signer/Verifier and owning ResumeProof. It does not claim to re-review all historical Evidence or rerun every baseline integration suite.

Latest external Jira/Confluence state was not independently queried in this task because remote operations were prohibited. Parent supplied unchanged LBT-60/D01–D06/205 context; the directly inspected owning Runtime already specifies this classification, so the fix does not add a new product decision. HTTP, Connector and Browser protocols are unchanged and no new external field is introduced; DB migrations are unchanged.

## Independent execution

Executed Windows local unit/mock/race only:

`go test -race ./internal/drcontrol ./internal/server/workercontrol ./cmd/labbit-dr-control ./cmd/labbit-dr-evidence -count=1 -json`

Result:exit0, four packages PASS,281 passing test events including subtests, zero fail/skip. Original JSONL:C:/Users/user/.codex/tmp/pr196-collector-p2-review-tests-20261123.jsonl. Also directly ran the36-combination known-backlog matrix successfully on the corrected source. The first focused invocation overlapped the implementer's source update and is therefore attributed only to corrected source, not presented as a baseline failing run.

No integration build tag, production DSN, AWS/Kubernetes, shared DB, Slack, Linux CI or real approval/deployment execution was used. Parent broad regressions remain separate submitted evidence. The affected deterministic classification does not require a new real cloud test; actual collector readback/runtime readiness remains independently unverified.

Nonblocking test clarity:the expiry+ACTIVE test also has WorkInventory NV, so its consumer rejection is not an isolated proof of WorkReconciled FAIL alone. Owning ResumeProof directly rejects Active WorkReconciled!=PASS, and existing tests cover that condition as NV. Adding WorkReconciled FAIL with the other five results PASS to the verifier matrix would make the exact FAIL consumer regression explicit. This was reported as test strengthening, not a confirmed implementation defect.

## Final boundary

P2-COLLECTOR-01 resolved within inspected offline code and unit/race scope. Existing ReplicaLag policy FAIL, four mandatory NOT_VERIFIED, execution_gate=HOLD, recovery_authorized=false and cloud_verified=false remain. Physical Fencing/SingleWriter, actual Provider reconciliation, operational keys/Policy, multi-Pod cloud rollout and actual Pause/Resume/Failover are not accepted by this correction.

Source hashes and any later test-only additions are recorded in a separate local snapshot/increment note. No push/commit/merge/deployment or external message is authorized by this report; this user task explicitly remains local.

## Final test-only consumer increment

The nonblocking consumer regression suggestion was accepted. internal/drcontrol/verified_test.go:165 now adds WorkReconciled FAIL to TestUnverifiedProofCannotCreateIntent while setup retains the other five safety fields PASS. The existing signer/verifier/request-hash path is used; the test asserts no successful execution/INTENT. Product collector source and the prior matrix remain unchanged.

Independent focused race rerun: `go test -race ./internal/drcontrol -run '^TestUnverifiedProofCannotCreateIntent$' -count=1 -json` => exit0,19 passing test events including parent/subtests,0 fail/skip. Original JSONL:pr196-collector-p2-review-consumer-20261123.jsonl. The preceding four-package281-event race result predates only this test-only addition and is not relabeled as a whole-suite run after it.

Original RECONCILE_ONLY semantics remain unchanged:ResumeProof.Validate requires the five common PASS conditions for both modes but WorkReconciled PASS only for Desired==Active. The new collector FAIL does not independently grant reconciliation; it still requires fresh authenticated scope/approval and common safety proof. Existing workercontrol active/reconcile transition cases passed in the independently executed four-package race run. This correction intentionally does not forbid controlled reconciliation of unresolved work, and it does not relax ACTIVE.

Parent reports baseline reproduction (24 failing subcases plus parent failure), initial fixture Mode typing error and subsequent full regressions; these are parent-executed evidence, not independent baseline reproductions. The reviewed final source increment is now three files only, reflected in the updated source snapshot. No further confirmed finding remains within this incremental code/test scope.

## Final four-file and Evidence review increment

The final product collector remains unchanged (same reviewed implementation). Two test-only lines now set ProviderReconciled=FAIL and assert WorkReconciled FAIL in the existing confirmed-fail-with-sql-error case. runtime/dr-control.md adds three clarifying lines:exact valid/fresh/nonnegative DB positive inventory independently establishes FAIL, zero without external Provider proof remains NV, and untrusted SQL observations are not trusted merely for containing a positive value. This matches the implemented gate and introduces no new Runtime field or version.

Independent last-test rerun:`go test -race ./internal/drcontrol -run '^TestExternalSafetyRequiresBoundImmutableSources$' -count=1 -json` => exit0,12 passing test events including subtests,0 fail/skip. It covers preservation of external SingleWriter/Fencing/Provider FAIL even when SQL is unavailable. Raw output:pr196-collector-p2-review-external-20261123.jsonl. The complete282-event final affected race is parent-executed submission evidence; no unnecessary whole-suite rerun was performed by reviewer after this assertion-only addition.

Inspected final Evidence AUDIT.md, ssot-source.json, commands.json, assemble.py and generated source-manifest/test-summary:

- Source manifest344 entries independently match current raw bytes/size and LF-canonical hashes; exactly four local source/doc deltas. Prior baseline audit is reused only for unchanged source.
- Independently decompressed/recounted logs agree with every test-summary entry:full Go4655 PASS/88 SKIP,53package PASS/14skip; final affected282 PASS; original expected-red25 FAIL/12PASS; focused79PASS; separate initial compilation package failure. These suites overlap and are not summed.
- AUDIT/commands explicitly distinguish broad Go before the final test-only assertions from final affected race, retain fixture compilation and expected-red failures, explain Windows CRLF format output and canonical658-file format check, and do not claim PostgreSQL/Linux/new remote CI/live testing.
- SSOT records identify LBT-60 comment11266, f5413c9 Draft PR review, Confluence9666637v4 and63275012v14. Parent's external read-only retrieval is not claimed as independently repeated here.
- assemble.py uses local git read operations, logs/format inspection, deterministic gzip and SHA manifests. It has no cloud/service/DB/network or Git mutation action. The generated packet excludes itself from source inventory and its artifact manifest excludes itself, preventing recursive hashes.
- Initial artifact snapshot28 listed files all matched; AUDIT/commands/ssot-source were still being assembled and unlisted at that instant. Parent confirmed the final assembly will include these, final scanner logs and latest reviewer report before regenerating the manifest. The reviewer did not silently accept that intermediate manifest as a complete final packet.
- Decompressed/current packet private-key, real Slack webhook, GitHub token and AWS access-key narrow patterns found no hits. This is not a proof that every possible secret is absent. Parent separately reported source/task-log gitleaks exit0 with empty reports; no credential value was output by reviewer.

Machine-readable independent intermediate artifact/source/log check:pr196-collector-p2-artifact-check-20261123.json. Updated independent source snapshot contains the final four files. No new confirmed source or Evidence semantic defect was found; final manifest regeneration and local hash comparison are mechanical parent checks after report copying, not a new product test requirement.

Final scoped disposition:P2 resolved; zero new confirmed P0/P1/P2; candidate remains local/uncommitted/unpushed, existing Draft and all DR HOLD/FAIL/NV boundaries unchanged. The prior stopped Infra/GitOps task was not resumed.
