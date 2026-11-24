# PR196 candidate publication — audit and execution boundaries

2026-11-23. Product HEAD be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1; main753eecbff737409a4c9cc8c542f3c6baa6acf0da.
User authorized candidate image publication and sequential preparation, prior notice for Seoul-impacting change, no merge.
PRE_MERGE_AUDIT.md was reread in full. This audits the publication/preparation delta against the exact previously reviewed PR196 source. Prior PR196 product code is unchanged; this record does not replace its scoped QA or grant whole-PR acceptance.

## Build workflow pre-publication pass

- New isolated build branch `codex/build/LBT-60-pr196-candidate-20261123`, based on main, changes only `.github/workflows/pr196-isolated-candidate.yml`.
- Checkout always pins product be4d0ba, persist-credentials=false and clean-source assertion. Builder uses unchanged exact Dockerfile, Linux/amd64.
- Trigger is push to this exact isolated branch plus exact workflow path. Existing Delivery push/main/tag and PR workflows do not match this branch. No PR/main/tag update/merge.
- Only contents:read; no packages write/id-token/secrets/AWS login/GitOps/Argo. Image build push:false/load:true; Docker archive is uploaded as Actions artifact, retention1day. Default build-record upload explicitly disabled.
- Candidate image archive is not a deployment. Windows publication stage verifies config + exact15 Git migration bytes before a unique trial-only ECR tag in separate `backend-dr196-trial`. Exact repository name/URI and strict IMMUTABLE with no exclusions are mandatory. It must not reuse latest/main/sha-serving tags or write to the existing backend repository.

| Requirement/SSOT | Implementation | Verification / actual gap |
| --- | --- | --- |
| Candidate source pinned, no merge/deployment (user; LBT60#11267) | workflow checkout exactSHA, no deployment step | Direct static consumer review, exact main/PR refreshed; build outcome is recorded separately after execution |
| Existing Delivery must not update Seoul GitOps | isolated branch/path push, contents:read only | Entire existing workflow triggers and main-only GitOps update inspected; no workflow/config modifications there |
| No hidden long retention | upload-artifact1day, build record uploadfalse | Reviewer CAND-01 low recommendation reported before fix; explicitfalse added |
| Unchanged application Runtime/HTTP/schema | exact source Dockerfile; image verified before ECR | Runtime0.1.14 and migration manifest consumed. No app/SSOT/schema source changes; no live SQL/claim/drain/Fencing proof |

Latest connected Jira LBT60 remains updated2026-11-23T12:43:03.347+0900, comment11267 scoped P2 resolved; Confluence63275012 historical v14/11-19 retained. Earlier live preparation packet separately identifies LBT59/205, exact source and read-only targets. Independent reviewer `audit_pr196_operator` read this workflow/source boundaries without executing cloud operations and reported no blocking finding; report will be copied into this packet.

### Findings before edits

CAND-01/Low: workflow build action defaults could retain an auxiliary build-record independently of explicit artifact retention. Reported in commentary before edit; DOCKER_BUILD_RECORD_UPLOAD='false' added. No product bug or Seoul runtime change was inferred.

CAND-02/P2: initial local publisher aws() inherited AWS endpoint overrides and lacked explicit endpoints. Reported before edit. Fixed with exact STS/api.ecr regional endpoints, service allowlist, endpoint environment removal and bounded CLI flags. Regression verifies hostile case-insensitive endpoint variables are removed. No credential was sent by the initial publisher; it had not been executed in publish mode.

CAND-03/P2: initial mutable repository check-absent→tag-write was not CAS. Reported before edit. Existing backend repository also has any15-image expiry, so it is left unchanged. Separate temporary backend-dr196-trial was announced before creation and created IMMUTABLE/AES256/scanOnPush with Purpose/Jira/Owner tags. Publisher rejects other repository names/URIs, MUTABLE or any exclusions. Server immutability prevents a concurrent different-image tag overwrite. Default verify and negative regression run before live publication. No IAM/lifecycle/existing repository changes were made. Candidate owner seobin; review within7days, later cleanup after trial/QA and separate approval.

CAND-04/P2 (execution orchestration, not product): initial pre-publication packet scan returned nonzero but consecutive PowerShell command lines still launched publisher. Reported to user before correction. The upload completed in the separately authorized trial repository and digest/config readback passed, but must not be described as following a zero-finding scan. Follow-up mutations are issued only after separate result/exit-code checks. Default scan findings are retained in redacted form: exact public dependency h1 checksum, exact Git migration SHA, explanatory reviewer phrase. A path+exact-content scoped allowlist documents these false positives; no broad secret rule disable. Review and rescan precede later preparation. No application credential/private key was present in the image/Evidence; ECR authorization token stayed in memory and was not logged. This process limitation is not hidden or rewritten as a clean first attempt.

### Failure, security and lifecycle

Clean exact source is copied by checkout, not the local dirty preparation worktree. No production credential is passed to the runner. On build failure no ECR publication is attempted. Artifact expiry1day limits build intermediate lifetime; runtime candidate retention/cleanup remains separate. A candidate image contains executable bytes/schema files, not migrated DB or runtime key material. Private source archives and artifacts retain the repository access boundary. Existing image serving tags are not overwritten.

### Current gate

Initial image was built/published/read back, not deployed. BuildKit manifest and Docker archive conversion produce different manifest digests; exact config and seven uncompressed layer IDs match, and the final registry digest is recorded in image-provenance-chain.json. No initial scan failure is erased by later success.

Actual ECR scan subsequently reported CRITICAL CVE-2026-4738, zlib1.3.2-r1. Initial image is NOT deployment-accepted. Candidate-only runtime correction is being verified separately; no package exception is silently granted. No live application trial/PR-wide acceptance is declared here. Existing ReplicaLag FAIL, four NOT_VERIFIED, DR HOLD, recovery_authorized=false and cloud_verified=false remain.

## DB access and protected-key preparation

- Two distinct local Ed25519 PKCS8 keys were generated O_EXCL/fsync, current-user-only protected directory. Exact candidate ReadProtected and public-key roundtrip passed. Only public metadata and paths are in this packet; no signing, execution Policy enablement, cloud Secret/key registration or application use occurred.
- Management EC2 has only AmazonSSMManagedInstanceCore, no inline policy. Its Secret-read attempt failed before DB connection. IAM simulation independently reports implicitDeny for the exact DSN Secret. No role permission was expanded.
- Isolated venv/psycopg/official CA were prepared under /var/tmp/labbit-pr196-readonly-20261123 on the existing dedicated management EC2; no global packages or EKS node changes. Owner seobin, inspect/remove task directory after trial preparation. Its original prepare script is a historical executed snapshot, not a reusable hardened installer: two Python assert checks must be replaced before reuse. Never rewrite historical execution bytes to imply a new clean execution.
- Official AWS-signed portable Session Manager plugin1.2.896.0 was extracted locally without installer/service/PATH registration. Existing admin3 credentials and SSM remote-host tunnel permitted the schema read without granting EC2 password access. Secret stayed in RAM; no raw DSN/password/error was saved.
- Actual SQL observed labbit_db/labbit_user, transaction_read_only=on, TLS1.3; migration1–11 match exact Git bytes11/11. All five new Worker control tables are absent. Transaction ended ROLLBACK, no DDL/DML/migration was executed. This proves neither Single Writer nor Fencing.
- Initial tunnel helper returned NOT_VERIFIED because remote termination ACK was absent despite SQL success and local port closure. Follow-up exact session terminate succeeded; History=Terminated and Active=[] are separately preserved. Do not rewrite original failure. Runtime automatic termination failure remains a limitation of this one-shot preparation helper, not a product fix or reusable operational collector acceptance.

### Additional findings reported before fixes

| ID / severity | Source / reproduction / impact | Correction / checks / remaining boundary |
| --- | --- | --- |
| CAND-SQL-01 / P2 | Original schema-readonly-remote.py.txt lines16,21,31–32 used assert; optimized Python removed exact identity/target/readonly/TLS guards. Reviewer fake reproduced secret/connect/SQL after mismatches. | Explicit raise guards and inherited PG* removal; optimize0/2 × four negative conditions PASS, independently rerun. Actual remote read stopped at Secret permission, no SQL. |
| CAND-TUNNEL-01 / P2 | Initial tunnel main.go190–200 ignored termination failure and used parent-only kill; Windows plugin child could outlive parent. Rollback/Close also lacked separate timeout. | Exact session ACK required, bounded taskkill /T for exact child PID, bounded wait and local port check; all early returns preserve cleanup receipt and become NOT_VERIFIED/HOLD. DB cleanup separate3s deadline. Config/environment two tests independently PASS; actual Windows/API failure injection not automated. Actual missing ACK failed closed and required the separately recorded exact-session cleanup. |
| CAND-05 / scanner-reported Critical, exploitability not established | Dockerfile runner25 retains inherited zlib1.3.2-r1; ECR scan flags CVE-2026-4738. Initial candidate digest9dc273... is not accepted for deployment. No running Seoul image was changed by this task. | Official Alpine tracker and v3.21 package identify fixed1.4-r0. Isolated security workflow pins exact app source and changes only the one runtime apk instruction, outside Git checkout; records Dockerfile/hash and tests zlib version/appuser without app startup. Replacement publication and scan must be recorded separately. |
| CAND-SEC-01 / P2 | Initial security-build.yml package comparison used test "$(apk version...)" != "<"; a failing comparator producing empty output passed. Independent pure-shell fake reproduced it; no Docker/AWS. | Separate assignment explicitly checks command exit, case permits only = or >. Author fake5casesPASS; independent fake6casesPASS, installed metadata parser3casesPASS. The actual first security-build package query then failed closed due apk cache lookup; preserved failed run45113239709/log. Revised installed-DB lookup passed actual run45113446633. No failed build was published. |

### Additional SSOT traceability and test limits

| Requirement / original | Implementation / evidence | Test and live boundary |
| --- | --- | --- |
| No Seoul-impacting change before notice, no merge (user) | Dedicated ECR repository; no serving tag/GitOps/CM/Deployment change; final-readonly-observations | Actual Backend remains2/2 at sha753eecb; maintenanceopen same UID/resourceVersion. Candidate publication, local key/tool preparation and bounded management-client setup occurred. No migration, rollout, closed/Pause/Resume, Provider, DNS, DB promotion. |
| Physical schema tracking / db/migrations and internal/postgres/migrate.go | schema-readonly-remote and schema-tunnel sources; schema-ledger-comparison | Actual READ ONLY ledger1–11 checksums equal candidate Git; pending12–15 unapplied, tables absent. Catalog presence alone is not full runtime/constraint validation. |
| Credential/private-key exclusion / AGENTS | Public-only key record; fixedAWS endpoints; fresh reconstructed TLS config and RAM-only credentials | Publisher4 tests and tunnel2 tests; Python negative8subconditions; independent review. Initial Gitleaks exit1 with4 reviewed false positives retained; exact path/content allowlist later scans0. Windows/API cleanup failure mock absent. |
| Unchanged HTTP/Connector/Realtime Runtime | Exact app source be4; helper sources are local tooling, not API implementation | Prior exactHEAD product CI/QA remains historical scope. Full app/Frontend suite not rerun for documentation and external packaging helpers; no product behavior changes. New image build and helper tests run separately. Live drain/committed replay/Provider/multiPod durable control/Fencing/SingleWriter remain NOT_VERIFIED. |
| Operational safety and lifecycle | Publication immutability, source/digest chain, key ACL, exact session cleanup receipt | ECR candidate remains until later trialQA/cleanup approval; local private keys are excluded from repository and need purpose-specific Policy acceptance. No automatic rollback/reapply of ambiguous DB/control state. |

## Final replacement publication and scope judgment

Replacement build run45113446633/01863ba088770113c401af1be3ed63d006ac745f succeeded. Exact app sourcebe4 plus one-line runtime Dockerfile SHA565356e64b883722e83b2d63502e81ccfe4e1014012b630ac0cf90b760fa30a8. Independent archive review confirms installed zlib1.4-r0 including actual library replacement/whiteout, both original executable bytes, all15 SQL files and 17 migration-directory files. `runtime-zlib14/image-provenance-chain.json` links runner, archive, final registry digestf3aa162b6cd5b28028c1681fb1daadffc73351b5e2785f0c7fb8ebd5a25b9469. Final ECR Basic scan COMPLETE, reported severities empty. Original Critical scan remains present and is not reclassified.

Security lookup sources: https://security.alpinelinux.org/vuln/CVE-2026-4738 and https://pkgs.alpinelinux.org/package/v3.21/main/x86_64/zlib (consulted2026-11-23). Upstream describes contrib/infback9 scope; this audit does not claim actual Labbit exploitability was established. Replacement uses available fixed package rather than silently granting an exception. No upstream PR196 Dockerfile commit was pushed.

The complete local delta includes three preparation documents, the earlier immutable preparation packet, this packet and ignored helper sources snapshotted here. Original preparation packet's manifest records the prior document snapshot; the dated follow-up appended to dr-pr196-trial-preparation.md intentionally supersedes that one document hash. Other earlier packet bytes are unchanged. Current full-delta artifact manifest supersedes previous cross-document hashes, without rewriting historical artifacts.

Independent reviewer audit_pr196_operator reviewed workflow triggers, helper failure/security paths, actual image archive bytes, public key receipt, public Evidence and tests. It made no live AWS/SQL/private-key call. Two reviewer findings were fixed/rechecked before relevant live operations; source/mock acceptance does not replace the parent's actual AWS readbacks. Historical assert-based installer is explicitly not reusable without correction. No unreviewed product code delta is introduced by this packet.

**Judgment:** candidate publication and stated read-only preparation are verified within their scope. No whole-PR readiness, live functional-trial readiness, shared migration/rollout approval or DR completion is claimed. Full app suites were not rerun for unchanged app source; exact candidate build, publisher4 tests, tunnel2 tests/vet, Python8subconditions and shell regression tests cover this delta. Actual cloud publication/scan and read-only DB/metadata were exercised separately. Live approval/Worker/drain/replay/Provider/Fencing/SingleWriter and migration12–15 remain unverified. Existing HOLD/false flags are unchanged.
