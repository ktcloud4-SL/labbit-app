param(
    [string]$Repository = (Resolve-Path (Join-Path $PSScriptRoot '../../../../../..')).Path,
    [ValidateSet('control','fault')][string]$Mode = 'fault',
    [switch]$MutateEOFBudget,
    [string]$OutputDirectory = (Join-Path ([System.IO.Path]::GetTempPath()) ('lbt173-independent-review-' + [guid]::NewGuid().ToString('N')))
)
$ErrorActionPreference = 'Stop'
$Repository = (Resolve-Path -LiteralPath $Repository).Path
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path
$probe = Join-Path $OutputDirectory 'review_close_test.go'
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'combined-probe_test.go.txt') -Destination $probe
$virtualProbe = Join-Path $Repository 'internal/connector/vmaccess/zz_review_close_test.go'
$replace = @{}
$replace[$virtualProbe] = $probe
$overlay = Join-Path $OutputDirectory 'overlay.json'
[System.IO.File]::WriteAllText($overlay, (@{Replace=$replace} | ConvertTo-Json -Depth 4), (New-Object System.Text.UTF8Encoding($false)))
$testArguments = @('test','-overlay',$overlay,'./internal/connector/vmaccess','-run','^TestReview(TCPReadUnblocksAfterCloseWithResponsiveSibling|CanceledLateTCPRespectsTransportBudget|EOFWithoutCloseRetainsTransportBudget)$','-count=1','-timeout=15s','-v')
if ($Mode -eq 'fault') {
    # Dependency source stays untouched. This copy is a deliberately faulty
    # SERVER peer: only incoming direct-tcpip CLOSE is ignored; PTY still works.
    Push-Location -LiteralPath $Repository
    try { $moduleCache = (& go env GOMODCACHE).Trim() } finally { Pop-Location }
    $upstreamModule = Join-Path $moduleCache 'golang.org/x/crypto@v0.57.0'
    if (-not (Test-Path -LiteralPath $upstreamModule -PathType Container)) {
        throw 'golang.org/x/crypto v0.57.0 must already be available in GOMODCACHE; run the repository setup first.'
    }
    $peerModule = Join-Path $OutputDirectory 'crypto-peer'
    Copy-Item -LiteralPath $upstreamModule -Destination $peerModule -Recurse
    $peerChannel = Join-Path $peerModule 'ssh/channel.go'
    (Get-Item -LiteralPath $peerChannel).IsReadOnly = $false
    $source = Get-Content -LiteralPath $peerChannel -Raw
    $needle = "case msgChannelClose:`n"
    if (-not $source.Contains($needle)) { throw 'Expected upstream peer patch point was not found' }
    $patch = "case msgChannelClose:`n        // Independent review fixture: peer ignores only TCP CLOSE.`n        if ch.direction == channelInbound && ch.chanType == `"direct-tcpip`" { return nil }`n"
    [System.IO.File]::WriteAllText($peerChannel, $source.Replace($needle,$patch), (New-Object System.Text.UTF8Encoding($false)))
    $modfile = Join-Path $OutputDirectory 'fault.mod'
    Copy-Item -LiteralPath (Join-Path $Repository 'go.mod') -Destination $modfile
    Copy-Item -LiteralPath (Join-Path $Repository 'go.sum') -Destination (Join-Path $OutputDirectory 'fault.sum')
    Add-Content -LiteralPath $modfile -Value ("`nreplace golang.org/x/crypto => " + '"' + $peerModule.Replace('\','/') + '"')
    $testArguments = @('test','-modfile',$modfile) + $testArguments[1..($testArguments.Length-1)]
}
if ($MutateEOFBudget) {
    # Deliberate one-line mutation; never modifies the repository source.
    $tcpPath = Join-Path $Repository 'internal/connector/vmaccess/tcp.go'
    $tcpSource = Get-Content -LiteralPath $tcpPath -Raw
    $waitForClose = 'pumps.Wait(); <-closed; m.releaseTCP(c)'
    if (-not $tcpSource.Contains($waitForClose)) { throw 'EOF budget mutation point was not found' }
    $mutantPath = Join-Path $OutputDirectory 'eof-budget-mutant.go'
    [System.IO.File]::WriteAllText($mutantPath, $tcpSource.Replace($waitForClose,'pumps.Wait(); m.releaseTCP(c)'), (New-Object System.Text.UTF8Encoding($false)))
    $replace[$tcpPath] = $mutantPath
    [System.IO.File]::WriteAllText($overlay, (@{Replace=$replace} | ConvertTo-Json -Depth 4), (New-Object System.Text.UTF8Encoding($false)))
}
Push-Location -LiteralPath $Repository
try {
    & go @testArguments 2>&1 | Tee-Object -FilePath (Join-Path $OutputDirectory ($Mode + '.log'))
    $testExitCode = $LASTEXITCODE
} finally { Pop-Location }
Write-Host ('External fixture and log: ' + $OutputDirectory)
exit $testExitCode
