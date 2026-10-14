param(
    [Parameter(Mandatory = $true)][string]$ProviderConfigFile,
    [Parameter(Mandatory = $true)][string]$Cloud,
    [Parameter(Mandatory = $true)][string]$SSHPrivateKeyFile,
    [string]$SSHUsername = 'ubuntu'
)
$ErrorActionPreference = 'Stop'
$testedSource = 'b0b5686d010f5c247ac007b76d373fbec109ea0f'
$currentSource = git rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $currentSource -ne $testedSource) {
    throw 'Checkout the tested source SHA before running this evidence reproduction.'
}
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
go test -mod=readonly -count=1 ./...
if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
go build ./...
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
go test -race -mod=readonly -count=1 -timeout=3m ./internal/connector/vmaccess ./internal/connector/terminal ./internal/connector/app ./internal/connector/provider/openstack ./internal/connector/wss
if ($LASTEXITCODE -ne 0) { throw 'Connector race tests failed' }

$accessEnvironment = @{
    LABBIT_PROVIDER_CONFIG_FILE = $ProviderConfigFile
    OS_CLOUD = $Cloud
    LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE = $SSHPrivateKeyFile
    LABBIT_OPENSTACK_SSH_USERNAME = $SSHUsername
    LABBIT_OPENSTACK_VM_ACCESS_TEST = '1'
    LABBIT_OPENSTACK_INTEGRATION = '1'
    LABBIT_OPENSTACK_MUTATION_TEST = '0'
}
$previousValues = @{}
try {
    foreach ($name in $accessEnvironment.Keys) {
        $previousValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $accessEnvironment[$name], 'Process')
    }
    go test -v -mod=readonly -count=1 -timeout=18m -run '^TestOpenStackVMAccessIntegration$' ./internal/connector/provider/openstack
    if ($LASTEXITCODE -ne 0) { throw 'Real VM access failed; inspect cleanup results before retrying.' }
    go test -v -mod=readonly -count=1 -timeout=2m -run '^TestOpenStackM1Integration$' ./internal/connector/provider/openstack
    if ($LASTEXITCODE -ne 0) { throw 'Post-cleanup discovery failed' }
} finally {
    foreach ($name in $previousValues.Keys) {
        [Environment]::SetEnvironmentVariable($name, $previousValues[$name], 'Process')
    }
}
