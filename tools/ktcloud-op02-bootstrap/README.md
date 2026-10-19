# KT D1 OP-02 fixture

This is a test operator tool, copied from the ten immutable Go source snapshots
in `docs/connector/evidence/LBT-145/2026-10-13-premerge/bootstrap-helper-source/`.
Those snapshots and the LBT-145/LBT-150 evidence remain unchanged. The copy adds
durable unknown-public-IP/forward guards, current File helper deployment,
explicit recovery, and protected failed-run archival. It does not change the
production Connector or define a new Runtime contract.

The tool requires a Windows operator and an empty compute/volume baseline in an
explicit KT D1 project. It creates a disposable Ubuntu bootstrap Connector host,
its root volume/keypair/public IP/forward and three firewall policies (8 owned
resources). Existing DMZ/Private/external tiers remain shared. The Linux acceptance
harness exercises the production Connector on that host and provisions two Lab
generations, with five owned resources per generation. The SaaS Control/Terminal
Data/File Data peer is synthetic, with TLS verification; the Provider APIs, VM,
SSH, PTYs and rooted File helper are real. Browser/Backend acceptance belongs to
LBT-22.

Build from the tested checkout with Go on PATH:

```powershell
$env:LABBIT_EVIDENCE_REPO = (Get-Location).Path
$fixtureDir = Join-Path $env:TEMP ('lbt151-' + [guid]::NewGuid())
$env:LABBIT_EVIDENCE_OUT = Join-Path $fixtureDir 'build'
python docs/connector/evidence/LBT-151/2026-10-14/validate.py build
foreach ($name in @('bootstrap.exe','labbit-connector-ktcloud','lbt145-control-linux','labbit-workspace-sftp')) {
    Copy-Item -LiteralPath (Join-Path $env:LABBIT_EVIDENCE_OUT $name) -Destination (Join-Path $fixtureDir $name)
}
$env:LABBIT_KTCLOUD_D1_BOOTSTRAP = '1'
$env:LBT145_PREMERGE_LIVE_TEST = '1'
$env:LBT145_CONTROL_GENERATIONS = '2'
$env:LBT145_BOOTSTRAP_STRICT_DOCUMENTED = '1'
$env:LABBIT_PROVIDER_CONFIG_FILE = 'absolute-protected-clouds.yaml'
$env:OS_CLOUD = 'ktcloud-d1'
$env:LBT145_BOOTSTRAP_LEDGER = Join-Path $fixtureDir 'ktcloud-bootstrap-ledger.json'
$env:LBT145_BOOTSTRAP_KEY = 'absolute-new-protected-private-key.pem'
$env:LBT145_BOOTSTRAP_FLAVOR = '2x4.itl'
foreach ($phase in @('inventory','create','access','ssh','deploy','control','collect','residual','cleanup','residual','final')) {
    $env:LBT145_BOOTSTRAP_PHASE = $phase
    & (Join-Path $fixtureDir 'bootstrap.exe')
    if ($LASTEXITCODE -ne 0) { throw "Fixture phase failed: $phase; retain ledger and recover exact owned IDs" }
}
```

`clouds.yaml` must contain the `ktcloud-d1` alias, verified D1 identity endpoint,
valid project credentials and CA trust. The chosen Ubuntu 24.04 image must support
the test cloud-init and the flavor's boot disk. This run used image
`os-ubuntu24.04-image-20260929`, flavor `2x4.itl`, 2 CPU / 4 GiB / 50 GiB. The Lab
and management CIDRs are fixture constants `172.25.240.0/24` and
`172.25.241.0/24`; both must be unused. Normalized Provider settings supply the
Connector Tier NSM UUID, its private `/32`, generated Nova keypair and ubuntu user.
The registered key is compared before VM creation. Bootstrap host enrollment is
keyed by exact Server UUID in `lbt145-bootstrap-known_hosts` next to the protected
private key. Provider-owned Lab Ready enrolls each Lab Server pin; Terminal/File
use those pins and private Management NICs. No insecure SSH/TLS override is used.

`deploy` places current binaries and protected credentials on the bootstrap host
under `/home/labbit/lbt145`, verifies all three SHA256s, enables File Runtime keys
and `LABBIT_KTCLOUD_OP02_TEST=1`. The legacy `LBT145_*` variable names preserve
compatibility with the existing tracked acceptance tool. Workspace installation
uses pinned fixture SSH and a root-owned wrapper around the unchanged real helper;
its optional `exec` delay enables cancellation/timeout observation without an
orphaned sleep child. The helper's actual hash/owner/mode are checked per generation.
No production timeout is shortened: SSH idle and PTY grace are 60 seconds, File
request timeout is 30 seconds. A fixture-only SSH setup budget is 40 seconds.

If `control` fails, do not run it again on the same journal or create more resources.
Collect and preserve the failed log/ledger and build manifests. Run
`recover-observe`, which uses the existing protected remote journal and the
production Connector to Reconcile all typed known IDs without mutation. Then
`recover-cleanup` performs Reconcile before requesting deletion of only the
confirmed present IDs, and verifies every recorded ID absent. `collect` and
`residual` retain that result. Unknown candidates are never adopted by name/tag.
The public-IP/forward create-attempt flags survive read-only inspection and restart;
an unknown receipt refuses another create and requires ownership investigation.

To repeat OP-02 on the same bootstrap host after all failed Lab IDs are absent,
`archive-control` collects again, queries actual residuals and refuses any present
Lab ID. It moves the fixed protected Control directory to a timestamped sibling,
preserving its files. Use a fresh local output directory, copy only the bootstrap
ownership ledger and original baseline, deploy fresh binaries, and run `control`.
The bootstrap ownership and key remain the same. Preserve all previous attempt
ledgers separately and query those exact IDs as well at the final check.
Never use `archive-control` to hide a failed acceptance result.

Always collect before bootstrap cleanup. `cleanup` deletes confirmed access IDs,
the exact bootstrap VM/root volume and imported keypair; it inspects actual state
before mutation and verifies absence. `residual` checks typed IDs, and `final`
requires all seven resource inventories to equal the original baseline and the
bootstrap ledger's cleanup flag. A nonzero residual is an incomplete cleanup.
`observe-ssh` is read-only: registered key equality, current protected Lab identity,
actual Server state/key name, Management NIC Tier and firewall match flags. It
does not adopt candidates or attempt SSH/cleanup. Other diagnostic/force-delete
functions copied from the historical source are excluded from the phase allowlist.
Logs and protected inputs stay outside Git; publish
only reviewed lifecycle metadata and hashes.

For the standalone LBT-173 Preview forwarding smoke, set
`LBT173_BOOTSTRAP_PREVIEW_SMOKE=1` before `deploy`. This selects
`LABBIT_KTCLOUD_PREVIEW_SMOKE=1` and disables OP-02. The fresh two-generation
Control lifecycle remains the same. Each generation starts an HTTP fixture on
VM loopback port 3000, then uses the production SSHForwarder and VMAccess library
on the Connector host to check exact HTTP response, shared SSH transport,
PTY/SFTP survival after forwarding close, target/port guards and refused app
classification. Reset verifies old target rejection. Preview Data WSS, Browser
and Backend acceptance are separate. Recovery explicitly clears both test modes
and only observes/cleans recorded owned IDs. No app port is exposed publicly.

For LBT-175, set `LBT175_BOOTSTRAP_PREVIEW_TRANSPORT=1` and leave
`LBT173_BOOTSTRAP_PREVIEW_SMOKE=0` before `deploy`. The modes are exclusive;
deployment selects `LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST=1` and disables OP-02
and the library smoke. This covers the actual production Control/Preview Data
WSS path, unlike the LBT-173 component smoke. Both generations use an application
bound only to VM loopback port 3000. The existing ownership, recovery and cleanup
rules apply; recovery clears all three modes. Keep the bootstrap VM until any
Linux regression checks and `collect` finish, then complete
`residual → cleanup → residual → final`.
See [LBT-175](../../docs/connector/evidence/LBT-175/2026-10-19/README.md).
