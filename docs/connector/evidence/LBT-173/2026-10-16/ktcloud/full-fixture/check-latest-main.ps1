$ErrorActionPreference = 'Stop'
$repoDir = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '../../../../../../..')).Path
$integrationDir = Join-Path $env:TEMP ('lbt173-main-check-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $integrationDir | Out-Null
$mainSha = '8f88833ec433c2de19ea91375dec57ae95a2baea'
$relative = 'internal/connector/provider/openstack/terminal_c2_integration_test.go'
$source = & git -C $repoDir show ($mainSha + ':' + $relative)
if ($LASTEXITCODE -ne 0) { throw 'Pinned main test source unavailable' }
$sourcePath = Join-Path $integrationDir 'terminal_c2_integration_test.go'
[System.IO.File]::WriteAllText($sourcePath, ($source -join "`n") + "`n", [System.Text.UTF8Encoding]::new($false))
$virtualPath = Join-Path $repoDir $relative
$overlay = @{Replace=@{$virtualPath=$sourcePath}} | ConvertTo-Json -Depth 4
$overlayPath = Join-Path $integrationDir 'overlay.json'
[System.IO.File]::WriteAllText($overlayPath, $overlay, [System.Text.UTF8Encoding]::new($false))
Push-Location $repoDir
try {
    & go test ('-overlay=' + $overlayPath) ./internal/connector/provider/openstack -run '^TestTerminalC2_FullStackLoopback$' -json -count=1 -timeout=3m *> (Join-Path $PSScriptRoot 'latest-main-c2-test.jsonl')
    $result = $LASTEXITCODE
} finally { Pop-Location }
[ordered]@{main=$mainSha;source=$relative;sourceSha256=(Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash;exitCode=$result;scope='Pinned new main C2 loopback test over current feature source via external Go overlay; no cloud'} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'latest-main-check.json')
Write-Output ('Latest main C2 overlay exit=' + $result)
exit $result

