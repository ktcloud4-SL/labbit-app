$ErrorActionPreference='Stop'
$review=$PSScriptRoot
$source='C:/Users/user/Documents/ChatGPT/3차 2/tmp/lbt174-routing-fix-20261016/Set-LabbitVMnet8Dhcp.ps1'
$sourceText=[IO.File]::ReadAllText($source)
$sourceHash=(Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
$text=$sourceText -replace '(?m)^\$principal=.*\r?\n','' -replace '(?m)^if\(-not \$principal\.IsInRole.*\r?\n',''
$statePath=Join-Path $review 'admin-service-fixture-state.json'
$configPath=Join-Path $review 'admin-service-fixture-config.txt'
$stateLiteral="'"+$statePath.Replace("'","''")+"'"
$configLiteral="'"+$configPath.Replace("'","''")+"'"
$text=[regex]::Replace($text,'(?m)^\$statePath=.*$',('$statePath='+$stateLiteral))
$text=[regex]::Replace($text,'(?m)^\$configPath=.*$',('$configPath='+$configLiteral))
$program=[scriptblock]::Create($text)
function Get-Service {param([string]$Name) if($Name -ne 'VMnetDHCP'){throw 'unexpected service'}; [pscustomobject]@{Name=$Name;Status=$global:reviewService.Status;StartType=$global:reviewService.StartType}}
function Set-Service {param([string]$Name,[string]$StartupType) if($Name -ne 'VMnetDHCP'){throw 'unexpected service'}; $global:reviewService.StartType=$StartupType; $global:reviewService.SetCalls++}
function Start-Service {param([string]$Name) if($Name -ne 'VMnetDHCP'){throw 'unexpected service'}; $global:reviewService.Status='Running'; $global:reviewService.StartCalls++}
function Stop-Service {param([string]$Name) if($Name -ne 'VMnetDHCP'){throw 'unexpected service'}; $global:reviewService.Status='Stopped'; $global:reviewService.StopCalls++}
$rows=@()
foreach($mode in @('prior-stopped-current-running','prior-running-current-stopped','configuration-drift')){
    $priorRunning=$mode -ne 'prior-stopped-current-running'
    'configuration differs from saved hash' | Set-Content -LiteralPath $configPath -Encoding UTF8
    $savedHash=$(if($mode -eq 'configuration-drift'){'0'*64}else{(Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash})
    [pscustomobject]@{Name='VMnetDHCP';StartType='Automatic';WasRunning=$priorRunning;ConfigSHA256=$savedHash} | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8
    $global:reviewService=@{Status=$(if($priorRunning){'Stopped'}else{'Running'});StartType='Disabled';SetCalls=0;StartCalls=0;StopCalls=0}
    $observed=$null
    try { & $program -Action Restore | Out-Null } catch { $observed=$_.Exception.Message }
    $passed=$(if($mode -eq 'configuration-drift'){$observed -and $global:reviewService.SetCalls -eq 0 -and $global:reviewService.StartCalls -eq 0 -and $global:reviewService.StopCalls -eq 0}else{(-not $observed) -and $global:reviewService.StartType -eq 'Automatic' -and $global:reviewService.Status -eq $(if($priorRunning){'Running'}else{'Stopped'})})
    $rows += [pscustomobject]@{Mode=$mode;Exception=$observed;Service=$global:reviewService.Clone();RealServiceOperations=0;Passed=[bool]$passed}
}
$result=[pscustomobject]@{SourceSHA256=$sourceHash;AdminGateOmittedForHarmlessStub=$true;NativeServiceCmdletsMocked=$true;Cases=$rows}
$result | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $review 'admin-service-restore-after.json') -Encoding UTF8
$result | ConvertTo-Json -Depth 6
if($rows.Passed -contains $false){exit 1}
