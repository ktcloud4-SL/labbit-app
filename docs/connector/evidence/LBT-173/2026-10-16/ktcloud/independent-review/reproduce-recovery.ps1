param([string]$Repository = (Resolve-Path (Join-Path $PSScriptRoot '../../../../../../..')).Path)
$ErrorActionPreference = 'Stop'
$taskTemp = Join-Path ([System.IO.Path]::GetTempPath()) ('lbt173-recovery-review-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskTemp | Out-Null
$taskUtf8 = [System.Text.UTF8Encoding]::new($false)
$taskSourcePath = Join-Path $Repository 'tools/ktcloud-d1-acceptance/recovery.go'
$taskRaw = [System.IO.File]::ReadAllText($taskSourcePath).Replace("`r`n", "`n")
$taskNeedle = "for _, item := range items {`n`t`tknown = append(known, item.ProviderResourceRef)"
$taskReplacement = "for _, item := range items {`n`t`tif item.ObservedState == `"DELETED`" {`n`t`t`tcontinue`n`t`t}`n`t`tknown = append(known, item.ProviderResourceRef)"
if (($taskRaw.Split([string[]]@($taskNeedle), [System.StringSplitOptions]::None).Length - 1) -ne 1) { throw 'Recovery mutation anchor did not occur exactly once' }
$taskMutant = Join-Path $taskTemp 'recovery.go'
[System.IO.File]::WriteAllText($taskMutant, $taskRaw.Replace($taskNeedle, $taskReplacement), $taskUtf8)
$taskOverlay = Join-Path $taskTemp 'overlay.json'
$taskMap = @{ Replace = @{ $taskSourcePath = $taskMutant } } | ConvertTo-Json -Depth 5
[System.IO.File]::WriteAllText($taskOverlay, $taskMap, $taskUtf8)
Push-Location $Repository
try {
  $taskControl = & go test ./tools/ktcloud-d1-acceptance -run '^TestRecoveryAfterReadOnlyDeletedLedgerRechecksAllIDs$' -count=1 -v -timeout=30s 2>&1
  $taskControlExit = $LASTEXITCODE
  $taskControl | Out-File -FilePath (Join-Path $PSScriptRoot 'recovery-control.log') -Encoding utf8
  $taskMutation = & go test ('-overlay=' + $taskOverlay) ./tools/ktcloud-d1-acceptance -run '^TestRecoveryAfterReadOnlyDeletedLedgerRechecksAllIDs$' -count=1 -v -timeout=30s 2>&1
  $taskMutationExit = $LASTEXITCODE
  $taskMutation | Out-File -FilePath (Join-Path $PSScriptRoot 'recovery-deleted-filter-mutant.log') -Encoding utf8
  $taskResult = [ordered]@{ control_exit = $taskControlExit; deleted_filter_mutant_exit = $taskMutationExit; mutation = 'Semantic mutant: reintroduce DELETED filtering in recoveryResourceRefs; not a historical production source snapshot'; current_helper_sha256 = (Get-FileHash -LiteralPath $taskSourcePath -Algorithm SHA256).Hash.ToLowerInvariant() }
  [System.IO.File]::WriteAllText((Join-Path $PSScriptRoot 'recovery-probe-result.json'), ($taskResult | ConvertTo-Json -Depth 5) + "`n", $taskUtf8)
  if ($taskControlExit -ne 0 -or $taskMutationExit -eq 0) { throw 'Expected current source PASS and semantic mutant FAIL' }
  $taskResult | ConvertTo-Json -Depth 5
} finally { Pop-Location }