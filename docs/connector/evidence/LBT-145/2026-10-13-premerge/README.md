# LBT-158 / LBT-145 — latest-main KT D1 acceptance

Date: 2026-10-13 KST. Tested production source: `6a6fb4f97e676a80cfce27f85269f0627970fc9c`, based on fetched main `4501f447d7a8c527b17ff2e08ee1d2d2fd88a8aa`. Branch: `fix/LBT-158-ktcloud-d1-verification`.

This run verifies the KT D1 compatibility changes using a separate actual KT Connector VM and production `labbit-connector`, CC-01 dispatch/results, OP-01 and actual KT APIs. The SaaS peer is a locally generated TLS Mock SaaS. It does **not** claim Backend/Browser C1 acceptance, SFTP/PTY acceptance or standard local OpenStack live testing.

## Results

| Check | Actual observation |
| --- | --- |
| Explicit KT readonly gate | New/ValidateConnection/SDK queries PASS; servers0/images12/flavors235, no lifecycle mutation in this test |
| Explicit disposable NSM gate | 46-character Tier create/list/job/firewall/delete PASS, UUID/refId distinct, fixture residual0; inventory3→3 and policies0→0 |
| Production Control | HELLO/HELLO_ACK/heartbeat and connection/image/flavor queries PASS |
| Generation1 | Provision SUCCEEDED, five confirmed IDs, expected Management NIC, pinned key-authenticated SSH login and command execution PASS, known-ID Reconcile PASS |
| Reset→Generation2 | Reset SUCCEEDED, same immutable snapshot, old5 absent/new5 present; new Management target and pinned SSH command execution PASS |
| Ownership guards | Empty Cleanup kept generation2, discovered candidate was not adopted or auto-deleted; negative ownership faults also exercised by Fake regressions |
| Final Lab Cleanup | SUCCEEDED; all ten generation1/2 typed resource IDs observed absent |
| Bootstrap cleanup and total baseline | PASS; bootstrap8 exact IDs absent, disposable NSM fixture absent, owned residual0; full seven-kind baseline exactly restored at `2026-10-12T17:12:03.5163046Z` |

The generation2 replacement is created only after generation1 cleanup, as implemented by the unchanged lifecycle order. There was one disposable Connector VM and sequential Lab VMs. The standalone NSM fixture created no VM. Ubuntu24.04 / `2x4.itl` (2vCPU/4GiB), 50GiB image-backed root volume, D1/DX-M1 were used. Basic Tiers are **DMZ and Private (two)**; the NSM list also has a separate external entry, so the baseline network-entry count is three. These baseline entries are shared and are never fixture cleanup targets.

`InternetOutbound=false` diagnostics deny TCP to `1.1.1.1:22` and `:443` from each Lab VM. These two probes do not prove denial of every external protocol/destination. Early readiness retries can occur while the VM boots and PAM/SSH initializes; this is not classified as a new SSH defect. No fixed60-second wait, retry/timeout change, SSH Ready feature or PAM policy was added.

## Provenance and raw records

- [Execution source and binary hashes frozen before creation](tested-source-before-live.json): 348 source/contract/runtime/rule files, CRLF normalized to LF.
- [Post-execution source comparison](tested-source-after-live.json): the same source projection; doc-only final commits are distinguished from the actual tested commit.
- [Go/Web execution summaries and raw logs](checks/summary.json), [KT readonly](kt-readonly.log), [NSM gate](kt-network.log), [network ownership ledger](network-ledger.json).
- `live/` contains lifecycle/bootstrap phase logs, safe exact-ID ledgers, production Connector log, final read-only residual and baseline comparison. Protected config, keys, auth header values and raw Provider failure payloads are excluded.
- [Full findings-first audit and traceability](PRE_MERGE_AUDIT_RESULT.md), [independent review](INDEPENDENT_REVIEW.md), [preservation comparison](preservation-check.json), final candidate file list and artifact hashes.

Artifact manifest `sha256/bytes` describe Git-published LF text; `originalWorktreeSHA256/Bytes` retain the pre-Git raw copies. The two intentional whitespace observations in unedited tool output (Vite reporter trailing space and Vitest final blank line) are preserved as raw execution evidence; source/contract whitespace checks are separate.

Deployed production Connector SHA256: `6e86adbbe49e1e39ac9c8eea4d76dfbbfff52b11940fb5ae8728ce1694ef090d`.
Deployed tracked acceptance harness SHA256: `2eaf38ce1de1fb6d450e334316f5c848b28b02caf238c5adfde96fbfcc81cdb6`.
Both were built from the tested source and their actual remote digests were verified over pinned authenticated SSH. `vcs.modified=true` in build information reflects the working checkout including existing untracked Evidence; this is not represented as a clean-source build. The frozen projection/digests identify the run; later Evidence commits do not guarantee byte-identical builds with changed VCS metadata.

## Reproduce

1. Checkout the PR commit and verify its source projection against the frozen manifest. Use the Go version in `go.mod`, `npm ci --prefix web` and the repository's default Go/Web/Contracts checks. Keep real KT integration gates unset for default checks.
2. Prepare customer-local protected `clouds.yaml`, the selected D1/DX-M1 project, image/flavor and ed25519 SSH key. Use ordinary KT TLS verification and pinned SSH host keys; never commit or export these values.
3. For readonly SDK acceptance, explicitly set `LABBIT_KTCLOUD_D1_INTEGRATION=1`, `LABBIT_PROVIDER_CONFIG_FILE` and `OS_CLOUD`, then run `TestKTCloudD1ReadOnlyIntegration` in the Provider package.
4. For the standalone NSM fixture, set `LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST=1`, a fresh absolute `LABBIT_KTCLOUD_TEST_LEDGER`, unused `LABBIT_KTCLOUD_TEST_TIER_CIDR=/24` and actual Connector DMZ `LABBIT_KTCLOUD_TEST_SOURCE_CIDR=/32`. Run only `TestKTCloudD1DisposableNetworkIntegration`. A pre-existing ledger fails before cloud access. Unknown creation/job/mapping stops automatic candidate adoption; preserve the ledger for manual exact-ID investigation.
5. Build the Linux production Connector and tracked [acceptance harness](../../../../../tools/ktcloud-d1-acceptance/README.md) from the same checkout. Run the original two-generation mode on an actual customer Connector host. The tool README documents the immutable `snapshot.json`, distinct Lab/Management CIDRs, Connector source `/32`, protected local config and explicit gates.
6. The exact fresh bootstrap helper source used for this disposable Connector is exported in `bootstrap-helper-source/*.go.txt`. Copy the Go files into this checkout's `.local/kt-repro-source/`, removing only the `.txt` suffix, and build from the repository root with `go build -o .local/kt-repro-live/bootstrap.exe ./.local/kt-repro-source`. Use the repository's existing `go.mod`; a separate external module cannot import its internal Provider/Protocol packages. Export-copy compilation was actually checked without cloud calls (`checks/helper-export-build.log`). Put the two Linux binaries and a copy of `run-phase.ps1.txt` as `run-phase.ps1` beside bootstrap.exe. The wrapper records the phase input names and absolute local locations; substitute your own protected local paths outside the repository. Run inventory→create→access→ssh→deploy→control→collect→residual→cleanup→residual→final in order; stop if observed ownership/deletion is uncertain. The local helper supports only the declared phases and has independent exact-ID/bootstrap ledgers.
7. Collect lifecycle logs **before** removing the Connector. Confirm Lab typed IDs absent, delete only confirmed bootstrap IDs, then compare complete Server/Volume/Tier/Firewall/PublicIP/PortForward/Keypair inventories with the saved baseline. Keep the pre-cleanup residual observation too: eight temporary bootstrap resources still existed at that intermediate point.

Never substitute recovery success or a single-generation run for this two-generation acceptance. Control/fixture ledger writes are not crash-atomic transactions and interrupted/ID-less mutations require reconciliation/manual investigation. Host credentials and provisioning permissions are environmental prerequisites, not bypasses built into production.

## Scope of automatic and skipped checks

Fresh full Go vet/test/build and formatting PASS; 991 top-level tests, 2966 PASS events. Targeted Provider/harness race PASS;124 top-level,317 PASS events. Web13files/163tests and typecheck/lint/build PASS. OpenAPI, eight JSON schemas/local refs and Runtime/migration metadata PASS.

Default Go's seven live skips are standard OpenStack5 and KT2. The KT2 were explicitly run separately and passed. Standard local OpenStack5 stay unrun under the user's instruction. Two Windows ledger attack subcases and one existing POSIX credential-permission subcase are OS-specific skips, separate from those seven; these branches will be checked in Linux PR CI. PostgreSQL integration is a separate CI service test, not claimed from local default Go PASS.

Historical October7 errors remain in the older record. Later October9/12 experiments and the selectively reverted SSH Ready work remain preserved locally; this fresh execution is the proof for the current implementation. No main push/merge, deployment, Jira write or external support message is part of this run.
