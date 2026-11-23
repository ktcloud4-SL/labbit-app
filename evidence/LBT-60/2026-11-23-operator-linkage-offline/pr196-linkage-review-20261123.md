# PR196 live-linkage independent incremental review — 2026-11-23

## Scope and limits

Reviewer is independent of implementation. HEAD 94fe1f61d7e398761291a967fab9820bfb08bc3d; parent worktree codex/feat/LBT-60-stage3-live-prep-20261123 has uncommitted/untracked linkage implementation under active development. Existing readiness documents/Evidence are preserved. This is an early code-correctness review, not a completed pre-merge audit or PR/operational approval.

PRE_MERGE_AUDIT.md has been read. Relevant source and owning runtime/dr-control.md, runtime listener contract, DB migrations 12–15, existing Resume producer/consumer were inspected. Latest LBT-205/60 and Confluence 9666637 v4 are parent-supplied context; this reviewer did not independently fetch the service. Runtime documentation is still being updated by implementation owner.

Reviewed source paths and SHA256 snapshot: pr196-linkage-review-source-20261123.json. Paths include restriction contract/store/CLI, protected signing, Collector/SQL/Kubernetes adapters, readback endpoint and assembly, shared WorkerControl helpers and Resume SQL change, migration 15, and new linkage tests. Existing full PR baseline audit remains a separate report. No product source was edited by reviewer. No AWS, cluster, shared DB, Slack, key, deployment, push or merge operation was performed.

## Findings before fixes

| ID | Severity and exact source | Reproduction / evidence | Impact and necessary regression |
| --- | --- | --- | --- |
| LINK-01 | P1 / internal/drcontrol/collector_adapters.go:35 | KubeCLI sets server/context/config but does not override or validate insecure-skip-tls-verify or tls-server-name in the selected protected kubeconfig. A legitimately owner-protected kubeconfig containing insecure-skip-tls-verify:true is accepted by CLI ReadProtected, and kubectl honors it. A network intermediary can then return the known kube-system UID, Pod UID/images and forged process readback. Source analysis, no live-network attack attempted. | ACL protection is not authenticated HTTPS. Pinned URL and non-secret UID cannot prove server identity without TLS. Scope/closed collection can be falsified and signed as LIVE_READBACK if other trusted safety sources are valid. Enforce TLS verification and exact API hostname at command boundary, or fail-closed validate effective selected cluster TLS/CA settings. Add tests covering insecure configuration, TLS-name override and correct fixed arguments; never require a real cluster to exercise rejection. |
| LINK-02 | P2 / internal/drcontrol/collector.go:232 | Fresh valid scope-bound signed SafetyAttestation has SingleWriter=FAIL and Fencing=FAIL; raw References retrieve and hash correctly; SQL reader times out. The `ok && validWork` condition prevents both known external FAIL results from being copied and returns NOT_VERIFIED for both. Independently reproduced using a Go overlay test without modifying product source. | Confirmed negative safety evidence is weakened into absence of verification due to failure in an independent source. Preserve independently verified FAIL results. Keep positive state promotion constrained by all required checks; WorkReconciled may remain dependent on SQL inventory. Regression must combine verified negative external evidence with SQL unavailable/wrong-target/invalid inventory, plus invalid external signature/source cases that must remain NOT_VERIFIED. |

The findings above are code findings, not claims that a real Writer was unfenced or that a DR event occurred. Both paths continue to emit global HOLD; that does not eliminate incorrect collection/authentication behavior.

## Independent test execution

Executed locally on Windows, cloud-free, without integration tag:

`go test ./internal/drcontrol ./internal/server/workercontrol ./cmd/labbit-dr-control ./cmd/labbit-dr-evidence ./internal/server/app -count=1`

Result: all packages with tests PASS. cmd/labbit-dr-evidence currently reported no test files at this execution; parent is still adding tests. No race or disposable PostgreSQL integration suite executed by this reviewer in this increment.

Independent negative regression probe:

`go test -overlay C:/Users/user/.codex/tmp/pr196-linkage-review-overlay-20261123.json ./internal/drcontrol -run TestIndependentReview -count=1 -v`

Result before fixes: FAIL, as expected for the confirmed defect. Output:

`verified external FAIL was downgraded: SingleWriter=NOT_VERIFIED Fencing=NOT_VERIFIED Reasons=[EXTERNAL_SAFETY_NOT_VERIFIED WORK_INVENTORY_NOT_VERIFIED]`

Probe source and overlay are stored only under .codex/tmp; no test or production file in worktree was modified. The probe uses the existing deterministic mock fixture and signed synthetic attestation, not actual cloud evidence.

## Confirmed correct scoped behavior in reviewed source

- Restriction approval has distinct restriction-approval signature domain and exact target, command request SHA, UUID approval ID, Principal, TTL and action validation.
- Restrictor authenticates before historical lookup; historical inspect uses receipt times and does not need a now-valid old approval. Existing INTENT is returned as UNKNOWN, never automatically reapplied.
- Added restriction journal retains full immutable payload/proof; INTENT is committed before apply, and state mutation plus APPLIED receipt share the same transaction. Initial creation is create-only paused; existing rows are not reset. Existing Pause helper is shared without an external second mutation transaction.
- Resume SQL includes pending restriction INTENT in begin/apply guard. Existing v1 Resume wire shape and signature domains remain unchanged; optional Policy field and extra explicit actions are additive.
- Runtime readback obtains actual in-process immutable Binding/BootID, rather than inventing it from Pod metadata. The admin listener route is read-only and only returns durable control bindings. It has no new public API route. Effective RBAC/network protection remains an operational prerequisite.
- Collector enumerates namespace Pods, requires pinned nonempty expected coverage and exclusion UIDs, validates selected image identity/readiness, and compares before/after resource versions/restarts. Empty inventory cannot establish coverage. Missing binding/counters/source yields NOT_VERIFIED.
- SQL inventory is read-only repeatable-read and spans all operations, items, open boundaries and intent journals in selected DB. The exact DB identity is checked. This cannot alone prove provider reconciliation, fencing, Single Writer or absence of replica-lost tasks.
- External safety evidence is independently signed, time/scope/Plan-bound and references retrievable fixed-version raw artifacts with SHA. No attestation yields NOT_VERIFIED for Fencing/SingleWriter/WorkReconciled. These are trusted issuer assertions, not new physical fencing implementation.
- Signing checks protected local PKCS8 Ed25519 key, matching Policy public key and purpose/phase; Windows DACL and Unix ownership/permissions are handled separately. Private key bytes are not returned in CLI results. New mutation/signing/collection CLIs default to preview and require dual explicit execution opt-in.

## Remaining review / acceptance limits

- Product changes are still under active development. Source may differ from this report's snapshot; finding fixes require focused re-review and regression run.
- New runtime/admin route contract, operator commands, Policy field/action contract and migration rollout/backups require owning documentation update and full final audit. That is known unfinished work, not an additional confirmed code defect in this early pass.
- Kubernetes TLS behavior, real Pod proxy/RBAC, reader role permissions, protected key provisioning/rotation, actual SQL integration, live Pause/Resume, full Pod coverage, Provider and Fencing/Single Writer remain unverified by this reviewer. Mock PASS must not be presented as those operational acceptances.
- Collection/signer produces local documents and references; actual immutable storage, approval decision, clock trust and deployment/change freeze remain separate operational gates.
- Existing ReplicaLag FAIL, four NOT_VERIFIED and global HOLD must remain unchanged. No release, merge, push or real execution is approved by this review.

## Re-review after LINK-01 / LINK-02 fixes

### Verification and resolution

- LINK-01 resolved in code review: CollectionPlan adds fixed CAFile/CASHA256; execute CLI validates protected CA bytes/hash; KubeCLI commands explicitly set insecure-skip-tls-verify=false, exact API hostname and fixed CA path. Command-boundary assertions cover the flags. Actual TLS/cluster invocation was not performed by reviewer.
- LINK-02 resolved and independently re-tested: signed external FAIL is preserved when SQL inventory is unavailable. The previously failing independent overlay probe now PASSes.
- Pending restriction count now participates in Report/Drained as well as Resume Begin/Apply. Producer/consumer struct and PostgreSQL scan order match. Existing migration SQL 1–14 is unchanged; migration15 explicitly requires non-null applied_at for APPLIED. Nil approval UUID is rejected.
- Owning runtime/contract.yaml v0.1.14, runtime/dr-control.md and runtime README now document restriction/collector/signing as an opt-in undeployed candidate, separate from physical Fencing and global DR acceptance. DB migration and deployment documents include migration15, backups/compatibility and live verification prerequisites.

### New finding before any fix: LINK-03 / P2

Location: internal/drcontrol/collector.go:194–200 and :262–265 in reviewed working copy.

Required distinction: missing/unobservable evidence is NOT_VERIFIED; a verified unmet condition is FAIL; neither may permit activation.

1. A scoped process readback lacks one required counter (e.g. Active[reconciliation]) while Pod inventory/readback binding are otherwise valid and all observed counts are zero. The loop sets closed=false but leaves coverage=true. The final branch unconditionally sets AdmissionBlocked=FAIL. This claims a negative condition where the required observation is missing. Independently reproduced by overlay test; output: `missing counter classified FAIL instead of NOT_VERIFIED`.
2. With zero unresolved SQL inventory and a valid external attestation whose ProviderReconciled is NOT_VERIFIED/missing/invalid, the final else sets WorkReconciled=FAIL rather than preserving absence of verified reconciliation. Source analysis. A nonzero unresolved inventory or verified Provider FAIL should still be FAIL; all zero with verified Provider PASS can be PASS; otherwise NOT_VERIFIED.

Impact is incorrect status/Evidence categorization, not a global gate bypass (HOLD remains). Regression should assert exact statuses for missing, negative, malformed and nonzero local counters; external Provider NOT_VERIFIED/missing/invalid vs PASS/FAIL; and confirmed negative evidence with an independent unavailable source.

### Incremental requirement traceability

| Requirement / owner | Implementation / consumer | Automated verification reviewed/executed | Live boundary |
| --- | --- | --- | --- |
| Exact authenticated restrictive action, purpose/TTL/target binding; runtime/dr-control v0.1.14 | RestrictInvocation, restriction verifier, workercontrol RestrictRequest, CLI action dispatch, PG Begin/Apply | unit/mock positive/negative signature, actor/action/phase/time/target, duplicate and UNKNOWN paths executed | Actual STS/authenticated mutation not executed |
| Durable INTENT / atomic mutation+receipt / restart inspect | migration15, worker_control_restrict, shared pause/init tx helpers; Resume pending guards | Mock replay/UNKNOWN/expiry executed; disposable PG tests read but not independently run | Shared DB migration/permissions/commit-loss behavior not tested |
| Protected purpose key / Policy public-key linkage | protected platform readers, SignApproval/SignDocument, CLI approve/collect, existing Verifier | Windows DACL, signing roundtrip, key/purpose/phase and consumer verification unit/mock executed | Real key provisioning/rotation, ACL deployment and artifact storage not tested |
| Read-only real adapter paths and scope completeness | KubeCLI fixed raw GET, process admin readback actual BootID, SQLWorkReader | Pagination/cluster/binding/coverage/closed/work/error mock tests executed; admin handler httptest executed | No real Kubernetes, SQL or Provider call; external Fencing remains trusted attestation |
| Preserve FAIL/NV/HOLD distinctions | Collector -> signer -> immutable Invocation -> existing verifier -> Resumer | Existing negative tests pass; LINK-02 independent probe now passes; LINK-03 probe currently fails | Full DR FAIL/4NOT_VERIFIED/HOLD unchanged; no live GO |
| Safe default CLI modes | two CLI producers + mutation consumer | Six-package unit/mock run includes evidence approve/collect preview and disabled mode checks | No external calls, local key read or DB mutation by reviewer |

The existing base-to-94fe1f6 PR audit/CI is reused only for unchanged baseline code. New local changes and affected consumers above were inspected directly. This does not claim old baseline CI validates these new uncommitted source changes.

### Tests and original bytes

Executed cloud-free: `go test ./internal/drcontrol ./internal/server/workercontrol ./internal/postgres ./internal/server/app ./cmd/labbit-dr-control ./cmd/labbit-dr-evidence -count=1 -json` => exit0, all six packages PASS. Integration-tag tests, race, Linux, real AWS/Kubernetes/shared DB and end-to-end orchestration were not executed by reviewer for this increment. CLI producers are tested in preview/disabled modes and signing/collection ports in mock fixtures; full execute CLI against real dependency chain remains unverified.

Independent overlay run: former LINK-02 probe PASS; new LINK-03 counter probe FAIL before fix. Only .codex/tmp artifacts were written by reviewer, not source files.

Readiness artifact-manifest: 26/26 files matched original size and SHA256; no mismatches. This independently verifies existing local artifact bytes, not the truth or freshness of old live observations. Hash-check result: pr196-linkage-readiness-hashcheck-20261123.json. Full local changed/untracked source snapshot: pr196-linkage-review-source-final-20261123.json (63 files: product/docs36, existing readiness27). Old migrations1–14 show no HEAD diff.

Residual non-blocking doc clarity: the original deployment table/paragraph says the CLI does not implement init/Pause while the appended new local-implementation section now does. It is clear that old readiness is historical in the added heading, but labeling that earlier claim as the historical 11/23 baseline or updating the current checklist would avoid operator confusion.

Current incremental code-review outcome: original LINK-01/02 fixed; LINK-03 classification correction pending. No additional confirmed P0/P1 found in inspected source. This is still not operational DR acceptance, external execution authorization or push approval.

## Final focused re-review after LINK-03 fix

LINK-03 is resolved in reviewed source. Maintenance Gate.Readback now observes the exact open/closed/NOT_VERIFIED file state and complete local count map under one mutex. Admin producer returns that typed MaintenanceState with the same count snapshot. Collector accepts only exact closed plus complete nonnegative zero counts for AdmissionBlocked PASS; missing/negative/inconsistent or invalid-file observations remain NOT_VERIFIED, while verified open or positive in-flight/count observations retain FAIL. ProviderReconciled missing/NOT_VERIFIED/invalid remains WorkReconciled NOT_VERIFIED when SQL inventory is otherwise zero; verified nonzero inventory or Provider FAIL remains FAIL. This is an observation/classification correction and does not open Worker or maintenance.

Re-read affected producer, DTO, Collector, local maintenance consumer/tests and external safety tests. New tests explicitly assert exact classification for missing count, negative count, invalid file, inconsistent closed flag, known open, positive recognized/unrecognized count and in-flight combinations, plus Provider NV/missing/invalid and confirmed external FAIL with SQL failure. Existing v1 Resume validation/purpose and restriction auth/INTENT/CAS consumers remain unchanged by this last fix. Deployment document now explicitly labels the previous init/Pause absence as the initial 11/23 observation.

Independent execution after fix:

- `go test ./internal/drcontrol ./internal/server/workercontrol ./internal/server/maintenance ./internal/postgres ./internal/server/app ./cmd/labbit-dr-control ./cmd/labbit-dr-evidence -count=1 -json`: exit0, seven package PASS; 459 passing test events including subtests, zero failed test events, zero skipped test events. Raw output: pr196-linkage-review-tests-final-20261123.jsonl. This is Windows unit/mock coverage without integration tag, not 459 live checks.
- Existing independent overlay probes both PASS: missing local counter correctly returns NOT_VERIFIED; verified external FAIL survives unavailable SQL. Overlay is outside product source and does not add files to repository.
- Local modified/untracked inventory is now 64 files. Raw SHA/size inventory taken before test and after test is identical (zero source changes during testing). Current inventory: pr196-linkage-review-source-final-20261123.json. Stability summary: pr196-linkage-review-stability-final-20261123.json.
- Existing readiness artifact manifest independently matches 26/26 bytes/size/SHA entries. Historical live evidence has not been rewritten or promoted to current new-code verification. This hash result establishes local byte preservation only.

Finding disposition: LINK-01 resolved; LINK-02 resolved with independent regression; LINK-03 resolved with independent regression. No additional confirmed P0/P1 or P2 issue was found in this scoped final increment. Current code-review conclusion is no unresolved confirmed defect in the reviewed linkage increment. This does not replace the parent's full pre-merge audit, fresh owning service SSOT check, broader CI or real acceptance.

Unchanged limits: reviewer made no actual AWS/Kubernetes/shared DB/Slack/external calls and no code changes, push, migration, deployment or key-operation change. No independently executed PostgreSQL integration, Linux/race or full execute CLI integration is claimed. Scope-external Pod/Job paths, change freeze, actual operational approval/key/Policy installation, physical Fencing/Single Writer, Provider reconciliation and actual Pause/Resume remain operationally unverified. Global ReplicaLag FAIL, four NOT_VERIFIED and DR HOLD remain intact. Prior base-to-94fe1f6 audit/CI supports unchanged baseline only; the new local files and affected consumers above were reviewed and unit/mock tested separately.

## Final test-only producer/consumer increment

Compared current product/document source SHA inventory to the prior reviewed snapshot: the only changed non-Evidence file is internal/drcontrol/linkage_test.go (SHA256 ca364252e9156aa5fc4176feabd28e2614bc3c91b24802fc3943d20013093cfc). All product implementation sources are byte-identical to the preceding reviewed pass.

The init and pause branches now both call production SignApproval with a protected synthetic PKCS8 key, update the fixed-reference size/SHA and execute ExecuteRestriction through the existing synthetic artifact/store ports. Existing APPLIED once, historical replay, payload conflict and UNKNOWN assertions remain. This exercises the real signer-to-restriction-verifier purpose/payload producer-consumer roundtrip without any external dependency.

Independent focused rerun `go test ./internal/drcontrol -run '^TestRestrictionSignedInitPauseHistoricalAndUnknown$' -count=1 -v` => PASS, including init and pause. No new issue found in the test-only increment. Prior 459-event unit/mock result is still attributed to the preceding snapshot; only the focused test was independently re-run after this test change. Parent's final Windows race/Linux no-NIC reruns were reported in progress and are not claimed as reviewer executions.

Source inventory updated in pr196-linkage-review-source-final-20261123.json (64 files, generated operator-linkage-offline audit packet excluded to avoid recursive inventory). Test-only comparison summary: pr196-linkage-review-testonly-final-20261123.json. No code, actual AWS/DB/service, key-operation, push or merge action was performed by reviewer.
