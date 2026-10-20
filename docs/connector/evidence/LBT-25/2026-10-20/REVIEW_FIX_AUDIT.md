# PR139 P2 Credential-transfer correction — 2026-10-20

Verdict: **P2 correction ready for re-review; no remaining blocking finding in reviewed scope**. This follow-up supersedes the initial publication audit for the private collector. Historical C3 execution evidence and its limitations remain intact. No real SSH/API/VM/browser rerun was performed or required by this review request.

## Frozen scope / requirements

Repository ktcloud4-SL/labbit-app, branch codex/test/LBT-25-c3-acceptance. Original PR base b5090bbe981d5b18b8c503851dbbd7567002153a; pre-correction PR HEAD a1821a2cc6118c6b1eb3508a3f426f792de8fb71. Final fetch found target main advanced to dcc148a0640fa92d286d5504f1446d2c5c105e7a (#140/#141). The topic branch retains its existing history; no force-push/rebase is needed for this correction. The final corrected commit is identified in PR139/Git, avoiding a self-reference in its own contents.

Latest-target impact: b5090bb→dcc148a changes six Web files only (login/logout Terminal resume handling, shared HTTP client JSON Content-Type validation and their tests). Preview Go/service/handler, Connector preview/vmaccess/wss/app, contracts/runtime and repository instructions have diff0. The shared HTTP client can affect Preview API consumers; correct JSON/204 responses remain supported (existing Preview HTTP tests assert application/json). These separate Web changes are not tested by the old C3 Browser evidence or by the new Python regressions. Current PR CI after push provides the separate latest-target integration check; this correction does not change or claim to revalidate those product behaviors live.

Latest [review5582498019](https://github.com/ktcloud4-SL/labbit-app/pull/139#pullrequestreview-5582498019) and [inline finding](https://github.com/ktcloud4-SL/labbit-app/pull/139#discussion_r4322898659) directly read. Required: select only needed fields, establish recipient authorization/SSH host identity or restrict Cloud authentication/GETs to local execution. SSH address hardcoding is explicitly nonblocking. No Secret Manager or new live test is required. User authorizes correction, regression/audit, push and re-review request; no merge/deploy.

Latest [LBT25](https://samsunglions.atlassian.net/browse/LBT-25) body/comment10223 refreshed, unchanged (updated16:23:27+0900, 진행 중). Primary and independent reviewer directly read [D05v1](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6553953), version time2026-09-13T04:54:13.231Z: OpenStack Credentials remain within the customer environment, never central SaaS payload/storage/errors/logs. Repository AGENTS/CONTRIBUTING/TESTING and user's root audit instructions re-read. Runtime README Connector Credential section and Connector contract Preview/security boundary checked. Other HTTP/Preview/realtime/DB contracts are unchanged; initial full C3 traceability remains in README/PRE_MERGE_AUDIT. No new consumer/producer/runtime product field or contract is introduced.

## Finding reported before repair

| ID / severity | Revision / location | Reproduction / impact / SSOT | Repair and regression |
| --- | --- | --- | --- |
| F-C3-PRIVATE / P2 | a1821a2 run-private-collector.py:8–10 | Normal helper call serializes whole auth/ledger to management SSH stdin although consumer needs only4auth fields and ledger.projectId. Recipient Cloud Credential authorization is unproven; SSH access/host identity alone does not authorize that handling. Review does not assert an actual Git/log disclosure. D05 and review5582498019 apply. | Select only required fields, remove SSH/remote mode entirely, fixed bundled consumer on credential-holding operator PC. Eight fake-input regressions check field minimization, local child/SSH0, project fence, consumer restriction and safe failures. |

The finding was accepted and explained to the user before edits. Earlier primary/independent audits missed this gap; their initial pass is preserved as history, not retrospective proof of recipient approval. No new approval claim about the management host is made.

## Whole-PR and correction diff review

The complete PR remains within docs/connector/evidence/LBT-25/2026-10-20. package-manifest.json inventories every final file except itself. Initial52-file review and original43-entry/hash/31-export chain remain identified in the prior audit; this pass rechecks the full final file-set/hashes and confirms historical real-output files did not change. No product source/config/contract/schema/dependency file changed.

| Changed file / class | Purpose / boundary / reviewed behavior |
| --- | --- |
| run-private-collector.py | Allowlisted private input; same-machine Python child/stdin; no SSH; fixed bundled reader; safe local-input/error handling; reserve fresh output before any protected read |
| read-workspace-metadata.py | Uses selected scalar projectId; same project fence, identity POST and2 resource GETs/output shape retained |
| test-private-collector.py | Eight offline behavior regressions using fake local protected-file layouts and fake HTTP; no real credentials/network |
| README.md | Local means credential-holding operator workstation, not local OpenStack/infra server; unknown prior recipient approval; no remote fallback; separate SSH known-host boundary |
| PRE_MERGE_AUDIT.md, REVIEW_FIX_AUDIT.md | Earlier missed finding, current evidence/limits/traceability and independent disposition |
| validation/private-collector-tests.txt, validation/private-collector-summary.json | Newly executed regression results; distinct from earlier Go/Web/live evidence |
| package-manifest.json, validation/helper-checks.json | Regenerated full integrity/guard records after publication changes |

No changed file is excluded. Protected originals, ignored dependencies and fixtures retain their prior role; no protected input is copied to the repository. The current reproduction wrapper/consumer intentionally differ from original-export hashes; final hashes are in package-manifest.

## Traceability / failure and security review

| Requirement / source | Implementation | Executed automatic behavior | Actual boundary / limitation |
| --- | --- | --- | --- |
| Minimum data; inlineP2 | run-private-collector.py minimal_inputs/read_private; reader project fence | test_only_required_fields_and_default_domain; test_protected_files_are_filtered_before_local_child; test_missing_required_field_fails_closed |4auth fields plus scalar ledger.projectId only, tested fake data; no new real auth |
| No unapproved remote recipient; review/D05 | collect uses sys.executable -B - with stdin, fixed consumer; no SSH mode | test_local_auth_and_read_only_inventory_with_safe_metadata asserts exact local child command and no extra env/argv secret; excludes auth/ledger sentinels | Operator PC already holds protected source; this is a CLI reproduction tool, not a new SaaS runtime Credential path. Direct KT Cloud access required or stop |
| Project ownership / no arbitrary consumer | main and read-workspace-metadata.py | test_wrong_project_prevents_resource_queries; test_unsupported_consumer_rejected_before_protected_read | Wrong project authenticates to fake identity then makes0resource GET; unsupported script reads0protected input |
| No error/output secret; D05/AGENTS | fixed error type/status; selected metadata; no raw stderr | test_http_failure_suppresses_credential_error_text; test_local_input_failure_does_not_persist_source_excerpt | Fake HTTP401/YAML-style local error checked, not an actual hostile provider fuzz campaign |
| Preserve evidence/output ownership; user audit | reserve_output unchanged; offline verifier/guard suite | check-publication-tools and verify-package pass; original fixture hashes and full manifest checked | Fresh outputs only; no actual session/resource mutation or cleanup performed here |
| Host identity vs Credential permission; review | README private collector boundary | Source review plus local-child command assertion | No SSH host/key applies to corrected collector. Other SSH probes require independently verified known_hosts and strict client config; no inferred permission to send Cloud Credentials |

Input/error boundaries: missing required fields fail closed; default user_domain_id preserves previous semantics. Arbitrary script paths reject before protected input is read. Outer parser/subprocess errors output only exception type; child HTTP errors output only type/integer status. No retry or remote fallback is added. Authentication password is necessarily sent to the existing KT Cloud identity HTTPS endpoint, token only to the two existing Cloud resource GETs; TLS verification/endpoints remain unchanged.

Process/resource ownership: child timeout90s and subprocess.run cleanup bound the local process; source travels only over stdin, not command arguments/environment/files. Existing C3 output-directory exclusivity and no-bytecode invocation remain. Product Preview concurrency/restart/cloud lifecycle are unaffected and not newly fault-injected. Full ledger exists only in the local reader while selecting projectId; it is neither passed to the child nor emitted. No Cloud Credential transfer to an SSH host remains.

## Tests, actual evidence and remaining limits

- Newly executed: eight Python offline unittest cases,0fail/0skip; fake input files, child boundary and HTTP only. AST/offline integrity/full guard regressions executed. These do not prove a fresh KT Cloud session or deployment.
- Historical C3 Browser/API39/sequential/expiry/close/app-negative and5session cleanup remain unchanged, as do their unknown initial VM_UNREACHABLE, uncorrelated attach, lost initial probe and OS socket-measurement gaps.
- Previous a1821a2 CI Go/Web/Contracts passed; that is not claimed as corrected-HEAD CI. No product Go/Web source change justifies repeating their broad local suites; corrected-HEAD CI is checked after push. New Python behavior tests are explicitly run locally, not represented as an existing automatic CI gate.
- Local here means the credential-holding operator PC calling the external KT Cloud API, not any local OpenStack installation. Management-host approval remains unproven and is not needed by the new collector. A local operator still needs permission to use the protected account and direct network reachability; the tool does not provide that permission or create a fallback recipient.
- No real Credential/SSH/KT endpoint/VM was exercised during the correction. No Git/log Credential compromise is inferred from the review. Original evidence and operational follow-ups remain as previously documented.

## Independent second pass / disposition

Reviewer /root/c2_c3_audit independently checked the review, exact consumed fields and D05, then reviewed the full56-file evidence-only PR and staged9-file correction. It independently executed all8 offline tests, verify-package and check-publication-tools successfully; confirmed tested-source SHA/index/worktree correspondence and18 historical outputs unchanged in Git/canonical LF bytes. It additionally reviewed latest main dcc148a's six Web files and Preview create201 JSON/close204 compatibility. Final judgment: P2 correction ready for re-review, no remaining blocking finding in this scope. This is not a broader quality approval of the other Web PRs or a new live/CI pass. After adding this final disposition, manifest and index/diff correspondence are checked again before push. The audit does not authorize merge/deploy; user explicitly authorizes the current push/re-review scope.
