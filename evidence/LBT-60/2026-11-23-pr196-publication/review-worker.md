# PR196 Worker/maintenance independent review — 2026-11-23

Reviewer: /root/audit_pr196_worker. Read-only review; no source changes, AWS/Slack/shared DB/Provider calls, commit or push.
Read PRE_MERGE_AUDIT.md, AGENTS.md, CONTRIBUTING.md, TESTING.md and relevant Runtime/HTTP/backend/schema SSOT. Latest Jira/Confluence lookup is delegated to the primary reviewer; this report does not claim an independent external-source refresh.

## Revision/scope

- Repo: ktcloud4-SL/labbit-app; worktree labbit-dr-stage3-offline-20261121.
- base origin/main 753eecbff737409a4c9cc8c542f3c6baa6acf0da; existing PR196 HEAD c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac plus local tracked/untracked additions.
- Reviewed new APPROVED_CONTROL settings/assembly/gate and their tests, Runtime0.1.13 and documentation. Cross-checked the existing base-to-PR implementation of local/durable WorkerControl, Resume/PG migration12-14, Executor/Reconciliation/Router, production app assembly, HTTP maintenance contract and original #194 behavior.
- This reviewer did not review all operator CLI code or test-only installer/OpenStack changes: those are separate primary/other-reviewer scopes. GitOps/Infra local branches are not part of App PR196.

## Findings before fixes

No confirmed behavioral defect found in this scope; no source fix was performed. Operational evidence gaps below remain explicit and are not classified as reproduced code defects.

## Trace and boundary review

| Requirement | Implementation | Tests / actual run | Limit |
| --- | --- | --- | --- |
| Preserve #194 default closed admission/Worker behavior | settings.go:14; control_stack.go:142 default gate.Enter; gate.go:76 | existing maintenance/App/provision race suites PASS | Existing live #194 idle test is not live drain proof |
| Separate HTTP block from opt-in approved Worker processing | settings.go:18 postgres requirement; control_stack.go:143 persistent+configured check; gate.go:96 worker-only valid file | TestApprovedControlAdmissionRequiresCompletePersistentSettings; TestApprovedControlAssemblyNeverFallsBackToUnconfiguredOrLocal; TestControlledWorkerStillRequiresValidFileAndCountsDrain; TestApprovedControlProcessesExistingWorkWhileNewHTTPStaysClosed; all directly run PASS | full backlog control, not per-Operation allowlist; closed alone cannot pause in opt-in |
| No bypass at claim/prepare/send/requeue | Executor/reconciliationClaims + Router share same immutable Control; authoritative Enter at every typed boundary | provision/connector/workercontrol race package suites PASS | Same-process fake/loopback; not real multi-Pod/Provider fencing |
| Old binding not self-upgraded | durable State.Matches, exact revision/epoch/Pod/boot ticket, stale/unverified latch | existing durable/binding regressions plus new opt-in tests PASS | All deploy participants and direct writers must be covered separately |
| Preserve UNKNOWN/INTENT and avoid blind retry | Resumer.Inspect/Resume, PG BeginResume/ApplyResume separate commits, additive migration14 | unit/race suites PASS; integration-tag tests inspected but not rerun by this reviewer | no PG/shared DB used by this reviewer; primary owns actual disposable-PG evidence |
| API auth/replay unchanged | labexecution gate.Enter remains separate; HTTP503 maintenance/replay200/current401/403/409 preserved | app/provision/maintenance unit packages PASS; integration source read | no real browser/API test |
| Whole worker/query lifetime counts | gate.EnterControlledWorker once release, ObserveReconciliation, Operation Worker held admission | gate tests and race PASS | counts are not total Provider or all-Writer state |

Failure/concurrency/security review: local pause remains one-way; persistent Enter response ambiguity latches unverified and does not auto-close potentially committed boundary; Leave uses exact old ticket; CAS pause does not undo local latch; mutation and typed query boundaries remain distinct; expired/invalid signed observations are not proven by this reviewer but owned operator scope; local valid file cannot grant Store authority. DB schema files12-14 remain unchanged from PR HEAD, preserving additive/checksum approach. No startup DDL, new HTTP endpoint, image/replica or workflow change in this assigned incremental scope.

## Direct independent execution

Command (Windows, Go1.27.1, race, count1):

go test -race -count=1 ./internal/server/maintenance ./internal/server/workercontrol ./internal/server/provision ./internal/server/app ./internal/server/connector ./internal/server/connectorwss

All 6 packages PASS, process exit0. Actual output:

- maintenance 3.422s
- workercontrol 3.107s
- provision 3.957s
- app 1.281s
- connector 2.955s
- connectorwss 20.376s

Not run with integration tag. No test-level PASS/SKIP count was collected by this command; package PASS is not a claim of no test-level skips. Logs were returned by tool sessions cf5231/82d7ab/523cff, not a separate raw JSON log file. Primary execution evidence should supply counted JSON logs.

## Reviewed key raw SHA256 snapshot

- internal/server/maintenance/gate.go: 256d5a2d98680020cee95ac466c4deda12cb1c99dae3d0b8d4fe2b4994ff7057
- internal/server/workercontrol/settings.go: 681be46d51f6a87a808375d96a4401f0e3605e1a182dd69925c33c9d06155aa3
- internal/server/app/control_stack.go: 9452083f84beadd362b7173bb35b819f3327f9bb276b880f72309e13b8fa63af
- runtime/contract.yaml: fbc2b70dc2d5fbe262c9f4cb9bd02210bbb4a951ccb975619028879fb2aed42a
- internal/server/maintenance/controlled_worker_test.go: f76c82137460f7a277822a185e081059a671aa918c6b905638f4bd750f492f05
- internal/server/provision/controlled_maintenance_test.go: 28079383021d53bbe21947825966a8f47103199e83b30cc0f6940b1d19c6d981
- internal/server/app/dr_maintenance_integration_test.go: 3858d27d0a06c02e5c6881a1b41f6a516c3d7de97ee548a41ff2a35d7a06ca3c

## Scoped conclusion

No new blocking defect found in the reviewed App Worker/maintenance increment and affected existing PR consumers. This is an independent offline code/unit/race review, not the entire PR final audit or a release approval. Existing ReplicaLag FAIL, four NOT_VERIFIED, execution_gate=HOLD/recovery_authorized=false/cloud_verified=false remain. Actual trusted collectors, authoritative Writer/Fencing, approval/key ownership, entire Pod coverage, shared migration backup/rollback, Provider/DR execution and existing work reconciliation remain separately unverified. Primary must check latest SSOT, remaining diff/evidence, no-deploy push workflow behavior, and exact reviewed hashes before pushing.
