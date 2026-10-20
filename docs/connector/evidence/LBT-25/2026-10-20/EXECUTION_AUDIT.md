# Historical execution audit

The text below records the earlier live execution, before this evidence-only PR. Its no-push/no-PR statements describe that execution. Current publication review is PRE_MERGE_AUDIT.md. Original raw hashes are in original-export.json; the old generated manifest is not the final package manifest.

# C3 verification audit — 2026-10-20

Judgment: **prepared Workspace VM scope C3 / LBT-25 representative integration acceptance criteria satisfied**. This is a verification result, not a new code/PR readiness or deployment decision. No new product implementation, push, PR, merge, deployment or Jira transition was performed in C3.

The user's root `PRE_MERGE_AUDIT.md`, repository `AGENTS.md`, `CONTRIBUTING.md`, `TESTING.md` were read. Latest Jira LBT-25 body/comments and D18v5/D20v7 were directly read in this task; the independent reviewer re-read them too. Requirement → implementation → automated test → actual Evidence mapping is in the README traceability table. HTTP, Connector and runtime contracts apply; DB migration/schema changes and Files/Live consumer changes are not part of this verification-only scope. They are not silently counted as C3 passes.

## Frozen scope and complete change boundary

Repository `ktcloud4-SL/labbit-app`, branch `codex/fix/LBT-22-c2-ktcloud-acceptance`, base9172e050f5660c80b84ba70d52f0c549d8f032d9, HEADd21ee48394eea59d65755a67be18f8bb5a21fbd9. Worktree clean before/after; staged/unstaged/untracked C3 repository files=0. No additional C3 diff is hidden in this C2 branch. Existing C2 full diff was audited before PR137 and is not reclassified as new C3 implementation.

C3 local generated files live outside this repository under `outputs/c3-acceptance-20261020`. Whole inventory/hash manifest: `evidence-manifest.json`. Roles:

- API helpers/matrix: real deployed service authorization, bootstrap and lifecycle tests with in-memory credentials; return only safe metadata. They start/stop a localhost-only forwarding process through the authorized admin SSH host.
- Read-only collectors: Backend enum/UUID lifecycle, whitelisted runtime config, image identity, read-only Nova metadata, temporary forward absence. Protected cloud/bootstrap inputs are read in memory and not copied into output.
- Browser Evidence and safe SaaS screenshots: actual public browser path, application body equality as booleans, separate session IDs, expiry and explicit close. No bootstrap URL fragment or app/Terminal payload stored.
- Go test JSON: current local behavior suites, distinct from actual KT Cloud acceptance.
- Earlier unsuccessful diagnostic files are retained and explained; empty output is not a success. `api-internal-initial.json` is the later502 duplicate, not initial201 proof.

## Findings and dispositions

| ID / severity | Revision / location | Reproduction and impact | Disposition |
| --- | --- | --- | --- |
| E-01 / P2 | C3 helper `read-backend.py:6`, original allowlist | Initial and deliberate failure messages not mapped; collector hid them as unknown, preventing accurate failure accounting. | Reported before modification; fixed by exact enums and safe reason allowlist. Re-collected `backend-final.json`: unknown0, VM_UNREACHABLE and APP_NOT_RUNNING both visible. No raw provider/SSH payload persisted. |
| E-02 / P2 | C3 helper `run-internal-api.py:10`, original fixed output | Re-running negative probe overwrote the initial successful probe file. | Reported before modification. Future probe output is timestamped and exclusive-create. Initial original is irrecoverably absent and explicitly not reconstructed; fresh API matrix201 and Backend metadata remain valid. |
| E-03 / P2 | Initial C3 report `README.md:3` | Broad wording that no VM changed contradicted the disclosed guest app stop/start and auto-restore timer. | Reported by final reviewer before correction. Opening scope now distinguishes no cloud resource or VM configuration changes from the explicit temporary guest service negative test. |
| O-01 / observation | Actual05:40:39Z Connector VM_UNREACHABLE | Initial external UI generic503/504; Backend reports actual VM access failure. Exact edge response and SSH/network root cause unavailable. | Preserved as initial failure, no speculative fix claimed. Subsequent actual integration flows pass. |
| O-02 / observation | Actual05:57:10Z Backend close interval | TUNNEL_CLOSED request event and sameConnector attach rejection occur near explicit close. Rejected attach has no session ID. | Retained and not assigned a speculative session/root cause. Post-close new request correctly401. |

No confirmed product defect was identified within reviewed C3 AC. This does not establish that all production failure modes are absent.

## Failure, security and lifecycle review

- Input/authorization: real role ownership rejects403 and22/3001reject403. Cross-organization/current-membership-invalid fixtures, malformed payload/bounds and READY/generation guards are local automated coverage, not newly mutated live identities/workspaces.
- Session state and retries: actual one-use exchange204 then401; TTL absolute expiry then freshGET401; explicit close204 then freshGET401 then repeat close204. Browser stale POST reload is explicitly excluded from new GET evidence.
- Concurrency/ownership: real independent student/teacher sessions; cross-session cookie rejected; forbidden close leaves owner session active; teacher close leaves student GET200. Local lifecycle suites also cover duplicate/concurrent/cancel/late-result paths.
- Partial failure: actual app-not-running returns exact502 error; application restored and fresh external session passes afterward. Initial VM_UNREACHABLE remains a recorded transient observation with unconfirmed root cause.
- Cleanup: 5 known created IDs exactly equal5 closed IDs. Browser/API post-close and post-expiry authentication invalidation observed. Temporary local forwarding process absent. Direct Connector registry and OS activeTCP0 are **not measured**; API/logical closure does not substitute for that physical measurement. No cloud create/delete or security-policy mutation occurred in C3. Existing user-owned VMs/resources remain for future tests.
- Secrets: user credentials and session/bootstrap cookies handled only in RAM/transient environment. All persisted real-system results are times, IDs, safe enums/statuses, booleans and selected config/images. Contract-prohibited Terminal/app payload and provider raw errors are excluded from Evidence.
- Restart and durable resource reconciliation: not live fault-injected; C3 sessions are documented ephemeral. No existing FAILED execution/operation state was rewritten. The C2 historical state and future G3/G4 scope remain distinct.

## Executed, reused and unexecuted validation

- Executed this run: real public Browser external bootstrap/GET/POST/fresh GET/5mTTL/explicit close; real deployed Backend+Connector+VM API matrix via localhost upstream; owned app-stop negative and recovery; read-only runtime/Nova/lifecycle/postcondition collection.
- Executed local suites: seven packages,964PASS including subtests,0FAIL,0SKIP, sourceHEADd21ee48. Scope exact command/log is in README. In-process/fake and local-loopback coverage remains distinguished from KT Cloud execution.
- Source correspondence: runtime Backend e0caea4 and local testedHEAD match specified Preview service/Gateway/handler, Connector Preview, HTTP/Connector contracts and runtime boundaries. Actual Web9172e05 matches local Preview/API source. Actual Connector binary identity is carried from preceding C2 source manifest, not freshly measured from its OS this run.
- Earlier C2 full repository checks are reused only as prior C2 context. Full build/lint/vet and Web suite were not rerun for this verification-only C3 run. No product source change requires new compile/build acceptance here.
- Not newly executed: tagged PostgreSQL integration, actual cloud provision/reset/cleanup, process restart fault injection, exhaustive load/performance and direct Connector socket/registry inspection. These are not claimed as actual live passes.

## Independent second review

Reviewer `/root/c2_c3_audit` independently compared current requirements/contracts and stored Evidence/harness; did not rerun the live tests. It confirmed API matrix metadata, Browser expiry/close records, known session ID set equality and Go964/7-package pass counts, and source boundary diff0. It raised E-01/E-02, verified collector/output repairs and accepted original-loss limitation. It required avoiding inference of rejected attach session identity and OS activeTCP0; this report follows those limits.

Final reviewer judgment: prepared VM C3 representative integration AC satisfied, no remaining blocking finding after E-03 wording correction. Reviewer also directly checked all43 existing manifest size/hash entries; manifest was regenerated after this final wording change. The root cause of the initial transient failure remains open as an operational observation; the acceptance result is not a claim that this root cause was fixed.

The user authorized C3 checks. Prior C2 push/PR instruction is not extended to a C3 push/PR or deployment. All new reports remain local and C2 PR137 is unchanged.
