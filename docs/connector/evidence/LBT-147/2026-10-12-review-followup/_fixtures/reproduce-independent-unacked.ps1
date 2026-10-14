param(
    [Parameter(Mandatory=$true)][string]$RepoPath,
    [string]$OutputDirectory,
    [switch]$PrepareOnly
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath $RepoPath).Path
$fixtureRoot = $PSScriptRoot
$utf8 = New-Object System.Text.UTF8Encoding($false)
function Write-Utf8([string]$Path, [string]$Value) {
    [IO.File]::WriteAllText($Path, $Value, $utf8)
}
function Assert-Hash([string]$Path, [string]$Expected) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Missing pinned file: $Path" }
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Expected) {
        throw "Pinned byte mismatch: $Path"
    }
}
$fixtureManifest = Get-Content -LiteralPath (Join-Path $fixtureRoot 'dependency-fixture-manifest.json') -Raw | ConvertFrom-Json
foreach ($file in $fixtureManifest.fixtures) {
    Assert-Hash (Join-Path $fixtureRoot $file.path) $file.sha256
}
$sourceManifest = Get-Content -LiteralPath (Join-Path $fixtureRoot 'reviewer-executed-source-manifest.json') -Raw | ConvertFrom-Json
foreach ($file in $sourceManifest.files) {
    Assert-Hash (Join-Path $repoRoot $file.path) $file.sha256Bytes
}
$expectedGo = @($sourceManifest.files | Where-Object { $_.path -match '^internal/connector/vmaccess/[^/]+\.go$' } | ForEach-Object { [IO.Path]::GetFileName($_.path) } | Sort-Object)
$actualGo = @(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'internal/connector/vmaccess') -File -Force | Where-Object { $_.Extension -eq '.go' } | ForEach-Object { $_.Name } | Sort-Object)
if (@(Compare-Object $expectedGo $actualGo).Count -ne 0) { throw 'Unexpected or missing VM access Go source (including ignored files).' }
Assert-Hash (Join-Path $repoRoot 'go.mod') $fixtureManifest.repoGoModSHA256
Assert-Hash (Join-Path $repoRoot 'go.sum') $fixtureManifest.repoGoSumSHA256
$moduleCache = (& go env GOMODCACHE | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { throw 'go env GOMODCACHE failed' }
$installedCrypto = Join-Path $moduleCache 'golang.org/x/crypto@v0.57.0'
if (-not (Test-Path -LiteralPath $installedCrypto -PathType Container)) {
    throw 'Run go mod download golang.org/x/crypto@v0.57.0 first; no automatic dependency download is performed.'
}
Assert-Hash (Join-Path $installedCrypto 'ssh/channel.go') $fixtureManifest.originalChannelSHA256
if (-not $OutputDirectory) {
    $OutputDirectory = Join-Path ([IO.Path]::GetTempPath()) ('lbt147-independent-' + [Guid]::NewGuid().ToString('N'))
}
$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $outputRoot) { throw 'OutputDirectory must not already exist.' }
$null = New-Item -ItemType Directory -Path $outputRoot
$cryptoCopy = Join-Path $outputRoot 'crypto-audit'
Copy-Item -LiteralPath $installedCrypto -Destination $cryptoCopy -Recurse
Copy-Item -LiteralPath (Join-Path $repoRoot 'go.mod') -Destination (Join-Path $outputRoot 'audit.mod')
Copy-Item -LiteralPath (Join-Path $repoRoot 'go.sum') -Destination (Join-Path $outputRoot 'audit.sum')
$goModText = [IO.File]::ReadAllText((Join-Path $outputRoot 'audit.mod'))
$replacePath = $cryptoCopy.Replace('\','/')
Write-Utf8 (Join-Path $outputRoot 'audit.mod') ($goModText + "`nreplace golang.org/x/crypto => " + '"' + $replacePath + '"' + "`n")
$overlayMap = [ordered]@{}
$overlayMap[(Join-Path $repoRoot 'internal/connector/vmaccess/independent_review_unacked_setup_test.go').Replace('\','/')] = (Join-Path $fixtureRoot 'independent_unacked_setup_test.go').Replace('\','/')
$overlayMap[(Join-Path $cryptoCopy 'ssh/channel.go').Replace('\','/')] = (Join-Path $fixtureRoot 'ssh-channel-no-b-close-ack.go').Replace('\','/')
$overlayPath = Join-Path $outputRoot 'overlay.json'
Write-Utf8 $overlayPath (@{Replace=$overlayMap} | ConvertTo-Json -Depth 5)
$prepared = [ordered]@{
    sourceHead=$sourceManifest.head; executionSource=$sourceManifest.executionSource
    fixtureSHA256=$fixtureManifest.fixtures; cryptoModule='golang.org/x/crypto@v0.57.0'
    originalChannelSHA256=$fixtureManifest.originalChannelSHA256
    copiedModule=$cryptoCopy; overlay=$overlayPath
    cloudMutation=$false; fault='Server-side channel B ignores SSH channel-close acknowledgement; outbound client code remains original.'
    tests='TestIndependentReviewUnacknowledgedCloseKeepsOneWorkerUntilIdle'
}
Write-Utf8 (Join-Path $outputRoot 'prepared.json') ($prepared | ConvertTo-Json -Depth 8)
if (-not $PrepareOnly) {
    Push-Location -LiteralPath $repoRoot
    try {
        $testOutput = & go test -race -modfile (Join-Path $outputRoot 'audit.mod') -mod=readonly -overlay $overlayPath -json -count=5 -timeout=2m -run '^TestIndependentReviewUnacknowledged' ./internal/connector/vmaccess 2> (Join-Path $outputRoot 'test.stderr.txt')
        $testExit = $LASTEXITCODE
        Write-Utf8 (Join-Path $outputRoot 'test.jsonl') (($testOutput -join "`n") + "`n")
        $stderrPath = Join-Path $outputRoot 'test.stderr.txt'
        if (Test-Path -LiteralPath $stderrPath) { Write-Utf8 $stderrPath ([IO.File]::ReadAllText($stderrPath)) }
    } finally { Pop-Location }
    Write-Utf8 (Join-Path $outputRoot 'exit-code.txt') ([string]$testExit)
    if ($testExit -ne 0) { throw "Independent SSH fixture failed with exit $testExit; inspect OutputDirectory." }
}
Write-Output $outputRoot
