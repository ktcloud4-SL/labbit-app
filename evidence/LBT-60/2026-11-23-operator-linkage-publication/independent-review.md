# PR196 linkage publication independent review — 2026-11-23

## Decision and boundary

검토한 로컬 후보의 Draft PR 공개 범위에서 새로 확인된 publication 차단 결함은 없습니다. 기존 코드 독립 검토(LINK-01/02/03 해결 및 producer→consumer roundtrip)를 재사용하고, 이번에는 Evidence 무결성·민감정보·tested-source 추적·PR 이벤트 배포 경계를 별도로 검토했습니다. 이는 전체 DR 승인이나 merge/deploy 허가가 아닙니다. 사용자 push 승인은 별도 근거이며, AWS·서울 서비스·공유 DB·Slack 및 merge 금지는 유지됩니다.

## Fixed scope and SSOT

- Repository worktree: C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-dr-stage3-live-prep-20261123
- Local base origin/main: 753eecbff737409a4c9cc8c542f3c6baa6acf0da
- Local HEAD and publication target tracking ref: 94fe1f61d7e398761291a967fab9820bfb08bc3d
- Current source candidate is uncommitted at this review. source-manifest.json records that fact; it does not claim HEAD94fe includes the local implementation.
- PRE_MERGE_AUDIT.md read. Full implementation review and traceability are in pr196-linkage-review-20261123.md. Latest remote/Jira LBT-205/LBT-60/Confluence v4 unchanged status was freshly checked by parent, not independently queried here because this review is network-free. Owning runtime/DB/docs consumers were independently reviewed in earlier code passes.

## Integrity and source traceability

Independent local recalculation found:

| Check | Result |
| --- | --- |
| operator-linkage-offline artifact manifest | 60/60 sizes/SHA256 match |
| historical live-readiness artifact manifest | 26/26 match |
| publication source manifest | 277/277 raw-byte and LF-canonical SHA match |
| prior independent source snapshot | 64/64 unchanged |

Machine-readable evidence: C:/Users/user/.codex/tmp/pr196-linkage-publication-checks-20261123.json. The generated audit packet is deliberately excluded from its own source inventory to avoid recursive manifests. Artifact manifest excludes itself. Evidence .gitattributes preserves original bytes with -text; CRLF source-log bytes must not be normalized merely to silence trailing-CR diagnostics.

Publication must preserve this historical tested-source capture. The eventual commit will have a new SHA, so evidence saying HEAD94fe plus uncommitted=true remains truthful. Parent should compare the final staged/tree source against this manifest and refresh the outer artifact manifest after adding this report; do not relabel uncommitted execution as tests executed on the future commit. Parent separately reported 125/125 staged Git blobs match expected bytes and cr-at-eol-aware whitespace check has zero errors; those are parent-executed checks, not independent executions here.

## Sensitive content and original logs

125 candidate/new packet/source-delta files, including decompressed gzip originals, were inspected with narrow private-key PEM, real Slack webhook, GitHub PAT and AWS access-key patterns. No hits found. Gitleaks source/raw-log outputs in submitted packet are empty and artifact hashes match. Parent separately reported a final export gitleaks scan with zero findings. These checks cannot prove absence of every possible secret; no active credential value was observed. Public Alpine signing-key material and fixed historical checksum exceptions are narrow .gitleaks.toml path-and-match allowlists, not broad exclusions.

Original logs retain initial failures and skips. Independent recount agrees with the selected final logs:

- Full Windows Go v5: 4604 passing test events, 88 skip, 0 fail; 53 package pass/14 package skip.
- Focused producer/race selected log: 335 pass, 0 skip/fail, 6 packages.
- Disposable PostgreSQL final: 1761 pass, 10 skip, 0 fail, 6 packages.
- Reviewer unit/mock run: 459 pass, 0 skip/fail, 7 packages.

The older full/focused/PG logs have different historical counts and are correctly preserved rather than substituted for final runs. The initial PG targeted failure remains archived and explained. Counts include subtests; they are not counts of unique test functions. Windows/PG/Linux/web runs by parent are submission evidence, whereas the 459 unit/mock events and focused last test-only SignApproval→ExecuteRestriction init/pause check were independently executed previously. No additional test rerun was necessary for publication because the 64-file snapshot is unchanged.

See C:/Users/user/.codex/tmp/pr196-linkage-publication-rawchecks-20261123.json for recounted log results. Linux no-NIC and web results remain submitted evidence; this publication pass did not reproduce their environments. Real AWS runtime/SQL/Single Writer/provider and remote deployment are not established by these offline results.

## PR workflow/deployment boundary

All seven current workflow files were reviewed; no workflow file is changed by this candidate. No pull_request_target/workflow_run path was found. Feature-branch push can synchronize the existing PR and run PR CI; those jobs use GitHub-hosted dependencies, caches and build services, so zero external CI traffic is not claimed.

- delivery-backend.yml: AWS credential/ECR steps exclude pull_request (lines62,69); image push excludes pull_request (line93); GitOps update is only push to refs/heads/main (line100). PR may build/login GHCR/cache but does not publish images or update Seoul/Osaka GitOps through these steps.
- delivery-web.yml: publish job excludes pull_request (line30); PR smoke explicitly uses push:false (line204); GitOps update is main push only (line97).
- delivery-connector.yml: release triggers connector tag/manual workflow and release steps require connector-v tag. This feature PR push does not publish Connector releases.
- contracts.yml, go.yml, web.yml: validation/build/tests and local/CI fixtures; no production deployment command.
- security.yml: scanner jobs; no production deploy.

This is static workflow analysis of the inspected source, not proof that a future CI run passed. Main/tag/manual triggers have separate behavior and are outside the approved push-only operation. Parent must retain Draft and must not merge.

## Findings and remaining limits

No new confirmed P0/P1/P2 publication finding. Earlier LINK-01 TLS pinning, LINK-02 verified FAIL preservation and LINK-03 missing/invalid readback classification are resolved and independently regression-checked in the code review report. Runtime source remains unchanged after those checks except the already-verified test-only real protected synthetic signing roundtrip.

ReplicaLag policy FAIL, four mandatory NOT_VERIFIED, execution_gate=HOLD and recovery_authorized=false remain. This packet does not assert shared-DB migration15, production key provisioning, actual authenticated Pause/init, physical Fencing or full DR are operationally accepted. Historical readonly AWS metadata smoke is explicitly scoped and cannot substitute for authentication/signature/SQL/Single Writer verification.

No product source was edited; no network/AWS/DB/service/Slack command was executed by this reviewer. Only local read-only inspection and this report were performed in the publication pass.
