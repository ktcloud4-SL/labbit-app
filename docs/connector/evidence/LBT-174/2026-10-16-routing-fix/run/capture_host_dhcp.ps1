$ErrorActionPreference='Stop'
$configPath='C:\ProgramData\VMware\vmnetdhcp.conf'
$config=[IO.File]::ReadAllText($configPath)
$service=Get-Service -Name VMnetDHCP
$principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$scopes=@([regex]::Matches($config,'(?m)^subnet\s+([^\s]+)\s+netmask\s+([^\s]+)\s*\{') | ForEach-Object { [pscustomobject]@{Subnet=$_.Groups[1].Value;Netmask=$_.Groups[2].Value} })
$ranges=@([regex]::Matches($config,'(?m)^\s*range\s+([^\s;]+)\s+([^\s;]+);') | ForEach-Object { [pscustomobject]@{Start=$_.Groups[1].Value;End=$_.Groups[2].Value} })
$routers=@([regex]::Matches($config,'(?m)^\s*option routers\s+([^;]+);') | ForEach-Object { $_.Groups[1].Value.Trim() })
$leaseStream=[IO.FileStream]::new('C:\ProgramData\VMware\vmnetdhcp.leases',[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::ReadWrite)
$leaseReader=[IO.StreamReader]::new($leaseStream)
try{$leases=$leaseReader.ReadToEnd()}finally{$leaseReader.Dispose();$leaseStream.Dispose()}
$selected=@([regex]::Matches($leases,'(?s)lease\s+172\.16\.8\.135\s*\{(.*?)\}') | ForEach-Object {
    $body=$_.Groups[1].Value
    $mac=[regex]::Match($body,'hardware ethernet\s+([^;]+);').Groups[1].Value.ToLowerInvariant()
    $sha=[Security.Cryptography.SHA256]::Create()
    try{$macHash=([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($mac)))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose()}
    [pscustomobject]@{IPAddress='172.16.8.135';Starts=[regex]::Match($body,'starts\s+([^;]+);').Groups[1].Value;Ends=[regex]::Match($body,'ends\s+([^;]+);').Groups[1].Value;MACSHA256=$macHash;ExactGuestMACComparisonPerformed=$false}
})
[pscustomobject]@{
    ObservedUTC=[DateTime]::UtcNow.ToString('o')
    Administrator=$principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
    ServiceName=$service.Name;ServiceStatus=$service.Status.ToString();ServiceStartType=$service.StartType.ToString()
    ServiceMutationAttempted=$false
    ConfigSHA256=(Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash
    Scopes=$scopes;Ranges=$ranges;Routers=$routers;HistoricalMatchingIPLeases=$selected
    AdminStateFileExists=(Test-Path -LiteralPath (Join-Path $PSScriptRoot 'vmnet8-dhcp-admin-state.json'))
    Limits='Read-only host observation. IP/time/config correlation is strong competing-DHCP evidence; no stop comparison or exact guest MAC match. Missing Neutron DHCP ports alone does not exclude OVN.'
} | ConvertTo-Json -Depth 7 | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'host-dhcp-observation.json') -Encoding utf8
