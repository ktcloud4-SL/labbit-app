param()
$ErrorActionPreference = 'Stop'
$snapshotPath = Join-Path $PSScriptRoot 'snapshot'
# This external snapshot owns the reviewer-only zz fixtures. No product checkout
# or cloud credentials are required. For a relocated copy, rewrite the two
# absolute path entries in retirement-overlay.json before the first command.
Push-Location $snapshotPath
try {
  go test -overlay (Join-Path $PSScriptRoot 'retirement-overlay.json') ./internal/connector/wss -run '^TestIndependentOldTrustRetirementCannotCancelNewOwner$' -count=10 -v
  if ($LASTEXITCODE -ne 0) { throw 'Retirement probe failed' }
  go test ./internal/connector/preview ./internal/connector/wss -run '^(TestIndependentCancelClosesPendingHTTPUpgrade|TestIndependentDirectControlReplacementRetiresOldIdleSession|TestSupervisor_ControlNormalDisconnect_PreservesPreviewManager)$' -count=10 -v
  if ($LASTEXITCODE -ne 0) { throw 'Upgrade/replacement/reconnect probe failed' }
  # -race additionally requires CGO_ENABLED=1 and a working C compiler.
  go test -race ./internal/connector/preview ./internal/connector/wss ./internal/connector/app -count=3
  if ($LASTEXITCODE -ne 0) { throw 'Boundary race run failed' }
} finally { Pop-Location }
