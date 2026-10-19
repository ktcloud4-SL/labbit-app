# LBT-174 independent second-pass review (intermediate)

Base/HEAD `4691c0ae8a43955549ae2b9e8bc4f5a464c30001`, branch codex/feat/LBT-174-preview-local-integration, uncommitted source. snapshot-manifest.json freezes the reviewed tracked/untracked files. source-alignment.json independently matches all 489 LF-normalized author final-manifest paths (aggregate 96ec0a79b149ac980d55dd8434a639f38ffa4d5d7f4dd8b480c5d3304db87e44). Additional zz_independent fixtures are reviewer-only files in this external snapshot. Production checkout/evidence/cloud were not mutated.

## Findings and status

- F174-R001 through R005: initial base findings cover stale attempt binding, pending-open resurrection, Close blocked behind a writer, ATTACHED cancellation, and Control credential/epoch borrowing. Their first-pass fixes and lifecycle/owner regressions were reviewed.
- F174-R006 High: fixed. Owner epoch verification, owner cancellation and registry snapshot retirement share Handler authority lock; OPEN reservation and CLOSE retirement recheck the epoch under that lock. Network I/O and callbacks run after authority lock release. The same post-unlock 20 ms scheduling-fault probe which failed 3/3 before the fix passes 10/10 now (old-retirement-scheduled-retest.log).
- F174-R007 Medium: fixed. A raw TCP socket has a dialCtx cancellation hook until successful Upgrade hands ownership to the exact tunnel; the hook is then detached. Actual HTTP peer withholds Upgrade response, context cancels, and the original probe returns promptly in all 10 repetitions (upgrade-cancel-retest.log).
- **F174-R008 Medium: open, reported before any fix.** handler.go:80-97 SetSender cancels old owner context but does not retire an idle old logical session. After actual Mock Gateway ATTACH, normal Data1000 close, direct nonnil-old -> nonnil-new Control replacement, and late old-epoch4002, the registry retains count1/destroyedfalse (idle-retirement-probe.log,3/3 FAIL). Section7b requires logical trust retirement on explicit replacement, and SaaS may omit PREVIEW_CLOSE. This is retained obsolete logical state, not observed unauthorized data access. A narrow active Control replacement retirement must preserve nil-mediated ordinary network reconnect behavior. Regression must cover both.

## Source and cross-file checks

All modified and untracked implementation/test files were read: App wiring/lifecycle wrapper/shutdown and lifecycle test; Preview client/manager/session with lifecycle and Upgrade-cancel tests; Handler dispatcher/Preview implementation/owner tests and existing sequential handler test; external-package OpenStack Preview integration and its test-only exports. Seven tracked functional files initially became eight with the App lifecycle regression; preview_test.go status alone is inherited line-ending churn without a functional diff. No dependency/schema/runtime/DB migration change is part of this feature at this point.

Lock order is Handler authority -> manager registry -> session -> tunnel during short state changes. Session/tunnel callbacks release their state locks before manager callback. Retirement detaches registry entries before closing sockets or invoking callbacks. Old callbacks compare exact session pointer before deleting; stale attempt cleanup is bound to messageID and pointer. A per-Handler64 pending-open worker limit remains bounded while attached sessions release worker slots. This is an internal resource bound and does not replace Backend exact-port approval. Forwarder/VMAccess ownership, management NIC and host pin enforcement remain unchanged from merged LBT173.

Latest main 0051d8d6803fd447f7b690dd895a0a928c3c7302 was independently read: 7 files in Backend operation worker/postgres/repository/docs; no Connector Preview, wire, runtime or dependency overlap. Fast-forward changes require new final identity and full verification. The local fixture directly drives Connector Control via Mock SaaS and does not assert Backend durable-worker end-to-end behavior.

## Requirement traceability

Sources: Jira LBT174 updated2026-10-16T09:23:57.168+0900, no comments, D18v5/decision-completionv11/teststrategyv3, owning contracts/connector/README.md section7b with preview-control/data schemas, runtime Preview lifecycle/settings, repository AGENTS/CONTRIBUTING/TESTING, user PRE_MERGE_AUDIT. Initial read metadata is ../lbt174-initial-review-4691c0a/ssot-read.json.

| Requirement | Implementation / automatic check | Actual verification / limit | Status at this snapshot |
|---|---|---|---|
| Actual App Control OPEN -> SSH TCP preopen -> outbound WSS -> exact ATTACH/ATTACHED -> HTTP bytes | app.BuildConnector, handler_preview.go, preview manager/client/session; SSHForwarder/VMAccess unchanged | Author local-openstack-final source96ec, commandexit0,233.094s;test222.86s PASS | Satisfied for local component path |
| Session/lab/gen/VM/server/port/attempt correlation, same Control credential | Captured owner/credential/sender; exact tunnel binding; lifecycle stale-attach and owner tests | Mock/loopback fault tests; actual Gateway exact fields and wrong server safe error | Satisfied; full SaaS/Browser C3 excluded |
| 1WSS=1TCP, active <=1/session, sequential and separate sessions | Manager synchronous reservation, attempt pointers, single reader/writer pumps; duplicate/isolation tests | Actual HTTP twice sequential; two sessions, CLOSE one and HTTP sibling | Satisfied |
| Close/error/cancel/backpressure/stale callbacks | Exact attempt callbacks, bounded WriteControl followed socket Close, raw Upgrade ownership; original fixture regressions | Independent Preview/WSS/App -race -count3 PASS; R006 and R007 external probes10PASS each | R008 idle direct replacement still open |
| Reset/Cleanup/old generation fencing and shutdown | accessLifecycleProvider preview InvalidateBefore; Manager Shutdown; App lifecycle16branches includes pending count | Actual gen1 active WSS retired, gen2 HTTP, Cleanup logical count0, stale OPEN denied | Satisfied for source96ec; final source pending |
| Safe APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE/UNAVAILABLE; no bodies/secrets Control | Fixed Handler safe errors, binary forwarding, JSON bounded read | Actual three target error classes and forbidden Control content assertions; timeout/cancel loopback | Satisfied for checked branches |
| Resource cleanup | Integration tracks Provider result resources, inherited exact GET absence helper, separate keypair/management SG helpers | Source96ec exact tracked Lab absence entries28, duplicate-inclusive assertion entries; auxiliary keypair/SG DeleteACK only | Local lifecycle PASS, no unique28-resource claim; final inventory pending |
| Related CI/Terminal/File regression | Independent3package race count3 PASS; author9package race PASS and Web4checks PASS at source96ec | First author fullGo failed two existing timing tests; serial/global rerun pending | Required final run classification still pending |

## Baseline inherited failures

Independent base archive tests in ../lbt174-baseline-probes-4691c0a: VMAccess timing20/20PASS; realtime trace19/20PASS+1same `control log is not JSON` failure. This naturally reproduces the inherited trace symptom. Empty log is parsed as JSON instead of allowing eventually to wait; Low follow-up. VMAccess first PTY setup has40ms whole-setup bound and feature/base files match, but original failure cause remains unconfirmed. Original failures and repeats must stay visible; do not relabel the initial full run as PASS.

## Decision / limits

**Intermediate verdict: supplementation required**, due confirmed F174-R008 and pending post-fix source/full-run/live evidence alignment. Final PR/overall Jira completion not determined here. No KT Cloud, real SaaS/Browser/C3 or cloud mutation by reviewer. Initial first-pass failed local run must remain visible separately from successful source96ec run. Audit does not authorize remote push/PR/merge.
