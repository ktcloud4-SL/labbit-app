# LBT-145 — KT Cloud D1 compatibility analysis and execution record

Date: 2026-10-07 KST. Inspected source revision: `a60d6ce53700bf5724e1a43a73bb5eb595c71933` (fetched `origin/main`).

Jira: [LBT-145](https://samsunglions.atlassian.net/browse/LBT-145).
Scope: execute CC-01 + OP-01 from a KT Cloud Connector VM and verify a separate Lab VM lifecycle. Joint Backend → CC-01 → OP-01 acceptance remains [LBT-16](https://samsunglions.atlassian.net/browse/LBT-16).

## Existing evidence

The project reference `KT_CLOUD_D1_VERIFICATION_RESULTS.md`, dated 2026-10-07, records user-run Gophercloud v2.15.0 checks against explicit KT D1 endpoints:

- `POST /d1/identity/auth/tokens`: HTTP 201, selected project scope confirmed.
- VM, Flavor and Image list calls: succeeded.
- Catalog: identity, compute, network, image, volumev3 and sharev2 entries present. Catalog endpoint reachability was not tested.
- `GET /d1/nsm/v1/network`: KT Tier list succeeded. DMZ and Private have different `networkId` and `refId`; external has an empty `refId`.

These are prior diagnostic results, not proof that this revision of OP-01 works on KT Cloud. No conclusion about standard Neutron mutation support follows from Tier list success. Credential values are absent from this record.

## Initial KT Cloud compatibility analysis (before probes)

1. Reusable: CC-01 Control handshake, heartbeat, dispatch, Provider interface, immutable CreationSnapshot, generation, Reset/Cleanup ordering, Reconcile semantics, and Gophercloud query mapping already exercised by the prior diagnostic.
2. Needs real verification: unchanged product authentication/version discovery, catalog endpoint reachability, Network/Subnet/Router/Port/SG/quota APIs, Server create/delete, management NIC/IP, keypair, authenticated SSH and cloud-init.
3. Confirmed KT-specific interface: existing Tier listing uses `/d1/nsm/v1/network`. The production lifecycle's need for a Tier adapter is still unconfirmed.
4. Undetermined: usable Neutron endpoint and permissions, VM attachment ID semantics, disposable topology support, Connector host and Lab VM SSH path.
5. Smallest candidate change: supply KT-specific local configuration first. Only after an observed failure, change the failing endpoint initialization or API boundary. Do not copy the full Provider or replace lifecycle orchestration.
6. Order: unchanged initializer → read-only discovery/catalog/Neutron → disposable Network/Subnet with confirmed cleanup → dedicated Connector VM → actual Control WSS commands → Provision/SSH/Reset/Reconcile/Cleanup → final resource inventory.

## Call sites at the inspected revision

Paths below are relative to the repository root.

| Functionality | Source and external call |
| --- | --- |
| Runtime Provider construction | `internal/connector/app/app.go`: `Run` creates lazy `openstackprovider.New(ConfigFromEnvironment())`; `runtime_provider.go` forwards Provider operations |
| HELLO / HELLO_ACK / Heartbeat | `internal/connector/wss/client.go`, `reconnect.go`, `internal/connector/heartbeat/loop.go` |
| OPERATION_COMMAND dispatch | `internal/connector/wss/handler.go`: `handleOperationCommand` → `internal/connector/provider/operation.go`: `DispatchOperation` |
| Authentication | `internal/connector/provider/openstack/client.go`: `clouds.Parse`, `NewProviderClient`, catalog clients; `ValidateConnection` uses Keystone token validation |
| Image | `query.go`: Glance `images.List`; `provision.go`: image lookup/preflight |
| Flavor | `query.go`: Nova `flavors.ListDetail`; `provision.go` and `quota.go`: flavor lookup/preflight |
| Network | `query.go`: Neutron `networks.List`; `network.go`: `EnsureNetwork` list/create |
| Subnet | `query.go`: Neutron `subnets.List`; `network.go`: `EnsureSubnet` list/create |
| Router | `router.go`: list/create, gateway and interface operations |
| Port | `port.go`: Neutron list/create/get; `server.go`: attached-port lookup |
| Security Group / Rule | `network.go`: list/create and rule handling; `query.go`: SG discovery |
| Quota | `quota.go`: Nova and Neutron detailed quota preflight |
| Server create / query / ACTIVE | `server.go`: Nova `servers.Create/List/Get`, ACTIVE polling; keypair extension |
| Management IP | `server.go`: management port `FixedIPs`; `terminal_target.go`: server metadata and management NIC lookup |
| Provision | `provision.go`: preflight → topology → ports → server → ACTIVE → SSH |
| Reset | `lifecycle.go`: validate older resources → cleanup → Provision from the immutable snapshot |
| Reconcile | `reconciliation.go`: real GET/list per resource, confirmed 404 → ABSENT, discovery candidates |
| Cleanup | `lifecycle.go`: dependency-ordered delete and confirmation, router interface removal |
| SSH Ready | `server.go`: `waitSSHReady`; `startup.go`: authenticated SSH exec `true`, host key trust and optional cloud-init completion |

## Execution results and current blocker

**LBT-145 remains IN_PROGRESS. Full Reset/Cleanup acceptance and residual 0 have not passed.** The final known residual is two test-owned NSM Tiers. The dedicated Connector VM and all its installation resources have been cleaned; no test VM or boot volume remains.

Source provenance:

- Analysis baseline: `a60d6ce53700bf5724e1a43a73bb5eb595c71933`, fetched latest main at the start.
- Authentication/Gateway patch: `fa158f30c63e18ef44b293aa84a2be5877ff955a`.
- NSM topology, ownership, quota and Compute compatibility: `9796e49ab39577a4537b6017328a1e8a0351e86d`.
- Subsequent observed Cleanup fixes: `afcd4ad31a40017bfb9bcd820373db60165edbed`.
- The first successful WSS Provision used the evolving compatibility worktree based on `fa158f3`; its exact binary hash was not retained. It is evidence for that candidate's behavior, not full acceptance of the final revision.
- A Linux amd64 production Connector built at `9796e49` was deployed to the actual KT D1 / DX-M1 Connector VM through pinned authenticated SSH. The deployed binary SHA256 was verified against the local binary: `1d194d8e99f08bef17c3081ce28b1063d98472c105b3011d841f23619681d5f5`. Harness SHA256: `c3509dd7812734829bd155010df025776c5baa180ac245da1ba6b9559c5faca9`. That run verifies current Control and read-only Reconcile, and does not rerun Provision/Reset. The later Cleanup fix has unit regression coverage and actual local Provider Cleanup observations; it has not completed two-generation WSS acceptance on the KT host.

### Actual KT Connector and product Control path

The dedicated Connector host was a separate Linux amd64 VM on the existing shared DMZ Tier. The Lab VM used two newly created, tracked Tiers. Production `labbit-connector` ran on that KT host against a TLS Mock SaaS and the real OP-01 Provider:

```text
HELLO -> HELLO_ACK -> Heartbeat
-> OPERATION_COMMAND -> CC-01 Dispatcher -> Provider Interface
-> existing OP-01 -> KT D1 -> OPERATION_RESULT
```

- Real WSS authentication/token validation, Image list (12) and Flavor list (235): PASS.
- Real WSS Provision generation 1: SUCCEEDED, with five actual resource IDs: Lab Tier, VM Management Tier, management SSH policy, Server and Root Volume.
- VM ACTIVE, Management IP from the expected fixed Management NIC, key-authenticated SSH and startup cloud-init readiness: PASS through production Provision. The NSM Tier UUID was tracked as `providerId`; the distinct physical `refId` was used for Nova NIC attachment. A Lab IP fallback was not used.
- WSS Reconcile generation 1: five known resources present, PASS.
- Reset generation 2: FAILED at read-only quota preflight. KT's Nova detail returns extended flavor fields (`original_name`, `vcpus`, `ram`) without `flavor.id`. The adapter now credits actual observed capacity; regression tests cover valid SDK JSON numbers and reject incomplete capacity. The corrected full Reset has not been rerun successfully.
- First Cleanup: UNKNOWN after ordinary Nova DELETE soft-deleted the VM while preserving its attached root volume. The exact server GET returned 400, default inventory excluded it, and the volume remained in-use. Inventory absence alone was not accepted as deletion proof.
- A separately reconciled, one-time Gophercloud `forceDelete` of the exact known Lab VM was accepted. Subsequent exact Server and Root Volume GETs returned actual 404. The Lab SSH firewall policy is absent from the complete NSM policy inventory.
- At `9796e49`, a fresh production Connector WSS HELLO/ACK/Heartbeat and queries passed again. Read-only WSS Reconcile confirmed all five known IDs: **two Tiers present, three other IDs ABSENT**, without any mutation. See [control-readonly-reconcile.log](control-readonly-reconcile.log).

The explicitly gated, reviewable [acceptance harness](../../../../../tools/ktcloud-d1-acceptance/README.md) runs a production process and preserves exact resource IDs before evaluating results. Its full path also verifies changed Reset targets, old known-ID absence, discovered-candidate protection and final Cleanup, but those later assertions have **not** passed on KT yet. The newly added external TCP denial diagnostic was not executed in the successful first Provision run; no Internet-isolation acceptance claim follows from it.

### Minimal compatibility boundaries and actual probes

- Gophercloud v2.15.0 remains responsible for token operations, locking/bounded refresh, one retry after definitive 401, Compute/Image query mapping, Server actions, quota and Cinder mapping. Generic OpenStack construction remains unchanged.
- Version discovery GET `/d1/identity/` returned 500. Catalog Nova/Glance/Neutron URLs returned 404. Explicit D1 Gateway token POST/HEAD and Compute/Image routes passed. See [gateway-probe.json](gateway-probe.json) and [op01-readonly.log](op01-readonly.log).
- Standard Neutron root/version and Network/Subnet reads returned 404, so disposable Neutron mutations were not issued.
- Before any VM creation, disposable NSM Tier/Firewall create/list/job/delete probes passed and restored Tier 3 -> 3 and policy 0 -> 0. See [network-adapter-integration.log](network-adapter-integration.log). This result does not override the later Tier delete failure.
- NSM handles only the confirmed Network/Firewall incompatibility inside the existing Adapter. Resource kinds are `KT_TIER`, `KT_FIREWALL_POLICY`, `SERVER`, `VOLUME`; no fictitious Neutron Port/Subnet/SG IDs are emitted. The common Provision/Reset/SSH/CC-01 orchestration is reused.
- Nova boot-from-image uses an actual 50 GiB image-backed root volume, `delete_on_termination=true`, omitted empty `imageRef`, string boot index and physical Tier refs. The available image/flavor combination actually accepted was Ubuntu 24.04 and `2x4.itl` (2 vCPU / 4 GiB).
- Cleanup now waits for observed soft-delete convergence before one `forceDelete`, requires actual Server 404, and checks actual Root Volume existence before deleting it. Already absent boot volumes are not sent another DELETE: KT returned an unconfirmed delete result for a root already absent after server purge. Failures remain UNKNOWN until reconciliation.
- Known-ID ownership, different generations, invalid resource kinds, missing/ambiguous Management IP, malformed HTTP 200 fault envelopes, contradictory delete 404, and no blind mutation retry have httptest regression coverage. Default tests never call KT.

### Remaining NSM Tier deletion inconsistency

Both exact owned Tier IDs are ACTIVE in the complete NSM list and each ID-filtered query returns one exact match in the selected token project. The documented DELETE with `networkId` returns HTTP 404 with the safe classification `network could not found`. The Lab physical ref and the legacy path also returned 404. Neither ref IDs nor 404 receipts were substituted for actual resource absence.

After all project test VMs, boot volumes and firewall policies were removed, a fresh exact-ID reconciliation and one documented Tier delete attempt still returned 404. No further speculative delete variants or blind retries were issued. The actual cause is unresolved; this is a contradiction between observed list and delete responses, not a proven diagnosis of KT's internal implementation. The existing KT portal tab requires user re-login after session expiry to compare the visible Tier state and deletion behavior.

| Test-owned remaining Tier | NSM networkId | CIDR |
| --- | --- | --- |
| `labbit-lbt145-control-df3cb3919-g1-bf33bc516a-lab-tier` | `d1c45e11-f00b-4ec1-9f92-2716c2afa215` | `172.25.240.0/24` |
| `labbit-lbt145-control-df3cb3919-g1-bf33bc516a-workspace-management-tier` | `1dba8bfa-0cf0-494a-813b-35770b1e5c6c` | `172.25.241.0/24` |

These IDs were confirmed from creation results and the private ownership ledger; names alone are not deletion authority. Shared DMZ, Private and external Tier IDs were preserved. Do not create another fixture in these CIDRs until both Tiers have confirmed absence.

### Bootstrap Cleanup and residual verification

The temporary Connector VM's dedicated firewall policies (3), public IP, TCP 22 port-forward, VM, image-backed root volume and imported keypair were all removed. The first soft-delete cleanup returned UNKNOWN; exact-ID observations preceded recovery, and actual Server/Volume 404 was required. Final production Adapter Cleanup and keypair absence checks succeeded. See [bootstrap-cleanup.log](bootstrap-cleanup.log).

The final resource report at **2026-10-07 19:57:45 KST** has all tracked Lab and bootstrap kinds at 0 except **LAB_KT_TIER = 2**. Total owned residual = **2**. Project Tier inventory = 5, comprising the unchanged shared baseline 3 plus these two owned Tiers. See [residual-current.json](residual-current.json). This is a failed residual-0 acceptance, not a completed cleanup.

Protected Provider configuration and local SSH files remain outside Git. The destroyed Connector disk held its protected deployed configuration. No password, token, Connector credential, SSH private key, Authorization header or raw sensitive Provider payload is exported in these files.

### Validation

- Final source: Go vet, `go test ./...`, `go build ./...`: PASS. Real cloud gates were unset. The new harness package is buildable but has no automatically executed cloud tests.
- Go formatting: PASS for 308 tracked Go files after canonical LF normalization, equivalent to Linux checkout formatting. Windows baseline CRLF files were not committed as mass formatting edits.
- Web typecheck/lint/test/production build: PASS; 9 test files / 109 tests. Existing dependencies match the repository lockfile. Bundled Node/pnpm were used; `make setup` was not executed literally.
- Contracts: OpenAPI, all eight Draft 2020-12 schemas and local refs, Runtime YAML and unique Migration prefixes: PASS using the workflow's pinned validator versions.

### KT Cloud D1 Result

```text
Gophercloud Identity: actual OP-01 token authentication/validation PASS
Gophercloud Compute: queries and first WSS VM create/ACTIVE PASS; SDK forceDelete confirmed
Gophercloud Image: actual OP-01 Image list and selected image preflight PASS
Gophercloud Neutron: standard documented/catalog Network/Subnet reads FAIL (404)
KT Tier API: create/list/Nova ref attachment PASS; current owned Tier delete BLOCKED (404 while present)
VM Create: first real WSS Provision SUCCEEDED
VM Delete: Lab and temporary Connector exact Server GET 404 confirmed
Management IP: expected fixed Management NIC resolved PASS
SSH: actual Connector-to-Lab key authentication and cloud-init PASS
Reset: first run FAILED; extended-flavor fix unit-tested, full rerun pending
Reconcile: actual WSS known-ID present/absent checks PASS; complete candidate scenarios pending
Cleanup: Server/Volume/Firewall/bootstrap resources absent; final Tier cleanup BLOCKED
Residual Resource: 2 test-owned Tiers; all other tracked cloud resources 0
Required KT-specific Adapter: Identity/Gateway initialization, NSM Tier/Firewall, Nova boot/detail/delete boundaries
Remaining limitation: unresolved Tier delete; single Linux VM, InternetOutbound=false; NSM/Cinder quota APIs unavailable; full acceptance pending
LBT-145 readiness: IN_PROGRESS / completion criteria unmet
C1 readiness: NOT_ASSESSED; joint acceptance belongs to LBT-16
```

## Official API references

- [KT D1 authentication and service Gateway](https://cloud.kt.com/docs/open-api-guide/d/guide/how-to-use)
- [KT Tier create/list/delete](https://cloud.kt.com/docs/open-api-guide/d/computing/tier)
- [KT networking/firewall and asynchronous jobs](https://cloud.kt.com/docs/open-api-guide/d/computing/networking)
- [Network API changes and VM attachment ID](https://cloud.kt.com/docs/open-api-guide/d/nsm/how-to-use): documents `networks.uuid` as Tier `refId`. Actual Nova VM attachment using the distinct physical ref was subsequently confirmed.

The current Connector contract requires isolated Lab traffic and Connector-only TCP 22 on Management NICs. NSM Tier/firewall policies cannot be silently reported as Neutron Subnet/Port/Security Group resources. The implemented limited profile tracks actual provider IDs using the contract's extensible resource types. Full lifecycle and isolation acceptance remain subject to the limitations above.
