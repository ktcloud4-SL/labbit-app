param(
    [string]$Repository = (Join-Path $PSScriptRoot '../../../../..'),
    [Parameter(Mandatory=$true)][string]$CloudConfig,
    [Parameter(Mandatory=$true)][string]$PrivateKey,
    [string]$CloudName = 'labbit-test',
    [string]$OutputDirectory = (Join-Path ([IO.Path]::GetTempPath()) ('lbt147-rerun-' + [Guid]::NewGuid().ToString('N')))
)
$ErrorActionPreference = 'Stop'
$repoPath = (Resolve-Path -LiteralPath $Repository).Path
$configPath = (Resolve-Path -LiteralPath $CloudConfig).Path
$keyPath = (Resolve-Path -LiteralPath $PrivateKey).Path
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'source-manifest.json') -Raw | ConvertFrom-Json
$checksums = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'artifact-checksums.json') -Raw | ConvertFrom-Json
$archive = Join-Path $PSScriptRoot 'audit-logs.zip'
$expectedArchive = @($checksums.artifacts | Where-Object { $_.file -eq 'audit-logs.zip' })
if ($expectedArchive.Count -ne 1 -or (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expectedArchive[0].sha256) { throw 'Evidence archive hash mismatch' }
$codeCommit = $manifest.publicationCodeCommit
& git -C $repoPath cat-file -e ($codeCommit + '^{commit}')
if ($LASTEXITCODE -ne 0) { throw 'Audited code commit is unavailable' }
$evidenceExclude = ':(exclude)docs/connector/evidence/LBT-147/2026-10-12/**'
& git -C $repoPath diff --quiet $codeCommit -- . $evidenceExclude
if ($LASTEXITCODE -ne 0) { throw 'Tracked source differs from the complete audited code commit' }
& git -C $repoPath diff --cached --quiet $codeCommit -- . $evidenceExclude
if ($LASTEXITCODE -ne 0) { throw 'Staged source differs from the complete audited code commit' }
$untrackedSource = @(& git -C $repoPath ls-files --others --exclude-standard -- . $evidenceExclude)
if ($LASTEXITCODE -ne 0 -or $untrackedSource.Count -gt 0) { throw 'Untracked files outside the evidence directory are not allowed for reproduction' }
$ignoredGoSource = @(& git -C $repoPath ls-files --others --ignored --exclude-standard -- '*.go' 'go.mod' 'go.sum')
if ($LASTEXITCODE -ne 0 -or $ignoredGoSource.Count -gt 0) { throw 'Ignored Go source could alter the audited reproduction' }
foreach ($file in $manifest.changedFiles) {
    $blob = & git -C $repoPath hash-object --path $file.path (Join-Path $repoPath $file.path)
    if ($LASTEXITCODE -ne 0 -or $blob -ne $file.committedGitBlob) { throw "Source differs from audited revision: $($file.path)" }
}
if (Test-Path -LiteralPath $OutputDirectory) { throw 'OutputDirectory must be a new directory' }
New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
$outputPath = (Resolve-Path -LiteralPath $OutputDirectory).Path
$extracted = Join-Path $outputPath 'archive'
Expand-Archive -LiteralPath $archive -DestinationPath $extracted
foreach ($file in $manifest.harnesses) {
    $harness = Join-Path $extracted ('real/' + $file.path)
    if ((Get-FileHash -LiteralPath $harness -Algorithm SHA256).Hash.ToLowerInvariant() -ne $file.sha256Bytes) { throw "Audit harness differs: $($file.path)" }
}
$appOverlay = Join-Path $outputPath 'app-overlay.json'
$residualOverlay = Join-Path $outputPath 'residual-overlay.json'
$utf8NoBOM = New-Object System.Text.UTF8Encoding($false)
$appJSON = @{Replace=@{(Join-Path $repoPath 'internal/connector/app/lbt147_real_audit_test.go')=(Join-Path $extracted 'real/app_real_integration_test.go')}} | ConvertTo-Json -Depth 4
$residualJSON = @{Replace=@{(Join-Path $repoPath 'internal/connector/provider/openstack/lbt147_residual_audit_test.go')=(Join-Path $extracted 'real/read_only_residual_test.go')}} | ConvertTo-Json -Depth 4
[IO.File]::WriteAllText($appOverlay,$appJSON,$utf8NoBOM)
[IO.File]::WriteAllText($residualOverlay,$residualJSON,$utf8NoBOM)
$inputs=@{
    LABBIT_PROVIDER_CONFIG_FILE=$configPath; OS_CLOUD=$CloudName;
    LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE=$keyPath; LABBIT_OPENSTACK_SSH_USERNAME='ubuntu';
    LABBIT_OPENSTACK_INTEGRATION='1'; LABBIT_OPENSTACK_MUTATION_TEST='0';
    LABBIT_OPENSTACK_VM_ACCESS_TEST='1'; LABBIT_OPENSTACK_APP_AUDIT='1'; LABBIT_OPENSTACK_RESIDUAL_AUDIT='1';
    LABBIT_OPENSTACK_TEST_IMAGE='ubuntu'; LABBIT_OPENSTACK_TEST_FLAVOR='m1.small';
    LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK='sharednet1'; LABBIT_OPENSTACK_TEST_SSH_CIDR='172.16.8.1/32';
    LABBIT_OPENSTACK_TEST_LAB_CIDR='198.20.0.0/24'
}
$previous=@{}
$codes=@{}
function Invoke-RecordedGoTest {
    param([string]$Name, [string[]]$Arguments, [int]$ExpectedPasses)
    $log = Join-Path $outputPath ($Name + '.jsonl')
    & go @Arguments 1> $log 2> (Join-Path $outputPath ($Name + '.stderr.txt'))
    $code=$LASTEXITCODE
    $codes[$Name]=$code
    if ($code -ne 0) { throw "Go test failed: $Name. Retain original logs." }
    $events=@(Get-Content -LiteralPath $log | Where-Object { $_.Trim() } | ForEach-Object { $_ | ConvertFrom-Json })
    if (@($events | Where-Object { $_.Action -eq 'skip' -or $_.Action -eq 'fail' }).Count -gt 0) { throw "Go test skipped or failed: $Name" }
    if (@($events | Where-Object { $_.Action -eq 'pass' -and $_.Test }).Count -ne $ExpectedPasses) { throw "Unexpected selected test count: $Name" }
}
Push-Location -LiteralPath $repoPath
try {
    foreach ($name in $inputs.Keys) { $previous[$name]=[Environment]::GetEnvironmentVariable($name,'Process'); [Environment]::SetEnvironmentVariable($name,$inputs[$name],'Process') }
    Invoke-RecordedGoTest 'pre-discovery' @('test','-mod=readonly','-json','-count=1','-timeout=2m','-run','^TestOpenStackM1Integration$','./internal/connector/provider/openstack') 1
    Invoke-RecordedGoTest 'real-vm-access' @('test','-race','-mod=readonly','-json','-count=1','-timeout=18m','-run','^TestOpenStackVMAccessIntegration$','./internal/connector/provider/openstack') 1
    Invoke-RecordedGoTest 'real-app-boundaries' @('test','-race','-mod=readonly','-overlay',$appOverlay,'-json','-count=1','-timeout=20m','-run','^TestLBT147RealAppBoundaries$','./internal/connector/app') 8
} finally {
    try {
        # Tests own exact-ID teardown. This step only queries inventory; it never deletes by name/prefix.
        Invoke-RecordedGoTest 'post-discovery-and-residual' @('test','-race','-mod=readonly','-overlay',$residualOverlay,'-json','-count=1','-timeout=3m','-run','^(TestLBT147ReadOnlyResiduals|TestOpenStackM1Integration)$','./internal/connector/provider/openstack') 2
    } finally {
        $codes | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $outputPath 'exit-codes.json') -Encoding utf8
        Pop-Location
        foreach ($name in $previous.Keys) { [Environment]::SetEnvironmentVariable($name,$previous[$name],'Process') }
    }
}
Write-Output "Real local OpenStack validation passed. Logs: $outputPath"
