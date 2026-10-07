param(
    [Parameter(Mandatory=$true)][string]$ProviderConfigFile,
    [Parameter(Mandatory=$true)][string]$SSHPrivateKeyFile,
    [string]$CloudName='labbit-test',
    [string]$SSHUsername='ubuntu',
    [switch]$WithPostgres,
    [string]$ExpectedSourceCommit='0f8ca010ed45eea0db669937871006fa0e7e5a7b'
)
$ErrorActionPreference='Stop'
# Save this script outside the checkout, then checkout the clean source SHA.
if ((git rev-parse HEAD) -ne $ExpectedSourceCommit -or @(git status --porcelain).Count) {
    throw 'Run from a clean checkout of the tested source commit'
}
if (-not (Test-Path -LiteralPath $ProviderConfigFile) -or -not (Test-Path -LiteralPath $SSHPrivateKeyFile)) {
    throw 'Customer-local Provider config and matching SSH private-key FILES are required'
}
$outputDirectory=Join-Path (Get-Location).Path '.local/evidence/LBT-82-reproduced-20261006'
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
$env:LABBIT_PROVIDER_CONFIG_FILE=$ProviderConfigFile
$env:OS_CLOUD=$CloudName
$env:LABBIT_OPENSTACK_SSH_USERNAME=$SSHUsername
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE=$SSHPrivateKeyFile
$gates=@('LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_MUTATION_TEST','LABBIT_OPENSTACK_M2_TEST','LABBIT_OPENSTACK_M3_TEST','LABBIT_OPENSTACK_M2_WSS_TEST','LABBIT_OPENSTACK_M3_WSS_TEST','LABBIT_OPENSTACK_INTERNET_POLICY_TEST')
if ($WithPostgres) {
    # Use an isolated PostgreSQL 16 server with CREATE DATABASE permission.
    # Never point this variable at a shared production database.
    if ([string]::IsNullOrWhiteSpace($env:LABBIT_TEST_DATABASE_DSN)) {throw 'Set LABBIT_TEST_DATABASE_DSN to an isolated test PostgreSQL 16 server'}
    $packages=@('./internal/postgres','./internal/postgres/postgrestest','./internal/server/app','./internal/server/auth','./internal/server/httpapi')
    & go vet -tags integration @packages
    if ($LASTEXITCODE) {throw 'Integration vet failed'}
    & go test -json -tags integration -mod=readonly -count=1 -timeout=8m @packages 2>&1 |
        Tee-Object -FilePath (Join-Path $outputDirectory 'postgres-integration.jsonl') | Out-Null
    if ($LASTEXITCODE) {throw 'PostgreSQL integration failed'}
}
foreach($target in @(
    @{name='TestOpenStackInternetPolicyIntegration';gate='LABBIT_OPENSTACK_INTERNET_POLICY_TEST';timeout='25m';log='real-internet-policy'},
    @{name='TestOpenStackM2ControlWSSIntegration';gate='LABBIT_OPENSTACK_M3_WSS_TEST';timeout='20m';log='real-wss-lifecycle'},
    @{name='TestOpenStackM1Integration';gate='LABBIT_OPENSTACK_INTEGRATION';timeout='2m';log='real-post-cleanup'}
)) {
    foreach($gate in $gates) {Remove-Item -LiteralPath ('Env:'+$gate) -ErrorAction SilentlyContinue}
    Set-Item -LiteralPath ('Env:'+$target.gate) -Value '1'
    $logPath=Join-Path $outputDirectory ($target.log+'.jsonl')
    & go test -json -mod=readonly -count=1 ('-timeout='+$target.timeout) -run ('^'+$target.name+'$') ./internal/connector/provider/openstack 2>&1 |
        Tee-Object -FilePath $logPath | Out-Null
    if ($LASTEXITCODE) {throw ('Failed '+$target.name+'; inspect result/owned-resource cleanup before retry')}
    $events=Get-Content -LiteralPath $logPath | ConvertFrom-Json
    if (-not @($events | Where-Object {$_.Action -eq 'pass' -and $_.Test -eq $target.name}).Count) {throw 'Target was skipped or failed; not a PASS'}
    Write-Output ($target.name+' PASS')
}
