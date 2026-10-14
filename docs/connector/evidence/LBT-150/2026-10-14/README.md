# LBT-150 검증 재현

[감사 보고서](AUDIT.md), [실행 요약](validation-summary.json), [tested source](source-manifest.json), [원본 로그](validation-logs.zip), [로그 SHA256 목록](raw-log-manifest.json), [독립 리뷰](independent-review.md).

Implementation source is pinned in source-manifest.json. Later evidence-only commit does not change executed production/tests. Full Go/actual VM source snapshots before/after must match and have clean local status. Earlier Web/Contracts results are reused only after checking their owning blobs unchanged. The final audit records failures and SKIPs separately.

Requirements: Git; Go1.27.1; compatible GCC for Windows `-race`; Python3 with PyYAML6.0.3, jsonschema4.26.0, openapi-spec-validator0.9.0; Node/pnpm matching the tracked web package. Actual inputs include accessible local OpenStack project, Ubuntu image, m1.small, sharednet1 management network, route/SSH security rule and a noninteractive sudo-capable ubuntu SSH key. No credential contents belong in logs or this repository.

From any checkout of the pinned source, configure task-specific variables then run the tracked portable validator. It imports the unchanged tracked LBT148 validator for exact existing contract workflow scripts and make-equivalent checks; no personal untracked helper is required.

```powershell
$validationPython = 'python'
$env:LABBIT_EVIDENCE_REPO = (Get-Location).Path
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt150-auto-' + [guid]::NewGuid())
& $validationPython docs/connector/evidence/LBT-150/2026-10-14/validate.py auto
```

`auto` clears cloud opt-ins, verifies LF-normalized gofmt, runs vet/full Go tests/build, relevant Connector/parser race suites, earlier Terminal/PTY20 repeats, File/parser/app race, rooted SFTP race and File ownership/concurrency/unknown Save20 repeats. It writes raw stdout/stderr, actual exit/count metadata and before/after hashes without rewriting source. The default full suite has15 named test SKIPs:9 OpenStack opt-ins,2 KT Cloud opt-ins and4 Windows filesystem/POSIX-permission platform cases. Eleven packages have no test files. Actual SaaS DB execution is outside this fixture. Final actual Linux VM verifies SFTP symlink rejection.

```powershell
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt150-web-' + [guid]::NewGuid())
# Set LABBIT_EVIDENCE_NODE_BIN and LABBIT_EVIDENCE_PNPM if node/pnpm are not on PATH.
& $validationPython docs/connector/evidence/LBT-150/2026-10-14/validate.py web
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt150-contracts-' + [guid]::NewGuid())
# Set LABBIT_EVIDENCE_VALIDATORS to the directory of pinned Python modules if needed.
& $validationPython docs/connector/evidence/LBT-150/2026-10-14/validate.py contracts
```

Web imports the tracked npm lock into a temporary pnpm lock, installs frozen, runs typecheck/lint/13files167tests/build, checks original package-lock SHA256 and removes only the generated adapter lock. Contracts executes the existing OpenAPI/schema/runtime/migration workflow and reset11 tests.

The live mode creates a disposable VM and seven tracked Provision resources in the explicit local cloud, installs a helper built as Linux/amd64 from the current source, and deletes tracked resources after success or failure. Its production File Data client trusts the generated local TLS CA; no certificate verification bypass is used. Control is a deliberate loopback WS fixture and auth is a generated in-process principal with synthetic token. SaaS DB/Browser/paid KT Cloud are outside this run.

```powershell
$env:OS_CLOUD = 'your-local-test-cloud'
$env:LABBIT_PROVIDER_CONFIG_FILE = 'absolute-path-to-clouds.yaml'
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = 'absolute-path-to-test-private-key'
$env:LABBIT_OPENSTACK_SSH_USERNAME = 'ubuntu'
# Optional environment overrides: LABBIT_OPENSTACK_TEST_IMAGE (ubuntu),
# TEST_FLAVOR (m1.small), TEST_MANAGEMENT_NETWORK (sharednet1),
# TEST_SSH_CIDR (172.16.8.1/32), TEST_LAB_CIDR (198.20.0.0/24).
# Use the full LABBIT_OPENSTACK_ prefix for each TEST_* override.
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt150-live-' + [guid]::NewGuid())
& $validationPython docs/connector/evidence/LBT-150/2026-10-14/validate.py live
$env:LABBIT_EVIDENCE_OUT = Join-Path $env:TEMP ('lbt150-residual-' + [guid]::NewGuid())
& $validationPython docs/connector/evidence/LBT-150/2026-10-14/validate.py residual
```

Live assertions cover Tree/Read/Save/empty/raw Binary/revision/size/symlink/targets, four distinct concurrent Reads, VM helper-started marker before cancellation/timeout/Data revoke, pending/File/SFTP/SSH teardown, same shared PTY transport and actual PTY output afterwards, and Control secrecy. `live` performs read-only discovery before and after and records binary target/hash rather than delivering the binary. Exact tracked IDs are queried absent after cleanup; `residual` additionally lists temporary integration management security group/keypair prefixes and must return0. It does not delete anything and may detect another concurrent integration run using the same prefix.

Independent red/green overlays are archived as reviewer source+logs. Their JSON Replace paths are original run provenance; adapt them to your checkout before re-execution. Tests originating from those overlays are now tracked and run by `auto`. The extra helper/JSON boundary20-repeat command is also archived with its source snapshots; equivalent manual execution:

```powershell
go test -mod=readonly -race -json -count=20 -timeout=2m -run '^(TestInterruptedHelperDrainsSFTPAndPreservesPTY|TestMalformedFramesNeverReachSave)$' ./internal/connector/workspacefile ./internal/connector/filetransport
```

The operator File request/attach/write deadlines, size16-concurrency/16384-ID bounds, failclosed duplicate memory, no-replay unknown Save and setup/SSH blackhole limitations are documented in [Workspace File transport](../../../workspace-file-transport.md). Entire OP02 Reset/Cleanup/CC02 and Browser/SaaS C2 remain LBT151/LBT22; this evidence does not expand their completion boundary.
