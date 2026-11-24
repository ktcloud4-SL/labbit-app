# PR196 isolated candidate publication — independent staged review

2026-11-23 / reviewer /root/audit_pr196_operator, separate from implementation.

## Current reviewed scope

Read PRE_MERGE_AUDIT.md and applicable App AGENTS/CONTRIBUTING/TESTING. Directly inspected `.local/pr196-candidate-publish-20261123/candidate-build.yml` in App worktree `C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-dr-stage3-live-prep-20261123`, current HEAD/exact build source `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`, base main753eecb. Related unchanged Dockerfile/.dockerignore and all existing workflow trigger/deployment conditions were checked. Earlier PR196 source audits are reused only for unchanged product code. This stage covers the one isolated build workflow, not the forthcoming Windows archive verifier/ECR publisher or actual candidate image.

No network, AWS, Docker, SQL, service, remote Git or Slack call was performed. Reviewer did not edit source or workflow, commit, push, merge or deploy.

## Findings / recommendations before changes

No confirmed product/security/Seoul-deployment blocking finding in the inspected workflow.

CAND-01 / Low lifecycle hardening recommendation, original workflow lines32–33 and64–69: the explicit upload-artifact has retention-days1, while build-push-action's own build-record upload/retention behavior is not explicitly controlled. Thus the workflow itself guarantees1-day retention only for its explicit candidate artifact. Recommended setting DOCKER_BUILD_RECORD_UPLOAD=false (or explicit build-record retention1) to remove dependence on the action's auxiliary artifact defaults. This was sent to parent before any edit; no external action documentation was fetched in this network-free review, so this is recorded as a default-dependency hardening recommendation rather than a newly reproduced production defect.

## Direct analysis and traceability

- User purpose is candidate publication without merging or changing Seoul. The workflow triggers only one named `codex/build/LBT-60-pr196-candidate-20261123` branch and one workflow path. Actual branch creation/push remains parent's separately authorized activity, outside reviewer execution.
- Repository token permission is contents:read only. No id-token, packages:write, AWS OIDC/configure-credentials, registry login, ECR push, GitOps token, kubectl/SSM/DB or deployment command occurs in this workflow. Upload-artifact uses hosted Actions artifact transport, not AWS environment configuration.
- Checkout is pinned to the full be4d0ba SHA, persist-credentials=false; HEAD and clean working tree are asserted before build. The build workflow commit is separately captured through github.sha, distinct from product SOURCE_SHA.
- Explicit path context `.` after the exact checkout avoids implicitly building the workflow branch's different HEAD. .dockerignore excludes .git, .github, .local, .env*, private-key patterns and docs; no local Windows worktree or cloud credential is uploaded as the build context. Runtime stage copies the two binaries and db tree, not the complete context.
- Linux/amd64, push:false and load:true produce only a hosted-runner local candidate. Candidate labels include exact product SHA and candidate-not-deployed scope. Image inspect validates architecture, user, entrypoint and revision before artifact upload. Docker save and outer tar SHA provide transfer integrity, not deployment or runtime functionality proof.
- Single upload step defaults to success-only, if-no-files-found:error, retention-days1; build/export/inspect failure prevents candidate artifact acceptance. Hosted runner cleanup removes ephemeral image/context after job. No automatic fallback to publishing main/latest is present.
- Existing delivery-backend/web workflows are limited to main/tag/PR/manual events, and connector to tag/manual; normal Go/web/contracts/security pushes target main. The proposed build-branch push does not match those deployment triggers. Their separate manual invocation or changing main is not part of this plan.
- Existing Dockerfile uses mutable base tags and dependency/toolchain network resolution. Exact source is pinned, but source SHA alone is not a reproducible final image digest or verified database migration bytes. The final image/config/layer digest and archive migration verification must be captured by the forthcoming verifier before ECR publication/use.
- No Kubernetes deployment, database migration, key/Policy provisioning, closed/Pause/Resume or production workload is run by this workflow. This build is not evidence of multi-Pod control, Fencing, SingleWriter or service recovery. Existing FAIL/four NOT_VERIFIED/HOLD remain.

## Executed local static checks

Parsed YAML with BaseLoader, asserted branch filter, contents-only permissions, exact checkout SHA, linux/amd64/push:false/load:true, no AWS action, and parsed embedded Python AST. All passed. Workflow raw snapshot: size2856 bytes, SHA256 `2fa28aeb51a0f3e8e8b5076234ed0e10f649dd6b20d6cebde11fcd7f1bd9ff55`.

No actual GitHub Actions build was run by reviewer; no image was built, loaded, pushed or deployed. No broad product/unit regression was repeated for this workflow-only static pass.

## Current conclusion

No confirmed blocking finding in the initial workflow scope. CAND-01 is a scoped lifecycle recommendation to resolve or explicitly document. This is not final review of the complete image publication chain: archive verifier, exact registry/tag/no-overwrite behavior, credential handling and interrupted/UNKNOWN publication must be separately reviewed when available. Audit does not grant push/merge/deploy/Slack permissions.

## Incremental review — Windows verifier and original tag publisher

Read `.local/pr196-candidate-publish-20261123/{main.go,main_test.go,go.mod,go.sum}` and installed go-containerregistry v0.22.1 source consumers. Product App source remains unchanged. Parent reports workflow-only build branch commit27e1a2cba6a414025155c02a2c522e635cfe228c; this network-free reviewer did not independently fetch the remote execution. CAND-01 readback confirms DOCKER_BUILD_RECORD_UPLOAD:'false', resolved.

### Findings reported before fixes

| ID / severity | Location in original helper | Reproduction / impact | Required regression |
| --- | --- | --- | --- |
| CAND-02 / P2 | main.go:42–44 aws() | Inherited AWS_ENDPOINT_URL*, configured profile endpoints or related CLI overrides can redirect STS/ECR requests although --profile/--region are fixed. Identity/tag-absence responses then need not originate from intended AWS endpoint; CLI-signed auth requests can leave the expected boundary. Static code proof, not a live exploit/test. | Pin service endpoint and disable configured/env endpoint overrides as in existing App CommandRunner; local argv/env assertion, no real AWS needed. |
| CAND-03 / P2 | main.go original describe-images absence check through remote.Write at208 | Another writer creates same candidate tag after absence read; mutable registry Write replaces it. Preflight absence is not create-only/CAS, contrary to “new unique tag only” claim. Static race proof, not actual ECR overwrite. | Atomic create-only/immutable guarantee, or remove tag writes by publishing verified content-addressed digest. Regression must verify destination digest/reference and forbid tag fallback. |

Both were sent to parent before any changes; parent accepted and reported them to user. The current original helper is not accepted for real publication until the fixes are independently rechecked.

### What the original verifier does and does not prove

Default path reads only local archive/manifest and produces local JSON; --publish is a separate explicit flag. Config checks Linux/amd64, appuser, entrypoint and exact revision label. Fifteen expected Git-byte SQL files and two executable ELF-magic binaries are read from the flattened image, checked for presence and migration SHA/size; extra SQL under the migration directory is rejected. This is artifact/config verification, not execution of application/migration, source-to-binary attestation or runtime functional acceptance. Supplied migration manifest authenticity still comes from its separately audited Git source/hash chain, not from arbitrary source_head string alone. Binary ELF check originally validates magic/executable mode, not full machine/type semantics.

Publication uses in-memory ECR auth, hardcoded registry/repository/profile/account/user, explicit remote.WithAuth rather than Docker credential-store persistence. Raw auth bytes are not printed. On write/readback error it returns without success and requires requery before retry. No deployment/GitOps/DB action occurs. Outputs remain HOLD/false. These positives do not negate the two findings above.

### Local execution / limits

Ran existing `go test -count=1 ./...` in the helper module with GOPROXY=off and GOSUMDB=off: PASS (two tests). They exercise config rejection and incomplete/duplicate/escaping manifest metadata; they do not exercise a complete image archive, failure after registry write, endpoint isolation or no-overwrite concurrency. No Docker/AWS/registry/DB/network call was made by reviewer. Additional focused archive/publisher boundary regressions should accompany the accepted fixes.

### Proposed digest-only correction review

Locally inspected go-containerregistry v0.22.1 remote.Write/Push and commitManifest. name.Reference supports name.Digest and the HTTP PUT target is `/v2/<repo>/manifests/<ref.Identifier()>`, avoiding tag writes. Exact destination must equal verified image digest; no tag fallback on ECR unsupported/UNKNOWN response. A manifest Subject can invoke referrers/fallback behavior, so accept only the ordinary subject-free candidate image. ECR support and actual untagged lifecycle/retention remain live checks outside this review; predeployment pinned digest readback is still required. Content-addressed duplicate PUT of identical bytes does not justify general mutation retries or an assumption of repository retention.

## Incremental correction readback — dedicated immutable repository

Parent superseded the digest-only proposal: existing backend repository lifecycle can expire other images by total-count policy, so it will not be used for candidate writes. The new scope is a dedicated `backend-dr196-trial` repository, not mutation of existing backend's mutability/lifecycle/IAM. Parent reports the user was notified beforehand; this reviewer did not create/read the repository or independently verify remote approval/configuration.

- **CAND-02 resolved in inspected source:** aws() now permits only sts/ecr, pins regional STS and api.ecr endpoints, sets explicit CLI timeout/pager/autoprompt flags, and uses a filtered environment removing AWS_ENDPOINT_URL* case-insensitively and constraining endpoint configuration/max attempts. TestAWSRemoteEndpointOverrideRemoved confirms hostile inherited override values are removed and ordinary Path retained.
- **CAND-03 resolved at the helper's observed-configuration boundary:** only hardcoded dedicated repository is writable. Before auth/write, DescribeRepositories must return exactly one matching repository name and exact account/region URI, IMMUTABLE and zero exclusion filters. Existing tag causes abort; absence lookup error must be ImageNotFoundException. The server's unchanged immutable policy, rather than absence preflight alone, supplies concurrent duplicate-tag rejection. TestMutableOrExcludedRepositoryRejected rejects mutable/excluded/production-backend responses. Actual IAM/repository state and no concurrent administrative policy relaxation remain operational preconditions, not proven by unit tests.
- Digest-only was a considered alternative, not the implemented publication path; its earlier discussion must not be represented as actual ECR behavior or a completed test.
- Independent `go test -count=1 ./...` rerun with GOPROXY=off/GOSUMDB=off passed the current four tests. No network/AWS/registry invocation by reviewer. There is still no full archive or fake-registry race/UNKNOWN integration test in this module; actual validated archive and publication/readback Evidence must be supplied separately.

No new confirmed blocker was found in this corrected local publisher source. The helper continues to write no Kubernetes/GitOps/DB state and returns HOLD/false authorization flags. Candidate repository creation/storage may incur its own cost and cleanup responsibility; those are distinct from app deployment and require a retained record, not automatic deletion or changes to the existing production repository.

Corrected local snapshot (raw SHA256): main.go `581b69d104daab4d4765a8de5084f45778addab7731041185b2a9616794c10ca`; main_test.go `adc57853a3db8a9802707b60167bfc55aa9467dae295b56b897ec18777a6687c`; go.mod `cd0f9125ad50ef494de0c03b2fd79330afd580daff0f792a4514a1254aaacf82`; go.sum `12be998f5bf07a4a78df5b2ce9f0c6653a44b8cbf82277dbf170ccc478212278`; workflow `4a5e9da36c182d3d29fb27e733675c9e2c67c9e133b0a019e24efb6557172969`.

## Actual candidate artifacts and protected-key preparation review

Reviewed the supplied `evidence/LBT-60/2026-11-23-candidate-publication/` records and `.local/pr196-protected-prep-20261123/main.go`; no AWS, registry, SQL, key content or service call by reviewer. The following distinguishes submitted real results from independent local artifact inspection.

### Artifact chain independently checked locally

- Supplied GitHub build summary identifies exact product be4d0ba and workflow-only commit27e1a2cba6a414025155c02a2c522e635cfe228c, run45109945049/artifact14788354068. Job text shows exact build/export and successful artifact upload. Reviewer did not refresh GitHub remotely.
- Actual local candidate.zip size18775046 and SHA256 `331dd169b8e1a559d1a4559d23a976785700fae5cb60658305d6d9ea08510024` match download-integrity. Actual candidate.tar size18842624 and SHA256 `a2bd08cf2ba33b36e27ba26442ebc2d5d8e9651544dabdb55294dbcdf3b68ff3` match archive record.
- Read tar bytes without filesystem extraction or application execution: configSHA439c33a68b6590357acf3bfc0e9fcba77b02f2f55f8657836a2a4f0d9cbd7213 matches the build log; config is Linux/amd64, appuser, expected entrypoint and exact source label. All seven layer decompressed diffIDs match config rootfs; no whiteouts encountered. All15 migration file hashes match the independently audited Git-blob manifest; both server/migrate hashes and executable bits match local/publication verification.
- Six helper/build/executable records in publisher-source-manifest match current local size/SHA, and five copied source/text files are byte-identical to the inspected helpers. No product source mutation was added.
- Build/export OCI manifest digest ffab608728a82b5f8190a8960943572d912788dbe6b4f1fda8bf070022c85e10 differs from loaded/published Docker-schema2 digest9dc273e94c1b22427059b2b246e3757b5a6f44d31a0abaa2ef858f62ccec6fac. The archive actually contains the former OCI manifest plus classic manifest.json. This is not evidence of identical manifest-byte preservation across conversion. Parent was advised to document the conversion and use the verified final digest for deployment; rootfs/config/source/migration checks bridge this chain.

### Actual cloud results reviewed as submitted Evidence

Publication result records exact admin3 actor, dedicated repository, trial-only tag and pinned digest9dc273…ccec6fac; local and publication results have matching source/digest/config/migration/binary metadata. Repository creation record is strict IMMUTABLE, AES256 and scanOnPush; separate owner seobin, review within7days, no automatic deletion/lifecycle and later explicit cleanup approval. Existing backend repository/IAM are reported unchanged. Connection-path readonly metadata separately observes the same image digest/tag. This is publication/readback scoped evidence, not actual deployment, application execution, vulnerability-scan acceptance or independent reviewer cloud readback. Existing HOLD/false flags remain.

### Protected-key helper and public result

Helper snapshot SHA256 `86c3a28dfcacc9096fa27c521679350fb17f4bcdeb35d440aae7d13c705cf3e2`. Source creates no cloud/DB/service client. It verifies an absolute non-symlink precreated directory, creates a nonsecret ACL probe and invokes exact candidate ReadProtected before key generation. Both generated Ed25519 PKCS8 files use O_EXCL/no overwrite and fsync, then candidate ReadProtected plus PKCS8 parse/public-key roundtrip. Success clears private byte buffers and emits only purpose/key ID/public key/path/check metadata. Public result has two distinct32-byte public keys and runtime protected-read/roundtrip PASS, while policy_enable_execution=false and operational_signing_acceptance=false remain. Reviewer read no private key/DSN. This is local protected-key preparation only; not actual approval/observation signing, trusted collector/fencing, Policy acceptance, rotation/recovery or remote IAM validation. Partial local generation failure must preserve existing files and require inspection rather than rerunning over them; O_EXCL already prevents blind overwrite.

### Scan-process finding and reviewed allowlist

Parent disclosed CAND-04/P2 before correction: nonzero prepublication Gitleaks did not stop subsequent PowerShell command lines, and the separately authorized candidate upload ran. The readback succeeded, but initial scan did not pass. This is an orchestration control failure; a later clean scan cannot retrospectively repair the gate. Parent reports follow-up mutations separated by explicit result/exit checks. This reviewer has not seen an automated orchestration regression, and does not recast this as one.

Reviewed gitleaks-reviewed.toml: default rules remain enabled; three allowlists use condition AND, bounded path plus exact known public match. They permit only the specific public docker-credential-helpers h1 checksum, exact migration10 Git SHA (independently matched above), and a precise reviewer explanatory phrase. They do not broadly ignore all checksums, tokens, keys, helper directories or image files. These exact strings are not credentials. Original default-scan findings must remain redacted and linked; allowlisted zero-findings is a separate later result, not a clean initial scan. No private material was observed in inspected public records.

No new confirmed blocking source/image defect found in this stage. Before final packet acceptance, include the nonsecret keyprep source/hash and explicit build-to-published digest explanation, update AUDIT's stale “ECR follow separately” current-gate wording, retain original scan result, and validate final artifact manifest. These are evidence/provenance clarifications, not requests for additional AWS/SQL/private-key calls.

## Pre-execution review — remote readonly schema helper

Reviewed `schema-readonly-remote.py.txt` before execution; original raw SHA256 `02767b739a2127f2925e866ef4108bd2b226dafd7a6184a06a1ee8ca7df58d28`. This reviewer made no AWS, Secret, SQL, SSM, network or key-content call. The parent separately reports management-instance identity, isolated venv/psycopg/RDS CA preparation; those actual preparations are not independently live-verified here.

**CAND-SQL-01 / P2, confirmed before fixes:** lines16,21,31–32 use Python assert for exact STS identity, DSN target/password, actual SQL identity/readonly and observed TLS. Python -O or PYTHONOPTIMIZE removes all these security checks. The remaining cfg is reconstructed from parsed DSN, so a mismatching secret host/db/user can then be used rather than being refused.

Cloud-free reproduction used synthetic in-memory subprocess and psycopg modules only (no real process or connection): with wrong STS/DSN/SQL/TLS values, optimize0 makes one fake STS call then NOT_VERIFIED, zero fake secret/connect; optimize1 performs fake Secret read, fake connection and six fake SQL executions then returns OBSERVED_READONLY. Static compile likewise removes the four assert failure paths. Sent finding to parent before any edit. Required correction: explicit conditional RuntimeError/fail-closed branches, with optimized-mode negative tests proving identity mismatch prevents Secret lookup and target mismatch prevents connection. This is not a request to run a real bad-account/DB test.

Nonblocking hardening recommendation: dropping DSN service/options/hostaddr does not remove libpq's inherited PGHOSTADDR/PGSERVICE/PGSERVICEFILE etc. Sanitize PG* process defaults for this one-shot reader, or explicitly constrain all relevant defaults; exact sslmode/sslrootcert already reduce endpoint ambiguity. No actual inherited-env redirection was reproduced.

Otherwise the inspected source limits AWS to fixed expected endpoints/account/session identity, one RAM-only secret, reconstructed target/credentials, verify-full with official CA, startup default readonly and short statement/lock/idle timeouts. SQL is explicit BEGIN READ ONLY, four SELECTs of identity/TLS/migration/catalog, finally ROLLBACK. No DML/DDL/provider mutation. Generic stage-only failure output avoids raw DSN/libpq/Secret disclosure. SQL metadata is not SingleWriter/Fencing proof. LIMIT100 migration rows bounds output but does not independently prove arbitrary unbounded history completeness; current known11/15 expected versions must still be compared.

Pending finding correction, this original schema helper is **not accepted for live execution**. Readonly intent does not waive exact caller/target validation.

### CAND-SQL-01 correction independently verified before live execution

Updated helper SHA256 `d8a557560ed459a0ee4a6612a8b8677ba3ccd5729383694400047b7f6fa46348`: all four assert guards replaced by explicit mismatch conditions raising RuntimeError, and all inherited PG* variables removed before libpq invocation. Direct source read confirms fixed expected account/role, DSN target, SQL identity/readonly and TLS checks remain active under optimized Python.

Read and independently ran `C:/Users/user/.codex/tmp/pr196-sql-guard-tests-20261123.py` against the corrected bytes: **1 top-level test /8 optimize0+2 negative subconditions PASS**. subprocess and psycopg are in-memory fakes: wrong identity causes zero Secret reads; wrong target causes zero connects; wrong readonly/TLS prevents migration SELECT and executes ROLLBACK; credential fixture is absent from output; PGHOSTADDR/PGSERVICE are removed. No real AWS, Secret, DB, SSM or key content was accessed by reviewer.

CAND-SQL-01 resolved. No additional confirmed blocker in the corrected readonly helper scope. This is source/mock acceptance only, not actual SQL outcome, trusted Writer/Fencing proof or permission for unrelated operations. Preserve actual execution result and helper hash separately when executed under the user's existing authorization.

## Pre-execution review — Windows SSM readonly tunnel helper

Reviewed `.local/pr196-schema-tunnel-20261123/main.go` and main_test.go. Original snapshot SHA256 main.go `b438455164f8b151a915f81db323983e184cb8646bb180836be9b321982f3f7b`; test `b136ed0ded1e1166ddf5cd831fb2632559c26733b26d18fd6f37442d2378539a`. Parent reports the prior remote management-role attempt was denied at Secret read and made no DB connection; it is not replaced by a claim that the role succeeded, nor is this a request for IAM expansion.

**CAND-TUNNEL-01 / P2, reported before live execution/fixes:** main.go:190–200 silently ignores terminate-session failure, then kills/waits only the AWS CLI parent process. On Windows, killing a process does not guarantee its session-manager-plugin child is terminated. API termination failure/timeout or missing session ID can leave a child/tunnel/remote session, while the successful result only says termination_requested=true. Required correction is bounded exact-session termination with outcome/readback and bounded exact local process-tree handling (or equivalent JobObject/direct-child ownership), plus explicit NOT_VERIFIED cleanup outcome if not confirmed. This is static lifecycle proof, not an actual leaked-session reproduction; no cloud process was started by reviewer.

Related bound gap at main.go:84 and227: deferred tx.Rollback(context.Background()) and conn.Close(context.Background()) have no cleanup deadline. A new network failure during those calls can prevent the enclosing session cleanup even after the main45-second operation budget. Use separately bounded cleanup contexts. Regression should cover failed/unknown remote termination, parser/timeout failure, local descendant cleanup/port still bound and bounded DB-close/rollback; do not inject faults into a live shared DB just for this audit.

Other security boundaries reviewed positively: fixed admin3/account, exact SecretsManager ARN, fixed management instance/document/remote host/local port; AWS/PG override filtering; only in-memory credential handling; config reconstruction and nil fallback; DialFunc only localhost35432; RDS ServerName+trusted roots+TLS1.2+no InsecureSkipVerify; readonly startup parameters plus explicit ReadOnly transaction; four bounded SELECTs and explicit rollback; generic public errors without raw DSN/SSM token/libpq content. These do not imply SingleWriter or operational approval.

Independent local `go test -count=1 .` with GOPROXY/GOSUMDB off passed two config/environment tests. No AWS, Secret, SQL, SSM plugin, socket connection or private key was used. These two tests do not cover the reported cleanup failure, so the original helper is not accepted for live execution pending correction.

### Tunnel cleanup correction — first readback and residual branch

Read the first correction before execution: SQL rollback and connection close now have separate3-second contexts; remote termination requires exact returned SessionId; local cleanup issues fixed System32 taskkill /PID exactCLIpid /T /F then parent kill and bounded wait; loopback port closure is observed; failed cleanup forces final NOT_VERIFIED/HOLD. This addresses the unsafe unconditional-success path at source level, without asserting actual Windows/plugin/API behavior was tested.

Remaining narrow CAND-TUNNEL-01 branch reported before further edit: the5-second cmd.Wait timeout returns false before filling result's sessionID/ACK/port fields. If earlier execution failed with nil result, final generic NOT_VERIFIED then loses the exact session ID and cleanup uncertainty needed for remediation. Initialize nonsecret cleanup receipt fields at cleanup entry and update them before any early return. No additional cloud execution is needed to fix this traceability branch. Full actual descendant/process/API cleanup is still untested; do not label config/env unit PASS as cleanup integration PASS.

### CAND-TUNNEL-01 final source correction verified

Final helper SHA256 `ed8dcaa4a74fbba98d6e305f6a5ed6571d6d0168f8f559693a3d75ffaff6e68e`; test SHA unchanged `b136ed0ded1e1166ddf5cd831fb2632559c26733b26d18fd6f37442d2378539a`. Direct readback confirms cleanup receipt map and sessionID/ACK=false/port=false/process_wait=false initialize before any early return, ACK updates immediately, and wait completion is recorded. Timeout/unknown cleanup forces final NOT_VERIFIED/HOLD with retained nonsecret receipt. Bounded rollback/close, exact session ACK, exact PID tree attempt, bounded wait and local-port check remain present.

Independently reran the two config/environment tests with GOPROXY/GOSUMDB off against final source: PASS. No real AWS/SQL/SSM/plugin/socket/key operation was performed by reviewer. No fake process-tree/API cleanup regression was added/executed, so actual Windows child termination and remote cleanup remain unverified at this source-review stage. CAND-TUNNEL-01 is resolved in the inspected source/fail-closed-reporting scope, not accepted as real cleanup proof.

No new confirmed source blocker in the final readonly tunnel helper. Parent may use the separately authorized exact-scope readonly attempt and record actual session termination/port results; this review neither grants that authorization nor expands it to SQL mutation, IAM changes, service configuration or DR control.

## Submitted real SQL attempt and provenance follow-up

Read added image-provenance-chain.json: explicitly distinguishes OCI/build-store digestffab608… from final Docker-schema2 registry digest9dc273… and bridges configSHA439c33… plus seven uncompressed layer diffIDs, consistent with independent local archive inspection. This resolves the provenance explanation gap; no false identical-manifest-byte claim remains in that record.

Read original initial-scan-redacted.json: four generic-api-key matches (module h1, migration10 checksum in two JSONs, reviewer phrase), each matching the three exact reviewed allowlist groups. “Gitleaks1” is exit code1, not one finding. Original findings are preserved/redacted rather than erased. The key-directory PowerShell source creates protected current-user owner/DACL and validates inherited access, and the nonsecret keyprep source is now copied; this addresses earlier helper provenance request.

Read historical remote schema attempt result: command499e86e5… targeted exact management instance with corrected Python helper SHAd8a55756… and failed at SECRET_READ. This records zero claimed DB observation and no IAM grant. Later tunnel result is a separate pathway with admin3 RAM-only secret use, not rewriting role failure as success.

Read submitted tunnel result: actual identity labbit_db/labbit_user, transaction_read_only=on, in_recovery=false, TLS1.3, ROLLBACK, applied migration1–11 and null control12–15 tables. The records are consistent with readonly helper and known Git1–11 checksums. This is schema/connection metadata only, not DB mutation, migration12–15 application, backup/restore or SingleWriter proof. Reviewer did not execute or independently repeat the real SQL.

Important actual limit: supplied result is **NOT_VERIFIED/HOLD** because ssm_termination_acknowledged=false, even though process_wait_completed=true and local_port_closed=true. This is the corrected tool preserving unknown remote cleanup, not a fully successful/cleaned tunnel trial. Exact sessionID admin3-8j89n6ezjjl5vuz3cc698atlde must be read back or independently confirmed ended under existing authority; keep this original failed result even if a later exact-session observation resolves cleanup. Parent was notified before any success/cleanup conclusion.

Historical management-client preparation contains assert-based preexisting directory/CA-format checks; no Secret/SQL occurs in that preparation. This is not a new reproduced runtime incident. Before treating it as a reusable preparation script, convert security preconditions to explicit checks or label it historical and requiring review; do not rewrite already-executed source provenance. Current corrected SQL guards are independent and explicit.

## Isolated runtime-security rebuild workflow — final pre-publication source review

Reviewed `.local/pr196-candidate-publish-20261123/security-build.yml` only as the new isolated build workflow, reusing prior be4 product/publisher audit boundaries. No fresh audit of all PR files, actual cloud query, Docker build, package execution, registry call, key read, SQL or service operation was performed by this reviewer.

**CAND-SEC-01 / P2 was reported before correction.** Original workflow package check used `test "$(apk version -t ...)" != "<"`; an empty comparator result or failed comparator in command substitution could pass. A pure local shell reproduction with a synthetic failing APK comparator returned success, so version verification could be falsely claimed. Required regression: reject comparator failure (including nonzero with output `=`), empty or unexpected result, and older version; accept only successful `=` or `>`.

Correction independently inspected: assignment now has explicit `|| exit 1`, followed by a case accepting only `=` or `>`. Independently executed the exact extracted comparison block through local Git Bash with an in-memory fake APK comparator: **6/6 PASS** (=, > accepted; <, empty, error2 with output=, unexpected rejected). No Docker/APK/network call occurred. CAND-SEC-01 is resolved in the corrected source/mock scope.

Final workflow raw SHA256: `fe857d1b975f2acdc34e38bb8bb113b3118084674a5b2fd399b0bee7f73f546f`. YAML and both embedded Python blocks parse. Source Dockerfile Git raw SHA256 `21b881299271320838a72cb32252dad58995218fd7a36513066943dca529e075`. Exact single RUN replacement on pinned be4 bytes yields patched Dockerfile SHA256 `565356e64b883722e83b2d63502e81ccfe4e1014012b630ac0cf90b760fa30a8`; reversing that replacement reproduces original bytes. The added RUN installs upgraded ca-certificates/tzdata and zlib>=1.4-r0; the patch concerns runtime package instructions, not a claim of identical rebuilt Go binary bytes.

Trigger is restricted to the separate codex/build/LBT-60-pr196-candidate-20261123 branch and its security-workflow path. Checkout pins full be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1 with credentials not persisted; source clean assertions surround external /tmp Dockerfile generation. GitHub token permissions are contents:read; no AWS credential, registry login, push, deployment, GitOps or service step. Build push=false/load=true and later export/upload stay in the GitHub runner; artifact retention1day and build-record upload=false remain. Runtime command overrides entrypoint to shell, checks package/binary executable bits/appuser, and does not launch app/migrate or receive secrets/DB parameters. No new confirmed source/trigger/permission blocker in this workflow scope.

Parent-provided Alpine primary-source verification names CVE-2026-4738 fixed at zlib1.4-r0. Reviewer did not browse it. Submitted original candidate scan explicitly records zlib1.3.2-r1 CRITICAL; original candidate digest9dc273... must remain a rejected/nondeployed historical artifact, never silently relabeled as remediated. New build archive/config/migrations/source, exact patched Dockerfile and package receipt, final registry digest and fresh scan must be separately bound and verified. Existing source revision alone cannot distinguish patched runtime candidates. This review is not a live package/scan result or deployment approval.

Also read the submitted exact-session follow-up: history records admin3-8j89n6ezjjl5vuz3cc698atlde Terminated, with its exact active filter empty. This resolves the submitted later remote-session state observation while preserving the original tunnel result's missing termination ACK and NOT_VERIFIED/HOLD. It does not retroactively turn the original ACK into success. Readonly deployment observation has replicas/ready/updated/available=2 and image sha753eecb, mode open/RV12249; the app=backend Pod selector returned an empty list, so that specific list does not prove per-Pod identity. No service change or full DR approval follows from these records.

### Last workflow delta: installed package metadata source

Concurrent final delta changed only runtime version extraction from apk info to exact zlib stanza in `/lib/apk/db/installed`, followed by nonempty check. Final workflow SHA256 is now `18cb35c838b0c1ba8aa8a47e99524659a4152f358dfbb7f2632dc3e38da805d8` (supersedes the earlier fe857... workflow snapshot; Dockerfile patch hashes unchanged). Exact comparator block was rerun locally:6/6 PASS. Actual sed extraction was additionally run against three synthetic installed-DB strings: exact zlib/other stanza and zlib-dev-only cases all yielded expected version or empty. No installed live package, Docker or cloud operation was accessed. Nonempty check rejects missing zlib; explicit comparator error guard and strict accepted output remain. No new confirmed blocker in this last source delta; real new build/package/scan results remain separate.

## Rebuilt zlib1.4 artifact — independent archive review before publication

Read parent verification helper `C:/Users/user/.codex/tmp/pr196-security-artifact-20261123.py` and local artifact directly. Did not execute that helper because it writes Evidence; reviewer instead made independent in-memory inspections, with no extraction, Docker/application execution, network, credential or registry access.

Verified ZIP SHA256 `b90948d5529584a5e64379502b627623ee8afad2f527aec0ae0017e70d613534`, exactly8 unique allowed members and each member equal to local extracted artifact. Summary binds sourcebe4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1, workflow commit01863ba088770113c401af1be3ed63d006ac745f, run45113446633. Tar bytes match tar checksum; SHA256 `3fe527fa2b8bec8f7bc79c0b25b6fb2408c0c2a533703a215586c66ba8939879`. Runtime Dockerfile exactly equals pinned Git Dockerfile with the one previously reviewed RUN replacement, SHA565356e64b883722e83b2d63502e81ccfe4e1014012b630ac0cf90b760fa30a8.

Read Docker-save config and layers in memory: config SHA256 `95df936db51b9936392e600bfe7dd6cb7bf054b26b1fa58560c52d5025398813` and all7 uncompressed diffIDs match. Config is linux/amd64, appuser, /app/labbit-server entrypoint, correct sourcebe4 label and explicit zlib-minimum-1.4-r0 trial patch label. Effective installed package DB has zlib1.4-r0, matching runtime-package-check.txt. Runtime upgrade replaces usr/lib/libz.so.1 symlink to libz.so.1.4, adds that108376-byte library and deletes old1.3.2 library through expected `usr/lib/.wh.libz.so.1.3.2` whiteout. Therefore historical original-image whiteouts0 must not be copied as a new-image claim. Initial reviewer strict zero-whiteout assumption stopped; source inspection confirmed this expected package-upgrade deletion and review continued without treating it as a product defect.

All15 SQL migrations equal pinned Git raw bytes. All17 migration-directory source files, including README/embed.go, also equal pinned Git. Both binaries have executable bits and ELF64 little-endian x86-64 headers. They are **byte-identical to original candidate binaries**: serverSHA256 `d068bd0ee5c48b913b0843f548fdef42df33d9b38c5742e498260654e0353369`; migrateSHA256 `f80deb0f968b66157295e0a922fe67c01ee013b7cf4f86bd973b497be865f85d`. This is direct byte comparison, not inferred from source label or build intent.

No new confirmed archive/provenance blocker in inspected scope. This accepts local artifact structure/source/runtime package metadata as PASS_SCOPED, not actual new registry publication, fresh vulnerability scan, DB migration, service deployment or DR execution. New image registry digest/config/layer readback and scanner result must remain separate Evidence; original CRITICAL image and process findings remain historical records. User's exact publication authorization and no-merge/no-Seoul-service boundary remain controlling.
