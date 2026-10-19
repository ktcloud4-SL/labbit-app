param([ValidateSet('Disable','Restore')][string]$Action='Disable')
$ErrorActionPreference='Stop'
$principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if(-not $principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)){throw '관리자 PowerShell에서 실행해야 합니다.'}
$statePath=Join-Path $PSScriptRoot 'vmnet8-dhcp-admin-state.json'
$configPath='C:\ProgramData\VMware\vmnetdhcp.conf'
if($Action -eq 'Disable'){
    $config=[IO.File]::ReadAllText($configPath)
    $scopes=[regex]::Matches($config,'(?m)^subnet\s+([^\s]+)\s+netmask\s+([^\s]+)\s*\{')
    if($scopes.Count -ne 1 -or $scopes[0].Groups[1].Value -ne '172.16.8.0' -or $scopes[0].Groups[2].Value -ne '255.255.255.0'){
        throw 'VMnet8 단일 DHCP scope 조건이 바뀌었습니다. 서비스 변경 없이 중단합니다.'
    }
    $expected='B7BB6CA59B4C77D4DF7C1BEAE6AC7D2D0FC4E2DAB6976CB706CEA7F354327277'
    if((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $expected){throw '검토한 VMware DHCP 설정과 달라졌습니다. 변경 없이 중단합니다.'}
    if(Test-Path -LiteralPath $statePath){throw '기존 복구 상태가 있습니다. 덮어쓰지 않습니다.'}
    $service=Get-Service -Name VMnetDHCP
    [pscustomobject]@{Name=$service.Name;StartType=$service.StartType.ToString();WasRunning=($service.Status -eq 'Running');ConfigSHA256=$expected;SavedUTC=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8
    try {
        Stop-Service -Name VMnetDHCP
        Set-Service -Name VMnetDHCP -StartupType Disabled
    } catch {
        $prior=Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
        Set-Service -Name VMnetDHCP -StartupType $prior.StartType
        if($prior.WasRunning){Start-Service -Name VMnetDHCP}
        throw
    }
} else {
    $prior=Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if($prior.Name -ne 'VMnetDHCP'){throw '예상한 복구 상태가 아닙니다.'}
    if((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $prior.ConfigSHA256){throw '저장 이후 DHCP 설정이 변경되어 자동 복구를 중단합니다.'}
    Set-Service -Name VMnetDHCP -StartupType $prior.StartType
    if($prior.WasRunning){Start-Service -Name VMnetDHCP}else{Stop-Service -Name VMnetDHCP}
}
Get-Service -Name VMnetDHCP | Select-Object Name,Status,StartType
