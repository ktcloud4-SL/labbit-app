# PR196 trial preparation — independent scoped review

Date: 2026-11-23. Reviewer: /root/audit_pr196_operator, separated from implementer.

## Scope and baseline

Reviewed PRE_MERGE_AUDIT.md and the current complete preparation delta in `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-dr-stage3-live-prep-20261123`: HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`, base `753eecbff737409a4c9cc8c542f3c6baa6acf0da`. Tracked modifications are docs/backend/dr-stage3-deployment.md and dr-maintenance-integration.md; untracked additions are dr-pr196-trial-preparation.md and evidence/LBT-60/2026-11-23-pr196-trial-preparation/.

No product code, workflow, migration or runtime contract changed. Earlier full PR/linkage/P2 reviews apply only to unchanged baseline; this review does not claim to re-audit the prior 387-file PR. Direct consumer inspection included both operator CLIs, AWSCLI fixed-version reader, Runtime0.1.14/dr-control.md, migration loader/runner/CheckSchema, current migration files and delivery workflow. Repo guidance was previously reviewed; PRE_MERGE_AUDIT.md was reread for this task.

This reviewer ran no AWS, Kubernetes, SQL, shared DB, service or Slack call, and made no source changes, commits or pushes. Parent supplied current connected Jira/Confluence observations, retained in ssot-reviewed.json. Their live remote freshness is not independently asserted by this network-free review.

## Findings before edits

- No confirmed product/security/publication-blocking finding in the reviewed delta.
- DOC-01 / Low clarification: Evidence README.md:14 originally says “이 문서와 schema query를 제외하고 공유 DB SQL이나 migration을 실행하지 않았다”, which can imply that schema query ran. README:10 and static-validation explicitly say SQL was not run. Reported to parent before any edit; recommended unequivocal “schema-readonly.sql은 준비만 했으며 공유 DB SQL·migration은 실행하지 않았다”. No runtime impact; required check is consistency with the no-SQL execution scope, not a new live test.

## Requirement → implementation / evidence trace

| Requirement / source | Directly reviewed implementation or preparation | Actual verification and limits |
| --- | --- | --- |
| Latest P2 QA / LBT60#11267 and current Runtime0.1.14 | ssot-reviewed.json, docs updated from historical Resume-only/migration12–14 to existing init/Pause/collector/signing and migration12–15 | Scoped QA is not deploy/execute approval. Historical Confluence Worker absence is not treated as current source truth. Reviewer did not independently refresh connector sources. |
| Read-only preparation / announce impactful changes first | Collector helper and all 16 command inventory entries; draft P01–P08 and pre-change notification list | STS/EKS/RDS/ECR Describe and Kubernetes get/logs/can-i only. SelfSubjectAccessReview is transient authorization review, not RoleBinding/config change. No helper execution by reviewer. Actual projection records selected metadata, not functional trial. |
| Exact source/target / failed query provenance | Actual account/cluster/DB ARN+ResourceID, deployment/Pod UID+digest, original/corrected ECR inventories | Initial labbit-backend NotFound retained; corrected backend exact two tags ImageNotFoundException retained. This does not prove absence from every registry/tag. ConfigMap open does not prove actual file contents, no users, no UNKNOWN or Provider inventory. |
| Preview and disabled paths do not authorize execution | cmd/labbit-dr-control/main.go and cmd/labbit-dr-evidence/main.go early preview and EnableExecution branches, synthetic fixture inputs/results | Four reported local checks distinguish preview exit0 from explicit-execute/disabled exit2, both HOLD and false authorization/cloud flags. Legacy inspect accepted only by control and companion input rejection is retained. These checks do not read operational key/SQL/S3/Pod or validate actual signatures. No duplicate test rerun for unchanged product. |
| Protected keys, exact purpose/actor/TTL and signed fencing | Runtime and draft purpose/permission table, unresolved values, signed fresh SingleWriter/Fencing/Provider requirement | Keys/Policy/DSN/CollectionPlan are not operationally generated/accepted. Windows owner/DACL, distinct approval/observation bytes, immutable Reference and authoritative control DB requirements retained. Admin3 broad access is not minimal-principal acceptance. No boolean/SQL-zero/local-primary shortcut to ACTIVE. |
| Storage endpoint compatibility | AWSCLI.Read uses Policy.Region for S3 endpoint; new doc explicitly notes Osaka bucket versus Seoul control policy | Actual exact-version endpoint/access remains pending. No existing Osaka bucket compatibility assumed and no implicit IAM expansion/upload. |
| Safe DB migration and restoration | Readonly SQL transaction/timeout statements, tracking columns, migrations12–15, exact-byte loader and filewise runner semantics | SQL is preparation only. Existing Job applied11 is historical, not live SELECT/checksum. Fresh backup/restore responsibility, partial-commit behavior, no down/DROP and no automatic old-image ACTIVE rollback are explicitly pending. |
| Separate M194/M196 and real safety acceptance | Prep sequence/consumer contracts | MAINTENANCE and APPROVED_CONTROL are separate config/RUNs. Global backlog effect is disclosed; operation ID is not an isolation boundary. Actual drain, full Pod/UNKNOWN/Provider inventory, trusted external safety evidence, rollout freeze/pin/restore and approved work window remain unresolved. |

## Independent local checks

- JSON parsing and helper AST parsing passed. Helper was only parsed/read, never executed.
- source-manifest14/14 listed raw SHA256 and size match working bytes: three changed documents plus eleven unchanged relevant dependencies.
- All 15 migration Git-blob SHA256/size and working-copy raw SHA256/size match migration-source-manifest. Migrations1–11 Git bytes equal base753eecb. Git LF versus Windows CRLF distinction is preserved; actual image embedded bytes and actual DB checksum remain explicitly false/unverified.
- Recomputed selected post-readonly baseline comparisons: Pod UID/imageID/restart, Deployment UID/image/readiness and ConfigMap UID/resourceVersion/mode all match. This does not prove zero mutations everywhere, complete functional service health or a global freeze. Deployment generation is correctly not claimed as a before/after comparison.
- Examined evidence projections for credentials/private keys/tokens; no such values observed. Hostnames, account IDs, ARNs, workload UIDs, image digests and selected migration log counters are operational metadata, not credentials. Raw response hashes do not make discarded full responses independently reproducible; README/inventory state this limit.
- Delivery workflow remains unchanged: PR build is distinct from main/tag publication; AWS OIDC/ECR and GitOps update are gated away from pull_request. This audit is not push or publication approval.

## Failure / lifecycle assessment

Draft missing inputs remain null/unapproved, EnableExecution=false and not_runtime_input=true. Read failures are NOT_VERIFIED, not inferred readiness. Existing ReplicaLag FAIL, four required NOT_VERIFIED and overall HOLD remain. The proposed init/Pause receipt is not drain, Single Writer or full Fencing proof. Restoring a snapshot or old image can erase control/journal knowledge, so manual reconciliation and explicit restoration approval remain required. No mutation retry, resource cleanup or new deployed lifecycle is introduced by this doc-only delta.

## Current conclusion and remaining verification

The reviewed preparation scope has no confirmed blocking defect; real #196 trial is not ready or accepted. The low wording issue was reported before edit. Final AUDIT/artifact manifest were still being assembled when this review pass was recorded; append final consistency/hash verification before treating the complete packet as reviewed. Live IAM/RBAC, protected-key placement, fixed-version S3 path, SQL/schema/backup/restore, candidate image/pinned rollout, real control initialization, full inventory and signed external safety facts remain separate gates. This report does not authorize merge, deployment, AWS/DB/service changes, Slack or external transmission.

### Incremental check — DOC-01 resolved

README.md:14 now states exactly: `schema-readonly.sql` was prepared only and shared-DB SQL/migrations were not executed. Direct readback confirms alignment with README:10, static-validation and the draft. No product source changed. DOC-01 is resolved; no additional confirmed finding.

### Final packet verification

Directly reviewed final AUDIT.md, local-checks.json, validation.json, supplemental-readonly-summary.json, prepare-packet.py.txt and finalize-readonly.py.txt. The supplementary S3 list/location metadata is explicitly a transcript-derived summary with missing exact argv/UTC, not an independently preserved raw payload or version readback. Preparation/finalization helpers parse and perform only the described local generation, preview/disabled runs and selected readonly queries; this reviewer did not execute them. Both ignored executable files exist and their actual size/SHA256 match local-checks and preview results.

Independently verified final artifact-manifest: **27/27 entries** raw SHA256 and size match, no missing or extra current delta file; the self-excluded manifest makes **28 delta files** in total (three docs plus25 packet files). source-manifest14/14 and migration manifest15/15 verification remain valid. Current HEAD remains be4d0ba and tracked diff is still only the two original docs. `git -c core.whitespace=cr-at-eol diff --check` passes. No product/runtime/workflow/migration changes were introduced. Validation limits preserve the two selected local Go tests versus prior full CI; this reviewer did not rerun them. Parent's Gitleaks exit0/findings0 is recorded as submitted validation, separately from direct content inspection.

Verified snapshot source-manifest SHA256: `15b8c759571ff0c8dd6089445d0ec2d72741c455db83f1580f7e52204dfd9761`. Artifact-manifest SHA256 at this review: `ac90354d0c168f09c64f55c67f46bb843840de8446fe9a39eebe03c626ace886`. Parent will copy this final external report back into independent-review.md and regenerate the manifest; those two expected metadata updates require final author hash verification, not a new product test pass.

Final scoped result: **no confirmed blocking finding in the final preparation delta**; DOC-01 resolved. The preparation documents/Evidence can be reported with their explicit limits. `live_test_ready=false`, existing FAIL/four NOT_VERIFIED/HOLD and all actual operational prerequisites remain. No actual trial/PR-wide acceptance, push, merge or deployment authorization follows from this review.
