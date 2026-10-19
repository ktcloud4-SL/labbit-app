param(
    [Parameter(Mandatory)][string]$ConfigFile,
    [Parameter(Mandatory)][string]$Cloud,
    [Parameter(Mandatory)][string]$PrivateKeyFile,
    [Parameter(Mandatory)][string]$ManagementNetwork,
    [Parameter(Mandatory)][string]$SSHCIDR,
    [string]$SSHUser = 'ubuntu',
    [string]$Image = 'ubuntu',
    [string]$Flavor = 'm1.small',
    [string]$LabCIDR = '198.20.0.0/24'
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../../../../..')).Path
$env:LABBIT_PROVIDER_CONFIG_FILE = (Resolve-Path -LiteralPath $ConfigFile).Path
$env:OS_CLOUD = $Cloud
$env:LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = (Resolve-Path -LiteralPath $PrivateKeyFile).Path
$env:LABBIT_OPENSTACK_SSH_USERNAME = $SSHUser
$env:LABBIT_OPENSTACK_TEST_IMAGE = $Image
$env:LABBIT_OPENSTACK_TEST_FLAVOR = $Flavor
$env:LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK = $ManagementNetwork
$env:LABBIT_OPENSTACK_TEST_SSH_CIDR = $SSHCIDR
$env:LABBIT_OPENSTACK_TEST_LAB_CIDR = $LabCIDR
$env:LABBIT_OPENSTACK_PREVIEW_TRANSPORT_TEST = '1'
Push-Location $repoRoot
try {
    go test ./internal/connector/provider/openstack -run '^TestOpenStackPreviewTransportIntegration$' -v -count=1 -timeout=22m
    if ($LASTEXITCODE -ne 0) { throw 'Local Preview transport acceptance failed; preserve output and verify tracked resource cleanup.' }
} finally {
    Pop-Location
    Remove-Item Env:LABBIT_OPENSTACK_PREVIEW_TRANSPORT_TEST -ErrorAction SilentlyContinue
}
