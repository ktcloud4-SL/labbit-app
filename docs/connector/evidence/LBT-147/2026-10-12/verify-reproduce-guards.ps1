param(
    [string]$Repository = (Join-Path $PSScriptRoot '../../../../..'),
    [string]$TestArtifacts = (Join-Path ([IO.Path]::GetTempPath()) ('lbt147-guard-checks-' + [Guid]::NewGuid().ToString('N')))
)
$ErrorActionPreference = 'Stop'
$originalRepository = (Resolve-Path -LiteralPath $Repository).Path
if (Test-Path -LiteralPath $TestArtifacts) { throw 'TestArtifacts must be a new directory' }
New-Item -ItemType Directory -Path $TestArtifacts | Out-Null
$testArtifacts = (Resolve-Path -LiteralPath $TestArtifacts).Path
$testCheckout = Join-Path $testArtifacts 'isolated-checkout'
$sourceManifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'source-manifest.json') -Raw | ConvertFrom-Json
$codeCommit = $sourceManifest.publicationCodeCommit
& git clone --shared --no-checkout --quiet -- $originalRepository $testCheckout
if ($LASTEXITCODE -ne 0) { throw 'Isolated local checkout failed' }
& git -C $testCheckout checkout --quiet --detach $codeCommit
if ($LASTEXITCODE -ne 0) { throw 'Isolated code checkout failed' }
$evidenceRelative = 'docs/connector/evidence/LBT-147/2026-10-12'
$testEvidence = Join-Path $testCheckout $evidenceRelative
Copy-Item -LiteralPath (Join-Path $originalRepository $evidenceRelative) -Destination $testEvidence -Recurse
$script = Join-Path $testEvidence 'reproduce.ps1'
$stubConfig = Join-Path $testArtifacts 'stub-cloud-config.yaml'
$stubKey = Join-Path $testArtifacts 'stub-private-key.txt'
'clouds: {}' | Set-Content -LiteralPath $stubConfig
'NO REAL KEY' | Set-Content -LiteralPath $stubKey
$utf8 = New-Object System.Text.UTF8Encoding($false)
$previousGoFunction = Get-Item -LiteralPath 'Function:/go' -ErrorAction SilentlyContinue
$previousGoScriptBlock = if ($null -ne $previousGoFunction) { $previousGoFunction.ScriptBlock } else { $null }
$goProbe = @{calls=0}
$stubGo = { $goProbe.calls += 1; throw 'CLOUD_EXECUTION_DISABLED_FOR_GUARD_VERIFICATION' }.GetNewClosure()
Set-Item -LiteralPath 'Function:global:go' -Value $stubGo
$results = @()
function Invoke-GuardCase {
    param([string]$CaseName,[int]$ExpectedGoCalls,[string]$ExpectedError)
    $goProbe.calls = 0
    $output = Join-Path $testArtifacts ('guard-' + $CaseName + '-' + [Guid]::NewGuid().ToString('N'))
    $failure = ''
    try { & $script -Repository $testCheckout -CloudConfig $stubConfig -PrivateKey $stubKey -OutputDirectory $output } catch { $failure = $_.Exception.Message }
    if ($goProbe.calls -ne $ExpectedGoCalls -or $failure -notlike ('*' + $ExpectedError + '*')) { throw "Unexpected guard outcome: $CaseName; calls=$($goProbe.calls); error=$failure" }
    if ($ExpectedGoCalls -gt 0) {
        foreach ($name in @('app-overlay.json','residual-overlay.json')) {
            $bytes = [IO.File]::ReadAllBytes((Join-Path $output $name))
            if ($bytes.Length -ge 3 -and $bytes[0] -eq 239 -and $bytes[1] -eq 187 -and $bytes[2] -eq 191) { throw 'Unexpected UTF8 BOM' }
            [void](Get-Content -LiteralPath (Join-Path $output $name) -Raw | ConvertFrom-Json)
        }
    }
    [PSCustomObject]@{case=$CaseName; result='PASS'; goStubCalls=$goProbe.calls; actualCloudCalls=0}
}
try {
    $results += Invoke-GuardCase 'matching-code-and-evidence-only' 2 'CLOUD_EXECUTION_DISABLED'
    $producerRelative = 'internal/connector/provider/openstack/lifecycle.go'
    $producer = Join-Path $testCheckout $producerRelative
    [IO.File]::AppendAllText($producer,"`n// Unchanged-file guard regression.`n",$utf8)
    $results += Invoke-GuardCase 'unmanifested-tracked-producer-change' 0 'Tracked source differs'
    & git -C $testCheckout add -- $producerRelative
    if ($LASTEXITCODE -ne 0) { throw 'Isolated staging failed' }
    & git -C $testCheckout restore --source $codeCommit --worktree -- $producerRelative
    if ($LASTEXITCODE -ne 0) { throw 'Isolated working source restore failed' }
    $results += Invoke-GuardCase 'staged-producer-change-with-original-worktree' 0 'Staged source differs'
    & git -C $testCheckout restore --source $codeCommit --staged --worktree -- $producerRelative
    if ($LASTEXITCODE -ne 0) { throw 'Isolated source restore failed' }
    $untracked = Join-Path $testCheckout 'internal/connector/app/guard_untracked_test.go'
    [IO.File]::WriteAllText($untracked,'package app',$utf8)
    $results += Invoke-GuardCase 'untracked-go-source' 0 'Untracked files outside'
    Remove-Item -LiteralPath $untracked
    $ignoredFolder = Join-Path $testCheckout '.local'
    New-Item -ItemType Directory -Path $ignoredFolder | Out-Null
    $ignoredSource = Join-Path $ignoredFolder 'guard.go'
    [IO.File]::WriteAllText($ignoredSource,'package guard',$utf8)
    $results += Invoke-GuardCase 'ignored-go-source' 0 'Ignored Go source'
    Remove-Item -LiteralPath $ignoredSource
    $results += Invoke-GuardCase 'restored-code-and-evidence-only' 2 'CLOUD_EXECUTION_DISABLED'
} finally {
    if ($null -ne $previousGoScriptBlock) { Set-Item -LiteralPath 'Function:global:go' -Value $previousGoScriptBlock } else { Remove-Item -LiteralPath 'Function:/go' }
}
$report = [PSCustomObject]@{checkedAt=(Get-Date -Format o); codeCommit=$codeCommit; isolatedCheckout=$testCheckout; cases=$results; cloudMutations=0; note='Actual Go replaced by a throwing function. Matching source reaches pre/post stubs; changed source refuses before any Go call. Overlay JSON is BOMless. No original repository source edits.'}
$report | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $testArtifacts 'reproduce-guard-regression.json') -Encoding utf8
$report | Select-Object codeCommit,cases,cloudMutations | ConvertTo-Json -Depth 4
