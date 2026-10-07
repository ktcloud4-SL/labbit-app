param(
    [Parameter(Mandatory=$true)][string]$ProviderConfigFile,
    [Parameter(Mandatory=$true)][string]$SSHPrivateKeyFile,
    [string]$CloudName='labbit-test',
    [string]$SSHUsername='ubuntu',
    [string]$OutputDirectory
)
$ErrorActionPreference='Stop'
$expectedCommit='57990544fac291def5d7cf7c6d5b54da3cabc4c8'
# Save this script before checking out the tested source SHA (the evidence-only
# commit contains this script; the tested source commit intentionally does not).
$repoDirectory=(git rev-parse --show-toplevel)
if ($LASTEXITCODE -ne 0) { throw 'Run from the repository checkout' }
Set-Location -LiteralPath $repoDirectory
if ((git rev-parse HEAD) -ne $expectedCommit) { throw "Checkout tested source commit $expectedCommit" }
if (@(git status --porcelain).Count) { throw 'Use a clean checkout; place this saved script outside it' }
if (-not (Test-Path -LiteralPath $ProviderConfigFile -PathType Leaf) -or -not (Test-Path -LiteralPath $SSHPrivateKeyFile -PathType Leaf)) { throw 'Customer-local credential/key file is missing' }
if (-not $OutputDirectory) { $OutputDirectory=Join-Path $repoDirectory ('.local/evidence/rerun-'+(Get-Date -Format 'yyyyMMdd-HHmmss')) }
New-Item -ItemType Directory -Path $OutputDirectory -ErrorAction Stop | Out-Null
$env:LABBIT_PROVIDER_CONFIG_FILE=(Resolve-Path -LiteralPath $ProviderConfigFile).Path
$env:OS_CLOUD=$CloudName
$env:LABBIT_OPENSTACK_SSH_USERNAME=$SSHUsername
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE=(Resolve-Path -LiteralPath $SSHPrivateKeyFile).Path
$gates=@('LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_MUTATION_TEST','LABBIT_OPENSTACK_M2_TEST','LABBIT_OPENSTACK_M3_TEST','LABBIT_OPENSTACK_M2_WSS_TEST','LABBIT_OPENSTACK_M3_WSS_TEST','LABBIT_OPENSTACK_INTERNET_POLICY_TEST')
foreach ($target in @(
    @{name='TestOpenStackM1Integration';gate='LABBIT_OPENSTACK_INTEGRATION';timeout='2m';file='real-m1'},
    @{name='TestOpenStackM2ControlWSSIntegration';gate='LABBIT_OPENSTACK_M3_WSS_TEST';timeout='20m';file='real-wss-lifecycle'},
    @{name='TestOpenStackInternetPolicyIntegration';gate='LABBIT_OPENSTACK_INTERNET_POLICY_TEST';timeout='25m';file='real-internet'},
    @{name='TestOpenStackM1Integration';gate='LABBIT_OPENSTACK_INTEGRATION';timeout='2m';file='real-post-cleanup'}
)) {
    foreach($gate in $gates) { Remove-Item -LiteralPath ('Env:'+$gate) -ErrorAction SilentlyContinue }
    Set-Item -LiteralPath ('Env:'+$target.gate) -Value '1'
    $log=Join-Path $OutputDirectory ($target.file+'.jsonl')
    & go test -json -count=1 ('-timeout='+$target.timeout) -run ('^'+$target.name+'$') './internal/connector/provider/openstack' 2>&1 | Tee-Object -FilePath $log
    $code=$LASTEXITCODE
    if($code -ne 0) { throw "Failed $($target.name); inspect cleanup logs before any retry" }
    $events=Get-Content -LiteralPath $log | ConvertFrom-Json
    if (-not @($events | Where-Object { $_.Action -eq 'pass' -and $_.Test -eq $target.name }).Count) { throw 'Selected target did not execute successfully' }
}
Write-Output "Tested source $expectedCommit; logs=$OutputDirectory"
