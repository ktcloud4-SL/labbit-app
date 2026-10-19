# Run from this repository. Existing evidence is not overwritten.
param([switch]$LocalOpenStack)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../../..')).Path
Push-Location $repo
try {
    go version
    go mod download
    if ($LASTEXITCODE -ne 0) { throw 'go mod download failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    go test ./... -count=1 -timeout=4m
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    go build ./...
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    # Set CC and add its bin directory to PATH if running -race on Windows.
    go test -race ./internal/connector/vmaccess ./internal/connector/preview ./internal/connector/app ./internal/connector/terminal ./internal/connector/workspacefile ./internal/connector/filetransport ./internal/connector/wss -count=1 -timeout=2m
    if ($LASTEXITCODE -ne 0) { throw 'race test failed' }
    Push-Location web
    try {
        # npm must be available; this run used pnpm --package=npm@10.9.2 dlx npm.
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        foreach ($target in @('typecheck', 'lint', 'test', 'build')) {
            npm run $target
            if ($LASTEXITCODE -ne 0) { throw "web $target failed" }
        }
    } finally { Pop-Location }
    if ($LocalOpenStack) {
        # Explicit opt-in: creates and cleans disposable local Workspace gen1/gen2.
        # Supply LABBIT_PROVIDER_CONFIG_FILE, OS_CLOUD, SSH_PRIVATE_KEY_FILE and
        # SSH_USERNAME using the LABBIT_OPENSTACK_ prefixes documented in README.
        $env:LABBIT_OPENSTACK_PREVIEW_TEST = '1'
        go test -run '^TestOpenStackPreviewForwardingIntegration$' -v ./internal/connector/provider/openstack -count=1 -timeout=18m
        if ($LASTEXITCODE -ne 0) { throw 'local OpenStack acceptance failed; inspect cleanup result' }
    }
} finally { Pop-Location }
