# PR196 candidate publication and non-disruptive preparation — 2026-11-23

Product source: `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`; main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
PR196 remains Draft, unmerged, unchanged. No shared Seoul application/DB/control settings were applied.

| Result | Evidence / scope |
| --- | --- |
| Replacement candidate published | `runtime-zlib14/image-publication-readback.json`; separate IMMUTABLE `backend-dr196-trial`, new trial-only tag. Fixed digest `sha256:f3aa162b6cd5b28028c1681fb1daadffc73351b5e2785f0c7fb8ebd5a25b9469`. |
| Runtime package correction verified | `runtime-zlib14/image-provenance-chain.json`; app sourcebe4 plus exact one-line Dockerfile override565356e6..., zlib1.4-r0. Both binaries and all15 SQL migrations byte-identical to original candidate. This is not an upstream PR196 Dockerfile commit. |
| Build / scan | GitHub run45113446633, workflow01863ba... SUCCESS. `runtime-zlib14/ecr-scan-readback.json`: actual ECR Basic COMPLETE, zero reported findings. This is neither all-scanner acceptance nor live app validation. |
| Initial unsafe candidate retained as evidence | Original digest9dc273... has ECR-reported Critical CVE2026-4738/zlib1.3.2-r1. Never deploy that digest. Original records and first security-build package-check failure are preserved, not overwritten by the replacement success. |
| Protected local keys | `protected-key-preparation.json`; distinct Ed25519 keys, O_EXCL and protected read/public roundtrip. Private key contents excluded. No operational approval/signing/Policy enablement. |
| Real DB ledger read | `schema-tunnel-result.json` and `schema-ledger-comparison.json`; TLS1.3/read-only/ROLLBACK, exact Git checksums1–11 match11/11; 12–15 and five Worker control tables absent. No migration/DML. |
| Session cleanup | Initial missing ACK is still NOT_VERIFIED. Separate termination and `schema-session-history-readback.json` plus final-readonly-observations show exact session Terminated, Active=[]; local port closed. No automatic replay of SQL. |
| Seoul baseline | `final-readonly-observations.json`: owner-linked original Backend Pods2/2, serving sha753eecb; maintenanceopen same UID/RV12249. Initial app=backend selector returned0; that initial query is retained separately and is not a no-Pod claim. |

## Reproduction and evidence boundaries

- `AUDIT.md`: findings reported before fixes, SSOT traceability, tests and live limitations. `independent-review.md`: independent source/archive/mock review; reviewer made no live AWS/DB/private-key call.
- Publisher Go source/dependencies/tests are copied as `.txt`; exact executable SHA is in publisher-source-manifest. Go tests4PASS. Schema tunnel helper source/test snapshots and vet/test2PASS; Python guard tests1top-level/8negative subconditions; shell comparator fake5cases plus independent6cases and installed metadata parser3cases.
- Gitleaks initial exit1/four reviewed false positives and narrow path+exact-match allowlist are retained. Initial scan did not prevent the first authorized publication because PowerShell continued; this orchestration failure is explicitly recorded. Later scans pass before subsequent writes. Do not infer an initial clean gate from a later scan.
- `management-client-prepare.sh.txt` is a historical executed installer snapshot, **not approved for reuse**. It retains assert-based ownership/CA checks; harden and review before any reuse. No credentials or SQL were involved in that preparation step.
- Local helpers depend on Windows AWS CLI/profileadmin3, the exact product Go dependencies, official signed portable SSM plugin and official RDS CA. Tool provenance and source snapshots are included. Credentials, signed download URLs, private key bytes and DSN are excluded. Historical SSM command IDs locate actual command records; do not replay one-shot helpers against a changed target.
- Full app/Frontend regression was not rerun for packaging/documentation-only work. Existing exactbe4 product CI/QA is historical. No drain/committed replay/multiPod durable control/Provider/Fencing/Single Writer/live approval/Resume test occurred.

## Resources and ownership

Owner: seobin. Dedicated candidate ECR now contains the original rejected image and replacement trial image; no lifecycle or existing backend repository policy was changed. Review within7days and remove only after trial/QA and separate cleanup authorization. Build archives expire1day; local archive checksums and reproducible build recipe remain recorded.

Local protected keys remain under the current Windows user's protected task directory; never commit/upload them. Local portable plugin/archive/tool files are temporary. Management EC2 `/var/tmp/labbit-pr196-readonly-20261123` holds only venv/psycopg/CA/task files; no global package/service or IAM permission change. Its later task-directory cleanup must not touch shared management tools. The exact temporary SSM session is terminated.

## Next boundary — no execution authorized by this packet

Before shared migration12–15, candidate rollout, maintenanceclosed or durable Pause/Resume, prepare fresh backup/restore acceptance, current user/work inventory, trusted Policy/SafetyAttestation/key/IAM/RBAC and a Seoul-exclusive trial window. Migration14 adds a validating CHECK lock; earlier successfully committed migrations survive a later migration failure. Stop and inspect the real ledger, never blindly down-migrate/drop tables or rerun UNKNOWN control/provider work.

The image is published, **live_test_ready=false**. No serving Pod rollout, migration, maintenance change, durable control, Provider, DNS/RDS promotion, Slack or merge. Existing ReplicaLag FAIL, four NOT_VERIFIED, DR HOLD, recovery_authorized=false and cloud_verified=false remain.
