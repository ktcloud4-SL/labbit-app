PR #48 / LBT-82 — exact-source acceptance evidence publication

Tested product commit:
2b40992279e16b8593c244c9169df72053a4622c
Tested product tree:
40c8cad13bd5f308b1459a066682870e65e00872
Integrated main:
32ead4d12b0d3f4b56a66c9007273e261dad62ed

This publication adds only this evidence directory after the tested commit.
It does NOT relabel the tests as executions on a later evidence-only commit.
All production/test/contracts/runtime/dependency blobs remain the same as the
339-blob source-manifest in base-validation.zip. The PR comment/body records
the later publication HEAD; remote CI must run on that exact published HEAD.

Contents
- base-validation.zip: unchanged previous exact2b409 source pack, 32 entries.
  Raw Go/race/Web/Contracts/PostgreSQL/real OpenStack lifecycle logs, source
  manifest, commands, cleanup census, reproduction and independent full-diff
  audit. See its README/validation-summary/execution-manifest.
- runtime-validation.zip: unchanged supplementary runtime pack, 19 entries.
  Public production-mode app.Run, actual cloud lifecycle/PTY/reconnect/restart,
  partial-delete recovery, worker/no-script branches, TCP/UDP Internet policy,
  actual Linux POSIX0600 and actual own-token invalidation/reauthentication.
  Includes separately hashed acceptance tooling, raw logs, SSOT snapshots,
  Linux test executable/provenance, findings-first independent audit and
  scoped secret scan. Its ELF is a test artifact, not a product release.
- publication-manifest.json: ZIP bytes/SHA256, entry counts, tested source
  and inner artifact checksum hashes, unchanged-source publication scope.
- .gitattributes: only this directory, prevents line-ending transformations.

Claim boundaries
- Real public app.Run talks to a local TLS Mock SaaS; actual OpenStack APIs,
  disposable VMs and SSH are real. It is NOT deployed SaaS/G1 or browser C2.
- Backend RESET schema/type/inbound/outbound mismatch was resolved in main74.
  Main32ead4d was integrated before running this exact product source.
- Successful2VM generation1+2 refs22, partial-delete fault refs8,
  no-script2VM refs10, worker-failing-input refs11 and Linux refs7 were
  individually confirmed ABSENT after cleanup. Owned keypair/Management SG
  were also absent; server0/network4 matched before/after baseline.
- Worker test observes intended failing-script input plus actual Provider
  failure/resources/cleanup, not directly collected guest cloud-init exit7.
- Token recovery is real own-token revocation, not natural TTL expiry;
  Glance401 is inferred from SDK recovery, not transport-captured.
- Internet checks cover IPv4 TCP443 to1.1.1.1/1.0.0.1 and direct UDP DNS53
  to1.1.1.1. No IPv6 default route was observed, not IPv6 reachability proof.
- No forced ID/IP reuse, global quota exhaustion/credential rotation,
  deliberately ambiguous mutation-result loss or OS-signal wrapper claim.
- Linux0600 ran without skip; Windows ACL is not verified. Credential test
  storage stays Fake, separately from real PostgreSQL integration in base pack.
- POSIX/reauth raw logs did not self-report helper-source hashes at execution.
  Final packaging hashes support reproduction, not retroactive execution proof.
- Raw PTY is intentionally not persisted. Actual deployed SaaS needs caller
  endpoint/credential file. Browser Terminal/durable Worker are incomplete in
  current main. CI success must not be represented as actual cloud E2E.
- Both inner README/audit files preserve their historical execution-local,
  not-yet-published language; this outer file records their later publication.
  Earlier exclusions in the base pack were tested selectively in the runtime
  supplement; neither is silently rewritten.
- Prior Web dev-HTTP smoke remains attestation-only; no new raw proof is claimed.
- Scoped secret scans and reviewer checks are not universal DLP guarantees.

Reproduction
1. Obtain this evidence directory at the published PR HEAD; verify ZIP hashes.
2. Extract the base ZIP into a separate evidence folder and retain its exact
   source manifest/raw logs/reproduce.ps1. Save scripts outside checkout as
   described there before checking out clean tested product2b409922.
3. Extract runtime ZIP into
   .local/evidence/LBT-82-runtime-20261006
   under that clean source checkout. The directory is ignored by Git.
4. Follow runtime README and runners, adapting only caller-local config/key
   paths and environment-specific image/flavor/network inputs in copied tools.
   Preserve original hashes and record any adaptations separately.
5. Configure reachable isolated OpenStack, Go1.27.1 and GCC/CGO1 for race.
   Run cloud mutations sequentially. Test credentials are disposable generated
   fixtures, not deployable credentials; never publish customer secret values.
6. Each actual cloud target deletes only its exact owned IDs. If FAILED/UNKNOWN
   and cleanup is incomplete, inspect owned resources before retry; do not
   adopt/delete arbitrary tagged resources.
7. PostgreSQL revalidation uses isolated PostgreSQL16 with CREATE DATABASE
   permission, not shared/customer data; follow base reproduce.ps1.

Remote CI and final team review are separate from these local results.
No main push, merge or auto-merge is authorized by this evidence publication.
