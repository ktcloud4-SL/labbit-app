# KT D1 Connector acceptance

This explicitly gated executable runs the **production `labbit-connector` process on the KT Connector VM**, starts a local TLS Mock SaaS, and sends real CC-01 Control messages. It uses the production OP-01 Provider and KT APIs. Ordinary `go test ./...` does not run it or call KT Cloud.

Build both Linux binaries from the same checkout:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o labbit-connector ./cmd/labbit-connector
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o control-harness ./tools/ktcloud-d1-acceptance
```

Prepare an access-restricted directory on the Connector VM with the two executables, `snapshot.json` containing an immutable `protocol.CreationSnapshot`, and protected customer-local Provider/SSH configuration. Supply the OP-01 Runtime Contract environment, including the KT Connector Tier, distinct canonical Lab and Management `/24` CIDRs, and the Connector's actual Management SSH source `/32`. Use a disposable dedicated project or CIDRs with confirmed ownership and no overlap. Do not put credentials or private keys in this directory's evidence exports or Git.

The test requires `LABBIT_KTCLOUD_D1_CONTROL_TEST=1` and an absolute `LBT145_CONTROL_DIRECTORY`. It creates one Lab VM in each of two generations, two Tiers and one management firewall policy per generation, and image-backed root volumes. It verifies HELLO/ACK/Heartbeat, Provider queries, Provision, pinned authenticated Management SSH, Reset from the same snapshot, old-ID absence, known resources and discovered candidates, empty Cleanup protection, final Cleanup and absence of all tracked Lab IDs. The SSH diagnostic checks denied TCP connections to `1.1.1.1:22` and `:443`; those observations alone do not prove denial of every Internet destination. The test includes a small startup script and checks production cloud-init readiness.

```sh
export LABBIT_KTCLOUD_D1_CONTROL_TEST=1
export LBT145_CONTROL_DIRECTORY=/absolute/protected/test-directory
./control-harness
```

For a single VM-use-and-delete check, additionally set `LBT145_CONTROL_GENERATIONS=1`. This creates only generation 1, verifies the same pinned SSH execution and known-resource Reconcile, then cleans its five exact resource references and confirms every one absent. The ledger records `plannedGenerations: 1` and a `RESET_NOT_RUN` final stage. It does not run Reset or the generation-2 candidate/empty-Cleanup checks and cannot satisfy full LBT-145 acceptance. Unset the option or use `2` for the original two-generation sequence. Other values are rejected before fixture registration.

`control-ledger.json` stores resource kinds, exact confirmed IDs, generations and stages without credentials. An existing ledger prevents a new fixture. Failure retains the ledger and attempts Cleanup of confirmed IDs; uncertain results require Reconcile before another mutation. Never delete by name or treat discovered candidates as owned. The harness generates a temporary Mock SaaS credential and test CA locally, trusts that CA through the process certificate store, and keeps normal certificate verification for KT APIs.

For an interrupted fixture, set `LBT145_CONTROL_RECOVERY=1`. Recovery reconciles the exact known IDs before issuing Cleanup and confirms absence afterward. Add `LBT145_CONTROL_RECOVERY_READ_ONLY=1` to observe all ledger IDs without issuing any mutation. Recovery success is **not** a replacement for the full two-generation acceptance result. The separately prepared Connector VM and its installation resources must also be cleaned and reconciled by their own ownership ledger.

Execution evidence and current limitations are in [LBT-145](../../docs/connector/evidence/LBT-145/2026-10-07/README.md). LBT-16 owns joint Backend → CC-01 → OP-01 → KT Cloud C1 acceptance.

For LBT-175's full Preview transport fixture, additionally set
`LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST=1`. This requires a fresh two-generation
journal, with OP-02 and the earlier library-only Preview smoke disabled. The TLS
Mock Gateway authenticates and checks the complete ATTACH binding, then relays
binary HTTP through the production Connector process to a loopback HTTP app in
each actual KT Workspace VM. It checks exact HTTP responses, sequential tunnels,
duplicate OPEN/session isolation, CLOSE/retry, Gateway normal/error closure,
port/target/refused guards, active Reset/Cleanup retirement, final TCP/Data drain
and HTTP/credential exclusion from Control. Recovery must disable this mode.
The fixture does not exercise the production SaaS Gateway, Backend authorization
or Browser. See [LBT-175 execution and handoff](../../docs/connector/evidence/LBT-175/2026-10-19/README.md).
