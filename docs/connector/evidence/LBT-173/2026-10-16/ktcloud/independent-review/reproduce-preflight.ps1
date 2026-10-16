param(
    [string]$Repository = (Resolve-Path (Join-Path $PSScriptRoot '../../../../../../..')).Path
)
$ErrorActionPreference = 'Stop'
$taskRepo = (Resolve-Path -LiteralPath $Repository).Path
$taskTemp = Join-Path ([System.IO.Path]::GetTempPath()) ('lbt173-kt-review-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskTemp | Out-Null
$taskFixture = Join-Path $taskTemp 'review_preview_test.go'
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'preflight-probe_test.go.txt') -Destination $taskFixture
$taskVirtual = Join-Path $taskRepo 'tools/ktcloud-d1-acceptance/zz_review_preview_test.go'
$taskReplace = @{}
$taskReplace[$taskVirtual] = $taskFixture
@{ Replace = $taskReplace } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $taskTemp 'overlay.json') -Encoding utf8
Push-Location -LiteralPath $taskRepo
try {
    & go test -overlay (Join-Path $taskTemp 'overlay.json') ./tools/ktcloud-d1-acceptance -run '^TestReviewPreview' -count=1 -timeout=15s -v
    $taskExit = $LASTEXITCODE
} finally {
    Pop-Location
}
Write-Output ('External fixture: ' + $taskTemp)
exit $taskExit
