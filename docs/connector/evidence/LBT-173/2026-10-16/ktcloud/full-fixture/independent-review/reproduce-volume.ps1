param(
 [ValidateSet('Current','Original','DeletingGuardOnly')][string]$Mode='Current',
 [string]$Repository=(Resolve-Path (Join-Path $PSScriptRoot '../../../../../../../..')).Path
)
$ErrorActionPreference='Stop'
$taskTemp=Join-Path ([System.IO.Path]::GetTempPath()) ('lbt173-volume-review-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskTemp | Out-Null
$taskUtf8=[System.Text.UTF8Encoding]::new($false)
$taskMapping=@{}
foreach($taskName in @('volume-convergence','volume-rejection')){
 $taskFile=Join-Path $taskTemp ($taskName.Replace('-','_')+'_test.go')
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot ($taskName+'-probe_test.go.txt')) -Destination $taskFile
 & gofmt -w $taskFile
 if($LASTEXITCODE -ne 0){throw 'External probe gofmt failed'}
 $taskMapping[(Join-Path $Repository ('internal/connector/provider/openstack/zz_review_'+$taskName.Replace('-','_')+'_test.go'))]=$taskFile
}
if($Mode -ne 'Current'){
 $taskInfo=[System.Diagnostics.ProcessStartInfo]::new('git');$taskInfo.WorkingDirectory=$Repository
 $taskInfo.ArgumentList.Add('show');$taskInfo.ArgumentList.Add('88097be5d9bb6996b718fd185515ca324629fda6:internal/connector/provider/openstack/ktcloud_d1_delete.go')
 $taskInfo.RedirectStandardOutput=$true;$taskInfo.RedirectStandardError=$true;$taskInfo.UseShellExecute=$false
 $taskProcess=[System.Diagnostics.Process]::Start($taskInfo);$taskMemory=[System.IO.MemoryStream]::new()
 $taskProcess.StandardOutput.BaseStream.CopyTo($taskMemory);$taskProcess.WaitForExit()
 if($taskProcess.ExitCode -ne 0){throw 'Pinned base source unavailable'}
 $taskRaw=[System.Text.Encoding]::UTF8.GetString($taskMemory.ToArray())
 if($Mode -eq 'DeletingGuardOnly'){
  $taskNeedle="`treturn volumes.Delete(ctx, a.volume, id, volumes.DeleteOpts{}).ExtractErr()"
  if(($taskRaw.Split([string[]]@($taskNeedle),[System.StringSplitOptions]::None).Length-1) -ne 1){throw 'Mutation anchor mismatch'}
  $taskRaw=$taskRaw.Replace($taskNeedle,"`tif item.Status == `"deleting`" {`n`t`treturn nil`n`t}`n"+$taskNeedle)
 }
 $taskProvider=Join-Path $taskTemp 'ktcloud_d1_delete.go'
 [System.IO.File]::WriteAllText($taskProvider,$taskRaw,$taskUtf8)
 $taskMapping[(Join-Path $Repository 'internal/connector/provider/openstack/ktcloud_d1_delete.go')]=$taskProvider
}
$taskOverlay=Join-Path $taskTemp 'overlay.json'
[System.IO.File]::WriteAllText($taskOverlay,(@{Replace=$taskMapping}|ConvertTo-Json -Depth 5),$taskUtf8)
Push-Location $Repository
try{
 & go test ('-overlay='+$taskOverlay) ./internal/connector/provider/openstack -run '^TestReviewKT(VolumeDeletionConvergence|RejectedVolumeDeleteFreshState)$' -v -count=1 -timeout=30s *> (Join-Path $PSScriptRoot ('volume-probe-'+$Mode.ToLowerInvariant()+'.log'))
 $taskExit=$LASTEXITCODE
 $taskResult=[ordered]@{mode=$Mode;go_test_exit=$taskExit;current_source_sha256=(Get-FileHash -LiteralPath 'internal/connector/provider/openstack/ktcloud_d1_delete.go' -Algorithm SHA256).Hash.ToLowerInvariant();scope='In-process local scripted HTTP peer only. Original replaces pinned base volume file (verified LF identity with original live); DeletingGuardOnly is an explicit semantic mutant. No cloud API or repository source edits.'}
 [System.IO.File]::WriteAllText((Join-Path $PSScriptRoot ('volume-probe-'+$Mode.ToLowerInvariant()+'.json')),($taskResult|ConvertTo-Json -Depth 5)+"`n",$taskUtf8)
 $taskResult|ConvertTo-Json -Depth 5
}finally{Pop-Location}
exit $taskExit